package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

// TestSystemHostingViewsNilRegistry proves system.realms/system.databases
// degrade to empty rows (never an error) on a legacy, non-hosted deployment
// where no hosting.Registry was ever wired via SetHostingRegistry.
func TestSystemHostingViewsNilRegistry(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()

	for _, view := range []string{"system.realms", "system.databases"} {
		res := execOK(t, sess, "SELECT * FROM "+view)
		if len(res.Rows) != 0 {
			t.Fatalf("%s with no hosting registry wired must be empty, got %v", view, res.Rows)
		}
	}
}

// TestSystemHostingViewsRBACAndContent proves system.realms/system.databases
// reflect a real hosting.Registry's manifest for an admin caller and stay
// empty for a non-admin one, mirroring system.resource_groups.
func TestSystemHostingViewsRBACAndContent(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()

	instanceRoot, err := crypto.CreateKeyFile(filepath.Join(dir, "instance.key"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ident1, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
		RealmName: "acme", DatabaseName: "db1", DatabaseIdentity: ident1, DatabaseState: hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	realm, db1, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}

	ident2, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	key2Path := filepath.Join(dir, "db2.key")
	if _, err := crypto.CreateKeyFile(key2Path, 1); err != nil {
		t.Fatal(err)
	}
	db2, _, err := reg.CreateDatabase(realm.ID, "db2", ident2, key2Path)
	if err != nil {
		t.Fatal(err)
	}

	acl, err := security.CreateACL(filepath.Join(dir, "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetHostingRegistry(reg)

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetHostingRegistry(reg)

	for _, view := range []string{"system.realms", "system.databases"} {
		res := execOK(t, app, "SELECT * FROM "+view)
		if len(res.Rows) != 0 {
			t.Fatalf("non-admin must see zero rows from %s, got %v", view, res.Rows)
		}
	}

	realmsRes := execOK(t, admin, "SELECT * FROM system.realms")
	if len(realmsRes.Rows) != 1 {
		t.Fatalf("system.realms = %v", realmsRes.Rows)
	}
	rrow := realmsRes.Rows[0]
	if rrow[0].Str != realm.ID.String() || rrow[1].Str != "acme" || rrow[2].Str != "active" {
		t.Fatalf("system.realms row = %+v", rrow)
	}
	if rrow[3].Dec.String() != "2" {
		t.Fatalf("system.realms database_count = %v, want 2", rrow[3])
	}

	dbsRes := execOK(t, admin, "SELECT * FROM system.databases")
	if len(dbsRes.Rows) != 2 {
		t.Fatalf("system.databases = %v", dbsRes.Rows)
	}
	byName := map[string][]types.Value{}
	for _, row := range dbsRes.Rows {
		byName[row[3].Str] = row
	}
	d1row, ok := byName["db1"]
	if !ok {
		t.Fatalf("system.databases missing db1: %v", dbsRes.Rows)
	}
	if d1row[0].Str != realm.ID.String() || d1row[1].Str != "acme" || d1row[2].Str != db1.ID.String() {
		t.Fatalf("db1 row = %+v", d1row)
	}
	if d1row[4].Str != "active" || d1row[5].Str != "legacy_default" {
		t.Fatalf("db1 row state/layout = %+v", d1row)
	}
	d2row, ok := byName["db2"]
	if !ok {
		t.Fatalf("system.databases missing db2: %v", dbsRes.Rows)
	}
	if d2row[2].Str != db2.ID.String() || d2row[4].Str != "provisioning" || d2row[5].Str != "managed" {
		t.Fatalf("db2 row = %+v", d2row)
	}
}
