package recovery

import (
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
