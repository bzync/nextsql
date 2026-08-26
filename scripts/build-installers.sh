#!/usr/bin/env bash
# Build Linux and Windows NextSQL installers.
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
# shellcheck source=../packaging/lib.sh
. "$ROOT/packaging/lib.sh"
require_repo_root

DIST="$ROOT/installers"
LINUX_ARGS=()
WINDOWS_ARGS=()
SKIP_LINUX=0
SKIP_WINDOWS=0

usage() {
	cat <<EOF
Usage: $(basename "$0") [options]

  --out DIR          Output directory (default: installers/).
  --linux-arch LIST  Passed to build-linux-installer.sh --arch
  --windows-arch LIST
                     Passed to build-windows-installer.sh --arch
  --skip-linux       Skip Linux artifacts.
  --skip-windows     Skip Windows artifacts.
  -h, --help         Show this help.

Extra flags after -- are forwarded to both builders.
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	-h|--help) usage; exit 0 ;;
	--out) DIST="${2:?}"; shift ;;
	--out=*) DIST="${1#--out=}" ;;
	--linux-arch) LINUX_ARGS+=(--arch "${2:?}"); shift ;;
	--windows-arch) WINDOWS_ARGS+=(--arch "${2:?}"); shift ;;
	--skip-linux) SKIP_LINUX=1 ;;
	--skip-windows) SKIP_WINDOWS=1 ;;
	--) shift; LINUX_ARGS+=("$@"); WINDOWS_ARGS+=("$@"); break ;;
	*) die "unknown argument: $1" ;;
	esac
	shift
done

mkdir -p "$DIST"
LINUX_ARGS+=(--out "$DIST")
WINDOWS_ARGS+=(--out "$DIST")

if [ "$SKIP_LINUX" -eq 0 ]; then
	"$ROOT/scripts/build-linux-installer.sh" "${LINUX_ARGS[@]}"
fi
if [ "$SKIP_WINDOWS" -eq 0 ]; then
	"$ROOT/scripts/build-windows-installer.sh" "${WINDOWS_ARGS[@]}"
fi

info "combined checksums"
(
	cd "$DIST"
	: >SHA256SUMS
	for s in SHA256SUMS.linux SHA256SUMS.windows; do
		[ -f "$s" ] || continue
		cat "$s" >>SHA256SUMS
	done
)
info "SHA256SUMS -> $DIST/SHA256SUMS"
ls -lh "$DIST"
