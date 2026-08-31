package binder

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestBindAIDefault(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	ct := b.(CreateTable)
	if ct.Table.Columns[0].Default.Kind != catalog.DefAI {
		t.Fatalf("default %+v", ct.Table.Columns[0].Default)
	}
	bad, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY DEFAULT AI())`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(bad, func(string) (*catalog.Table, bool) { return nil, false }, 1); err == nil {
		t.Fatal("expected UUID DEFAULT AI() to fail")
	}
	scaled, err := parser.Parse(`CREATE TABLE t (id DECIMAL(10,2) PRIMARY KEY DEFAULT AI())`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(scaled, func(string) (*catalog.Table, bool) { return nil, false }, 1); err == nil {
		t.Fatal("expected DECIMAL scale DEFAULT AI() to fail")
	}
}

func TestBindWindow(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id STRING PRIMARY KEY, k STRING, v DECIMAL(10,0))`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "t" {
			return tab, true
		}
		return nil, false
	}
	sel, err := parser.Parse(`SELECT k, ROW_NUMBER() OVER (PARTITION BY k ORDER BY v) AS n FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	s := bs.(Select)
	if len(s.Windows) != 1 || s.Windows[0].Fun != "row_number" || s.Windows[0].Result == "" {
		t.Fatalf("bound window: %+v", s.Windows)
	}
	if _, ok := s.OutExprs[1].(ast.Ident); !ok {
		t.Fatalf("window should rewrite to ident: %#v", s.OutExprs[1])
	}
	bad, err := parser.Parse(`SELECT k FROM t WHERE ROW_NUMBER() OVER () = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(bad, lookup, 1); err == nil {
		t.Fatal("expected window in WHERE to fail")
	}
}

