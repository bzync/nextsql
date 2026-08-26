#!/bin/sh
set -eu

data_dir=${NEXTSQL_DATA_DIR:-/var/lib/nextsql}
key_file=${NEXTSQL_KEY_FILE:-/run/secrets/root.key}
listen_addr=${NEXTSQL_LISTEN:-0.0.0.0:7210}
password_file=${NEXTSQL_PASSWORD_FILE:-/run/bootstrap/password}

mkdir -p "$data_dir"

# The key lives in the separate /run/secrets volume, never the database volume.
if [ ! -e "$data_dir/nextsql.db" ]; then
	user=${NEXTSQL_USER:-}
	if [ -z "$user" ] || [ ! -r "$password_file" ]; then
		echo "nextsql: first start requires NEXTSQL_USER and a readable NEXTSQL_PASSWORD_FILE" >&2
		exit 64
	fi
	/usr/local/bin/nextsql init --data-dir "$data_dir" --key-file "$key_file" \
		--user "$user" --password-file "$password_file"
fi

set -- /usr/local/bin/nextsqld --data-dir "$data_dir" --key-file "$key_file" \
	--listen "$listen_addr"
if [ -n "${NEXTSQL_AUTH_FILE:-}" ]; then
	set -- "$@" --auth-file "$NEXTSQL_AUTH_FILE"
fi
if [ -n "${NEXTSQL_TLS_CERT:-}" ] || [ -n "${NEXTSQL_TLS_KEY:-}" ]; then
	if [ -z "${NEXTSQL_TLS_CERT:-}" ] || [ -z "${NEXTSQL_TLS_KEY:-}" ]; then
		echo "nextsql: NEXTSQL_TLS_CERT and NEXTSQL_TLS_KEY must be set together" >&2
		exit 64
	fi
	set -- "$@" --tls-cert "$NEXTSQL_TLS_CERT" --tls-key "$NEXTSQL_TLS_KEY"
fi
if [ -n "${NEXTSQL_BUFFER_PAGES:-}" ]; then
	set -- "$@" --buffer-pages "$NEXTSQL_BUFFER_PAGES"
fi
exec "$@"
