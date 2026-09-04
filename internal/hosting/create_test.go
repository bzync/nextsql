package hosting

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestCreateRealmAddsRealmAndFirstDatabase(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, created, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil || !created {
		t.Fatalf("bootstrap: created=%v err=%v", created, err)
	}
	defer reg.Close()
	before := reg.Manifest()

	ident := testIdentity(t)
	realm, db, created, err := reg.CreateRealm("Customer-B", "Production", ident, "/run/keys/b.key")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created=true for a brand-new realm")
	}
	if realm.Name != "customer-b" || realm.State != StateActive {
		t.Fatalf("unexpected realm: %+v", realm)
	}
	if db.Name != "production" || db.State != StateProvisioning || db.Layout != LayoutManaged {
		t.Fatalf("unexpected database: %+v", db)
	}
	if db.ID != ID(ident.Database) {
		t.Fatal("database ID must equal the identity's database UUID")
	}
	if db.KeyRef != "/run/keys/b.key" {
		t.Fatalf("KeyRef not stored: %+v", db)
	}

	after := reg.Manifest()
	if after.Generation <= before.Generation {
		t.Fatalf("expected a new durable generation: before=%d after=%d", before.Generation, after.Generation)
	}
	if len(after.Realms) != len(before.Realms)+1 {
		t.Fatalf("expected one additional realm: before=%d after=%d", len(before.Realms), len(after.Realms))
	}

	// Survives a restart.
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, root)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	found := false
	for _, r := range reopened.Manifest().Realms {
		if r.Name == "customer-b" {
			found = true
			if len(r.Databases) != 1 || r.Databases[0].Name != "production" || r.Databases[0].State != StateProvisioning {
				t.Fatalf("realm did not survive restart intact: %+v", r)
			}
		}
	}
	if !found {
		t.Fatal("new realm did not survive restart")
	}
}

func TestCreateRealmIsIdempotentOnRetryWithSameIdentity(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	ident := testIdentity(t)
	_, db1, created1, err := reg.CreateRealm("Customer-B", "Production", ident, "/run/keys/b.key")
	if err != nil || !created1 {
		t.Fatalf("first call: created=%v err=%v", created1, err)
	}
	genAfterFirst := reg.Manifest().Generation

	// Simulates a crash-and-retry: the same call, with the same identity
	// (as a real caller would get by re-reading the same on-disk keystore
	// file rather than generating a fresh one), must be a safe no-op.
	realm2, db2, created2, err := reg.CreateRealm("Customer-B", "Production", ident, "/run/keys/b.key")
	if err != nil {
		t.Fatal(err)
	}
	if created2 {
		t.Fatal("expected created=false on idempotent retry")
	}
	if db2 != db1 {
		t.Fatalf("idempotent retry returned a different database: %+v vs %+v", db2, db1)
	}
	if realm2.Name != "customer-b" {
		t.Fatalf("idempotent retry returned wrong realm: %+v", realm2)
	}
	if reg.Manifest().Generation != genAfterFirst {
		t.Fatal("idempotent retry must not durably persist a new generation")
	}
}

func TestCreateRealmRejectsRetryWithDifferentIdentity(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	if _, _, _, err := reg.CreateRealm("Customer-B", "Production", testIdentity(t), "/run/keys/b.key"); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = reg.CreateRealm("Customer-B", "Production", testIdentity(t), "/run/keys/b2.key")
	if !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected AlreadyExists for a name collision with a different identity, got %v", err)
	}
}

func TestCreateDatabaseAddsToExistingRealm(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	realmID := reg.Manifest().DefaultRealm

	ident := testIdentity(t)
	db, created, err := reg.CreateDatabase(realmID, "Staging", ident, "/run/keys/staging.key")
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("expected created=true")
	}
	if db.Name != "staging" || db.State != StateProvisioning {
		t.Fatalf("unexpected database: %+v", db)
	}

	m := reg.Manifest()
	var realm Realm
	for _, r := range m.Realms {
		if r.ID == realmID {
			realm = r
		}
	}
	if len(realm.Databases) != 2 {
		t.Fatalf("expected 2 databases (default + staging), got %d: %+v", len(realm.Databases), realm.Databases)
	}
}

