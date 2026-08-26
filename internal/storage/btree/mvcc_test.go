package btree

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

func TestReadersDoNotSeeUncommitted(t *testing.T) {
	tr, _ := testTree(t, 32)
	if err := tr.Insert([]byte("a"), []byte("old")); err != nil {
		t.Fatal(err)
	}
	w, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Update([]byte("a"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := w.Insert([]byte("b"), []byte("uncommitted")); err != nil {
		t.Fatal(err)
	}

	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("dirty read of update: %q", got)
	}
	if _, err := tr.Lookup([]byte("b")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("dirty read of insert: %v", err)
	}

	r, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	got, err = r.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("snapshot dirty read: %q", got)
	}
	if err := r.Rollback(); err != nil {
		t.Fatal(err)
	}
	if err := w.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err = tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "old" {
		t.Fatalf("after rollback: %q", got)
	}
}

func TestPurgeDeadWaitsForReadSnapshotAndIsBounded(t *testing.T) {
	tr, _ := testTree(t, 32)
	for _, key := range []string{"a", "b"} {
		if err := tr.Insert([]byte(key), []byte("old-"+key)); err != nil {
			t.Fatal(err)
		}
	}
	r, err := tr.BeginRead(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"a", "b"} {
		w, err := tr.BeginTxn(txn.SnapshotIsolation)
		if err != nil {
			t.Fatal(err)
		}
		if err := w.Delete([]byte(key)); err != nil {
			t.Fatal(err)
		}
		if err := w.Commit(); err != nil {
			t.Fatal(err)
		}
		got, err := r.Lookup([]byte(key))
		if err != nil || string(got) != "old-"+key {
			t.Fatalf("snapshot lost %s: %q %v", key, got, err)
		}
	}
	if n, err := tr.PurgeDead(1); n != 0 || !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("purge with live reader = %d, %v", n, err)
	}
	r.MarkDone()
	tr.eng.TM.EndRead(r.Handle().ID)

	if n, err := tr.PurgeDead(1); err != nil || n != 1 {
		t.Fatalf("first bounded purge = %d, %v", n, err)
	}
	remaining := 0
	for _, key := range []string{"a", "b"} {
		_, _, found, err := tr.leafRaw([]byte(key))
		if err != nil {
			t.Fatal(err)
		}
		if found {
			remaining++
		}
	}
	if remaining != 1 {
		t.Fatalf("remaining tombstones = %d", remaining)
	}
	if n, err := tr.PurgeDead(8); err != nil || n != 1 {
		t.Fatalf("second purge = %d, %v", n, err)
	}
}

