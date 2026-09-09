// Package limits is the operational limit catalog — the single source of truth
// for every server limit an operator can configure, what it defaults to, and
// the absolute bounds it may not be moved outside of.
//
// The distinction the catalog exists to keep is between a *default*, which an
// operator moves freely to match their hardware and workload, and a *ceiling*,
// which they cannot raise. Without the second, a legitimate tuning knob is also
// an unbounded allocation request: `buffer_pages=1000000000` is a plausible
// typo that asks for a 16 TiB buffer pool at startup, and overload must mean
// controlled rejection, never OOM.
//
// It is deliberately a leaf package (only "fmt", "sort" and internal/nerr) so
// that internal/config, the CLI flag paths, and diagnostics can all validate
// against the same numbers rather than each re-deriving their own — the failure
// mode this replaces was internal/config's Load and Validate disagreeing with
// each other while a stale third copy of the wire limits sat unused in
// internal/security. See docs/limits.md, which is generated from Catalog() and
// checked against it by TestDocsLimitsMatchCatalog.
//
// This package does not hold structural or format limits (page size, vector
// dimension, geometry vertices, catalog descriptor size). Those are not
// operational knobs — changing one is a format or encoding change, and they
// live with the code that defines the encoding.
package limits

import (
	"fmt"
	"sort"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// Class groups limits by the resource an operator is actually controlling.
type Class string

const (
	// ClassWire bounds a single request's decoded bytes before allocation.
	ClassWire Class = "wire"
	// ClassMemory bounds resident memory the server reserves or accumulates.
	ClassMemory Class = "memory"
	// ClassConcurrency bounds how many things run or queue at once.
	ClassConcurrency Class = "concurrency"
	// ClassResult bounds the work one query is allowed to produce.
	ClassResult Class = "result"
	// ClassTime bounds how long something may take or wait.
	ClassTime Class = "time"
	// ClassStorage bounds durable space and retained history.
	ClassStorage Class = "storage"
)

// Unit names what a limit counts, for display.
type Unit string

const (
	UnitBytes   Unit = "bytes"
	UnitPages   Unit = "16 KiB pages"
	UnitRows    Unit = "rows"
	UnitCount   Unit = "count"
	UnitMillis  Unit = "ms"
	UnitEntries Unit = "entries"
)

// Spec is one configurable limit.
type Spec struct {
	// Key is the nextsql.conf key. Flags and programmatic Config fields
	// validate against the same key.
	Key   string
	Class Class
	Unit  Unit

	// Default is the value a fresh configuration carries. Zero means the
	// owning subsystem's own default is left in place rather than overridden.
	Default int

	// Min and Max are the inclusive absolute bounds of a configured value.
	// Max is the ceiling: an operator tunes below it, never above.
	Min, Max int

	// ZeroMeans documents the sentinel when 0 is accepted alongside the
	// [Min, Max] range — a disabled feature, an unbounded setting, or "leave
	// the subsystem default". Empty when 0 is not a legal value.
	ZeroMeans string

	// Why states what the ceiling protects. A ceiling without a reason is a
	// number nobody can revise responsibly.
	Why string
}

// AcceptsZero reports whether 0 is a legal value for this limit.
func (s Spec) AcceptsZero() bool { return s.ZeroMeans != "" }

// Range renders the accepted values for an operator-facing message.
func (s Spec) Range() string {
	if s.AcceptsZero() {
		return fmt.Sprintf("0 or [%d, %d]", s.Min, s.Max)
	}
	return fmt.Sprintf("[%d, %d]", s.Min, s.Max)
}

// DefaultString renders Default for display in documentation. Zero renders as
// "0" when 0 is accepted, or as "subsystem default" when an unset field leaves
// the subsystem's compiled-in default in place.
func (s Spec) DefaultString() string {
	if s.Default != 0 {
		return fmt.Sprintf("%d", s.Default)
	}
	if s.AcceptsZero() {
		return "0"
	}
	return "subsystem default"
}

const (
	// maxBufferPages is 64 GiB of 16 KiB pages. The buffer pool is reserved
	// at startup, so this is a real allocation an operator can demand.
	maxBufferPages = 4 << 20
	// maxPreallocPages is 16 GiB of runway per database. The runway is
	// fallocate'd, so it consumes real blocks per open database.
	maxPreallocPages = 1 << 20
	// maxFrameBytes is the hard allocation ceiling for one decoded request.
	maxFrameBytes = 64 << 20
	// maxParams is the wire representation: the parameter count is a uint16.
	maxParams = 65535
	// maxDay is 24 hours. A limit above it is not a timeout an operator is
	// relying on; it is one they meant to disable.
	maxDay = 24 * 60 * 60 * 1000
	// maxHour bounds a wait an in-flight request holds resources across.
	maxHour = 60 * 60 * 1000
	// maxYear bounds retained history expressed in milliseconds.
	maxYear = 365 * 24 * 60 * 60 * 1000
	// maxSockets bounds per-connection state: every accepted connection
	// carries a session, buffers and file descriptors.
	maxSockets = 1 << 20
	// minRaftTimingMS floors every Raft interval. hashicorp/raft's own
	// minimum is 5 ms; this is deliberately higher because a heartbeat
	// shorter than a plausible scheduler delay makes a healthy node
	// re-campaign instead of converging — the failure is a cluster that
	// never elects, not a rejected value.
	minRaftTimingMS = 10
	// maxRaftTimingMS bounds a Raft interval. An election that may take a
	// minute to notice a dead leader is an outage an operator is choosing,
	// not a timeout they are relying on.
	maxRaftTimingMS = 60 * 1000
)

// The built-in Raft intervals, in milliseconds — the values a cluster runs
// with when the operator configures none.
//
// They live here, with the bounds that constrain them, so that
// internal/replication (which applies them), internal/config (which must not
// import the replication stack to validate them) and the generated limit
// documentation all read the same numbers instead of keeping three copies that
// can drift apart.
const (
	DefaultRaftHeartbeatMS     = 250
	DefaultRaftElectionMS      = 250
	DefaultRaftLeaderLeaseMS   = 200
	DefaultRaftCommitTimeoutMS = 50
)

// catalog is the authoritative list. Keep it sorted by key.
var catalog = []Spec{
	{
		Key: "buffer_pages", Class: ClassMemory, Unit: UnitPages,
		Default: 1024, Min: 1, Max: maxBufferPages,
		Why: "the buffer pool is reserved at startup; the ceiling is 64 GiB of pages, so a mistyped value is rejected instead of becoming an allocation the host cannot satisfy",
	},
	{
		Key: "checkpoint_interval_ms", Class: ClassTime, Unit: UnitMillis,
		Default: 300_000, Min: 1, Max: maxDay, ZeroMeans: "no periodic checkpoint; the redo boundary advances only on clean close or backup",
		Why: "bounds the WAL suffix an unclean restart must replay before the node can listen",
	},
	{
		Key: "disk_watermark_check_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay, ZeroMeans: "disk watermarks disabled",
		Why: "a background probe; a longer period than a day is an operator meaning to disable it",
	},
	{
		Key: "idle_timeout_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay,
		Why: "an idle connection holds a session, its buffers and a file descriptor",
	},
	{
		Key: "idle_transaction_timeout_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay, ZeroMeans: "no idle-transaction timeout",
		Why: "an idle open transaction pins a snapshot and its UNDO, holding back vacuum",
	},
	{
		Key: "lock_timeout_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay, ZeroMeans: "wait indefinitely for a lock",
		Why: "bounds how long one statement blocks holding its own locks",
	},
	{
		Key: "max_connections", Class: ClassConcurrency, Unit: UnitCount,
		Min: 1, Max: maxSockets,
		Why: "every accepted connection carries a session, buffers and a file descriptor",
	},
	{
		Key: "max_connections_per_database", Class: ClassConcurrency, Unit: UnitCount,
		Min: 1, Max: maxSockets, ZeroMeans: "unlimited within max_connections",
		Why: "keeps one database from consuming the whole connection budget",
	},
	{
		Key: "max_connections_per_realm", Class: ClassConcurrency, Unit: UnitCount,
		Min: 1, Max: maxSockets, ZeroMeans: "unlimited within max_connections",
		Why: "keeps one tenant realm from consuming the whole connection budget",
	},
	{
		Key: "max_connections_per_user", Class: ClassConcurrency, Unit: UnitCount,
		Min: 1, Max: maxSockets, ZeroMeans: "unlimited within max_connections",
		Why: "keeps one principal from consuming the whole connection budget",
	},
	{
		Key: "max_frame_bytes", Class: ClassWire, Unit: UnitBytes,
		Default: 64 << 20, Min: 64, Max: maxFrameBytes,
		Why: "the total decoded bytes of one request, checked before allocation; independent of the statement limit so a statement cannot claim the whole frame by default",
	},
	{
		Key: "max_inflight_queries", Class: ClassConcurrency, Unit: UnitCount,
		Default: 32, Min: 1, Max: 65536,
		Why: "each admitted query holds a worker, a memory budget and a snapshot; admission control is what turns overload into queueing instead of OOM",
	},
	{
		Key: "max_open_databases", Class: ClassMemory, Unit: UnitCount,
		Default: 8, Min: 1, Max: 65536, ZeroMeans: "leave the default",
		Why: "accepted and ignored: multi-database hosting was removed and a deployment opens exactly one database. Retained so a nextsql.conf written by an earlier release still loads",
	},
	{
		Key: "max_parameters", Class: ClassWire, Unit: UnitCount,
		Default: maxParams, Min: 1, Max: maxParams,
		Why: "the wire parameter count is a uint16, so the ceiling is the representation; decoding grows only with the parameters actually supplied",
	},
	{
		Key: "max_prepared_statements", Class: ClassMemory, Unit: UnitCount,
		Default: 64, Min: 1, Max: 4096,
		Why: "each prepared statement retains parsed and planned state for the lifetime of a session; the ceiling bounds per-connection resident memory",
	},
	{
		Key: "max_query_queue", Class: ClassConcurrency, Unit: UnitCount,
		Default: 128, Min: 1, Max: 1 << 20, ZeroMeans: "no queue; reject immediately past max_inflight_queries",
		Why: "a queued request holds its decoded frame while it waits",
	},
	{
		Key: "max_result_bytes", Class: ClassResult, Unit: UnitBytes,
		Default: 64 << 20, Min: 1, Max: maxFrameBytes,
		Why: "bounds the bytes one query may produce; a row budget must not silently change SQL semantics, so this fails the query rather than truncating it",
	},
	{
		Key: "max_result_rows", Class: ClassResult, Unit: UnitRows,
		Default: 1_000_000, Min: 1, Max: 1 << 31,
		Why: "bounds buffered and streamed rows per query; exceeding it is an explicit exhausted error, never a silent LIMIT",
	},
	{
		Key: "max_statement_bytes", Class: ClassWire, Unit: UnitBytes,
		Default: 16 << 20, Min: 1, Max: maxFrameBytes,
		Why: "bounds SQL text handed to the lexer and parser; must not exceed max_frame_bytes, which already bounds the whole request",
	},
	{
		Key: "max_total_buffer_pages", Class: ClassMemory, Unit: UnitPages,
		Min: 1, Max: maxBufferPages, ZeroMeans: "unbounded; each database keeps its own buffer_pages",
		Why: "the shared budget across every open database in a multi-database deployment; must be at least buffer_pages",
	},
	{
		Key: "prealloc_ahead_pages", Class: ClassStorage, Unit: UnitPages,
		Default: 16384, Min: 1, Max: maxPreallocPages,
		Why: "the allocation runway fallocate reserves ahead of the data file, per database; real blocks, so many small databases multiply it",
	},
	{
		Key: "query_queue_wait_ms", Class: ClassTime, Unit: UnitMillis,
		Default: 5000, Min: 1, Max: maxHour,
		Why: "how long an admitted-but-queued request waits before being rejected; it holds its decoded frame the whole time",
	},
	{
		Key: "replica_lag_check_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay, ZeroMeans: "replica lag monitoring disabled",
		Why: "a background probe; a longer period than a day is an operator meaning to disable it",
	},
	{
		Key: "replica_lag_warn_entries", Class: ClassConcurrency, Unit: UnitEntries,
		Min: 1, Max: 1 << 31, ZeroMeans: "no lag warning threshold",
		Why: "the replicated-entry backlog that raises an operational warning",
	},
	{
		Key: "raft_commit_timeout_ms", Class: ClassTime, Unit: UnitMillis,
		Min: minRaftTimingMS, Max: maxRaftTimingMS, ZeroMeans: defaultRaftZeroMeans(DefaultRaftCommitTimeoutMS),
		Why: "how long a leader batches log entries before flushing them to followers; raising it trades commit latency for fewer, larger append rounds",
	},
	{
		Key: "raft_election_ms", Class: ClassTime, Unit: UnitMillis,
		Min: minRaftTimingMS, Max: maxRaftTimingMS, ZeroMeans: defaultRaftZeroMeans(DefaultRaftElectionMS),
		Why: "how long a follower waits without leader contact before campaigning; must be at least raft_heartbeat_ms, or a node campaigns while the leader is still healthy",
	},
	{
		Key: "raft_heartbeat_ms", Class: ClassTime, Unit: UnitMillis,
		Min: minRaftTimingMS, Max: maxRaftTimingMS, ZeroMeans: defaultRaftZeroMeans(DefaultRaftHeartbeatMS),
		Why: "the leader-contact interval, and the unit the follower-read freshness window is built from: the healthy-contact window is five heartbeats, so raising this widens the default MAX STALENESS with it",
	},
	{
		Key: "raft_leader_lease_ms", Class: ClassTime, Unit: UnitMillis,
		Min: minRaftTimingMS, Max: maxRaftTimingMS, ZeroMeans: defaultRaftZeroMeans(DefaultRaftLeaderLeaseMS),
		Why: "how long a leader may act as leader without contacting a quorum; must not exceed raft_heartbeat_ms, or a leader keeps acting past the point it can still prove it is one",
	},
	{
		Key: "shutdown_drain_ms", Class: ClassTime, Unit: UnitMillis,
		Default: 30_000, Min: 1, Max: maxHour, ZeroMeans: "do not wait for in-flight work on shutdown",
		Why: "how long a shutdown waits for in-flight statements before closing; a drain longer than an hour is an outage, not a drain",
	},
	{
		Key: "statement_timeout_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay,
		Why: "bounds one statement's wall time so a pathological query cannot hold a worker forever",
	},
	{
		Key: "task_workers", Class: ClassConcurrency, Unit: UnitCount,
		Min: 1, Max: 4096, ZeroMeans: "leave the scheduler default",
		Why: "the bounded background pool for scheduled tasks and maintenance",
	},
	{
		Key: "transaction_timeout_ms", Class: ClassTime, Unit: UnitMillis,
		Min: 1, Max: maxDay, ZeroMeans: "no transaction timeout",
		Why: "bounds a whole transaction, which holds locks and pins a snapshot for its lifetime",
	},
	{
		Key: "wal_retention_ms", Class: ClassStorage, Unit: UnitMillis,
		Min: 1, Max: maxYear, ZeroMeans: "retain WAL history indefinitely",
		Why: "how long checkpointed WAL is kept for PITR and page repair; pruning is a no-op until an archiver is configured",
	},
}

// Catalog returns every configurable limit, sorted by key.
func Catalog() []Spec {
	out := make([]Spec, len(catalog))
	copy(out, catalog)
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Lookup returns the spec for a config key.
func Lookup(key string) (Spec, bool) {
	for _, s := range catalog {
		if s.Key == key {
			return s, true
		}
	}
	return Spec{}, false
}

// Check reports whether v is an acceptable value for key. The error names the
// key, the value and the accepted range, so an operator can fix a rejected
// configuration without reading the source.
//
// An unknown key is an error rather than a pass: a limit that is enforced
// nowhere but validated here would be worse than no validation at all.
func Check(key string, v int) error {
	s, ok := Lookup(key)
	if !ok {
		return nerr.New(nerr.InvalidArgument, "limits.Check", fmt.Sprintf("unknown limit %q", key))
	}
	if v == 0 && s.AcceptsZero() {
		return nil
	}
	if v < s.Min || v > s.Max {
		return nerr.New(nerr.InvalidArgument, "limits.Check", fmt.Sprintf(
			"%s = %d is outside %s", key, v, s.Range()))
	}
	return nil
}

// defaultRaftZeroMeans renders the "zero leaves the default" note for one Raft
// interval from that interval's actual default, so the documentation cannot
// state a number the code does not use.
func defaultRaftZeroMeans(ms int) string {
	return fmt.Sprintf("the built-in default (%d ms)", ms)
}

// RaftTimingKeys are the Raft interval limits, in the order CheckRaftTimings
// takes them. They are related to each other, not only individually bounded,
// which is why they need a check of their own.
var RaftTimingKeys = struct{ Heartbeat, Election, LeaderLease, CommitTimeout string }{
	Heartbeat:     "raft_heartbeat_ms",
	Election:      "raft_election_ms",
	LeaderLease:   "raft_leader_lease_ms",
	CommitTimeout: "raft_commit_timeout_ms",
}

// CheckRaftTimings validates the Raft intervals against each other as well as
// against their individual ranges. Zero means "leave the built-in default in
// place" and is resolved here before the relationship is judged, so a
// partially configured set is checked as the cluster will actually run it.
//
// The two relationships are consensus-protocol requirements, not preferences:
// a leader lease longer than the heartbeat lets a leader keep acting past the
// point it can still prove it holds quorum, and an election timeout shorter
// than the heartbeat makes a follower campaign against a leader that is
// contacting it on schedule. hashicorp/raft rejects both at Raft construction;
// checking them here means an operator learns at configuration time instead of
// at the next restart.
//
// It lives in this package rather than in internal/replication so that
// internal/config — which must not import the replication stack — validates
// against the same rule the cluster enforces, rather than a second copy of it.
func CheckRaftTimings(heartbeatMS, electionMS, leaderLeaseMS, commitTimeoutMS int) error {
	const op = "limits.CheckRaftTimings"
	heartbeatMS = raftOrDefault(heartbeatMS, DefaultRaftHeartbeatMS)
	electionMS = raftOrDefault(electionMS, DefaultRaftElectionMS)
	leaderLeaseMS = raftOrDefault(leaderLeaseMS, DefaultRaftLeaderLeaseMS)
	commitTimeoutMS = raftOrDefault(commitTimeoutMS, DefaultRaftCommitTimeoutMS)
	pairs := []struct {
		key string
		v   int
	}{
		{RaftTimingKeys.Heartbeat, heartbeatMS},
		{RaftTimingKeys.Election, electionMS},
		{RaftTimingKeys.LeaderLease, leaderLeaseMS},
		{RaftTimingKeys.CommitTimeout, commitTimeoutMS},
	}
	for _, p := range pairs {
		if err := Check(p.key, p.v); err != nil {
			return err
		}
	}
	if leaderLeaseMS > heartbeatMS {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf(
			"%s = %d must not exceed %s = %d: a leader would keep acting past the point it can still prove it holds quorum",
			RaftTimingKeys.LeaderLease, leaderLeaseMS, RaftTimingKeys.Heartbeat, heartbeatMS))
	}
	if electionMS < heartbeatMS {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf(
			"%s = %d must be at least %s = %d: a follower would campaign against a leader that is contacting it on schedule",
			RaftTimingKeys.Election, electionMS, RaftTimingKeys.Heartbeat, heartbeatMS))
	}
	return nil
}

