package executor

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

func TestSharedTenantSQLIsRemoved(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	for _, sql := range []string{
		`SET TENANT = '` + tenantA + `'`,
		`RESET TENANT`,
		`CREATE TABLE legacy_partition (tenant_id STRING PRIMARY KEY) PARTITION BY TENANT (tenant_id) (PARTITION p VALUES IN ('a'))`,
	} {
		_, err := s.Exec(sql)
		if !nerr.HasCode(err, nerr.Syntax) {
			t.Fatalf("%q: expected syntax rejection, got %v", sql, err)
		}
		if !strings.Contains(err.Error(), "isolated") {
			t.Fatalf("%q: missing hosted-database guidance: %v", sql, err)
		}
	}
}

func TestLegacyTenantTableFailsClosedForNonAdmin(t *testing.T) {
	db := testDB(t)
	local := db.Session()
	execOK(t, local, `CREATE TABLE legacy_rows (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, value STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO legacy_rows (id, tenant_id, value) VALUES
		('a', '`+tenantA+`', 'alpha'), ('b', '`+tenantB+`', 'beta')`)
	execOK(t, local, `CREATE TABLE isolated_rows (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, local, `INSERT INTO isolated_rows (id, value) VALUES ('a', 'alpha')`)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, privilege := range []security.Privilege{security.PrivSelect, security.PrivInsert, security.PrivUpdate, security.PrivDelete, security.PrivCDC} {
		if err := acl.Grant("app", privilege, security.ScopeTable, "legacy_rows"); err != nil {
			t.Fatal(err)
		}
	}
	if err := acl.Grant("app", security.PrivSelect, security.ScopeTable, "isolated_rows"); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	for _, sql := range []string{
		`SELECT value FROM legacy_rows`,
		`SELECT isolated_rows.value FROM isolated_rows JOIN legacy_rows ON legacy_rows.id = isolated_rows.id`,
		`SELECT value FROM isolated_rows WHERE EXISTS (SELECT id FROM legacy_rows)`,
		`INSERT INTO legacy_rows (id, tenant_id, value) VALUES ('c', '` + tenantA + `', 'blocked')`,
		`UPDATE legacy_rows SET value = 'blocked' WHERE id = 'a'`,
		`DELETE FROM legacy_rows WHERE id = 'a'`,
	} {
		if _, err := app.Exec(sql); !nerr.HasCode(err, nerr.Forbidden) {
			t.Fatalf("legacy table did not fail closed for %q: %v", sql, err)
		}
	}
	if err := app.ForEachVisible("legacy_rows", func([]types.Value) error { return nil }); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("legacy export did not fail closed: %v", err)
	}
	if _, err := app.Query(`SUBSCRIBE TO legacy_rows`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("legacy CDC subscription did not fail closed: %v", err)
	}
	res := execOK(t, app, `SELECT value FROM isolated_rows`)
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "alpha" {
		t.Fatalf("isolated database table access changed: %+v", res.Rows)
	}

	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	res = execOK(t, admin, `SELECT id, value FROM legacy_rows ORDER BY id`)
	if len(res.Rows) != 2 {
		t.Fatalf("ADMIN migration view lost legacy rows: %+v", res.Rows)
	}
}
