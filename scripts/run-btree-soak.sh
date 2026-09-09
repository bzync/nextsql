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
soak_progress=${NEXTSQL_BTREE_PROGRESS:-1}
soak_progress_every=${NEXTSQL_BTREE_PROGRESS_EVERY:-}
soak_profile=${NEXTSQL_SOAK_PROFILE:-custom}
soak_require_durable_fs=false
if [ -n "${NEXTSQL_REQUIRE_DURABLE_FS:-}" ]; then
	soak_require_durable_fs=true
fi
soak_started=$(date -u +%Y%m%dT%H%M%SZ)
# The artifact name states the run it actually holds. It used to be the literal
# "btree-100m-p16", which described neither the current default profile (100M
# operations over 24576 pool pages) nor the constrained one (10M over 8192) —
# so two different profiles produced names claiming the same shape, and the only
# way to tell a retained report apart was to open its manifest. Retained
# evidence must not misdescribe itself.
soak_name="btree-$soak_profile-ops$soak_ops-pool$soak_pool_pages"
soak_log=${NEXTSQL_SOAK_LOG:-"$repo_dir/.bench-results/$soak_name-$soak_started.log"}
soak_manifest=${NEXTSQL_SOAK_MANIFEST:-"${soak_log%.log}.manifest"}
soak_timev=${NEXTSQL_SOAK_TIMEV:-"${soak_log%.log}.timev"}
soak_metrics=${NEXTSQL_SOAK_METRICS:-"${soak_log%.log}.metrics"}

mkdir -p "$soak_tmpdir" "$(dirname -- "$soak_log")" "$(dirname -- "$soak_manifest")" "$(dirname -- "$soak_timev")" "$(dirname -- "$soak_metrics")"
soak_filesystem=$(stat -f -c %T "$soak_tmpdir" 2>/dev/null || printf unknown)

write_metrics() {
	{
		printf 'nextsql_btree_soak_metrics_version=1\n'
		printf 'profile=%s\n' "$soak_profile"
		printf 'started_utc=%s\n' "$soak_started"
		printf 'ended_utc=%s\n' "$(date -u +%Y%m%dT%H%M%SZ)"
		printf 'timev=%s\n' "$soak_timev"
		if [ -r "$soak_timev" ]; then
			# The value is everything after the final ": ". Splitting on ":"
			# and taking field 2 is wrong: GNU time's own label for elapsed
			# time contains colons ("Elapsed (wall clock) time (h:mm:ss or
			# m:ss):"), so field 2 was the literal "mm" and every retained
			# report recorded elapsed_wall=mm — the headline soak number was
			# not measured at all. No value here contains ": ".
			awk '
				function val(line,   v) { v = line; sub(/^.*: /, "", v); return v }
				/Elapsed \(wall clock\) time/ { print "elapsed_wall=" val($0) }
				/User time \(seconds\)/ { print "user_seconds=" val($0) }
				/System time \(seconds\)/ { print "system_seconds=" val($0) }
				/Maximum resident set size/ { print "max_rss_kib=" val($0) }
				/File system inputs/ { print "filesystem_inputs=" val($0) }
				/File system outputs/ { print "filesystem_outputs=" val($0) }
				/Major \(requiring I\/O\) page faults/ { print "major_page_faults=" val($0) }
				/Voluntary context switches/ { print "voluntary_context_switches=" val($0) }
				/Involuntary context switches/ { print "involuntary_context_switches=" val($0) }
			' "$soak_timev"
		else
			printf 'metrics_status=unavailable (/usr/bin/time -v was not started)\n'
		fi
	} >"$soak_metrics"
}

write_manifest() {
	{
		printf 'nextsql_btree_soak_manifest_version=1\n'
		printf 'profile=%s\n' "$soak_profile"
		printf 'started_utc=%s\n' "$soak_started"
		printf 'ended_utc=%s\n' "$(date -u +%Y%m%dT%H%M%SZ)"
		printf 'exit=%s\n' "$1"
		printf 'repository=%s\n' "$repo_dir"
		printf 'git_revision=%s\n' "$(git -C "$repo_dir" rev-parse HEAD 2>/dev/null || printf unavailable)"
		printf 'git_dirty=%s\n' "$(if git -C "$repo_dir" diff --quiet --ignore-submodules -- 2>/dev/null && git -C "$repo_dir" diff --cached --quiet --ignore-submodules -- 2>/dev/null; then printf false; else printf true; fi)"
		printf 'ops=%s\n' "$soak_ops"
		printf 'pool_pages=%s\n' "$soak_pool_pages"
		printf 'space=%s\n' "${soak_space:-auto}"
		printf 'gomemlimit=%s\n' "$soak_gomemlimit"
		printf 'gogc=%s\n' "$soak_gogc"
		printf 'godebug=%s\n' "$soak_godebug"
		printf 'progress=%s\n' "$soak_progress"
		printf 'progress_every=%s\n' "${soak_progress_every:-auto}"
		printf 'tmpdir=%s\n' "$soak_tmpdir"
		printf 'filesystem_type=%s\n' "$soak_filesystem"
		printf 'require_durable_fs=%s\n' "$soak_require_durable_fs"
		printf 'log=%s\n' "$soak_log"
		printf 'metrics=%s\n' "$soak_metrics"
		printf 'timev=%s\n' "$soak_timev"
		printf 'hostname=%s\n' "$(hostname 2>/dev/null || printf unavailable)"
		printf 'kernel=%s\n' "$(uname -srmo 2>/dev/null || printf unavailable)"
		printf 'cpu=%s\n' "$(getconf _NPROCESSORS_ONLN 2>/dev/null || printf unavailable)"
		printf 'memory_bytes=%s\n' "$(awk '/MemTotal:/ { print $2 * 1024; found=1 } END { if (!found) print "unavailable" }' /proc/meminfo 2>/dev/null)"
		printf 'filesystem=%s\n' "$(df -PT "$soak_tmpdir" 2>/dev/null | awk 'NR == 2 { print "type=" $2 " device=" $1 " available_kib=" $5 }')"
		printf 'go_version=%s\n' "$(go version 2>/dev/null || printf unavailable)"
	} >"$soak_manifest"
}