func TestCreateDatabaseRejectsUnknownRealm(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	_, _, err = reg.CreateDatabase(ID{0xff}, "staging", testIdentity(t), "/run/keys/x.key")
	if !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestCreateDatabaseRejectsNonActiveRealm(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	ident := testIdentity(t)
	reg, created, err := EnsureManifest(path, root, func(dep ID) (Manifest, error) {
		realmID := deriveRealmID(dep, "suspended-realm")
		return Manifest{
			DeploymentID:    dep,
			Generation:      1,
			DefaultRealm:    realmID,
			DefaultDatabase: ID(ident.Database),
			Realms: []Realm{
				{ID: realmID, Name: "suspended-realm", State: StateSuspended, Databases: []Database{
					{ID: ID(ident.Database), Name: "prod", State: StateActive, Layout: LayoutManaged, Identity: ident, KeyRef: "/run/keys/prod.key"},
				}},
			},
		}, nil
	})
	if err != nil || !created {
		t.Fatalf("build suspended-realm registry: created=%v err=%v", created, err)
	}
	defer reg.Close()

	_, _, err = reg.CreateDatabase(reg.Manifest().DefaultRealm, "second", testIdentity(t), "/run/keys/second.key")
	if !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("expected Conflict for a non-active realm, got %v", err)
	}
}

func TestCreateDatabaseRejectsIdentityCollisionAcrossRealms(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	shared := testIdentity(t)
	realmA, _, _, err := reg.CreateRealm("realm-a", "one", shared, "/run/keys/a.key")
	if err != nil {
		t.Fatal(err)
	}
	realmB, _, _, err := reg.CreateRealm("realm-b", "one", testIdentity(t), "/run/keys/b.key")
	if err != nil {
		t.Fatal(err)
	}
	_ = realmA

	// A different name, but the same physical database identity as
	// realm-a's "one" — must never be allowed to register twice.
	_, _, err = reg.CreateDatabase(realmB.ID, "two", shared, "/run/keys/collide.key")
	if !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("expected Conflict for an identity collision, got %v", err)
	}
}

func TestLookupResolvesRealmAndDatabaseCaseInsensitively(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	realm, db, _, err := reg.CreateRealm("Customer-B", "Production", testIdentity(t), "/run/keys/b.key")
	if err != nil {
		t.Fatal(err)
	}
	// CreateRealm leaves the new database StateProvisioning; Lookup only
	// resolves Active pairs (M3-1), so activate it first — this test is
	// about name resolution, not state gating (see
	// TestLookupRejectsNonActiveDatabaseState/TestLookupRejectsNonActiveRealm
	// for that).
	if err := reg.SetDatabaseState(realm.ID, db.ID, StateActive); err != nil {
		t.Fatal(err)
	}

	gotRealm, gotDB, err := reg.Lookup("CUSTOMER-B", "production")
	if err != nil {
		t.Fatal(err)
	}
	if gotRealm.ID != realm.ID || gotDB.ID != db.ID {
		t.Fatalf("Lookup mismatch: realm=%+v db=%+v", gotRealm, gotDB)
	}
}

func TestLookupUnknownRealmOrDatabase(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	if _, _, _, err := reg.CreateRealm("customer-b", "production", testIdentity(t), "/run/keys/b.key"); err != nil {
		t.Fatal(err)
	}

	if _, _, err := reg.Lookup("no-such-realm", "production"); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("unknown realm: want NotFound, got %v", err)
	}
	if _, _, err := reg.Lookup("customer-b", "no-such-database"); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("unknown database: want NotFound, got %v", err)
	}
}

