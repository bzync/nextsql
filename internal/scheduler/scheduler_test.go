package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestBudgetEnforced(t *testing.T) {
	b := NewBudget(context.Background(), Limits{Workers: 2, Memory: 100, Disk: 50, IO: 50, Time: time.Second, BatchSize: 1024})
	defer b.Close()
	if err := b.ChargeMem(80); err != nil {
		t.Fatal(err)
	}
	if err := b.ChargeMem(30); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("mem: %v", err)
	}
	if err := b.ChargeDisk(60); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("disk: %v", err)
	}
	short := NewBudget(context.Background(), Limits{Workers: 1, Memory: 1 << 20, Disk: 1 << 20, IO: 1 << 20, Time: time.Millisecond})
	defer short.Close()
	time.Sleep(5 * time.Millisecond)
	if err := short.Check(); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("time: %v", err)
	}
}

func TestPoolBounded(t *testing.T) {
	p := NewPool(2)
	var live atomic.Int32
	var peak atomic.Int32
	tasks := make([]func() error, 8)
	for i := range tasks {
		tasks[i] = func() error {
			n := live.Add(1)
			for {
				cur := peak.Load()
				if n <= cur || peak.CompareAndSwap(cur, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			live.Add(-1)
			return nil
		}
	}
	if err := p.Run(context.Background(), 2, tasks); err != nil {
		t.Fatal(err)
	}
	if peak.Load() > 2 {
		t.Fatalf("peak workers %d", peak.Load())
	}
}

func TestSpillRoundTrip(t *testing.T) {
	b := NewBudget(context.Background(), DefaultLimits())
	defer b.Close()
	sp, err := NewSpill(b)
	if err != nil {
		t.Fatal(err)
	}
	defer sp.Close()
	row := []types.Value{types.StringValue("alpha"), types.DecimalValue(mustDec("3.5"), types.Type{Kind: types.KindDecimal})}
	if err := sp.Write(1, [][]types.Value{row}); err != nil {
		t.Fatal(err)
	}
	raws, err := sp.ReadRaw(1)
	if err != nil {
		t.Fatal(err)
	}
	if len(raws) != 1 {
		t.Fatalf("got %d", len(raws))
	}
	got, err := types.DecodeRow(raws[0], []types.Type{types.String(), types.Type{Kind: types.KindDecimal}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Str != "alpha" {
		t.Fatalf("%+v", got)
	}
}

func mustDec(s string) types.Decimal {
	d, err := types.ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

func TestAdmissionQueueAndReject(t *testing.T) {
	a := NewAdmission(AdmissionConfig{MaxInflight: 1, MaxQueue: 1, QueueWait: 30 * time.Millisecond})
	rel, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	blocked := make(chan error, 1)
	go func() {
		r, err := a.Acquire(context.Background())
		if err == nil {
			r()
		}
		blocked <- err
	}()
	time.Sleep(5 * time.Millisecond)
	if _, err := a.Acquire(context.Background()); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("full queue: %v", err)
	}
	rel()
	if err := <-blocked; err != nil {
		t.Fatal(err)
	}
	st := a.Stats()
	if st.Rejected < 1 || st.Admitted < 2 {
		t.Fatalf("%+v", st)
	}
}

func TestAdmissionCancelWhileQueued(t *testing.T) {
	a := NewAdmission(AdmissionConfig{MaxInflight: 1, MaxQueue: 4, QueueWait: time.Second})
	rel, err := a.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer rel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := a.Acquire(ctx)
		done <- err
	}()
	time.Sleep(5 * time.Millisecond)
	cancel()
	err = <-done
	if !nerr.HasCode(err, nerr.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestNormalizeBatch(t *testing.T) {
	if NormalizeBatch(100) != 1024 || NormalizeBatch(2048) != 2048 || NormalizeBatch(9000) != 4096 {
		t.Fatal(NormalizeBatch(100), NormalizeBatch(2048), NormalizeBatch(9000))
	}
}
