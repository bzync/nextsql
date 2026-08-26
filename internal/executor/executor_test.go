package executor

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor/vector"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/wal"
)

func testKeys(t testing.TB) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func TestOpenCreatesMissingPrimaryTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	eng, err := storage.Create(path, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Session().Exec(`CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
}

func testDB(t testing.TB) *DB {
	t.Helper()
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func execOK(t *testing.T, s *Session, sql string) *Result {
	t.Helper()
	res, err := s.Exec(sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return res
}

func TestStatementsAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2),
		note TEXT,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	execOK(t, s, `INSERT INTO products (name, price) VALUES ('alpha', 1000), ('beta', 2500.50), ('gamma', 5000)`)
	execOK(t, s, `CREATE INDEX ix_name ON products (name)`)
	res := execOK(t, s, `SELECT name, price FROM products WHERE price BETWEEN 1000 AND 4000`)
	if len(res.Rows) != 2 {
		t.Fatalf("got %d rows", len(res.Rows))
	}
	upd := execOK(t, s, `UPDATE products SET price = 1100 WHERE name = 'alpha'`)
	if upd.Affected != 1 {
		t.Fatalf("updated %d", upd.Affected)
	}
	del := execOK(t, s, `DELETE FROM products WHERE name = 'gamma'`)
	if del.Affected != 1 {
		t.Fatalf("deleted %d", del.Affected)
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
	res = execOK(t, s, `SELECT name, price FROM products`)
	if len(res.Rows) != 2 {
		t.Fatalf("after restart %d rows", len(res.Rows))
	}
	names := map[string]string{}
	for _, r := range res.Rows {
		names[r[0].Str] = r[1].Dec.String()
	}
	if names["alpha"] != "1100.00" || names["beta"] != "2500.50" {
		t.Fatalf("%v", names)
	}
	if _, err := s.Exec(`SELECT * FROM missing`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("missing table: %v", err)
	}
}

func TestTypedParameters(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL, qty DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO t (n, qty) VALUES ('alpha', 3), ('beta', 9)`)
	res, err := s.ExecContext(context.Background(), `SELECT n FROM t WHERE n = $1`, []Param{{Value: types.StringValue("beta")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "beta" {
		t.Fatalf("%+v", res.Rows)
	}
	upd, err := s.ExecContext(context.Background(), `UPDATE t SET qty = $1 WHERE n = $2`, []Param{
		{Value: types.DecimalValue(mustDec(t, "12"), types.Type{Kind: types.KindDecimal, Precision: 10})},
		{Name: "2", Value: types.StringValue("alpha")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if upd.Affected != 1 {
		t.Fatalf("updated %d", upd.Affected)
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

func TestBeginCommitRollback(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('keep')`)

	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('temp')`)
	got := execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != 2 {
		t.Fatalf("in txn: %d", len(got.Rows))
	}
	other := db.Session()
	seen := execOK(t, other, `SELECT n FROM t`)
	if len(seen.Rows) != 1 {
		t.Fatalf("other session dirty read: %d", len(seen.Rows))
	}
	execOK(t, s, `ROLLBACK`)
	got = execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "keep" {
		t.Fatalf("after rollback: %+v", got.Rows)
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('committed')`)
	execOK(t, s, `COMMIT`)
	got = execOK(t, other, `SELECT n FROM t`)
	if len(got.Rows) != 2 {
		t.Fatalf("after commit: %d", len(got.Rows))
	}
}

func TestCreateTableRollback(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('x')`)
	execOK(t, s, `ROLLBACK`)
	if _, err := s.Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("rolled back table still visible: %v", err)
	}
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('y')`)
	got := execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "y" {
		t.Fatalf("%+v", got.Rows)
	}
}

func TestDefaultsAndTypes(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (
		id UUID PRIMARY KEY DEFAULT UUID(),
		meta JSON,
		vec VECTOR<F32,3>,
		ts TIMESTAMPTZ DEFAULT NOW(),
		flag STRING
	)`)
	execOK(t, s, `INSERT INTO t (meta, vec) VALUES ('{"a":1}', (1, 2, 3))`)
	got := execOK(t, s, `SELECT id, meta, vec, ts FROM t`)
	if len(got.Rows) != 1 {
		t.Fatal(len(got.Rows))
	}
	row := got.Rows[0]
	if row[0].Typ.Kind != types.KindUUID || row[0].Null {
		t.Fatalf("uuid %+v", row[0])
	}
	if row[1].String() != `{"a":1}` {
		t.Fatalf("json %s", row[1].String())
	}
	if len(row[1].JSON) < 4 || string(row[1].JSON[:4]) != "NSJB" {
		t.Fatalf("json stored form %q", row[1].JSON)
	}
	if len(row[2].Vec) != 3 || row[2].Vec[2] != 3 {
		t.Fatalf("vec %+v", row[2].Vec)
	}
	if row[3].Null || row[3].Time == 0 {
		t.Fatalf("ts %+v", row[3])
	}
}

func TestConcurrentBeginCommit(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)

	const workers = 8
	var wg sync.WaitGroup
	errc := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ss := db.Session()
			if _, err := ss.Exec(`BEGIN`); err != nil {
				errc <- err
				return
			}
			name := string(rune('a' + i))
			if _, err := ss.Exec(`INSERT INTO t (n) VALUES ('` + name + `')`); err != nil {
				_ = ss.abort()
				errc <- err
				return
			}
			if _, err := ss.Exec(`COMMIT`); err != nil {
				errc <- err
			}
		}(i)
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
	got := execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != workers {
		t.Fatalf("got %d want %d", len(got.Rows), workers)
	}
}

func TestSQLSurvivesKill(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('durable')`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('lost')`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "durable" {
		t.Fatalf("after kill: %+v", valuesOf(got))
	}
}

func valuesOf(res *Result) []string {
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 {
			out = append(out, r[0].String())
		}
	}
	return out
}

func TestIndexBuildCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b')`)
	db.Eng.SetCrash(wal.PointDuringIndexBuild)
	if _, err := s.Exec(`CREATE INDEX ix ON t (n)`); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != 2 {
		t.Fatalf("rows after index-build crash: %d", len(got.Rows))
	}
	// Index must not be in the recovered catalog.
	if _, err := s.Exec(`CREATE INDEX ix ON t (n)`); err != nil {
		t.Fatal(err)
	}
}

func TestParserRejectsGarbage(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`???`); !nerr.HasCode(err, nerr.Syntax) {
		t.Fatalf("%v", err)
	}
}

func TestExplainAndIndexScan(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2)
	)`)
	execOK(t, s, `INSERT INTO products (name, price) VALUES ('alpha', 10), ('beta', 20), ('gamma', 30)`)
	execOK(t, s, `CREATE INDEX ix_name ON products (name)`)
	got := execOK(t, s, `SELECT name, price FROM products WHERE name = 'beta'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "beta" {
		t.Fatalf("index lookup: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT name FROM products WHERE name = 'beta'`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "ix_name") {
		t.Fatalf("explain index: %+v", explainOps(plan))
	}
	plan = execOK(t, s, `EXPLAIN SELECT name FROM products WHERE price > 15`)
	if !explainHas(plan, "SeqScan") {
		t.Fatalf("explain seq: %+v", explainOps(plan))
	}
	an := execOK(t, s, `EXPLAIN ANALYZE SELECT name FROM products WHERE name = 'beta'`)
	if !explainHas(an, "IndexScan") {
		t.Fatalf("analyze: %+v", explainOps(an))
	}
	// actuals column
	if len(an.Columns) != 11 {
		t.Fatalf("explain columns %v", an.Columns)
	}
	upd := execOK(t, s, `UPDATE products SET price = 21 WHERE name = 'beta'`)
	if upd.Affected != 1 {
		t.Fatalf("updated %d", upd.Affected)
	}
	del := execOK(t, s, `DELETE FROM products WHERE name = 'gamma'`)
	if del.Affected != 1 {
		t.Fatalf("deleted %d", del.Affected)
	}
	got = execOK(t, s, `SELECT name FROM products`)
	if len(got.Rows) != 2 {
		t.Fatalf("after dml %d", len(got.Rows))
	}
}