// TestLookupRejectsNonActiveDatabaseState is M3-1's core enforcement test:
// Lookup (dbmanager's sole connection-routing resolution) must fail closed
// for every non-Active database state, not just silently hand back a
// database a connection has no business being routed to.
func TestLookupRejectsNonActiveDatabaseState(t *testing.T) {
	cases := []struct {
		name       string
		reach      func(reg *Registry, realmID, dbID ID) error
		wantCode   nerr.Code
		wantSubstr string
	}{
		{
			name:       "provisioning",
			reach:      func(reg *Registry, realmID, dbID ID) error { return nil }, // CreateRealm's own initial state
			wantCode:   nerr.Unavailable,
			wantSubstr: "not yet active",
		},
		{
			name: "suspended",
			reach: func(reg *Registry, realmID, dbID ID) error {
				if err := reg.SetDatabaseState(realmID, dbID, StateActive); err != nil {
					return err
				}
				return reg.SetDatabaseState(realmID, dbID, StateSuspended)
			},
			wantCode:   nerr.Unavailable,
			wantSubstr: "suspended",
		},
		{
			name: "failed",
			reach: func(reg *Registry, realmID, dbID ID) error {
				return reg.SetDatabaseState(realmID, dbID, StateFailed)
			},
			wantCode:   nerr.Unavailable,
			wantSubstr: "failed state",
		},
		{
			name: "deleting",
			reach: func(reg *Registry, realmID, dbID ID) error {
				if err := reg.SetDatabaseState(realmID, dbID, StateActive); err != nil {
					return err
				}
				return reg.SetDatabaseState(realmID, dbID, StateDeleting)
			},
			wantCode:   nerr.NotFound,
			wantSubstr: "deleted",
		},
		{
			name: "tombstoned",
			reach: func(reg *Registry, realmID, dbID ID) error {
				if err := reg.SetDatabaseState(realmID, dbID, StateActive); err != nil {
					return err
				}
				if err := reg.SetDatabaseState(realmID, dbID, StateDeleting); err != nil {
					return err
				}
				return reg.SetDatabaseState(realmID, dbID, StateTombstoned)
			},
			wantCode:   nerr.NotFound,
			wantSubstr: "deleted",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := Path(t.TempDir())
			root := testRoot(t)
			reg, _, err := EnsureBootstrap(path, root, testBootstrap(t, StateActive))
			if err != nil {
				t.Fatal(err)
			}
			defer reg.Close()

			realm, db, _, err := reg.CreateRealm("customer-b", "production", testIdentity(t), "/run/keys/b.key")
			if err != nil {
				t.Fatal(err)
			}
			if err := tc.reach(reg, realm.ID, db.ID); err != nil {
				t.Fatalf("reach %s: %v", tc.name, err)
			}
			_, _, err = reg.Lookup("customer-b", "production")
			if !nerr.HasCode(err, tc.wantCode) {
				t.Fatalf("Lookup(%s): want code %s, got %v", tc.name, tc.wantCode, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Fatalf("Lookup(%s): want message containing %q, got %v", tc.name, tc.wantSubstr, err)
			}
		})
	}
}

// TestLookupRejectsNonActiveRealm proves a suspended realm blocks every one
// of its databases from Lookup even when the database's own state is
// Active — the same precedence CreateDatabase's realm.State check already
// uses. No public API can suspend a realm yet (realm suspend/delete is a
// separate, still-open M3 item), so this constructs the state directly via
// EnsureManifest, the same technique TestCreateDatabaseRejectsNonActiveRealm
// already uses.
func TestLookupRejectsNonActiveRealm(t *testing.T) {
	path := Path(t.TempDir())
	root := testRoot(t)
	ident := testIdentity(t)
	reg, created, err := EnsureManifest(path, root, func(dep ID) (Manifest, error) {
		realmID := deriveRealmID(dep, "suspended-realm")
		return Manifest{
			DeploymentID:    dep,
			Generation:      1,
			DefaultRealm:    realmID,
			DefaultDatabase: ID(ident.Database),
			Realms: []Realm{
				{ID: realmID, Name: "suspended-realm", State: StateSuspended, Databases: []Database{
					{ID: ID(ident.Database), Name: "prod", State: StateActive, Layout: LayoutManaged, Identity: ident, KeyRef: "/run/keys/prod.key"},
				}},
			},
		}, nil
	})
	if err != nil || !created {
		t.Fatalf("build suspended-realm registry: created=%v err=%v", created, err)
	}
	defer reg.Close()

	_, _, err = reg.Lookup("suspended-realm", "prod")
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("want Unavailable for a suspended realm, got %v", err)
	}
	if !strings.Contains(err.Error(), "realm suspended") {
		t.Fatalf("want a realm-specific message, got %v", err)
	}
}
