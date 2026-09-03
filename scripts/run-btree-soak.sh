#!/bin/bash
set -euo pipefail

repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
soak_tmpdir=${NEXTSQL_SOAK_TMPDIR:-"$repo_dir/.bench-tmp"}
soak_ops=${NEXTSQL_BTREE_OPS:-100000000}
# Pool pages: 24576 (384 MiB) keeps most of the resident tree cached without
# crowding a RAM-constrained host. Raise it (and GOMEMLIMIT) on a bigger box, or
# set NEXTSQL_BTREE_SPACE to cap the key space so the tree fits the pool.
soak_pool_pages=${NEXTSQL_BTREE_POOL_PAGES:-24576}
soak_space=${NEXTSQL_BTREE_SPACE:-}
soak_gomemlimit=${NEXTSQL_SOAK_GOMEMLIMIT:-6GiB}
# GOGC 40 (was 25): fewer GC cycles per unit work once the pool is large enough
# to hold the working set, without a large heap ceiling.
soak_gogc=${NEXTSQL_SOAK_GOGC:-40}
soak_godebug=${NEXTSQL_SOAK_GODEBUG:-madvdontneed=1}
soak_started=$(date -u +%Y%m%dT%H%M%SZ)
soak_log=${NEXTSQL_SOAK_LOG:-"$repo_dir/.bench-results/btree-100m-p16-$soak_started.log"}

mkdir -p "$soak_tmpdir" "$(dirname -- "$soak_log")"

finish() {
	soak_status=$?
	trap - EXIT
	set +e
	printf 'nextsql-btree-soak end_utc=%s exit=%d\n' "$(date -u +%Y%m%dT%H%M%SZ)" "$soak_status" | tee -a "$soak_log"
	exit "$soak_status"
}
trap finish EXIT

{
	printf 'nextsql-btree-soak start_utc=%s\n' "$soak_started"
	printf 'repo=%s\n' "$repo_dir"
	printf 'ops=%s pool_pages=%s space=%s tmpdir=%s log=%s\n' "$soak_ops" "$soak_pool_pages" "${soak_space:-auto}" "$soak_tmpdir" "$soak_log"
	printf 'GOMEMLIMIT=%s GOGC=%s GODEBUG=%s\n' "$soak_gomemlimit" "$soak_gogc" "$soak_godebug"
	go version
} | tee -a "$soak_log"

set +e
TMPDIR="$soak_tmpdir" \
NEXTSQL_BTREE_OPS="$soak_ops" \
NEXTSQL_BTREE_POOL_PAGES="$soak_pool_pages" \
NEXTSQL_BTREE_SPACE="$soak_space" \
GOMEMLIMIT="$soak_gomemlimit" \
GOGC="$soak_gogc" \
GODEBUG="$soak_godebug" \
	go test ./internal/storage/btree \
		-run '^TestRandomizedLargeInvariants$' \
		-count=1 \
		-timeout=0 \
		-v 2>&1 | tee -a "$soak_log"
soak_pipeline_status=("${PIPESTATUS[@]}")
soak_status=${soak_pipeline_status[0]}
if (( soak_status == 0 && soak_pipeline_status[1] != 0 )); then
	soak_status=${soak_pipeline_status[1]}
fi
exit "$soak_status"
