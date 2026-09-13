package auth

import (
	"context"
	"runtime"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// Password hashing is the one per-connection cost that runs before a client
// has proven anything. An Argon2id verification allocates its full memory cost
// (64 MiB for a current record, and a validated record read from disk may ask
// for up to maxArgon2MemoryKiB) and a login with an unknown user runs a dummy
// verification at the same cost on purpose, so usernames cannot be probed by
// timing. Without a bound, N simultaneous connection attempts -- with any
// password, for any user -- allocate N times that: measured, 64 wrong-password
// clients took an in-process server from 9 MiB to 3.8 GiB of heap. That is an
// unauthenticated out-of-memory path.
//
// The gate bounds how many hashes run at once, process-wide, because memory is
// a process-wide resource. Weakening the hash parameters would also cut memory,
// but security ranks above efficiency in this project's priority order, so the
// cost per hash is kept and only the concurrency is bounded.
//
// The default follows from Argon2id's own parallelism: each hash already runs
// argon2Threads lanes, so running more hashes than GOMAXPROCS/argon2Threads at
// once adds memory and contention without adding throughput.

// hashWaitCap bounds how long a caller without its own deadline waits for a
// slot, so no path can queue forever behind a saturated gate.
const hashWaitCap = 30 * time.Second

type hashGate struct {
	slots chan struct{}
}

var currentGate atomic.Pointer[hashGate]

func init() { SetMaxConcurrentPasswordHashes(0) }

// DefaultMaxConcurrentPasswordHashes is the gate size used when none is
// configured: one hash per argon2Threads schedulable CPUs, and at least two so
// a single slow login cannot serialize every other one.
func DefaultMaxConcurrentPasswordHashes() int {
	n := runtime.GOMAXPROCS(0) / int(argon2Threads)
	if n < 2 {
		n = 2
	}
	return n
}

// SetMaxConcurrentPasswordHashes resizes the process-wide gate; n <= 0 selects
// DefaultMaxConcurrentPasswordHashes. A hash already holding a slot finishes
// against the gate it acquired from, so resizing never strands a waiter.
func SetMaxConcurrentPasswordHashes(n int) {
	if n <= 0 {
		n = DefaultMaxConcurrentPasswordHashes()
	}
	currentGate.Store(&hashGate{slots: make(chan struct{}, n)})
}

// MaxConcurrentPasswordHashes reports the current gate size.
func MaxConcurrentPasswordHashes() int { return cap(currentGate.Load().slots) }

// acquireHashSlot waits for a slot until ctx ends or hashWaitCap passes,
// whichever is first, and returns the function that gives the slot back. A
// caller that cannot get a slot is told the server is busy, which is not an
// authentication verdict and says nothing about whether the user exists.
func acquireHashSlot(ctx context.Context) (func(), error) {
	g := currentGate.Load()
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	default:
	}
	ctx, cancel := context.WithTimeout(ctx, hashWaitCap)
	defer cancel()
	select {
	case g.slots <- struct{}{}:
		return func() { <-g.slots }, nil
	case <-ctx.Done():
		return nil, nerr.New(nerr.Exhausted, "auth", "authentication capacity exhausted; retry")
	}
}
