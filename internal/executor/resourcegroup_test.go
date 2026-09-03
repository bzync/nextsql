package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestResourceGroupCatalogLifecycleAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE RESOURCE GROUP reporting WITH (MAX_CONCURRENCY = 8, MEMORY = 1073741824, WORKERS = 2, PRIORITY = 3)`)
	group, ok := s.lookupResourceGroup("reporting")
	if !ok || group.MaxConcurrency != 8 || group.MemoryBytes != 1073741824 || group.Workers != 2 || group.Priority != 3 {
		t.Fatalf("group=%+v ok=%v", group, ok)
	}
	if _, err := s.Exec(`CREATE RESOURCE GROUP reporting`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("duplicate create: %v", err)
	}
	execOK(t, s, `CREATE RESOURCE GROUP IF NOT EXISTS reporting`)
	execOK(t, s, `ALTER RESOURCE GROUP reporting WITH (WORKERS = 4)`)
	if group, ok = s.lookupResourceGroup("reporting"); !ok || group.Workers != 4 || group.MaxConcurrency != 8 {
		t.Fatalf("altered group=%+v ok=%v", group, ok)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if group, ok = s.lookupResourceGroup("reporting"); !ok || group.Workers != 4 || group.Priority != 3 {
		t.Fatalf("reloaded group=%+v ok=%v", group, ok)
	}
	execOK(t, s, `DROP RESOURCE GROUP reporting`)
	if _, ok = s.lookupResourceGroup("reporting"); ok {
		t.Fatal("dropped group remained visible")
	}
	execOK(t, s, `DROP RESOURCE GROUP IF EXISTS reporting`)
	if _, err := s.Exec(`DROP RESOURCE GROUP reporting`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("drop missing group: %v", err)
	}
}

func TestResourceGroupRollback(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE RESOURCE GROUP rolled_back`)
	execOK(t, s, `ROLLBACK`)
	if _, ok := s.lookupResourceGroup("rolled_back"); ok {
		t.Fatal("rolled-back resource group remained visible")
	}
}

func TestResourceGroupRBACAndIntrospection(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "s3cret"); err != nil {
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
	app.SetAuth(users)
	if _, err := app.Exec(`CREATE RESOURCE GROUP reporting`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("non-admin create must be forbidden: %v", err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	execOK(t, admin, `CREATE RESOURCE GROUP reporting WITH (MAX_CONCURRENCY = 5)`)

	if _, err := app.Exec(`ALTER RESOURCE GROUP reporting WITH (MAX_CONCURRENCY = 1)`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("non-admin alter must be forbidden: %v", err)
	}
	if _, err := app.Exec(`DROP RESOURCE GROUP reporting`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("non-admin drop must be forbidden: %v", err)
	}

	resApp := execOK(t, app, `SELECT * FROM system.resource_groups`)
	if len(resApp.Rows) != 0 {
		t.Fatalf("non-admin must see zero rows from system.resource_groups, got %v", resApp.Rows)
	}

	resAdmin := execOK(t, admin, `SELECT * FROM system.resource_groups`)
	if len(resAdmin.Rows) != 1 || resAdmin.Rows[0][0].Str != "reporting" {
		t.Fatalf("admin system.resource_groups = %v", resAdmin.Rows)
	}
}
