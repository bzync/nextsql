package optimizer

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/binder"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

func sampleTable() *catalog.Table {
	dec, _ := types.DecimalType(12, 2)
	return &catalog.Table{
		ID:   1,
		Name: "products",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "name", Type: types.String(), NotNull: true},
			{Name: "price", Type: dec},
			{Name: "note", Type: types.Text()},
		},
		PK:      []int{0},
		Indexes: []catalog.Index{{Name: "ix_name", Columns: []int{1}}},
	}
}

func planSQL(t *testing.T, sql string, tab *catalog.Table) planner.Logical {
	t.Helper()
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
		if name == tab.Name {
			return tab, true
		}
		return nil, false
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func optSQL(t *testing.T, sql string, tab *catalog.Table, st *catalog.TableStats) Outcome {
	t.Helper()
	p := planSQL(t, sql, tab)
	var fn StatsFunc
	if st != nil {
		fn = func(name string) (*catalog.TableStats, bool) {
			if name == st.Table {
				return st, true
			}
			return nil, false
		}
	}
	out, err := Optimize(Request{Plan: p, SQL: sql, Stats: fn, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSubscribePlanIsDirectAndNonCacheable(t *testing.T) {
	tab := sampleTable()
	p := planSQL(t, `SUBSCRIBE TO products WHERE operation = 'DELETE' AFTER 42`, tab)
	sub, ok := p.(planner.Subscribe)
	if !ok || sub.Table != tab || sub.Operation != "DELETE" || sub.After != 42 {
		t.Fatalf("plan=%#v", p)
	}
	out, err := Optimize(Request{Plan: p, SQL: `SUBSCRIBE TO products WHERE operation = 'DELETE' AFTER 42`, Gen: 1, Cache: NewCache()})
	if err != nil {
		t.Fatal(err)
	}
	if out.Trace == nil || out.Trace.Op != "Subscribe" || out.Cached {
		t.Fatalf("outcome=%+v", out)
	}
}

func TestConstantFolding(t *testing.T) {
	tab := sampleTable()
	p := planSQL(t, `SELECT name FROM products WHERE 1 = 0`, tab)
	got := rewrite(p)
	if _, ok := got.(planner.Empty); !ok {
		t.Fatalf("want Empty, got %s", formatPlan(got))
	}
	p = planSQL(t, `SELECT name FROM products WHERE 1 = 1`, tab)
	got = rewrite(p)
	if strings.Contains(formatPlan(got), "Filter") {
		t.Fatalf("true pred should drop filter: %s", formatPlan(got))
	}
	p = planSQL(t, `SELECT name FROM products WHERE 1 + 1 = 2 AND name = 'a'`, tab)
	got = rewrite(p)
	s := formatPlan(got)
	if !strings.Contains(s, "name = a") && !strings.Contains(s, "(name = a)") {
		t.Fatalf("folded AND: %s", s)
	}
}

func TestPredicatePushdown(t *testing.T) {
	tab := sampleTable()
	inner := planner.Project{
		Input: planner.Scan{Table: tab},
		Cols:  []int{1},
		Exprs: []ast.Expr{ast.Ident{Name: "name"}},
		Names: []string{"name"},
	}
	p := planner.Filter{Input: inner, Pred: ast.Binary{Op: "=", Left: ast.Ident{Name: "name"}, Right: ast.Literal{Value: types.StringValue("a")}}}
	got := rewrite(p)
	s := formatPlan(got)
	if !strings.HasPrefix(s, "Project") || !strings.Contains(s, "Filter") {
		t.Fatalf("%s", s)
	}
	// Filter should sit below Project.
	pr, ok := got.(planner.Project)
	if !ok {
		t.Fatalf("%T", got)
	}
	if _, ok := pr.Input.(planner.Filter); !ok {
		t.Fatalf("filter not pushed: %T", pr.Input)
	}
}

func TestProjectionPushdownAndColumnPrune(t *testing.T) {
	tab := sampleTable()
	p := planSQL(t, `SELECT name FROM products WHERE price > 1`, tab)
	got := rewrite(p)
	sc := findScan(got)
	if sc == nil {
		t.Fatal("no scan")
	}
	if len(sc.Needed) == 0 {
		t.Fatalf("expected pruned columns, got %+v", sc.Needed)
	}
	seen := map[int]bool{}
	for _, i := range sc.Needed {
		seen[i] = true
	}
	if !seen[1] || !seen[2] {
		t.Fatalf("need name+price, got %v", sc.Needed)
	}
	if seen[3] {
		t.Fatalf("note should be pruned: %v", sc.Needed)
	}
}

func findScan(p planner.Logical) *planner.Scan {
	switch n := p.(type) {
	case planner.Scan:
		return &n
	case planner.Filter:
		return findScan(n.Input)
	case planner.Project:
		return findScan(n.Input)
	case planner.Limit:
		return findScan(n.Input)
	case planner.Search:
		return findScan(n.Input)
	case planner.Nearest:
		return findScan(n.Input)
	case planner.Candidates:
		return findScan(n.Input)
	case planner.Rerank:
		return findScan(n.Input)
	default:
		return nil
	}
}

func TestLimitPushdown(t *testing.T) {
	tab := sampleTable()
	p := planSQL(t, `SELECT name FROM products LIMIT 5`, tab)
	got := rewrite(p)
	pr, ok := got.(planner.Project)
	if !ok {
		t.Fatalf("%T %s", got, formatPlan(got))
	}
	if _, ok := pr.Input.(planner.Limit); !ok {
		t.Fatalf("limit not under project: %T", pr.Input)
	}
	p = planSQL(t, `SELECT name FROM products LIMIT 0`, tab)
	got = rewrite(p)
	if _, ok := got.(planner.Empty); !ok {
		t.Fatalf("limit 0: %s", formatPlan(got))
	}
	p = planSQL(t, `SELECT name FROM products LIMIT 5 OFFSET 2`, tab)
	got = rewrite(p)
	pr, ok = got.(planner.Project)
	if !ok {
		t.Fatalf("offset %T %s", got, formatPlan(got))
	}
	lim, ok := pr.Input.(planner.Limit)
	if !ok || lim.N != 5 || lim.Offset != 2 {
		t.Fatalf("offset not under project: %+v", pr.Input)
	}
}

func TestLeftDeepMultiJoin(t *testing.T) {
	tabs := map[string]*catalog.Table{}
	for _, name := range []string{"a", "b", "c"} {
		st, err := parser.Parse(`CREATE TABLE ` + name + ` (id UUID PRIMARY KEY, k STRING)`)
		if err != nil {
			t.Fatal(err)
		}
		b, err := binder.Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
		if err != nil {
			t.Fatal(err)
		}
		tabs[name] = b.(binder.CreateTable).Table
	}
	stmt, err := parser.Parse(`SELECT a.k, b.k, c.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
		t, ok := tabs[name]
		return t, ok
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	pr, ok := p.(planner.Project)
	if !ok {
		t.Fatalf("%T", p)
	}
	outer, ok := pr.Input.(planner.Join)
	if !ok {
		t.Fatalf("want outer Join, got %T", pr.Input)
	}
	inner, ok := outer.Left.(planner.Join)
	if !ok {
		t.Fatalf("want left-deep inner Join, got %T", outer.Left)
	}
	if _, ok := inner.Left.(planner.Scan); !ok {
		t.Fatalf("want Scan at leftmost, got %T", inner.Left)
	}
	if _, ok := inner.Right.(planner.Scan); !ok {
		t.Fatalf("want Scan as first right, got %T", inner.Right)
	}
	if _, ok := outer.Right.(planner.Scan); !ok {
		t.Fatalf("want Scan as second right, got %T", outer.Right)
	}
	out, err := Optimize(Request{Plan: p, SQL: `SELECT a.k, b.k, c.k FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k`, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "Join") && !strings.Contains(s, "HashJoin") {
		t.Fatalf("want join plan: %s", s)
	}
	got := joinScanNames(out.Plan)
	if strings.Join(got, ",") != "a,b,c" {
		t.Fatalf("equal-size inner joins keep written order, got %v\n%s", got, s)
	}
}

func TestJoinReorderBuildSide(t *testing.T) {
	tabs := map[string]*catalog.Table{}
	for _, name := range []string{"small", "big"} {
		st, err := parser.Parse(`CREATE TABLE ` + name + ` (id UUID PRIMARY KEY, k STRING, n STRING)`)
		if err != nil {
			t.Fatal(err)
		}
		b, err := binder.Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
		if err != nil {
			t.Fatal(err)
		}
		tabs[name] = b.(binder.CreateTable).Table
	}
	sql := `SELECT small.n, big.n FROM small JOIN big ON small.k = big.k`
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
		t, ok := tabs[name]
		return t, ok
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	stats := func(name string) (*catalog.TableStats, bool) {
		switch name {
		case "small":
			return &catalog.TableStats{Table: "small", Rows: 10}, true
		case "big":
			return &catalog.TableStats{Table: "big", Rows: 100000}, true
		}
		return nil, false
	}
	out, err := Optimize(Request{Plan: p, SQL: sql, Stats: stats, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := formatPlan(out.Plan)
	got := joinScanNames(out.Plan)
	if len(got) != 2 || got[0] != "big" || got[1] != "small" {
		t.Fatalf("want big probe / small build, got %v\n%s", got, s)
	}
	if pr, ok := out.Plan.(planner.Project); ok {
		if _, ok := pr.Input.(planner.Project); ok {
			// SELECT project over restore project
		}
	}
}

func TestJoinReorderSkipsOuterAndRank(t *testing.T) {
	tabs := map[string]*catalog.Table{}
	for _, sql := range []string{
		`CREATE TABLE small (id UUID PRIMARY KEY, k STRING, n STRING)`,
		`CREATE TABLE big (id UUID PRIMARY KEY, k STRING, n STRING, body TEXT)`,
	} {
		st, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		b, err := binder.Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
		if err != nil {
			t.Fatal(err)
		}
		tabs[b.(binder.CreateTable).Table.Name] = b.(binder.CreateTable).Table
	}
	stats := func(name string) (*catalog.TableStats, bool) {
		switch name {
		case "small":
			return &catalog.TableStats{Table: "small", Rows: 10}, true
		case "big":
			return &catalog.TableStats{Table: "big", Rows: 100000}, true
		}
		return nil, false
	}
	plan := func(sql string) planner.Logical {
		t.Helper()
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
			tab, ok := tabs[name]
			return tab, ok
		}, 1)
		if err != nil {
			t.Fatal(err)
		}
		p, err := planner.Plan(bound)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Optimize(Request{Plan: p, SQL: sql, Stats: stats, Gen: 1})
		if err != nil {
			t.Fatal(err)
		}
		return out.Plan
	}
	left := plan(`SELECT small.n, big.n FROM small LEFT JOIN big ON small.k = big.k`)
	if got := joinScanNames(left); len(got) != 2 || got[0] != "small" || got[1] != "big" {
		t.Fatalf("LEFT JOIN must not swap, got %v\n%s", got, formatPlan(left))
	}
	ranked := plan(`SELECT big.n, small.n FROM big JOIN small ON big.k = small.k SEARCH body FOR 'x'`)
	if !rankUnderJoin(ranked) {
		t.Fatalf("rank-then-join must stay, got %s", formatPlan(ranked))
	}
	if got := joinScanNames(ranked); len(got) != 2 || got[0] != "big" || got[1] != "small" {
		t.Fatalf("SEARCH join must keep FROM table first, got %v\n%s", got, formatPlan(ranked))
	}
}

func TestJoinReorderPushesLocalFilters(t *testing.T) {
	tabs := map[string]*catalog.Table{}
	for _, name := range []string{"orders", "lines"} {
		st, err := parser.Parse(`CREATE TABLE ` + name + ` (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, k STRING NOT NULL)`)
		if err != nil {
			t.Fatal(err)
		}
		b, err := binder.Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
		if err != nil {
			t.Fatal(err)
		}
		tabs[name] = b.(binder.CreateTable).Table
	}
	sql := `SELECT orders.id, lines.id FROM orders JOIN lines ON orders.k = lines.k WHERE orders.tenant_id = '11111111-1111-1111-1111-111111111111' AND lines.tenant_id = '11111111-1111-1111-1111-111111111111'`
	stmt, err := parser.Parse(sql)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
		t, ok := tabs[name]
		return t, ok
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Optimize(Request{Plan: p, SQL: sql, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "Filter") {
		t.Fatalf("local join/WHERE conjuncts should become per-scan filters:\n%s", s)
	}
	if strings.Count(s, "Filter") < 2 {
		t.Fatalf("want a filter on each side:\n%s", s)
	}
}

func joinScanNames(p planner.Logical) []string {
	var out []string
	var walk func(planner.Logical)
	walk = func(n planner.Logical) {
		switch x := n.(type) {
		case planner.Scan:
			out = append(out, tableName(x.Table))
		case planner.SeqScan:
			out = append(out, tableName(x.Table))
		case planner.IndexScan:
			out = append(out, tableName(x.Table))
		case planner.Search:
			if x.Table != nil {
				out = append(out, tableName(x.Table))
				return
			}
			walk(x.Input)
		case planner.Nearest:
			if x.Table != nil {
				out = append(out, tableName(x.Table))
				return
			}
			walk(x.Input)
		case planner.Candidates:
			walk(x.Input)
		case planner.Rerank:
			walk(x.Input)
		case planner.Filter:
			walk(x.Input)
		case planner.Project:
			walk(x.Input)
		case planner.Limit:
			walk(x.Input)
		case planner.Join:
			walk(x.Left)
			walk(x.Right)
		}
	}
	walk(p)
	return out
}

func TestSearchJoinRanksThenJoins(t *testing.T) {
	tabs := map[string]*catalog.Table{}
	for _, sql := range []string{
		`CREATE TABLE articles (id UUID PRIMARY KEY, k STRING, body TEXT)`,
		`CREATE TABLE authors (id UUID PRIMARY KEY, k STRING, name STRING)`,
	} {
		st, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		b, err := binder.Bind(st, func(string) (*catalog.Table, bool) { return nil, false }, 1)
		if err != nil {
			t.Fatal(err)
		}
		tab := b.(binder.CreateTable).Table
		tabs[tab.Name] = tab
	}
	tabs["articles"].Indexes = []catalog.Index{{Name: "ix_body", Fulltext: true, Columns: []int{2}}}
	stmt, err := parser.Parse(`SELECT articles.body, authors.name FROM articles JOIN authors ON articles.k = authors.k SEARCH body FOR 'cat' LIMIT 5`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
		t, ok := tabs[name]
		return t, ok
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	if !rankUnderJoin(p) {
		t.Fatalf("want Search under Join, got %s", formatPlan(p))
	}
	out, err := Optimize(Request{Plan: p, SQL: `SELECT articles.body, authors.name FROM articles JOIN authors ON articles.k = authors.k SEARCH body FOR 'cat' LIMIT 5`, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "Search") || (!strings.Contains(s, "Join") && !strings.Contains(s, "HashJoin")) {
		t.Fatalf("want Search then Join: %s", s)
	}
	if !rankUnderJoin(out.Plan) {
		t.Fatalf("optimized plan should rank then join: %s", s)
	}
}

func TestLimitDoesNotSetRankKAcrossJoin(t *testing.T) {
	vt, _ := types.VectorF32(3)
	articles := &catalog.Table{
		ID:   1,
		Name: "articles",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "k", Type: types.String()},
			{Name: "body", Type: types.Text()},
			{Name: "emb", Type: vt},
		},
		PK:      []int{0},
		Indexes: []catalog.Index{{Name: "ix_emb", Vector: true, Columns: []int{3}}},
	}
	authors := &catalog.Table{
		ID:   2,
		Name: "authors",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "k", Type: types.String()},
			{Name: "name", Type: types.String()},
		},
		PK: []int{0},
	}
	lookup := func(name string) (*catalog.Table, bool) {
		switch name {
		case articles.Name:
			return articles, true
		case authors.Name:
			return authors, true
		}
		return nil, false
	}
	stmt, err := parser.Parse(`SELECT articles.k, authors.name FROM articles JOIN authors ON articles.k = authors.k NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Optimize(Request{Plan: p, SQL: `SELECT articles.k, authors.name FROM articles JOIN authors ON articles.k = authors.k NEAREST emb TO (1, 0, 0) LIMIT 1`, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	nr := findNearest(out.Plan)
	if nr == nil {
		t.Fatalf("want Nearest under join: %s", formatPlan(out.Plan))
	}
	if nr.K != 0 {
		t.Fatalf("LIMIT must not become Nearest.K across a join, got K=%d\n%s", nr.K, formatPlan(out.Plan))
	}

	articles.Indexes = append(articles.Indexes, catalog.Index{Name: "ix_body", Fulltext: true, Columns: []int{2}})
	hsql := `SELECT articles.k, authors.name FROM articles JOIN authors ON articles.k = authors.k SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 1`
	stmt, err = parser.Parse(hsql)
	if err != nil {
		t.Fatal(err)
	}
	bound, err = binder.Bind(stmt, lookup, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err = planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	out, err = Optimize(Request{Plan: p, SQL: hsql, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	rr := findRerank(out.Plan)
	if rr == nil {
		t.Fatalf("want Rerank under join: %s", formatPlan(out.Plan))
	}
	if rr.K != 0 {
		t.Fatalf("hybrid under join must not default Rerank.K, got K=%d\n%s", rr.K, formatPlan(out.Plan))
	}
}

func TestHybridJoinRanksThenJoins(t *testing.T) {
	tab := hybridTable()
	authors := &catalog.Table{
		ID:   5,
		Name: "authors",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "k", Type: types.String()},
			{Name: "name", Type: types.String()},
		},
		PK: []int{0},
	}
	tab.Columns = append(tab.Columns, catalog.Column{Name: "k", Type: types.String()})
	stmt, err := parser.Parse(`SELECT products.name, authors.name FROM products JOIN authors ON products.k = authors.k WHERE products.price <= 15000 SEARCH description FOR 'wireless' NEAREST embedding TO (1, 0, 0) LIMIT 10`)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := binder.Bind(stmt, func(name string) (*catalog.Table, bool) {
		switch name {
		case tab.Name:
			return tab, true
		case authors.Name:
			return authors, true
		}
		return nil, false
	}, 1)
	if err != nil {
		t.Fatal(err)
	}
	p, err := planner.Plan(bound)
	if err != nil {
		t.Fatal(err)
	}
	if !rankUnderJoin(p) {
		t.Fatalf("want rank under Join, got %s", formatPlan(p))
	}
	out, err := Optimize(Request{Plan: p, SQL: `SELECT products.name, authors.name FROM products JOIN authors ON products.k = authors.k WHERE products.price <= 15000 SEARCH description FOR 'wireless' NEAREST embedding TO (1, 0, 0) LIMIT 10`, Gen: 1})
	if err != nil {
		t.Fatal(err)
	}
	s := formatPlan(out.Plan)
	if !hasOp(out.Plan, "Rerank") || (!strings.Contains(s, "Join") && !strings.Contains(s, "HashJoin")) {
		t.Fatalf("want Rerank then Join: %s", s)
	}
	if !rankUnderJoin(out.Plan) {
		t.Fatalf("hybrid should rank then join: %s", s)
	}
}

func rankUnderJoin(p planner.Logical) bool {
	switch n := p.(type) {
	case planner.Join:
		return hasRankOp(n.Left) && !hasRankOp(n.Right)
	case planner.Filter:
		return rankUnderJoin(n.Input)
	case planner.Project:
		return rankUnderJoin(n.Input)
	case planner.Limit:
		return rankUnderJoin(n.Input)
	default:
		return false
	}
}

func hasRankOp(p planner.Logical) bool {
	switch n := p.(type) {
	case planner.Search, planner.Nearest, planner.Rerank, planner.Candidates:
		return true
	case planner.Filter:
		return hasRankOp(n.Input)
	case planner.Limit:
		return hasRankOp(n.Input)
	case planner.Join:
		return hasRankOp(n.Left) || hasRankOp(n.Right)
	case planner.Project:
		return hasRankOp(n.Input)
	default:
		return false
	}
}

func TestJoinSimplification(t *testing.T) {
	tab := sampleTable()
	j := planner.Join{
		Left:  planner.Empty{Names: []string{"a"}},
		Right: planner.Scan{Table: tab},
		Pred:  ast.Literal{Value: types.BoolValue(true)},
	}
	got := rewrite(j)
	if _, ok := got.(planner.Empty); !ok {
		t.Fatalf("empty join: %s", formatPlan(got))
	}
	j = planner.Join{
		Left:  planner.Scan{Table: tab},
		Right: planner.Scan{Table: tab},
		Pred:  ast.Literal{Value: types.BoolValue(false)},
	}
	got = rewrite(j)
	if _, ok := got.(planner.Empty); !ok {
		t.Fatalf("false join: %s", formatPlan(got))
	}
	left := planner.Scan{Table: tab}
	right := planner.Scan{Table: &catalog.Table{
		Name:    "other",
		Columns: []catalog.Column{{Name: "id", Type: types.UUID()}, {Name: "sku", Type: types.String()}},
		PK:      []int{0},
	}}
	pred := ast.Binary{Op: "AND",
		Left:  ast.Binary{Op: "=", Left: ast.Ident{Name: "name"}, Right: ast.Literal{Value: types.StringValue("a")}},
		Right: ast.Binary{Op: "=", Left: ast.Ident{Name: "sku"}, Right: ast.Literal{Value: types.StringValue("b")}},
	}
	got = rewrite(planner.Filter{Input: planner.Join{Left: left, Right: right}, Pred: pred})
	s := formatPlan(got)
	if !strings.Contains(s, "Filter") || !strings.Contains(s, "Join") && !strings.Contains(s, "CrossJoin") {
		t.Fatalf("pushed join filters: %s", s)
	}
	jn, ok := got.(planner.Join)
	if !ok {
		t.Fatalf("%T %s", got, s)
	}
	if _, ok := jn.Left.(planner.Filter); !ok {
		t.Fatalf("left filter missing: %T", jn.Left)
	}
	if _, ok := jn.Right.(planner.Filter); !ok {
		t.Fatalf("right filter missing: %T", jn.Right)
	}
}

func TestLeftJoinNotEmptyCollapsed(t *testing.T) {
	tab := sampleTable()
	other := &catalog.Table{
		Name:    "other",
		Columns: []catalog.Column{{Name: "id", Type: types.UUID()}, {Name: "sku", Type: types.String()}},
		PK:      []int{0},
	}
	got := rewrite(planner.Join{
		Left:  planner.Scan{Table: tab},
		Right: planner.Empty{Names: []string{"id"}},
		Kind:  ast.JoinLeft,
		Pred:  ast.Literal{Value: types.BoolValue(true)},
	})
	if _, ok := got.(planner.Empty); ok {
		t.Fatalf("LEFT + empty right must not collapse: %s", formatPlan(got))
	}
	jn, ok := got.(planner.Join)
	if !ok || jn.Kind != ast.JoinLeft {
		t.Fatalf("want LeftJoin, got %T %s", got, formatPlan(got))
	}
	got = rewrite(planner.Join{
		Left:  planner.Scan{Table: tab},
		Right: planner.Scan{Table: other},
		Kind:  ast.JoinLeft,
		Pred:  ast.Literal{Value: types.BoolValue(false)},
	})
	if _, ok := got.(planner.Empty); ok {
		t.Fatalf("LEFT + false ON must not collapse: %s", formatPlan(got))
	}
	jn, ok = got.(planner.Join)
	if !ok || jn.Kind != ast.JoinLeft || jn.Cross {
		t.Fatalf("false ON LEFT %+v %s", jn, formatPlan(got))
	}
}

func TestLeftJoinDoesNotPushRightFilter(t *testing.T) {
	left := planner.Scan{Table: sampleTable()}
	right := planner.Scan{Table: &catalog.Table{
		Name:    "other",
		Columns: []catalog.Column{{Name: "id", Type: types.UUID()}, {Name: "sku", Type: types.String()}},
		PK:      []int{0},
	}}
	pred := ast.Binary{Op: "AND",
		Left:  ast.Binary{Op: "=", Left: ast.Ident{Name: "name"}, Right: ast.Literal{Value: types.StringValue("a")}},
		Right: ast.Binary{Op: "=", Left: ast.Ident{Name: "sku"}, Right: ast.Literal{Value: types.StringValue("b")}},
	}
	got := rewrite(planner.Filter{Input: planner.Join{Left: left, Right: right, Kind: ast.JoinLeft}, Pred: pred})
	s := formatPlan(got)
	f, ok := got.(planner.Filter)
	if !ok {
		t.Fatalf("want Filter above LEFT, got %T %s", got, s)
	}
	jn, ok := f.Input.(planner.Join)
	if !ok {
		t.Fatalf("want Join under Filter, got %T %s", f.Input, s)
	}
	if _, ok := jn.Right.(planner.Filter); ok {
		t.Fatalf("must not push right-only WHERE into LEFT right: %s", s)
	}
	if _, ok := jn.Left.(planner.Filter); !ok {
		t.Fatalf("left-only WHERE should push: %s", s)
	}
}

func TestRightJoinRewritesToLeft(t *testing.T) {
	left := planner.Scan{Table: sampleTable()}
	right := planner.Scan{Table: &catalog.Table{
		Name:    "other",
		Columns: []catalog.Column{{Name: "id", Type: types.UUID()}, {Name: "sku", Type: types.String()}},
		PK:      []int{0},
	}}
	schema := &catalog.Table{
		Name: "products+other",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID()},
			{Name: "name", Type: types.String()},
			{Name: "other.id", Type: types.UUID()},
			{Name: "sku", Type: types.String()},
		},
	}
	got := rewrite(planner.Join{Left: left, Right: right, Kind: ast.JoinRight, Pred: ast.Literal{Value: types.BoolValue(true)}, Schema: schema})
	pr, ok := got.(planner.Project)
	if !ok {
		t.Fatalf("want Project over LeftJoin, got %T %s", got, formatPlan(got))
	}
	jn, ok := pr.Input.(planner.Join)
	if !ok || jn.Kind != ast.JoinLeft {
		t.Fatalf("want LeftJoin under Project, got %T %s", pr.Input, formatPlan(got))
	}
	if formatPlan(jn.Left) != formatPlan(right) || formatPlan(jn.Right) != formatPlan(left) {
		t.Fatalf("sides not swapped: %s", formatPlan(got))
	}
}

func TestFullJoinNotEmptyCollapsed(t *testing.T) {
	tab := sampleTable()
	other := &catalog.Table{
		Name:    "other",
		Columns: []catalog.Column{{Name: "id", Type: types.UUID()}, {Name: "sku", Type: types.String()}},
		PK:      []int{0},
	}
	got := rewrite(planner.Join{
		Left:  planner.Scan{Table: tab},
		Right: planner.Empty{Names: []string{"id"}},
		Kind:  ast.JoinFull,
		Pred:  ast.Literal{Value: types.BoolValue(true)},
	})
	if _, ok := got.(planner.Empty); ok {
		t.Fatalf("FULL + empty right must not collapse: %s", formatPlan(got))
	}
	jn, ok := got.(planner.Join)
	if !ok || jn.Kind != ast.JoinFull || jn.Cross {
		t.Fatalf("want FullJoin, got %T %s", got, formatPlan(got))
	}
	got = rewrite(planner.Join{
		Left:  planner.Empty{Names: []string{"id"}},
		Right: planner.Scan{Table: other},
		Kind:  ast.JoinFull,
		Pred:  ast.Literal{Value: types.BoolValue(false)},
	})
	if _, ok := got.(planner.Empty); ok {
		t.Fatalf("FULL + empty left / false ON must not collapse: %s", formatPlan(got))
	}
}

func TestFullJoinDoesNotPushFilters(t *testing.T) {
	left := planner.Scan{Table: sampleTable()}
	right := planner.Scan{Table: &catalog.Table{
		Name:    "other",
		Columns: []catalog.Column{{Name: "id", Type: types.UUID()}, {Name: "sku", Type: types.String()}},
		PK:      []int{0},
	}}
	pred := ast.Binary{Op: "AND",
		Left:  ast.Binary{Op: "=", Left: ast.Ident{Name: "name"}, Right: ast.Literal{Value: types.StringValue("a")}},
		Right: ast.Binary{Op: "=", Left: ast.Ident{Name: "sku"}, Right: ast.Literal{Value: types.StringValue("b")}},
	}
	got := rewrite(planner.Filter{Input: planner.Join{Left: left, Right: right, Kind: ast.JoinFull}, Pred: pred})
	s := formatPlan(got)
	f, ok := got.(planner.Filter)
	if !ok {
		t.Fatalf("want Filter above FULL, got %T %s", got, s)
	}
	jn, ok := f.Input.(planner.Join)
	if !ok || jn.Kind != ast.JoinFull {
		t.Fatalf("want FullJoin under Filter, got %T %s", f.Input, s)
	}
	if _, ok := jn.Left.(planner.Filter); ok {
		t.Fatalf("must not push left WHERE into FULL: %s", s)
	}
	if _, ok := jn.Right.(planner.Filter); ok {
		t.Fatalf("must not push right WHERE into FULL: %s", s)
	}
}

func hybridTable() *catalog.Table {
	dec, _ := types.DecimalType(12, 2)
	vt, _ := types.VectorF32(1536)
	return &catalog.Table{
		ID:   4,
		Name: "products",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "name", Type: types.String(), NotNull: true},
			{Name: "price", Type: dec},
			{Name: "description", Type: types.Text()},
			{Name: "metadata", Type: types.JSON()},
			{Name: "embedding", Type: vt},
		},
		PK: []int{0},
		Indexes: []catalog.Index{
			{Name: "ix_cat", Columns: []int{4}, Path: []string{"category"}},
			{Name: "ix_desc", Fulltext: true, Columns: []int{3}},
			{Name: "ix_emb", Vector: true, Columns: []int{5}},
		},
	}
}

func TestHybridPlansAreOneProblem(t *testing.T) {
	tab := hybridTable()
	sql := `SELECT id, name, price FROM products WHERE metadata.category = 'headphones' AND price <= 15000 SEARCH description FOR 'wireless noise cancelling' NEAREST embedding TO (1, 0, 0) LIMIT 20`
	out := optSQL(t, sql, tab, nil)
	s := formatPlan(out.Plan)
	if !hasOp(out.Plan, "Rerank") || !hasOp(out.Plan, "Candidates") {
		t.Fatalf("want Rerank + Candidates, got %s", s)
	}
	if !strings.Contains(s, "bm25+vector") {
		t.Fatalf("want fused rerank: %s", s)
	}
}

func TestHybridChoosesFilterThenANN(t *testing.T) {
	tab := hybridTable()
	dec, _ := types.DecimalType(12, 2)
	st := &catalog.TableStats{
		Table: "products",
		Rows:  1_000_000,
		Columns: []catalog.ColumnStats{
			{Ord: 2, NDV: 10_000, HasMinMax: true,
				Min: types.DecimalValue(mustDec(t, "1"), dec),
				Max: types.DecimalValue(mustDec(t, "50000"), dec),
			},
		},
		Indexes: []catalog.IndexStats{
			{Name: "ix_cat", Selectivity: 0.001, NDV: 1000},
			{Name: "ix_desc", Selectivity: 0.1, NDV: 10000},
			{Name: "ix_emb", Selectivity: 1, NDV: 1_000_000},
		},
		Vectors: []catalog.VectorStats{{Ord: 5, Count: 1_000_000, Dim: 1536, IndexName: "ix_emb", M: 16, EfConstruct: 64}},
	}
	sql := `SELECT id, name FROM products WHERE metadata.category = 'headphones' AND price <= 15000 SEARCH description FOR 'wireless' NEAREST embedding TO (1, 0, 0) LIMIT 20`
	out := optSQL(t, sql, tab, st)
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "filter-ann") {
		t.Fatalf("want structured-filter-then-ANN, got %s", s)
	}
	if !hasOp(out.Plan, "IndexScan") && !strings.Contains(s, "ix_cat") {
		t.Fatalf("want category index under candidates: %s", s)
	}
}

func TestHybridChoosesANNThenFilter(t *testing.T) {
	tab := hybridTable()
	st := &catalog.TableStats{
		Table:   "products",
		Rows:    1_000_000,
		Vectors: []catalog.VectorStats{{Ord: 5, Count: 1_000_000, Dim: 1536, IndexName: "ix_emb", M: 16, EfConstruct: 64}},
	}
	sql := `SELECT id, name FROM products SEARCH description FOR 'wireless' NEAREST embedding TO (1, 0, 0) LIMIT 20`
	out := optSQL(t, sql, tab, st)
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "ann-filter") {
		t.Fatalf("want ANN-then-filter, got %s", s)
	}
	if !strings.Contains(s, "hnsw") {
		t.Fatalf("want HNSW candidates: %s", s)
	}
}

func TestHybridChoosesSearchThenANN(t *testing.T) {
	tab := hybridTable()
	tab.Indexes = []catalog.Index{
		{Name: "ix_desc", Fulltext: true, Columns: []int{3}},
	}
	st := &catalog.TableStats{
		Table:   "products",
		Rows:    1_000_000,
		Indexes: []catalog.IndexStats{{Name: "ix_desc", Selectivity: 0.001, NDV: 1000}},
		Vectors: []catalog.VectorStats{{Ord: 5, Count: 1_000_000, Dim: 1536}},
	}
	sql := `SELECT id, name FROM products SEARCH description FOR 'wireless noise cancelling' NEAREST embedding TO (1, 0, 0) LIMIT 20`
	out := optSQL(t, sql, tab, st)
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "search-ann") {
		t.Fatalf("want SEARCH-then-ANN, got %s", s)
	}
	if !strings.Contains(s, "ix_desc") {
		t.Fatalf("want fulltext candidates: %s", s)
	}
}

func TestHybridPlansDeterministic(t *testing.T) {
	tab := hybridTable()
	st := &catalog.TableStats{
		Table:   "products",
		Rows:    250_000,
		Indexes: []catalog.IndexStats{{Name: "ix_cat", Selectivity: 0.03, NDV: 30}},
		Vectors: []catalog.VectorStats{{Ord: 5, Count: 250_000, Dim: 1536, IndexName: "ix_emb", M: 16, EfConstruct: 64}},
	}
	sql := `SELECT id FROM products WHERE metadata.category = 'headphones' SEARCH description FOR 'wireless' NEAREST embedding TO (1, 0, 0) LIMIT 20`
	var first string
	for i := 0; i < 20; i++ {
		out := optSQL(t, sql, tab, st)
		s := formatPlan(out.Plan)
		if i == 0 {
			first = s
		} else if s != first {
			t.Fatalf("nondeterministic\n%s\nvs\n%s", first, s)
		}
	}
	if !hasOp(optSQL(t, sql, tab, st).Plan, "Rerank") {
		t.Fatalf("expected hybrid plan: %s", first)
	}
}

func TestSearchChoosesFulltextIndex(t *testing.T) {
	tab := sampleTable()
	tab.Indexes = append(tab.Indexes, catalog.Index{Name: "ix_note", Fulltext: true, Columns: []int{3}})
	out := optSQL(t, `SELECT name FROM products SEARCH note FOR 'database performance'`, tab, nil)
	if !hasOp(out.Plan, "Search") || !strings.Contains(formatPlan(out.Plan), "ix_note") {
		t.Fatalf("want fulltext Search, got %s", formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM products SEARCH note FOR 'x'`, sampleTable(), nil)
	if !hasOp(out.Plan, "Search") || !strings.Contains(formatPlan(out.Plan), "seq") {
		t.Fatalf("want seq Search, got %s", formatPlan(out.Plan))
	}
}

func TestIndexSelection(t *testing.T) {
	tab := sampleTable()
	out := optSQL(t, `SELECT name FROM products WHERE name = 'alpha'`, tab, nil)
	if !hasOp(out.Plan, "IndexScan") {
		t.Fatalf("want IndexScan, got %s", formatPlan(out.Plan))
	}
	is := findIndex(out.Plan)
	if is == nil || is.IndexName != "ix_name" {
		t.Fatalf("%+v", is)
	}
	out = optSQL(t, `SELECT name FROM products WHERE id = '00000000-0000-4000-8000-000000000001'`, tab, nil)
	is = findIndex(out.Plan)
	if is == nil || !is.PK {
		t.Fatalf("want pk scan, got %s", formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM products WHERE price > 1`, tab, nil)
	if hasOp(out.Plan, "IndexScan") {
		t.Fatalf("price should seq scan: %s", formatPlan(out.Plan))
	}
	if !hasOp(out.Plan, "SeqScan") {
		t.Fatalf("want SeqScan: %s", formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM products WHERE name = 'alpha' AND price > 1`, tab, nil)
	is = findIndex(out.Plan)
	if is == nil || is.Residual == nil {
		t.Fatalf("want residual on index: %+v %s", is, formatPlan(out.Plan))
	}
}

func TestPrimaryKeyRangeSelection(t *testing.T) {
	tab := sampleTable()
	out := optSQL(t, `SELECT name FROM products WHERE id >= '00000000-0000-4000-8000-000000000010' AND id < '00000000-0000-4000-8000-000000000015'`, tab, nil)
	is := findIndex(out.Plan)
	if is == nil || !is.PK {
		t.Fatalf("want bounded pk IndexScan, got %s", formatPlan(out.Plan))
	}
	if len(is.Low) != 1 || len(is.High) != 1 || !is.LowIncl || is.HighIncl {
		t.Fatalf("bad pk range bounds: %+v", is)
	}
}

func hasOp(p planner.Logical, op string) bool {
	s := formatPlan(p)
	return strings.Contains(s, op)
}

func findIndex(p planner.Logical) *planner.IndexScan {
	switch n := p.(type) {
	case planner.IndexScan:
		return &n
	case planner.Filter:
		return findIndex(n.Input)
	case planner.Project:
		return findIndex(n.Input)
	case planner.Limit:
		return findIndex(n.Input)
	case planner.Search:
		return findIndex(n.Input)
	case planner.Nearest:
		return findIndex(n.Input)
	case planner.Candidates:
		return findIndex(n.Input)
	case planner.Rerank:
		return findIndex(n.Input)
	case planner.Join:
		if got := findIndex(n.Left); got != nil {
			return got
		}
		return findIndex(n.Right)
	case planner.Update:
		return findIndex(n.Input)
	case planner.Delete:
		return findIndex(n.Input)
	default:
		return nil
	}
}

func findNearest(p planner.Logical) *planner.Nearest {
	switch n := p.(type) {
	case planner.Nearest:
		return &n
	case planner.Filter:
		return findNearest(n.Input)
	case planner.Project:
		return findNearest(n.Input)
	case planner.Limit:
		return findNearest(n.Input)
	case planner.Search:
		return findNearest(n.Input)
	case planner.Join:
		if got := findNearest(n.Left); got != nil {
			return got
		}
		return findNearest(n.Right)
	default:
		return nil
	}
}

func findRerank(p planner.Logical) *planner.Rerank {
	switch n := p.(type) {
	case planner.Rerank:
		return &n
	case planner.Filter:
		return findRerank(n.Input)
	case planner.Project:
		return findRerank(n.Input)
	case planner.Limit:
		return findRerank(n.Input)
	case planner.Join:
		if got := findRerank(n.Left); got != nil {
			return got
		}
		return findRerank(n.Right)
	default:
		return nil
	}
}

func TestSegmentPrune(t *testing.T) {
	tab := sampleTable()
	lo := types.StringValue("m")
	hi := types.StringValue("z")
	st := &catalog.TableStats{
		Table: "products",
		Rows:  100,
		Columns: []catalog.ColumnStats{{
			Ord: 1, NDV: 50, HasMinMax: true, Min: types.StringValue("a"), Max: types.StringValue("z"),
		}},
		Segments: []catalog.SegmentStats{
			{ID: 0, Rows: 50, HasBounds: true, ColMin: []types.Value{{}, types.StringValue("a")}, ColMax: []types.Value{{}, types.StringValue("l")}},
			{ID: 1, Rows: 50, HasBounds: true, ColMin: []types.Value{{}, lo}, ColMax: []types.Value{{}, hi}},
		},
	}
	out := optSQL(t, `SELECT name FROM products WHERE name = 'q'`, tab, st)
	// name = 'q' is sargable on ix_name so IndexScan wins; use a non-indexed column for prune.
	st = &catalog.TableStats{
		Table: "products",
		Rows:  100,
		Columns: []catalog.ColumnStats{{
			Ord: 2, NDV: 10, HasMinMax: true,
			Min: types.DecimalValue(mustDec(t, "1"), types.Type{Kind: types.KindDecimal}),
			Max: types.DecimalValue(mustDec(t, "9"), types.Type{Kind: types.KindDecimal}),
		}},
		Segments: []catalog.SegmentStats{
			{
				ID: 0, Rows: 50, HasBounds: true,
				ColMin: []types.Value{{}, {}, types.DecimalValue(mustDec(t, "1"), types.Type{Kind: types.KindDecimal})},
				ColMax: []types.Value{{}, {}, types.DecimalValue(mustDec(t, "3"), types.Type{Kind: types.KindDecimal})},
			},
			{
				ID: 1, Rows: 50, HasBounds: true,
				ColMin: []types.Value{{}, {}, types.DecimalValue(mustDec(t, "8"), types.Type{Kind: types.KindDecimal})},
				ColMax: []types.Value{{}, {}, types.DecimalValue(mustDec(t, "9"), types.Type{Kind: types.KindDecimal})},
			},
		},
	}
	out = optSQL(t, `SELECT name FROM products WHERE price = 2`, tab, st)
	if _, ok := out.Plan.(planner.Empty); ok {
		return
	}
	seq := findSeq(out.Plan)
	if seq == nil {
		t.Fatalf("want seq or empty: %s", formatPlan(out.Plan))
	}
	if len(seq.Segments) != 1 || seq.Segments[0].ID != 0 {
		t.Fatalf("pruned segs %+v in %s", seq.Segments, formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM products WHERE price = 99`, tab, st)
	if !hasOp(out.Plan, "Empty") {
		t.Fatalf("out of range should be empty: %s", formatPlan(out.Plan))
	}
}

func findSeq(p planner.Logical) *planner.SeqScan {
	switch n := p.(type) {
	case planner.SeqScan:
		return &n
	case planner.Filter:
		return findSeq(n.Input)
	case planner.Project:
		return findSeq(n.Input)
	case planner.Limit:
		return findSeq(n.Input)
	default:
		return nil
	}
}

func TestSpatialIndexSelection(t *testing.T) {
	tab := &catalog.Table{
		ID:   2,
		Name: "places",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "name", Type: types.String()},
			{Name: "loc", Type: types.Point(), NotNull: true},
		},
		PK:      []int{0},
		Indexes: []catalog.Index{{Name: "ix_loc", Spatial: true, Columns: []int{2}}},
	}
	out := optSQL(t, `SELECT name FROM places WHERE DWITHIN(loc, POINT(-73.98, 40.75), 1000)`, tab, nil)
	is := findIndex(out.Plan)
	if is == nil || !is.Spatial || is.IndexName != "ix_loc" {
		t.Fatalf("want spatial IndexScan, got %s", formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM places WHERE DISTANCE(loc, POINT(-73.98, 40.75)) < 500`, tab, nil)
	is = findIndex(out.Plan)
	if is == nil || !is.Spatial {
		t.Fatalf("distance < r: %s", formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM places WHERE WITHIN(loc, BOX(-74, 40, -73, 41))`, tab, nil)
	is = findIndex(out.Plan)
	if is == nil || !is.Spatial {
		t.Fatalf("within: %s", formatPlan(out.Plan))
	}
	out = optSQL(t, `SELECT name FROM places WHERE WITHIN(loc, POLYGON('POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40))'))`, tab, nil)
	is = findIndex(out.Plan)
	if is == nil || !is.Spatial {
		t.Fatalf("within polygon: %s", formatPlan(out.Plan))
	}
}

func TestJSONPathIndexSelection(t *testing.T) {
	tab := &catalog.Table{
		ID:   3,
		Name: "products",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "name", Type: types.String()},
			{Name: "metadata", Type: types.JSON()},
		},
		PK:      []int{0},
		Indexes: []catalog.Index{{Name: "ix_cat", Columns: []int{2}, Path: []string{"category"}}},
	}
	out := optSQL(t, `SELECT name FROM products WHERE metadata.category = 'electronics'`, tab, nil)
	is := findIndex(out.Plan)
	if is == nil || is.IndexName != "ix_cat" || is.Spatial {
		t.Fatalf("want path IndexScan, got %s", formatPlan(out.Plan))
	}
	if len(is.Low) != 1 || is.Low[0].Str != "electronics" {
		t.Fatalf("low %+v", is.Low)
	}
	out = optSQL(t, `SELECT name FROM products WHERE name = 'x'`, tab, nil)
	is = findIndex(out.Plan)
	if is != nil && is.IndexName == "ix_cat" {
		t.Fatalf("should not use path index: %s", formatPlan(out.Plan))
	}
}

