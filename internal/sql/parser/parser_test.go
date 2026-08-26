package parser

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestParseCreateTableProduct(t *testing.T) {
	src := `
CREATE TABLE products (
    id          UUID PRIMARY KEY DEFAULT UUID(),
    tenant_id   UUID NOT NULL,
    name        STRING NOT NULL,
    description TEXT,
    price       DECIMAL(12,2),
    metadata    JSON,
    embedding   VECTOR<F32,1536>,
    created_at  TIMESTAMPTZ DEFAULT NOW()
);`
	stmt, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	ct, ok := stmt.(ast.CreateTable)
	if !ok {
		t.Fatalf("%T", stmt)
	}
	if ct.Name != "products" || len(ct.Columns) != 8 || len(ct.PK) != 1 || ct.PK[0] != "id" {
		t.Fatalf("%+v", ct)
	}
	if ct.Columns[4].Type.Kind != types.KindDecimal || ct.Columns[4].Type.Precision != 12 || ct.Columns[4].Type.Scale != 2 {
		t.Fatalf("decimal %+v", ct.Columns[4].Type)
	}
	if ct.Columns[6].Type.Kind != types.KindVector || ct.Columns[6].Type.Precision != 1536 {
		t.Fatalf("vector %+v", ct.Columns[6].Type)
	}
	if _, ok := ct.Columns[0].Default.(ast.Call); !ok {
		t.Fatalf("default uuid %T", ct.Columns[0].Default)
	}
}

func TestParseSubscribe(t *testing.T) {
	stmt, err := Parse(`SUBSCRIBE TO orders WHERE operation = 'update' AFTER 18446744073709551614`)
	if err != nil {
		t.Fatal(err)
	}
	sub, ok := stmt.(ast.Subscribe)
	if !ok || sub.Table != "orders" || sub.Operation != "UPDATE" || sub.After != 18446744073709551614 {
		t.Fatalf("subscribe=%#v", stmt)
	}
	for _, sql := range []string{
		`SUBSCRIBE orders`,
		`SUBSCRIBE TO orders AFTER '1'`,
		`SUBSCRIBE TO orders AFTER 18446744073709551616`,
		`SUBSCRIBE TO orders WHERE tenant = 'other'`,
		`SUBSCRIBE TO orders WHERE operation = 'truncate'`,
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatalf("expected parse failure for %q", sql)
		}
	}
}

