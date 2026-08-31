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

func TestStorageCapsDurableAndBounded(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	m := reg.Manifest()
	realmID := m.DefaultRealm
	dbID := m.DefaultDatabase

	// No caps by default.
	if m.Realms[0].StorageCapBytes != 0 || m.Realms[0].Databases[0].StorageCapBytes != 0 {
		t.Fatalf("unexpected default caps: %+v", m.Realms[0])
	}

	// A database cap with no realm cap is allowed.
	if err := reg.SetDatabaseStorageCap(realmID, dbID, 4096); err != nil {
		t.Fatal(err)
	}
	// A realm cap below an existing database cap is rejected.
	if err := reg.SetRealmStorageCap(realmID, 2048); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("realm cap below db cap: %v", err)
	}
	if err := reg.SetRealmStorageCap(realmID, 1<<20); err != nil {
		t.Fatal(err)
	}
	// A database cap above the realm cap is rejected.
	if err := reg.SetDatabaseStorageCap(realmID, dbID, 1<<21); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("db cap over realm cap: %v", err)
	}
	// Unknown realm / database fail closed.
	if err := reg.SetDatabaseStorageCap(ID{1}, dbID, 10); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("unknown realm: %v", err)
	}
	if err := reg.SetDatabaseStorageCap(realmID, ID{2}, 10); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("unknown database: %v", err)
	}

	// A no-op set does not advance the generation.
	before := reg.Manifest().Generation
	if err := reg.SetDatabaseStorageCap(realmID, dbID, 4096); err != nil {
		t.Fatal(err)
	}
	if reg.Manifest().Generation != before {
		t.Fatal("no-op cap set advanced the registry generation")
	}
	_ = reg.Close()

	// Caps survive restart.
	reopened, err := Open(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.Manifest()
	if got.Realms[0].StorageCapBytes != 1<<20 || got.Realms[0].Databases[0].StorageCapBytes != 4096 {
		t.Fatalf("caps lost across restart: %+v", got.Realms[0])
	}

	// Clearing caps round-trips through 0.
	if err := reopened.SetDatabaseStorageCap(realmID, dbID, 0); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SetRealmStorageCap(realmID, 0); err != nil {
		t.Fatal(err)
	}
	if final := reopened.Manifest(); final.Realms[0].StorageCapBytes != 0 || final.Realms[0].Databases[0].StorageCapBytes != 0 {
		t.Fatalf("caps not cleared: %+v", final.Realms[0])
	}
}

func TestRealmRootCapDelegation(t *testing.T) {
	dir := t.TempDir()
	path := Path(dir)
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	m := reg.Manifest()
	realmID := m.DefaultRealm
	dbID := m.DefaultDatabase
	if err := reg.SetRealmStorageCap(realmID, 1<<20); err != nil {
		t.Fatal(err)
	}
	secret := []byte("realm-root-delegation-secret-01")

	// No delegation configured yet.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 4096, secret); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("undelegated realm-root set: %v", err)
	}

	// A too-short secret is rejected.
	if err := reg.SetRealmRootAuth(realmID, []byte("short")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("short secret: %v", err)
	}
	if err := reg.SetRealmRootAuth(realmID, secret); err != nil {
		t.Fatal(err)
	}

	// Wrong secret fails closed.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 4096, []byte("wrong-secret-wrong-secret-wrong")); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("wrong secret: %v", err)
	}
	// Missing secret fails closed.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 4096, nil); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("nil secret: %v", err)
	}

	// Correct secret, within the realm ceiling.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 4096, secret); err != nil {
		t.Fatal(err)
	}
	got := reg.Manifest()
	if got.Realms[0].Databases[0].StorageCapBytes != 4096 {
		t.Fatalf("realm-root cap not applied: %+v", got.Realms[0])
	}
	// The realm cap itself is untouched — there is no realm-root path to it.
	if got.Realms[0].StorageCapBytes != 1<<20 {
		t.Fatalf("realm cap changed under realm-root path: %d", got.Realms[0].StorageCapBytes)
	}

	// The realm ceiling still binds the realm root.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 1<<21, secret); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("realm-root cap over ceiling: %v", err)
	}
	_ = reg.Close()

	// Delegation survives restart.
	reopened, err := Open(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if err := reopened.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 8192, secret); err != nil {
		t.Fatalf("realm-root set after restart: %v", err)
	}

	// Clearing the delegation revokes realm-root access.
	if err := reopened.SetRealmRootAuth(realmID, nil); err != nil {
		t.Fatal(err)
	}
	if err := reopened.SetDatabaseStorageCapAsRealmRoot(realmID, dbID, 100, secret); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("realm-root set after clear: %v", err)
	}
}

