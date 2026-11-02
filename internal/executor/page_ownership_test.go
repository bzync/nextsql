package executor

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/storage/allocator"
	"github.com/bzync/nextsql/internal/storage/format"
)

type fakeOwner struct {
	pages    []format.PageID
	err      error
	checkErr error
}

func (f fakeOwner) OwnedPages() ([]format.PageID, error)  { return f.pages, f.err }
func (f fakeOwner) LinkedPages() ([]format.PageID, error) { return f.pages, f.err }
func (f fakeOwner) Check() error                          { return f.checkErr }

func TestAnalyzePageOwnershipClassifiesConflicts(t *testing.T) {
	first := format.FirstAllocPageID
	trees := []labeledTree{
		{"heap a", fakeOwner{pages: []format.PageID{first, first + 1, first + 2}}},
		{"heap b", fakeOwner{pages: []format.PageID{first + 2, first + 3, first + 50}}},
	}
	state := allocator.State{
		Next:     first + 10,
		Free:     []format.PageID{first + 1, first + 4},
		Metadata: []format.PageID{first + 3, first + 5},
	}
	rep := analyzePageOwnership(trees, state)
	if rep.Clean() {
		t.Fatal("conflicting ownership reported clean")
	}
	want := func(name string, got []PageConflict, page format.PageID, owners ...string) {
		t.Helper()
		if !reflect.DeepEqual(got, []PageConflict{{Page: page, Owners: owners}}) {
			t.Fatalf("%s = %+v, want page %d owners %v", name, got, page, owners)
		}
	}
	want("free", rep.FreeAndOwned, first+1, "heap a")
	want("metadata", rep.MetadataAndOwned, first+3, "heap b")
	want("shared", rep.SharedPages, first+2, "heap a", "heap b")
	want("out of range", rep.OutOfRange, first+50, "heap b")
	wantOrphans := []format.PageID{first + 6, first + 7, first + 8, first + 9}
	if !reflect.DeepEqual(rep.Orphans, wantOrphans) {
		t.Fatalf("orphans = %v, want %v", rep.Orphans, wantOrphans)
	}
}

func TestAnalyzePageOwnershipBrokenTreeHidesOrphans(t *testing.T) {
	first := format.FirstAllocPageID
	rep := analyzePageOwnership([]labeledTree{
		{"heap a", fakeOwner{err: errors.New("wrong page type")}},
	}, allocator.State{Next: first + 3})
	if rep.Clean() {
		t.Fatal("broken tree reported clean")
	}
	if len(rep.BrokenTrees) != 1 || rep.BrokenTrees[0].Owner != "heap a" {
		t.Fatalf("broken trees = %+v", rep.BrokenTrees)
	}
	if rep.Orphans != nil {
		t.Fatalf("orphans must be unknown when a walk failed, got %v", rep.Orphans)
	}
	if rep.FreeAndOwned == nil || rep.MetadataAndOwned == nil || rep.SharedPages == nil || rep.OutOfRange == nil {
		t.Fatalf("empty conflict lists must encode as [], not null: %+v", rep)
	}
}

// A tree whose pages are all accounted for can still be unreadable: its leaf
// chain can point at a page that scans refuse.
func TestAnalyzePageOwnershipReportsFailedStructureCheck(t *testing.T) {
	first := format.FirstAllocPageID
	rep := analyzePageOwnership([]labeledTree{
		{"heap a", fakeOwner{pages: []format.PageID{first}, checkErr: errors.New("leaf next pointer")}},
	}, allocator.State{Next: first + 1})
	if rep.Clean() {
		t.Fatal("failed structure check reported clean")
	}
	if len(rep.BrokenTrees) != 1 || rep.BrokenTrees[0].Error != "leaf next pointer" {
		t.Fatalf("broken trees = %+v", rep.BrokenTrees)
	}
}

