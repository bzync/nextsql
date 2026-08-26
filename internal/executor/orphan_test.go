package executor

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/bzync/nextsql/internal/storage/btree"
)

func TestOrphanPagesCleanDropAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('1', 'a'), ('2', 'b')`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	if got, err := db.OrphanPages(); err != nil || len(got) != 0 {
		t.Fatalf("live database orphans=%v err=%v", got, err)
	}
	execOK(t, s, `DROP INDEX ix_n`)
	if got, err := db.OrphanPages(); err != nil || len(got) != 0 {
		t.Fatalf("after drop orphans=%v err=%v", got, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got, err := db.OrphanPages(); err != nil || len(got) != 0 {
		t.Fatalf("after restart orphans=%v err=%v", got, err)
	}
}

func TestOrphanPagesFindsUncatalogedDetachedTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := db.Eng.StartTxn()
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.Enter(owner)
	orphan, err := btree.CreateDetached(db.Eng)
	db.Eng.Leave(owner)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.CommitTxn(owner); err != nil {
		t.Fatal(err)
	}
	want, err := orphan.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	got, err := db.OrphanPages()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("got=%v want=%v", got, want)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got, err = db.OrphanPages()
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("restart got=%v want=%v", got, want)
	}
}
