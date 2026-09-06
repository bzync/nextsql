# Shared helpers for NextSQL installer build scripts.
# Sourced by scripts/build-*-installer.sh. Not executable on its own.

set -euo pipefail
shopt -s inherit_errexit

packaging_root() {
	# packaging/ when sourced as packaging/lib.sh
	cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd
}

log()  { printf '%s\n' "$*" >&2; }
info() { printf '==> %s\n' "$*" >&2; }
die()  { printf 'error: %s\n' "$*" >&2; exit 1; }

need_cmd() {
	command -v "$1" >/dev/null 2>&1 || die "missing required command: $1"
}

# Product version as committed (e.g. 0.0.1).
version_string() {
	local f="$ROOT/internal/version/version.go"
	local v
	v="$(sed -n 's/^[[:space:]]*String = "\(.*\)".*/\1/p' "$f" | head -n 1)"
	[ -n "$v" ] || die "could not read version from $f"
	printf '%s\n' "$v"
}

# Debian upstream version: 0.0.1 -> 0.1.0~dev so it sorts before 0.1.0.
# Do not write '~' inline in ${var/a/b}: bash expands a bare tilde to $HOME.
deb_version() {
	awk -v v="$1" 'BEGIN { sub("-", "~", v); print v "-1" }'
}

# Windows FILEVERSION / ProductVersion: 0.0.1 -> 0.1.0.0
win_version() {
	local v="$1"
	v="${v%%-*}"
	IFS=. read -r a b c _ <<<"${v}.0.0"
	printf '%s.%s.%s.0\n' "${a:-0}" "${b:-0}" "${c:-0}"
}

# Safe file token: 0.0.1 stays 0.0.1.
file_version() {
	printf '%s\n' "$1"
}

goarch_to_deb() {
	case "$1" in
	amd64) echo amd64 ;;
	arm64) echo arm64 ;;
	386) echo i386 ;;
	arm) echo armhf ;;
	*) echo "$1" ;;
	esac
}

goarch_to_rpm() {
	case "$1" in
	amd64) echo x86_64 ;;
	arm64) echo aarch64 ;;
	386) echo i686 ;;
	arm) echo armv7hl ;;
	*) echo "$1" ;;
	esac
}

goarch_to_win() {
	case "$1" in
	amd64) echo x64 ;;
	386) echo x86 ;;
	arm64) echo arm64 ;;
	*) echo "$1" ;;
	esac
}

default_go_arches() {
	local os="$1"
	local host
	host="$(go env GOARCH)"
	case "$os" in
	linux) echo "${host:-amd64}" ;;
	windows) echo "${host:-amd64}" ;;
	*) echo amd64 ;;
	esac
}

parse_arches() {
	local spec="$1"
	if [ "$spec" = "all" ]; then
		echo "amd64 arm64"
		return
	fi
	# comma or space separated
	echo "$spec" | tr ',;' ' '
}

build_go() {
	local os="$1" arch="$2" out="$3" pkg="$4"
	info "go build $pkg ($os/$arch) -> $out"
	mkdir -p "$(dirname "$out")"
	CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
		go build -trimpath -ldflags="-s -w" -o "$out" "$pkg"
}

sign_checksums() {
	# Detached-sign a checksums file with GPG. `key_id` comes from the
	# calling script's `--gpg-key` flag (or the NEXTSQL_RELEASE_GPG_KEY env
	# var it falls back to) — signing is opt-in, since no release key exists
	# in this repo or CI yet. Unconfigured (no key_id) is a silent no-op, the
	# same tolerance already used for optional tools like rpmbuild/makensis.
	# Once a key IS requested, failure to sign must not be silent: producing
	# release artifacts while quietly skipping an explicitly requested
	# signature would be exactly the "fake success" this repo's engineering
	# contract forbids, so both a missing `gpg` and a signing failure `die`.
	local sums_file="$1" key_id="$2"
	[ -n "$key_id" ] || return 0
	command -v gpg >/dev/null 2>&1 || die "gpg not found but --gpg-key was given"
	rm -f "${sums_file}.asc"
	gpg --batch --yes --local-user "$key_id" --detach-sign --armor \
		--output "${sums_file}.asc" "$sums_file" \
		|| die "gpg failed to sign $sums_file with key $key_id"
	info "signed $sums_file -> ${sums_file}.asc (key $key_id)"
}

write_sha256() {
	local dir="$1"
	(
		cd "$dir"
		# shellcheck disable=SC2035
		sha256sum -- * >SHA256SUMS 2>/dev/null || sha256sum * >SHA256SUMS
	)
}

require_repo_root() {
	[ -f "$ROOT/go.mod" ] || die "not a NextSQL checkout (missing go.mod)"
	grep -q 'module github.com/bzync/nextsql' "$ROOT/go.mod" \
		|| die "go.mod is not github.com/bzync/nextsql"
}

usage_common() {
	cat <<EOF
Environment:
  GOOS / GOARCH are set by the script; do not export CGO_ENABLED=1.

Outputs land in DIST (default: \$ROOT/installers).
EOF
}
