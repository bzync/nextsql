package wal

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// TestAppendHeldNotFlushedUntilReleaseCommit proves a held record stays out
// of the durable prefix even when Flush is asked to make everything durable,
// and becomes durable once ReleaseHold(true) resolves it.
func TestAppendHeldNotFlushedUntilReleaseCommit(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	heldLSN, err := lg.AppendHeld(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}

	// Flush(heldLSN) must return once nothing more can become durable, not
	// hang or busy-loop, and must not have made the held record durable.
	if err := lg.Flush(heldLSN - 1); err != nil {
		t.Fatal(err)
	}
	if lg.DurableLSN() >= heldLSN {
		t.Fatalf("held record became durable early: durable=%d held=%d", lg.DurableLSN(), heldLSN)
	}

	if err := lg.ReleaseHold(true); err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(heldLSN); err != nil {
		t.Fatal(err)
	}
	if lg.DurableLSN() < heldLSN {
		t.Fatalf("release(commit) did not make the held record flushable: durable=%d held=%d", lg.DurableLSN(), heldLSN)
	}
}

// TestReleaseHoldDiscardSplicesOutHeldRecordOnly proves discarding a held
// record removes exactly its own bytes and leaves an unrelated record
// appended after it (from another, concurrently-committing transaction)
// intact and independently flushable.
func TestReleaseHoldDiscardSplicesOutHeldRecordOnly(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	heldTxn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(heldTxn)); err != nil {
		t.Fatal(err)
	}
	heldLSN, err := lg.AppendHeld(CommitRec(heldTxn, 1))
	if err != nil {
		t.Fatal(err)
	}

	// An unrelated transaction appends and commits normally while the
	// first transaction's commit record is held, exactly as
	// flushDirtyImages releasing e.mu mid-commitLocked allows in the
	// engine.
	otherTxn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(otherTxn)); err != nil {
		t.Fatal(err)
	}
	otherLSN, err := lg.Append(CommitRec(otherTxn, heldLSN+1))
	if err != nil {
		t.Fatal(err)
	}

	if err := lg.ReleaseHold(false); err != nil {
		t.Fatal(err)
	}
	if err := lg.Flush(otherLSN); err != nil {
		t.Fatal(err)
	}
	if lg.DurableLSN() < otherLSN {
		t.Fatalf("unrelated record after a discarded hold never became durable: durable=%d want>=%d", lg.DurableLSN(), otherLSN)
	}

	recs, _, err := lg.ScanFrom(1)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recs {
		if r.LSN == heldLSN {
			t.Fatalf("discarded held record %d is still present on disk", heldLSN)
		}
	}
	sawOther := false
	for _, r := range recs {
		if r.LSN == otherLSN && r.Type == RecCommit && r.TxnID == otherTxn {
			sawOther = true
		}
	}
	if !sawOther {
		t.Fatal("unrelated transaction's commit record did not survive the discard")
	}
}

// TestFlushWaitsForHeldRecordThenProceeds proves a Flush call blocked on a
// held record wakes up and completes once ReleaseHold resolves it, rather
// than busy-spinning under l.mu (which would also starve ReleaseHold
// itself of the lock).
func TestFlushWaitsForHeldRecordThenProceeds(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	heldLSN, err := lg.AppendHeld(CommitRec(txn, 1))
	if err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		done <- lg.Flush(heldLSN)
	}()

	select {
	case err := <-done:
		t.Fatalf("Flush returned before the hold was released: err=%v", err)
	case <-time.After(100 * time.Millisecond):
	}

	if err := lg.ReleaseHold(true); err != nil {
		t.Fatal(err)
	}

	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Flush never woke up after ReleaseHold")
	}
	if lg.DurableLSN() < heldLSN {
		t.Fatalf("durable=%d want>=%d", lg.DurableLSN(), heldLSN)
	}
}

// TestAppendHeldRejectsSecondHold proves the single-slot invariant is
// enforced defensively, even though Engine.replMu is expected to make a
// second concurrent hold unreachable in production.
func TestAppendHeldRejectsSecondHold(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	txn := lg.AllocTxn()
	if _, err := lg.AppendHeld(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.AppendHeld(CommitRec(txn, 1)); !nerr.HasCode(err, nerr.Internal) {
		t.Fatalf("second AppendHeld = %v, want Internal", err)
	}
	if err := lg.ReleaseHold(false); err != nil {
		t.Fatal(err)
	}
}

// TestReleaseHoldNoopWithoutHold proves ReleaseHold tolerates being called
// when nothing is held (the e.repl == nil legacy commit path never holds
// anything, and finishCommitLocked's release call must still be safe).
func TestReleaseHoldNoopWithoutHold(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()
	if err := lg.ReleaseHold(true); err != nil {
		t.Fatal(err)
	}
	if err := lg.ReleaseHold(false); err != nil {
		t.Fatal(err)
	}
}

// TestRotateBlockedByHold proves a hold refuses to let segment rotation run
// out from under it (which would strand the held bytes at an offset that
// no longer belongs to the open segment) rather than silently corrupting
// segment/LSN alignment.
func TestRotateBlockedByHold(t *testing.T) {
	keys, id := testIdent(t)
	lg, err := Create(filepath.Join(t.TempDir(), "wal"), keys, id, Options{SegmentSize: 4 << 10})
	if err != nil {
		t.Fatal(err)
	}
	defer lg.Close()

	txn := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(txn)); err != nil {
		t.Fatal(err)
	}
	if _, err := lg.AppendHeld(CommitRec(txn, 1)); err != nil {
		t.Fatal(err)
	}

	other := lg.AllocTxn()
	if _, err := lg.Append(BeginRec(other)); err != nil {
		t.Fatal(err)
	}
	var lastErr error
	for i := 0; i < 200; i++ {
		_, lastErr = lg.Append(Record{Type: RecInsert, TxnID: other, Body: make([]byte, 128)})
		if lastErr != nil {
			break
		}
	}
	if lastErr != ErrHoldBlocksRotation {
		t.Fatalf("append past the segment while held = %v, want ErrHoldBlocksRotation", lastErr)
	}

	if err := lg.ReleaseHold(false); err != nil {
		t.Fatal(err)
	}
}
