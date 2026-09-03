package auth

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
)

// encodeV1 builds a legacy (pre-Argon2id) NSAU file: no Algo/Memory/Threads
// fields, every record implicitly PBKDF2. Used only to test that Decode
// still reads the old format and that Verify transparently upgrades it.
func encodeV1(users map[string]record) []byte {
	n := 4 + 2 + 4
	for name := range users {
		n += 2 + len(name) + saltSize + 4 + hashSize
	}
	buf := make([]byte, n)
	copy(buf[0:4], fileMagic)
	encoding.PutU16(buf, 4, fileVersionV1)
	encoding.PutU32(buf, 6, uint32(len(users)))
	off := 10
	for name, rec := range users {
		encoding.PutU16(buf, off, uint16(len(name)))
		off += 2
		copy(buf[off:], name)
		off += len(name)
		copy(buf[off:], rec.Salt[:])
		off += saltSize
		encoding.PutU32(buf, off, rec.Iter)
		off += 4
		copy(buf[off:], rec.Hash[:])
		off += hashSize
	}
	return buf[:off]
}

func TestUpsertVerifyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.users")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("App", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "wrong"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := s.Verify("missing", "s3cret"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("missing user: %v", err)
	}

	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := re.Verify("APP", "s3cret"); err != nil {
		t.Fatal(err)
	}
}

func TestV1FormatDecodesAndVerifies(t *testing.T) {
	legacy, err := hashPasswordPBKDF2("s3cret", defaultIter)
	if err != nil {
		t.Fatal(err)
	}
	raw := encodeV1(map[string]record{"app": legacy})
	path := filepath.Join(t.TempDir(), "users")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "s3cret"); err != nil {
		t.Fatalf("v1 record should verify: %v", err)
	}
	if err := s.Verify("app", "wrong"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("wrong password on v1 record: %v", err)
	}
}

func TestNewRecordsAreArgon2idFromCreation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	rec := s.users[userKey{Name: "app"}]
	s.mu.Unlock()
	if rec.Algo != algoArgon2id {
		t.Fatalf("new record algo = %d, want algoArgon2id", rec.Algo)
	}
	if rec.Memory == 0 || rec.Threads == 0 {
		t.Fatalf("new record missing argon2id params: %+v", rec)
	}
}

func TestStoreSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.Snapshot(); len(got) != 0 {
		t.Fatalf("fresh store snapshot not empty: %v", got)
	}
	if err := s.Upsert("bob", "s3cretbob"); err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("alice", "s3cretalice"); err != nil {
		t.Fatal(err)
	}
	// legacy PBKDF2 record inserted directly, bypassing Upsert (which always
	// writes Argon2id), to exercise the mixed-algo snapshot path.
	legacy, err := hashPasswordPBKDF2("s3cretcarol", defaultIter)
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.users[userKey{Name: "carol"}] = legacy
	s.mu.Unlock()

	got := s.Snapshot()
	if len(got) != 3 {
		t.Fatalf("snapshot len = %d, want 3: %v", len(got), got)
	}
	// sorted by name
	if got[0].Name != "alice" || got[1].Name != "bob" || got[2].Name != "carol" {
		t.Fatalf("snapshot not sorted: %v", got)
	}
	if got[0].Algo != "argon2id" || got[1].Algo != "argon2id" {
		t.Fatalf("new records must report argon2id: %v", got)
	}
	if got[2].Algo != "pbkdf2" {
		t.Fatalf("legacy record must report pbkdf2: %v", got)
	}
	// no hash/salt material leaked via reflection-visible fields
	for _, u := range got {
		if u.Name == "" || u.Algo == "" {
			t.Fatalf("incomplete UserInfo: %+v", u)
		}
	}
}

func TestTransparentRehashUpgradesToArgon2id(t *testing.T) {
	legacy, err := hashPasswordPBKDF2("s3cret", defaultIter)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "users")
	if err := os.WriteFile(path, encodeV1(map[string]record{"app": legacy}), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}

	// A wrong password must never trigger a rehash.
	if err := s.Verify("app", "wrong"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	s.mu.Lock()
	stillLegacy := s.users[userKey{Name: "app"}].Algo
	s.mu.Unlock()
	if stillLegacy != algoPBKDF2 {
		t.Fatal("a failed verify must not rehash the record")
	}

	// A correct password rehashes and persists the upgrade.
	if err := s.Verify("app", "s3cret"); err != nil {
		t.Fatalf("correct password: %v", err)
	}
	s.mu.Lock()
	upgraded := s.users[userKey{Name: "app"}]
	s.mu.Unlock()
	if upgraded.Algo != algoArgon2id {
		t.Fatalf("record should be rehashed to argon2id, got algo=%d", upgraded.Algo)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.mu.Lock()
	persisted := reopened.users[userKey{Name: "app"}]
	reopened.mu.Unlock()
	if persisted.Algo != algoArgon2id {
		t.Fatal("rehash upgrade did not persist to disk")
	}
	if err := reopened.Verify("app", "s3cret"); err != nil {
		t.Fatalf("verify after reopen: %v", err)
	}
}

func TestDeleteUser(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("app"); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "s3cret"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("deleted user still verifies: %v", err)
	}
}

