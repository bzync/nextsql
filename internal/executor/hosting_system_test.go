package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/storage/format"
)

// TestSystemDatabasesViewNilRegistry proves system.databases and
// system.quotas degrade to empty rows (never an error) on a deployment where
// no hosting.Registry was ever wired via SetHostingRegistry.
func TestSystemDatabasesViewNilRegistry(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()

	for _, view := range []string{"system.databases", "system.quotas"} {
		res := execOK(t, sess, "SELECT * FROM "+view)
		if len(res.Rows) != 0 {
			t.Fatalf("%s with no hosting registry wired must be empty, got %v", view, res.Rows)
		}
	}
}

// TestSystemRealmsViewIsGone proves the view removed with multi-realm hosting
// stays removed: a deployment serves exactly one database, so there is no
// realm dimension to select.
func TestSystemRealmsViewIsGone(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	if _, err := db.Session().Exec("SELECT * FROM system.realms"); err == nil {
		t.Fatal("system.realms must no longer resolve")
	}
	if _, err := db.Session().Exec("SHOW REALMS"); err == nil {
		t.Fatal("SHOW REALMS must no longer parse")
	}
}

// bootstrapSingleDatabaseRegistry builds the one-database deployment registry
// every deployment now has, in the state `nextsql init` leaves it.
func bootstrapSingleDatabaseRegistry(t *testing.T, dir string) *hosting.Registry {
	t.Helper()
	instanceRoot, err := crypto.CreateKeyFile(filepath.Join(dir, "instance.key"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
		RealmName: "default", DatabaseName: "db1", DatabaseIdentity: ident, DatabaseState: hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestSystemDatabasesRBACAndContent proves system.databases reports the one
// database this deployment serves for an admin caller and stays empty for a
// non-admin one, mirroring system.resource_groups.
func TestSystemDatabasesRBACAndContent(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()

	reg := bootstrapSingleDatabaseRegistry(t, dir)
	defer reg.Close()
	_, only, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}

	acl, err := security.CreateACL(filepath.Join(dir, "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []struct {
		user string
		priv security.Privilege
		sc   security.ScopeKind
	}{
		{"dba", security.PrivAdmin, security.ScopeCluster},
		{"dba", security.PrivConnect, security.ScopeDatabase},
		{"app", security.PrivConnect, security.ScopeDatabase},
	} {
		if err := acl.Grant(g.user, g.priv, g.sc, ""); err != nil {
			t.Fatal(err)
		}
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetHostingRegistry(reg)

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetHostingRegistry(reg)

	if res := execOK(t, app, "SELECT * FROM system.databases"); len(res.Rows) != 0 {
		t.Fatalf("non-admin must see zero rows, got %v", res.Rows)
	}

	res := execOK(t, admin, "SELECT * FROM system.databases")
	if len(res.Rows) != 1 {
		t.Fatalf("system.databases must hold exactly one row, got %v", res.Rows)
	}
	row := res.Rows[0]
	if row[0].Str != only.ID.String() || row[1].Str != "db1" || row[2].Str != "active" {
		t.Fatalf("system.databases row = %+v", row)
	}
}

// TestSystemQuotasReportsTheOneDatabase proves system.quotas surfaces the
// deployment's storage cap for an admin, stays empty for a non-admin, and
// fills the usage columns for the connected database.
func TestSystemQuotasReportsTheOneDatabase(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	db.SetDatabaseName("db1")

	reg := bootstrapSingleDatabaseRegistry(t, dir)
	defer reg.Close()

	acl, err := security.CreateACL(filepath.Join(dir, "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []struct {
		user string
		priv security.Privilege
		sc   security.ScopeKind
	}{
		{"dba", security.PrivAdmin, security.ScopeCluster},
		{"dba", security.PrivConnect, security.ScopeDatabase},
		{"app", security.PrivConnect, security.ScopeDatabase},
	} {
		if err := acl.Grant(g.user, g.priv, g.sc, ""); err != nil {
			t.Fatal(err)
		}
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetHostingRegistry(reg)
	if res := execOK(t, app, "SELECT * FROM system.quotas"); len(res.Rows) != 0 {
		t.Fatalf("non-admin must see zero quota rows, got %v", res.Rows)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetHostingRegistry(reg)
	res := execOK(t, admin, "SELECT * FROM system.quotas")
	if len(res.Rows) != 1 {
		t.Fatalf("system.quotas must hold exactly one row, got %v", res.Rows)
	}
	row := res.Rows[0]
	if row[0].Str != "db1" || row[1].Str != "active" {
		t.Fatalf("system.quotas row = %+v", row)
	}
	if !row[4].Bool {
		t.Fatalf("usage_known must be true for the connected database: %+v", row)
	}
}
