#!/usr/bin/env bash
# Build NextSQL installers. NextSQL ships Linux packages only; on Windows it
# runs inside WSL 2 from those same Linux packages.
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
# shellcheck source=../packaging/lib.sh
. "$ROOT/packaging/lib.sh"
require_repo_root

DIST="$ROOT/installers"
LINUX_ARGS=()

usage() {
	cat <<EOF
Usage: $(basename "$0") [options]

  --out DIR          Output directory (default: installers/).
  --linux-arch LIST  Passed to build-linux-installer.sh --arch
  -h, --help         Show this help.

Extra flags after -- are forwarded to build-linux-installer.sh.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	-h|--help) usage; exit 0 ;;
	--out) DIST="${2:?}"; shift ;;
	--out=*) DIST="${1#--out=}" ;;
	--linux-arch) LINUX_ARGS+=(--arch "${2:?}"); shift ;;
	--) shift; LINUX_ARGS+=("$@"); break ;;
	*) die "unknown argument: $1" ;;
	esac
	shift
done

mkdir -p "$DIST"
LINUX_ARGS+=(--out "$DIST")

"$ROOT/scripts/build-linux-installer.sh" "${LINUX_ARGS[@]}"

info "combined checksums"
cp "$DIST/SHA256SUMS.linux" "$DIST/SHA256SUMS"
info "SHA256SUMS -> $DIST/SHA256SUMS"
ls -lh "$DIST"
