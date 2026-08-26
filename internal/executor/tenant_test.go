package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	tenantA = "11111111-1111-1111-1111-111111111111"
	tenantB = "22222222-2222-2222-2222-222222222222"
)

func TestTenantIsolationBoundSession(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		tenant_id UUID NOT NULL,
		name STRING NOT NULL
	)`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `INSERT INTO products (name) VALUES ('alpha')`)
	execOK(t, s, `INSERT INTO products (tenant_id, name) VALUES ('`+tenantB+`', 'should-rewrite')`)

	res := execOK(t, s, `SELECT name FROM products`)
	if len(res.Rows) != 2 {
		t.Fatalf("bound A should see injected rows: %d", len(res.Rows))
	}
	for _, row := range res.Rows {
		if row[0].Str != "alpha" && row[0].Str != "should-rewrite" {
			t.Fatalf("unexpected %q", row[0].Str)
		}
	}

	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	res = execOK(t, s, `SELECT name FROM products`)
	if len(res.Rows) != 0 {
		t.Fatalf("tenant B must not see A's rows: %d", len(res.Rows))
	}
	execOK(t, s, `INSERT INTO products (name) VALUES ('beta')`)

	execOK(t, s, `RESET TENANT`)
	res = execOK(t, s, `SELECT name FROM products`)
	if len(res.Rows) != 3 {
		t.Fatalf("unbound embedded session sees all: %d", len(res.Rows))
	}
}

func TestTenantWriteIsolation(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id STRING PRIMARY KEY,
		tenant_id UUID NOT NULL,
		name STRING NOT NULL
	)`)
	execOK(t, s, `INSERT INTO products (id, tenant_id, name) VALUES
		('a', '`+tenantA+`', 'alpha'),
		('b', '`+tenantB+`', 'beta')`)

	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	upd := execOK(t, s, `UPDATE products SET name = 'ALPHA'`)
	if upd.Affected != 1 {
		t.Fatalf("update affected %d", upd.Affected)
	}
	del := execOK(t, s, `DELETE FROM products WHERE id = 'b'`)
	if del.Affected != 0 {
		t.Fatalf("must not delete other tenant: %d", del.Affected)
	}
	if _, err := s.Exec(`UPDATE products SET tenant_id = '` + tenantB + `' WHERE id = 'a'`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("reassign tenant: %v", err)
	}

	execOK(t, s, `RESET TENANT`)
	res := execOK(t, s, `SELECT id, name FROM products WHERE id = 'b'`)
	if len(res.Rows) != 1 || res.Rows[0][1].Str != "beta" {
		t.Fatalf("other tenant row must survive: %+v", res.Rows)
	}
}

