package crash

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/wal"
)

func testKeys(t *testing.T) *crypto.MemoryKeyProvider {
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

func reopen(t *testing.T, path string, keys crypto.KeyProvider, pages int) (*storage.Engine, *btree.Tree) {
	t.Helper()
	e, err := storage.Open(path, keys, pages)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Open(e)
	if err != nil {
		_ = e.Close()
		t.Fatal(err)
	}
	return e, tr
}

func TestCrashBeforeCommitLosesInsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointAfterCommitRecordBeforeSync)
	if err := tr.Insert([]byte("a"), []byte("1")); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	if _, err := tr.Lookup([]byte("a")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("uncommitted insert must not survive, got %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCrashAfterCommitKeepsInsert(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	// Committed, pages may still be only in the buffer. Kill without flush.
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("got %q", got)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCrashDuringDelete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointDuringDelete)
	if err := tr.Delete([]byte("a")); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("uncommitted delete must not apply, got %q", got)
	}
}

func TestCrashDuringUpdate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointDuringUpdate)
	if err := tr.Update([]byte("a"), []byte("2")); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("uncommitted update must not apply, got %q", got)
	}
}

func TestRollbackDiscardsWrites(t *testing.T) {
	e, err := storage.Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("keep"), []byte("yes")); err != nil {
		t.Fatal(err)
	}
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert([]byte("gone"), []byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Update([]byte("keep"), []byte("no")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := tr.Lookup([]byte("gone")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("rolled back insert: %v", err)
	}
	got, err := tr.Lookup([]byte("keep"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "yes" {
		t.Fatalf("got %q", got)
	}
}

func TestCrashDuringSplit(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	// Fill enough to force splits, then crash mid-split.
	for i := 0; i < 40; i++ {
		k := []byte{byte(i)}
		v := make([]byte, 400)
		if err := tr.Insert(k, v); err != nil {
			t.Fatal(err)
		}
	}
	e.SetCrash(wal.PointDuringSplit)
	err = tr.Insert([]byte{0xff}, make([]byte, 400))
	if err != nil && !wal.IsCrash(err) {
		t.Fatal(err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 64)
	defer e.Close()
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		if _, err := tr.Lookup([]byte{byte(i)}); err != nil {
			t.Fatalf("key %d: %v", i, err)
		}
	}
}

func TestCrashDuringMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 256)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	const n = 2000
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), fatVal(i)); err != nil {
			t.Fatalf("insert %d: %v", i, err)
		}
	}
	e.SetCrash(wal.PointDuringMerge)
	crashed := false
	last := 0
	for i := 0; i < n; i++ {
		if i%4 == 0 {
			continue
		}
		err := tr.Delete(keyOf(i))
		if wal.IsCrash(err) {
			crashed = true
			last = i
			break
		}
		if err != nil {
			t.Fatalf("delete %d: %v", i, err)
		}
	}
	if !crashed {
		t.Fatal("expected crash during leaf merge")
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 256)
	defer e.Close()
	if err := tr.Check(); err != nil {
		t.Fatalf("check after merge crash: %v", err)
	}
	if _, err := tr.Lookup(keyOf(n - 1)); err != nil {
		t.Fatalf("surviving key: %v", err)
	}
	if _, err := tr.Lookup(keyOf(last)); err != nil && !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("crashed delete key: %v", err)
	}
}

func keyOf(i int) []byte { return []byte(fmt.Sprintf("k%06d", i)) }
func fatVal(i int) []byte {
	return []byte(fmt.Sprintf("v%06d-%s", i, "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"))
}

func TestCrashDuringCheckpoint(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointAfterCheckpointRecordBeforeControl)
	if err := e.Checkpoint(); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("got %q", got)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestPartialDataWriteRepaired(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := e.Sync(); err != nil {
		t.Fatal(err)
	}
	e.Kill()

	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	// Tear the last physical page (partial data write).
	if st.Size() > 100 {
		if err := os.Truncate(path, st.Size()-50); err != nil {
			t.Fatal(err)
		}
	}

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("got %q", got)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitCommitAndRollbackCrashPoints(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert([]byte("x"), []byte("y")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointBeforeCommitRecord)
	if err := tx.Commit(); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	if _, err := tr.Lookup([]byte("x")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("uncommitted txn survived: %v", err)
	}
}

func TestCrashDuringPageFlush(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointDuringPageFlush)
	if err := e.Checkpoint(); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("got %q", got)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestRollbackCrash(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert([]byte("x"), []byte("y")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointBeforeRollback)
	if err := tx.Rollback(); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	if _, err := tr.Lookup([]byte("x")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("rolled-back insert survived: %v", err)
	}
}

func TestUpdateThenRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Update([]byte("a"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	e.Kill()

	e, tr = reopen(t, path, keys, 32)
	defer e.Close()
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2" {
		t.Fatalf("got %q", got)
	}
}

func TestCrashDuringFKCascade(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := executor.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE parents (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO parents (id) VALUES ('p1')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1')`); err != nil {
		t.Fatal(err)
	}
	db.Eng.SetCrash(wal.PointDuringDelete)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !wal.IsCrash(err) {
		t.Fatalf("expected crash during cascade, got %v", err)
	}
	db.Eng.Kill()

	db, err = executor.Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	parents, err := s.Exec(`SELECT id FROM parents`)
	if err != nil {
		t.Fatal(err)
	}
	if len(parents.Rows) != 1 {
		t.Fatalf("uncommitted cascade must not delete parent: %d", len(parents.Rows))
	}
	children, err := s.Exec(`SELECT id FROM children`)
	if err != nil {
		t.Fatal(err)
	}
	if len(children.Rows) != 2 {
		t.Fatalf("uncommitted cascade must not delete children: %d", len(children.Rows))
	}
}
