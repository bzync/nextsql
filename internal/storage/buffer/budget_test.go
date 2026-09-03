package buffer

import (
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestBudgetReserveUpToCapThenExhausted(t *testing.T) {
	b := NewBudget(10)
	if err := b.Reserve(6); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve(4); err != nil {
		t.Fatal(err)
	}
	if got := b.Used(); got != 10 {
		t.Fatalf("Used() = %d, want 10", got)
	}
	if err := b.Reserve(1); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("Reserve past cap: got %v, want Exhausted", err)
	}
	// A rejected Reserve must not charge anything.
	if got := b.Used(); got != 10 {
		t.Fatalf("Used() after rejected reserve = %d, want 10", got)
	}
}

func TestBudgetReleaseFreesCapacity(t *testing.T) {
	b := NewBudget(10)
	if err := b.Reserve(10); err != nil {
		t.Fatal(err)
	}
	b.Release(4)
	if got := b.Used(); got != 6 {
		t.Fatalf("Used() = %d, want 6", got)
	}
	if err := b.Reserve(4); err != nil {
		t.Fatal(err)
	}
	if err := b.Reserve(1); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("Reserve past cap after partial release: got %v, want Exhausted", err)
	}
}

func TestBudgetReleaseClampsAtZero(t *testing.T) {
	b := NewBudget(10)
	b.Release(5)
	if got := b.Used(); got != 0 {
		t.Fatalf("Used() = %d, want 0 (clamped)", got)
	}
}

func TestBudgetZeroCapUnbounded(t *testing.T) {
	b := NewBudget(0)
	if err := b.Reserve(1 << 30); err != nil {
		t.Fatalf("unbounded Reserve: %v", err)
	}
	if got := b.Cap(); got != 0 {
		t.Fatalf("Cap() = %d, want 0", got)
	}
}

func TestBudgetNilSafe(t *testing.T) {
	var b *Budget
	if err := b.Reserve(100); err != nil {
		t.Fatalf("nil Budget Reserve: %v", err)
	}
	b.Release(100) // must not panic
	if got := b.Used(); got != 0 {
		t.Fatalf("nil Budget Used() = %d, want 0", got)
	}
	if got := b.Cap(); got != 0 {
		t.Fatalf("nil Budget Cap() = %d, want 0", got)
	}
}

func TestBudgetConcurrentReserveRelease(t *testing.T) {
	b := NewBudget(100)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := b.Reserve(2); err == nil {
				b.Release(2)
			}
		}()
	}
	wg.Wait()
	if got := b.Used(); got != 0 {
		t.Fatalf("Used() after concurrent reserve/release = %d, want 0", got)
	}
}