func TestPrimaryKeyRangeScan(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE ranges (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO ranges (id, n) VALUES
		('s09998', 'before'), ('s10000', 'a'), ('s10001', 'b'),
		('s14999', 'c'), ('s15000', 'after')`)

	plan := execOK(t, s, `EXPLAIN SELECT id FROM ranges WHERE id >= 's10000' AND id < 's15000'`)
	if !explainHas(plan, "IndexScan") {
		t.Fatalf("want pk IndexScan: %+v", explainOps(plan))
	}
	got := execOK(t, s, `SELECT id FROM ranges WHERE id >= 's10000' AND id < 's15000'`)
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "s10000" || got.Rows[2][0].Str != "s14999" {
		t.Fatalf("range rows: %+v", got.Rows)
	}
}

func TestAnalyzeStatsSurviveRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL, k DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO t (n, k) VALUES ('a', 1), ('b', 2), ('c', 3)`)
	execOK(t, s, `ANALYZE t`)
	if _, ok := db.Cat.Stats("t"); !ok {
		t.Fatal("stats missing after ANALYZE")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	st, ok := db.Cat.Stats("t")
	if !ok || st.Rows != 3 {
		t.Fatalf("stats after restart: ok=%v %+v", ok, st)
	}
	s = db.Session()
	plan := execOK(t, s, `EXPLAIN SELECT n FROM t WHERE k = 99`)
	if !explainHas(plan, "Empty") && !explainHas(plan, "SeqScan") {
		t.Fatalf("pruned or seq: %+v", explainOps(plan))
	}
}

func TestAutomaticStatisticsRefreshPolicy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE auto_stats (id STRING PRIMARY KEY, value DECIMAL(10,0))`)
	var sql strings.Builder
	sql.WriteString(`INSERT INTO auto_stats (id, value) VALUES `)
	for i := 0; i < autoAnalyzeMinChanges; i++ {
		if i > 0 {
			sql.WriteByte(',')
		}
		fmt.Fprintf(&sql, "('%d',%d)", i, i)
	}
	execOK(t, s, sql.String())
	st, ok := db.Cat.Stats("auto_stats")
	if !ok || st.Rows != autoAnalyzeMinChanges {
		t.Fatalf("automatic stats missing or stale: ok=%v stats=%+v", ok, st)
	}
}

func explainHas(res *Result, sub string) bool {
	for _, r := range res.Rows {
		for _, v := range r {
			if strings.Contains(v.String(), sub) {
				return true
			}
		}
	}
	return false
}

func TestGeospatialSQL(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE places (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		loc POINT NOT NULL
	)`)
	execOK(t, s, `INSERT INTO places (name, loc) VALUES
		('empire', POINT(-73.9857, 40.7484)),
		('central_park', POINT(-73.9654, 40.7829)),
		('jfk', POINT(-73.7781, 40.6413)),
		('tokyo', POINT(139.6917, 35.6895))`)
	execOK(t, s, `CREATE SPATIAL INDEX ix_loc ON places (loc)`)

	got := execOK(t, s, `SELECT name FROM places WHERE DWITHIN(loc, POINT(-73.9857, 40.7484), 5000)`)
	names := map[string]bool{}
	for _, r := range got.Rows {
		names[r[0].Str] = true
	}
	if !names["empire"] || !names["central_park"] || names["tokyo"] {
		t.Fatalf("dwithin 5km: %v", names)
	}

	got = execOK(t, s, `SELECT name FROM places WHERE WITHIN(loc, BOX(-74.1, 40.60, -73.90, 40.80))`)
	names = map[string]bool{}
	for _, r := range got.Rows {
		names[r[0].Str] = true
	}
	if !names["empire"] || names["tokyo"] {
		t.Fatalf("within box: %v", names)
	}

	got = execOK(t, s, `SELECT name, DISTANCE(loc, POINT(-73.9857, 40.7484)) FROM places WHERE name = 'empire'`)
	if len(got.Rows) != 1 {
		t.Fatal(len(got.Rows))
	}
	// same point ≈ 0
	if got.Rows[0][1].Dec.String() != "0.000" {
		t.Fatalf("self distance %s", got.Rows[0][1].Dec.String())
	}

	got = execOK(t, s, `SELECT LON(loc), LAT(loc) FROM places WHERE name = 'tokyo'`)
	if len(got.Rows) != 1 {
		t.Fatal(len(got.Rows))
	}

	plan := execOK(t, s, `EXPLAIN SELECT name FROM places WHERE DWITHIN(loc, POINT(-73.9857, 40.7484), 2000)`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "ix_loc") {
		t.Fatalf("spatial plan: %+v", explainOps(plan))
	}

	// WKT coerce
	execOK(t, s, `INSERT INTO places (name, loc) VALUES ('wkt', 'POINT(-74.0 40.7)')`)
	got = execOK(t, s, `SELECT name FROM places WHERE name = 'wkt'`)
	if len(got.Rows) != 1 {
		t.Fatal("wkt insert")
	}

	if _, err := s.Exec(`INSERT INTO places (name, loc) VALUES ('bad', POINT(200, 0))`); err == nil {
		t.Fatal("expected invalid lon")
	}

	got = execOK(t, s, `SELECT name FROM places WHERE WITHIN(loc, POLYGON('((-74.1 40.60, -73.90 40.60, -73.90 40.80, -74.1 40.80, -74.1 40.60))'))`)
	names = map[string]bool{}
	for _, r := range got.Rows {
		names[r[0].Str] = true
	}
	if !names["empire"] || names["tokyo"] {
		t.Fatalf("within polygon: %v", names)
	}

	plan = execOK(t, s, `EXPLAIN SELECT name FROM places WHERE WITHIN(loc, POLYGON('((-74.1 40.60, -73.90 40.60, -73.90 40.80, -74.1 40.80, -74.1 40.60))'))`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "ix_loc") {
		t.Fatalf("polygon spatial plan: %+v", explainOps(plan))
	}

	got = execOK(t, s, `SELECT DISTANCE_SPHEROID(POINT(-74.0060, 40.7128), POINT(-118.2437, 34.0522)) FROM places WHERE name = 'empire'`)
	if len(got.Rows) != 1 {
		t.Fatal(len(got.Rows))
	}
	sph, err := strconv.ParseFloat(got.Rows[0][0].Dec.String(), 64)
	if err != nil || sph < 3.943e6 || sph > 3.946e6 {
		t.Fatalf("spheroid %s %v", got.Rows[0][0].Dec.String(), err)
	}
}

func TestGeospatialRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE spots (id UUID PRIMARY KEY DEFAULT UUID(), loc POINT NOT NULL)`)
	execOK(t, s, `INSERT INTO spots (loc) VALUES (POINT(10, 20))`)
	execOK(t, s, `CREATE SPATIAL INDEX ix ON spots (loc)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT LON(loc), LAT(loc) FROM spots`)
	if len(got.Rows) != 1 {
		t.Fatal(len(got.Rows))
	}
	got = execOK(t, s, `SELECT loc FROM spots WHERE DWITHIN(loc, POINT(10, 20), 10)`)
	if len(got.Rows) != 1 {
		t.Fatalf("after restart %d", len(got.Rows))
	}
}

func TestGeospatialLinePolygonRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE shapes (
		id UUID PRIMARY KEY DEFAULT UUID(),
		route LINESTRING NOT NULL,
		zone POLYGON NOT NULL
	)`)
	execOK(t, s, `INSERT INTO shapes (route, zone) VALUES (
		'LINESTRING(-74.0 40.7, -73.9 40.8)',
		'POLYGON((-74.1 40.6, -73.8 40.6, -73.8 40.9, -74.1 40.9, -74.1 40.6))'
	)`)
	got := execOK(t, s, `SELECT ST_Length(route) FROM shapes`)
	if len(got.Rows) != 1 {
		t.Fatal(len(got.Rows))
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
	got = execOK(t, s, `SELECT ST_Length(route) FROM shapes`)
	if len(got.Rows) != 1 {
		t.Fatalf("line after restart %d", len(got.Rows))
	}
	got = execOK(t, s, `SELECT 1 FROM shapes WHERE WITHIN(POINT(-73.9857, 40.7484), zone)`)
	if len(got.Rows) != 1 {
		t.Fatalf("polygon after restart %d", len(got.Rows))
	}
}