func TestParseAIDefault(t *testing.T) {
	stmt, err := Parse(`CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	ct, ok := stmt.(ast.CreateTable)
	if !ok {
		t.Fatalf("%T", stmt)
	}
	call, ok := ct.Columns[0].Default.(ast.Call)
	if !ok || call.Name != "ai" || len(call.Args) != 0 {
		t.Fatalf("default %+v", ct.Columns[0].Default)
	}
}

func TestParseLimitOffset(t *testing.T) {
	stmt, err := Parse(`SELECT n FROM t ORDER BY n LIMIT 5 OFFSET 10`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	if sel.Limit == nil || *sel.Limit != 5 || sel.Offset == nil || *sel.Offset != 10 {
		t.Fatalf("limit/offset %+v %+v", sel.Limit, sel.Offset)
	}
	stmt, err = Parse(`SELECT n FROM t OFFSET 3 LIMIT 2`)
	if err != nil {
		t.Fatal(err)
	}
	sel = stmt.(ast.Select)
	if sel.Limit == nil || *sel.Limit != 2 || sel.Offset == nil || *sel.Offset != 3 {
		t.Fatalf("offset first %+v %+v", sel.Limit, sel.Offset)
	}
	stmt, err = Parse(`SELECT n FROM t OFFSET 4`)
	if err != nil {
		t.Fatal(err)
	}
	sel = stmt.(ast.Select)
	if sel.Limit != nil || sel.Offset == nil || *sel.Offset != 4 {
		t.Fatalf("offset only %+v %+v", sel.Limit, sel.Offset)
	}
}

func TestParseStatements(t *testing.T) {
	cases := []struct {
		src string
		typ any
	}{
		{`CREATE INDEX ix_name ON products (name)`, ast.CreateIndex{}},
		{`CREATE SPATIAL INDEX ix_loc ON places (loc)`, ast.CreateIndex{}},
		{`CREATE FULLTEXT INDEX ix_body ON articles (body)`, ast.CreateIndex{}},
		{`SELECT * FROM articles SEARCH body FOR 'database performance' LIMIT 20`, ast.Select{}},
		{`CREATE UNIQUE INDEX uq ON t (a, b)`, ast.CreateIndex{}},
		{`INSERT INTO t (a, b) VALUES (1, 'x'), (2, 'y')`, ast.Insert{}},
		{`INSERT INTO t (a) VALUES (1) RETURNING a`, ast.Insert{}},
		{`INSERT INTO t (a) VALUES (1) RETURNING *`, ast.Insert{}},
		{`UPSERT INTO t (a, b) VALUES (1, 'x')`, ast.Upsert{}},
		{`UPSERT INTO t (email, name) VALUES ('a@b', 'n') ON UNIQUE (email)`, ast.Upsert{}},
		{`UPSERT INTO t (email, name) VALUES ('a@b', 'n') ON UNIQUE (email) SET name = excluded.name`, ast.Upsert{}},
		{`UPSERT INTO t (a, b) VALUES (1, 'x') RETURNING a, b`, ast.Upsert{}},
		{`UPDATE products SET price = 1.5 WHERE id = 'x' RETURNING id, price`, ast.Update{}},
		{`DELETE FROM products WHERE name IS NULL RETURNING *`, ast.Delete{}},
		{`SELECT id, name, price FROM products WHERE price BETWEEN 1000 AND 5000`, ast.Select{}},
		{`SELECT * FROM products WHERE name = 'a' AND price <= 10 LIMIT 20`, ast.Select{}},
		{`SELECT n FROM t ORDER BY n LIMIT 5 OFFSET 10`, ast.Select{}},
		{`SELECT COUNT(*) FROM t GROUP BY k`, ast.Select{}},
		{`SELECT a.n, b.n FROM t a JOIN u b ON a.k = b.k`, ast.Select{}},
		{`SELECT a.n, b.n FROM t a LEFT JOIN u b ON a.k = b.k`, ast.Select{}},
		{`SELECT a.n, b.n FROM t a LEFT OUTER JOIN u b ON a.k = b.k`, ast.Select{}},
		{`SELECT a.n, b.n FROM t a RIGHT JOIN u b ON a.k = b.k`, ast.Select{}},
		{`SELECT a.n, b.n FROM t a FULL OUTER JOIN u b ON a.k = b.k`, ast.Select{}},
		{`SELECT a.n, b.n FROM t a CROSS JOIN u b`, ast.Select{}},
		{`UPDATE products SET price = 1.5 WHERE id = 'x'`, ast.Update{}},
		{`UPDATE scan SET n = 0 WHERE n <> 0 LIMIT 8192`, ast.Update{}},
		{`DELETE FROM products WHERE name IS NULL`, ast.Delete{}},
		{`DELETE FROM scan LIMIT 8192`, ast.Delete{}},
		{`BEGIN`, ast.Begin{}},
		{`BEGIN SNAPSHOT`, ast.Begin{}},
		{`BEGIN READ COMMITTED`, ast.Begin{}},
		{`COMMIT`, ast.Commit{}},
		{`ROLLBACK TRANSACTION`, ast.Rollback{}},
		{`EXPLAIN SELECT * FROM t`, ast.Explain{}},
		{`EXPLAIN ANALYZE SELECT id FROM t WHERE n = 1`, ast.Explain{}},
		{`ANALYZE`, ast.Analyze{}},
		{`ANALYZE products`, ast.Analyze{}},
		{`MAINTAIN DATABASE`, ast.Maintain{}},
		{`MAINTAIN TABLE products`, ast.Maintain{}},
		{`MAINTAIN INDEX ix_products_name`, ast.Maintain{}},
		{`SET TENANT = '11111111-1111-1111-1111-111111111111'`, ast.SetTenant{}},
		{`SET TENANT = $1`, ast.SetTenant{}},
		{`RESET TENANT`, ast.SetTenant{}},
		{`SELECT name, price FROM products ORDER BY price DESC, name`, ast.Select{}},
		{`SELECT * FROM products ORDER BY 1 ASC LIMIT 10`, ast.Select{}},
		{`SELECT k, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v) FROM t`, ast.Select{}},
		{`SELECT SUM(v) OVER (ORDER BY k ROWS BETWEEN UNBOUNDED PRECEDING AND CURRENT ROW) FROM t`, ast.Select{}},
		{`SELECT LAG(v, 1, 0) OVER (PARTITION BY k ORDER BY id) FROM t`, ast.Select{}},
		{`DROP TABLE items`, ast.DropTable{}},
		{`DROP TABLE IF EXISTS items`, ast.DropTable{}},
		{`DROP INDEX ix_items_name`, ast.DropIndex{}},
		{`DROP INDEX IF EXISTS ix_items_name`, ast.DropIndex{}},
		{`REBUILD INDEX ix_items_name`, ast.RebuildIndex{}},
		{`ALTER TABLE items ADD name STRING`, ast.AlterTable{}},
		{`ALTER TABLE items ADD COLUMN note TEXT`, ast.AlterTable{}},
		{`ALTER TABLE items DROP COLUMN note`, ast.AlterTable{}},
		{`ALTER TABLE items RENAME COLUMN name TO title`, ast.AlterTable{}},
		{`ALTER TABLE items RENAME TO products`, ast.AlterTable{}},
		{`ALTER TABLE orders ADD CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)`, ast.AlterTable{}},
		{`ALTER TABLE orders DROP CONSTRAINT fk_orders_customer`, ast.AlterTable{}},
		{`CREATE DATABASE app`, ast.CreateDatabase{}},
		{`CREATE DATABASE IF NOT EXISTS app`, ast.CreateDatabase{}},
	}
	for _, tc := range cases {
		stmt, err := Parse(tc.src)
		if err != nil {
			t.Fatalf("%s: %v", tc.src, err)
		}
		switch tc.typ.(type) {
		case ast.CreateIndex:
			if _, ok := stmt.(ast.CreateIndex); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Insert:
			if _, ok := stmt.(ast.Insert); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Select:
			if _, ok := stmt.(ast.Select); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Update:
			if _, ok := stmt.(ast.Update); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Delete:
			if _, ok := stmt.(ast.Delete); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Begin:
			if _, ok := stmt.(ast.Begin); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Commit:
			if _, ok := stmt.(ast.Commit); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Rollback:
			if _, ok := stmt.(ast.Rollback); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Explain:
			ex, ok := stmt.(ast.Explain)
			if !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
			if strings.Contains(tc.src, "ANALYZE") != ex.Analyze {
				t.Fatalf("%s analyze=%v", tc.src, ex.Analyze)
			}
		case ast.SetTenant:
			if _, ok := stmt.(ast.SetTenant); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Analyze:
			if _, ok := stmt.(ast.Analyze); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.Maintain:
			if _, ok := stmt.(ast.Maintain); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.DropTable:
			if _, ok := stmt.(ast.DropTable); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.AlterTable:
			if _, ok := stmt.(ast.AlterTable); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		case ast.CreateDatabase:
			if _, ok := stmt.(ast.CreateDatabase); !ok {
				t.Fatalf("%s: %T", tc.src, stmt)
			}
		}
	}
}

func TestParseOrderBy(t *testing.T) {
	stmt, err := Parse(`SELECT name, price FROM products ORDER BY price DESC, name ASC`)
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(ast.Select)
	if len(s.Order) != 2 || !s.Order[0].Desc || s.Order[1].Desc {
		t.Fatalf("%+v", s.Order)
	}
}

func TestParseSelectDistinct(t *testing.T) {
	stmt, err := Parse(`SELECT DISTINCT name, price FROM products ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := stmt.(ast.Select)
	if !ok || !sel.Distinct || len(sel.List) != 2 || len(sel.Order) != 1 {
		t.Fatalf("unexpected DISTINCT AST: %#v", stmt)
	}
}

