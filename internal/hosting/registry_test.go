package hosting

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestRegistryBootstrapRestartAndLifecycle(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	root := testRoot(t)
	bootstrap := testBootstrap(t, StateProvisioning)

	reg, created, err := EnsureBootstrap(path, root, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("new registry was not reported created")
	}
	first := reg.Manifest()
	if first.Generation == 0 || first.DeploymentID.zero() {
		t.Fatalf("invalid manifest: %+v", first)
	}
	if len(first.Realms) != 1 || len(first.Realms[0].Databases) != 1 {
		t.Fatalf("unexpected bootstrap manifest: %+v", first)
	}
	if first.Realms[0].Name != "customer-a" || first.Realms[0].Databases[0].Name != "production" {
		t.Fatalf("unexpected normalized names: %+v", first.Realms[0])
	}
	if first.Realms[0].Databases[0].State != StateProvisioning {
		t.Fatal("bootstrap database was published active prematurely")
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("customer-a")) || bytes.Contains(raw, []byte("production")) {
		t.Fatal("registry names were persisted in plaintext")
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("registry mode: %v %v", st, err)
	}

	realmID := first.DefaultRealm
	databaseID := first.DefaultDatabase
	if err := reg.SetDatabaseState(realmID, databaseID, StateActive); err != nil {
		t.Fatal(err)
	}
	second := reg.Manifest()
	if second.Generation <= first.Generation || second.Realms[0].Databases[0].State != StateActive {
		t.Fatalf("lifecycle update not durable: first=%d second=%+v", first.Generation, second)
	}
	if err := reg.SetDatabaseState(realmID, databaseID, StateProvisioning); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("invalid reverse transition: %v", err)
	}
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := Open(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.Manifest()
	if got.Generation != second.Generation || got.Realms[0].Databases[0].State != StateActive {
		t.Fatalf("restart lost registry update: %+v", got)
	}
}

func TestEnsureBootstrapIsIdempotentAndRejectsMismatch(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	root := testRoot(t)
	bootstrap := testBootstrap(t, StateProvisioning)
	reg, _, err := EnsureBootstrap(path, root, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	want := reg.Manifest()
	_ = reg.Close()

	reopened, created, err := EnsureBootstrap(path, root, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	if created {
		t.Fatal("existing registry was reported newly created")
	}
	if got := reopened.Manifest(); got.DeploymentID != want.DeploymentID || got.DefaultRealm != want.DefaultRealm {
		t.Fatalf("bootstrap identity changed: got=%+v want=%+v", got, want)
	}
	_ = reopened.Close()

	mismatch := bootstrap
	mismatch.DatabaseName = "other"
	if _, _, err := EnsureBootstrap(path, root, mismatch); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("mismatched bootstrap: %v", err)
	}
}

func TestEnsureBootstrapRecoversEnvelopeOnlyCrash(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	root := testRoot(t)
	bootstrap := testBootstrap(t, StateProvisioning)
	reg, _, err := EnsureBootstrap(path, root, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	want := reg.Manifest()
	_ = reg.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	recovered, created, err := EnsureBootstrap(path, root, bootstrap)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	got := recovered.Manifest()
	if !created || got.DeploymentID != want.DeploymentID || got.DefaultRealm != want.DefaultRealm {
		t.Fatalf("envelope-only recovery changed identity: created=%v got=%+v want=%+v", created, got, want)
	}
	if got.Generation <= want.Generation {
		t.Fatalf("nonce generation was not advanced: got=%d want>%d", got.Generation, want.Generation)
	}
}

func TestRegistryWrongRootTamperAndTruncateFailClosed(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	_ = reg.Close()

	if _, err := Open(path, testRoot(t)); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong root: %v", err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), original...)
	tampered[len(tampered)-1] ^= 0x80
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, root); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("tamper: %v", err)
	}
	if err := os.WriteFile(path, original[:len(original)/2], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, root); err == nil || (!nerr.HasCode(err, nerr.InvalidFormat) && !nerr.HasCode(err, nerr.Crypto)) {
		t.Fatalf("truncate: %v", err)
	}
}

