package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestRBACDeniesAndGrants(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	if _, err := admin.Exec(`CREATE TABLE t (id UUID PRIMARY KEY, n STRING)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE USER app IDENTIFIED BY 'pw'`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`GRANT SELECT ON TABLE t TO app`); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`SELECT * FROM t`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO t (id, n) VALUES (UUID(), 'x')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("insert must be forbidden: %v", err)
	}
	if _, err := admin.Exec(`GRANT INSERT ON TABLE t TO app`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO t (id, n) VALUES (UUID(), 'x')`); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(`CREATE USER nobody IDENTIFIED BY 'pw'`); err != nil {
		t.Fatal(err)
	}
	nobody := db.Session()
	nobody.SetIdentity("nobody")
	nobody.SetACL(acl)
	if _, err := nobody.Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted select must fail closed: %v", err)
	}
	if _, err := nobody.Exec(`BEGIN`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted begin must fail closed: %v", err)
	}
}