func TestSemiAntiJoinPlans(t *testing.T) {
	outer := &catalog.Table{
		ID:   1,
		Name: "outer_t",
		Columns: []catalog.Column{
			{Name: "id", Type: types.String(), Primary: true, NotNull: true},
			{Name: "value", Type: types.String(), NotNull: true},
		},
		PK: []int{0},
	}
	inner := &catalog.Table{
		ID:   2,
		Name: "inner_t",
		Columns: []catalog.Column{
			{Name: "id", Type: types.String(), Primary: true, NotNull: true},
			{Name: "owner", Type: types.String(), NotNull: true},
		},
		PK: []int{0},
	}
	lookup := func(name string) (*catalog.Table, bool) {
		switch name {
		case outer.Name:
			return outer, true
		case inner.Name:
			return inner, true
		}
		return nil, false
	}
	plan := func(sql string) planner.Logical {
		t.Helper()
		stmt, err := parser.Parse(sql)
		if err != nil {
			t.Fatal(err)
		}
		bound, err := binder.Bind(stmt, lookup, 1)
		if err != nil {
			t.Fatal(err)
		}
		p, err := planner.Plan(bound)
		if err != nil {
			t.Fatal(err)
		}
		out, err := Optimize(Request{Plan: p, SQL: sql, Gen: 1})
		if err != nil {
			t.Fatal(err)
		}
		return out.Plan
	}
	s := formatPlan(plan(`SELECT id FROM outer_t WHERE EXISTS (SELECT id FROM inner_t WHERE owner = outer_t.id)`))
	if !strings.Contains(s, "HashSemiJoin") {
		t.Fatalf("EXISTS: %s", s)
	}
	s = formatPlan(plan(`SELECT id FROM outer_t WHERE NOT EXISTS (SELECT id FROM inner_t WHERE owner = outer_t.id)`))
	if !strings.Contains(s, "HashAntiJoin") {
		t.Fatalf("NOT EXISTS: %s", s)
	}
	s = formatPlan(plan(`SELECT id FROM outer_t WHERE value IN (SELECT owner FROM inner_t)`))
	if !strings.Contains(s, "HashSemiJoin") {
		t.Fatalf("IN: %s", s)
	}
}