func TestManifestDeterministicRoundTripAndValidation(t *testing.T) {
	id1 := testIdentity(t)
	id2 := testIdentity(t)
	deployment, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	realm1 := deriveRealmID(deployment, "a")
	realm2 := deriveRealmID(deployment, "b")
	m := Manifest{
		DeploymentID:    deployment,
		Generation:      7,
		DefaultRealm:    realm1,
		DefaultDatabase: ID(id1.Database),
		Realms: []Realm{
			{ID: realm2, Name: "b", State: StateActive, Databases: []Database{{ID: ID(id2.Database), Name: "two", State: StateSuspended, Layout: LayoutManaged, Identity: id2, KeyRef: "/run/keys/two.key"}}},
			{ID: realm1, Name: "a", State: StateActive, Databases: []Database{{ID: ID(id1.Database), Name: "one", State: StateActive, Layout: LayoutLegacyDefault, Identity: id1}}},
		},
	}
	a, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	b, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatal("manifest encoding is not deterministic")
	}
	got, err := DecodeManifest(a)
	if err != nil {
		t.Fatal(err)
	}
	reencoded, err := EncodeManifest(got)
	if err != nil || !bytes.Equal(a, reencoded) {
		t.Fatalf("round trip mismatch: %v", err)
	}

	bad := cloneManifest(m)
	bad.Realms[1].Databases[0].ID = bad.Realms[0].Databases[0].ID
	bad.Realms[1].Databases[0].Identity.Database = [16]byte(bad.Realms[0].Databases[0].ID)
	if _, err := EncodeManifest(bad); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("duplicate database identity: %v", err)
	}
	for i := 0; i < len(a); i++ {
		if _, err := DecodeManifest(a[:i]); err == nil {
			t.Fatalf("accepted truncation at %d", i)
		}
	}
}

func TestManifestForwardCompatibleCapDecode(t *testing.T) {
	id := testIdentity(t)
	deployment, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	realmID := deriveRealmID(deployment, "a")
	m := Manifest{
		DeploymentID:    deployment,
		Generation:      3,
		DefaultRealm:    realmID,
		DefaultDatabase: ID(id.Database),
		Realms: []Realm{{
			ID: realmID, Name: "a", State: StateActive, StorageCapBytes: 9000,
			RealmRootAuthHash: [32]byte{1, 2, 3, 30, 31},
			Databases:         []Database{{ID: ID(id.Database), Name: "one", State: StateActive, Layout: LayoutLegacyDefault, Identity: id, StorageCapBytes: 4500}},
		}},
	}
	raw, err := EncodeManifest(m)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Realms[0].StorageCapBytes != 9000 || got.Realms[0].Databases[0].StorageCapBytes != 4500 {
		t.Fatalf("cap round trip lost: %+v", got.Realms[0])
	}
	reencoded, err := EncodeManifest(got)
	if err != nil || !bytes.Equal(raw, reencoded) {
		t.Fatalf("cap manifest not deterministic: %v", err)
	}
}

func TestCanTransition(t *testing.T) {
	allowed := [][2]State{
		{StateProvisioning, StateActive},
		{StateActive, StateSuspended},
		{StateSuspended, StateActive},
		{StateActive, StateDeleting},
		{StateDeleting, StateTombstoned},
		{StateFailed, StateProvisioning},
	}
	for _, pair := range allowed {
		if !CanTransition(pair[0], pair[1]) {
			t.Fatalf("transition %d -> %d rejected", pair[0], pair[1])
		}
	}
	if CanTransition(StateTombstoned, StateActive) || CanTransition(StateActive, StateProvisioning) {
		t.Fatal("invalid lifecycle transition accepted")
	}
}

func testRoot(t *testing.T) *crypto.DEK {
	t.Helper()
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

func testIdentity(t *testing.T) format.Identity {
	t.Helper()
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func testBootstrap(t *testing.T, state State) Bootstrap {
	t.Helper()
	return Bootstrap{
		RealmName:        "Customer-A",
		DatabaseName:     "Production",
		DatabaseIdentity: testIdentity(t),
		DatabaseState:    state,
	}
}

func TestPathIsDataDirRelative(t *testing.T) {
	dir := t.TempDir()
	if got, want := Path(dir), filepath.Join(dir, RegistryFileName); got != want {
		t.Fatalf("Path()=%q want %q", got, want)
	}
}