// Every kind of catalog-owned tree must count as one owner, or a healthy
// database would be reported as damaged.
func TestCheckPageOwnershipCleanOnEveryTreeKindAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING, v VECTOR<F32,3>)`)
	execOK(t, s, `INSERT INTO t (id, n, v) VALUES ('1', 'a', (1, 0, 0)), ('2', 'b', (0, 1, 0))`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	execOK(t, s, `CREATE TABLE p (
		region STRING NOT NULL,
		id STRING NOT NULL,
		body TEXT NOT NULL,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION americas VALUES IN ('us'),
		PARTITION europe VALUES IN ('eu')
	)`)
	execOK(t, s, `INSERT INTO p (region, id, body) VALUES ('us', '1', 'cat'), ('eu', '2', 'dog')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON p (body)`)
	// Delete and rollback history must not read as damage: merges, tombstones,
	// and splits a rollback keeps all leave a valid tree.
	execOK(t, s, `CREATE TABLE churn (id STRING PRIMARY KEY, body STRING)`)
	body := strings.Repeat("x", 600)
	for i := 0; i < 200; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO churn (id, body) VALUES ('%04d', '%s')`, i, body))
	}
	execOK(t, s, `CREATE INDEX ix_churn ON churn (body)`)
	execOK(t, s, `DELETE FROM churn WHERE id < '0150'`)
	execOK(t, s, `BEGIN`)
	for i := 200; i < 300; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO churn (id, body) VALUES ('%04d', '%s')`, i, body))
	}
	execOK(t, s, `ROLLBACK`)
	execOK(t, s, `UPDATE churn SET body = 'short' WHERE id >= '0150'`)
	execOK(t, s, `CREATE TABLE gone (id STRING PRIMARY KEY)`)
	execOK(t, s, `DROP TABLE gone`)
	check := func(when string) {
		t.Helper()
		rep, err := db.CheckPageOwnership()
		if err != nil {
			t.Fatal(err)
		}
		if !rep.Clean() {
			t.Fatalf("%s: healthy database reported damaged: %+v", when, rep)
		}
		if rep.Orphans == nil {
			t.Fatalf("%s: a completed scan must report orphans, even none", when)
		}
		if rep.OwnedPages == 0 || rep.FreePages == 0 {
			t.Fatalf("%s: scan saw nothing to compare: %+v", when, rep)
		}
	}
	check("live")
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	check("after restart")
}

// A live page on the persisted freelist is exactly what the rollback bug left
// behind. It must be found after a restart, and the check must not change it.
func TestCheckPageOwnershipFindsLivePageOnPersistedFreelist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY)`)
	heap, err := db.heap("t")
	if err != nil {
		t.Fatal(err)
	}
	pages, err := heap.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	live := pages[len(pages)-1]
	txn, err := db.Eng.StartTxn()
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Alloc.Free(live); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.CommitTxn(txn); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for pass := 0; pass < 2; pass++ {
		rep, err := db.CheckPageOwnership()
		if err != nil {
			t.Fatal(err)
		}
		want := []PageConflict{{Page: live, Owners: []string{"heap t"}}}
		if !reflect.DeepEqual(rep.FreeAndOwned, want) {
			t.Fatalf("pass %d: free and owned = %+v, want %+v", pass, rep.FreeAndOwned, want)
		}
		if rep.Clean() {
			t.Fatalf("pass %d: damaged database reported clean", pass)
		}
	}
}

func TestFreelistConflictsFindsLivePageWithoutReadingLeaves(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 256)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	body := strings.Repeat("x", 600)
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, body STRING)`)
	for i := 0; i < 400; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, body) VALUES ('%04d', '%s')`, i, body))
	}
	execOK(t, s, `CREATE TABLE gone (id STRING PRIMARY KEY)`)
	execOK(t, s, `DROP TABLE gone`)
	if got, err := db.FreelistConflicts(); err != nil || len(got) != 0 {
		t.Fatalf("healthy database: conflicts=%+v err=%v", got, err)
	}
	heap, err := db.heap("t")
	if err != nil {
		t.Fatal(err)
	}
	linked, err := heap.LinkedPages()
	if err != nil {
		t.Fatal(err)
	}
	owned, err := heap.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(linked)
	if !slices.Equal(linked, owned) {
		t.Fatalf("linked pages %v differ from owned pages %v", linked, owned)
	}
	live := owned[len(owned)-1]
	if err := db.Eng.Alloc.Free(live); err != nil {
		t.Fatal(err)
	}
	got, err := db.FreelistConflicts()
	if err != nil {
		t.Fatal(err)
	}
	want := []PageConflict{{Page: live, Owners: []string{"heap t"}}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("conflicts = %+v, want %+v", got, want)
	}
}
