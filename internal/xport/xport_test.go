package xport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

func setupSQL(t *testing.T, dir string) (dataDir, dbPath string, root *crypto.DEK, env *crypto.Envelope) {
	t.Helper()
	dataDir = filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath = filepath.Join(dataDir, config.DataFileName)
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	env, err = crypto.CreateEnvelope(crypto.KeystorePath(dbPath), id, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, id, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE items (
		id UUID PRIMARY KEY DEFAULT UUID(),
		sku STRING NOT NULL,
		qty DECIMAL(10,0),
		meta JSON,
		loc POINT,
		emb VECTOR<F32,4>
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO items (sku, qty, meta, loc, emb) VALUES
		('alpha', 3, '{"category":"a"}', POINT(-73.98, 40.75), (1, 0, 0, 0)),
		('beta', 9, '{"category":"b"}', POINT(-74.00, 40.70), (0, 1, 0, 0))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE INDEX ix_sku ON items (sku)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE INDEX ix_cat ON items (meta.category)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE SPATIAL INDEX ix_loc ON items (loc)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE VECTOR INDEX ix_emb ON items (emb) USING HNSW`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dataDir, dbPath, root, env
}

func destKeys(t *testing.T, destDir string, root *crypto.DEK) *crypto.Envelope {
	t.Helper()
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	env, err := crypto.CreateEnvelope(crypto.KeystorePath(filepath.Join(destDir, config.DataFileName)), id, root)
	if err != nil {
		t.Fatal(err)
	}
	return env
}

func execSKUs(t *testing.T, dbPath string, keys crypto.KeyProvider) []string {
	t.Helper()
	db, err := executor.Open(dbPath, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.Session().Exec(`SELECT sku FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, row[0].Str)
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestExportImportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "dump.nsex")
	res, err := Export(dataDir, dest, env, Options{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified || !res.ImportTest || res.Tables != 1 || res.Rows != 2 {
		t.Fatalf("unexpected result %+v", res)
	}
	_ = env.Close()

	outDir := filepath.Join(dir, "imported")
	denv := destKeys(t, outDir, root)
	defer denv.Close()
	got, err := Import(dest, outDir, denv, ImportOptions{Root: root})
	if err != nil {
		t.Fatal(err)
	}
	if got.Tables != 1 || got.Rows != 2 {
		t.Fatalf("import result %+v", got)
	}
	skus := execSKUs(t, filepath.Join(outDir, config.DataFileName), denv)
	if !contains(skus, "alpha") || !contains(skus, "beta") {
		t.Fatalf("imported rows %v", skus)
	}

	db, err := executor.Open(filepath.Join(outDir, config.DataFileName), denv, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	res2, err := s.Exec(`SELECT sku FROM items WHERE sku = 'beta'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Rows) != 1 {
		t.Fatalf("index lookup %d rows", len(res2.Rows))
	}
	res3, err := s.Exec(`SELECT sku FROM items NEAREST emb TO (0, 1, 0, 0) LIMIT 1`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res3.Rows) != 1 || res3.Rows[0][0].Str != "beta" {
		t.Fatalf("vector lookup %+v", res3.Rows)
	}
	res4, err := s.Exec(`SELECT sku FROM items WHERE meta.category = 'a'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res4.Rows) != 1 || res4.Rows[0][0].Str != "alpha" {
		t.Fatalf("json path %+v", res4.Rows)
	}
}

func TestExportWrongKeyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	other, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, nil, other, true); err == nil {
		t.Fatal("wrong root must fail")
	}
	outDir := filepath.Join(dir, "imported")
	denv := destKeys(t, outDir, other)
	defer denv.Close()
	if _, err := Import(dest, outDir, denv, ImportOptions{Root: other}); err == nil {
		t.Fatal("import with wrong root must fail")
	}
}

func TestExportTamperFailsClosed(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dest, payloadName)
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xFF
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, env, root, false); err == nil {
		t.Fatal("tampered payload must fail")
	}
}

func TestExportTruncateFailsClosed(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(dest, headerName), 8); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadHeader(dest); !nerr.HasCode(err, nerr.InvalidFormat) && !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("truncated header: %v", err)
	}
}

func TestUnverifiedExportRefused(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root, SkipImportTest: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dest, verifiedName)); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "imported")
	denv := destKeys(t, outDir, root)
	defer denv.Close()
	if _, err := Import(dest, outDir, denv, ImportOptions{Root: root}); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("expected unverified refusal, got %v", err)
	}
}