func TestBindCreateAndSelect(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	ct := b.(CreateTable)
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "t" {
			return ct.Table, true
		}
		return nil, false
	}
	sel, err := parser.Parse(`SELECT id, n FROM t WHERE n = 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs.(Select).OutNames) != 2 {
		t.Fatalf("%+v", bs)
	}
}

func TestBindWith(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := ct.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "t" {
			return tab, true
		}
		return nil, false
	}
	sel, err := parser.Parse(`WITH c AS (SELECT n FROM t) SELECT n FROM c`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	w := b.(With)
	if len(w.CTEs) != 1 || w.CTEs[0].Name != "c" || w.CTEs[0].Refs != 1 {
		t.Fatalf("bound WITH: %+v", w)
	}
	if _, err := BindMustParse(t, `WITH c AS (SELECT n FROM t), c AS (SELECT n FROM t) SELECT n FROM c`, lookup); err == nil {
		t.Fatal("duplicate CTE name must fail")
	}
	if _, err := BindMustParse(t, `WITH c AS (SELECT n FROM c) SELECT n FROM c`, lookup); err == nil {
		t.Fatal("self-reference without RECURSIVE must fail")
	}
	if _, err := BindMustParse(t, `WITH c(a, b) AS (SELECT n FROM t) SELECT a FROM c`, lookup); err == nil {
		t.Fatal("CTE column count mismatch must fail")
	}
	rec, err := parser.Parse(`WITH RECURSIVE w AS (SELECT id, n FROM t UNION ALL SELECT t.id, t.n FROM t JOIN w ON t.id = w.id) SELECT id FROM w`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(rec, lookup, 1); err != nil {
		t.Fatalf("recursive CTE bind: %v", err)
	}
}

func BindMustParse(t *testing.T, sql string, lookup Lookup) (Bound, error) {
	t.Helper()
	st, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	return Bind(st, lookup, 1)
}

func TestBindForeignKey(t *testing.T) {
	parentStmt, err := parser.Parse(`CREATE TABLE customers (
		tenant_id UUID NOT NULL,
		id UUID NOT NULL DEFAULT UUID(),
		email STRING NOT NULL,
		PRIMARY KEY (tenant_id, id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	parentBound, err := Bind(parentStmt, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	parent := parentBound.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "customers" {
			return parent, true
		}
		return nil, false
	}
	childStmt, err := parser.Parse(`CREATE TABLE orders (
		tenant_id UUID NOT NULL,
		id UUID NOT NULL DEFAULT UUID(),
		customer_id UUID NOT NULL,
		PRIMARY KEY (tenant_id, id),
		CONSTRAINT fk_orders_customer
			FOREIGN KEY (tenant_id, customer_id)
			REFERENCES customers (tenant_id, id)
			ON DELETE RESTRICT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(childStmt, lookup, 2)
	if err != nil {
		t.Fatal(err)
	}
	ct := b.(CreateTable)
	if len(ct.Table.ForeignKeys) != 1 || ct.Table.ForeignKeys[0].RefTableID != parent.ID {
		t.Fatalf("%+v", ct.Table.ForeignKeys)
	}

	missing, err := parser.Parse(`CREATE TABLE orders (
		id UUID PRIMARY KEY,
		customer_id UUID NOT NULL REFERENCES nope (id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(missing, lookup, 3); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("missing parent: %v", err)
	}

	vector, err := parser.Parse(`CREATE TABLE t (
		id UUID PRIMARY KEY,
		emb VECTOR<F32,4> REFERENCES customers (id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(vector, lookup, 4); err == nil {
		t.Fatal("expected VECTOR fk error")
	}

	keysStmt, err := parser.Parse(`CREATE TABLE keys (id UUID PRIMARY KEY)`)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := Bind(keysStmt, func(string) (*catalog.Table, bool) { return nil, false }, 5)
	if err != nil {
		t.Fatal(err)
	}
	keys := kb.(CreateTable).Table
	lookupKeys := func(name string) (*catalog.Table, bool) {
		if name == "keys" {
			return keys, true
		}
		return lookup(name)
	}
	setNull, err := parser.Parse(`CREATE TABLE t (
		id UUID PRIMARY KEY,
		customer_id UUID NOT NULL REFERENCES keys (id) ON DELETE SET NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if err := bindErr(setNull, lookupKeys, 6); err == nil || !strings.Contains(err.Error(), "SET NULL") {
		t.Fatalf("expected SET NULL error, got %v", err)
	}
	okNull, err := parser.Parse(`CREATE TABLE t2 (
		id UUID PRIMARY KEY,
		customer_id UUID REFERENCES keys (id) ON DELETE SET NULL
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(okNull, lookupKeys, 7); err != nil {
		t.Fatal(err)
	}

	self, err := parser.Parse(`CREATE TABLE org (
		id UUID PRIMARY KEY,
		parent_id UUID REFERENCES org (id) ON DELETE RESTRICT
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(self, func(string) (*catalog.Table, bool) { return nil, false }, 8); err != nil {
		t.Fatal(err)
	}
}

func bindErr(stmt ast.Stmt, lookup Lookup, nextID uint32) error {
	_, err := Bind(stmt, lookup, nextID)
	return err
}

func TestBindForeignKeyCascadeCycle(t *testing.T) {
	bOnly, err := parser.Parse(`CREATE TABLE b (id UUID PRIMARY KEY)`)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := Bind(bOnly, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	bTab := bb.(CreateTable).Table
	aStmt, err := parser.Parse(`CREATE TABLE a (id UUID PRIMARY KEY, b_id UUID REFERENCES b (id) ON DELETE CASCADE)`)
	if err != nil {
		t.Fatal(err)
	}
	ab, err := Bind(aStmt, func(name string) (*catalog.Table, bool) {
		if name == "b" {
			return bTab, true
		}
		return nil, false
	}, 2)
	if err != nil {
		t.Fatal(err)
	}
	aTab := ab.(CreateTable).Table
	// a CASCADE-> b plus c CASCADE-> a is a line, not a cycle.
	cStmt, err := parser.Parse(`CREATE TABLE c (id UUID PRIMARY KEY, a_id UUID REFERENCES a (id) ON DELETE CASCADE)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(cStmt, func(name string) (*catalog.Table, bool) {
		if name == "a" {
			return aTab, true
		}
		return nil, false
	}, 3); err != nil {
		t.Fatal(err)
	}
	// a CASCADE-> y plus y CASCADE-> a is a cycle.
	aCyc := aTab.Clone()
	aCyc.ForeignKeys[0].RefTable = "y"
	aCyc.ForeignKeys[0].OnDelete = catalog.FKCascade
	yStmt, err := parser.Parse(`CREATE TABLE y (id UUID PRIMARY KEY, a_id UUID REFERENCES a (id) ON DELETE CASCADE)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(yStmt, func(name string) (*catalog.Table, bool) {
		if name == "a" {
			return aCyc, true
		}
		return nil, false
	}, 4); err == nil {
		t.Fatal("expected CASCADE cycle")
	}
}

func TestBindForeignKeyTenantIDIsNotSpecial(t *testing.T) {
	parentStmt, err := parser.Parse(`CREATE TABLE customers (
		tenant_id UUID NOT NULL,
		id UUID PRIMARY KEY
	)`)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Bind(parentStmt, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	parent := pb.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "customers" {
			return parent, true
		}
		return nil, false
	}
	// A legacy marker name no longer changes foreign-key validation.
	global, err := parser.Parse(`CREATE TABLE notes (id UUID PRIMARY KEY, customer_id UUID REFERENCES customers (id))`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(global, lookup, 2); err != nil {
		t.Fatal(err)
	}
	// An ordinary FK does not need to include the legacy marker column.
	both, err := parser.Parse(`CREATE TABLE orders (
		tenant_id UUID NOT NULL,
		id UUID PRIMARY KEY,
		customer_id UUID NOT NULL REFERENCES customers (id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(both, lookup, 3); err != nil {
		t.Fatal(err)
	}

	acctStmt, err := parser.Parse(`CREATE TABLE accounts (
		tenant_id UUID NOT NULL,
		id UUID NOT NULL,
		PRIMARY KEY (tenant_id, id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	ab, err := Bind(acctStmt, func(string) (*catalog.Table, bool) { return nil, false }, 4)
	if err != nil {
		t.Fatal(err)
	}
	accounts := ab.(CreateTable).Table
	lookupAcct := func(name string) (*catalog.Table, bool) {
		if name == "accounts" {
			return accounts, true
		}
		return nil, false
	}
	// Column order follows the declared FK mapping without a hidden pairing rule.
	mispair, err := parser.Parse(`CREATE TABLE orders (
		tenant_id UUID NOT NULL,
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		FOREIGN KEY (account_id, tenant_id) REFERENCES accounts (tenant_id, id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(mispair, lookupAcct, 5); err != nil {
		t.Fatal(err)
	}
	// same set, tenant columns aligned
	aligned, err := parser.Parse(`CREATE TABLE orders (
		tenant_id UUID NOT NULL,
		id UUID PRIMARY KEY,
		account_id UUID NOT NULL,
		FOREIGN KEY (account_id, tenant_id) REFERENCES accounts (id, tenant_id)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(aligned, lookupAcct, 6); err != nil {
		t.Fatal(err)
	}
}

func TestBindUnknownTable(t *testing.T) {
	st, err := parser.Parse(`SELECT * FROM missing`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("%v", err)
	}
}

func TestBindFulltextIndexRequiresText(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, n DECIMAL(4,0))`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	ix, err := parser.Parse(`CREATE FULLTEXT INDEX ix ON t (n)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(ix, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected fulltext type error")
	}
}

func TestBindHighlightRequiresSearch(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, body TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	ok, err := parser.Parse(`SELECT HIGHLIGHT(body) FROM t SEARCH body FOR 'cat'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(ok, func(string) (*catalog.Table, bool) { return tab, true }, 1); err != nil {
		t.Fatal(err)
	}
	snip, err := parser.Parse(`SELECT SNIPPET(body, 64) FROM t SEARCH body FOR 'cat'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(snip, func(string) (*catalog.Table, bool) { return tab, true }, 1); err != nil {
		t.Fatal(err)
	}
	noSearch, err := parser.Parse(`SELECT HIGHLIGHT(body) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(noSearch, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected HIGHLIGHT without SEARCH to fail")
	}
	inWhere, err := parser.Parse(`SELECT id FROM t WHERE HIGHLIGHT(body) = 'x' SEARCH body FOR 'cat'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(inWhere, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected HIGHLIGHT in WHERE to fail")
	}
}

func TestBindSearch(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, body TEXT)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	sel, err := parser.Parse(`SELECT id FROM t SEARCH body FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs.(Select).SearchCols) != 1 || bs.(Select).SearchCols[0] != 1 {
		t.Fatalf("%+v", bs)
	}
	ix, err := parser.Parse(`CREATE FULLTEXT INDEX ix ON t (body)`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(ix, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bi.(CreateIndex).Index.Fulltext {
		t.Fatal("expected fulltext")
	}
	if bi.(CreateIndex).Index.FTAnalyzer != 0 {
		t.Fatal("default analyzer must be simple")
	}
	en, err := parser.Parse(`CREATE FULLTEXT INDEX ix_en ON t (body) WITH (ANALYZER = 'english')`)
	if err != nil {
		t.Fatal(err)
	}
	be, err := Bind(en, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := be.(CreateIndex).Index; idx.FTAnalyzer != catalog.FTAnalyzerEnglish || idx.FTVersion != catalog.FTAnalyzerEnglishV3 {
		t.Fatalf("english analyzer %+v", idx)
	}
	sm, err := parser.Parse(`CREATE FULLTEXT INDEX ix_s ON t (body) WITH (ANALYZER = 'simple')`)
	if err != nil {
		t.Fatal(err)
	}
	bsm, err := Bind(sm, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bsm.(CreateIndex).Index.FTAnalyzer != catalog.FTAnalyzerSimple {
		t.Fatal("simple analyzer")
	}
	bad, err := parser.Parse(`CREATE FULLTEXT INDEX ix_bad ON t (body) WITH (ANALYZER = 'klingon')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(bad, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected unknown analyzer")
	}
	fr, err := parser.Parse(`CREATE FULLTEXT INDEX ix_fr ON t (body) WITH (ANALYZER = 'french')`)
	if err != nil {
		t.Fatal(err)
	}
	bfr, err := Bind(fr, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := bfr.(CreateIndex).Index; idx.FTAnalyzer != catalog.FTAnalyzerFrench || idx.FTVersion != catalog.FTAnalyzerFrenchV1 {
		t.Fatalf("french analyzer %+v", idx)
	}
	de, err := parser.Parse(`CREATE FULLTEXT INDEX ix_de ON t (body) WITH (ANALYZER = 'german')`)
	if err != nil {
		t.Fatal(err)
	}
	bde, err := Bind(de, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := bde.(CreateIndex).Index; idx.FTAnalyzer != catalog.FTAnalyzerGerman {
		t.Fatalf("german analyzer %+v", idx)
	}
	es, err := parser.Parse(`CREATE FULLTEXT INDEX ix_es ON t (body) WITH (ANALYZER = 'spanish')`)
	if err != nil {
		t.Fatal(err)
	}
	bes, err := Bind(es, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := bes.(CreateIndex).Index; idx.FTAnalyzer != catalog.FTAnalyzerSpanish {
		t.Fatalf("spanish analyzer %+v", idx)
	}
}

func TestBindFulltextMultiField(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, title STRING, body TEXT, n DECIMAL(4,0))`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	ix, err := parser.Parse(`CREATE FULLTEXT INDEX ix ON t (title, body)`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(ix, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bi.(CreateIndex).Index
	if !got.Fulltext || len(got.Columns) != 2 || got.Columns[0] != 1 || got.Columns[1] != 2 {
		t.Fatalf("multi-field index %+v", got)
	}
	sel, err := parser.Parse(`SELECT id FROM t SEARCH title, body FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if cols := bs.(Select).SearchCols; len(cols) != 2 || cols[0] != 1 || cols[1] != 2 {
		t.Fatalf("search cols %v", cols)
	}
	if bs.(Select).SearchWeights != nil {
		t.Fatalf("unweighted %v", bs.(Select).SearchWeights)
	}
	wsel, err := parser.Parse(`SELECT id FROM t SEARCH title WEIGHT 3, body FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bsw, err := Bind(wsel, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if w := bsw.(Select).SearchWeights; len(w) != 2 || w[0] != 3 || w[1] != 1 {
		t.Fatalf("search weights %v", w)
	}
	one, err := parser.Parse(`SELECT id FROM t SEARCH title WEIGHT 1 FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bo, err := Bind(one, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bo.(Select).SearchWeights != nil {
		t.Fatalf("WEIGHT 1 is a no-op: %v", bo.(Select).SearchWeights)
	}
	dup, err := parser.Parse(`CREATE FULLTEXT INDEX ix_dup ON t (title, title)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(dup, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected duplicate FULLTEXT column")
	}
	mixed, err := parser.Parse(`CREATE FULLTEXT INDEX ix_mix ON t (title, n)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(mixed, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected non-text FULLTEXT column")
	}
	dupSearch, err := parser.Parse(`SELECT id FROM t SEARCH title, title FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(dupSearch, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected duplicate SEARCH column")
	}
}

func TestBindFulltextFacet(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, title STRING, body TEXT, category STRING, n DECIMAL(4,0), payload JSON)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	ok, err := parser.Parse(`SELECT * FROM t SEARCH body FOR 'x' FACET category`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(ok, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bs.(Select)
	if len(got.FacetCols) != 1 || got.FacetCols[0] != 3 {
		t.Fatalf("facet cols %v", got.FacetCols)
	}
	if len(got.OutNames) != 3 || got.OutNames[0] != "facet" || got.OutNames[1] != "value" || got.OutNames[2] != "count" {
		t.Fatalf("facet output %v", got.OutNames)
	}
	num, err := parser.Parse(`SELECT * FROM t SEARCH body FOR 'x' FACET n`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(num, func(string) (*catalog.Table, bool) { return tab, true }, 1); err != nil {
		t.Fatalf("DECIMAL facet: %v", err)
	}
	for _, q := range []string{
		`SELECT title FROM t SEARCH body FOR 'x' FACET category`,
		`SELECT * FROM t WHERE true FACET category`,
		`SELECT * FROM t SEARCH body FOR 'x' FACET title, title`,
		`SELECT * FROM t SEARCH body FOR 'x' FACET payload`,
		`SELECT * FROM t SEARCH body FOR 'x' FACET category ORDER BY count`,
		`SELECT DISTINCT * FROM t SEARCH body FOR 'x' FACET category`,
		`SELECT * FROM t SEARCH body FOR 'x' FACET category OFFSET 1`,
	} {
		stmt, err := parser.Parse(q)
		if err != nil {
			t.Fatalf("parse %s: %v", q, err)
		}
		if _, err := Bind(stmt, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
			t.Fatalf("expected fail-closed: %s", q)
		}
	}
}

func TestBindVectorIndexAndNearest(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, emb VECTOR<F32,3>, n STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	bad, err := parser.Parse(`CREATE VECTOR INDEX ix ON t (n) USING HNSW`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(bad, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected vector type error")
	}
	ix, err := parser.Parse(`CREATE VECTOR INDEX ix ON t (emb) USING HNSW`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(ix, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bi.(CreateIndex).Index.Vector {
		t.Fatal("expected vector index")
	}
	if bi.(CreateIndex).Index.VecQuant != 0 {
		t.Fatalf("default quantisation = %d, want 0", bi.(CreateIndex).Index.VecQuant)
	}
	qix, err := parser.Parse(`CREATE VECTOR INDEX ixq ON t (emb) USING HNSW WITH (QUANTIZATION = 'I8')`)
	if err != nil {
		t.Fatal(err)
	}
	qbi, err := Bind(qix, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if qbi.(CreateIndex).Index.VecQuant != types.VecI8 {
		t.Fatalf("quantised index VecQuant = %d, want %d", qbi.(CreateIndex).Index.VecQuant, types.VecI8)
	}
	badQ, err := parser.Parse(`CREATE VECTOR INDEX ixb ON t (emb) USING HNSW WITH (QUANTIZATION = 'f64')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(badQ, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected unknown quantisation to be rejected")
	}
	sel, err := parser.Parse(`SELECT id FROM t NEAREST emb TO (1, 0, 0) LIMIT 3`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bs.(Select).NearestCol != 1 {
		t.Fatalf("%+v", bs)
	}

	// IVF method: LISTS is required, PROBES cannot exceed it, and the params
	// round-trip onto the catalog index.
	ivf, err := parser.Parse(`CREATE VECTOR INDEX ivx ON t (emb) USING IVF WITH (LISTS = 16, PROBES = 4)`)
	if err != nil {
		t.Fatal(err)
	}
	ivbi, err := Bind(ivf, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := ivbi.(CreateIndex).Index; idx.VecMethod != catalog.VecMethodIVF || idx.IVFLists != 16 || idx.IVFProbes != 4 {
		t.Fatalf("ivf index %+v", idx)
	}
	noLists, _ := parser.Parse(`CREATE VECTOR INDEX ivx ON t (emb) USING IVF`)
	if _, err := Bind(noLists, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected USING IVF without LISTS to be rejected")
	}
	tooMany, _ := parser.Parse(`CREATE VECTOR INDEX ivx ON t (emb) USING IVF WITH (LISTS = 4, PROBES = 9)`)
	if _, err := Bind(tooMany, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected PROBES > LISTS to be rejected")
	}

	// IVF-PQ method: SUBSPACES is required and must divide the vector dimension.
	pq, _ := parser.Parse(`CREATE VECTOR INDEX pqx ON t (emb) USING IVFPQ WITH (LISTS = 8, PROBES = 2, SUBSPACES = 3)`)
	pqbi, err := Bind(pq, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := pqbi.(CreateIndex).Index; idx.VecMethod != catalog.VecMethodIVFPQ || idx.IVFLists != 8 || idx.IVFProbes != 2 || idx.IVFSubspaces != 3 {
		t.Fatalf("ivfpq index %+v", idx)
	}
	noSub, _ := parser.Parse(`CREATE VECTOR INDEX pqx ON t (emb) USING IVFPQ WITH (LISTS = 8)`)
	if _, err := Bind(noSub, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected USING IVFPQ without SUBSPACES to be rejected")
	}
	badSub, _ := parser.Parse(`CREATE VECTOR INDEX pqx ON t (emb) USING IVFPQ WITH (LISTS = 8, SUBSPACES = 2)`)
	if _, err := Bind(badSub, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected SUBSPACES not dividing the dimension to be rejected")
	}
	subOnIVF, _ := parser.Parse(`CREATE VECTOR INDEX pqx ON t (emb) USING IVF WITH (LISTS = 8, SUBSPACES = 3)`)
	if _, err := Bind(subOnIVF, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected SUBSPACES on USING IVF to be rejected")
	}

	// A centroid at a very high dimension does not fit one B+Tree record, so IVF
	// is rejected up front (HNSW on the same column is fine).
	wideDDL, err := parser.Parse(`CREATE TABLE w (id UUID PRIMARY KEY, emb VECTOR<F32,4096>)`)
	if err != nil {
		t.Fatal(err)
	}
	wb, err := Bind(wideDDL, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	wtab := wb.(CreateTable).Table
	wideIVF, _ := parser.Parse(`CREATE VECTOR INDEX wx ON w (emb) USING IVF WITH (LISTS = 8)`)
	if _, err := Bind(wideIVF, func(string) (*catalog.Table, bool) { return wtab, true }, 1); err == nil {
		t.Fatal("expected IVF at dimension 4096 to be rejected")
	}
	wideHNSW, _ := parser.Parse(`CREATE VECTOR INDEX wx ON w (emb) USING HNSW`)
	if _, err := Bind(wideHNSW, func(string) (*catalog.Table, bool) { return wtab, true }, 1); err != nil {
		t.Fatalf("HNSW at dimension 4096 should be allowed: %v", err)
	}
}

func TestBindBitvector(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, sig BITVECTOR<16>, emb VECTOR<F32,4>)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	if tab.Columns[1].Type.VecElem != types.VecBit || tab.Columns[1].Type.String() != "BITVECTOR<16>" {
		t.Fatalf("bitvector column type: %+v", tab.Columns[1].Type)
	}
	lookup := func(string) (*catalog.Table, bool) { return tab, true }

	// HAMMING requires a BITVECTOR column; a BITVECTOR requires HAMMING.
	mustBindErr(t, lookup, `SELECT id FROM t NEAREST emb TO (1,0,0,0) USING HAMMING LIMIT 1`)
	mustBindErr(t, lookup, `SELECT id FROM t NEAREST sig TO (1,0,1,0,1,0,1,0,1,0,1,0,1,0,1,0) USING COSINE LIMIT 1`)

	// Default (no USING) and explicit HAMMING both bind on a BITVECTOR column.
	for _, q := range []string{
		`SELECT id FROM t NEAREST sig TO (1,0,1,0,1,0,1,0,1,0,1,0,1,0,1,0) LIMIT 1`,
		`SELECT id FROM t NEAREST sig TO (1,0,1,0,1,0,1,0,1,0,1,0,1,0,1,0) USING HAMMING LIMIT 1`,
	} {
		ps, err := parser.Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Bind(ps, lookup, 1); err != nil {
			t.Fatalf("bind %q: %v", q, err)
		}
	}

	// QUANTIZATION on a BITVECTOR index is rejected.
	mustBindErr(t, lookup, `CREATE VECTOR INDEX ix ON t (sig) USING HNSW WITH (QUANTIZATION = 'I8')`)
}

func TestBindSparsevector(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, emb SPARSEVECTOR<64>, dense VECTOR<F32,4>)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	if tab.Columns[1].Type.VecElem != types.VecSparse || tab.Columns[1].Type.String() != "SPARSEVECTOR<64>" {
		t.Fatalf("sparse column type: %+v", tab.Columns[1].Type)
	}
	lookup := func(string) (*catalog.Table, bool) { return tab, true }

	sp, err := parser.Parse(`CREATE VECTOR INDEX ix ON t (emb) USING SPARSE`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(sp, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx := bi.(CreateIndex).Index; idx.VecMethod != catalog.VecMethodSPARSE || !idx.Vector {
		t.Fatalf("sparse index %+v", idx)
	}

	mustBindErr(t, lookup, `CREATE VECTOR INDEX ix ON t (emb) USING HNSW`)
	mustBindErr(t, lookup, `CREATE VECTOR INDEX ix ON t (emb) USING IVF WITH (LISTS = 4)`)
	mustBindErr(t, lookup, `CREATE VECTOR INDEX ix ON t (dense) USING SPARSE`)
	mustBindErr(t, lookup, `SELECT id FROM t NEAREST emb TO (1,0,0,0) USING L2 LIMIT 1`)
	mustBindErr(t, lookup, `SELECT id FROM t NEAREST emb TO (1,0,0,0) USING HAMMING LIMIT 1`)
	mustBindErr(t, lookup, `SELECT id FROM t NEAREST dense TO $d NEAREST dense TO $s LIMIT 1`)

	ps, err := parser.Parse(`SELECT id FROM t NEAREST dense TO $d NEAREST emb TO $s LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Bind(ps, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	sel := got.(Select)
	if sel.NearestCol != 2 || sel.Nearest2Col != 1 {
		t.Fatalf("fusion bind cols nearest=%d nearest2=%d", sel.NearestCol, sel.Nearest2Col)
	}

	for _, q := range []string{
		`SELECT id FROM t NEAREST emb TO $q LIMIT 1`,
		`SELECT id FROM t NEAREST emb TO $q USING COSINE LIMIT 1`,
		`SELECT id FROM t NEAREST emb TO $q USING INNER_PRODUCT LIMIT 1`,
		`SELECT id FROM t NEAREST dense TO $d NEAREST emb TO $s LIMIT 1`,
	} {
		ps, err := parser.Parse(q)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Bind(ps, lookup, 1); err != nil {
			t.Fatalf("bind %q: %v", q, err)
		}
	}
}

func mustBindErr(t *testing.T, lookup func(string) (*catalog.Table, bool), q string) {
	t.Helper()
	ps, err := parser.Parse(q)
	if err != nil {
		return // a parse error is also an acceptable rejection
	}
	if _, err := Bind(ps, lookup, 1); err == nil {
		t.Fatalf("expected %q to be rejected", q)
	}
}

func TestBindHybridSearchAndNearest(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, body TEXT, emb VECTOR<F32,3>, price DECIMAL(10,2))`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	sel, err := parser.Parse(`SELECT id FROM t WHERE price <= 10 SEARCH body FOR 'wireless' NEAREST emb TO (1, 0, 0) LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bs.(Select)
	if len(got.SearchCols) != 1 || got.SearchCols[0] != 1 || got.NearestCol != 2 || got.Where == nil {
		t.Fatalf("%+v", got)
	}
}

func TestBindSpatialIndexRequiresPoint(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, n STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	ix, err := parser.Parse(`CREATE SPATIAL INDEX ix ON t (n)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Bind(ix, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err == nil {
		t.Fatal("expected spatial type error")
	}
}

func TestBindMultiInnerJoin(t *testing.T) {
	tabs := map[string]*catalog.Table{}
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		st, err := parser.Parse(`CREATE TABLE ` + name + ` (id UUID PRIMARY KEY, k STRING)`)
		if err != nil {
			t.Fatal(err)
		}
		b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
		if err != nil {
			t.Fatal(err)
		}
		tabs[name] = b.(CreateTable).Table
	}
	lookup := func(name string) (*catalog.Table, bool) {
		t, ok := tabs[name]
		return t, ok
	}
	sel, err := parser.Parse(`SELECT a.k, b.k, c.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bs.(Select)
	if len(got.Joins) != 2 {
		t.Fatalf("joins %d", len(got.Joins))
	}
	if got.Joins[0].On == nil || got.Joins[1].On == nil {
		t.Fatal("expected ON predicates")
	}

	four, err := parser.Parse(`SELECT a.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(four, lookup, 1); err != nil {
		t.Fatal(err)
	}
	five, err := parser.Parse(`SELECT a.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k JOIN e ON a.k = e.k`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(five, lookup, 1); err != nil {
		t.Fatal(err)
	}
	eight, err := parser.Parse(`SELECT a.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k JOIN e ON a.k = e.k JOIN f ON a.k = f.k JOIN g ON a.k = g.k JOIN h ON a.k = h.k`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(eight, lookup, 1); err != nil {
		t.Fatal(err)
	}
	nine, err := parser.Parse(`SELECT a.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k JOIN e ON a.k = e.k JOIN f ON a.k = f.k JOIN g ON a.k = g.k JOIN h ON a.k = h.k JOIN i ON a.k = i.k`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(nine, lookup, 1); err == nil {
		t.Fatal("expected join complexity error")
	}
}

func TestBindLeftJoinNullableSchema(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE customers (id STRING PRIMARY KEY, name STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	cb, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	cust := cb.(CreateTable).Table
	st, err = parser.Parse(`CREATE TABLE orders (id STRING PRIMARY KEY, customer_id STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	ob, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 2)
	if err != nil {
		t.Fatal(err)
	}
	ord := ob.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		switch name {
		case "customers":
			return cust, true
		case "orders":
			return ord, true
		}
		return nil, false
	}
	sel, err := parser.Parse(`SELECT customers.name, orders.id FROM customers LEFT JOIN orders ON orders.customer_id = customers.id`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bs.(Select)
	if len(got.Joins) != 1 || got.Joins[0].Kind != ast.JoinLeft {
		t.Fatalf("kind %+v", got.Joins)
	}
	for _, c := range got.Joins[0].Table.Columns {
		if c.NotNull {
			t.Fatalf("left-join right column %q must be nullable", c.Name)
		}
	}
	sel, err = parser.Parse(`SELECT customers.name, orders.id FROM orders RIGHT JOIN customers ON orders.customer_id = customers.id`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err = Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	got = bs.(Select)
	if len(got.Joins) != 1 || got.Joins[0].Kind != ast.JoinRight {
		t.Fatalf("right kind %+v", got.Joins)
	}
	nLeft := len(got.Table.Columns)
	for i := 0; i < nLeft && i < len(got.Schema.Columns); i++ {
		if got.Schema.Columns[i].NotNull {
			t.Fatalf("right-join left column %q must be nullable", got.Schema.Columns[i].Name)
		}
	}
	sel, err = parser.Parse(`SELECT customers.name, orders.id FROM customers FULL OUTER JOIN orders ON orders.customer_id = customers.id`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err = Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	got = bs.(Select)
	if len(got.Joins) != 1 || got.Joins[0].Kind != ast.JoinFull {
		t.Fatalf("full kind %+v", got.Joins)
	}
	for _, c := range got.Schema.Columns {
		if c.NotNull {
			t.Fatalf("full-join column %q must be nullable", c.Name)
		}
	}
}

func TestBindSearchAndNearestRejectOuterJoin(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, body TEXT, emb VECTOR<F32,3>, k STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	u, err := parser.Parse(`CREATE TABLE u (id UUID PRIMARY KEY, k STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	ub, err := Bind(u, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	utab := ub.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		switch name {
		case "t":
			return tab, true
		case "u":
			return utab, true
		}
		return nil, false
	}
	sel, err := parser.Parse(`SELECT t.id FROM t LEFT JOIN u ON t.k = u.k SEARCH body FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(sel, lookup, 1); err == nil {
		t.Fatal("expected SEARCH+LEFT JOIN error")
	}
}

func TestBindSearchAndNearestJoinFromTable(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, body TEXT, emb VECTOR<F32,3>, k STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	u, err := parser.Parse(`CREATE TABLE u (id UUID PRIMARY KEY, k STRING, note TEXT, vec VECTOR<F32,3>)`)
	if err != nil {
		t.Fatal(err)
	}
	ub, err := Bind(u, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	utab := ub.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		switch name {
		case "t":
			return tab, true
		case "u":
			return utab, true
		}
		return nil, false
	}
	sel, err := parser.Parse(`SELECT t.id FROM t JOIN u ON t.k = u.k SEARCH body FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bs.(Select).SearchCols) != 1 || bs.(Select).SearchCols[0] != 1 {
		t.Fatalf("SEARCH col %+v", bs)
	}
	sel, err = parser.Parse(`SELECT t.id FROM t JOIN u ON t.k = u.k NEAREST emb TO (1, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err = Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bs.(Select).NearestCol != 2 {
		t.Fatalf("NEAREST col %+v", bs)
	}
	sel, err = parser.Parse(`SELECT t.id FROM t JOIN u ON t.k = u.k WHERE t.k = 'a' SEARCH body FOR 'x' NEAREST emb TO (1, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err = Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bs.(Select)
	if len(got.SearchCols) != 1 || got.SearchCols[0] != 1 || got.NearestCol != 2 || got.Where == nil {
		t.Fatalf("hybrid join %+v", got)
	}
	sel, err = parser.Parse(`SELECT t.id FROM t JOIN u ON t.k = u.k SEARCH note FOR 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(sel, lookup, 1); err == nil {
		t.Fatal("expected SEARCH on joined table to fail")
	}
	sel, err = parser.Parse(`SELECT t.id FROM t JOIN u ON t.k = u.k NEAREST vec TO (1, 0, 0)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(sel, lookup, 1); err == nil {
		t.Fatal("expected NEAREST on joined table to fail")
	}
}

func TestBindParamAllowed(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, n STRING NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	sel, err := parser.Parse(`SELECT n FROM t WHERE n = $1`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(sel, func(string) (*catalog.Table, bool) { return tab, true }, 1); err != nil {
		t.Fatal(err)
	}
}

func TestBindJSONPath(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, metadata JSON)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	sel, err := parser.Parse(`SELECT metadata.category FROM t WHERE metadata.category = 'x'`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := bs.(Select).Where.(ast.Binary); !ok {
		t.Fatalf("%T", bs.(Select).Where)
	}
	ix, err := parser.Parse(`CREATE INDEX ix_cat ON t (metadata.category)`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(ix, func(string) (*catalog.Table, bool) { return tab, true }, 1)
	if err != nil {
		t.Fatal(err)
	}
	idx := bi.(CreateIndex).Index
	if len(idx.Path) != 1 || idx.Path[0] != "category" || len(idx.Columns) != 1 {
		t.Fatalf("%+v", idx)
	}
	bad, err := parser.Parse(`CREATE INDEX ix ON t (id.foo)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(bad, func(string) (*catalog.Table, bool) { return tab, true }, 1); err == nil {
		t.Fatal("expected non-JSON path index error")
	}
}

func TestBindCoveringPartialExpressionIndexes(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (
		id UUID PRIMARY KEY,
		name STRING NOT NULL,
		status STRING NOT NULL,
		note TEXT,
		qty DECIMAL(10,0)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	lookup := func(string) (*catalog.Table, bool) { return tab, true }

	ix, err := parser.Parse(`CREATE INDEX ix_cover ON t (name) INCLUDE (note, qty)`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(ix, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	idx := bi.(CreateIndex).Index
	if len(idx.Include) != 2 || tab.Columns[idx.Include[0]].Name != "note" {
		t.Fatalf("include %+v", idx)
	}

	ix, err = parser.Parse(`CREATE INDEX ix_active ON t (name) WHERE status = 'active'`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err = Bind(ix, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bi.(CreateIndex).Index.Predicate == nil {
		t.Fatal("expected partial predicate")
	}

	ix, err = parser.Parse(`CREATE INDEX ix_lower ON t (LOWER(name))`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err = Bind(ix, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	idx = bi.(CreateIndex).Index
	if !idx.HasExpr() || idx.ExprTypes[0].Kind != types.KindString {
		t.Fatalf("expression index %+v", idx)
	}

	vol, err := parser.Parse(`CREATE INDEX ix_now ON t (NOW())`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(vol, lookup, 1); err == nil {
		t.Fatal("expected volatile rejection")
	}
	uuidIx, err := parser.Parse(`CREATE INDEX ix_uuid ON t (UUID())`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(uuidIx, lookup, 1); err == nil {
		t.Fatal("expected uuid rejection")
	}
	dup, err := parser.Parse(`CREATE INDEX ix_dup ON t (name) INCLUDE (name)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(dup, lookup, 1); err == nil {
		t.Fatal("expected INCLUDE key overlap rejection")
	}
	ft, err := parser.Parse(`CREATE FULLTEXT INDEX ix_ft ON t (name) WHERE status = 'active'`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(ft, lookup, 1); err == nil {
		t.Fatal("expected fulltext WHERE rejection")
	}
}

func TestBindOrderByAndDDL(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id UUID PRIMARY KEY, n STRING NOT NULL, q DECIMAL(10,0))`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "t" {
			return tab, true
		}
		return nil, false
	}

	sel, err := parser.Parse(`SELECT n FROM t ORDER BY q DESC, n`)
	if err != nil {
		t.Fatal(err)
	}
	bs, err := Bind(sel, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	got := bs.(Select)
	if len(got.Order) != 2 || !got.Order[0].Desc || got.Hidden != 1 {
		t.Fatalf("%+v", got)
	}

	drop, err := parser.Parse(`DROP TABLE t`)
	if err != nil {
		t.Fatal(err)
	}
	bd, err := Bind(drop, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bd.(DropTable).Table == nil {
		t.Fatal("expected table")
	}

	missing, err := parser.Parse(`DROP TABLE IF EXISTS nope`)
	if err != nil {
		t.Fatal(err)
	}
	bm, err := Bind(missing, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bm.(DropTable).Table != nil || !bm.(DropTable).IfExists {
		t.Fatalf("%+v", bm)
	}

	alt, err := parser.Parse(`ALTER TABLE t ADD note STRING`)
	if err != nil {
		t.Fatal(err)
	}
	ba, err := Bind(alt, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(ba.(AlterTable).Result.Columns) != 4 {
		t.Fatalf("%+v", ba.(AlterTable).Result.Columns)
	}

	cdb, err := parser.Parse(`CREATE DATABASE app`)
	if err != nil {
		t.Fatal(err)
	}
	bc, err := Bind(cdb, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if bc.(CreateDatabase).Name != "app" {
		t.Fatalf("%+v", bc)
	}
}

func TestBindUpsertAndReturning(t *testing.T) {
	st, err := parser.Parse(`CREATE TABLE t (id STRING PRIMARY KEY, email STRING NOT NULL, n STRING)`)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab := b.(CreateTable).Table
	lookup := func(name string) (*catalog.Table, bool) {
		if name == "t" {
			return tab, true
		}
		return nil, false
	}
	ix, err := parser.Parse(`CREATE UNIQUE INDEX ux_email ON t (email)`)
	if err != nil {
		t.Fatal(err)
	}
	bi, err := Bind(ix, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	tab = tab.Clone()
	tab.Indexes = append(tab.Indexes, bi.(CreateIndex).Index)
	lookup = func(name string) (*catalog.Table, bool) {
		if name == "t" {
			return tab, true
		}
		return nil, false
	}

	up, err := parser.Parse(`UPSERT INTO t (id, email, n) VALUES ('1', 'a@b', 'x') ON UNIQUE (email) SET n = excluded.n RETURNING id, n`)
	if err != nil {
		t.Fatal(err)
	}
	bu, err := Bind(up, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	u := bu.(Upsert)
	if u.UniquePK || u.UniqueIdx != "ux_email" || len(u.Sets) != 1 || len(u.Returning.Names) != 2 {
		t.Fatalf("%+v", u)
	}
	if u.DefaultSet {
		t.Fatal("explicit SET should not default")
	}

	pk, err := parser.Parse(`UPSERT INTO t (id, email, n) VALUES ('1', 'a@b', 'x')`)
	if err != nil {
		t.Fatal(err)
	}
	bp, err := Bind(pk, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !bp.(Upsert).UniquePK || !bp.(Upsert).DefaultSet {
		t.Fatalf("infer PK: %+v", bp)
	}

	_, err = parser.Parse(`UPSERT INTO t (n) VALUES ('x')`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Bind(mustParse(t, `UPSERT INTO t (n) VALUES ('x')`), lookup, 1); err == nil {
		t.Fatal("expected UPSERT without unique coverage to fail")
	}

	ins, err := parser.Parse(`INSERT INTO t (id, email, n) VALUES ('1', 'a@b', 'x') RETURNING *`)
	if err != nil {
		t.Fatal(err)
	}
	bi2, err := Bind(ins, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(bi2.(Insert).Returning.Names) != 3 {
		t.Fatalf("returning *: %+v", bi2.(Insert).Returning)
	}
}

func mustParse(t *testing.T, src string) ast.Stmt {
	t.Helper()
	st, err := parser.Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	return st
}