func TestParseWindow(t *testing.T) {
	stmt, err := Parse(`SELECT k, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v DESC) AS n FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	w, ok := sel.List[1].Expr.(ast.Window)
	if !ok || w.Fn.Name != "row_number" || len(w.Partition) != 1 || len(w.Order) != 1 || !w.Order[0].Desc || w.Frame != nil {
		t.Fatalf("unexpected window: %#v", sel.List[1].Expr)
	}

	stmt, err = Parse(`SELECT SUM(v) OVER (ORDER BY k ROWS BETWEEN 1 PRECEDING AND CURRENT ROW) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	sel = stmt.(ast.Select)
	w, ok = sel.List[0].Expr.(ast.Window)
	if !ok || w.Fn.Name != "sum" || w.Frame == nil || w.Frame.Mode != ast.FrameRows || w.Frame.Start.Kind != ast.BoundPreceding || w.Frame.End.Kind != ast.BoundCurrentRow {
		t.Fatalf("unexpected framed window: %#v", sel.List[0].Expr)
	}

	stmt, err = Parse(`SELECT RANK() OVER () FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	sel = stmt.(ast.Select)
	if _, ok := sel.List[0].Expr.(ast.Window); !ok {
		t.Fatalf("expected empty OVER: %#v", sel.List[0].Expr)
	}

	if _, err := Parse(`SELECT k OVER () FROM t`); err == nil {
		t.Fatal("expected OVER on non-call to fail")
	}
}

func TestParseHaving(t *testing.T) {
	stmt, err := Parse(`SELECT category, COUNT(*) AS total FROM products GROUP BY category HAVING total > 1 ORDER BY category`)
	if err != nil {
		t.Fatal(err)
	}
	sel, ok := stmt.(ast.Select)
	if !ok || sel.Having == nil || len(sel.Group) != 1 {
		t.Fatalf("unexpected HAVING AST: %#v", stmt)
	}
}

func TestParseCaseExpressions(t *testing.T) {
	stmt, err := Parse(`SELECT CASE WHEN price > 10 THEN 'high' ELSE 'low' END AS band, CASE state WHEN 'p' THEN 1 ELSE 0 END AS pending FROM products`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	if len(sel.List) != 2 {
		t.Fatalf("unexpected CASE select list: %#v", sel.List)
	}
	searched, ok := sel.List[0].Expr.(ast.Case)
	if !ok || searched.Operand != nil || len(searched.Whens) != 1 {
		t.Fatalf("unexpected searched CASE: %#v", sel.List[0].Expr)
	}
	simple, ok := sel.List[1].Expr.(ast.Case)
	if !ok || simple.Operand == nil || len(simple.Whens) != 1 {
		t.Fatalf("unexpected simple CASE: %#v", sel.List[1].Expr)
	}
}

func TestParseUnionAll(t *testing.T) {
	stmt, err := Parse(`SELECT id AS value FROM left_rows UNION ALL SELECT id FROM right_rows UNION ALL SELECT id FROM third_rows`)
	if err != nil {
		t.Fatal(err)
	}
	op, ok := stmt.(ast.SetOperation)
	if !ok || !op.All {
		t.Fatalf("unexpected UNION ALL AST: %#v", stmt)
	}
	if _, ok := op.Left.(ast.SetOperation); !ok {
		t.Fatalf("UNION ALL must associate left: %#v", op.Left)
	}
	plain, err := Parse(`SELECT id FROM left_rows UNION SELECT id FROM right_rows`)
	if err != nil {
		t.Fatal(err)
	}
	if op, ok := plain.(ast.SetOperation); !ok || op.All {
		t.Fatalf("unexpected UNION AST: %#v", plain)
	}
}

func TestParseIntersectExceptPrecedence(t *testing.T) {
	stmt, err := Parse(`SELECT id FROM a UNION SELECT id FROM b INTERSECT SELECT id FROM c EXCEPT SELECT id FROM d`)
	if err != nil {
		t.Fatal(err)
	}
	top, ok := stmt.(ast.SetOperation)
	if !ok || top.Op != "except" {
		t.Fatalf("unexpected top set operation: %#v", stmt)
	}
	union, ok := top.Left.(ast.SetOperation)
	if !ok || union.Op != "union" {
		t.Fatalf("unexpected UNION level: %#v", top.Left)
	}
	intersect, ok := union.Right.(ast.SetOperation)
	if !ok || intersect.Op != "intersect" {
		t.Fatalf("INTERSECT did not bind tighter: %#v", union.Right)
	}
}

func TestParseScalarSubquery(t *testing.T) {
	stmt, err := Parse(`SELECT (SELECT value FROM inner_rows WHERE id = '1') AS nested FROM outer_rows`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	if len(sel.List) != 1 {
		t.Fatalf("unexpected select list: %#v", sel.List)
	}
	sub, ok := sel.List[0].Expr.(ast.ScalarSubquery)
	if !ok {
		t.Fatalf("expected scalar subquery, got %T", sel.List[0].Expr)
	}
	if _, ok := sub.Query.(ast.Select); !ok {
		t.Fatalf("unexpected scalar query: %T", sub.Query)
	}
}

func TestParseInSubquery(t *testing.T) {
	stmt, err := Parse(`SELECT id FROM outer_rows WHERE id NOT IN (SELECT value FROM inner_rows)`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	in, ok := sel.Where.(ast.InSubquery)
	if !ok || !in.Not {
		t.Fatalf("unexpected NOT IN expression: %#v", sel.Where)
	}
}

func TestParseExistsSubquery(t *testing.T) {
	stmt, err := Parse(`SELECT id FROM outer_rows WHERE NOT EXISTS (SELECT id FROM inner_rows)`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	not, ok := sel.Where.(ast.Unary)
	if !ok || not.Op != "NOT" {
		t.Fatalf("unexpected NOT EXISTS expression: %#v", sel.Where)
	}
	if _, ok := not.Right.(ast.ExistsSubquery); !ok {
		t.Fatalf("unexpected EXISTS operand: %T", not.Right)
	}
}

func TestParseWith(t *testing.T) {
	stmt, err := Parse(`WITH c AS (SELECT value FROM t) SELECT value FROM c`)
	if err != nil {
		t.Fatal(err)
	}
	w := stmt.(ast.With)
	if w.Recursive || len(w.CTEs) != 1 || w.CTEs[0].Name != "c" || w.CTEs[0].Materialize != ast.CTEAuto {
		t.Fatalf("unexpected WITH: %#v", w)
	}
	if _, ok := w.Query.(ast.Select); !ok {
		t.Fatalf("query %T", w.Query)
	}

	stmt, err = Parse(`WITH RECURSIVE c(a, b) AS MATERIALIZED (SELECT id, parent FROM t) SELECT a FROM c`)
	if err != nil {
		t.Fatal(err)
	}
	w = stmt.(ast.With)
	if !w.Recursive || w.CTEs[0].Name != "c" || len(w.CTEs[0].Columns) != 2 || w.CTEs[0].Materialize != ast.CTEAlways {
		t.Fatalf("recursive MATERIALIZED: %#v", w)
	}

	stmt, err = Parse(`WITH c AS NOT MATERIALIZED (SELECT value FROM t), d AS (SELECT value FROM c) SELECT value FROM d`)
	if err != nil {
		t.Fatal(err)
	}
	w = stmt.(ast.With)
	if len(w.CTEs) != 2 || w.CTEs[0].Materialize != ast.CTENever || w.CTEs[1].Name != "d" {
		t.Fatalf("multiple CTEs: %#v", w)
	}

	if _, err := Parse(`WITH SELECT value FROM t`); err == nil {
		t.Fatal("WITH without CTE must fail")
	}
	if _, err := Parse(`WITH c AS (SELECT value FROM t)`); err == nil {
		t.Fatal("WITH without query must fail")
	}
}

func TestParseDerivedTable(t *testing.T) {
	stmt, err := Parse(`SELECT d.value FROM (SELECT value FROM source_rows) AS d WHERE d.value = 'a'`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	if sel.FromQuery == nil || sel.Alias != "d" || sel.Table != "" {
		t.Fatalf("unexpected derived table: %#v", sel)
	}
	if _, err := Parse(`SELECT value FROM (SELECT value FROM source_rows)`); err == nil {
		t.Fatal("derived table without alias must fail")
	}
}

func TestParseDropTableIfExists(t *testing.T) {
	stmt, err := Parse(`DROP TABLE IF EXISTS items`)
	if err != nil {
		t.Fatal(err)
	}
	d := stmt.(ast.DropTable)
	if d.Name != "items" || !d.IfExists {
		t.Fatalf("%+v", d)
	}
}

func TestParseDropIndexIfExists(t *testing.T) {
	stmt, err := Parse(`DROP INDEX IF EXISTS ix_items_name`)
	if err != nil {
		t.Fatal(err)
	}
	d := stmt.(ast.DropIndex)
	if d.Name != "ix_items_name" || !d.IfExists || d.Table != "" {
		t.Fatalf("%+v", d)
	}
}

func TestParseRebuildIndex(t *testing.T) {
	stmt, err := Parse(`REBUILD INDEX ix_items_name`)
	if err != nil {
		t.Fatal(err)
	}
	r := stmt.(ast.RebuildIndex)
	if r.Name != "ix_items_name" || r.Table != "" {
		t.Fatalf("%+v", r)
	}
	if _, err := Parse(`REBUILD INDEX ix_items_name ONLINE`); err == nil {
		t.Fatal("ONLINE must remain unsupported until concurrent-write safety is implemented")
	}
}

func TestParseAlterTableCmds(t *testing.T) {
	stmt, err := Parse(`ALTER TABLE items ADD note STRING NOT NULL DEFAULT ''`)
	if err != nil {
		t.Fatal(err)
	}
	a := stmt.(ast.AlterTable)
	if a.Table != "items" {
		t.Fatalf("%+v", a)
	}
	add, ok := a.Cmd.(ast.AlterAddColumn)
	if !ok || add.Column.Name != "note" || !add.Column.NotNull {
		t.Fatalf("%T %+v", a.Cmd, a.Cmd)
	}

	stmt, err = Parse(`CREATE DATABASE IF NOT EXISTS analytics`)
	if err != nil {
		t.Fatal(err)
	}
	cd := stmt.(ast.CreateDatabase)
	if cd.Name != "analytics" || !cd.IfNotExists {
		t.Fatalf("%+v", cd)
	}
}

func TestParseLeftJoin(t *testing.T) {
	stmt, err := Parse(`SELECT c.name, o.id FROM customers c LEFT JOIN orders o ON o.customer_id = c.id`)
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(ast.Select)
	if len(s.Joins) != 1 || s.Joins[0].Kind != ast.JoinLeft || s.Joins[0].On == nil {
		t.Fatalf("%+v", s.Joins)
	}
	stmt, err = Parse(`SELECT c.name, o.id FROM customers c LEFT OUTER JOIN orders o ON o.customer_id = c.id`)
	if err != nil {
		t.Fatal(err)
	}
	s = stmt.(ast.Select)
	if len(s.Joins) != 1 || s.Joins[0].Kind != ast.JoinLeft {
		t.Fatalf("OUTER %+v", s.Joins)
	}
	if _, err := Parse(`SELECT c.name FROM customers c LEFT JOIN orders o`); err == nil {
		t.Fatal("expected LEFT JOIN without ON to fail")
	}
}

func TestParseRightFullCrossJoin(t *testing.T) {
	stmt, err := Parse(`SELECT c.name, o.id FROM orders o RIGHT JOIN customers c ON o.customer_id = c.id`)
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(ast.Select)
	if len(s.Joins) != 1 || s.Joins[0].Kind != ast.JoinRight || s.Joins[0].On == nil {
		t.Fatalf("RIGHT %+v", s.Joins)
	}
	stmt, err = Parse(`SELECT a.e, b.e FROM t a FULL OUTER JOIN u b ON a.e = b.e`)
	if err != nil {
		t.Fatal(err)
	}
	s = stmt.(ast.Select)
	if len(s.Joins) != 1 || s.Joins[0].Kind != ast.JoinFull || s.Joins[0].On == nil {
		t.Fatalf("FULL %+v", s.Joins)
	}
	stmt, err = Parse(`SELECT a.n, b.n FROM t a CROSS JOIN u b`)
	if err != nil {
		t.Fatal(err)
	}
	s = stmt.(ast.Select)
	if len(s.Joins) != 1 || s.Joins[0].Kind != ast.JoinCross || s.Joins[0].On != nil || !s.Joins[0].Cross {
		t.Fatalf("CROSS %+v", s.Joins)
	}
	if _, err := Parse(`SELECT a.n FROM t a CROSS JOIN u b ON a.k = b.k`); err == nil {
		t.Fatal("expected CROSS JOIN ... ON to fail")
	}
	if _, err := Parse(`SELECT a.n FROM t a RIGHT JOIN u b`); err == nil {
		t.Fatal("expected RIGHT JOIN without ON to fail")
	}
	if _, err := Parse(`SELECT a.n FROM t a FULL JOIN u b`); err == nil {
		t.Fatal("expected FULL JOIN without ON to fail")
	}
}

func TestParseSecurityStatements(t *testing.T) {
	stmt, err := Parse(`CREATE USER app IDENTIFIED BY 's3cret'`)
	if err != nil {
		t.Fatal(err)
	}
	cu, ok := stmt.(ast.CreateUser)
	if !ok || cu.Name != "app" || cu.Password != "s3cret" {
		t.Fatalf("%+v", stmt)
	}
	g, err := Parse(`GRANT SELECT, INSERT ON TABLE products TO analyst`)
	if err != nil {
		t.Fatal(err)
	}
	gr, ok := g.(ast.Grant)
	if !ok || gr.Grantee != "analyst" || gr.Scope != "table" || gr.Object != "products" || len(gr.Privileges) != 2 {
		t.Fatalf("%+v", g)
	}
	r, err := Parse(`GRANT analyst TO bob`)
	if err != nil {
		t.Fatal(err)
	}
	if rg, ok := r.(ast.Grant); !ok || rg.Role != "analyst" || rg.Grantee != "bob" {
		t.Fatalf("%+v", r)
	}
	if _, err := Parse(`GRANT ADMIN ON CLUSTER TO dba`); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(`REVOKE SELECT ON products FROM analyst`); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(`DROP USER app`); err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(`CREATE ROLE analyst`); err != nil {
		t.Fatal(err)
	}
}

func TestParsePointAndSpatialIndex(t *testing.T) {
	stmt, err := Parse(`CREATE TABLE places (id UUID PRIMARY KEY, loc POINT, area BOX, route LINESTRING, zone POLYGON, name STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	ct := stmt.(ast.CreateTable)
	if ct.Columns[1].Type.Kind != types.KindPoint || ct.Columns[2].Type.Kind != types.KindBox ||
		ct.Columns[3].Type.Kind != types.KindLine || ct.Columns[4].Type.Kind != types.KindPolygon {
		t.Fatalf("%+v", ct.Columns)
	}
	stmt, err = Parse(`CREATE TABLE t (id UUID PRIMARY KEY, loc LOCATION)`)
	if err != nil {
		t.Fatal(err)
	}
	if stmt.(ast.CreateTable).Columns[1].Type.Kind != types.KindPoint {
		t.Fatal("LOCATION should be POINT")
	}
	stmt, err = Parse(`CREATE SPATIAL INDEX ix ON places (loc)`)
	if err != nil {
		t.Fatal(err)
	}
	ix := stmt.(ast.CreateIndex)
	if !ix.Spatial || ix.Name != "ix" || ix.Cols[0] != "loc" {
		t.Fatalf("%+v", ix)
	}
	stmt, err = Parse(`CREATE INDEX category_index ON products (metadata.category)`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	if ix.Spatial || ix.Fulltext || len(ix.Keys) != 1 || len(ix.Keys[0]) != 2 || ix.Keys[0][0] != "metadata" || ix.Keys[0][1] != "category" {
		t.Fatalf("path index %+v", ix)
	}
	stmt, err = Parse(`CREATE FULLTEXT INDEX ix_body ON articles (body)`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	if !ix.Fulltext || ix.Spatial || ix.Name != "ix_body" || ix.Cols[0] != "body" {
		t.Fatalf("fulltext %+v", ix)
	}
	stmt, err = Parse(`SELECT id FROM articles SEARCH body FOR 'database performance' LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	sel := stmt.(ast.Select)
	if sel.SearchCol != "body" {
		t.Fatalf("search col %q", sel.SearchCol)
	}
	lit, ok := sel.SearchQuery.(ast.Literal)
	if !ok || lit.Value.Str != "database performance" {
		t.Fatalf("search query %+v", sel.SearchQuery)
	}
	stmt, err = Parse(`CREATE VECTOR INDEX docs_embedding ON documents(embedding) USING HNSW`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	if !ix.Vector || ix.Using != "hnsw" || ix.Name != "docs_embedding" || ix.Cols[0] != "embedding" {
		t.Fatalf("vector index %+v", ix)
	}
	stmt, err = Parse(`CREATE INDEX ix_cover ON products (name) INCLUDE (price, note)`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	if ix.Name != "ix_cover" || len(ix.Include) != 2 || ix.Include[0] != "price" || ix.Include[1] != "note" {
		t.Fatalf("include %+v", ix)
	}
	stmt, err = Parse(`CREATE INDEX ix_active ON products (name) WHERE status = 'active'`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	bin, ok := ix.Where.(ast.Binary)
	if !ok || bin.Op != "=" {
		t.Fatalf("partial where %+v", ix.Where)
	}
	stmt, err = Parse(`CREATE INDEX ix_lower ON products (LOWER(name), sku)`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	if len(ix.Exprs) != 2 || ix.Exprs[0] == nil || ix.Exprs[1] != nil || ix.Cols[1] != "sku" {
		t.Fatalf("expression keys %+v exprs=%+v", ix, ix.Exprs)
	}
	call, ok := ix.Exprs[0].(ast.Call)
	if !ok || call.Name != "lower" || len(call.Args) != 1 {
		t.Fatalf("lower key %+v", ix.Exprs[0])
	}
	stmt, err = Parse(`CREATE UNIQUE INDEX ix_paren ON products ((LOWER(name))) INCLUDE (price) WHERE price > 0`)
	if err != nil {
		t.Fatal(err)
	}
	ix = stmt.(ast.CreateIndex)
	if !ix.Unique || ix.Where == nil || len(ix.Include) != 1 || ix.Exprs[0] == nil {
		t.Fatalf("paren expression index %+v", ix)
	}
	stmt, err = Parse(`SELECT id FROM products NEAREST embedding TO $query LIMIT 20`)
	if err != nil {
		t.Fatal(err)
	}
	sel = stmt.(ast.Select)
	if sel.NearestCol != "embedding" {
		t.Fatalf("nearest col %q", sel.NearestCol)
	}
	if _, ok := sel.NearestQuery.(ast.Param); !ok {
		t.Fatalf("nearest query %+v", sel.NearestQuery)
	}
	stmt, err = Parse(`SELECT id FROM t NEAREST emb TO (1, 0) USING L2 LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	sel = stmt.(ast.Select)
	if sel.NearestMetric != "l2" {
		t.Fatalf("metric %q", sel.NearestMetric)
	}
	stmt, err = Parse(`SELECT name FROM places WHERE DWITHIN(loc, POINT(-73.98, 40.75), 1000)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stmt.(ast.Select).Where.(ast.Call); !ok {
		t.Fatalf("%T", stmt.(ast.Select).Where)
	}
	stmt, err = Parse(`SELECT name FROM places WHERE WITHIN(loc, POLYGON('POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40))'))`)
	if err != nil {
		t.Fatal(err)
	}
	stmt, err = Parse(`SELECT DISTANCE_SPHEROID(a, b), ST_Length(route) FROM places`)
	if err != nil {
		t.Fatal(err)
	}
}

func TestParseJSONPathAndVectorLit(t *testing.T) {
	stmt, err := Parse(`SELECT * FROM t WHERE metadata.category = 'electronics'`)
	if err != nil {
		t.Fatal(err)
	}
	s := stmt.(ast.Select)
	bin := s.Where.(ast.Binary)
	if _, ok := bin.Left.(ast.Path); !ok {
		t.Fatalf("want path, got %T", bin.Left)
	}
	stmt, err = Parse(`INSERT INTO t (embedding) VALUES ((1.0, 2.5, -3))`)
	if err != nil {
		t.Fatal(err)
	}
	ins := stmt.(ast.Insert)
	if _, ok := ins.Rows[0][0].(ast.VectorLit); !ok {
		t.Fatalf("want vector, got %T", ins.Rows[0][0])
	}
}

func TestParseErrors(t *testing.T) {
	for _, src := range []string{"", "FOO", "SELECT FROM t", "CREATE TABLE t", "INSERT t VALUES (1)"} {
		_, err := Parse(src)
		if !nerr.HasCode(err, nerr.Syntax) {
			t.Fatalf("%q: %v", src, err)
		}
	}
}

func TestParseForeignKeyTableConstraint(t *testing.T) {
	src := `
CREATE TABLE orders (
    id          UUID PRIMARY KEY DEFAULT UUID(),
    tenant_id   UUID NOT NULL,
    customer_id UUID NOT NULL,
    CONSTRAINT fk_orders_customer
        FOREIGN KEY (tenant_id, customer_id)
        REFERENCES customers (tenant_id, id)
        ON DELETE RESTRICT
        ON UPDATE RESTRICT
)`
	stmt, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	ct := stmt.(ast.CreateTable)
	if len(ct.FKs) != 1 {
		t.Fatalf("%+v", ct.FKs)
	}
	fk := ct.FKs[0]
	if fk.Name != "fk_orders_customer" || fk.RefTable != "customers" {
		t.Fatalf("%+v", fk)
	}
	if len(fk.Columns) != 2 || fk.Columns[0] != "tenant_id" || fk.Columns[1] != "customer_id" {
		t.Fatalf("%+v", fk.Columns)
	}
	if len(fk.RefCols) != 2 || fk.RefCols[0] != "tenant_id" || fk.RefCols[1] != "id" {
		t.Fatalf("%+v", fk.RefCols)
	}
	if fk.OnDelete != ast.FKRestrict || fk.OnUpdate != ast.FKRestrict {
		t.Fatalf("actions %v %v", fk.OnDelete, fk.OnUpdate)
	}
}

func TestParseForeignKeyColumnShorthand(t *testing.T) {
	src := `CREATE TABLE lines (
    id UUID PRIMARY KEY,
    order_id UUID NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    sku STRING NOT NULL
)`
	stmt, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	ct := stmt.(ast.CreateTable)
	ref := ct.Columns[1].References
	if ref == nil || ref.RefTable != "orders" || len(ref.Columns) != 1 || ref.Columns[0] != "order_id" {
		t.Fatalf("%+v", ct.Columns[1])
	}
	if ref.OnDelete != ast.FKCascade || ref.OnUpdate != ast.FKRestrict {
		t.Fatalf("actions %+v", ref)
	}
}

func TestParseForeignKeyNoActionAndMatch(t *testing.T) {
	src := `CREATE TABLE t (
    id UUID PRIMARY KEY,
    p UUID REFERENCES parent (id) MATCH SIMPLE ON DELETE NO ACTION ON UPDATE SET DEFAULT
)`
	stmt, err := Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	ref := stmt.(ast.CreateTable).Columns[1].References
	if ref == nil || ref.OnDelete != ast.FKRestrict || ref.OnUpdate != ast.FKSetDefault {
		t.Fatalf("%+v", ref)
	}
	if _, err := Parse(`CREATE TABLE t (id UUID PRIMARY KEY, p UUID REFERENCES parent (id) MATCH FULL)`); err == nil {
		t.Fatal("expected MATCH FULL error")
	}
	if _, err := Parse(`CREATE TABLE t (foreign UUID PRIMARY KEY)`); err == nil {
		t.Fatal("expected reserved keyword error")
	}
}

func TestParseComments(t *testing.T) {
	stmt, err := Parse("-- hi\nSELECT * FROM t /* x */ WHERE a = 1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stmt.(ast.Select); !ok {
		t.Fatalf("%T", stmt)
	}
}

func TestParseUpsertAndReturning(t *testing.T) {
	stmt, err := Parse(`UPSERT INTO t (email, name) VALUES ('a@b', 'n') ON UNIQUE (email) SET name = excluded.name RETURNING email, name`)
	if err != nil {
		t.Fatal(err)
	}
	u := stmt.(ast.Upsert)
	if u.Table != "t" || len(u.OnUnique) != 1 || u.OnUnique[0] != "email" || len(u.Sets) != 1 || len(u.Returning) != 2 {
		t.Fatalf("%+v", u)
	}
	stmt, err = Parse(`INSERT INTO t (a) VALUES (1) RETURNING *`)
	if err != nil {
		t.Fatal(err)
	}
	ins := stmt.(ast.Insert)
	if !ins.ReturningStar || len(ins.Returning) != 0 {
		t.Fatalf("%+v", ins)
	}
	stmt, err = Parse(`UPDATE t SET n = 1 RETURNING n AS x`)
	if err != nil {
		t.Fatal(err)
	}
	up := stmt.(ast.Update)
	if len(up.Returning) != 1 || up.Returning[0].Alias != "x" {
		t.Fatalf("%+v", up)
	}
	stmt, err = Parse(`DELETE FROM t WHERE n = 1 RETURNING *`)
	if err != nil {
		t.Fatal(err)
	}
	if !stmt.(ast.Delete).ReturningStar {
		t.Fatalf("%+v", stmt)
	}
	if _, err := Parse(`UPSERT INTO t (a) VALUES (1) ON CONFLICT`); err == nil {
		t.Fatal("expected ON CONFLICT to fail")
	}
}

func TestParseWorkflowStatements(t *testing.T) {
	stmt, err := Parse(`CREATE WORKFLOW IF NOT EXISTS fulfill_order(order_id UUID, note TEXT) AS BEGIN
		UPDATE orders SET status = 'processing' WHERE id = $order_id;
		INSERT INTO events (id, note) VALUES (UUID(), $note);
		RUN WORKFLOW audit_order($order_id);
	END`)
	if err != nil {
		t.Fatal(err)
	}
	create, ok := stmt.(ast.CreateWorkflow)
	if !ok {
		t.Fatalf("%T", stmt)
	}
	if create.Name != "fulfill_order" || !create.IfNotExists || len(create.Params) != 2 || len(create.Body) != 3 {
		t.Fatalf("%+v", create)
	}
	if create.Params[0].Name != "order_id" || create.Params[0].Type.Kind != types.KindUUID {
		t.Fatalf("%+v", create.Params[0])
	}
	if _, ok := create.Body[2].(ast.RunWorkflow); !ok {
		t.Fatalf("nested statement %T", create.Body[2])
	}

	stmt, err = Parse(`RUN WORKFLOW fulfill_order($1, 'ready')`)
	if err != nil {
		t.Fatal(err)
	}
	run := stmt.(ast.RunWorkflow)
	if run.Name != "fulfill_order" || len(run.Args) != 2 {
		t.Fatalf("%+v", run)
	}
	stmt, err = Parse(`RUN WORKFLOW no_args()`)
	if err != nil || len(stmt.(ast.RunWorkflow).Args) != 0 {
		t.Fatalf("zero args: %T %v", stmt, err)
	}

	stmt, err = Parse(`ALTER WORKFLOW fulfill_order RENAME TO process_order`)
	if err != nil {
		t.Fatal(err)
	}
	rename := stmt.(ast.AlterWorkflow)
	if rename.Name != "fulfill_order" || rename.NewName != "process_order" {
		t.Fatalf("%+v", rename)
	}
	stmt, err = Parse(`DROP WORKFLOW IF EXISTS process_order`)
	if err != nil {
		t.Fatal(err)
	}
	drop := stmt.(ast.DropWorkflow)
	if drop.Name != "process_order" || !drop.IfExists {
		t.Fatalf("%+v", drop)
	}
}

func TestParseWorkflowRejectsUnsafeBodiesAndMalformedLimits(t *testing.T) {
	cases := []string{
		`CREATE WORKFLOW empty() AS BEGIN END`,
		`CREATE WORKFLOW duplicate(x UUID, x TEXT) AS BEGIN DELETE FROM t END`,
		`CREATE WORKFLOW query() AS BEGIN SELECT * FROM t; END`,
		`CREATE WORKFLOW ddl() AS BEGIN DROP TABLE t; END`,
		`CREATE WORKFLOW txn() AS BEGIN COMMIT; END`,
		`CREATE WORKFLOW rows() AS BEGIN DELETE FROM t RETURNING *; END`,
		`CREATE WORKFLOW undeclared(id UUID) AS BEGIN DELETE FROM t WHERE id = $missing; END`,
		`CREATE WORKFLOW positional(id UUID) AS BEGIN DELETE FROM t WHERE id = $1; END`,
		`CREATE WORKFLOW missing_separator() AS BEGIN DELETE FROM t UPDATE t SET n = 1 END`,
		`RUN WORKFLOW f(1,)`,
	}
	for _, src := range cases {
		if _, err := Parse(src); err == nil {
			t.Fatalf("expected rejection: %s", src)
		}
	}
}

func TestParseTriggerStatements(t *testing.T) {
	tests := []struct {
		sql    string
		timing ast.TriggerTiming
		event  ast.TriggerEvent
		args   int
	}{
		{`CREATE TRIGGER audit_insert AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW audit_order(NEW.id, 'insert')`, ast.TriggerAfter, ast.TriggerInsert, 2},
		{`CREATE TRIGGER guard_update BEFORE UPDATE ON orders FOR EACH ROW RUN WORKFLOW guard_change(OLD.status, NEW.status)`, ast.TriggerBefore, ast.TriggerUpdate, 2},
		{`CREATE TRIGGER cleanup BEFORE DELETE ON orders FOR EACH ROW RUN WORKFLOW remove_refs(OLD.id)`, ast.TriggerBefore, ast.TriggerDelete, 1},
	}
	for _, tc := range tests {
		stmt, err := Parse(tc.sql)
		if err != nil {
			t.Fatalf("%s: %v", tc.sql, err)
		}
		got, ok := stmt.(ast.CreateTrigger)
		if !ok || got.Timing != tc.timing || got.Event != tc.event || len(got.Args) != tc.args || got.Table != "orders" {
			t.Fatalf("%s: %#v", tc.sql, stmt)
		}
	}
	stmt, err := Parse(`ALTER TRIGGER audit_insert RENAME TO audit_created`)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stmt.(ast.AlterTrigger); !ok {
		t.Fatalf("ALTER TRIGGER: %#v", stmt)
	}
	stmt, err = Parse(`DROP TRIGGER IF EXISTS audit_created`)
	if err != nil {
		t.Fatal(err)
	}
	if drop, ok := stmt.(ast.DropTrigger); !ok || !drop.IfExists {
		t.Fatalf("DROP TRIGGER: %#v", stmt)
	}
}

func TestParseTriggerRejectsInvalid(t *testing.T) {
	for _, sql := range []string{
		`CREATE TRIGGER t INSERT ON orders FOR EACH ROW RUN WORKFLOW w(NEW.id)`,
		`CREATE TRIGGER t AFTER SELECT ON orders FOR EACH ROW RUN WORKFLOW w(NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT orders FOR EACH ROW RUN WORKFLOW w(NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT ON orders RUN WORKFLOW w(NEW.id)`,
		`CREATE TRIGGER t AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW w(OLD.id)`,
		`CREATE TRIGGER t AFTER DELETE ON orders FOR EACH ROW RUN WORKFLOW w(NEW.id)`,
		`CREATE TRIGGER t AFTER UPDATE ON orders FOR EACH ROW RUN WORKFLOW w(id)`,
		`CREATE TRIGGER t AFTER UPDATE ON orders FOR EACH ROW RUN WORKFLOW w(OTHER.id)`,
		`CREATE TRIGGER t AFTER UPDATE ON orders FOR EACH ROW RUN WORKFLOW w(UUID())`,
		`CREATE TRIGGER t AFTER UPDATE ON orders FOR EACH ROW RUN WORKFLOW w(NEW.id,)`,
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatalf("accepted invalid trigger: %s", sql)
		}
	}
}

func TestParseScheduleStatements(t *testing.T) {
	stmt, err := Parse(`CREATE SCHEDULE IF NOT EXISTS hourly EVERY '1h' RUN WORKFLOW rollup('hour', -1)`)
	if err != nil {
		t.Fatal(err)
	}
	create, ok := stmt.(ast.CreateSchedule)
	if !ok || create.Name != "hourly" || create.Kind != ast.ScheduleEvery || create.Spec != "1h" || create.Workflow != "rollup" || len(create.Args) != 2 || !create.IfNotExists {
		t.Fatalf("schedule=%#v", stmt)
	}
	stmt, err = Parse(`CREATE SCHEDULE once AT '2026-08-25T00:00:00Z' RUN WORKFLOW rollup()`)
	if err != nil {
		t.Fatal(err)
	}
	if got := stmt.(ast.CreateSchedule); got.Kind != ast.ScheduleAt || got.Spec != "2026-08-25T00:00:00Z" {
		t.Fatalf("schedule=%#v", got)
	}
	if stmt, err = Parse(`ALTER SCHEDULE hourly RENAME TO every_hour`); err != nil {
		t.Fatal(err)
	} else if got := stmt.(ast.AlterSchedule); got.Name != "hourly" || got.NewName != "every_hour" {
		t.Fatalf("alter=%#v", got)
	}
	if stmt, err = Parse(`DROP SCHEDULE IF EXISTS every_hour`); err != nil {
		t.Fatal(err)
	} else if got := stmt.(ast.DropSchedule); got.Name != "every_hour" || !got.IfExists {
		t.Fatalf("drop=%#v", got)
	}
}

func TestParseTaskStatements(t *testing.T) {
	stmt, err := Parse(`SHOW TASKS AFTER 's/00000001/1' LIMIT 32`)
	if err != nil {
		t.Fatal(err)
	}
	show, ok := stmt.(ast.ShowTasks)
	if !ok || show.After != "s/00000001/1" || show.Limit != 32 {
		t.Fatalf("show=%+v ok=%v", stmt, ok)
	}
	stmt, err = Parse(`CANCEL TASK 's/00000001/1'`)
	if err != nil {
		t.Fatal(err)
	}
	cancel, ok := stmt.(ast.CancelTask)
	if !ok || cancel.ID != "s/00000001/1" {
		t.Fatalf("cancel=%+v ok=%v", stmt, ok)
	}
	for _, sql := range []string{`SHOW TASKS LIMIT 0`, `SHOW TASKS LIMIT 257`, `SHOW TASKS AFTER 1`, `CANCEL TASK ''`} {
		if _, err := Parse(sql); err == nil {
			t.Fatalf("accepted %q", sql)
		}
	}
}

func TestParseScheduleRejectsInvalid(t *testing.T) {
	for _, sql := range []string{
		`CREATE SCHEDULE s RUN WORKFLOW w()`,
		`CREATE SCHEDULE s EVERY 1h RUN WORKFLOW w()`,
		`CREATE SCHEDULE s EVERY '1h' RUN WORKFLOW w($1)`,
		`CREATE SCHEDULE s AT 'now' RUN WORKFLOW w(column_name)`,
		`CREATE SCHEDULE s EVERY '1h' RUN WORKFLOW w(1,)`,
	} {
		if _, err := Parse(sql); err == nil {
			t.Fatalf("accepted invalid schedule: %s", sql)
		}
	}
}