func TestPurgeDeadCompactsUnderfullLeaves(t *testing.T) {
	tr, _ := testTree(t, 64)
	value := make([]byte, 700)
	for i := 0; i < 180; i++ {
		key := []byte(fmt.Sprintf("k%04d", i))
		if err := tr.Insert(key, value); err != nil {
			t.Fatal(err)
		}
	}
	before, err := tr.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	r, err := tr.BeginRead(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	w, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 170; i++ {
		if err := w.Delete([]byte(fmt.Sprintf("k%04d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}
	r.MarkDone()
	tr.eng.TM.EndRead(r.Handle().ID)
	if n, err := tr.PurgeDead(200); err != nil || n != 170 {
		t.Fatalf("purge = %d, %v", n, err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	after, err := tr.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	if len(after) >= len(before) {
		t.Fatalf("maintenance did not compact pages: before=%d after=%d", len(before), len(after))
	}
}

func TestRollbackRestoresPriorState(t *testing.T) {
	tr, _ := testTree(t, 32)
	if err := tr.Insert([]byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	tx, err := tr.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Update([]byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert([]byte("n"), []byte("x")); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1" {
		t.Fatalf("got %q", got)
	}
	if _, err := tr.Lookup([]byte("n")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("inserted key survived rollback: %v", err)
	}
}

func TestSnapshotRepeatableRead(t *testing.T) {
	tr, _ := testTree(t, 32)
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	r, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatal(got)
	}
	if err := tr.Update([]byte("a"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	got, err = r.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("snapshot saw non-repeatable read %q", got)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	got, err = tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2" {
		t.Fatalf("after snapshot commit %q", got)
	}
}

func TestLookupAtUsesProbeSnapshot(t *testing.T) {
	tr, e := testTree(t, 32)
	if err := tr.Insert([]byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	r, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Rollback() }()
	oldXmax := r.Handle().Snap.Xmax

	w, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Update([]byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := w.Insert([]byte("n"), []byte("new")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	got, err := r.Lookup([]byte("k"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v1" {
		t.Fatalf("Lookup must keep begin snapshot: %q", got)
	}
	if r.Handle().Snap.Xmax != oldXmax {
		t.Fatal("Lookup must not Refresh h.Snap")
	}

	probe := e.TM.Capture(r.Handle().ID)
	got, err = r.LookupAt([]byte("k"), probe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Fatalf("LookupAt must see committed-after-begin: %q", got)
	}
	var seen []string
	if err := r.RangeAt(nil, nil, probe, func(k, v []byte) error {
		seen = append(seen, string(k)+"="+string(v))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(seen) != 2 {
		t.Fatalf("RangeAt probe: %v", seen)
	}
	if r.Handle().Snap.Xmax != oldXmax {
		t.Fatal("LookupAt/RangeAt must not mutate h.Snap")
	}
}

func TestLookupAtDoesNotRefreshRC(t *testing.T) {
	tr, e := testTree(t, 32)
	if err := tr.Insert([]byte("k"), []byte("v1")); err != nil {
		t.Fatal(err)
	}
	r, err := tr.BeginTxn(txn.ReadCommitted)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Rollback() }()
	beginXmax := r.Handle().Snap.Xmax

	w, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Update([]byte("k"), []byte("v2")); err != nil {
		t.Fatal(err)
	}
	if err := w.Commit(); err != nil {
		t.Fatal(err)
	}

	probe := e.TM.Capture(r.Handle().ID)
	got, err := r.LookupAt([]byte("k"), probe)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "v2" {
		t.Fatalf("probe %q", got)
	}
	if r.Handle().Snap.Xmax != beginXmax {
		t.Fatal("LookupAt must not Refresh RC")
	}
}

func TestReadCommittedSeesCommit(t *testing.T) {
	tr, _ := testTree(t, 32)
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	r, err := tr.BeginTxn(txn.ReadCommitted)
	if err != nil {
		t.Fatal(err)
	}
	got, err := r.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatal(string(got))
	}
	if err := tr.Update([]byte("a"), []byte("2")); err != nil {
		t.Fatal(err)
	}
	got, err = r.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "2" {
		t.Fatalf("read committed should see committed update, got %q", got)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestSerializableBlocksPhantom(t *testing.T) {
	tr, _ := testTree(t, 32)
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	r, err := tr.BeginTxn(txn.Serializable)
	if err != nil {
		t.Fatal(err)
	}
	var keys []string
	if err := r.Range([]byte("a"), []byte("z"), func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("first scan %v", keys)
	}

	done := make(chan error, 1)
	go func() {
		done <- tr.Insert([]byte("m"), []byte("phantom"))
	}()
	select {
	case err := <-done:
		t.Fatalf("insert should wait on serializable range, got %v", err)
	case <-time.After(40 * time.Millisecond):
	}

	keys = nil
	if err := r.Range([]byte("a"), []byte("z"), func(k, v []byte) error {
		keys = append(keys, string(k))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || keys[0] != "a" {
		t.Fatalf("phantom read %v", keys)
	}
	if err := r.Commit(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("insert did not complete after range unlock")
	}
}

func TestSerializableWriteSkewDeadlock(t *testing.T) {
	tr, _ := testTree(t, 32)
	if err := tr.Insert([]byte("a"), []byte("100")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("b"), []byte("100")); err != nil {
		t.Fatal(err)
	}
	t1, err := tr.BeginTxn(txn.Serializable)
	if err != nil {
		t.Fatal(err)
	}
	t2, err := tr.BeginTxn(txn.Serializable)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := t1.Lookup([]byte("a")); err != nil {
		t.Fatal(err)
	}
	if _, err := t2.Lookup([]byte("b")); err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 2)
	wg.Add(2)
	go func() {
		defer wg.Done()
		err := t1.Update([]byte("b"), []byte("0"))
		if nerr.HasCode(err, nerr.Deadlock) {
			_ = t1.Rollback()
		}
		errCh <- err
	}()
	time.Sleep(20 * time.Millisecond)
	go func() {
		defer wg.Done()
		err := t2.Update([]byte("a"), []byte("0"))
		if nerr.HasCode(err, nerr.Deadlock) {
			_ = t2.Rollback()
		}
		errCh <- err
	}()
	wg.Wait()
	close(errCh)
	deadlock := 0
	for err := range errCh {
		if err == nil {
			continue
		}
		if nerr.HasCode(err, nerr.Deadlock) {
			deadlock++
			continue
		}
		t.Fatalf("unexpected %v", err)
	}
	if deadlock != 1 {
		t.Fatalf("expected one deadlock abort, got %d", deadlock)
	}
	_ = t1.Rollback()
	_ = t2.Rollback()
}

func TestConcurrentReaderWriter(t *testing.T) {
	tr, _ := testTree(t, 64)
	if err := tr.Insert([]byte("k"), []byte("0")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errCh := make(chan error, 8)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 20; n++ {
				tx, err := tr.BeginTxn(txn.SnapshotIsolation)
				if err != nil {
					errCh <- err
					return
				}
				if _, err := tx.Lookup([]byte("k")); err != nil {
					_ = tx.Rollback()
					errCh <- err
					return
				}
				if err := tx.Commit(); err != nil {
					errCh <- err
					return
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 20; n++ {
			if err := tr.Update([]byte("k"), []byte("1")); err != nil {
				errCh <- err
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestCrashRecoveryKeepsUndoRedo(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	tx, err := tr.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Insert([]byte("b"), []byte("lost")); err != nil {
		t.Fatal(err)
	}
	e.SetCrash(wal.PointAfterCommitRecordBeforeSync)
	if err := tx.Commit(); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	e.Kill()

	e, err = storage.Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tr, err = Open(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("committed key missing: %q", got)
	}
	if _, err := tr.Lookup([]byte("b")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("uncommitted insert survived recovery: %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}

func TestCommittedInsertVisibleAfterReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("a"), []byte("1")); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}
	e, err = storage.Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tr, err = Open(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("a"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "1" {
		t.Fatalf("got %q", got)
	}
}
