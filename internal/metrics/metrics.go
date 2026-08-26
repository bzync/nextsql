// Package metrics is the process observability registry. Counters never
// include passwords, keys, tokens, or secrets.
package metrics

import (
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const ringSize = 2048

// Registry is a thread-safe set of engine counters and a latency ring.
type Registry struct {
	queries   atomic.Int64
	errors    atomic.Int64
	commits   atomic.Int64
	rollbacks atomic.Int64
	rejected  atomic.Int64
	admitted  atomic.Int64
	canceled  atomic.Int64
	rows      atomic.Int64

	sealNs    atomic.Int64
	sealN     atomic.Int64
	sealBytes atomic.Int64
	openNs    atomic.Int64
	openN     atomic.Int64
	openBytes atomic.Int64
	walBytes  atomic.Int64
	isolated  atomic.Int64
	repaired  atomic.Int64

	fkChecks            atomic.Int64
	fkViolations        atomic.Int64
	fkCascadeRows       atomic.Int64
	fkCascadeRejects    atomic.Int64
	indexRebuilds       atomic.Int64
	indexRebuildFailure atomic.Int64
	indexRebuildRows    atomic.Int64
	indexRebuildEntries atomic.Int64
	indexRebuildNs      atomic.Int64
	maintenanceRuns     atomic.Int64
	maintenanceFailures atomic.Int64
	maintenanceRows     atomic.Int64
	maintenanceNs       atomic.Int64
	cdcSubscriptions    atomic.Int64
	cdcActive           atomic.Int64
	cdcTransactions     atomic.Int64
	cdcEvents           atomic.Int64
	cdcErrors           atomic.Int64
	cdcLagLSN           atomic.Uint64

	mu   sync.Mutex
	ring []int64
	pos  int
	n    int
	born time.Time
}

// Snapshot is a point-in-time view suitable for diagnose / benches.
type Snapshot struct {
	Queries              int64
	Errors               int64
	Commits              int64
	Rollbacks            int64
	Rejected             int64
	Admitted             int64
	Canceled             int64
	Rows                 int64
	QPS                  float64
	TPS                  float64
	P50                  time.Duration
	P95                  time.Duration
	P99                  time.Duration
	P999                 time.Duration
	SealNs               int64
	SealOps              int64
	SealBytes            int64
	OpenNs               int64
	OpenOps              int64
	OpenBytes            int64
	EncryptPct           float64
	WALBytes             int64
	Isolated             int64
	Repaired             int64
	FKChecks             int64
	FKViolations         int64
	FKCascadeRows        int64
	FKCascadeRejects     int64
	IndexRebuilds        int64
	IndexRebuildFailures int64
	IndexRebuildRows     int64
	IndexRebuildEntries  int64
	IndexRebuildDuration time.Duration
	MaintenanceRuns      int64
	MaintenanceFailures  int64
	MaintenanceRows      int64
	MaintenanceDuration  time.Duration
	CDCSubscriptions     int64
	CDCActive            int64
	CDCTransactions      int64
	CDCEvents            int64
	CDCErrors            int64
	CDCLagLSN            uint64
	HeapAlloc            uint64
	TotalAlloc           uint64
	Sys                  uint64
	NumGC                uint32
	NumGoroutine         int
	NumCPU               int
	GOMAXPROCS           int
	Uptime               time.Duration
}

var process = New()

// Default is the process-wide registry (crypto / WAL hooks).
func Default() *Registry { return process }

// New returns an empty registry.
func New() *Registry {
	return &Registry{born: time.Now(), ring: make([]int64, ringSize)}
}

func (r *Registry) ObserveQuery(d time.Duration, err error) {
	if r == nil {
		return
	}
	r.queries.Add(1)
	if err != nil {
		r.errors.Add(1)
		if nerr.HasCode(err, nerr.Canceled) || nerr.HasCode(err, nerr.Exhausted) {
			r.canceled.Add(1)
		}
	}
	ns := d.Nanoseconds()
	if ns < 0 {
		ns = 0
	}
	r.mu.Lock()
	r.ring[r.pos] = ns
	r.pos = (r.pos + 1) % ringSize
	if r.n < ringSize {
		r.n++
	}
	r.mu.Unlock()
}

func (r *Registry) AddCommit() {
	if r != nil {
		r.commits.Add(1)
	}
}

func (r *Registry) AddRollback() {
	if r != nil {
		r.rollbacks.Add(1)
	}
}

func (r *Registry) AddRejected() {
	if r != nil {
		r.rejected.Add(1)
	}
}

func (r *Registry) AddAdmitted() {
	if r != nil {
		r.admitted.Add(1)
	}
}

func (r *Registry) AddRows(n int64) {
	if r != nil && n > 0 {
		r.rows.Add(n)
	}
}

func (r *Registry) ObserveSeal(n int64, d time.Duration) {
	if r == nil || n <= 0 {
		return
	}
	r.sealN.Add(1)
	r.sealBytes.Add(n)
	r.sealNs.Add(d.Nanoseconds())
}

func (r *Registry) ObserveOpen(n int64, d time.Duration) {
	if r == nil || n <= 0 {
		return
	}
	r.openN.Add(1)
	r.openBytes.Add(n)
	r.openNs.Add(d.Nanoseconds())
}

func (r *Registry) AddWAL(n int64) {
	if r != nil && n > 0 {
		r.walBytes.Add(n)
	}
}

func (r *Registry) AddIsolated() {
	if r != nil {
		r.isolated.Add(1)
	}
}

func (r *Registry) AddRepaired() {
	if r != nil {
		r.repaired.Add(1)
	}
}

func (r *Registry) AddFKCheck() {
	if r != nil {
		r.fkChecks.Add(1)
	}
}

func (r *Registry) AddFKViolation() {
	if r != nil {
		r.fkViolations.Add(1)
	}
}

func (r *Registry) AddFKCascadeRows(n int64) {
	if r != nil && n > 0 {
		r.fkCascadeRows.Add(n)
	}
}

func (r *Registry) AddFKCascadeReject() {
	if r != nil {
		r.fkCascadeRejects.Add(1)
	}
}

func (r *Registry) ObserveIndexRebuild(rows, entries int64, d time.Duration, err error) {
	if r == nil {
		return
	}
	r.indexRebuilds.Add(1)
	if err != nil {
		r.indexRebuildFailure.Add(1)
	}
	if rows > 0 {
		r.indexRebuildRows.Add(rows)
	}
	if entries > 0 {
		r.indexRebuildEntries.Add(entries)
	}
	r.indexRebuildNs.Add(d.Nanoseconds())
}

func (r *Registry) ObserveMaintenance(rows int64, d time.Duration, err error) {
	if r == nil {
		return
	}
	r.maintenanceRuns.Add(1)
	if err != nil {
		r.maintenanceFailures.Add(1)
	}
	if rows > 0 {
		r.maintenanceRows.Add(rows)
	}
	r.maintenanceNs.Add(d.Nanoseconds())
}

// AddCDCSubscription records one admitted stream and increments the active
// gauge. No table, tenant, key, or token labels are retained.
func (r *Registry) AddCDCSubscription() {
	if r != nil {
		r.cdcSubscriptions.Add(1)
		r.cdcActive.Add(1)
	}
}

func (r *Registry) CloseCDCSubscription() {
	if r != nil {
		r.cdcActive.Add(-1)
	}
}

func (r *Registry) AddCDCDelivery(transactions, events int64, lag uint64) {
	if r == nil {
		return
	}
	if transactions > 0 {
		r.cdcTransactions.Add(transactions)
	}
	if events > 0 {
		r.cdcEvents.Add(events)
	}
	r.cdcLagLSN.Store(lag)
}

func (r *Registry) AddCDCError() {
	if r != nil {
		r.cdcErrors.Add(1)
	}
}

// Snapshot copies counters and process memory stats.
func (r *Registry) Snapshot() Snapshot {
	if r == nil {
		return Snapshot{}
	}
	up := time.Since(r.born)
	if up <= 0 {
		up = time.Nanosecond
	}
	s := Snapshot{
		Queries:              r.queries.Load(),
		Errors:               r.errors.Load(),
		Commits:              r.commits.Load(),
		Rollbacks:            r.rollbacks.Load(),
		Rejected:             r.rejected.Load(),
		Admitted:             r.admitted.Load(),
		Canceled:             r.canceled.Load(),
		Rows:                 r.rows.Load(),
		SealNs:               r.sealNs.Load(),
		SealOps:              r.sealN.Load(),
		SealBytes:            r.sealBytes.Load(),
		OpenNs:               r.openNs.Load(),
		OpenOps:              r.openN.Load(),
		OpenBytes:            r.openBytes.Load(),
		WALBytes:             r.walBytes.Load(),
		Isolated:             r.isolated.Load(),
		Repaired:             r.repaired.Load(),
		FKChecks:             r.fkChecks.Load(),
		FKViolations:         r.fkViolations.Load(),
		FKCascadeRows:        r.fkCascadeRows.Load(),
		FKCascadeRejects:     r.fkCascadeRejects.Load(),
		IndexRebuilds:        r.indexRebuilds.Load(),
		IndexRebuildFailures: r.indexRebuildFailure.Load(),
		IndexRebuildRows:     r.indexRebuildRows.Load(),
		IndexRebuildEntries:  r.indexRebuildEntries.Load(),
		IndexRebuildDuration: time.Duration(r.indexRebuildNs.Load()),
		MaintenanceRuns:      r.maintenanceRuns.Load(),
		MaintenanceFailures:  r.maintenanceFailures.Load(),
		MaintenanceRows:      r.maintenanceRows.Load(),
		MaintenanceDuration:  time.Duration(r.maintenanceNs.Load()),
		CDCSubscriptions:     r.cdcSubscriptions.Load(),
		CDCActive:            r.cdcActive.Load(),
		CDCTransactions:      r.cdcTransactions.Load(),
		CDCEvents:            r.cdcEvents.Load(),
		CDCErrors:            r.cdcErrors.Load(),
		CDCLagLSN:            r.cdcLagLSN.Load(),
		NumCPU:               runtime.NumCPU(),
		GOMAXPROCS:           runtime.GOMAXPROCS(0),
		NumGoroutine:         runtime.NumGoroutine(),
		Uptime:               up,
	}
	s.QPS = float64(s.Queries) / up.Seconds()
	s.TPS = float64(s.Commits) / up.Seconds()
	cryptoNs := s.SealNs + s.OpenNs
	if up.Nanoseconds() > 0 && cryptoNs > 0 {
		s.EncryptPct = 100 * float64(cryptoNs) / float64(up.Nanoseconds())
	}
	r.mu.Lock()
	s.P50, s.P95, s.P99, s.P999 = percentiles(r.ring, r.n, r.pos)
	r.mu.Unlock()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	s.HeapAlloc = ms.HeapAlloc
	s.TotalAlloc = ms.TotalAlloc
	s.Sys = ms.Sys
	s.NumGC = ms.NumGC
	return s
}

func percentiles(ring []int64, n, pos int) (p50, p95, p99, p999 time.Duration) {
	if n <= 0 {
		return 0, 0, 0, 0
	}
	sample := make([]int64, n)
	if n < len(ring) {
		copy(sample, ring[:n])
	} else {
		copy(sample, ring[pos:])
		copy(sample[len(ring)-pos:], ring[:pos])
	}
	sort.Slice(sample, func(i, j int) bool { return sample[i] < sample[j] })
	pick := func(p float64) time.Duration {
		if len(sample) == 1 {
			return time.Duration(sample[0])
		}
		idx := int(float64(len(sample)-1) * p)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(sample) {
			idx = len(sample) - 1
		}
		return time.Duration(sample[idx])
	}
	return pick(0.50), pick(0.95), pick(0.99), pick(0.999)
}
