#!/usr/bin/env bash
# Build NextSQL Windows installers: zip archive, self-extracting setup.exe,
# and an NSIS installer if makensis is on PATH.
#
# Usage:
#   scripts/build-windows-installer.sh
#   scripts/build-windows-installer.sh --arch amd64,arm64 --out dist
set -euo pipefail

ROOT="$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)"
# shellcheck source=../packaging/lib.sh
. "$ROOT/packaging/lib.sh"
require_repo_root
need_cmd go
need_cmd zip
need_cmd sha256sum
need_cmd python3

ARCHES=""
DIST="$ROOT/dist"
SKIP_ZIP=0
SKIP_SETUP=0
SKIP_NSIS=0
NAME="nextsql"

usage() {
	cat <<EOF
Usage: $(basename "$0") [options]

  --arch LIST     Go arches (comma-separated, or 'all' for amd64,arm64).
                  Default: host GOARCH ($(go env GOARCH)).
  --out DIR       Output directory (default: dist/).
  --skip-zip      Do not write the portable zip.
  --skip-setup    Do not write the self-extracting setup.exe.
  --skip-nsis     Do not run makensis even if it is installed.
  -h, --help      Show this help.

Cross-compiles with CGO_ENABLED=0. NSIS is optional; the Go setup.exe is
the primary Windows installer and does not need makensis.

$(usage_common)
EOF
}

while [ $# -gt 0 ]; do
	case "$1" in
	-h|--help) usage; exit 0 ;;
	--arch) ARCHES="${2:?}"; shift ;;
	--arch=*) ARCHES="${1#--arch=}" ;;
	--out) DIST="${2:?}"; shift ;;
	--out=*) DIST="${1#--out=}" ;;
	--skip-zip) SKIP_ZIP=1 ;;
	--skip-setup) SKIP_SETUP=1 ;;
	--skip-nsis) SKIP_NSIS=1 ;;
	*) die "unknown argument: $1" ;;
	esac
	shift
done

if [ -z "$ARCHES" ]; then
	ARCHES="$(default_go_arches windows)"
fi
ARCHES="$(parse_arches "$ARCHES")"

VERSION="$(version_string)"
FVER="$(file_version "$VERSION")"
WIN_VER="$(win_version "$VERSION")"
STAGE="$DIST/.windows-stage"
PKG="$ROOT/packaging"
mkdir -p "$DIST"
rm -rf "$STAGE"
mkdir -p "$STAGE"

info "NextSQL $VERSION  windows installers  arches: $ARCHES"

ICO="$STAGE/nextsql.ico"
ICON_SRC=""
for cand in \
	"$ROOT/docs/web/public/icons/icon-512.png" \
	"$ROOT/docs/web/public/icons/icon-192.png" \
	"$ROOT/docs/web/public/icons/icon.png"
do
	if [ -f "$cand" ]; then
		ICON_SRC="$cand"
		break
	fi
done
if [ -n "$ICON_SRC" ]; then
	if python3 "$PKG/windows/make-ico.py" --src "$ICON_SRC" --out "$ICO"; then
		info "icon $ICO"
	else
		log "warning: could not build .ico (Python PIL missing?); continuing without it"
		ICO=""
	fi
else
	ICO=""
fi

stage_windows() {
	local arch="$1" dest="$2"
	rm -rf "$dest"
	mkdir -p "$dest"
	build_go windows "$arch" "$dest/nextsql.exe"       "$ROOT/cmd/nextsql"
	build_go windows "$arch" "$dest/nextsqld.exe"      "$ROOT/cmd/nextsqld"
	build_go windows "$arch" "$dest/nextsql-bench.exe" "$ROOT/cmd/nextsql-bench"
	cp "$PKG/windows/nextsql.conf" "$dest/nextsql.conf"
	cp "$PKG/windows/README.txt" "$dest/README.txt"
	cp "$PKG/windows/install.ps1" "$dest/install.ps1"
	cp "$PKG/windows/uninstall.ps1" "$dest/uninstall.ps1"
	cp "$PKG/COPYRIGHT" "$dest/COPYRIGHT"
	printf '%s\n' "$VERSION" >"$dest/VERSION"
	if [ -n "$ICO" ] && [ -f "$ICO" ]; then
		cp "$ICO" "$dest/nextsql.ico"
	fi
}

