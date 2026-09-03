package nextsql

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func testFieldKey(id string, fill byte) FieldKey {
	k := FieldKey{ID: id}
	for i := range k.Material {
		k.Material[i] = fill
	}
	return k
}

func TestFieldEncryptionHelpersRotateAndRevoke(t *testing.T) {
	v1 := testFieldKey("v1", 1)
	ring, err := NewMemoryFieldKeyring(v1)
	if err != nil {
		t.Fatal(err)
	}
	c := &Conn{cfg: Config{Database: "app", FieldKeys: ring}}
	ctx := context.Background()
	oldCipher, err := c.EncryptField(ctx, "accounts", "secret", types.TextValue("old"))
	if err != nil {
		t.Fatal(err)
	}
	oldPlain, err := c.DecryptField(ctx, "accounts", "secret", types.Text(), oldCipher)
	if err != nil || oldPlain.Str != "old" {
		t.Fatalf("old decrypt: %+v %v", oldPlain, err)
	}
	v2 := testFieldKey("v2", 2)
	if err := ring.Rotate(v2); err != nil {
		t.Fatal(err)
	}
	newCipher, err := c.EncryptField(ctx, "accounts", "secret", types.TextValue("new"))
	if err != nil {
		t.Fatal(err)
	}
	if oldCipher.Str == newCipher.Str {
		t.Fatal("rotation/randomization did not change ciphertext")
	}
	if _, err := c.DecryptField(ctx, "accounts", "secret", types.Text(), oldCipher); err != nil {
		t.Fatalf("overlap decrypt: %v", err)
	}
	if err := ring.Revoke("v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.DecryptField(ctx, "accounts", "secret", types.Text(), oldCipher); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("revoked key: %v", err)
	}
	if _, err := c.DecryptField(ctx, "accounts", "other", types.Text(), newCipher); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong column context: %v", err)
	}
	nullCipher, err := c.EncryptField(ctx, "accounts", "secret", types.Null(types.Text()))
	if err != nil || !nullCipher.Null || nullCipher.Typ.Kind != types.KindString {
		t.Fatalf("NULL encryption: %+v %v", nullCipher, err)
	}
	nullPlain, err := c.DecryptField(ctx, "accounts", "secret", types.Text(), nullCipher)
	if err != nil || !nullPlain.Null || nullPlain.Typ.Kind != types.KindText {
		t.Fatalf("NULL decrypt: %+v %v", nullPlain, err)
	}
}

func TestFileFieldKeyringPersistsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	v1 := testFieldKey("v1", 1)
	kr, err := CreateFileFieldKeyring(path, v1)
	if err != nil {
		t.Fatal(err)
	}
	c := &Conn{cfg: Config{Database: "app", FieldKeys: kr}}
	ctx := context.Background()
	cipher, err := c.EncryptField(ctx, "accounts", "secret", types.TextValue("old"))
	if err != nil {
		t.Fatal(err)
	}

	reopened, err := OpenFileFieldKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	c2 := &Conn{cfg: Config{Database: "app", FieldKeys: reopened}}
	plain, err := c2.DecryptField(ctx, "accounts", "secret", types.Text(), cipher)
	if err != nil || plain.Str != "old" {
		t.Fatalf("decrypt after reopen: %+v %v", plain, err)
	}
}

func TestFileFieldKeyringCreateFailsIfExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	if _, err := CreateFileFieldKeyring(path, testFieldKey("v1", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateFileFieldKeyring(path, testFieldKey("v2", 2)); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("got %v", err)
	}
}

func TestFileFieldKeyringRotateRevokePersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	v1 := testFieldKey("v1", 1)
	kr, err := CreateFileFieldKeyring(path, v1)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	c := &Conn{cfg: Config{Database: "app", FieldKeys: kr}}
	oldCipher, err := c.EncryptField(ctx, "accounts", "secret", types.TextValue("old"))
	if err != nil {
		t.Fatal(err)
	}

	v2 := testFieldKey("v2", 2)
	if err := kr.Rotate(v2); err != nil {
		t.Fatal(err)
	}

	// A fresh open must see the rotation: v1 still resolves for overlap
	// reads, v2 is now current for new writes.
	afterRotate, err := OpenFileFieldKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	cAfterRotate := &Conn{cfg: Config{Database: "app", FieldKeys: afterRotate}}
	if _, err := cAfterRotate.DecryptField(ctx, "accounts", "secret", types.Text(), oldCipher); err != nil {
		t.Fatalf("overlap decrypt after reopen: %v", err)
	}
	newCipher, err := cAfterRotate.EncryptField(ctx, "accounts", "secret", types.TextValue("new"))
	if err != nil {
		t.Fatal(err)
	}
	if !containsFieldKeyID(afterRotate.List(), "v2", true, false) {
		t.Fatalf("expected v2 current after rotate: %+v", afterRotate.List())
	}

	if err := kr.Revoke("v1"); err != nil {
		t.Fatal(err)
	}

	afterRevoke, err := OpenFileFieldKeyring(path)
	if err != nil {
		t.Fatal(err)
	}
	cAfterRevoke := &Conn{cfg: Config{Database: "app", FieldKeys: afterRevoke}}
	if _, err := cAfterRevoke.DecryptField(ctx, "accounts", "secret", types.Text(), oldCipher); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("revoked key after reopen: %v", err)
	}
	if plain, err := cAfterRevoke.DecryptField(ctx, "accounts", "secret", types.Text(), newCipher); err != nil || plain.Str != "new" {
		t.Fatalf("current key after reopen: %+v %v", plain, err)
	}
	if !containsFieldKeyID(afterRevoke.List(), "v1", false, true) {
		t.Fatalf("expected v1 revoked after reopen: %+v", afterRevoke.List())
	}
}

func containsFieldKeyID(list []FieldKeyInfo, id string, current, revoked bool) bool {
	for _, info := range list {
		if info.ID == id {
			return info.Current == current && info.Revoked == revoked
		}
	}
	return false
}

func TestFileFieldKeyringRevokeZeroesMaterialOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	kr, err := CreateFileFieldKeyring(path, testFieldKey("v1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Rotate(testFieldKey("v2", 2)); err != nil {
		t.Fatal(err)
	}
	if err := kr.Revoke("v1"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := decodeFieldKeyring(raw)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, k := range keys {
		if k.ID != "v1" {
			continue
		}
		found = true
		if !k.Revoked {
			t.Fatal("v1 not marked revoked on disk")
		}
		if k.Material != ([32]byte{}) {
			t.Fatal("revoked key material was not zeroed on disk")
		}
	}
	if !found {
		t.Fatal("revoked record v1 missing from disk")
	}
}

func TestFileFieldKeyringCannotRevokeCurrent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	kr, err := CreateFileFieldKeyring(path, testFieldKey("v1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Revoke("v1"); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("got %v", err)
	}
}

func TestFileFieldKeyringCannotReuseRevokedID(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	kr, err := CreateFileFieldKeyring(path, testFieldKey("v1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := kr.Rotate(testFieldKey("v2", 2)); err != nil {
		t.Fatal(err)
	}
	if err := kr.Revoke("v1"); err != nil {
		t.Fatal(err)
	}
	if err := kr.Rotate(testFieldKey("v1", 9)); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("got %v", err)
	}
}

func TestFileFieldKeyringReloadLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	kr, err := CreateFileFieldKeyring(path, testFieldKey("v1", 1))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a keyring"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := kr.Reload(); err == nil {
		t.Fatal("expected reload of corrupt file to fail")
	}
	if _, err := kr.CurrentFieldKey(context.Background(), "db", "t", "c"); err != nil {
		t.Fatalf("in-memory state should survive a failed reload: %v", err)
	}
}

func TestFileFieldKeyringCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyring.nsfk")
	kr, err := CreateFileFieldKeyring(path, testFieldKey("k0", 1))
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i < maxFieldKeyringKeys; i++ {
		id := "k" + string(rune('a'+i%26)) + string(rune('A'+i/26))
		if err := kr.Rotate(testFieldKey(id, byte(i))); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
	if err := kr.Rotate(testFieldKey("overflow", 9)); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("got %v", err)
	}
}