func TestJSONPathAndIndex(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		metadata JSON
	)`)
	execOK(t, s, `INSERT INTO products (name, metadata) VALUES
		('alpha', '{"category":"electronics","n":1}'),
		('beta', '{"category":"books","n":2}'),
		('gamma', '{"category":"electronics","n":3}'),
		('delta', '{"n":4}')`)
	execOK(t, s, `CREATE INDEX category_index ON products (metadata.category)`)

	got := execOK(t, s, `SELECT metadata.category FROM products WHERE name = 'alpha'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "electronics" {
		t.Fatalf("extract %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT name FROM products WHERE metadata.category = 'electronics'`)
	names := map[string]bool{}
	for _, r := range got.Rows {
		names[r[0].Str] = true
	}
	if !names["alpha"] || !names["gamma"] || names["beta"] || names["delta"] {
		t.Fatalf("where path: %v", names)
	}

	got = execOK(t, s, `SELECT name FROM products WHERE metadata.n = 2`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "beta" {
		t.Fatalf("numeric path %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT name FROM products WHERE metadata.category IS NULL`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "delta" {
		t.Fatalf("missing path %+v", got.Rows)
	}

	plan := execOK(t, s, `EXPLAIN SELECT name FROM products WHERE metadata.category = 'books'`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "category_index") {
		t.Fatalf("path index plan: %+v", explainOps(plan))
	}

	upd := execOK(t, s, `UPDATE products SET metadata = '{"category":"music","n":1}' WHERE name = 'alpha'`)
	if upd.Affected != 1 {
		t.Fatalf("updated %d", upd.Affected)
	}
	got = execOK(t, s, `SELECT name FROM products WHERE metadata.category = 'electronics'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "gamma" {
		t.Fatalf("after update %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT name FROM products WHERE metadata.category = 'music'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "alpha" {
		t.Fatalf("updated path %+v", got.Rows)
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO products (name, metadata) VALUES ('txn', '{"category":"txn"}')`)
	got = execOK(t, s, `SELECT name FROM products WHERE metadata.category = 'txn'`)
	if len(got.Rows) != 1 {
		t.Fatal("in-txn path")
	}
	execOK(t, s, `ROLLBACK`)
	got = execOK(t, s, `SELECT name FROM products WHERE metadata.category = 'txn'`)
	if len(got.Rows) != 0 {
		t.Fatalf("rolled back path %+v", got.Rows)
	}

	if _, err := s.Exec(`INSERT INTO products (name, metadata) VALUES ('bad', '{')`); err == nil {
		t.Fatal("expected invalid JSON")
	}
}

func TestJSONRestartAndNoPlaintext(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (id UUID PRIMARY KEY DEFAULT UUID(), metadata JSON)`)
	marker := `{"category":"electronics","secret":"PLAINTEXT_JSON_MARKER"}`
	execOK(t, s, `INSERT INTO products (metadata) VALUES ('`+marker+`')`)
	execOK(t, s, `CREATE INDEX ix_cat ON products (metadata.category)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte("PLAINTEXT_JSON_MARKER")) {
		t.Fatal("plaintext JSON on disk")
	}
	for _, sibling := range []string{path + ".wal", path + ".undo"} {
		_ = filepath.Walk(sibling, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.Contains(b, []byte(marker)) || bytes.Contains(b, []byte("PLAINTEXT_JSON_MARKER")) {
				t.Errorf("plaintext JSON in %s", p)
			}
			return nil
		})
	}

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT metadata.category FROM products`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "electronics" {
		t.Fatalf("after restart %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT metadata FROM products WHERE metadata.category = 'electronics'`)
	if len(got.Rows) != 1 || !strings.Contains(got.Rows[0][0].String(), "electronics") {
		t.Fatalf("index after restart %+v", got.Rows)
	}
}

func explainOps(res *Result) []string {
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		if len(r) > 0 {
			out = append(out, r[0].String())
		}
	}
	return out
}

func TestChunkedUpdateDelete(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 128)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 2, Memory: 32 << 20, Disk: 32 << 20, IO: 1 << 30, Time: time.Minute, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE scan (id STRING PRIMARY KEY, k STRING NOT NULL, n DECIMAL(10,0) NOT NULL)`)
	const n = 9000
	for start := 0; start < n; start += 250 {
		end := start + 250
		if end > n {
			end = n
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO scan (id, k, n) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `('s%d', '%c', %d)`, i, 'a'+rune(i%10), i)
		}
		execOK(t, s, b.String())
	}
	part := execOK(t, s, `UPDATE scan SET n = 1 LIMIT 100`)
	if part.Affected != 100 {
		t.Fatalf("limited update %d", part.Affected)
	}
	nset, err := s.BulkSetDecimal("scan", "n", "0")
	if err != nil {
		t.Fatal(err)
	}
	if nset != n {
		t.Fatalf("bulk set %d want %d", nset, n)
	}
	upd := execOK(t, s, `UPDATE scan SET n = 0`)
	if upd.Affected != n {
		t.Fatalf("updated %d want %d", upd.Affected, n)
	}
	got := execOK(t, s, `SELECT COUNT(*) FROM scan`)
	if got.Rows[0][0].Dec.String() != strconv.Itoa(n) {
		t.Fatalf("count after update %v", got.Rows)
	}
	got = execOK(t, s, `SELECT n FROM scan WHERE id = 's42'`)
	if got.Rows[0][0].Dec.String() != "0" {
		t.Fatalf("row after update %v", got.Rows)
	}
	ndel, err := s.BulkDeleteAll("scan")
	if err != nil {
		t.Fatal(err)
	}
	if ndel != n {
		t.Fatalf("bulk delete %d want %d", ndel, n)
	}
	del := execOK(t, s, `DELETE FROM scan`)
	if del.Affected != 0 {
		t.Fatalf("second delete %d", del.Affected)
	}
	got = execOK(t, s, `SELECT COUNT(*) FROM scan`)
	if got.Rows[0][0].Dec.String() != "0" {
		t.Fatalf("count after delete %v", got.Rows)
	}
}

func TestBulkDeleteMany(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 256)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, Time: 2 * time.Minute, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE scan (id STRING PRIMARY KEY, k STRING NOT NULL, n DECIMAL(10,0) NOT NULL)`)
	const n = 20000
	for start := 0; start < n; start += 250 {
		end := start + 250
		if end > n {
			end = n
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO scan (id, k, n) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `('s%d', '%c', %d)`, i, 'a'+rune(i%10), i)
		}
		execOK(t, s, b.String())
	}
	got := execOK(t, s, `SELECT COUNT(*) FROM scan`)
	if got.Rows[0][0].Dec.String() != strconv.Itoa(n) {
		t.Fatalf("precount %v", got.Rows)
	}
	ndel, err := s.BulkDeleteAll("scan")
	if err != nil {
		t.Fatal(err)
	}
	if ndel != n {
		t.Fatalf("deleted %d want %d", ndel, n)
	}
	got = execOK(t, s, `SELECT COUNT(*) FROM scan`)
	if got.Rows[0][0].Dec.String() != "0" {
		t.Fatalf("postcount %v", got.Rows)
	}
}

func seedHybrid256(t *testing.T, s *Session) {
	t.Helper()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2),
		description TEXT,
		metadata JSON,
		embedding VECTOR<F32,8>
	)`)
	const hn = 256
	var b strings.Builder
	b.WriteString(`INSERT INTO products (name, price, description, metadata, embedding) VALUES `)
	for i := 0; i < hn; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		cat := "headphones"
		if i%5 == 0 {
			cat = "home"
		}
		fmt.Fprintf(&b, `('p%d', %d, 'wireless noise cancelling item %d', '{"category":"%s"}', (%d, %d, 0, 0, 0, 0, 0, 0))`,
			i, 1000+i*10, i, cat, i%3, 3-i%3)
	}
	execOK(t, s, b.String())
	execOK(t, s, `CREATE INDEX ix_prod_cat ON products (metadata.category)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_prod_desc ON products (description)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_prod_emb ON products (embedding) USING HNSW`)
	got := execOK(t, s, `SELECT COUNT(*) FROM products`)
	if got.Rows[0][0].Dec.String() != strconv.Itoa(hn) {
		t.Fatalf("hybrid count %v", got.Rows)
	}
}

func TestHybridSeed256(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, Time: 2 * time.Minute, BatchSize: 1024})
	seedHybrid256(t, s)
}

func TestHybridSeedAfterBulkDelete(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, Time: 2 * time.Minute, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE scan (id STRING PRIMARY KEY, k STRING NOT NULL, n DECIMAL(10,0) NOT NULL)`)
	const n = 100000
	for start := 0; start < n; start += 256 {
		end := start + 256
		if end > n {
			end = n
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO scan (id, k, n) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `('s%d', '%c', %d)`, i, 'a'+rune(i%10), i)
		}
		execOK(t, s, b.String())
	}
	if _, err := s.BulkDeleteAll("scan"); err != nil {
		t.Fatalf("bulk delete: %v", err)
	}
	seedHybrid256(t, s)
}

