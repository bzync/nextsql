#!/bin/sh
set -eu

data_dir=${NEXTSQL_DATA_DIR:-/var/lib/nextsql}
key_file=${NEXTSQL_KEY_FILE:-/run/secrets/root.key}
listen_addr=${NEXTSQL_LISTEN:-0.0.0.0:7210}
password_file=${NEXTSQL_SERVER_PASSWORD_FILE:-/run/bootstrap/password}

mkdir -p "$data_dir"

# The key lives in the separate /run/secrets volume, never the database volume.
if [ ! -e "$data_dir/nextsql.db" ]; then
	user=${NEXTSQL_SERVER_USER:-}
	if [ -z "$user" ]; then
		echo "nextsql: first start requires NEXTSQL_SERVER_USER" >&2
		exit 64
	fi
	if [ -r "$password_file" ]; then
		/usr/local/bin/nextsql init --data-dir "$data_dir" --key-file "$key_file" \
			--user "$user" --password-file "$password_file"
	elif [ -n "${NEXTSQL_SERVER_PASS:-}" ]; then
		/usr/local/bin/nextsql init --data-dir "$data_dir" --key-file "$key_file" \
			--user "$user"
	else
		echo "nextsql: first start requires NEXTSQL_SERVER_PASSWORD_FILE or NEXTSQL_SERVER_PASS" >&2
		exit 64
	fi
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
if [ -n "${NEXTSQL_TLS_CLIENT_CA:-}" ]; then
	if [ -z "${NEXTSQL_TLS_CERT:-}" ] || [ -z "${NEXTSQL_TLS_KEY:-}" ]; then
		echo "nextsql: NEXTSQL_TLS_CLIENT_CA requires NEXTSQL_TLS_CERT and NEXTSQL_TLS_KEY" >&2
		exit 2
	fi
	set -- "$@" --tls-client-ca "$NEXTSQL_TLS_CLIENT_CA"
fi
if [ -n "${NEXTSQL_TLS_CLIENT_CRL:-}" ]; then
	if [ -z "${NEXTSQL_TLS_CLIENT_CA:-}" ]; then
		echo "nextsql: NEXTSQL_TLS_CLIENT_CRL requires NEXTSQL_TLS_CLIENT_CA" >&2
		exit 2
	fi
	set -- "$@" --tls-client-crl "$NEXTSQL_TLS_CLIENT_CRL"
fi
if [ -n "${NEXTSQL_BUFFER_PAGES:-}" ]; then
	set -- "$@" --buffer-pages "$NEXTSQL_BUFFER_PAGES"
fi
exec "$@"
