package metrics

import (
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestObserveAndSnapshot(t *testing.T) {
	r := New()
	r.ObserveQuery(2*time.Millisecond, nil)
	r.ObserveQuery(4*time.Millisecond, nil)
	r.ObserveQuery(10*time.Millisecond, nerr.New(nerr.Canceled, "t", "canceled"))
	r.AddCommit()
	r.AddCommit()
	r.AddRollback()
	r.AddRejected()
	r.AddAdmitted()
	r.AddRows(7)
	r.ObserveSeal(16384, time.Millisecond)
	r.ObserveOpen(16384, 500*time.Microsecond)
	r.AddWAL(4096)
	r.AddIsolated()
	r.AddIsolated()
	r.AddRepaired()
	r.AddFKCheck()
	r.AddFKCheck()
	r.AddFKViolation()
	r.AddFKCascadeRows(3)
	r.AddFKCascadeReject()
	r.ObserveIndexRebuild(11, 13, 7*time.Millisecond, nil)
	r.ObserveIndexRebuild(3, 5, 2*time.Millisecond, nerr.New(nerr.IO, "test", "failed"))
	r.AddCDCSubscription()
	r.AddCDCDelivery(2, 7, 19)
	r.AddCDCError()
	r.CloseCDCSubscription()
	s := r.Snapshot()
	if s.Queries != 3 || s.Errors != 1 || s.Canceled != 1 {
		t.Fatalf("queries %+v", s)
	}
	if s.Commits != 2 || s.Rollbacks != 1 || s.Rejected != 1 || s.Admitted != 1 || s.Rows != 7 {
		t.Fatalf("txn %+v", s)
	}
	if s.SealOps != 1 || s.OpenOps != 1 || s.WALBytes != 4096 || s.SealBytes != 16384 {
		t.Fatalf("io %+v", s)
	}
	if s.Isolated != 2 || s.Repaired != 1 {
		t.Fatalf("integrity %+v", s)
	}
	if s.FKChecks != 2 || s.FKViolations != 1 || s.FKCascadeRows != 3 || s.FKCascadeRejects != 1 {
		t.Fatalf("fk %+v", s)
	}
	if s.IndexRebuilds != 2 || s.IndexRebuildFailures != 1 || s.IndexRebuildRows != 14 || s.IndexRebuildEntries != 18 || s.IndexRebuildDuration != 9*time.Millisecond {
		t.Fatalf("index rebuild %+v", s)
	}
	if s.CDCSubscriptions != 1 || s.CDCActive != 0 || s.CDCTransactions != 2 || s.CDCEvents != 7 || s.CDCErrors != 1 || s.CDCLagLSN != 19 {
		t.Fatalf("cdc %+v", s)
	}
	if s.P50 <= 0 || s.P99 < s.P50 {
		t.Fatalf("percentiles %v %v %v %v", s.P50, s.P95, s.P99, s.P999)
	}
	if s.NumCPU < 1 || s.HeapAlloc == 0 {
		t.Fatalf("process %+v", s)
	}
}

func TestPercentilesEmpty(t *testing.T) {
	s := New().Snapshot()
	if s.P50 != 0 || s.P999 != 0 {
		t.Fatalf("%+v", s)
	}
}

func TestNilRegistry(t *testing.T) {
	var r *Registry
	r.ObserveQuery(time.Millisecond, nil)
	r.AddCommit()
	r.ObserveSeal(1, time.Millisecond)
	if r.Snapshot().Queries != 0 {
		t.Fatal("nil snapshot")
	}
}
