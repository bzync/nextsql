#!/bin/sh
# NextSQL tarball installer. Run from the extracted archive.
set -eu

HERE="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"

usage() {
	cat <<'EOF'
Usage: install.sh [--user | --system] [--prefix DIR] [--no-service] [--no-gui]

  --system     Install under /usr/local (default when running as root)
  --user       Install under $HOME/.local; systemd --user unit
  --prefix DIR Override prefix (binaries in DIR/bin)
  --no-service Skip systemd unit installation
  --no-gui     Print manual setup instructions instead of launching the
               browser-based setup wizard (nextsql-admin). The wizard is
               only launched when this flag is absent, stdin is a terminal,
               and no configuration file exists here yet — a scripted or
               already-configured install always gets the manual output.
EOF
}

MODE=""
PREFIX=""
NO_SERVICE=0
NO_GUI=0

while [ $# -gt 0 ]; do
	case "$1" in
	-h|--help) usage; exit 0 ;;
	--user) MODE=user ;;
	--system) MODE=system ;;
	--prefix) PREFIX="${2:?}"; shift ;;
	--prefix=*) PREFIX="${1#--prefix=}" ;;
	--no-service) NO_SERVICE=1 ;;
	--no-gui) NO_GUI=1 ;;
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
# nextsql-admin (the GUI setup wizard) is optional here on purpose: a
# hand-assembled or pre-M4 tarball layout may not ship it, and installation
# must not fail just because the wizard binary is missing — see the
# LAUNCH_GUI gate below, which degrades to the manual instructions whenever
# it isn't present.
HAVE_GUI=0
if [ -x "$HERE/bin/nextsql-admin" ]; then
	install -m 0755 "$HERE/bin/nextsql-admin" "$BIN_DIR/nextsql-admin"
	HAVE_GUI=1
fi

CONFIG_EXISTED=0
[ -f "$CONF_DIR/nextsql.conf" ] && CONFIG_EXISTED=1

# LAUNCH_GUI: hand off to the browser-based setup wizard instead of printing
# manual next steps. Deliberately scoped to --user mode only for now: in
# --system mode the wizard (which inherits install.sh's root privileges)
# would create the data directory and key file as root, but the "nextsql"
# system service account is what actually needs to read/write them — a
# correct handoff needs either running the wizard's subprocess as that
# unprivileged account or restricting which path it's allowed to write to,
# neither of which this increment does. Getting that wrong would be a real
# permission/availability bug, not a convenience trade worth making here, so
# --system installs keep exactly today's tested manual-instructions path
# (with a one-line pointer to nextsql-admin as a same-user alternative).
# See docs/design-installer-gui.md M4 for the follow-up.
LAUNCH_GUI=0
if [ "$HAVE_GUI" -eq 1 ] && [ "$NO_GUI" -eq 0 ] && [ "$CONFIG_EXISTED" -eq 0 ] \
	&& [ "$MODE" = user ] && [ -t 0 ]; then
	LAUNCH_GUI=1
fi

if [ "$LAUNCH_GUI" -eq 0 ]; then
	if [ "$CONFIG_EXISTED" -eq 1 ]; then
		echo "Keeping existing $CONF_DIR/nextsql.conf"
	else
		sed \
			-e "s|^data_dir=.*|data_dir=$DATA_DIR|" \
			-e "s|^key_file=.*|key_file=$KEY_FILE|" \
			-e "s|^# wal_archive=.*|# wal_archive=$WAL_DIR|" \
			"$HERE/etc/nextsql.conf" >"$CONF_DIR/nextsql.conf"
		chmod 0640 "$CONF_DIR/nextsql.conf"
	fi
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

if [ "$LAUNCH_GUI" -eq 1 ]; then
	echo
	echo "Launching the NextSQL setup wizard in your browser..."
	echo "(nothing is written until you confirm the summary screen there;"
	echo " press Ctrl+C now, or click Finish when done, to return here)"
	echo
	# Not exec'd: install.sh regains control once the wizard process exits
	# (Finish button or Ctrl+C) so it can print a closing note below,
	# regardless of whether setup actually completed.
	"$BIN_DIR/nextsql-admin" || true
	echo
	echo "Setup wizard exited. Re-run it any time with:"
	echo "  $BIN_DIR/nextsql-admin"
	echo
	echo "Keep $KEY_FILE off the data volume in production."
	echo "Done."
	exit 0
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

if [ "$HAVE_GUI" -eq 1 ]; then
	echo
	if [ "$MODE" = system ]; then
		echo "Or, as a regular (non-root) user, run the browser-based setup wizard"
		echo "instead of the steps above: $BIN_DIR/nextsql-admin"
		echo "(not offered automatically here: it would create the database as"
		echo " root, not as the unprivileged 'nextsql' service account)"
	elif [ "$CONFIG_EXISTED" -eq 1 ]; then
		echo "The browser-based setup wizard was not offered because a config"
		echo "already exists at $CONF_DIR/nextsql.conf. Run it directly if you"
		echo "still want it: $BIN_DIR/nextsql-admin"
	else
		echo "Or run the browser-based setup wizard instead of the steps above:"
		echo "  $BIN_DIR/nextsql-admin"
	fi
fi

echo
echo "Keep $KEY_FILE off the data volume in production."
echo "Done."
