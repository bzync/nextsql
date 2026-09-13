package xport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

// A logical export re-creates every table by rendering its canonical DDL and
// executing it on the way back in, so a column type whose rendering does not
// re-parse cannot be restored at all. Two such columns are covered here:
//
//   - BOOL, which the engine has always stored but no CREATE TABLE could
//     declare until the type surface gained it;
//   - a plain GEOGRAPHY, whose SRID defaults to WGS84 and which therefore
//     renders with an explicit subtype -- GEOGRAPHY(Geometry, 4326) -- a
//     spelling the renderer produced but the grammar did not accept.
//
// Both are exercised end to end (export, import, query the imported database)
// rather than by comparing rendered text, because the restore is the property
// that matters.
func TestExportImportRestoresBoolAndDefaultSRIDGeography(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	env, err := crypto.CreateEnvelope(crypto.KeystorePath(dbPath), id, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, id, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL, area GEOGRAPHY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO flags (id, active) VALUES (1, TRUE), (2, FALSE), (3, NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root}); err != nil {
		t.Fatalf("export: %v", err)
	}
	_ = env.Close()

	outDir := filepath.Join(dir, "imported")
	denv := destKeys(t, outDir, root)
	defer denv.Close()
	if _, err := Import(dest, outDir, denv, ImportOptions{Root: root}); err != nil {
		t.Fatalf("import: %v", err)
	}

	back, err := executor.Open(filepath.Join(outDir, config.DataFileName), denv, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	rs := back.Session()
	got, err := rs.Exec(`SELECT id, active FROM flags ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("imported %d rows, want 3", len(got.Rows))
	}
	for i, want := range []struct {
		null bool
		val  bool
	}{{false, true}, {false, false}, {true, false}} {
		cell := got.Rows[i][1]
		if cell.Typ.Kind != types.KindBool {
			t.Fatalf("row %d came back as %s, want BOOL", i+1, cell.Typ.String())
		}
		if cell.Null != want.null || (!cell.Null && cell.Bool != want.val) {
			t.Fatalf("row %d = %v (null=%v), want %v (null=%v)", i+1, cell.Bool, cell.Null, want.val, want.null)
		}
	}
	// The geography column's declared type survives with its defaulted SRID.
	ddlRows, err := rs.Exec(`SELECT * FROM system.table_ddl`)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range ddlRows.Rows {
		if strings.Contains(r[len(r)-1].Str, `"area" GEOGRAPHY(Geometry, 4326)`) {
			found = true
		}
	}
	if !found {
		t.Fatalf("imported table did not keep its GEOGRAPHY column: %v", ddlRows.Rows)
	}
}

// A NULL stored in a column that has a DEFAULT must come back as NULL. Import
// replays each row as a full-column INSERT with bound parameters, and INSERT
// used to fill every NULL cell from the column default, so the restore
// silently replaced NULLs with the default value (a literal, NOW() or UUID()).
func TestExportImportKeepsNullInColumnsWithDefaults(t *testing.T) {
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	env, err := crypto.CreateEnvelope(crypto.KeystorePath(dbPath), id, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, id, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	for _, q := range []string{
		`CREATE TABLE notes (id INT64 PRIMARY KEY, note STRING DEFAULT 'unset', seen TIMESTAMPTZ DEFAULT NOW(), ref UUID DEFAULT UUID())`,
		`INSERT INTO notes (id) VALUES (1)`,
		`UPDATE notes SET note = NULL, seen = NULL, ref = NULL WHERE id = 1`,
	} {
		if _, err := s.Exec(q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "dump.nsex")
	if _, err := Export(dataDir, dest, env, Options{Root: root}); err != nil {
		t.Fatalf("export: %v", err)
	}
	_ = env.Close()
	outDir := filepath.Join(dir, "imported")
	denv := destKeys(t, outDir, root)
	defer denv.Close()
	if _, err := Import(dest, outDir, denv, ImportOptions{Root: root}); err != nil {
		t.Fatalf("import: %v", err)
	}
	back, err := executor.Open(filepath.Join(outDir, config.DataFileName), denv, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer back.Close()
	got, err := back.Session().Exec(`SELECT note, seen, ref FROM notes WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"note", "seen", "ref"} {
		if !got.Rows[0][i].Null {
			t.Errorf("restored %s = %v, want NULL (the column default was substituted)", name, got.Rows[0][i])
		}
	}
}
