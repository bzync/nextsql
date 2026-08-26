package migrate

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

type sessDB struct{ s *executor.Session }

func (d sessDB) Exec(ctx context.Context, sql string, params ...types.Value) (Result, error) {
	ps := make([]executor.Param, len(params))
	for i, v := range params {
		ps[i] = executor.Param{Value: v}
	}
	res, err := d.s.ExecContext(ctx, sql, ps)
	if err != nil {
		return Result{}, err
	}
	return Result{Columns: res.Columns, Rows: res.Rows, Affected: res.Affected}, nil
}

func testExecer(t *testing.T) sessDB {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(filepath.Join(t.TempDir(), "nextsql.db"), keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return sessDB{s: db.Session()}
}

func TestUpAppliesAndIsIdempotent(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")

	applied, err := Up(ctx, db, dir, UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 3 {
		t.Fatalf("applied %v", applied)
	}
	res, err := db.Exec(ctx, `SELECT name FROM customers`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str != "acme" {
		t.Fatalf("seed %+v %v", res, err)
	}

	again, err := Up(ctx, db, dir, UpOptions{})
	if err != nil || len(again) != 0 {
		t.Fatalf("second up %v %v", again, err)
	}

	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "20260818120200" {
		t.Fatalf("version %q %v", ver, err)
	}
	pend, err := Pending(ctx, db, dir)
	if err != nil || len(pend) != 0 {
		t.Fatalf("pending %v %v", pend, err)
	}
	rep, err := Status(ctx, db, dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Applied != 3 || rep.Pending != 0 || rep.Dirty || rep.Version != "20260818120200" {
		t.Fatalf("%+v", rep)
	}
}

func TestUpDryRunDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	got, err := Up(ctx, db, dir, UpOptions{DryRun: true})
	if err != nil || len(got) != 3 {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := db.Exec(ctx, `SELECT name FROM customers`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("dry-run must not create tables: %v", err)
	}
	rows, err := loadHistory(ctx, db, true)
	if err != nil || len(rows) != 0 {
		t.Fatalf("dry-run history %v %v", rows, err)
	}
}

func TestUpCountAndTo(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	got, err := Up(ctx, db, dir, UpOptions{Count: 1})
	if err != nil || len(got) != 1 || got[0] != "20260818120000" {
		t.Fatalf("count %v %v", got, err)
	}
	got, err = Up(ctx, db, dir, UpOptions{To: "20260818120100"})
	if err != nil || len(got) != 1 || got[0] != "20260818120100" {
		t.Fatalf("to %v %v", got, err)
	}
	pend, err := Pending(ctx, db, dir)
	if err != nil || len(pend) != 1 || pend[0].Version != "20260818120200" {
		t.Fatalf("pending %v %v", pend, err)
	}
}

func TestUpUnknownTo(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	_, err := Up(ctx, db, filepath.Join("testdata", "ok"), UpOptions{To: "20990101000000"})
	if err == nil || !errors.Is(err, cli.ErrValidation) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitValidation {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestUpRefusesDirty(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	if _, err := Up(ctx, db, dir, UpOptions{Count: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE nsql_schema_migrations SET dirty = 1 WHERE version = $1`, types.StringValue("20260818120000")); err != nil {
		t.Fatal(err)
	}
	_, err := Up(ctx, db, dir, UpOptions{})
	if !errors.Is(err, cli.ErrDirty) || cli.Code(err) != cli.ExitDirty {
		t.Fatalf("%v code %d", err, cli.Code(err))
	}
}

func TestUpRefusesChecksumMismatch(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	copyOK(t, dir)
	if _, err := Up(ctx, db, dir, UpOptions{Count: 1}); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "20260818120000_create_customers.up.sql")
	if err := os.WriteFile(path, []byte("CREATE TABLE customers (id UUID PRIMARY KEY DEFAULT UUID(), name STRING NOT NULL);\n-- edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Up(ctx, db, dir, UpOptions{})
	if !errors.Is(err, cli.ErrChecksum) || cli.Code(err) != cli.ExitChecksum {
		t.Fatalf("%v code %d", err, cli.Code(err))
	}
	n, err := Repair(ctx, db, dir)
	if err != nil || n != 1 {
		t.Fatalf("repair %d %v", n, err)
	}
	if _, err := Up(ctx, db, dir, UpOptions{Count: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestForceAndRepair(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	if err := Force(ctx, db, dir, "20260818120000"); err != nil {
		t.Fatal(err)
	}
	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "20260818120000" {
		t.Fatalf("forced %q %v", ver, err)
	}
	pend, err := Pending(ctx, db, dir)
	if err != nil || len(pend) != 2 {
		t.Fatalf("pending after force %v %v", pend, err)
	}
	if err := Force(ctx, db, dir, "none"); err != nil {
		t.Fatal(err)
	}
	ver, err = CurrentVersion(ctx, db)
	if err != nil || ver != "" {
		t.Fatalf("cleared %q %v", ver, err)
	}
}

func TestApplyInvalidArgumentIsExitSQL(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_bad.up.sql"), []byte("CREATE TABLE t (id STRING);\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Up(ctx, db, dir, UpOptions{})
	if err == nil {
		t.Fatal("expected apply error")
	}
	if !errors.Is(err, cli.ErrApply) || cli.Code(err) != cli.ExitSQL {
		t.Fatalf("code %d %v", cli.Code(err), err)
	}
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("want InvalidArgument: %v", err)
	}
}

func TestForceUsesTargetFileDespiteSibling(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	copyOK(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "20260818129999_oops.down.sql"), []byte("ANALYZE;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Validate(dir); err == nil {
		t.Fatal("expected sibling validate error")
	}
	body, err := os.ReadFile(filepath.Join(dir, "20260818120000_create_customers.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Force(ctx, db, dir, "20260818120000"); err != nil {
		t.Fatal(err)
	}
	rows, err := loadHistory(ctx, db, false)
	if err != nil || len(rows) != 1 {
		t.Fatalf("%v %v", rows, err)
	}
	if rows[0].Checksum != Checksum(body) || rows[0].Checksum == forcedChecksum {
		t.Fatalf("checksum %q", rows[0].Checksum)
	}
	if rows[0].Name != "create_customers" {
		t.Fatalf("name %q", rows[0].Name)
	}
}

func TestForceClearsOlderDirty(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	if _, err := Up(ctx, db, dir, UpOptions{Count: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE nsql_schema_migrations SET dirty = 1 WHERE version = $1`, types.StringValue("20260818120000")); err != nil {
		t.Fatal(err)
	}
	if err := Force(ctx, db, dir, "20260818120100"); err != nil {
		t.Fatal(err)
	}
	rows, err := loadHistory(ctx, db, false)
	if err != nil {
		t.Fatal(err)
	}
	if dirtyVersion(rows) != "" {
		t.Fatalf("dirty remains %v", rows)
	}
	if _, err := Up(ctx, db, dir, UpOptions{Count: 1}); err != nil {
		t.Fatal(err)
	}
}

func TestPendingListsWhenDirty(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	if _, err := Up(ctx, db, dir, UpOptions{Count: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE nsql_schema_migrations SET dirty = 1 WHERE version = $1`, types.StringValue("20260818120000")); err != nil {
		t.Fatal(err)
	}
	pend, err := Pending(ctx, db, dir)
	if err != nil || len(pend) != 2 {
		t.Fatalf("pending %v %v", pend, err)
	}
	_, err = Up(ctx, db, dir, UpOptions{})
	if !errors.Is(err, cli.ErrDirty) {
		t.Fatalf("up %v", err)
	}
}

func TestApplyRejectsChangedOrForbiddenFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "20260818120000_x.up.sql")
	if err := os.WriteFile(path, []byte("ANALYZE;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	migs, err := Validate(dir)
	if err != nil || len(migs) != 1 {
		t.Fatalf("%v %v", migs, err)
	}
	if err := os.WriteFile(path, []byte("GRANT SELECT ON t TO app;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err = loadApplyStatements(migs[0])
	if !errors.Is(err, cli.ErrChecksum) {
		t.Fatalf("changed file: %v", err)
	}
	m := migs[0]
	m.Up.Checksum = Checksum([]byte("GRANT SELECT ON t TO app;\n"))
	_, err = loadApplyStatements(m)
	if err == nil || !errors.Is(err, cli.ErrValidation) || !strings.Contains(err.Error(), "GRANT") {
		t.Fatalf("forbidden: %v", err)
	}
}

func TestFailedFileRollsBack(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_bad.up.sql"), []byte("INSERT INTO missing (id) VALUES ('x');\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Up(ctx, db, dir, UpOptions{})
	if err == nil {
		t.Fatal("expected SQL error")
	}
	if cli.Code(err) != cli.ExitSQL {
		t.Fatalf("code %d %v", cli.Code(err), err)
	}
	rows, err := loadHistory(ctx, db, true)
	if err != nil || len(rows) != 0 {
		t.Fatalf("dirty row persisted %v %v", rows, err)
	}
}

func TestStatusBootstrapsHistory(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	rep, err := Status(ctx, db, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Version != "" || rep.Applied != 0 {
		t.Fatalf("%+v", rep)
	}
	if _, err := db.Exec(ctx, historySelectSQL); err != nil {
		t.Fatal(err)
	}
}

func TestSentinelsMapToCLI(t *testing.T) {
	if !errors.Is(ErrDirty, cli.ErrDirty) || cli.Code(ErrDirty) != cli.ExitDirty {
		t.Fatalf("dirty %v %d", ErrDirty, cli.Code(ErrDirty))
	}
	if !errors.Is(ErrChecksum, cli.ErrChecksum) || cli.Code(ErrChecksum) != cli.ExitChecksum {
		t.Fatalf("checksum %v %d", ErrChecksum, cli.Code(ErrChecksum))
	}
}

func copyOK(t *testing.T, dir string) {
	t.Helper()
	src := filepath.Join("testdata", "ok")
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDownDeleteOnly(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	writeMig(t, dir, "20260818120000_items.up.sql",
		"CREATE TABLE items (id STRING PRIMARY KEY, name STRING NOT NULL);\nINSERT INTO items (id, name) VALUES ('1', 'acme');\n")
	writeMig(t, dir, "20260818120000_items.down.sql",
		"DELETE FROM items WHERE id = '1';\n")

	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	res, err := db.Exec(ctx, `SELECT name FROM items`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("seed %+v %v", res, err)
	}

	got, err := Down(ctx, db, dir, DownOptions{})
	if err != nil || len(got) != 1 || got[0] != "20260818120000" {
		t.Fatalf("down %v %v", got, err)
	}
	res, err = db.Exec(ctx, `SELECT name FROM items`)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("delete-only down must leave the table empty: %+v %v", res, err)
	}
	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "" {
		t.Fatalf("version %q %v", ver, err)
	}
	again, err := Down(ctx, db, dir, DownOptions{})
	if err != nil || len(again) != 0 {
		t.Fatalf("second down %v %v", again, err)
	}
}

func TestDownAppliesDropTable(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	writeMig(t, dir, "20260818120000_items.up.sql",
		"CREATE TABLE items (id STRING PRIMARY KEY);\n")
	writeMig(t, dir, "20260818120000_items.down.sql",
		"DROP TABLE items;\n")

	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Down(ctx, db, dir, DownOptions{})
	if err != nil || len(got) != 1 || got[0] != "20260818120000" {
		t.Fatalf("down %v %v", got, err)
	}
	if _, err := db.Exec(ctx, `SELECT * FROM items`); err == nil {
		t.Fatal("expected missing table after DROP TABLE down")
	}
}

func TestDownMissingFile(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := Down(ctx, db, dir, DownOptions{Count: 1})
	if err == nil || !errors.Is(err, cli.ErrValidation) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitValidation {
		t.Fatalf("code %d", cli.Code(err))
	}
	if !strings.Contains(err.Error(), "no down file for 20260818120200") {
		t.Fatalf("%v", err)
	}
	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "20260818120200" {
		t.Fatalf("refused version must stay applied: %q %v", ver, err)
	}
}

func TestDownCountAndTo(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	writeMig(t, dir, "20260818120000_items.up.sql",
		"CREATE TABLE items (id STRING PRIMARY KEY, name STRING NOT NULL);\n")
	writeMig(t, dir, "20260818120000_items.down.sql", "-- keep table; no DROP TABLE in v1\n")
	writeMig(t, dir, "20260818120100_seed.up.sql",
		"INSERT INTO items (id, name) VALUES ('1', 'acme');\n")
	writeMig(t, dir, "20260818120100_seed.down.sql",
		"DELETE FROM items WHERE id = '1';\n")
	writeMig(t, dir, "20260818120200_seed2.up.sql",
		"INSERT INTO items (id, name) VALUES ('2', 'beta');\n")
	writeMig(t, dir, "20260818120200_seed2.down.sql",
		"DELETE FROM items WHERE id = '2';\n")

	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Down(ctx, db, dir, DownOptions{Count: 1})
	if err != nil || len(got) != 1 || got[0] != "20260818120200" {
		t.Fatalf("count %v %v", got, err)
	}
	got, err = Down(ctx, db, dir, DownOptions{To: "20260818120000"})
	if err != nil || len(got) != 2 || got[0] != "20260818120100" || got[1] != "20260818120000" {
		t.Fatalf("to %v %v", got, err)
	}
	res, err := db.Exec(ctx, `SELECT name FROM items`)
	if err != nil || len(res.Rows) != 0 {
		t.Fatalf("rows after down %+v %v", res, err)
	}
	if _, err := db.Exec(ctx, `SELECT name FROM items`); err != nil {
		t.Fatalf("empty down must not drop the table: %v", err)
	}
	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "" {
		t.Fatalf("version %q %v", ver, err)
	}
}

func TestDownDryRunDoesNotWrite(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	writeMig(t, dir, "20260818120000_items.up.sql",
		"CREATE TABLE items (id STRING PRIMARY KEY, name STRING NOT NULL);\nINSERT INTO items (id, name) VALUES ('1', 'acme');\n")
	writeMig(t, dir, "20260818120000_items.down.sql",
		"DELETE FROM items WHERE id = '1';\n")
	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	got, err := Down(ctx, db, dir, DownOptions{DryRun: true})
	if err != nil || len(got) != 1 || got[0] != "20260818120000" {
		t.Fatalf("%v %v", got, err)
	}
	res, err := db.Exec(ctx, `SELECT name FROM items`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("dry-run must not delete: %+v %v", res, err)
	}
	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "20260818120000" {
		t.Fatalf("dry-run version %q %v", ver, err)
	}
}

func TestDownUnknownTo(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	_, err := Down(ctx, db, filepath.Join("testdata", "ok"), DownOptions{To: "20990101000000"})
	if err == nil || !errors.Is(err, cli.ErrValidation) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitValidation {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestDownRefusesDirty(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := filepath.Join("testdata", "ok")
	if _, err := Up(ctx, db, dir, UpOptions{Count: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `UPDATE nsql_schema_migrations SET dirty = 1 WHERE version = $1`, types.StringValue("20260818120000")); err != nil {
		t.Fatal(err)
	}
	_, err := Down(ctx, db, dir, DownOptions{})
	if !errors.Is(err, cli.ErrDirty) || cli.Code(err) != cli.ExitDirty {
		t.Fatalf("%v code %d", err, cli.Code(err))
	}
}

func TestDownFailedSQLRollsBack(t *testing.T) {
	ctx := context.Background()
	db := testExecer(t)
	dir := t.TempDir()
	writeMig(t, dir, "20260818120000_items.up.sql",
		"CREATE TABLE items (id STRING PRIMARY KEY, name STRING NOT NULL);\nINSERT INTO items (id, name) VALUES ('1', 'acme');\n")
	writeMig(t, dir, "20260818120000_items.down.sql",
		"DELETE FROM missing WHERE id = '1';\n")
	if _, err := Up(ctx, db, dir, UpOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err := Down(ctx, db, dir, DownOptions{})
	if err == nil || cli.Code(err) != cli.ExitSQL {
		t.Fatalf("code %v", err)
	}
	ver, err := CurrentVersion(ctx, db)
	if err != nil || ver != "20260818120000" {
		t.Fatalf("rolled back version %q %v", ver, err)
	}
	res, err := db.Exec(ctx, `SELECT name FROM items`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("row must remain %+v %v", res, err)
	}
	rows, err := loadHistory(ctx, db, false)
	if err != nil || len(rows) != 1 || rows[0].Dirty {
		t.Fatalf("dirty row persisted %v %v", rows, err)
	}
}

func writeMig(t *testing.T, dir, name, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestValidateExitCode(t *testing.T) {
	_, err := Validate(filepath.Join("testdata", "invalid", "begin"))
	if err == nil || !errors.Is(err, cli.ErrValidation) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitValidation {
		t.Fatalf("code %d", cli.Code(err))
	}
	if !strings.Contains(err.Error(), "BEGIN/COMMIT/ROLLBACK") {
		t.Fatalf("%v", err)
	}
}