func TestCrashDuringExportNotPublished(t *testing.T) {
	points := []Point{PointBeforeWrite, PointDuringWrite, PointBeforeVerify}
	for _, p := range points {
		t.Run(p.String(), func(t *testing.T) {
			dir := t.TempDir()
			dataDir, dbPath, root, env := setupSQL(t, dir)
			dest := filepath.Join(dir, "dump.nsex")
			inj := NewInjector()
			inj.Arm(p)
			_, err := Export(dataDir, dest, env, Options{Root: root, Crash: inj})
			if !IsCrash(err) {
				t.Fatalf("expected crash, got %v", err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("incomplete export must not be published")
			}
			outDir := filepath.Join(dir, "imported")
			denv := destKeys(t, outDir, root)
			defer denv.Close()
			if _, err := Import(dest, outDir, denv, ImportOptions{Root: root}); err == nil {
				t.Fatal("import of missing export must fail")
			}
			_ = env.Close()
			re, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), root)
			if err != nil {
				t.Fatal(err)
			}
			defer re.Close()
			got := execSKUs(t, dbPath, re)
			if !contains(got, "alpha") {
				t.Fatalf("source must survive crash-during-export, got %v", got)
			}
		})
	}
}

func TestImportExistingTableFails(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root}); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "imported")
	denv := destKeys(t, outDir, root)
	defer denv.Close()
	if _, err := Import(dest, outDir, denv, ImportOptions{Root: root}); err != nil {
		t.Fatal(err)
	}
	if _, err := Import(dest, outDir, denv, ImportOptions{Root: root}); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("expected already exists, got %v", err)
	}
}

func TestVectorsAreInlined(t *testing.T) {
	row := []types.Value{
		types.StringValue("x"),
		types.VectorRef(mustVec(t, 2)),
	}
	row[1].Vec = []float32{1, 2}
	rec, err := encodeRowRec("t", row)
	if err != nil {
		t.Fatal(err)
	}
	body := rec[1:]
	nl := int(body[0]) | int(body[1])<<8
	raw := body[2+nl+4:]
	got, err := types.DecodeRow(raw, []types.Type{types.String(), mustVec(t, 2)})
	if err != nil {
		t.Fatal(err)
	}
	if got[1].VecRef || len(got[1].Vec) != 2 || got[1].Vec[1] != 2 {
		t.Fatalf("inlined vector %+v", got[1])
	}
}

func mustVec(t *testing.T, n uint16) types.Type {
	t.Helper()
	vt, err := types.VectorF32(n)
	if err != nil {
		t.Fatal(err)
	}
	return vt
}

func TestCreateTableSQLForeignKey(t *testing.T) {
	parent := &catalog.Table{
		Name: "customers",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), NotNull: true, Primary: true},
		},
		PK: []int{0},
	}
	child := &catalog.Table{
		Name: "orders",
		Columns: []catalog.Column{
			{Name: "id", Type: types.UUID(), NotNull: true, Primary: true},
			{Name: "customer_id", Type: types.UUID(), NotNull: true},
		},
		PK: []int{0},
		ForeignKeys: []catalog.ForeignKey{{
			Name: "fk_orders_customer_id", Columns: []int{1},
			RefTable: "customers", RefColumns: []int{0},
			OnDelete: catalog.FKCascade, OnUpdate: catalog.FKRestrict,
		}},
	}
	parents := map[string]*catalog.Table{"customers": parent, "orders": child}
	sql, err := createTableSQLWithParents(child, parents)
	if err != nil {
		t.Fatal(err)
	}
	want := `CONSTRAINT "fk_orders_customer_id" FOREIGN KEY ("customer_id") REFERENCES "customers" ("id") ON DELETE CASCADE ON UPDATE RESTRICT`
	if !strings.Contains(sql, want) {
		t.Fatalf("missing FK clause: %s", sql)
	}
	ordered, err := orderTablesByFK([]tableDump{{Table: child}, {Table: parent}})
	if err != nil {
		t.Fatal(err)
	}
	if ordered[0].Table.Name != "customers" || ordered[1].Table.Name != "orders" {
		t.Fatalf("order %s then %s", ordered[0].Table.Name, ordered[1].Table.Name)
	}
}