func TestDeterministicPlans(t *testing.T) {
	tab := sampleTable()
	st := &catalog.TableStats{Table: "products", Rows: 1000, Columns: []catalog.ColumnStats{{Ord: 1, NDV: 800}}}
	var first string
	for i := 0; i < 20; i++ {
		out := optSQL(t, `SELECT name, price FROM products WHERE name = 'alpha' LIMIT 10`, tab, st)
		s := formatPlan(out.Plan)
		if i == 0 {
			first = s
		} else if s != first {
			t.Fatalf("nondeterministic\n%s\nvs\n%s", first, s)
		}
	}
}

func TestPlanCache(t *testing.T) {
	tab := sampleTable()
	cache := NewCache()
	p := planSQL(t, `SELECT name FROM products WHERE name = 'x'`, tab)
	a, err := Optimize(Request{Plan: p, SQL: "SELECT name FROM products WHERE name = 'x'", Gen: 1, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if a.Cached {
		t.Fatal("first hit")
	}
	b, err := Optimize(Request{Plan: p, SQL: "SELECT name FROM products WHERE name = 'x'", Gen: 1, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if !b.Cached {
		t.Fatal("expected cache hit")
	}
	c, err := Optimize(Request{Plan: p, SQL: "SELECT name FROM products WHERE name = 'x'", Gen: 2, Cache: cache})
	if err != nil {
		t.Fatal(err)
	}
	if c.Cached {
		t.Fatal("generation change should miss")
	}
}

func TestExprIndexRangeMatch(t *testing.T) {
	expr := ast.Call{Name: "lower", Args: []ast.Expr{ast.Ident{Name: "name"}}}
	pred := ast.Binary{Op: "=", Left: expr, Right: ast.Literal{Value: types.StringValue("alpha")}}
	r, used, ok := matchExprRange(pred, expr)
	if !ok || !r.eq || r.low == nil || used == nil {
		t.Fatalf("matchExprRange ok=%v r=%+v equal=%v", ok, r, catalog.ExprEqual(expr, expr))
	}
	tab := &catalog.Table{
		Name: "items",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "name", Type: types.String(), NotNull: true},
		},
		PK: []int{0},
		Indexes: []catalog.Index{{
			Name:      "ix_lower",
			Columns:   []int{1},
			Exprs:     []ast.Expr{expr},
			ExprTypes: []types.Type{types.String()},
		}},
	}
	alt, ok := exprIndexAlt(tab, tab.Indexes[0], pred, []int{1}, nil, 1000)
	if !ok {
		t.Fatal("exprIndexAlt should match LOWER(name) = const")
	}
	scan, ok := alt.plan.(planner.IndexScan)
	if !ok || scan.IndexName != "ix_lower" {
		t.Fatalf("%T %+v", alt.plan, alt.plan)
	}
}

func TestCoveringPartialExpressionPlans(t *testing.T) {
	dec, _ := types.DecimalType(10, 0)
	tab := &catalog.Table{
		ID:   1,
		Name: "items",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), Primary: true, NotNull: true},
			{Name: "name", Type: types.String(), NotNull: true},
			{Name: "status", Type: types.String(), NotNull: true},
			{Name: "note", Type: types.Text()},
			{Name: "qty", Type: dec},
		},
		PK: []int{0},
		Indexes: []catalog.Index{
			{Name: "ix_cover", Columns: []int{1}, Include: []int{3, 4}},
			{
				Name:      "ix_active",
				Columns:   []int{1},
				Predicate: ast.Binary{Op: "=", Left: ast.Ident{Name: "status"}, Right: ast.Literal{Value: types.StringValue("active")}},
			},
			{
				Name:      "ix_lower",
				Columns:   []int{1},
				Exprs:     []ast.Expr{ast.Call{Name: "lower", Args: []ast.Expr{ast.Ident{Name: "name"}}}},
				ExprTypes: []types.Type{types.String()},
			},
		},
	}
	out := optSQL(t, `SELECT name, note, qty FROM items WHERE name = 'Beta'`, tab, nil)
	s := formatPlan(out.Plan)
	if !strings.Contains(s, "ix_cover") || !strings.Contains(s, "covering") {
		t.Fatalf("covering: %s", s)
	}
	out = optSQL(t, `SELECT name FROM items WHERE name = 'Alpha' AND status = 'active'`, tab, nil)
	s = formatPlan(out.Plan)
	if !strings.Contains(s, "ix_active") {
		t.Fatalf("partial implied: %s", s)
	}
	out = optSQL(t, `SELECT name FROM items WHERE name = 'Beta' AND status = 'inactive'`, tab, nil)
	s = formatPlan(out.Plan)
	if strings.Contains(s, "ix_active") {
		t.Fatalf("partial not implied: %s", s)
	}
	out = optSQL(t, `SELECT name FROM items WHERE LOWER(name) = 'alpha'`, tab, nil)
	s = formatPlan(out.Plan)
	if !strings.Contains(s, "ix_lower") {
		t.Fatalf("expression: %s", s)
	}
	out = optSQL(t, `SELECT name FROM items WHERE name = 'alpha'`, tab, nil)
	s = formatPlan(out.Plan)
	if strings.Contains(s, "ix_lower") {
		t.Fatalf("expression used for raw column: %s", s)
	}
}

func mustDec(t *testing.T, s string) types.Decimal {
	t.Helper()
	d, err := types.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