func TestDecodeFieldKeyringRejectsCorruption(t *testing.T) {
	valid, err := encodeFieldKeyring([]fieldKeyRecord{{ID: "v1", Current: true, Material: [32]byte{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFieldKeyring(valid); err != nil {
		t.Fatalf("valid keyring should decode: %v", err)
	}
	cases := map[string][]byte{
		"empty":         nil,
		"bad magic":     append([]byte("XXXX"), valid[4:]...),
		"truncated":     valid[:len(valid)-4],
		"trailing junk": append(append([]byte{}, valid...), 0xff),
	}
	for name, raw := range cases {
		if _, err := decodeFieldKeyring(raw); err == nil {
			t.Fatalf("%s: expected decode error", name)
		}
	}
	noCurrent, err := encodeFieldKeyring([]fieldKeyRecord{{ID: "v1", Material: [32]byte{1}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFieldKeyring(noCurrent); err == nil {
		t.Fatal("expected error: no current key")
	}
	twoCurrent, err := encodeFieldKeyring([]fieldKeyRecord{
		{ID: "v1", Current: true, Material: [32]byte{1}},
		{ID: "v2", Current: true, Material: [32]byte{2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFieldKeyring(twoCurrent); err == nil {
		t.Fatal("expected error: two current keys")
	}
	revokedWithMaterial, err := encodeFieldKeyring([]fieldKeyRecord{
		{ID: "v1", Current: true, Material: [32]byte{1}},
		{ID: "v2", Revoked: true, Material: [32]byte{2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFieldKeyring(revokedWithMaterial); err == nil {
		t.Fatal("expected error: revoked key retains material")
	}
	dup, err := encodeFieldKeyring([]fieldKeyRecord{
		{ID: "v1", Current: true, Material: [32]byte{1}},
		{ID: "v1", Material: [32]byte{2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeFieldKeyring(dup); err == nil {
		t.Fatal("expected error: duplicate id")
	}
}

func TestOpenRejectsURLWithKey(t *testing.T) {
	_, err := Open(Config{
		Address:  "nextsql://app:secret@db.example.com:7210/prod?key=deadbeef",
		User:     "app",
		Password: "x",
		TLS:      &tls.Config{MinVersion: tls.VersionTLS13},
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestClusterRoutingClassifiers(t *testing.T) {
	reads := []string{
		"SELECT 1",
		"  select n from t",
		"\n-- comment\nSELECT n FROM t",
		"(SELECT n FROM t) UNION (SELECT n FROM u)",
		"SHOW TASKS",
		"WITH c AS (SELECT n FROM t) SELECT n FROM c",
	}
	for _, s := range reads {
		if !isReadOnlySQL(s) {
			t.Fatalf("expected read-only: %q", s)
		}
	}
	writes := []string{
		"INSERT INTO t (n) VALUES ('x')",
		"UPDATE t SET n = 'x'",
		"DELETE FROM t",
		"UPSERT INTO t (id) VALUES ('x')",
		"CREATE TABLE t (id STRING PRIMARY KEY)",
		"EXPLAIN ANALYZE SELECT n FROM t",
		"WITH c AS (SELECT 1) INSERT INTO t SELECT * FROM c",
		"BEGIN",
	}
	for _, s := range writes {
		if isReadOnlySQL(s) {
			t.Fatalf("expected not read-only: %q", s)
		}
	}
	for _, tc := range []struct {
		sql        string
		begin, end bool
	}{
		{"BEGIN", true, false},
		{"  begin transaction ", true, false},
		{"START TRANSACTION", true, false},
		{"COMMIT", false, true},
		{"rollback", false, true},
		{"SELECT 1", false, false},
	} {
		b, e := txnControl(tc.sql)
		if b != tc.begin || e != tc.end {
			t.Fatalf("txnControl(%q) = %v,%v want %v,%v", tc.sql, b, e, tc.begin, tc.end)
		}
	}
}

func TestOpenClusterNeedsAnAddress(t *testing.T) {
	if _, err := OpenCluster(nil, Config{User: "app"}); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRequiresTLSOffLoopback(t *testing.T) {
	_, err := Open(Config{
		Address:       "db.example.com:7210",
		User:          "app",
		Password:      "x",
		InsecureNoTLS: true,
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}
