package txn

import (
	"bytes"
	"sync"
	"time"

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
	tag  string
	ch   chan error
}

type keyRange struct {
	start []byte
	end   []byte // nil = unbounded
}

// keyState.tag is a caller-supplied label (table name) for introspection
// (system.locks). It is set once from the first Acquire that supplies a
// non-empty tag and never overwritten, so it survives the lock being
// re-acquired by other holders. The lock table's key namespace is shared
// across every table in one storage engine, so two different tables can in
// principle collide on identical raw key bytes; the tag reflects whichever
// caller happened to create the entry, a pre-existing, documented sharp edge
// (see TODO.md Phase 26), not something this introspection layer can fix.
type keyState struct {
	holders map[format.TxnID]Mode
	tag     string
}

// LockManager is a key / range lock table with wait-for deadlock detection.
type LockManager struct {
	mu      sync.Mutex
	keys    map[string]*keyState
	ranges  []heldRange
	waiters []*waiter
	waitFor map[format.TxnID]map[format.TxnID]struct{}
	// waitTimeout bounds how long Acquire/AcquireRange block on a contended,
	// non-deadlocking wait. 0 (the default) blocks indefinitely — only
	// deadlock cycles are ever detected without configuring this.
	waitTimeout time.Duration
}

type heldRange struct {
	txn   format.TxnID
	mode  Mode
	start []byte
	end   []byte
	tag   string
}

// LockInfo is a snapshot of one held key or range lock, for introspection
// (system.locks). Range is empty for a single-key lock.
type LockInfo struct {
	Txn   format.TxnID
	Mode  Mode
	Tag   string
	Range bool
}

func NewLockManager() *LockManager {
	return &LockManager{
		keys:    make(map[string]*keyState),
		waitFor: make(map[format.TxnID]map[format.TxnID]struct{}),
	}
}

func (lm *LockManager) Acquire(txn format.TxnID, key []byte, mode Mode, tag string) error {
	k := string(key)
	lm.mu.Lock()
	if lm.canGrantKey(txn, k, key, mode) {
		lm.grantKey(txn, k, mode, tag)
		lm.mu.Unlock()
		return nil
	}
	w := &waiter{txn: txn, mode: mode, key: k, tag: tag, ch: make(chan error, 1)}
	lm.waiters = append(lm.waiters, w)
	lm.setWait(txn, lm.blockersKey(txn, k, key, mode))
	if lm.hasCycle(txn) {
		lm.removeWaiter(w)
		lm.clearWait(txn)
		lm.mu.Unlock()
		return nerr.New(nerr.Deadlock, "txn.Lock", "deadlock detected")
	}
	wait := lm.waitTimeout
	lm.mu.Unlock()
	return lm.await(w, txn, wait)
}

// AcquireRange locks [start, end). A nil end is unbounded.
func (lm *LockManager) AcquireRange(txn format.TxnID, start, end []byte, mode Mode, tag string) error {
	lm.mu.Lock()
	if lm.canGrantRange(txn, start, end, mode) {
		lm.grantRange(txn, start, end, mode, tag)
		lm.mu.Unlock()
		return nil
	}
	w := &waiter{
		txn:  txn,
		mode: mode,
		rng:  &keyRange{start: append([]byte(nil), start...), end: append([]byte(nil), end...)},
		tag:  tag,
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
	wait := lm.waitTimeout
	lm.mu.Unlock()
	return lm.await(w, txn, wait)
}

// await blocks until w is granted (wake() sends nil on w.ch) or wait
// elapses. wait <= 0 blocks indefinitely, matching pre-timeout behavior.
// On timeout it removes w from the waiter set so no other transaction stays
// blocked behind an abandoned wait — but only if w has not already been
// granted: wake() removes a waiter from lm.waiters and sends on its channel
// atomically under lm.mu, so re-checking membership under the same mutex
// after the timer fires distinguishes "really timed out" from "granted the
// instant before the timer fired," where the grant must be honored (the
// lock is really held; failing here without releasing it would leak it).
func (lm *LockManager) await(w *waiter, txn format.TxnID, wait time.Duration) error {
	if wait <= 0 {
		return <-w.ch
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case err := <-w.ch:
		return err
	case <-timer.C:
		lm.mu.Lock()
		stillWaiting := false
		for _, x := range lm.waiters {
			if x == w {
				stillWaiting = true
				break
			}
		}
		if !stillWaiting {
			lm.mu.Unlock()
			return <-w.ch
		}
		lm.removeWaiter(w)
		lm.clearWait(txn)
		lm.mu.Unlock()
		return nerr.New(nerr.Exhausted, "txn.Lock", "lock wait timeout exceeded")
	}
}

// SetWaitTimeout changes the contended-lock wait bound for future
// Acquire/AcquireRange calls. d <= 0 disables the bound (blocks
// indefinitely, subject only to deadlock-cycle detection).
func (lm *LockManager) SetWaitTimeout(d time.Duration) {
	if d < 0 {
		d = 0
	}
	lm.mu.Lock()
	lm.waitTimeout = d
	lm.mu.Unlock()
}

// Snapshot returns every currently held key/range lock, for introspection
// (system.locks). Waiting (not-yet-granted) lock requests are not included.
func (lm *LockManager) Snapshot() []LockInfo {
	lm.mu.Lock()
	defer lm.mu.Unlock()
	out := make([]LockInfo, 0, len(lm.keys)+len(lm.ranges))
	for _, st := range lm.keys {
		for id, mode := range st.holders {
			out = append(out, LockInfo{Txn: id, Mode: mode, Tag: st.tag})
		}
	}
	for _, r := range lm.ranges {
		out = append(out, LockInfo{Txn: r.txn, Mode: r.mode, Tag: r.tag, Range: true})
	}
	return out
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

func (lm *LockManager) grantKey(txn format.TxnID, k string, mode Mode, tag string) {
	st := lm.keys[k]
	if st == nil {
		st = &keyState{holders: make(map[format.TxnID]Mode)}
		lm.keys[k] = st
	}
	if st.tag == "" && tag != "" {
		st.tag = tag
	}
	if have, ok := st.holders[txn]; !ok || mode == Exclusive || have == Exclusive {
		st.holders[txn] = mode
	}
}

func (lm *LockManager) grantRange(txn format.TxnID, start, end []byte, mode Mode, tag string) {
	lm.ranges = append(lm.ranges, heldRange{
		txn:   txn,
		mode:  mode,
		start: append([]byte(nil), start...),
		end:   append([]byte(nil), end...),
		tag:   tag,
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
				lm.grantRange(w.txn, w.rng.start, w.rng.end, w.mode, w.tag)
			} else {
				lm.grantKey(w.txn, w.key, w.mode, w.tag)
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