func TestRejectEmptyPassword(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("app", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("xxxx"),
		[]byte("NSAU"),
		append([]byte("NSAU"), 0, 99, 0, 0, 0, 1),
		append([]byte("BAD!"), 1, 0, 0, 0, 0, 0),
	}
	for _, c := range cases {
		if _, err := Decode(c); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}

func TestDecodeRejectsUnboundedArgon2ParametersAndTrailingBytes(t *testing.T) {
	rec, err := hashPasswordArgon2id("s3cret")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := Encode(map[userKey]record{{Name: "app"}: rec})
	if err != nil {
		t.Fatal(err)
	}

	// One "app" record places Memory at byte 52 and Threads at byte 56 (the
	// 16-byte RealmID prefix shifts every v2-layout offset by 16).
	tooMuchMemory := append([]byte(nil), raw...)
	encoding.PutU32(tooMuchMemory, 52, maxArgon2MemoryKiB+1)
	if _, err := Decode(tooMuchMemory); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("unbounded memory accepted: %v", err)
	}
	badMinimum := append([]byte(nil), raw...)
	badMinimum[56] = maxArgon2Threads
	encoding.PutU32(badMinimum, 52, uint32(maxArgon2Threads)*8-1)
	if _, err := Decode(badMinimum); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("invalid memory/parallelism combination accepted: %v", err)
	}
	if _, err := Decode(append(raw, 0)); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("trailing byte accepted: %v", err)
	}
}

func TestEncodeIsDeterministic(t *testing.T) {
	a, err := hashPasswordArgon2id("alpha")
	if err != nil {
		t.Fatal(err)
	}
	b, err := hashPasswordArgon2id("beta")
	if err != nil {
		t.Fatal(err)
	}
	users := map[userKey]record{{Name: "zeta"}: a, {Name: "alpha"}: b}
	first, err := Encode(users)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		next, err := Encode(users)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(first, next) {
			t.Fatal("NSAU encoding changed across identical inputs")
		}
	}
}

func FuzzDecode(f *testing.F) {
	s, err := Create(filepath.Join(f.TempDir(), "users"))
	if err != nil {
		f.Fatal(err)
	}
	if err := s.Upsert("app", "s3cret"); err != nil {
		f.Fatal(err)
	}
	good, err := os.ReadFile(s.Path())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	legacy, err := hashPasswordPBKDF2("s3cret", defaultIter)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(encodeV1(map[string]record{"app": legacy}))
	f.Add([]byte("NSAU"))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		users, err := Decode(raw)
		if err != nil {
			if users != nil {
				t.Fatalf("error with non-nil map")
			}
			return
		}
		if users == nil {
			t.Fatalf("nil map without error")
		}
	})
}

func TestVerifyInRealmIsolatesSameUsername(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	realmA := hosting.ID{1}
	realmB := hosting.ID{2}
	if err := s.UpsertInRealm(realmA, "dba", "passwordA"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertInRealm(realmB, "dba", "passwordB"); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyInRealm(realmA, "dba", "passwordA"); err != nil {
		t.Fatalf("realm A own password: %v", err)
	}
	if err := s.VerifyInRealm(realmB, "dba", "passwordB"); err != nil {
		t.Fatalf("realm B own password: %v", err)
	}
	if err := s.VerifyInRealm(realmA, "dba", "passwordB"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("realm A must reject realm B's password: %v", err)
	}
	if err := s.VerifyInRealm(realmB, "dba", "passwordA"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("realm B must reject realm A's password: %v", err)
	}
}

func TestVerifyInRealmFallsBackToDeploymentWide(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	// Deployment-wide bootstrap admin, e.g. from `nextsql init --user dba`.
	if err := s.Upsert("dba", "rootpw"); err != nil {
		t.Fatal(err)
	}
	realm := hosting.ID{9}
	if err := s.VerifyInRealm(realm, "dba", "rootpw"); err != nil {
		t.Fatalf("deployment-wide admin must authenticate into any realm: %v", err)
	}
	if !s.HasInRealm(realm, "dba") {
		t.Fatal("HasInRealm must also see the deployment-wide fallback")
	}
}

func TestVerifyInRealmScopedShadowsDeploymentWide(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	realm := hosting.ID{9}
	if err := s.Upsert("dba", "rootpw"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertInRealm(realm, "dba", "realmpw"); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyInRealm(realm, "dba", "realmpw"); err != nil {
		t.Fatalf("realm-scoped password must work: %v", err)
	}
	if err := s.VerifyInRealm(realm, "dba", "rootpw"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("a realm-scoped entry must shadow the deployment-wide one of the same name: %v", err)
	}
	// The deployment-wide entry itself is untouched.
	if err := s.Verify("dba", "rootpw"); err != nil {
		t.Fatalf("deployment-wide entry must still work: %v", err)
	}
}

func TestLegacyFileDecodesEveryRecordDeploymentWide(t *testing.T) {
	legacy, err := hashPasswordPBKDF2("s3cret", defaultIter)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "users")
	if err := os.WriteFile(path, encodeV1(map[string]record{"app": legacy}), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "s3cret"); err != nil {
		t.Fatalf("flat Verify must see the legacy record: %v", err)
	}
	realm := hosting.ID{7}
	if err := s.VerifyInRealm(realm, "app", "s3cret"); err != nil {
		t.Fatalf("VerifyInRealm must fall back to the legacy deployment-wide record: %v", err)
	}
}
