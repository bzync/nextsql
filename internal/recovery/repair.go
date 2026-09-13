package recovery

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/wal"
)

// RepairPage writes the latest committed WAL page image for id.
// It scans from LSN 1 so a post-checkpoint disk smash can still be
// rebuilt from retained segments. The recovered image is validated
// before it is written. Uncommitted images are ignored.
func RepairPage(fm *file.Manager, lg *wal.Log, id format.PageID) ([]byte, error) {
	if fm == nil || lg == nil {
		return nil, nerr.New(nerr.InvalidArgument, "recovery.RepairPage", "nil file or WAL")
	}
	if err := id.UserData(); err != nil {
		return nil, err
	}
	recs, _, err := lg.ScanFrom(1)
	if err != nil {
		return nil, err
	}
	committed := make(map[format.TxnID]struct{})
	for _, r := range recs {
		if r.Type == wal.RecCommit {
			committed[r.TxnID] = struct{}{}
		}
	}
	// Rebuild the page from its committed records in LSN order: a full image
	// resets it, a delta applies to the state the previous records produced.
	// A delta whose base is not the rebuilt state (its full image predates the
	// retained segments, say) cannot be used, and neither can anything before
	// it: the page it would produce is older than a committed change the log
	// holds, which is a silently stale page, not a repair. The rebuild
	// restarts at the next full image, and with none it fails closed.
	var (
		best    []byte
		current []byte
	)
	for _, r := range recs {
		if r.PageID != id || (r.Type != wal.RecPageImage && r.Type != wal.RecPageDelta) {
			continue
		}
		if _, ok := committed[r.TxnID]; !ok {
			continue
		}
		switch r.Type {
		case wal.RecPageImage:
			if len(r.Body) != format.LogicalPageSize {
				continue
			}
			current = append(current[:0], r.Body...)
			best = append([]byte(nil), current...)
		case wal.RecPageDelta:
			if current == nil {
				best = nil
				continue
			}
			d, err := wal.DecodePageDelta(r.Body)
			if err != nil {
				current, best = nil, nil
				continue
			}
			next := append([]byte(nil), current...)
			if err := wal.ApplyPageDelta(next, d, r.LSN); err != nil {
				current, best = nil, nil
				continue
			}
			current = next
			best = append([]byte(nil), current...)
		}
	}
	// WAL stamps the page LSN after the writer finalized the checksum.
	// Structural validation is the gate; WriteLogical recomputes CRC32C.
	if _, err := page.ParseID(best, id); err != nil {
		return nil, nerr.Wrap(nerr.Corruption, "recovery.RepairPage", "wal image failed validation", err)
	}
	if err := fm.WriteLogical(id, best); err != nil {
		return nil, err
	}
	out := make([]byte, format.LogicalPageSize)
	copy(out, best)
	return out, nil
}
