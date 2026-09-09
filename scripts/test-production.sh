#!/bin/bash
# Run a bounded, evidence-oriented production test profile.  Long-running
# fuzzing and soaks are intentionally separate from the PR profile.
set -euo pipefail

profile=${1:-production}
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo_dir"
# Storage tests exercise durable preallocation and can legitimately consume
# hundreds of MiB per temporary database. Keep their short-lived files on the
# repository filesystem rather than an arbitrarily small system /tmp mount.
# A killed run (timeout, Ctrl-C) leaves every t.TempDir() behind, and each one
# can hold a preallocated database, so the runner reclaims the scratch space it
# owns. It must never reclaim a *live* run's directory: two concurrent runs
# once shared one scratch path, and the second run's startup wipe deleted the
# first run's open t.TempDir() mid-test, failing it with a bogus
#   wal.writeControl: create: .../nextsql.db.wal/control.tmp: no such file or directory
# that looks exactly like a storage bug. So each run gets its own subdirectory
# keyed by pid, and startup prunes only subdirectories whose owning process is
# gone. An operator-supplied NEXTSQL_TEST_TMPDIR is used as-is and never
# pruned.
if [ -n "${NEXTSQL_TEST_TMPDIR:-}" ]; then
  test_tmpdir=$NEXTSQL_TEST_TMPDIR
else
  test_tmpdir_root="$repo_dir/.test-tmp-production"
  mkdir -p "$test_tmpdir_root"
  for stale in "$test_tmpdir_root"/run-*; do
    [ -d "$stale" ] || continue
    stale_pid=${stale##*/run-}
    case "$stale_pid" in
      ''|*[!0-9]*) continue ;;
    esac
    # kill -0 succeeding means the pid is alive (or alive and not ours); either
    # way, leave the directory alone. Erring towards a leaked directory is
    # cheap; erring the other way corrupts a running gate.
    if kill -0 "$stale_pid" 2>/dev/null; then
      continue
    fi
    rm -rf "$stale"
  done
  test_tmpdir="$test_tmpdir_root/run-$$"
  rm -rf "$test_tmpdir"
fi
mkdir -p "$test_tmpdir"
export TMPDIR="$test_tmpdir"

# Record the filesystem the durable tests actually run on. /tmp is tmpfs on
# some development hosts, and an unconfigured run there fsyncs into RAM: it
# proves nothing about durability while looking like a full green profile.
# Retained CI evidence must carry this line.
test_fs=$(stat -f -c %T "$test_tmpdir" 2>/dev/null || echo unknown)
echo "test scratch: $test_tmpdir (filesystem: $test_fs)"
case "$test_fs" in
  tmpfs|ramfs)
    echo "WARNING: the durable test profile is running on a RAM-backed filesystem;" >&2
    echo "         its fsyncs never reach a device, so it is not durability evidence." >&2
    if [ -n "${NEXTSQL_REQUIRE_DURABLE_FS:-}" ]; then
      echo "NEXTSQL_REQUIRE_DURABLE_FS is set: refusing to report a durability result from RAM." >&2
      exit 2
    fi
    ;;
esac

# These tests are dominated by real fsync latency, not CPU: internal/backup
# spends ~82% of its wall time waiting on I/O, and every durable package
# preallocates a ~256 MiB runway per temporary database. go test defaults to
# one package per CPU, so the untuned profile ran a dozen of them against one
# volume at once, inflating fsync latency until packages blew the default 10m
# per-package timeout while still making progress. Parallelism buys nothing
# here — the disk is the bottleneck — so the durable set runs serially and the
# CPU-bound set keeps the default fan-out.
#
# TIMEOUT is per package and deliberately generous: a durable package on a slow
# volume can legitimately take many minutes, and a timeout that trips on a slow
# disk teaches everyone to ignore it. It still fails a genuine hang.
IO_PARALLEL=${NEXTSQL_TEST_IO_PARALLEL:-1}
TIMEOUT=${NEXTSQL_TEST_TIMEOUT:-45m}

# CPU-bound: pure decoders, policy and scheduling logic.
#
# internal/replication belongs here rather than with the engine set even though
# it is neither a decoder nor policy: its clusters use in-memory transport and
# stores, so it does no disk work, but Raft elects on a 250 ms timeout and a
# node that cannot complete a round inside that window re-campaigns instead of
# converging. Run while the machine is still quiet it is stable; run after the
# disk-saturating suites it reported "no leader" for an engine that was
# behaving correctly. The intervals are configurable now (docs/ha.md), but a
# deployment's own timings are not a test-ordering knob: the suite must prove
# the shipped defaults work, so ordering stays the lever here.
#
# internal/limits is the operational limit catalog every one of these packages
# validates against, and docs/limits.md is generated from it — a drift between
# the two is a release-note defect, so the gate checks it.
light=(
  ./internal/config
  ./internal/limits
  ./internal/scheduler
  ./internal/protocol
  ./internal/json
  ./internal/security
  ./internal/auth
  ./internal/replication
)

# Disk-bound: every package that fsyncs a durable file.
heavy=(
  ./internal/storage/...
  ./internal/wal
  ./internal/recovery
  ./internal/txn
  ./internal/backup
)

