package buffer

import (
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
)

// Budget is a process-wide ceiling on how many buffer-pool frames may be
// committed across every open Pool (one per open database). A Pool's frames
// are allocated once, in full, at construction (see New) — there is no
// per-page dynamic grant to gate at runtime, so the budget is charged once
// when a Pool is created and released once when its owning Engine closes.
//
// A nil *Budget, or one constructed with cap <= 0, is unbounded: Reserve
// always succeeds. This keeps every existing New call site (a single-database
// deployment with no dbmanager, and every direct test of buffer.Pool) exactly
// as before.
type Budget struct {
	mu   sync.Mutex
	cap  int
	used int
}

// NewBudget returns a Budget capped at capFrames total frames across every
// Pool that reserves against it. capFrames <= 0 means unbounded.
func NewBudget(capFrames int) *Budget {
	return &Budget{cap: capFrames}
}

// Reserve charges frames against the budget, failing with nerr.Exhausted if
// doing so would exceed the cap. A nil Budget always succeeds.
func (b *Budget) Reserve(frames int) error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.cap <= 0 {
		return nil
	}
	if b.used+frames > b.cap {
		return nerr.New(nerr.Exhausted, "buffer.Budget", "global buffer memory budget exceeded")
	}
	b.used += frames
	return nil
}

// Release returns frames previously granted by Reserve. A nil Budget is a
// no-op, and Release below zero clamps at zero rather than going negative
// (defensive against a mismatched Reserve/Release pair, which would
// otherwise silently let the budget over-admit forever).
func (b *Budget) Release(frames int) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.used -= frames
	if b.used < 0 {
		b.used = 0
	}
}

// Used reports frames currently reserved. Unbounded (cap <= 0) budgets still
// track usage for observability even though Reserve never rejects.
func (b *Budget) Used() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.used
}

// Cap reports the configured ceiling (0 = unbounded).
func (b *Budget) Cap() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cap
}
