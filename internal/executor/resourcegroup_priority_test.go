package executor

import (
	"context"
	"sync"
	"time"

	"testing"

	"github.com/bzync/nextsql/internal/scheduler"
)

// TestResourceGroupPriorityAdmitsHigherPriorityQueuedQueryFirst proves
// RESOURCE GROUP Priority actually affects process-wide admission order:
// with the sole slot held externally, a low-priority session's query is
// enqueued first, then a high-priority session's query is enqueued
// second; releasing the slot must admit the high-priority query first
// despite it having queued later.
func TestResourceGroupPriorityAdmitsHigherPriorityQueuedQueryFirst(t *testing.T) {
	db := testDB(t)
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{MaxInflight: 1, MaxQueue: 8, QueueWait: 2 * time.Second}))
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('a', 'x')`)
	execOK(t, s, `CREATE RESOURCE GROUP low WITH (PRIORITY = 0)`)
	execOK(t, s, `CREATE RESOURCE GROUP high WITH (PRIORITY = 9)`)

	sLow := db.Session()
	execOK(t, sLow, `SET RESOURCE GROUP low`)
	sHigh := db.Session()
	execOK(t, sHigh, `SET RESOURCE GROUP high`)

	rel, err := db.Admission().Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var order []string

	go func() {
		if _, err := sLow.ExecContext(context.Background(), `SELECT n FROM t`, nil); err != nil {
			t.Errorf("low: %v", err)
		}
		mu.Lock()
		order = append(order, "low")
		mu.Unlock()
	}()
	time.Sleep(20 * time.Millisecond) // ensure low is enqueued first

	go func() {
		if _, err := sHigh.ExecContext(context.Background(), `SELECT n FROM t`, nil); err != nil {
			t.Errorf("high: %v", err)
		}
		mu.Lock()
		order = append(order, "high")
		mu.Unlock()
	}()
	time.Sleep(20 * time.Millisecond) // ensure both are enqueued before release

	rel()

	deadline := time.Now().Add(2 * time.Second)
	for {
		mu.Lock()
		n := len(order)
		mu.Unlock()
		if n == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for both queries, got %v", order)
		}
		time.Sleep(time.Millisecond)
	}

	mu.Lock()
	got := append([]string(nil), order...)
	mu.Unlock()
	if len(got) != 2 || got[0] != "high" || got[1] != "low" {
		t.Fatalf("order = %v, want [high low]", got)
	}
}