# I/O fault injection: the seam in internal/storage/io is process-global, so
# this package must run as its own test binary. It never runs in parallel with
# anything else that installs a fault.
fault=(
  ./tests/fault
)

# Retained release fixtures: real data directories written by shipped binaries,
# replayed by the current build. The package shells out to nextsqld and copies
# hundreds of megabytes per fixture, so it runs serially like the durable set,
# and it builds the current binaries itself in TestMain.
upgrade=(
  ./tests/upgrade
)

# Query engine and SQL front end. A storage-only gate cannot see a correctness
# regression here: the release profile ran neither the executor nor the SQL
# layers, so a query could return a wrong answer and still pass. internal/
# executor creates a durable database per test, so it belongs with the
# disk-bound set rather than the CPU-bound one.
engine=(
  ./internal/executor
  ./internal/sql/...
  ./internal/catalog
  ./internal/crypto
)

# End to end: crash injection and recovery, Raft failover, and the whole client
# path over TLS including every official driver. These spin real servers, so
# they run serially. Together they cost about 40 seconds and were the only
# evidence of the restart, failover and driver categories the engineering
# contract requires — none of which the profile ran.
e2e=(
  ./tests/crash
  ./tests/ha
  ./tests/integration
)

heavy_race=(
  ./internal/storage
  ./internal/storage/btree
  ./internal/wal
  ./internal/recovery
  ./internal/txn
  ./internal/backup
)

run_light() { go test -timeout "$TIMEOUT" "${light[@]}"; }
run_heavy() { go test -p "$IO_PARALLEL" -timeout "$TIMEOUT" "${heavy[@]}"; }
run_fault() { go test -p 1 -timeout "$TIMEOUT" -count=1 "${fault[@]}" ./internal/storage/io; }
run_upgrade() { go test -p 1 -timeout "$TIMEOUT" -count=1 "${upgrade[@]}"; }
run_engine() { go test -p "$IO_PARALLEL" -timeout "$TIMEOUT" "${engine[@]}"; }
run_e2e() { go test -p 1 -timeout "$TIMEOUT" -count=1 "${e2e[@]}"; }

case "$profile" in
  pr)
    run_light
    run_engine
    run_heavy
    run_fault
    run_upgrade
    run_e2e
    go test -p "$IO_PARALLEL" -timeout "$TIMEOUT" ./cmd/nextsqld ./cmd/nextsql
    ;;
  production)
    run_light
    run_engine
    run_heavy
    run_fault
    run_upgrade
    run_e2e
    go test -race -timeout "$TIMEOUT" ./internal/protocol ./internal/security
    go test -race -p "$IO_PARALLEL" -timeout "$TIMEOUT" ./internal/executor
    go test -race -p "$IO_PARALLEL" -timeout "$TIMEOUT" "${heavy_race[@]}"
    go test -race -p 1 -timeout "$TIMEOUT" -count=1 "${fault[@]}"
    go test -p "$IO_PARALLEL" -timeout "$TIMEOUT" ./cmd/nextsqld ./cmd/nextsql
    ;;
  chaos)
    go test -p "$IO_PARALLEL" -timeout "$TIMEOUT" ./internal/wal ./internal/recovery ./internal/storage ./internal/storage/btree ./internal/backup -run 'Crash|Corrupt|Recovery|Restore|Redo' -count=1
    go test -p 1 -timeout "$TIMEOUT" -count=1 ./tests/crash
    run_fault
    ;;
  fault)
    run_fault
    go test -race -p 1 -timeout "$TIMEOUT" -count=1 "${fault[@]}"
    ;;
  upgrade)
    run_upgrade
    ;;
  nightly)
    run_light
    run_engine
    run_heavy
    run_fault
    run_upgrade
    run_e2e
    go test -race -timeout "$TIMEOUT" ./internal/protocol ./internal/security
    go test -race -p "$IO_PARALLEL" -timeout "$TIMEOUT" ./internal/executor
    go test -race -p "$IO_PARALLEL" -timeout "$TIMEOUT" "${heavy_race[@]}"
    go test -race -p 1 -timeout "$TIMEOUT" -count=1 "${fault[@]}"
    fuzz_time=${NEXTSQL_FUZZ_TIME:-15s}
    go test ./internal/protocol -run '^$' -fuzz '^FuzzReadFrame$' -fuzztime="$fuzz_time"
    go test ./internal/protocol -run '^$' -fuzz '^FuzzDecodeQuery$' -fuzztime="$fuzz_time"
    go test ./internal/wal -run '^$' -fuzz '^FuzzDecodePhysical$' -fuzztime="$fuzz_time"
    go test ./internal/storage/page -run '^$' -fuzz '^FuzzParse$' -fuzztime="$fuzz_time"
    go test ./internal/backup -run '^$' -fuzz '^FuzzDecodeHeader$' -fuzztime="$fuzz_time"
    go test ./internal/backup -run '^$' -fuzz '^FuzzDecodeManifest$' -fuzztime="$fuzz_time"
    ;;
  *)
    echo "usage: $0 {pr|production|fault|upgrade|nightly|chaos}" >&2
    exit 2
    ;;
esac
