package txn

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/storage/format"
)

func TestSnapshotHidesInProgress(t *testing.T) {
	m := NewManager(1)
	a := m.Attach(1, SnapshotIsolation)
	_ = m.Attach(2, SnapshotIsolation)
	if a.Snap.Sees(2, m.Status) {
		t.Fatal("snapshot must not see in-progress writer")
	}
	if !a.Snap.Visible(1, 0, m.Status) {
		t.Fatal("txn must see its own xmin")
	}
	if a.Snap.Visible(2, 0, m.Status) {
		t.Fatal("must not see uncommitted xmin")
	}
	m.Commit(2)
	// existing snapshot still hides 2 (it was active at capture)
	if a.Snap.Sees(2, m.Status) {
		t.Fatal("snapshot isolation must not see commits after snapshot")
	}
	fresh := m.Capture(1)
	if !fresh.Sees(2, m.Status) {
		t.Fatal("read-committed refresh should see committed 2")
	}
}

func TestLockKeySoleWriter(t *testing.T) {
	m := NewManager(1)
	h := m.Attach(1, SnapshotIsolation)
	if m.ActiveCount() != 1 {
		t.Fatalf("active %d", m.ActiveCount())
	}
	if err := m.LockKey(h, []byte("parent-pk"), Exclusive); err != nil {
		t.Fatal(err)
	}
	h2 := m.Attach(2, SnapshotIsolation)
	got := make(chan error, 1)
	go func() { got <- m.LockKey(h2, []byte("parent-pk"), Shared) }()
	time.Sleep(20 * time.Millisecond)
	select {
	case err := <-got:
		t.Fatalf("Shared must wait for sole-writer Exclusive: %v", err)
	default:
	}
	m.Commit(h.ID)
	if err := <-got; err != nil {
		t.Fatal(err)
	}
	m.Commit(h2.ID)
}

func TestReadCommittedRefresh(t *testing.T) {
	m := NewManager(1)
	h := m.Attach(1, ReadCommitted)
	w := m.Attach(2, SnapshotIsolation)
	m.Commit(w.ID)
	m.Refresh(h)
	if !h.Snap.Sees(2, m.Status) {
		t.Fatal("RC should see committed writer after refresh")
	}
	_ = format.TxnID(0)
}