append_sfx() {
	local stub="$1" zipfile="$2" out="$3"
	python3 - "$stub" "$zipfile" "$out" <<'PY'
import pathlib, struct, sys
stub, zipfile, out = map(pathlib.Path, sys.argv[1:4])
payload = zipfile.read_bytes()
data = stub.read_bytes() + payload + struct.pack("<Q", len(payload)) + b"NEXTSFX1"
out.write_bytes(data)
out.chmod(0o755)
PY
}

build_zip() {
	local arch="$1" dest="$2"
	local out="$DIST/nextsql-${FVER}-windows-${arch}.zip"
	info "zip $out"
	(
		cd "$dest"
		rm -f "$out"
		zip -q -9 "$out" ./*
	)
	printf '%s\n' "$out"
}

build_setup() {
	local arch="$1" dest="$2"
	local stub="$STAGE/setup-${arch}.exe"
	info "setup stub ($arch)"
	CGO_ENABLED=0 GOOS=windows GOARCH="$arch" \
		go build -trimpath -ldflags="-s -w -H windowsgui -X main.version=${VERSION}" \
		-o "$stub" "$ROOT/packaging/windows/setup"
	local payload="$STAGE/payload-${arch}.zip"
	(
		cd "$dest"
		rm -f "$payload"
		zip -q -9 "$payload" ./*
	)
	local out="$DIST/nextsql-${FVER}-windows-${arch}-setup.exe"
	info "setup $out"
	append_sfx "$stub" "$payload" "$out"
	printf '%s\n' "$out"
}

build_nsis() {
	local arch="$1" dest="$2"
	command -v makensis >/dev/null 2>&1 || return 0
	local out="$DIST/nextsql-${FVER}-windows-${arch}-nsis.exe"
	info "nsis $out"
	local ico_arg=()
	if [ -n "$ICO" ] && [ -f "$ICO" ]; then
		ico_arg=(-DICO="$ICO")
	fi
	if ! makensis -V2 \
		-DPRODUCT_VERSION="$VERSION" \
		-DPRODUCT_VERSION_CSV="$WIN_VER" \
		-DARCH="$arch" \
		-DSTAGING="$dest" \
		-DOUTFILE="$out" \
		"${ico_arg[@]}" \
		"$PKG/windows/nextsql.nsi"
	then
		log "warning: makensis failed for $arch (skipping)"
		return 0
	fi
	printf '%s\n' "$out"
}

ARTIFACTS=()
for arch in $ARCHES; do
	case "$arch" in
	amd64|arm64|386) ;;
	*) die "unsupported windows arch: $arch" ;;
	esac
	dest="$STAGE/pkg-${arch}"
	stage_windows "$arch" "$dest"
	if [ "$SKIP_ZIP" -eq 0 ]; then
		ARTIFACTS+=("$(build_zip "$arch" "$dest")")
	fi
	if [ "$SKIP_SETUP" -eq 0 ]; then
		ARTIFACTS+=("$(build_setup "$arch" "$dest")")
	fi
	if [ "$SKIP_NSIS" -eq 0 ]; then
		nsis_out="$(build_nsis "$arch" "$dest" || true)"
		if [ -n "${nsis_out}" ] && [ -f "${nsis_out}" ]; then
			ARTIFACTS+=("$nsis_out")
		fi
	fi
done

info "checksums"
(
	cd "$DIST"
	shopt -s nullglob
	: >SHA256SUMS.windows
	for f in nextsql-*-windows-*.zip nextsql-*-windows-*-setup.exe nextsql-*-windows-*-nsis.exe; do
		sha256sum "$f" >>SHA256SUMS.windows
	done
)

info "Windows installers"
printf '    %s\n' "${ARTIFACTS[@]}"
info "SHA256SUMS.windows -> $DIST/SHA256SUMS.windows"
rm -rf "$STAGE"
