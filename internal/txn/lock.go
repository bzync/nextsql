package txn

import (
	"bytes"
	"sync"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// Mode is a lock mode.
type Mode uint8

const (
	Shared Mode = iota + 1
	Exclusive
)

type waiter struct {
	txn  format.TxnID
	mode Mode
	key  string
	rng  *keyRange
	ch   chan error
}

type keyRange struct {
	start []byte
	end   []byte // nil = unbounded
}

type keyState struct {
	holders map[format.TxnID]Mode
}

// LockManager is a key / range lock table with wait-for deadlock detection.
type LockManager struct {
	mu      sync.Mutex
	keys    map[string]*keyState
	ranges  []heldRange
	waiters []*waiter
	waitFor map[format.TxnID]map[format.TxnID]struct{}
}

type heldRange struct {
	txn   format.TxnID
	mode  Mode
	start []byte
	end   []byte
}

func NewLockManager() *LockManager {
	return &LockManager{
		keys:    make(map[string]*keyState),
		waitFor: make(map[format.TxnID]map[format.TxnID]struct{}),
	}
}

func (lm *LockManager) Acquire(txn format.TxnID, key []byte, mode Mode) error {
	k := string(key)
	lm.mu.Lock()
	if lm.canGrantKey(txn, k, key, mode) {
		lm.grantKey(txn, k, mode)
		lm.mu.Unlock()
		return nil
	}
	w := &waiter{txn: txn, mode: mode, key: k, ch: make(chan error, 1)}
	lm.waiters = append(lm.waiters, w)
	lm.setWait(txn, lm.blockersKey(txn, k, key, mode))
	if lm.hasCycle(txn) {
		lm.removeWaiter(w)
		lm.clearWait(txn)
		lm.mu.Unlock()
		return nerr.New(nerr.Deadlock, "txn.Lock", "deadlock detected")
	}
	lm.mu.Unlock()
	return <-w.ch
}

// AcquireRange locks [start, end). A nil end is unbounded.
func (lm *LockManager) AcquireRange(txn format.TxnID, start, end []byte, mode Mode) error {
	lm.mu.Lock()
	if lm.canGrantRange(txn, start, end, mode) {
		lm.grantRange(txn, start, end, mode)
		lm.mu.Unlock()
		return nil
	}
	w := &waiter{
		txn:  txn,
		mode: mode,
		rng:  &keyRange{start: append([]byte(nil), start...), end: append([]byte(nil), end...)},
		ch:   make(chan error, 1),
	}
	lm.waiters = append(lm.waiters, w)
	lm.setWait(txn, lm.blockersRange(txn, start, end, mode))
	if lm.hasCycle(txn) {
		lm.removeWaiter(w)
		lm.clearWait(txn)
		lm.mu.Unlock()
		return nerr.New(nerr.Deadlock, "txn.Lock", "deadlock detected")
	}
	lm.mu.Unlock()
	return <-w.ch
}

func (lm *LockManager) ReleaseAll(txn format.TxnID) {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	for k, st := range lm.keys {
		delete(st.holders, txn)
		if len(st.holders) == 0 {
			delete(lm.keys, k)
		}
	}
	kept := lm.ranges[:0]
	for _, r := range lm.ranges {
		if r.txn != txn {
			kept = append(kept, r)
		}
	}
	lm.ranges = kept
	lm.clearWait(txn)
	lm.wake()
}

func (lm *LockManager) canGrantKey(txn format.TxnID, k string, key []byte, mode Mode) bool {
	if st := lm.keys[k]; st != nil {
		for id, have := range st.holders {
			if id == txn {
				continue
			}
			if have == Exclusive || mode == Exclusive {
				return false
			}
		}
	}
	for _, r := range lm.ranges {
		if r.txn == txn {
			continue
		}
		if !inRange(key, r.start, r.end) {
			continue
		}
		if r.mode == Exclusive || mode == Exclusive {
			return false
		}
	}
	return true
}

func (lm *LockManager) canGrantRange(txn format.TxnID, start, end []byte, mode Mode) bool {
	for k, st := range lm.keys {
		if !inRange([]byte(k), start, end) {
			continue
		}
		for id, have := range st.holders {
			if id == txn {
				continue
			}
			if have == Exclusive || mode == Exclusive {
				return false
			}
		}
	}
	for _, r := range lm.ranges {
		if r.txn == txn {
			continue
		}
		if !rangeOverlap(start, end, r.start, r.end) {
			continue
		}
		if r.mode == Exclusive || mode == Exclusive {
			return false
		}
	}
	return true
}

func (lm *LockManager) grantKey(txn format.TxnID, k string, mode Mode) {
	st := lm.keys[k]
	if st == nil {
		st = &keyState{holders: make(map[format.TxnID]Mode)}
		lm.keys[k] = st
	}
	if have, ok := st.holders[txn]; !ok || mode == Exclusive || have == Exclusive {
		st.holders[txn] = mode
	}
}

func (lm *LockManager) grantRange(txn format.TxnID, start, end []byte, mode Mode) {
	lm.ranges = append(lm.ranges, heldRange{
		txn:   txn,
		mode:  mode,
		start: append([]byte(nil), start...),
		end:   append([]byte(nil), end...),
	})
}

func (lm *LockManager) blockersKey(self format.TxnID, k string, key []byte, mode Mode) []format.TxnID {
	seen := map[format.TxnID]struct{}{}
	var out []format.TxnID
	add := func(id format.TxnID) {
		if id == self {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	if st := lm.keys[k]; st != nil {
		for id, have := range st.holders {
			if have == Exclusive || mode == Exclusive {
				add(id)
			}
		}
	}
	for _, r := range lm.ranges {
		if inRange(key, r.start, r.end) && (r.mode == Exclusive || mode == Exclusive) {
			add(r.txn)
		}
	}
	return out
}

func (lm *LockManager) blockersRange(self format.TxnID, start, end []byte, mode Mode) []format.TxnID {
	seen := map[format.TxnID]struct{}{}
	var out []format.TxnID
	add := func(id format.TxnID) {
		if id == self {
			return
		}
		if _, ok := seen[id]; ok {
			return
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	for k, st := range lm.keys {
		if !inRange([]byte(k), start, end) {
			continue
		}
		for id, have := range st.holders {
			if have == Exclusive || mode == Exclusive {
				add(id)
			}
		}
	}
	for _, r := range lm.ranges {
		if rangeOverlap(start, end, r.start, r.end) && (r.mode == Exclusive || mode == Exclusive) {
			add(r.txn)
		}
	}
	return out
}

func (lm *LockManager) setWait(txn format.TxnID, blockers []format.TxnID) {
	m := make(map[format.TxnID]struct{}, len(blockers))
	for _, b := range blockers {
		m[b] = struct{}{}
	}
	lm.waitFor[txn] = m
}

func (lm *LockManager) clearWait(txn format.TxnID) {
	delete(lm.waitFor, txn)
}

func (lm *LockManager) hasCycle(start format.TxnID) bool {
	seen := map[format.TxnID]int{} // 0 unseen, 1 visiting, 2 done
	var dfs func(format.TxnID) bool
	dfs = func(id format.TxnID) bool {
		st := seen[id]
		if st == 1 {
			return true
		}
		if st == 2 {
			return false
		}
		seen[id] = 1
		for nxt := range lm.waitFor[id] {
			if dfs(nxt) {
				return true
			}
		}
		seen[id] = 2
		return false
	}
	return dfs(start)
}

func (lm *LockManager) removeWaiter(w *waiter) {
	out := lm.waiters[:0]
	for _, x := range lm.waiters {
		if x != w {
			out = append(out, x)
		}
	}
	lm.waiters = out
}

func (lm *LockManager) wake() {
	again := true
	for again {
		again = false
		kept := lm.waiters[:0]
		for _, w := range lm.waiters {
			ok := false
			if w.rng != nil {
				ok = lm.canGrantRange(w.txn, w.rng.start, w.rng.end, w.mode)
			} else {
				ok = lm.canGrantKey(w.txn, w.key, []byte(w.key), w.mode)
			}
			if !ok {
				kept = append(kept, w)
				continue
			}
			if w.rng != nil {
				lm.grantRange(w.txn, w.rng.start, w.rng.end, w.mode)
			} else {
				lm.grantKey(w.txn, w.key, w.mode)
			}
			lm.clearWait(w.txn)
			w.ch <- nil
			again = true
		}
		lm.waiters = kept
	}
}

func inRange(key, start, end []byte) bool {
	if start != nil && bytes.Compare(key, start) < 0 {
		return false
	}
	if end != nil && bytes.Compare(key, end) >= 0 {
		return false
	}
	return true
}

func rangeOverlap(a0, a1, b0, b1 []byte) bool {
	// [a0,a1) overlaps [b0,b1)
	if a1 != nil && b0 != nil && bytes.Compare(a1, b0) <= 0 {
		return false
	}
	if b1 != nil && a0 != nil && bytes.Compare(b1, a0) <= 0 {
		return false
	}
	return true
}