func TestBulkDeleteSoak(t *testing.T) {
	n := 25_000
	if v := os.Getenv("NEXTSQL_SOAK_ROWS"); v != "" {
		parsed, err := strconv.Atoi(v)
		if err != nil || parsed < 1 {
			t.Fatalf("NEXTSQL_SOAK_ROWS=%q", v)
		}
		n = parsed
	} else if testing.Short() {
		t.Skip("short")
	}
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 2048)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, Time: 4 * time.Hour, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE scan (id STRING PRIMARY KEY, k STRING NOT NULL, n DECIMAL(10,0) NOT NULL)`)
	for start := 0; start < n; start += 256 {
		end := start + 256
		if end > n {
			end = n
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO scan (id, k, n) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `('s%d', '%c', %d)`, i, 'a'+rune(i%10), i)
		}
		execOK(t, s, b.String())
	}
	got := execOK(t, s, `SELECT COUNT(*) FROM scan`)
	if got.Rows[0][0].Dec.String() != strconv.Itoa(n) {
		t.Fatalf("precount %v", got.Rows)
	}
	ndel, err := s.BulkDeleteAll("scan")
	if err != nil {
		t.Fatalf("soak delete: %v", err)
	}
	if ndel != int64(n) {
		t.Fatalf("deleted %d want %d", ndel, n)
	}
	got = execOK(t, s, `SELECT COUNT(*) FROM scan`)
	if got.Rows[0][0].Dec.String() != "0" {
		t.Fatalf("postcount %v", got.Rows)
	}
	if _, err := s.Exec(`INSERT INTO scan (id, k, n) VALUES ('again', 'z', 1)`); err != nil {
		t.Fatal(err)
	}
}

func TestJoinAndAggregate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id UUID PRIMARY KEY DEFAULT UUID(), k STRING NOT NULL, qty DECIMAL(10,0))`)
	execOK(t, s, `CREATE TABLE items (id UUID PRIMARY KEY DEFAULT UUID(), k STRING NOT NULL, name STRING)`)
	execOK(t, s, `INSERT INTO orders (k, qty) VALUES ('a', 1), ('a', 2), ('b', 5)`)
	execOK(t, s, `INSERT INTO items (k, name) VALUES ('a', 'alpha'), ('b', 'beta')`)

	got := execOK(t, s, `SELECT COUNT(*) FROM orders`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "3" {
		t.Fatalf("count %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT k, COUNT(*) FROM orders GROUP BY k`)
	if len(got.Rows) != 2 {
		t.Fatalf("groups %d", len(got.Rows))
	}
	got = execOK(t, s, `SELECT orders.k, items.name FROM orders JOIN items ON orders.k = items.k`)
	if len(got.Rows) != 3 {
		t.Fatalf("join %d rows %+v", len(got.Rows), got.Rows)
	}
	got = execOK(t, s, `SELECT COUNT(*) FROM orders JOIN items ON orders.k = items.k`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "3" {
		t.Fatalf("join count %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT orders.k, items.name FROM orders JOIN items ON orders.k = items.k`)
	ops := strings.Join(explainOps(plan), " ")
	if !strings.Contains(ops, "Join") && !strings.Contains(ops, "HashJoin") && !strings.Contains(ops, "MergeJoin") {
		t.Fatalf("explain %s", ops)
	}
}

func TestLeftDeepInnerJoins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	for _, name := range []string{"a", "b", "c", "d", "e", "f", "g", "h", "i"} {
		execOK(t, s, `CREATE TABLE `+name+` (id UUID PRIMARY KEY DEFAULT UUID(), k STRING, n STRING)`)
	}
	execOK(t, s, `INSERT INTO a (k, n) VALUES ('x', 'a1'), ('y', 'a2')`)
	for _, name := range []string{"b", "c", "d", "e", "f", "g", "h"} {
		execOK(t, s, `INSERT INTO `+name+` (k, n) VALUES ('x', '`+name+`1')`)
	}
	got := execOK(t, s, `SELECT a.n, b.n, c.n FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k`)
	if len(got.Rows) != 1 {
		t.Fatalf("3-way join %d rows %+v", len(got.Rows), got.Rows)
	}
	got = execOK(t, s, `SELECT a.n, b.n, c.n, d.n FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k`)
	if len(got.Rows) != 1 {
		t.Fatalf("4-way join %d rows %+v", len(got.Rows), got.Rows)
	}
	got = execOK(t, s, `SELECT a.n FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k JOIN e ON a.k = e.k JOIN f ON a.k = f.k JOIN g ON a.k = g.k JOIN h ON a.k = h.k`)
	if len(got.Rows) != 1 {
		t.Fatalf("8-way join %d rows %+v", len(got.Rows), got.Rows)
	}
	if _, err := s.Exec(`SELECT a.n FROM a JOIN b ON a.k = b.k JOIN c ON a.k = c.k JOIN d ON a.k = d.k JOIN e ON a.k = e.k JOIN f ON a.k = f.k JOIN g ON a.k = g.k JOIN h ON a.k = h.k JOIN i ON a.k = i.k`); err == nil {
		t.Fatal("expected 9-table join to be rejected")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument, got %v", err)
	}
}

func TestJoinReorderPreservesResults(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE small (id UUID PRIMARY KEY DEFAULT UUID(), k STRING, n STRING)`)
	execOK(t, s, `CREATE TABLE big (id UUID PRIMARY KEY DEFAULT UUID(), k STRING, n STRING)`)
	execOK(t, s, `INSERT INTO small (k, n) VALUES ('x', 's1'), ('z', 's2')`)
	for i := 0; i < 80; i++ {
		k := "n"
		if i < 2 {
			k = "x"
		}
		execOK(t, s, `INSERT INTO big (k, n) VALUES ('`+k+`', 'b`+strconv.Itoa(i)+`')`)
	}
	execOK(t, s, `ANALYZE`)
	got := execOK(t, s, `SELECT small.n, big.n FROM small JOIN big ON small.k = big.k ORDER BY small.n, big.n`)
	if len(got.Rows) != 2 {
		t.Fatalf("join rows %d %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0][0].Str != "s1" || got.Rows[1][0].Str != "s1" {
		t.Fatalf("small.n after reorder %+v", got.Rows)
	}
	star := execOK(t, s, `SELECT * FROM small JOIN big ON small.k = big.k`)
	if len(star.Columns) != 6 {
		t.Fatalf("SELECT * columns %v", star.Columns)
	}
	if star.Columns[0] != "small.id" || star.Columns[3] != "big.id" {
		t.Fatalf("SELECT * column order %v", star.Columns)
	}
	grouped := execOK(t, s, `SELECT small.k, COUNT(*) FROM small JOIN big ON small.k = big.k GROUP BY small.k`)
	if len(grouped.Rows) != 1 || grouped.Rows[0][0].Str != "x" || grouped.Rows[0][1].Dec.String() != "2" {
		t.Fatalf("group after reorder %+v", grouped.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT small.n, big.n FROM small JOIN big ON small.k = big.k`)
	ops := strings.Join(explainOps(plan), "\n")
	bigPos := strings.Index(ops, "SeqScan big")
	smallPos := strings.Index(ops, "SeqScan small")
	if bigPos < 0 || smallPos < 0 {
		t.Fatalf("want both scans in explain:\n%s", ops)
	}
	if bigPos > smallPos {
		t.Fatalf("want big as probe (left), small as build (right):\n%s", ops)
	}
	nulls := execOK(t, s, `SELECT small.n, big.n FROM small JOIN big ON small.k = big.k WHERE small.n IS NOT NULL`)
	if len(nulls.Rows) != 2 {
		t.Fatalf("null-safe reorder %d %+v", len(nulls.Rows), nulls.Rows)
	}
}

func TestJoinNullKeysDoNotMatch(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE lhs (id UUID PRIMARY KEY DEFAULT UUID(), k STRING, n STRING)`)
	execOK(t, s, `CREATE TABLE rhs (id UUID PRIMARY KEY DEFAULT UUID(), k STRING, n STRING)`)
	execOK(t, s, `INSERT INTO lhs (k, n) VALUES (NULL, 'ln'), ('1', 'l1')`)
	execOK(t, s, `INSERT INTO rhs (k, n) VALUES (NULL, 'rn'), ('1', 'r1')`)
	got := execOK(t, s, `SELECT lhs.n, rhs.n FROM lhs JOIN rhs ON lhs.k = rhs.k`)
	if len(got.Rows) != 1 {
		t.Fatalf("hash null join %d rows %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0][0].Str != "l1" || got.Rows[0][1].Str != "r1" {
		t.Fatalf("row %+v", got.Rows[0])
	}
}

func TestLeftJoinUnmatched(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE customers (id STRING PRIMARY KEY, name STRING NOT NULL)`)
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, customer_id STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO customers (id, name) VALUES ('c1', 'alice'), ('c2', 'bob')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o1', 'c1')`)
	got := execOK(t, s, `SELECT customers.name, orders.id FROM customers LEFT JOIN orders ON orders.customer_id = customers.id`)
	if len(got.Rows) != 2 {
		t.Fatalf("left join %d rows %+v", len(got.Rows), got.Rows)
	}
	var sawAlice, sawBobNull bool
	for _, row := range got.Rows {
		if row[0].Str == "alice" && !row[1].Null && row[1].Str == "o1" {
			sawAlice = true
		}
		if row[0].Str == "bob" && row[1].Null {
			sawBobNull = true
		}
	}
	if !sawAlice || !sawBobNull {
		t.Fatalf("rows %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT customers.name, orders.id FROM customers LEFT OUTER JOIN orders ON orders.customer_id = customers.id`)
	if len(got.Rows) != 2 {
		t.Fatalf("left outer %d", len(got.Rows))
	}
	plan := execOK(t, s, `EXPLAIN SELECT customers.name, orders.id FROM customers LEFT JOIN orders ON orders.customer_id = customers.id`)
	ops := strings.Join(explainOps(plan), " ")
	if !strings.Contains(ops, "LeftJoin") {
		t.Fatalf("explain %s", ops)
	}
	// empty right
	execOK(t, s, `DELETE FROM orders`)
	got = execOK(t, s, `SELECT customers.name, orders.id FROM customers LEFT JOIN orders ON orders.customer_id = customers.id`)
	if len(got.Rows) != 2 {
		t.Fatalf("empty right %d %+v", len(got.Rows), got.Rows)
	}
	for _, row := range got.Rows {
		if !row[1].Null {
			t.Fatalf("want null order id, got %+v", row)
		}
	}
}

func TestRightJoinUnmatched(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE customers (id STRING PRIMARY KEY, name STRING NOT NULL)`)
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, customer_id STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO customers (id, name) VALUES ('c1', 'alice'), ('c2', 'bob')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o1', 'c1')`)
	got := execOK(t, s, `SELECT customers.name, orders.id FROM orders RIGHT JOIN customers ON orders.customer_id = customers.id`)
	if len(got.Rows) != 2 {
		t.Fatalf("right join %d rows %+v", len(got.Rows), got.Rows)
	}
	var sawAlice, sawBobNull bool
	for _, row := range got.Rows {
		if row[0].Str == "alice" && !row[1].Null && row[1].Str == "o1" {
			sawAlice = true
		}
		if row[0].Str == "bob" && row[1].Null {
			sawBobNull = true
		}
	}
	if !sawAlice || !sawBobNull {
		t.Fatalf("rows %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT customers.name, orders.id FROM orders RIGHT JOIN customers ON orders.customer_id = customers.id`)
	ops := strings.Join(explainOps(plan), " ")
	if !strings.Contains(ops, "LeftJoin") {
		t.Fatalf("RIGHT should rewrite to LeftJoin, explain %s", ops)
	}
}

func TestFullOuterJoin(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE lhs (id STRING PRIMARY KEY, k STRING, n STRING)`)
	execOK(t, s, `CREATE TABLE rhs (id STRING PRIMARY KEY, k STRING, n STRING)`)
	execOK(t, s, `INSERT INTO lhs (id, k, n) VALUES ('l1', '1', 'L1'), ('l2', '2', 'L2')`)
	execOK(t, s, `INSERT INTO rhs (id, k, n) VALUES ('r1', '1', 'R1'), ('r3', '3', 'R3')`)
	got := execOK(t, s, `SELECT lhs.n, rhs.n FROM lhs FULL OUTER JOIN rhs ON lhs.k = rhs.k`)
	if len(got.Rows) != 3 {
		t.Fatalf("full join %d rows %+v", len(got.Rows), got.Rows)
	}
	var sawMatch, sawL, sawR bool
	for _, row := range got.Rows {
		if !row[0].Null && row[0].Str == "L1" && !row[1].Null && row[1].Str == "R1" {
			sawMatch = true
		}
		if !row[0].Null && row[0].Str == "L2" && row[1].Null {
			sawL = true
		}
		if row[0].Null && !row[1].Null && row[1].Str == "R3" {
			sawR = true
		}
	}
	if !sawMatch || !sawL || !sawR {
		t.Fatalf("rows %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT lhs.n, rhs.n FROM lhs FULL JOIN rhs ON lhs.k = rhs.k`)
	ops := strings.Join(explainOps(plan), " ")
	if !strings.Contains(ops, "FullJoin") {
		t.Fatalf("explain %s", ops)
	}
}

func TestCrossJoin(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE lhs (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE TABLE rhs (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO lhs (id, n) VALUES ('l1', 'A'), ('l2', 'B')`)
	execOK(t, s, `INSERT INTO rhs (id, n) VALUES ('r1', 'X')`)
	got := execOK(t, s, `SELECT lhs.n, rhs.n FROM lhs CROSS JOIN rhs`)
	if len(got.Rows) != 2 {
		t.Fatalf("cross join %d rows %+v", len(got.Rows), got.Rows)
	}
	got = execOK(t, s, `SELECT lhs.n, rhs.n FROM lhs JOIN rhs`)
	if len(got.Rows) != 2 {
		t.Fatalf("implicit cross %d rows %+v", len(got.Rows), got.Rows)
	}
}

func TestSearchNearestJoinFromTable(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE articles (
		id UUID PRIMARY KEY DEFAULT UUID(),
		k STRING NOT NULL,
		title STRING NOT NULL,
		body TEXT,
		emb VECTOR<F32,3>
	)`)
	execOK(t, s, `CREATE TABLE authors (
		id UUID PRIMARY KEY DEFAULT UUID(),
		k STRING NOT NULL,
		name STRING NOT NULL,
		note TEXT,
		vec VECTOR<F32,3>
	)`)
	execOK(t, s, `INSERT INTO articles (k, title, body, emb) VALUES
		('one', 'one', 'the cat sat', (1, 0, 0)),
		('two', 'two', 'the cat sat on the mat', (0.7, 0.7, 0)),
		('four', 'four', 'database performance tuning', (0, 1, 0))`)
	execOK(t, s, `INSERT INTO authors (k, name, note, vec) VALUES
		('one', 'ann', 'alpha notes', (1, 0, 0)),
		('one', 'abe', 'more notes', (0.9, 0.1, 0)),
		('two', 'bev', 'beta notes', (0, 1, 0)),
		('four', 'cam', 'gamma notes', (0, 0, 1))`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON articles (body)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON articles (emb) USING HNSW`)

	got := execOK(t, s, `SELECT articles.title, authors.name FROM articles JOIN authors ON articles.k = authors.k SEARCH body FOR 'cat'`)
	if len(got.Rows) != 3 {
		t.Fatalf("search join %d rows %v", len(got.Rows), got.Rows)
	}
	if got.Rows[0][0].Str != "one" || got.Rows[1][0].Str != "one" || got.Rows[2][0].Str != "two" {
		t.Fatalf("1:N rank order %v", got.Rows)
	}

	got = execOK(t, s, `SELECT articles.title FROM articles JOIN authors ON articles.k = authors.k WHERE articles.title = 'two' SEARCH body FOR 'cat'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "two" {
		t.Fatalf("from-table residual %v", titles(got))
	}

	got = execOK(t, s, `SELECT articles.title, authors.name FROM articles JOIN authors ON articles.k = authors.k NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(got.Rows) != 2 {
		t.Fatalf("nearest join %d rows %v", len(got.Rows), got.Rows)
	}
	if got.Rows[0][0].Str != "one" {
		t.Fatalf("nearest top %v", got.Rows)
	}

	got = execOK(t, s, `SELECT articles.title, authors.name FROM articles JOIN authors ON articles.k = authors.k
		SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 5`)
	if len(got.Rows) == 0 {
		t.Fatal("hybrid join returned no rows")
	}
	if got.Rows[0][0].Str != "one" {
		t.Fatalf("hybrid join top %v", got.Rows)
	}

	plan := execOK(t, s, `EXPLAIN SELECT articles.title, authors.name FROM articles JOIN authors ON articles.k = authors.k SEARCH body FOR 'cat'`)
	ops := strings.Join(explainOps(plan), " ")
	if !strings.Contains(ops, "Search") || (!strings.Contains(ops, "Join") && !strings.Contains(ops, "HashJoin")) {
		t.Fatalf("explain missing Search/Join: %s", ops)
	}

	if _, err := s.Exec(`SELECT articles.title FROM articles JOIN authors ON articles.k = authors.k SEARCH note FOR 'x'`); err == nil {
		t.Fatal("expected SEARCH on joined table to fail")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument, got %v", err)
	}
	if _, err := s.Exec(`SELECT articles.title FROM articles JOIN authors ON articles.k = authors.k NEAREST vec TO (1, 0, 0)`); err == nil {
		t.Fatal("expected NEAREST on joined table to fail")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want invalid_argument, got %v", err)
	}
}

func TestNearestJoinLimitSkipsUnjoinedTop(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (
		id UUID PRIMARY KEY DEFAULT UUID(),
		k STRING NOT NULL,
		title STRING NOT NULL,
		body TEXT,
		emb VECTOR<F32,3>
	)`)
	execOK(t, s, `CREATE TABLE tags (
		id UUID PRIMARY KEY DEFAULT UUID(),
		k STRING NOT NULL,
		name STRING NOT NULL
	)`)
	// (1,0,0) is closest; it has no tag. (0.7,0.7,0) is next and joins.
	execOK(t, s, `INSERT INTO docs (k, title, body, emb) VALUES
		('miss', 'miss', 'the cat sat', (1, 0, 0)),
		('hit', 'hit', 'the cat sat on the mat', (0.7, 0.7, 0)),
		('far', 'far', 'unrelated text', (0, 1, 0))`)
	execOK(t, s, `INSERT INTO tags (k, name) VALUES ('hit', 'ok')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_docs_body ON docs (body)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_docs_emb ON docs (emb) USING HNSW`)

	got := execOK(t, s, `SELECT docs.title, tags.name FROM docs JOIN tags ON docs.k = tags.k NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "hit" {
		t.Fatalf("nearest join LIMIT must skip unjoined top neighbor: %v", got.Rows)
	}

	got = execOK(t, s, `SELECT docs.title, tags.name FROM docs JOIN tags ON docs.k = tags.k
		SEARCH body FOR 'cat' NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "hit" {
		t.Fatalf("hybrid join LIMIT must skip unjoined top neighbor: %v", got.Rows)
	}
}

func TestHybridJoinLimitBeyondDefaultK(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (
		id UUID PRIMARY KEY DEFAULT UUID(),
		k STRING NOT NULL,
		title STRING NOT NULL,
		body TEXT,
		emb VECTOR<F32,3>
	)`)
	execOK(t, s, `CREATE TABLE tags (
		id UUID PRIMARY KEY DEFAULT UUID(),
		k STRING NOT NULL,
		name STRING NOT NULL
	)`)
	var docs, tags strings.Builder
	docs.WriteString(`INSERT INTO docs (k, title, body, emb) VALUES `)
	tags.WriteString(`INSERT INTO tags (k, name) VALUES `)
	const n = 12
	for i := 0; i < n; i++ {
		if i > 0 {
			docs.WriteByte(',')
			tags.WriteByte(',')
		}
		// Distinct vectors so rank is defined; all match SEARCH 'cat'.
		fmt.Fprintf(&docs, `('k%d', 't%d', 'the cat sat %d', (%d, 0, 0))`, i, i, i, n-i)
		fmt.Fprintf(&tags, `('k%d', 'n%d')`, i, i)
	}
	execOK(t, s, docs.String())
	execOK(t, s, tags.String())
	execOK(t, s, `CREATE FULLTEXT INDEX ix_docs_body ON docs (body)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_docs_emb ON docs (emb) USING HNSW`)

	got := execOK(t, s, `SELECT docs.title, tags.name FROM docs JOIN tags ON docs.k = tags.k
		SEARCH body FOR 'cat' NEAREST emb TO (12, 0, 0) LIMIT 12`)
	if len(got.Rows) != n {
		t.Fatalf("hybrid join LIMIT 12 must return all 12 1:1 partners, got %d %v", len(got.Rows), got.Rows)
	}
}

func TestParallelIndexBuildOnVectorTable(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 4, Memory: 32 << 20, Disk: 32 << 20, IO: 1 << 30, Time: time.Minute, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE docs (id STRING PRIMARY KEY, name STRING NOT NULL, emb VECTOR<F32,4>)`)
	var b strings.Builder
	b.WriteString(`INSERT INTO docs (id, name, emb) VALUES `)
	pad := strings.Repeat("n", 80)
	for i := 0; i < 120; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString("('d")
		b.WriteString(strconv.Itoa(i))
		b.WriteString("', '")
		b.WriteString(pad)
		b.WriteString(strconv.Itoa(i))
		b.WriteString("', (1, 0, 0, 0))")
	}
	execOK(t, s, b.String())
	execOK(t, s, `CREATE INDEX ix_docs_name ON docs (name)`)
	got := execOK(t, s, `SELECT COUNT(*) FROM docs`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "120" {
		t.Fatalf("count %+v", got.Rows)
	}
}

func TestBudgetCancel(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64, Disk: 1024, IO: 1 << 20, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b'), ('c'), ('d'), ('e')`)
	if _, err := s.Exec(`SELECT n FROM t`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("expected memory budget, got %v", err)
	}
}

func TestQueryStreams(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b')`)
	res, err := s.Query(`SELECT n FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("%d", len(res.Rows))
	}
	var n int
	if err := s.Stream(`SELECT n FROM t`, func(b *vector.Batch) error {
		n += b.Count
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("streamed %d", n)
	}
}

func TestFulltextSearch(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE articles (
		id UUID PRIMARY KEY DEFAULT UUID(),
		title STRING NOT NULL,
		body TEXT
	)`)
	execOK(t, s, `INSERT INTO articles (title, body) VALUES
		('one', 'the cat sat'),
		('two', 'the cat sat on the mat'),
		('three', 'dogs and cats'),
		('four', 'database performance tuning'),
		('five', 'performance of the database engine')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON articles (body)`)

	got := execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'cat'`)
	if len(got.Rows) != 2 {
		t.Fatalf("cat: %+v", titles(got))
	}
	if got.Rows[0][0].Str != "one" {
		t.Fatalf("shorter doc should rank first: %v", titles(got))
	}

	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'database performance'`)
	if len(got.Rows) != 2 {
		t.Fatalf("and: %v", titles(got))
	}

	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR '"database performance"'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "four" {
		t.Fatalf("phrase: %v", titles(got))
	}

	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'cat' LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "one" {
		t.Fatalf("limit: %v", titles(got))
	}

	got = execOK(t, s, `SELECT title FROM articles WHERE title = 'two' SEARCH body FOR 'cat'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "two" {
		t.Fatalf("residual: %v", titles(got))
	}

	plan := execOK(t, s, `EXPLAIN SELECT title FROM articles SEARCH body FOR 'cat'`)
	if !explainHas(plan, "Search") || !explainHas(plan, "ix_body") {
		t.Fatalf("plan: %+v", explainOps(plan))
	}

	upd := execOK(t, s, `UPDATE articles SET body = 'unrelated text' WHERE title = 'one'`)
	if upd.Affected != 1 {
		t.Fatalf("updated %d", upd.Affected)
	}
	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'cat'`)
	names := map[string]bool{}
	for _, r := range got.Rows {
		names[r[0].Str] = true
	}
	if names["one"] || !names["two"] {
		t.Fatalf("after update %v", names)
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO articles (title, body) VALUES ('txn', 'the cat sat')`)
	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'cat'`)
	if !containsTitle(got, "txn") {
		t.Fatal("in-txn search")
	}
	execOK(t, s, `ROLLBACK`)
	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'cat'`)
	if containsTitle(got, "txn") {
		t.Fatalf("rolled back search %v", titles(got))
	}

	del := execOK(t, s, `DELETE FROM articles WHERE title = 'two'`)
	if del.Affected != 1 {
		t.Fatalf("deleted %d", del.Affected)
	}
	got = execOK(t, s, `SELECT title FROM articles SEARCH body FOR 'cat'`)
	if containsTitle(got, "two") {
		t.Fatalf("after delete %v", titles(got))
	}
}

func TestFulltextRestartAndEncryption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE articles (id UUID PRIMARY KEY DEFAULT UUID(), body TEXT)`)
	marker := "UNIQUE_FT_MARKER_database_performance"
	execOK(t, s, `INSERT INTO articles (body) VALUES ('`+marker+`')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix ON articles (body)`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(marker)) || bytes.Contains(raw, []byte("unique_ft_marker")) {
		t.Fatal("plaintext full-text on disk")
	}
	for _, sibling := range []string{path + ".wal", path + ".undo"} {
		_ = filepath.Walk(sibling, func(p string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return err
			}
			b, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			if bytes.Contains(b, []byte(marker)) || bytes.Contains(b, []byte("unique_ft_marker")) {
				t.Errorf("plaintext full-text in %s", p)
			}
			return nil
		})
	}

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT body FROM articles SEARCH body FOR 'database performance'`)
	if len(got.Rows) != 1 {
		t.Fatalf("after restart %d", len(got.Rows))
	}
}

func TestFulltextSeqScanFallback(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE notes (id UUID PRIMARY KEY DEFAULT UUID(), body TEXT)`)
	execOK(t, s, `INSERT INTO notes (body) VALUES ('the cat sat'), ('dogs only')`)
	got := execOK(t, s, `SELECT body FROM notes SEARCH body FOR 'cat'`)
	if len(got.Rows) != 1 {
		t.Fatalf("seq search %d", len(got.Rows))
	}
	plan := execOK(t, s, `EXPLAIN SELECT body FROM notes SEARCH body FOR 'cat'`)
	if !explainHas(plan, "Search") {
		t.Fatalf("seq plan %+v", explainOps(plan))
	}
}

func TestVectorNearestFlatAndHNSW(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		emb VECTOR<F32,3>
	)`)
	execOK(t, s, `INSERT INTO docs (name, emb) VALUES
		('x', (1, 0, 0)),
		('y', (0, 1, 0)),
		('xy', (0.7, 0.7, 0))`)

	got := execOK(t, s, `SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "x" {
		t.Fatalf("flat: %v", titles(got))
	}
	plan := execOK(t, s, `EXPLAIN SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if !explainHas(plan, "Nearest") || !explainHas(plan, "flat") {
		t.Fatalf("flat plan: %+v", explainOps(plan))
	}

	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON docs (emb) USING HNSW`)
	got = execOK(t, s, `SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "x" {
		t.Fatalf("hnsw: %v", titles(got))
	}
	ids := execOK(t, s, `SELECT id FROM docs NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if len(ids.Rows) != 2 || ids.Rows[0][0].Null {
		t.Fatalf("hnsw covering pk: %+v", ids.Rows)
	}
	plan = execOK(t, s, `EXPLAIN SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 2`)
	if !explainHas(plan, "Nearest") || !explainHas(plan, "ix_emb") {
		t.Fatalf("hnsw plan: %+v", explainOps(plan))
	}
	vt, err := types.VectorF32(3)
	if err != nil {
		t.Fatal(err)
	}
	param, err := s.ExecContext(context.Background(), `SELECT name FROM docs NEAREST emb TO $1 LIMIT 2`, []Param{{Value: types.VectorValue([]float32{1, 0, 0}, vt)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(param.Rows) != 2 || param.Rows[0][0].Str != "x" {
		t.Fatalf("hnsw param: %v", titles(param))
	}

	fn := execOK(t, s, `SELECT name, COSINE(emb, (1, 0, 0)) FROM docs WHERE name = 'x'`)
	if len(fn.Rows) != 1 {
		t.Fatalf("cosine rows %d", len(fn.Rows))
	}

	got = execOK(t, s, `SELECT name FROM docs WHERE name = 'xy' NEAREST emb TO (1, 0, 0)`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "xy" {
		t.Fatalf("residual %v", titles(got))
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO docs (name, emb) VALUES ('txn', (0.99, 0.01, 0))`)
	got = execOK(t, s, `SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if len(got.Rows) != 1 || (got.Rows[0][0].Str != "x" && got.Rows[0][0].Str != "txn") {
		t.Fatalf("in-txn %v", titles(got))
	}
	execOK(t, s, `ROLLBACK`)
	got = execOK(t, s, `SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 3`)
	if containsTitle(got, "txn") {
		t.Fatalf("rolled back %v", titles(got))
	}

	execOK(t, s, `UPDATE docs SET emb = (0, 0, 1) WHERE name = 'x'`)
	got = execOK(t, s, `SELECT name FROM docs NEAREST emb TO (1, 0, 0) LIMIT 1`)
	if got.Rows[0][0].Str == "x" {
		t.Fatalf("after update still x: %v", titles(got))
	}

	sel := execOK(t, s, `SELECT emb FROM docs WHERE name = 'y'`)
	if len(sel.Rows) != 1 || len(sel.Rows[0][0].Vec) != 3 || sel.Rows[0][0].Vec[1] != 1 {
		t.Fatalf("hydrate %+v", sel.Rows)
	}
}

func TestVectorRestartAndEncryption(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), name STRING, emb VECTOR<F32,4>)`)
	execOK(t, s, `INSERT INTO docs (name, emb) VALUES ('keep', (1.25, 2.5, 3.75, 4))`)
	execOK(t, s, `CREATE VECTOR INDEX ix ON docs (emb) USING HNSW`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("NSVV")) || bytes.Contains(raw, []byte("NSHM")) {
		t.Fatal("plaintext vector magic on disk")
	}

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got := execOK(t, s, `SELECT name FROM docs NEAREST emb TO (1.25, 2.5, 3.75, 4) LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "keep" {
		t.Fatalf("after restart %v", titles(got))
	}
}

func TestVectorRejectsBad(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (id UUID PRIMARY KEY DEFAULT UUID(), emb VECTOR<F32,2>)`)
	if _, err := s.Exec(`INSERT INTO docs (emb) VALUES ((1, 2, 3))`); err == nil {
		t.Fatal("expected dim mismatch")
	}
	if _, err := s.Exec(`CREATE VECTOR INDEX ix ON docs (emb)`); err == nil {
		t.Fatal("expected USING HNSW")
	}
}

func TestHybridSearchNearest(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2),
		description TEXT,
		metadata JSON,
		embedding VECTOR<F32,4>
	)`)
	execOK(t, s, `INSERT INTO products (name, price, description, metadata, embedding) VALUES
		('buds', 800, 'wired earbuds', '{"category":"headphones"}', (0, 1, 0, 0)),
		('air', 12000, 'wireless noise cancelling headphones', '{"category":"headphones"}', (1, 0, 0, 0)),
		('studio', 14000, 'wireless over ear', '{"category":"headphones"}', (0.9, 0.1, 0, 0)),
		('lamp', 2000, 'wireless noise cancelling desk lamp', '{"category":"home"}', (1, 0, 0, 0)),
		('cheap', 500, 'wired budget pair', '{"category":"headphones"}', (0, 0, 1, 0))`)
	execOK(t, s, `CREATE INDEX ix_cat ON products (metadata.category)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_desc ON products (description)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW`)
	execOK(t, s, `ANALYZE products`)

	got := execOK(t, s, `SELECT name FROM products
		WHERE metadata.category = 'headphones' AND price <= 15000
		SEARCH description FOR 'wireless noise cancelling'
		NEAREST embedding TO (1, 0, 0, 0)
		LIMIT 3`)
	if len(got.Rows) == 0 {
		t.Fatal("hybrid returned no rows")
	}
	if got.Rows[0][0].Str != "air" {
		t.Fatalf("top hybrid hit %v", titles(got))
	}
	if containsTitle(got, "lamp") || containsTitle(got, "buds") {
		t.Fatalf("structured filter leaked: %v", titles(got))
	}

	plan := execOK(t, s, `EXPLAIN SELECT name FROM products
		WHERE metadata.category = 'headphones' AND price <= 15000
		SEARCH description FOR 'wireless noise cancelling'
		NEAREST embedding TO (1, 0, 0, 0)
		LIMIT 3`)
	if !explainHas(plan, "Rerank") || !explainHas(plan, "Candidates") {
		t.Fatalf("explain missing candidate generation/rerank: %+v", explainOps(plan))
	}
	if !explainHas(plan, "bm25+vector") {
		t.Fatalf("explain missing fusion: %+v", explainOps(plan))
	}

	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO products (name, price, description, metadata, embedding) VALUES
		('proto', 9000, 'wireless noise cancelling prototype', '{"category":"headphones"}', (0.99, 0.01, 0, 0))`)
	inTxn := execOK(t, s, `SELECT name FROM products
		WHERE metadata.category = 'headphones'
		SEARCH description FOR 'wireless'
		NEAREST embedding TO (1, 0, 0, 0)
		LIMIT 5`)
	if !containsTitle(inTxn, "proto") {
		t.Fatalf("hybrid should see uncommitted insert: %v", titles(inTxn))
	}
	execOK(t, s, `ROLLBACK`)
	after := execOK(t, s, `SELECT name FROM products
		WHERE metadata.category = 'headphones'
		SEARCH description FOR 'wireless'
		NEAREST embedding TO (1, 0, 0, 0)
		LIMIT 5`)
	if containsTitle(after, "proto") {
		t.Fatalf("rolled back hybrid row still visible: %v", titles(after))
	}

	st, ok := db.Cat.Stats("products")
	if !ok || len(st.Vectors) != 1 || st.Vectors[0].Dim != 4 || st.Vectors[0].IndexName != "ix_emb" {
		t.Fatalf("vector stats %+v ok=%v", st, ok)
	}
}

func TestHybridProductSQL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2),
		description TEXT,
		metadata JSON,
		embedding VECTOR<F32,1536>,
		created_at TIMESTAMPTZ DEFAULT NOW()
	)`)
	vt, err := types.VectorF32(1536)
	if err != nil {
		t.Fatal(err)
	}
	insert := func(name, price, desc, meta string, axis int) {
		t.Helper()
		vec := make([]float32, 1536)
		vec[axis] = 1
		if _, err := s.ExecContext(context.Background(),
			`INSERT INTO products (name, price, description, metadata, embedding) VALUES ('`+name+`', `+price+`, '`+desc+`', '`+meta+`', $1)`,
			[]Param{{Name: "1", Value: types.VectorValue(vec, vt)}},
		); err != nil {
			t.Fatal(err)
		}
	}
	insert("air", "12000", "wireless noise cancelling headphones", `{"category":"headphones"}`, 0)
	insert("studio", "14000", "wireless over ear", `{"category":"headphones"}`, 1)
	insert("lamp", "2000", "wireless noise cancelling desk lamp", `{"category":"home"}`, 3)
	insert("budget", "900", "wired headphones", `{"category":"headphones"}`, 2)

	rel := execOK(t, s, `SELECT name FROM products WHERE price BETWEEN 1000 AND 15000`)
	if len(rel.Rows) != 3 {
		t.Fatalf("price between: %v", titles(rel))
	}
	js := execOK(t, s, `SELECT name FROM products WHERE metadata.category = 'headphones'`)
	if len(js.Rows) != 3 {
		t.Fatalf("json category: %v", titles(js))
	}

	q := make([]float32, 1536)
	q[0] = 1
	near, err := s.ExecContext(context.Background(),
		`SELECT name FROM products NEAREST embedding TO $query LIMIT 2`,
		[]Param{{Name: "query", Value: types.VectorValue(q, vt)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(near.Rows) != 2 || near.Rows[0][0].Str != "air" {
		t.Fatalf("nearest %v", titles(near))
	}
	ft := execOK(t, s, `SELECT name FROM products SEARCH description FOR 'wireless noise cancelling' LIMIT 5`)
	if !containsTitle(ft, "air") || !containsTitle(ft, "lamp") {
		t.Fatalf("search %v", titles(ft))
	}

	execOK(t, s, `CREATE INDEX ix_cat ON products (metadata.category)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_desc ON products (description)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW`)
	execOK(t, s, `ANALYZE products`)

	hyb, err := s.ExecContext(context.Background(),
		`SELECT id, name, price FROM products
		 WHERE metadata.category = 'headphones' AND price <= 15000
		 SEARCH description FOR 'wireless noise cancelling'
		 NEAREST embedding TO $query
		 LIMIT 20`,
		[]Param{{Name: "query", Value: types.VectorValue(q, vt)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(hyb.Rows) == 0 || hyb.Rows[0][1].Str != "air" {
		t.Fatalf("hybrid %v", titles(hyb))
	}
	for _, r := range hyb.Rows {
		if r[1].Str == "lamp" || r[1].Str == "budget" {
			t.Fatalf("hybrid leaked %v", titles(hyb))
		}
	}
	plan, err := s.ExecContext(context.Background(),
		`EXPLAIN SELECT id, name, price FROM products
		 WHERE metadata.category = 'headphones' AND price <= 15000
		 SEARCH description FOR 'wireless noise cancelling'
		 NEAREST embedding TO $query
		 LIMIT 20`,
		[]Param{{Name: "query", Value: types.VectorValue(q, vt)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if !explainHas(plan, "Rerank") || !explainHas(plan, "Candidates") {
		t.Fatalf("product hybrid plan %+v", explainOps(plan))
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("NSVV")) || bytes.Contains(raw, []byte("NSHM")) || bytes.Contains(raw, []byte("wireless noise")) {
		t.Fatal("plaintext hybrid payload on disk")
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	got, err := s.ExecContext(context.Background(),
		`SELECT name FROM products
		 WHERE metadata.category = 'headphones' AND price <= 15000
		 SEARCH description FOR 'wireless noise cancelling'
		 NEAREST embedding TO $query
		 LIMIT 20`,
		[]Param{{Name: "query", Value: types.VectorValue(q, vt)}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) == 0 || got.Rows[0][0].Str != "air" {
		t.Fatalf("after restart %v", titles(got))
	}
}

func titles(res *Result) []string {
	out := make([]string, len(res.Rows))
	for i, r := range res.Rows {
		out[i] = r[0].Str
	}
	return out
}

func containsTitle(res *Result, name string) bool {
	for _, r := range res.Rows {
		if r[0].Str == name {
			return true
		}
	}
	return false
}

func TestAdmissionRejectsOverload(t *testing.T) {
	db := testDB(t)
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{MaxInflight: 1, MaxQueue: 0, QueueWait: 5 * time.Millisecond}))
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('a', 'x')`)

	rel, err := db.Admission().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Exec(`SELECT n FROM t`)
	rel()
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	snap := db.Metrics().Snapshot()
	if snap.Rejected < 1 {
		t.Fatalf("rejected %d", snap.Rejected)
	}
}

func TestResultRowBudget(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 8 << 20, Disk: 8 << 20, IO: 1 << 20, ResultRows: 2, BatchSize: 1024})
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('a', '1'), ('b', '2'), ('c', '3')`)
	if _, err := s.Exec(`SELECT n FROM t`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("want exhausted, got %v", err)
	}
}

func TestMetricsCountQueries(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('a', '1')`)
	execOK(t, s, `SELECT n FROM t`)
	snap := db.Metrics().Snapshot()
	if snap.Queries < 3 || snap.Commits < 1 {
		t.Fatalf("%+v", snap)
	}
	if snap.P50 <= 0 {
		t.Fatalf("p50 %v", snap.P50)
	}
}