finish() {
	soak_status=$?
	trap - EXIT
	set +e
	printf 'nextsql-btree-soak end_utc=%s exit=%d\n' "$(date -u +%Y%m%dT%H%M%SZ)" "$soak_status" | tee -a "$soak_log"
	write_metrics
	write_manifest "$soak_status"
	printf 'nextsql-btree-soak manifest=%s\n' "$soak_manifest" | tee -a "$soak_log"
	exit "$soak_status"
}
trap finish EXIT

case "$soak_filesystem" in
	tmpfs|ramfs)
		printf 'WARNING: soak scratch %s is %s; its fsyncs are not durability evidence\n' "$soak_tmpdir" "$soak_filesystem" | tee -a "$soak_log" >&2
		if [ -n "${NEXTSQL_REQUIRE_DURABLE_FS:-}" ]; then
			printf 'NEXTSQL_REQUIRE_DURABLE_FS is set: refusing RAM-backed soak evidence\n' | tee -a "$soak_log" >&2
			exit 2
		fi
		;;
esac

{
	printf 'nextsql-btree-soak start_utc=%s\n' "$soak_started"
	printf 'profile=%s manifest=%s\n' "$soak_profile" "$soak_manifest"
	printf 'scratch_filesystem=%s require_durable_fs=%s\n' "$soak_filesystem" "$soak_require_durable_fs"
	printf 'repo=%s\n' "$repo_dir"
	printf 'ops=%s pool_pages=%s space=%s tmpdir=%s log=%s\n' "$soak_ops" "$soak_pool_pages" "${soak_space:-auto}" "$soak_tmpdir" "$soak_log"
	printf 'GOMEMLIMIT=%s GOGC=%s GODEBUG=%s progress=%s progress_every=%s\n' "$soak_gomemlimit" "$soak_gogc" "$soak_godebug" "$soak_progress" "${soak_progress_every:-auto}"
	go version
} | tee -a "$soak_log"

set +e
if [ -x /usr/bin/time ]; then
	/usr/bin/time -v -o "$soak_timev" env \
		TMPDIR="$soak_tmpdir" \
		NEXTSQL_BTREE_OPS="$soak_ops" \
		NEXTSQL_BTREE_POOL_PAGES="$soak_pool_pages" \
		NEXTSQL_BTREE_SPACE="$soak_space" \
		NEXTSQL_BTREE_PROGRESS="$soak_progress" \
		NEXTSQL_BTREE_PROGRESS_EVERY="$soak_progress_every" \
		GOMEMLIMIT="$soak_gomemlimit" \
		GOGC="$soak_gogc" \
		GODEBUG="$soak_godebug" \
		go test ./internal/storage/btree \
			-run '^TestRandomizedLargeInvariants$' \
			-count=1 \
			-timeout=0 \
			-v 2>&1 | tee -a "$soak_log"
	soak_pipeline_status=("${PIPESTATUS[@]}")
else
	env TMPDIR="$soak_tmpdir" \
		NEXTSQL_BTREE_OPS="$soak_ops" \
		NEXTSQL_BTREE_POOL_PAGES="$soak_pool_pages" \
		NEXTSQL_BTREE_SPACE="$soak_space" \
		NEXTSQL_BTREE_PROGRESS="$soak_progress" \
		NEXTSQL_BTREE_PROGRESS_EVERY="$soak_progress_every" \
		GOMEMLIMIT="$soak_gomemlimit" \
		GOGC="$soak_gogc" \
		GODEBUG="$soak_godebug" \
		go test ./internal/storage/btree \
			-run '^TestRandomizedLargeInvariants$' \
			-count=1 \
			-timeout=0 \
			-v 2>&1 | tee -a "$soak_log"
	soak_pipeline_status=("${PIPESTATUS[@]}")
fi
soak_status=${soak_pipeline_status[0]}
if (( soak_status == 0 && soak_pipeline_status[1] != 0 )); then
	soak_status=${soak_pipeline_status[1]}
fi
exit "$soak_status"
