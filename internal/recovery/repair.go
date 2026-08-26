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
	var (
		best    []byte
		bestLSN format.LSN
	)
	for _, r := range recs {
		if r.Type != wal.RecPageImage || r.PageID != id {
			continue
		}
		if _, ok := committed[r.TxnID]; !ok {
			continue
		}
		if len(r.Body) != format.LogicalPageSize {
			continue
		}
		if r.LSN < bestLSN {
			continue
		}
		bestLSN = r.LSN
		best = r.Body
	}
	if best == nil {
		return nil, nerr.New(nerr.Corruption, "recovery.RepairPage", "no committed page image")
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