// raftOrDefault resolves a "leave the built-in default" zero to that default.
func raftOrDefault(v, def int) int {
	if v == 0 {
		return def
	}
	return v
}

// Classes returns all recognized limit classes in display order.
func Classes() []Class {
	return []Class{
		ClassWire,
		ClassMemory,
		ClassConcurrency,
		ClassResult,
		ClassTime,
		ClassStorage,
	}
}

// ByClass returns every limit in the catalog belonging to class, sorted by key.
func ByClass(c Class) []Spec {
	var out []Spec
	for _, s := range Catalog() {
		if s.Class == c {
			out = append(out, s)
		}
	}
	return out
}

// RenderMarkdown formats the complete markdown documentation for docs/limits.md.
// It includes the authoritative operational limits table generated from
// Catalog() as well as the structural and format limits inventory.
func RenderMarkdown() string {
	var b strings.Builder
	b.WriteString("# Production limits\n\n")
	b.WriteString("This document is the authoritative limit catalog for NextSQL. The operational\n")
	b.WriteString("server limits table is generated from `internal/limits.Catalog()` and checked against it\n")
	b.WriteString("by `TestDocsLimitsMatchCatalog`.\n\n")
	b.WriteString("The distinction the catalog exists to keep is between a *default*, which an\n")
	b.WriteString("operator moves freely to match their hardware and workload, and a *ceiling*,\n")
	b.WriteString("which they cannot raise. Without the second, a legitimate tuning knob is also\n")
	b.WriteString("an unbounded allocation request: `buffer_pages=1000000000` is a plausible\n")
	b.WriteString("typo that asks for a 16 TiB buffer pool at startup, and overload must mean\n")
	b.WriteString("controlled rejection, never OOM.\n\n")
	b.WriteString("## Operational limits\n\n")
	b.WriteString("| Key | Class | Unit | Default | Accepted range | Zero means | Why |\n")
	b.WriteString("| --- | --- | --- | ---: | --- | --- | --- |\n")
	for _, s := range Catalog() {
		zeroMeans := s.ZeroMeans
		if zeroMeans == "" {
			zeroMeans = "-"
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s |\n",
			s.Key, s.Class, s.Unit, s.DefaultString(), s.Range(), zeroMeans, s.Why)
	}
	b.WriteString("\n## Structural and format limits\n\n")
	b.WriteString("This catalog does not hold structural or format limits (page size, vector\n")
	b.WriteString("dimension, geometry vertices, catalog descriptor size). Those are not\n")
	b.WriteString("operational knobs — changing one is a format or encoding change, and they\n")
	b.WriteString("live with the code that defines the encoding.\n\n")
	b.WriteString("| Limit | Value | Scope / location | Reason and failure mode |\n")
	b.WriteString("| --- | ---: | --- | --- |\n")
	b.WriteString("| Logical page | 16 KiB | `internal/storage/format` | Structural page layout; changing it requires a format migration. |\n")
	b.WriteString("| JSON bytes / depth | 1 MiB / 32 | `internal/json` | Prevents oversized allocation and recursive parser exhaustion. |\n")
	b.WriteString("| Dense vector dimension | 8192 | `internal/sql/types` | Bounds vector allocation and index work. |\n")
	b.WriteString("| Sparse vector dimension / NNZ | 2^24 / 65536 | `internal/vector` | Prevents coordinate and posting-list abuse. |\n")
	b.WriteString("| Fixed geo vertices | 256 | `internal/sql/types` | LINESTRING/POLYGON abuse cap; lifting requires serialized-byte/work budgets. |\n")
	b.WriteString("| General spatial vertices / depth / parts | 65536 / 8 / 4096 | `internal/sql/types` | GEOMETRY/GEOGRAPHY recursive construction protection. |\n")
	b.WriteString("| Collection nesting / elements | 8 / 2^20 | `internal/sql/types` | Protects recursive type/decoder and allocation paths. |\n")
	b.WriteString("| JOIN relations | 8 | `internal/security` | Planner complexity protection; do not raise without independent work budgets. |\n")
	b.WriteString("| FK cascade depth / rows | 8 / 100,000 | `internal/security` | Bounds recursive mutation, WAL growth and transaction work. |\n")
	b.WriteString("| Workflow statements / params | 256 / 64 | `internal/catalog` | Bounds stored descriptor and execution work. |\n")
	b.WriteString("| FTS fuzzy vocabulary | 4096 | `internal/fulltext` | Bounds vocabulary expansion and CPU work. |\n")
	return b.String()
}