// TestResellerTierControlBoundaries exercises the Daemon / Realm / Nano control
// surfaces described in docs/design-multidatabase-dbaas.md §10.1 against a
// two-realm deployment.
func TestResellerTierControlBoundaries(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	idA1, idA2, idB1 := testIdentity(t), testIdentity(t), testIdentity(t)
	reg, created, err := EnsureManifest(path, root, func(dep ID) (Manifest, error) {
		ra := deriveRealmID(dep, "realm-a")
		rb := deriveRealmID(dep, "realm-b")
		return Manifest{
			DeploymentID:    dep,
			Generation:      1,
			DefaultRealm:    ra,
			DefaultDatabase: ID(idA1.Database),
			Realms: []Realm{
				{ID: ra, Name: "realm-a", State: StateActive, Databases: []Database{
					{ID: ID(idA1.Database), Name: "prod", State: StateActive, Layout: LayoutManaged, Identity: idA1, KeyRef: "/run/keys/a1.key"},
					{ID: ID(idA2.Database), Name: "stage", State: StateActive, Layout: LayoutManaged, Identity: idA2, KeyRef: "/run/keys/a2.key"},
				}},
				{ID: rb, Name: "realm-b", State: StateActive, Databases: []Database{
					{ID: ID(idB1.Database), Name: "prod", State: StateActive, Layout: LayoutManaged, Identity: idB1, KeyRef: "/run/keys/b1.key"},
				}},
			},
		}, nil
	})
	if err != nil || !created {
		t.Fatalf("build two-realm registry: created=%v err=%v", created, err)
	}
	defer reg.Close()

	realm := func(name string) Realm {
		for _, r := range reg.Manifest().Realms {
			if r.Name == name {
				return r
			}
		}
		t.Fatalf("realm %q not found", name)
		return Realm{}
	}
	dbIn := func(r Realm, name string) ID {
		for _, d := range r.Databases {
			if d.Name == name {
				return d.ID
			}
		}
		t.Fatalf("database %q not in realm %s", name, r.Name)
		return ID{}
	}
	realmA, realmB := realm("realm-a").ID, realm("realm-b").ID
	dbA1, dbA2 := dbIn(realm("realm-a"), "prod"), dbIn(realm("realm-a"), "stage")
	dbB1 := dbIn(realm("realm-b"), "prod")

	// --- Daemon: registry root does everything ---
	for _, r := range []ID{realmA, realmB} {
		if err := reg.SetRealmStorageCap(r, 1<<20); err != nil {
			t.Fatalf("daemon set realm cap: %v", err)
		}
	}
	if err := reg.SetDatabaseStorageCap(realmA, dbA1, 4096); err != nil {
		t.Fatalf("daemon set db cap A: %v", err)
	}
	if err := reg.SetDatabaseStorageCap(realmB, dbB1, 4096); err != nil {
		t.Fatalf("daemon set db cap B: %v", err)
	}
	if err := reg.SetDatabaseState(realmA, dbA1, StateSuspended); err != nil {
		t.Fatalf("daemon lifecycle: %v", err)
	}
	if err := reg.SetDatabaseState(realmA, dbA1, StateActive); err != nil {
		t.Fatalf("daemon lifecycle restore: %v", err)
	}

	secretA := []byte("realm-a-delegation-secret-000001")
	secretB := []byte("realm-b-delegation-secret-000002")
	if err := reg.SetRealmRootAuth(realmA, secretA); err != nil {
		t.Fatalf("daemon delegate realm A: %v", err)
	}

	// --- Realm: realm-root A is scoped to realm A's per-database caps ---
	// realm B not delegated yet.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmB, dbB1, 10, secretA); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("realm-root A into undelegated realm B: %v", err)
	}
	if err := reg.SetRealmRootAuth(realmB, secretB); err != nil {
		t.Fatal(err)
	}
	// A's secret cannot act on realm B, and B's cannot act on realm A.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmB, dbB1, 10, secretA); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("realm-root A secret against realm B: %v", err)
	}
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmA, dbA1, 10, secretB); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("realm-root B secret against realm A: %v", err)
	}
	// A database from another realm is not addressable even with the right secret.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmA, dbB1, 10, secretA); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("realm-root A addressing realm B's database: %v", err)
	}
	// The realm cap still bounds the realm root.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmA, dbA2, 1<<21, secretA); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("realm-root A over realm cap: %v", err)
	}
	// Within scope and ceiling: allowed.
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmA, dbA2, 8192, secretA); err != nil {
		t.Fatalf("realm-root A legitimate set: %v", err)
	}

	// The realm root has no path to the realm cap or to any other realm's state.
	after := reg.Manifest()
	for _, r := range after.Realms {
		if r.StorageCapBytes != 1<<20 {
			t.Fatalf("realm %s cap changed under realm-root activity: %d", r.Name, r.StorageCapBytes)
		}
	}

	// --- Nano: a single database; a realm-root secret touches only the cap ---
	var beforeA2 Database
	for _, d := range realm("realm-a").Databases {
		if d.ID == dbA2 {
			beforeA2 = d
		}
	}
	if err := reg.SetDatabaseStorageCapAsRealmRoot(realmA, dbA2, 9000, secretA); err != nil {
		t.Fatal(err)
	}
	var gotA2 Database
	for _, d := range realm("realm-a").Databases {
		if d.ID == dbA2 {
			gotA2 = d
		}
	}
	if gotA2.StorageCapBytes != 9000 {
		t.Fatalf("cap not applied: %d", gotA2.StorageCapBytes)
	}
	if gotA2.State != beforeA2.State || gotA2.Name != beforeA2.Name ||
		gotA2.Identity != beforeA2.Identity || gotA2.Layout != beforeA2.Layout || gotA2.KeyRef != beforeA2.KeyRef {
		t.Fatalf("realm-root path mutated non-cap fields: before=%+v after=%+v", beforeA2, gotA2)
	}
	// Registry access itself requires the deployment root — a Nano/Realm holder
	// (any non-root key) cannot open it.
	_ = reg.Close()
	if _, err := Open(path, testRoot(t)); err == nil {
		t.Fatal("registry opened with a non-deployment-root key")
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
