#!/bin/sh
set -eu

data_dir=${NEXTSQL_DATA_DIR:-/var/lib/nextsql}
key_file=${NEXTSQL_KEY_FILE:-/run/secrets/root.key}
listen_addr=${NEXTSQL_LISTEN:-0.0.0.0:7210}
password_file=${NEXTSQL_SERVER_PASSWORD_FILE:-/run/bootstrap/password}

# wait_for_file polls until $1 exists, up to five minutes, then fails loudly.
# Used for the Raft seed handoff (docs/docker.md "Multi-node HA cluster"):
# a joining node's data directory stays empty until the bootstrap node's
# backup is fully verified (backup.md's "verified" marker, written last).
wait_for_file() {
	tries=0
	while [ ! -f "$1" ]; do
		tries=$((tries + 1))
		if [ "$tries" -gt 300 ]; then
			echo "nextsql: timed out waiting for $2 ($1)" >&2
			exit 1
		fi
		sleep 1
	done
}

# wait_for_tcp polls until host:port ($1) accepts a connection, up to five
# minutes. Used only by the Raft-bootstrap node so its one-shot AddVoter
# attempt (internal/replication.Cluster.JoinPeers, ~1s of retries) finds
# every peer's Raft transport already listening instead of racing it.
wait_for_tcp() {
	host=${1%:*}
	port=${1##*:}
	tries=0
	while ! nc -z "$host" "$port" 2>/dev/null; do
		tries=$((tries + 1))
		if [ "$tries" -gt 300 ]; then
			echo "nextsql: timed out waiting for $1 to accept connections" >&2
			exit 1
		fi
		sleep 1
	done
}

# The key lives in the separate /run/secrets volume, never the database volume.
if [ ! -e "$data_dir/nextsql.db" ]; then
	if [ -n "${NEXTSQL_SEED_FROM:-}" ]; then
		# HA join path: this node's data/identity must match the Raft
		# group's (docs/ha.md "All replicas of one database share the
		# keystore / root unlock key"), so it never runs its own `init` —
		# it restores the bootstrap node's backup instead.
		echo "nextsql: waiting for seed backup at ${NEXTSQL_SEED_FROM}"
		wait_for_file "${NEXTSQL_SEED_FROM}/verified" "seed backup"
		echo "nextsql: restoring from seed backup ${NEXTSQL_SEED_FROM}"
		# `nextsql restore` also writes bookkeeping into its --from source
		# while opening the backup's keystore (backup.OpenKeys), and
		# NEXTSQL_SEED_FROM is one shared volume every joining node reads —
		# concurrent joiners restoring straight from it race each other's
		# writes there. Copy it to a private scratch path first so each
		# node only ever mutates its own copy.
		seed_copy=$(mktemp -d)
		cp -a "$NEXTSQL_SEED_FROM"/. "$seed_copy"/
		# `nextsql restore --data-dir` refuses an already-existing directory,
		# but $data_dir is a volume mount point Docker creates before this
		# script ever runs — so restore into a scratch path instead and
		# relocate its contents up into $data_dir.
		restore_tmp=$(mktemp -d)
		rmdir "$restore_tmp"
		/usr/local/bin/nextsql restore --from "$seed_copy" --data-dir "$restore_tmp" --key-file "$key_file"
		find "$restore_tmp" -mindepth 1 -maxdepth 1 -exec mv -t "$data_dir" {} +
		rmdir "$restore_tmp"
		rm -rf "$seed_copy"
	else
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
fi

# HA seed path: the bootstrap node publishes a backup of its just-initialized
# (pre-Raft) database for the other voters to restore from. Idempotent — a
# restarted bootstrap node with a backup already marked "verified" skips this.
if [ -n "${NEXTSQL_SEED_TO:-}" ] && [ ! -f "${NEXTSQL_SEED_TO}/verified" ]; then
	echo "nextsql: producing seed backup at ${NEXTSQL_SEED_TO}"
	/usr/local/bin/nextsql backup --data-dir "$data_dir" --key-file "$key_file" --out "$NEXTSQL_SEED_TO"
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
if [ -n "${NEXTSQL_NODE_ID:-}" ]; then
	set -- "$@" --node-id "$NEXTSQL_NODE_ID"
fi
if [ -n "${NEXTSQL_RAFT_BIND:-}" ]; then
	set -- "$@" --raft-bind "$NEXTSQL_RAFT_BIND"
fi
if [ -n "${NEXTSQL_RAFT_JOIN:-}" ]; then
	set -- "$@" --raft-join "$NEXTSQL_RAFT_JOIN"
fi
if [ -n "${NEXTSQL_RAFT_BOOTSTRAP:-}" ]; then
	set -- "$@" --raft-bootstrap
fi
# Only the bootstrap node waits: it is the one whose startup fires the
# one-shot AddVoter attempt (internal/replication.Cluster.JoinPeers), and
# that attempt must find every peer's Raft transport already listening.
if [ -n "${NEXTSQL_RAFT_BOOTSTRAP:-}" ] && [ -n "${NEXTSQL_JOIN_WAIT:-}" ]; then
	old_ifs=$IFS
	IFS=','
	for peer in $NEXTSQL_JOIN_WAIT; do
		IFS=$old_ifs
		echo "nextsql: waiting for raft peer $peer"
		wait_for_tcp "$peer"
		IFS=','
	done
	IFS=$old_ifs
fi
exec "$@"
