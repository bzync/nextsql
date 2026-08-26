#!/bin/sh
# NextSQL tarball installer. Run from the extracted archive.
set -eu

HERE="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"

usage() {
	cat <<'EOF'
Usage: install.sh [--user | --system] [--prefix DIR] [--no-service]

  --system     Install under /usr/local (default when running as root)
  --user       Install under $HOME/.local; systemd --user unit
  --prefix DIR Override prefix (binaries in DIR/bin)
  --no-service Skip systemd unit installation
EOF
}

MODE=""
PREFIX=""
NO_SERVICE=0

while [ $# -gt 0 ]; do
	case "$1" in
	-h|--help) usage; exit 0 ;;
	--user) MODE=user ;;
	--system) MODE=system ;;
	--prefix) PREFIX="${2:?}"; shift ;;
	--prefix=*) PREFIX="${1#--prefix=}" ;;
	--no-service) NO_SERVICE=1 ;;
	*) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
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
	[ "$(id -u)" -eq 0 ] || { echo "system-wide install requires root (or pass --user)" >&2; exit 1; }
	PREFIX="${PREFIX:-/usr/local}"
	CONF_DIR=/etc/nextsql
	DATA_DIR=/var/lib/nextsql
	WAL_DIR=/var/lib/nextsql-wal
	KEY_FILE=/etc/nextsql/root.key
	UNIT_DIR=/etc/systemd/system
	RUN_USER=nextsql
	SYSTEMCTL_USER=""
else
	PREFIX="${PREFIX:-$HOME/.local}"
	CONF_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/nextsql"
	DATA_DIR="${XDG_DATA_HOME:-$HOME/.local/share}/nextsql"
	WAL_DIR="${DATA_DIR}-wal"
	KEY_FILE="$CONF_DIR/root.key"
	UNIT_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/systemd/user"
	RUN_USER="$(id -un)"
	SYSTEMCTL_USER="--user"
fi

BIN_DIR="$PREFIX/bin"

for b in nextsql nextsqld nextsql-bench; do
	[ -x "$HERE/bin/$b" ] || { echo "missing $HERE/bin/$b" >&2; exit 1; }
done

echo "Installing NextSQL ($MODE)"
echo "  binaries : $BIN_DIR"
echo "  config   : $CONF_DIR/nextsql.conf"
echo "  data     : $DATA_DIR"
echo "  key file : $KEY_FILE"

mkdir -p "$BIN_DIR" "$CONF_DIR" "$DATA_DIR" "$WAL_DIR"
install -m 0755 "$HERE/bin/nextsql" "$BIN_DIR/nextsql"
install -m 0755 "$HERE/bin/nextsqld" "$BIN_DIR/nextsqld"
install -m 0755 "$HERE/bin/nextsql-bench" "$BIN_DIR/nextsql-bench"

if [ ! -f "$CONF_DIR/nextsql.conf" ]; then
	sed \
		-e "s|^data_dir=.*|data_dir=$DATA_DIR|" \
		-e "s|^key_file=.*|key_file=$KEY_FILE|" \
		-e "s|^# wal_archive=.*|# wal_archive=$WAL_DIR|" \
		"$HERE/etc/nextsql.conf" >"$CONF_DIR/nextsql.conf"
	chmod 0640 "$CONF_DIR/nextsql.conf"
else
	echo "Keeping existing $CONF_DIR/nextsql.conf"
fi

if [ "$MODE" = system ]; then
	if ! getent group nextsql >/dev/null 2>&1; then
		groupadd --system nextsql
	fi
	if ! getent passwd nextsql >/dev/null 2>&1; then
		useradd --system --gid nextsql --home-dir "$DATA_DIR" \
			--shell /usr/sbin/nologin --comment "NextSQL database" nextsql
	fi
	chown -R nextsql:nextsql "$DATA_DIR" "$WAL_DIR"
	chmod 0750 "$DATA_DIR" "$WAL_DIR" "$CONF_DIR"
	chown root:nextsql "$CONF_DIR/nextsql.conf"
	chmod 0640 "$CONF_DIR/nextsql.conf"
fi

if [ "$NO_SERVICE" -eq 0 ] && command -v systemctl >/dev/null 2>&1; then
	mkdir -p "$UNIT_DIR"
	if [ "$MODE" = user ]; then
		src="$HERE/systemd/nextsql.user.service"
		[ -f "$src" ] || src="$HERE/systemd/nextsql.service"
		sed \
			-e "s|%h/.local/bin/nextsqld|$BIN_DIR/nextsqld|g" \
			-e "s|%h/.config/nextsql/nextsql.conf|$CONF_DIR/nextsql.conf|g" \
			-e "s|%h/.local/share/nextsql/nextsql.db|$DATA_DIR/nextsql.db|g" \
			-e "s|%h/.local/share/nextsql|$DATA_DIR|g" \
			-e "s|%h/.config/nextsql|$CONF_DIR|g" \
			"$src" >"$UNIT_DIR/nextsql.service"
	else
		sed \
			-e "s|/usr/bin/nextsqld|$BIN_DIR/nextsqld|g" \
			-e "s|/etc/nextsql/nextsql.conf|$CONF_DIR/nextsql.conf|g" \
			-e "s|/var/lib/nextsql-wal|$WAL_DIR|g" \
			-e "s|/var/lib/nextsql|$DATA_DIR|g" \
			"$HERE/systemd/nextsql.service" >"$UNIT_DIR/nextsql.service"
	fi
	chmod 0644 "$UNIT_DIR/nextsql.service"
	systemctl $SYSTEMCTL_USER daemon-reload >/dev/null 2>&1 || true
	echo "Installed systemd unit nextsql.service (not enabled; init first)."
fi

cat <<EOF

NextSQL binaries are on disk. The server is not started until you initialize:

  printf 'secret\\n' > /tmp/nextsql.pw && chmod 600 /tmp/nextsql.pw
  $BIN_DIR/nextsql init \\
    --data-dir $DATA_DIR \\
    --key-file $KEY_FILE \\
    --user app --password-file /tmp/nextsql.pw
EOF

if [ "$MODE" = system ]; then
	cat <<EOF
  chown -R nextsql:nextsql $DATA_DIR
  chown nextsql:nextsql $KEY_FILE
  chmod 600 $KEY_FILE
  systemctl enable --now nextsql
EOF
else
	cat <<EOF
  chmod 600 $KEY_FILE
  systemctl --user enable --now nextsql
  # linger so it survives logout: sudo loginctl enable-linger $RUN_USER
EOF
	case ":$PATH:" in
	*":$BIN_DIR:"*) ;;
	*) echo "Put $BIN_DIR on your PATH." ;;
	esac
fi

echo
echo "Keep $KEY_FILE off the data volume in production."
echo "Done."