func TestTenantRequiredWithACL(t *testing.T) {
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
	execOK(t, admin, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		tenant_id UUID NOT NULL,
		name STRING NOT NULL
	)`)
	execOK(t, admin, `INSERT INTO products (tenant_id, name) VALUES
		('`+tenantA+`', 'alpha'),
		('`+tenantB+`', 'beta')`)
	execOK(t, admin, `CREATE USER app IDENTIFIED BY 'pw'`)
	execOK(t, admin, `GRANT SELECT, INSERT, UPDATE, DELETE ON TABLE products TO app`)

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`SELECT name FROM products`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("unbound app must not read tenant table: %v", err)
	}

	execOK(t, app, `SET TENANT = '`+tenantA+`'`)
	res := execOK(t, app, `SELECT name FROM products`)
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "alpha" {
		t.Fatalf("app A: %+v", res.Rows)
	}
	if _, err := app.Exec(`INSERT INTO products (name) VALUES ('alpha-2')`); err != nil {
		t.Fatal(err)
	}

	execOK(t, app, `SET TENANT = '`+tenantB+`'`)
	res = execOK(t, app, `SELECT name FROM products`)
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "beta" {
		t.Fatalf("app B: %+v", res.Rows)
	}

	all := execOK(t, admin, `SELECT name FROM products`)
	if len(all.Rows) != 3 {
		t.Fatalf("admin unbound sees all: %d", len(all.Rows))
	}
}

func TestTenantExportAndParam(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id STRING PRIMARY KEY,
		tenant_id UUID NOT NULL,
		name STRING NOT NULL
	)`)
	execOK(t, s, `INSERT INTO products (id, tenant_id, name) VALUES
		('a', '`+tenantA+`', 'alpha'),
		('b', '`+tenantB+`', 'beta')`)

	ua, err := types.ParseUUID(tenantA)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(context.Background(), `SET TENANT = $1`, []Param{{Value: ua}}); err != nil {
		t.Fatal(err)
	}
	var names []string
	if err := s.ForEachVisible("products", func(row []types.Value) error {
		names = append(names, row[2].Str)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "alpha" {
		t.Fatalf("export leaked: %v", names)
	}
}

func TestTenantJoinAndExplain(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, k STRING NOT NULL)`)
	execOK(t, s, `CREATE TABLE lines (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, k STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO orders (id, tenant_id, k) VALUES ('o1', '`+tenantA+`', 'k'), ('o2', '`+tenantB+`', 'k')`)
	execOK(t, s, `INSERT INTO lines (id, tenant_id, k) VALUES ('l1', '`+tenantA+`', 'k'), ('l2', '`+tenantB+`', 'k')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	res := execOK(t, s, `SELECT orders.id, lines.id FROM orders JOIN lines ON orders.k = lines.k`)
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "o1" || res.Rows[0][1].Str != "l1" {
		t.Fatalf("join leaked: %+v", res.Rows)
	}
}

func TestTenantLeftJoinUnmatched(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE customers (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, name STRING NOT NULL)`)
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, customer_id STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO customers (id, tenant_id, name) VALUES
		('c1', '`+tenantA+`', 'alice'),
		('c2', '`+tenantA+`', 'bob'),
		('c3', '`+tenantB+`', 'carol')`)
	execOK(t, s, `INSERT INTO orders (id, tenant_id, customer_id) VALUES
		('o1', '`+tenantA+`', 'c1'),
		('oB', '`+tenantB+`', 'c2')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	res := execOK(t, s, `SELECT customers.name, orders.id FROM customers LEFT JOIN orders ON orders.customer_id = customers.id`)
	if len(res.Rows) != 2 {
		t.Fatalf("want unmatched left kept, got %d %+v", len(res.Rows), res.Rows)
	}
	var sawAlice, sawBobNull bool
	for _, row := range res.Rows {
		if row[0].Str == "alice" && !row[1].Null && row[1].Str == "o1" {
			sawAlice = true
		}
		if row[0].Str == "bob" && row[1].Null {
			sawBobNull = true
		}
		if row[0].Str == "carol" {
			t.Fatalf("other tenant leaked: %+v", res.Rows)
		}
		if !row[1].Null && row[1].Str == "oB" {
			t.Fatalf("other-tenant order matched: %+v", res.Rows)
		}
	}
	if !sawAlice || !sawBobNull {
		t.Fatalf("rows %+v", res.Rows)
	}
}

func TestTenantFullOuterJoinOnlyBoundTenant(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE lhs (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, k STRING NOT NULL, n STRING NOT NULL)`)
	execOK(t, s, `CREATE TABLE rhs (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, k STRING NOT NULL, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO lhs (id, tenant_id, k, n) VALUES
		('lx', '`+tenantA+`', 'x', 'LX'),
		('ly', '`+tenantB+`', 'y', 'LY'),
		('lz', '`+tenantB+`', 'z', 'LZ')`)
	execOK(t, s, `INSERT INTO rhs (id, tenant_id, k, n) VALUES
		('rx', '`+tenantA+`', 'x', 'RX'),
		('ry', '`+tenantB+`', 'y', 'RY'),
		('rw', '`+tenantB+`', 'w', 'RW')`)
	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	res := execOK(t, s, `SELECT lhs.n, rhs.n FROM lhs FULL OUTER JOIN rhs ON lhs.k = rhs.k`)
	if len(res.Rows) != 3 {
		t.Fatalf("want only tenant Y, got %d %+v", len(res.Rows), res.Rows)
	}
	var sawMatch, sawL, sawR bool
	for _, row := range res.Rows {
		for _, v := range row {
			if !v.Null && (v.Str == "LX" || v.Str == "RX") {
				t.Fatalf("tenant X leaked: %+v", res.Rows)
			}
		}
		if !row[0].Null && row[0].Str == "LY" && !row[1].Null && row[1].Str == "RY" {
			sawMatch = true
		}
		if !row[0].Null && row[0].Str == "LZ" && row[1].Null {
			sawL = true
		}
		if row[0].Null && !row[1].Null && row[1].Str == "RW" {
			sawR = true
		}
	}
	if !sawMatch || !sawL || !sawR {
		t.Fatalf("rows %+v", res.Rows)
	}
}

func TestTenantDoesNotAffectUntaggedTables(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE items (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `INSERT INTO items (id, n) VALUES ('1', 'x')`)
	res := execOK(t, s, `SELECT n FROM items`)
	if len(res.Rows) != 1 {
		t.Fatalf("%d", len(res.Rows))
	}
}
