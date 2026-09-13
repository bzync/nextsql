package recovery

import (
	"fmt"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/wal"
)

// Redo replays committed WAL records onto the data file.
// Uncommitted records are ignored here; Apply UNDO after Redo for
// transactions that never committed. A torn WAL tail is truncated
// by the log scanner.
func Redo(fm *file.Manager, lg *wal.Log) error {
	return RedoUntil(fm, lg, 0)
}

// RedoUntil is Redo, ignoring records with LSN greater than until.
// until == 0 means replay the entire scanned prefix (normal recovery).
func RedoUntil(fm *file.Manager, lg *wal.Log, until format.LSN) error {
	if fm == nil || lg == nil {
		return nerr.New(nerr.InvalidArgument, "recovery.Redo", "nil file or WAL")
	}
	recs, last, err := lg.ScanFrom(lg.RedoLSN())
	if err != nil {
		return err
	}
	recs, last = clipRecords(recs, last, until)

	// A log holding a page delta must say so in its control file before
	// anything depends on it, so a release that predates deltas refuses the
	// log instead of reading the record as a torn tail. The engine and the
	// replica path keep that true as they write; a point-in-time restore can
	// still pair a base backup's version-1 control file with archived
	// segments written after the source moved to version 2.
	if !lg.PageDeltas() {
		for _, r := range recs {
			if r.Type == wal.RecPageDelta {
				if err := lg.EnablePageDeltas(); err != nil {
					return err
				}
				break
			}
		}
	}

	committed := make(map[format.TxnID]struct{})
	var maxTxn format.TxnID
	for _, r := range recs {
		if r.TxnID > maxTxn {
			maxTxn = r.TxnID
		}
		if r.Type == wal.RecCommit {
			committed[r.TxnID] = struct{}{}
		}
	}

	sb := fm.Superblock()
	root, height := sb.PrimaryRoot, sb.PrimaryHeight
	next, head, count := sb.NextPageID, sb.FreeListHead, sb.FreeCount
	haveTree, haveAlloc := false, false

	for _, r := range recs {
		if r.Type == wal.RecCheckpoint {
			body, err := wal.DecodeCheckpoint(r.Body)
			if err != nil {
				return err
			}
			if body.Root != 0 || body.Height != 0 {
				root, height = body.Root, body.Height
				haveTree = true
			}
			if body.NextPageID != 0 {
				next, head, count = body.NextPageID, body.FreeHead, body.FreeCount
				haveAlloc = true
			}
			continue
		}
		if _, ok := committed[r.TxnID]; !ok {
			continue
		}
		switch r.Type {
		case wal.RecPageImage:
			if err := applyPage(fm, r); err != nil {
				return err
			}
		case wal.RecPageDelta:
			if err := applyPageDelta(fm, r); err != nil {
				return err
			}
		case wal.RecTreeMeta:
			nr, nh, err := wal.DecodeTreeMeta(r.Body)
			if err != nil {
				return err
			}
			root, height = nr, nh
			haveTree = true
		case wal.RecAllocState:
			n, h, c, err := wal.DecodeAllocState(r.Body)
			if err != nil {
				return err
			}
			next, head, count = n, h, c
			haveAlloc = true
		}
	}

	if haveTree {
		if err := fm.SetPrimaryTree(root, height); err != nil {
			return err
		}
	}
	if haveAlloc {
		if err := fm.SetAllocState(next, head, count); err != nil {
			return err
		}
	}

	nextLSN := last + 1
	if last == 0 {
		nextLSN = lg.NextLSN()
		if nextLSN == 0 {
			nextLSN = 1
		}
	}
	nextTxn := maxTxn + 1
	if nextTxn == 0 {
		nextTxn = 1
	}
	lg.AdvanceAfterRecovery(nextLSN, nextTxn)
	return nil
}

// Uncommitted returns transaction ids that began after redoLSN and never
// committed or aborted. Recovery applies UNDO for these after REDO.
func Uncommitted(lg *wal.Log) ([]format.TxnID, error) {
	return UncommittedUntil(lg, 0)
}

// UncommittedUntil is Uncommitted, ignoring records after until (0 = no clip).
func UncommittedUntil(lg *wal.Log, until format.LSN) ([]format.TxnID, error) {
	if lg == nil {
		return nil, nerr.New(nerr.InvalidArgument, "recovery.Uncommitted", "nil WAL")
	}
	recs, last, err := lg.ScanFrom(lg.RedoLSN())
	if err != nil {
		return nil, err
	}
	recs, _ = clipRecords(recs, last, until)
	open := map[format.TxnID]struct{}{}
	done := map[format.TxnID]struct{}{}
	for _, r := range recs {
		switch r.Type {
		case wal.RecBegin:
			open[r.TxnID] = struct{}{}
		case wal.RecCommit, wal.RecAbort:
			done[r.TxnID] = struct{}{}
			delete(open, r.TxnID)
		}
	}
	out := make([]format.TxnID, 0, len(open))
	for id := range open {
		if _, ok := done[id]; !ok {
			out = append(out, id)
		}
	}
	return out, nil
}

