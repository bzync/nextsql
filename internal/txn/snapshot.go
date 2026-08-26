package txn

import "github.com/bzync/nextsql/internal/storage/format"

// Snapshot is the visibility horizon of one transaction.
//
// A committed xid is visible when xid < Xmax and xid is not in Active
// (the in-progress set at snapshot time). xid 0 is a pre-MVCC row and
// is always visible.
type Snapshot struct {
	Tid    format.TxnID
	Xmax   format.TxnID
	Active map[format.TxnID]struct{}
}

func (s Snapshot) Sees(xid format.TxnID, status func(format.TxnID) Status) bool {
	if xid == 0 || xid == s.Tid {
		return true
	}
	if status != nil {
		switch status(xid) {
		case StatusAborted:
			return false
		case StatusInProgress:
			return false
		}
	}
	if s.Xmax != 0 && xid >= s.Xmax {
		return false
	}
	if _, ok := s.Active[xid]; ok {
		return false
	}
	return true
}

// Visible reports whether a row version with the given xmin/xmax is
// visible to this snapshot.
func (s Snapshot) Visible(xmin, xmax format.TxnID, status func(format.TxnID) Status) bool {
	if !s.Sees(xmin, status) {
		return false
	}
	if xmax == 0 {
		return true
	}
	if xmax == s.Tid {
		return false
	}
	return !s.Sees(xmax, status)
}
