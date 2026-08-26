#!/bin/sh
# NextSQL tarball uninstaller. Does not delete data directories or key files.
set -eu

HERE="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"

MODE=""
PREFIX=""

while [ $# -gt 0 ]; do
	case "$1" in
	-h|--help)
		echo "Usage: uninstall.sh [--user | --system] [--prefix DIR]"
		exit 0
		;;
	--user) MODE=user ;;
	--system) MODE=system ;;
	--prefix) PREFIX="${2:?}"; shift ;;
	--prefix=*) PREFIX="${1#--prefix=}" ;;
	*) echo "unknown argument: $1" >&2; exit 2 ;;
	esac
	shift
done

if [ -z "$MODE" ]; then
	if [ "$(id -u)" -eq 0 ]; then
		MODE=system
	else
		MODE=user
	fi
fi

if [ "$MODE" = system ]; then
	PREFIX="${PREFIX:-/usr/local}"
	UNIT=/etc/systemd/system/nextsql.service
	SYSTEMCTL_USER=""
else
	PREFIX="${PREFIX:-$HOME/.local}"
	UNIT="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user/nextsql.service"
	SYSTEMCTL_USER="--user"
fi

if command -v systemctl >/dev/null 2>&1; then
	systemctl $SYSTEMCTL_USER disable --now nextsql >/dev/null 2>&1 || true
fi
rm -f "$UNIT"
if command -v systemctl >/dev/null 2>&1; then
	systemctl $SYSTEMCTL_USER daemon-reload >/dev/null 2>&1 || true
fi

rm -f "$PREFIX/bin/nextsql" "$PREFIX/bin/nextsqld" "$PREFIX/bin/nextsql-bench"
echo "Removed NextSQL binaries and the systemd unit."
echo "Data directories and /etc/nextsql (or ~/.config/nextsql) were left in place."