// NotCommittedUntil returns every transaction recovery must treat as not
// committed: those that began after the redo boundary and never committed or
// aborted, and those that aborted. Both lists are undone and marked aborted.
//
// The aborted ones matter as much as the open ones. A rollback changes pages
// without logging them, but another transaction's committed image of a shared
// page may already carry the aborted transaction's row versions. Recovery
// replays that image; if it then neither undid the aborted transaction nor
// knew its outcome, the transaction manager would default it to committed and
// the rolled-back versions would come back as live data.
func NotCommittedUntil(lg *wal.Log, until format.LSN) (open, aborted []format.TxnID, err error) {
	if lg == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, "recovery.NotCommitted", "nil WAL")
	}
	recs, last, err := lg.ScanFrom(lg.RedoLSN())
	if err != nil {
		return nil, nil, err
	}
	recs, _ = clipRecords(recs, last, until)
	began := map[format.TxnID]struct{}{}
	committed := map[format.TxnID]struct{}{}
	abortedSet := map[format.TxnID]struct{}{}
	for _, r := range recs {
		switch r.Type {
		case wal.RecBegin:
			began[r.TxnID] = struct{}{}
		case wal.RecCommit:
			committed[r.TxnID] = struct{}{}
		case wal.RecAbort:
			abortedSet[r.TxnID] = struct{}{}
		}
	}
	for id := range began {
		_, c := committed[id]
		_, a := abortedSet[id]
		if !c && !a {
			open = append(open, id)
		}
	}
	for id := range abortedSet {
		if _, c := committed[id]; !c {
			aborted = append(aborted, id)
		}
	}
	return open, aborted, nil
}

func clipRecords(recs []wal.Record, last format.LSN, until format.LSN) ([]wal.Record, format.LSN) {
	if until == 0 {
		return recs, last
	}
	out := recs[:0]
	var clipped format.LSN
	for _, r := range recs {
		if r.LSN > until {
			continue
		}
		out = append(out, r)
		clipped = r.LSN
	}
	return out, clipped
}

func applyPage(fm *file.Manager, r wal.Record) error {
	if r.PageID == 0 {
		return nerr.New(nerr.Corruption, "recovery.applyPage", "page image missing page id")
	}
	if len(r.Body) != format.LogicalPageSize {
		return nerr.New(nerr.Corruption, "recovery.applyPage", "page image has wrong size")
	}
	if existing, err := fm.ReadLogical(r.PageID); err == nil {
		if page.LSNOf(existing) >= r.LSN {
			return nil
		}
	}
	return fm.WriteLogical(r.PageID, r.Body)
}

// applyPageDelta replays a RecPageDelta onto the data file.
//
// A page already at or past the delta's LSN is left alone, exactly as for a
// full image. Otherwise the page must be precisely the delta's base -- at its
// base LSN, with its base digest -- because the records replayed before this
// one are what put it there. Anything else means the log and the data file
// disagree, and applying the delta would write a wrong page, so recovery
// fails closed naming the page and both LSNs.
func applyPageDelta(fm *file.Manager, r wal.Record) error {
	if r.PageID == 0 {
		return nerr.New(nerr.Corruption, "recovery.applyPageDelta", "page delta missing page id")
	}
	d, err := wal.DecodePageDelta(r.Body)
	if err != nil {
		return nerr.Wrap(nerr.Corruption, "recovery.applyPageDelta", "undecodable page delta", err)
	}
	existing, err := fm.ReadLogical(r.PageID)
	if err != nil {
		return nerr.Wrap(nerr.Corruption, "recovery.applyPageDelta", fmt.Sprintf("page %d is unreadable for the delta at LSN %d", r.PageID, r.LSN), err)
	}
	if page.LSNOf(existing) >= r.LSN {
		return nil
	}
	if err := wal.ApplyPageDelta(existing, d, r.LSN); err != nil {
		return nerr.Wrap(nerr.Corruption, "recovery.applyPageDelta", fmt.Sprintf(
			"page %d is at LSN %d; the delta at LSN %d needs base LSN %d", r.PageID, page.LSNOf(existing), r.LSN, d.BaseLSN), err)
	}
	return fm.WriteLogical(r.PageID, existing)
}
