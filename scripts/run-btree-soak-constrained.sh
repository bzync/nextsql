#!/bin/bash
set -euo pipefail

# A repeatable low-memory B+Tree invariant-soak profile. It is intentionally
# separate from the 100M release soak so memory-pressure evidence can be
# collected without silently changing the normal performance profile.
repo_dir=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)

export NEXTSQL_SOAK_PROFILE=constrained-memory-v1
export NEXTSQL_BTREE_OPS=${NEXTSQL_BTREE_OPS:-10000000}
export NEXTSQL_BTREE_POOL_PAGES=${NEXTSQL_BTREE_POOL_PAGES:-8192}
export NEXTSQL_BTREE_SPACE=${NEXTSQL_BTREE_SPACE:-250000}
export NEXTSQL_SOAK_GOMEMLIMIT=${NEXTSQL_SOAK_GOMEMLIMIT:-512MiB}
export NEXTSQL_SOAK_GOGC=${NEXTSQL_SOAK_GOGC:-25}

exec "$repo_dir/scripts/run-btree-soak.sh"
