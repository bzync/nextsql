package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	DefaultListenAddr  = "127.0.0.1:7210"
	DefaultBufferPages = 1024
	DefaultLogLevel    = "info"
	DefaultMaxInflight = 32
	DefaultMaxQueue    = 128
	// DefaultMaxOpenDatabases bounds how many distinct databases a single
	// nextsqld process will ever open at once via dbmanager (M2-3a). An
	// opened database is never evicted in this slice, so this is also a
	// hard ceiling on total databases ever opened by one process lifetime.
	DefaultMaxOpenDatabases = 8
	DefaultQueueWaitMS      = 5000
	DefaultDrainTimeoutMS   = 30000
	// DefaultDiskWatermarkCheckMS is used when the disk-watermark feature is
	// enabled (DiskWatermarkCheckMS > 0) but the operator didn't override
	// the percentages — see Config.DiskWatermarkThresholds.
	DefaultDiskWatermarkWarnPercent   = 85.0
	DefaultDiskWatermarkRejectPercent = 95.0
	// DefaultReplicaLagWarnEntries is used when the replica-lag check is
	// enabled (ReplicaLagCheckMS > 0) but the operator didn't override the
	// entry threshold — see Config.ReplicaLagWarnThreshold.
	DefaultReplicaLagWarnEntries = 1000
	DataFileName                 = "nextsql.db"
	AuthFileName                 = "nextsql.users"
	ACLFileName                  = "nextsql.acl"
	AuditFileName                = "nextsql.audit"
	AuthBrokerFileName           = "nextsql-auth-broker.conf"
)

// Config holds process settings. Encryption keys are never represented here.
type Config struct {
	DataDir          string
	KeyFile          string
	InstanceKeyFile  string
	AuthFile         string
	ListenAddr       string
	LogLevel         string
	TLSCert          string
	TLSKey           string
	TLSClientCA      string
	TLSClientCRL     string
	TokenKeyset      string
	TokenRevocations string
	TokenAudience    string
	// TokenIdentitySourceHints maps verified NSTK key ids to an audit-only
	// identity source. The only accepted source is "oidc". The map is never
	// consulted until after the credential signature verifies.
	TokenIdentitySourceHints map[uint32]string
	// AuthBrokerConfig and AuthBrokerListen enable the optional embedded OIDC
	// broker on a separate HTTP(S) listener. The broker config uses the same
	// format as the standalone nextsql-auth-broker command.
	AuthBrokerConfig string
	AuthBrokerListen string
	BufferPages      int
	RequireClientKey bool
	AuditFile        string
	// AuditSigningKeyset is an optional NSAK signer keyset. When configured,
	// nextsqld verifies the retained signed segment before append and signs
	// every new audit record. Private key material stays outside the database.
	AuditSigningKeyset string
	WalArchive         string
	// BackupDir, when set, is the directory the server writes backups into
	// (one timestamped subdirectory per `BACKUP DATABASE`) and lists /
	// verifies from (`system.backups`, `VERIFY BACKUP`). The NextSQL Manager
	// drives all three (`docs/design-manager.md` M5). Unset disables those
	// operations — there is nowhere for the server to put a backup, and a
	// driver-only client must never name a server filesystem path itself.
	BackupDir string
	// WalRetentionMS, when positive and WalArchive is configured, makes
	// nextsqld periodically advance the WAL pruning horizon
	// (DB.SetWALRetentionHorizon) to the newest archived-segment LSN at or
	// before now-WalRetentionMS, so a scheduled MAINTAIN DATABASE can prune
	// local WAL history older than this policy. 0 (default) leaves the
	// horizon unmanaged (existing manual-only behavior). Requires
	// WalArchive: pruning without an archiver would destroy the only copy
	// of that history, so retention is a no-op until one is configured.
	WalRetentionMS int
	// DiskWatermarkCheckMS, when positive, makes nextsqld periodically check
	// free disk space on the volume holding --data-dir and act on it — see
	// DiskWatermarkWarnPercent/DiskWatermarkRejectPercent. 0 (default)
	// disables the whole feature (existing behavior: no disk-space policy).
	DiskWatermarkCheckMS int
	// DiskWatermarkWarnPercent (default 85 when the check is enabled and
	// this is left 0) logs a warning once used space reaches this
	// percentage of the volume, and is also the recovery threshold: once
	// tripped (see below), the reject state only clears after usage drops
	// back below this line, not merely below the (higher) reject line —
	// hysteresis, so the two states don't flap right at one boundary.
	DiskWatermarkWarnPercent float64
	// DiskWatermarkRejectPercent (default 95 when the check is enabled and
	// this is left 0) additionally rejects new mutating statements
	// (Unavailable, independently of and without disturbing an operator's
	// own CLUSTER MAINTENANCE ENABLE) once used space reaches this
	// percentage — a last-resort backstop against actually running out of
	// disk mid-write, not a substitute for capacity planning.
	DiskWatermarkRejectPercent float64
	// ReplicaLagCheckMS, when positive, makes nextsqld periodically read this
	// node's own replication health (replication.ReplicaHealth.ApplyBacklog,
	// the same "entries known committed but not yet applied locally" figure
	// system.replica_health already exposes) and log a warning once it
	// reaches ReplicaLagWarnEntries. 0 (default) disables the check — the
	// figure remains readable on demand via system.replica_health, just not
	// proactively monitored. A no-op on a single-node deployment (no
	// cluster attached) and, in steady state, on the leader itself (its own
	// backlog is always 0) — this is a follower-focused signal.
	ReplicaLagCheckMS int
	// ReplicaLagWarnEntries (default 1000 when the check is enabled and
	// this is left 0) is the ApplyBacklog threshold that triggers the
	// warning above. Unlike the disk watermark, there is no reject/blocking
	// counterpart: a lagging follower does not affect the leader's ability
	// to accept writes, and STRONG/BOUNDED read consistency already refuse
	// to route reads to a follower that has fallen too far behind (see
	// Cluster.FollowerReadHealthy) — this setting is purely for operator
	// visibility (alerting on a classical "replica lag" condition), not an
	// admission-control gate.
	ReplicaLagWarnEntries int
	MaxInflight           int
	// MaxOpenDatabases bounds the dbmanager (M2-3a) open-database limit.
	// Zero means "use DefaultMaxOpenDatabases" (see Default()); it is not
	// itself a valid "unbounded" sentinel, since M2-3a never evicts.
	MaxOpenDatabases int
	// MaxTotalBufferPages caps, across every database this process has open
	// at once (the primary plus every dbmanager-opened secondary, M2-3b-2),
	// the total buffer-pool frames committed. Unlike MaxOpenDatabases, 0
	// here does mean unbounded — each Pool's frames are allocated in full at
	// open, so there is nothing to gate unless an operator opts in.
	MaxTotalBufferPages int
	// TaskWorkers sizes the one shared TaskPool every open database's
	// scheduled-task execution submits to (M2-3b-3a). Zero means "use
	// executor.defaultTaskWorkers" (the same default every individual
	// TaskRuntime used before centralizing) — like MaxOpenDatabases, this is
	// not itself a valid "unbounded" sentinel.
	TaskWorkers   int
	MaxQueryQueue int
	QueueWaitMS   int
	MaxResultRows int
	// MaxConnections and MaxConnectionsPerUser are 0 to leave the protocol
	// package's own default (128 / unlimited) untouched. IdleTimeoutMS is 0
	// to leave the protocol default (60s) untouched.
	MaxConnections        int
	MaxConnectionsPerUser int
	// MaxConnectionsPerDatabase and MaxConnectionsPerRealm (P27's own last
	// open exit-gate item) cap concurrent connections to one specific
	// (realm, database) pair, and to one realm across all its databases.
	// Both 0 (unlimited) by default, matching MaxConnectionsPerUser.
	MaxConnectionsPerDatabase int
	MaxConnectionsPerRealm    int
	IdleTimeoutMS             int
	// StatementTimeoutMS overrides the per-statement scheduler.Budget wall-
	// clock bound (scheduler.DefaultTimeout, 30s, otherwise). 0 leaves the
	// scheduler package's own default untouched.
	StatementTimeoutMS int
	// TransactionTimeoutMS bounds a transaction's total open lifetime
	// (BEGIN/first autocommit statement to COMMIT/ROLLBACK), checked lazily
	// at the start of each statement dispatched inside it. 0 (the default)
	// is unbounded — unlike StatementTimeoutMS/IdleTimeoutMS this has no
	// pre-existing non-zero default, so upgrading never starts aborting
	// already-long-running transactions unless an operator opts in.
	TransactionTimeoutMS int
	// LockTimeoutMS bounds how long a contended, non-deadlocking key/range
	// lock wait blocks before failing Exhausted. 0 (the default) blocks
	// indefinitely — only deadlock cycles are detected without this.
	LockTimeoutMS int
	// IdleTransactionTimeoutMS bounds how long a connection may sit with an
	// open transaction and no traffic before it is force-timed-out and the
	// transaction released, distinct from (and typically tighter than) the
	// general IdleTimeoutMS bound. 0 (the default) applies no distinct
	// bound — an idle transaction is then governed only by IdleTimeoutMS,
	// matching pre-P27 behavior.
	IdleTransactionTimeoutMS int
	// DrainTimeoutMS bounds a signal-triggered graceful shutdown: nextsqld
	// stops accepting new connections immediately, then waits up to this long
	// for each open connection to become idle (no in-flight statement, no
	// open transaction) before closing it, force-closing whatever remains at
	// the deadline. 0 disables draining (immediate hard close, the pre-P27
	// behavior).
	DrainTimeoutMS int
	NodeID         string
	RaftBind       string
	RaftJoin       string
	RaftBootstrap  bool
}

func Default() Config {
	return Config{
		ListenAddr:       DefaultListenAddr,
		LogLevel:         DefaultLogLevel,
		BufferPages:      DefaultBufferPages,
		MaxInflight:      DefaultMaxInflight,
		MaxOpenDatabases: DefaultMaxOpenDatabases,
		MaxQueryQueue:    DefaultMaxQueue,
		QueueWaitMS:      DefaultQueueWaitMS,
		DrainTimeoutMS:   DefaultDrainTimeoutMS,
	}
}

func (c Config) DataFile() string {
	if c.DataDir == "" {
		return DataFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + DataFileName
}

// InstanceRootFile returns the external deployment-registry root key path.
// It remains off the data volume when KeyFile does. An explicit setting is
// required when the database root is supplied only by a client.
func (c Config) InstanceRootFile() string {
	if c.InstanceKeyFile != "" {
		return c.InstanceKeyFile
	}
	if c.KeyFile == "" {
		return ""
	}
	return c.KeyFile + ".instance"
}

func (c Config) UsersFile() string {
	if c.AuthFile != "" {
		return c.AuthFile
	}
	if c.DataDir == "" {
		return AuthFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + AuthFileName
}

func (c Config) ACLFile() string {
	if c.DataDir == "" {
		return ACLFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + ACLFileName
}

func (c Config) AuditPath() string {
	if c.AuditFile != "" {
		return c.AuditFile
	}
	if c.DataDir == "" {
		return AuditFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + AuditFileName
}

// EmbeddedAuthBrokerConfigPath returns the explicitly configured broker file,
// or the data-directory default used by --auth-broker-listen.
func (c Config) EmbeddedAuthBrokerConfigPath() string {
	if c.AuthBrokerConfig != "" {
		return c.AuthBrokerConfig
	}
	if c.DataDir == "" {
		return AuthBrokerFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + AuthBrokerFileName
}

// EmbeddedAuthBrokerEnabled reports whether nextsqld should host the broker.
func (c Config) EmbeddedAuthBrokerEnabled() bool {
	return c.AuthBrokerConfig != "" || c.AuthBrokerListen != ""
}

// DiskWatermarkThresholds resolves the configured warn/reject percentages,
// substituting the defaults for either one left at its zero value.
func (c Config) DiskWatermarkThresholds() (warn, reject float64) {
	warn = c.DiskWatermarkWarnPercent
	if warn == 0 {
		warn = DefaultDiskWatermarkWarnPercent
	}
	reject = c.DiskWatermarkRejectPercent
	if reject == 0 {
		reject = DefaultDiskWatermarkRejectPercent
	}
	return warn, reject
}

// ReplicaLagWarnThreshold resolves the configured apply-backlog warn
// threshold, substituting DefaultReplicaLagWarnEntries when left at 0.
func (c Config) ReplicaLagWarnThreshold() uint64 {
	if c.ReplicaLagWarnEntries == 0 {
		return DefaultReplicaLagWarnEntries
	}
	return uint64(c.ReplicaLagWarnEntries)
}

// Load reads a simple key=value file. Unknown keys are rejected.
func Load(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, nerr.Wrap(nerr.IO, "config.Load", "open", err)
	}
	defer f.Close()
	return loadFrom(f)
}

// loadFrom parses key=value lines from r onto a fresh Default(), the shared
// body of Load and WithSetting. Every accepted key is one Marshal emits.
func loadFrom(r io.Reader) (Config, error) {
	cfg := Default()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "expected key=value")
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "data_dir":
			cfg.DataDir = v
		case "key_file":
			cfg.KeyFile = v
		case "instance_key_file":
			cfg.InstanceKeyFile = v
		case "listen_addr":
			cfg.ListenAddr = v
		case "log_level":
			cfg.LogLevel = v
		case "buffer_pages":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "buffer_pages must be a positive integer")
			}
			cfg.BufferPages = n
		case "auth_file":
			cfg.AuthFile = v
		case "tls_cert":
			cfg.TLSCert = v
		case "tls_key":
			cfg.TLSKey = v
		case "tls_client_ca":
			cfg.TLSClientCA = v
		case "tls_client_crl":
			cfg.TLSClientCRL = v
		case "token_verify_keyset":
			cfg.TokenKeyset = v
		case "token_revocations":
			cfg.TokenRevocations = v
		case "token_audience":
			cfg.TokenAudience = v
		case "token_identity_source_hint":
			hints, err := parseTokenIdentitySourceHints(v)
			if err != nil {
				return Config{}, err
			}
			cfg.TokenIdentitySourceHints = hints
		case "auth_broker_config":
			cfg.AuthBrokerConfig = v
		case "auth_broker_listen":
			cfg.AuthBrokerListen = v
		case "require_client_key":
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				cfg.RequireClientKey = true
			case "false", "0", "no":
				cfg.RequireClientKey = false
			default:
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "require_client_key must be true or false")
			}
		case "audit_file":
			cfg.AuditFile = v
		case "audit_signing_keyset":
			cfg.AuditSigningKeyset = v
		case "wal_archive":
			cfg.WalArchive = v
		case "backup_dir":
			cfg.BackupDir = v
		case "wal_retention_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "wal_retention_ms must be >= 0")
			}
			cfg.WalRetentionMS = n
		case "disk_watermark_check_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "disk_watermark_check_ms must be >= 0")
			}
			cfg.DiskWatermarkCheckMS = n
		case "disk_watermark_warn_percent":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 100 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "disk_watermark_warn_percent must be in [0, 100]")
			}
			cfg.DiskWatermarkWarnPercent = f
		case "disk_watermark_reject_percent":
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 || f > 100 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "disk_watermark_reject_percent must be in [0, 100]")
			}
			cfg.DiskWatermarkRejectPercent = f
		case "replica_lag_check_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "replica_lag_check_ms must be >= 0")
			}
			cfg.ReplicaLagCheckMS = n
		case "replica_lag_warn_entries":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "replica_lag_warn_entries must be >= 0")
			}
			cfg.ReplicaLagWarnEntries = n
		case "max_inflight_queries":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_inflight_queries must be a positive integer")
			}
			cfg.MaxInflight = n
		case "max_open_databases":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_open_databases must be >= 0")
			}
			cfg.MaxOpenDatabases = n
		case "max_total_buffer_pages":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_total_buffer_pages must be >= 0")
			}
			cfg.MaxTotalBufferPages = n
		case "task_workers":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "task_workers must be >= 0")
			}
			cfg.TaskWorkers = n
		case "max_query_queue":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_query_queue must be >= 0")
			}
			cfg.MaxQueryQueue = n
		case "query_queue_wait_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "query_queue_wait_ms must be a positive integer")
			}
			cfg.QueueWaitMS = n
		case "max_result_rows":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_result_rows must be a positive integer")
			}
			cfg.MaxResultRows = n
		case "max_connections":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_connections must be a positive integer")
			}
			cfg.MaxConnections = n
		case "max_connections_per_user":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_connections_per_user must be >= 0")
			}
			cfg.MaxConnectionsPerUser = n
		case "max_connections_per_database":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_connections_per_database must be >= 0")
			}
			cfg.MaxConnectionsPerDatabase = n
		case "max_connections_per_realm":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_connections_per_realm must be >= 0")
			}
			cfg.MaxConnectionsPerRealm = n
		case "idle_timeout_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "idle_timeout_ms must be a positive integer")
			}
			cfg.IdleTimeoutMS = n
		case "statement_timeout_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "statement_timeout_ms must be a positive integer")
			}
			cfg.StatementTimeoutMS = n
		case "transaction_timeout_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "transaction_timeout_ms must be >= 0")
			}
			cfg.TransactionTimeoutMS = n
		case "lock_timeout_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "lock_timeout_ms must be >= 0")
			}
			cfg.LockTimeoutMS = n
		case "idle_transaction_timeout_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "idle_transaction_timeout_ms must be >= 0")
			}
			cfg.IdleTransactionTimeoutMS = n
		case "shutdown_drain_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "shutdown_drain_ms must be >= 0")
			}
			cfg.DrainTimeoutMS = n
		case "node_id":
			cfg.NodeID = v
		case "raft_bind":
			cfg.RaftBind = v
		case "raft_join":
			cfg.RaftJoin = v
		case "raft_bootstrap":
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				cfg.RaftBootstrap = true
			case "false", "0", "no":
				cfg.RaftBootstrap = false
			default:
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "raft_bootstrap must be true or false")
			}
		default:
			return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "unknown key")
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, nerr.Wrap(nerr.IO, "config.Load", "read", err)
	}
	return cfg, nil
}

// Marshal renders the config back to the same key=value format Load reads.
// Every key it emits is one Load accepts, so Load(Marshal(c)) reconstructs an
// equal Config (given both start from the same Default()). String fields are
// emitted only when non-empty, numeric fields only when non-zero, and bools
// only when true — so the output records exactly the settings that differ
// from a bare Default(), plus the core fields Default() itself populates.
//
// It does not write a file or add comment headers; callers that persist a
// generated config (e.g. `nextsql setup`) prepend their own provenance
// header.
func (c Config) Marshal() []byte {
	var b strings.Builder
	str := func(k, v string) {
		if v != "" {
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	num := func(k string, v int) {
		if v != 0 {
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(strconv.Itoa(v))
			b.WriteByte('\n')
		}
	}
	flt := func(k string, v float64) {
		if v != 0 {
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(strconv.FormatFloat(v, 'g', -1, 64))
			b.WriteByte('\n')
		}
	}
	boolean := func(k string, v bool) {
		if v {
			b.WriteString(k)
			b.WriteString("=true\n")
		}
	}

	str("data_dir", c.DataDir)
	str("key_file", c.KeyFile)
	str("instance_key_file", c.InstanceKeyFile)
	str("auth_file", c.AuthFile)
	str("listen_addr", c.ListenAddr)
	str("log_level", c.LogLevel)
	num("buffer_pages", c.BufferPages)

	str("tls_cert", c.TLSCert)
	str("tls_key", c.TLSKey)
	str("tls_client_ca", c.TLSClientCA)
	str("tls_client_crl", c.TLSClientCRL)
	boolean("require_client_key", c.RequireClientKey)

	str("token_verify_keyset", c.TokenKeyset)
	str("token_revocations", c.TokenRevocations)
	str("token_audience", c.TokenAudience)
	if len(c.TokenIdentitySourceHints) != 0 {
		ids := make([]int, 0, len(c.TokenIdentitySourceHints))
		for id := range c.TokenIdentitySourceHints {
			ids = append(ids, int(id))
		}
		sort.Ints(ids)
		parts := make([]string, 0, len(ids))
		for _, id := range ids {
			parts = append(parts, strconv.Itoa(id)+":"+c.TokenIdentitySourceHints[uint32(id)])
		}
		str("token_identity_source_hint", strings.Join(parts, ","))
	}

	str("auth_broker_config", c.AuthBrokerConfig)
	str("auth_broker_listen", c.AuthBrokerListen)

	str("audit_file", c.AuditFile)
	str("audit_signing_keyset", c.AuditSigningKeyset)
	str("wal_archive", c.WalArchive)
	str("backup_dir", c.BackupDir)
	num("wal_retention_ms", c.WalRetentionMS)

	num("disk_watermark_check_ms", c.DiskWatermarkCheckMS)
	flt("disk_watermark_warn_percent", c.DiskWatermarkWarnPercent)
	flt("disk_watermark_reject_percent", c.DiskWatermarkRejectPercent)
	num("replica_lag_check_ms", c.ReplicaLagCheckMS)
	num("replica_lag_warn_entries", c.ReplicaLagWarnEntries)

	num("max_inflight_queries", c.MaxInflight)
	num("max_open_databases", c.MaxOpenDatabases)
	num("max_total_buffer_pages", c.MaxTotalBufferPages)
	num("task_workers", c.TaskWorkers)
	num("max_query_queue", c.MaxQueryQueue)
	num("query_queue_wait_ms", c.QueueWaitMS)
	num("max_result_rows", c.MaxResultRows)

	num("max_connections", c.MaxConnections)
	num("max_connections_per_user", c.MaxConnectionsPerUser)
	num("max_connections_per_database", c.MaxConnectionsPerDatabase)
	num("max_connections_per_realm", c.MaxConnectionsPerRealm)

	num("idle_timeout_ms", c.IdleTimeoutMS)
	num("statement_timeout_ms", c.StatementTimeoutMS)
	num("transaction_timeout_ms", c.TransactionTimeoutMS)
	num("lock_timeout_ms", c.LockTimeoutMS)
	num("idle_transaction_timeout_ms", c.IdleTransactionTimeoutMS)
	num("shutdown_drain_ms", c.DrainTimeoutMS)

	str("node_id", c.NodeID)
	str("raft_bind", c.RaftBind)
	str("raft_join", c.RaftJoin)
	boolean("raft_bootstrap", c.RaftBootstrap)

	return []byte(b.String())
}

// Entry is one redacted key=value pair from SafeEntries.
type Entry struct {
	Key, Value string
}

// redactedConfigKeys are the Marshal keys that name a network address. Never
// expose a network address over SQL (the same convention
// system.replication.leader_addr already follows) — the connecting operator
// already knows the address they dialed, but SafeEntries backs an
// admin-visible system.* table, and a different admin session shouldn't
// learn Raft peer topology or an OIDC broker's listener from it.
var redactedConfigKeys = map[string]bool{
	"listen_addr":        true,
	"raft_bind":          true,
	"raft_join":          true,
	"auth_broker_listen": true,
}

// SafeEntries renders the same settings Marshal does — a key it emits is one
// Marshal (and therefore Load) also emits, so this can never drift into
// naming a field Marshal doesn't already know about — as structured
// key/value pairs with every network-address-shaped value replaced by
// "[redacted]". Nothing in Config ever holds key material to begin with (see
// the type's own doc comment), so no other redaction is needed: every other
// value here is already safe to show an authenticated admin, the same trust
// tier system.users/system.tls/system.key_versions already use.
//
// Reuses Marshal's exact byte output (parsed back on "=") rather than
// re-enumerating every field a second time, so the two can never disagree
// about which keys exist or how a value is formatted.
func (c Config) SafeEntries() []Entry {
	lines := strings.Split(strings.TrimRight(string(c.Marshal()), "\n"), "\n")
	out := make([]Entry, 0, len(lines))
	for _, line := range lines {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		if redactedConfigKeys[k] {
			v = "[redacted]"
		}
		out = append(out, Entry{Key: k, Value: v})
	}
	return out
}

// settableKeys is every key SET CONFIG may target — exactly the keys Marshal
// emits and Load accepts. Derived once from a fully-populated Config so it
// can never silently drift from Marshal's own list: a key Marshal stops
// emitting (or starts emitting) changes this set automatically.
var settableKeys = func() map[string]bool {
	// A Config with every string non-empty, every number non-zero, every
	// bool true — so Marshal emits one line per settable key.
	probe := Config{
		DataDir: "x", KeyFile: "x", InstanceKeyFile: "x", AuthFile: "x",
		ListenAddr: "x", LogLevel: "x", BufferPages: 1,
		TLSCert: "x", TLSKey: "x", TLSClientCA: "x", TLSClientCRL: "x", RequireClientKey: true,
		TokenKeyset: "x", TokenRevocations: "x", TokenAudience: "x",
		TokenIdentitySourceHints: map[uint32]string{1: "x"},
		AuthBrokerConfig:         "x", AuthBrokerListen: "x",
		AuditFile: "x", AuditSigningKeyset: "x", WalArchive: "x", BackupDir: "x", WalRetentionMS: 1,
		DiskWatermarkCheckMS: 1, DiskWatermarkWarnPercent: 1, DiskWatermarkRejectPercent: 1,
		ReplicaLagCheckMS: 1, ReplicaLagWarnEntries: 1,
		MaxInflight: 1, MaxOpenDatabases: 1, MaxTotalBufferPages: 1, TaskWorkers: 1,
		MaxQueryQueue: 1, QueueWaitMS: 1, MaxResultRows: 1,
		MaxConnections: 1, MaxConnectionsPerUser: 1, MaxConnectionsPerDatabase: 1, MaxConnectionsPerRealm: 1,
		IdleTimeoutMS: 1, StatementTimeoutMS: 1, TransactionTimeoutMS: 1, LockTimeoutMS: 1,
		IdleTransactionTimeoutMS: 1, DrainTimeoutMS: 1,
		NodeID: "x", RaftBind: "x", RaftJoin: "x", RaftBootstrap: true,
	}
	keys := map[string]bool{}
	for _, line := range strings.Split(strings.TrimRight(string(probe.Marshal()), "\n"), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			keys[strings.TrimSpace(k)] = true
		}
	}
	return keys
}()

// Setting returns the marshalled value of one key (the same text Marshal
// would emit for it), or "" when the key is at its built-in default and
// therefore not emitted. Used to compare a running config against an
// on-disk one for SET CONFIG's restart-required indicator.
func (c Config) Setting(key string) string {
	key = strings.TrimSpace(key)
	for _, line := range strings.Split(strings.TrimRight(string(c.Marshal()), "\n"), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// SettableKeys returns the sorted list of keys SET CONFIG accepts.
func SettableKeys() []string {
	out := make([]string, 0, len(settableKeys))
	for k := range settableKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// WithSetting returns a copy of c with the single setting key=value applied,
// parsed and range-checked exactly as Load would and then Validate()d. reset
// (SET CONFIG key = DEFAULT) removes the key so it falls back to its built-in
// default. An unknown key, an unparseable value, or a result that fails
// validation is an error, and c is returned unchanged. Every network-address
// key is settable (there is no reason an operator can't move a listener),
// but the caller is responsible for the "no non-loopback listen without
// TLS" rule — Validate does not enforce it, matching Load.
//
// It works by splicing one line into c.Marshal()'s own output and re-parsing
// through loadFrom, so it can never disagree with Load about which keys
// exist or how a value is formatted — the same trick SafeEntries uses.
func (c Config) WithSetting(key, value string, reset bool) (Config, error) {
	key = strings.TrimSpace(key)
	if !settableKeys[key] {
		return c, nerr.New(nerr.InvalidArgument, "config.WithSetting", "unknown setting "+key)
	}
	kv := map[string]string{}
	var order []string
	for _, line := range strings.Split(strings.TrimRight(string(c.Marshal()), "\n"), "\n") {
		if line == "" {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		if _, seen := kv[k]; !seen {
			order = append(order, k)
		}
		kv[k] = strings.TrimSpace(v)
	}
	if reset {
		delete(kv, key)
	} else {
		if strings.ContainsAny(value, "\n\r") {
			return c, nerr.New(nerr.InvalidArgument, "config.WithSetting", "value must not contain a newline")
		}
		value = strings.TrimSpace(value)
		if value == "" {
			return c, nerr.New(nerr.InvalidArgument, "config.WithSetting", "empty value; use SET CONFIG "+key+" = DEFAULT to reset")
		}
		if _, seen := kv[key]; !seen {
			order = append(order, key)
		}
		kv[key] = value
	}
	var b strings.Builder
	for _, k := range order {
		if v, ok := kv[k]; ok {
			fmt.Fprintf(&b, "%s=%s\n", k, v)
		}
	}
	next, err := loadFrom(strings.NewReader(b.String()))
	if err != nil {
		return c, err
	}
	if err := next.Validate(); err != nil {
		return c, err
	}
	return next, nil
}

// WriteFile atomically writes c to path in the canonical key=value form Load
// reads, prefixed with a provenance header naming `by` (e.g. "SET CONFIG" or
// "nextsql setup"). It writes a sibling ".tmp" then renames, so a crash
// mid-write never leaves a truncated config. Any comments or non-canonical
// formatting a previous file had are not preserved — a config managed through
// SET CONFIG is canonical-form.
func WriteFile(path string, c Config, by string) error {
	header := "# NextSQL server configuration\n" +
		"# Last written by " + by + " on " + time.Now().UTC().Format(time.RFC3339) + "\n" +
		"# Edit and restart nextsqld to apply. Keys/secrets are never stored here.\n\n"
	body := append([]byte(header), c.Marshal()...)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return nerr.Wrap(nerr.IO, "config.WriteFile", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "config.WriteFile", "rename", err)
	}
	return nil
}

// EntryState is one row for the system.config viewer: a setting's value in
// the running process, its value in the node's on-disk nextsql.conf, and
// whether the two differ (i.e. whether a restart would change behavior).
// Value and FileValue are redacted exactly as SafeEntries does; the
// RestartRequired flag is computed from the unredacted values, so it stays
// correct even for the address keys SafeEntries masks.
type EntryState struct {
	Key             string
	Value           string
	FileValue       string
	RestartRequired bool
}

func marshalMap(c Config) map[string]string {
	m := map[string]string{}
	for _, line := range strings.Split(strings.TrimRight(string(c.Marshal()), "\n"), "\n") {
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}

// DiffState returns one EntryState per key that either config sets away from
// its default, comparing the running config to the on-disk one. Keys are in
// running-config Marshal order, then any file-only keys. Pass the running
// config for both arguments when the server has no config file — every row
// then reports RestartRequired=false with FileValue == Value.
func DiffState(running, file Config) []EntryState {
	rawRun := marshalMap(running)
	rawFile := marshalMap(file)
	safeRun := map[string]string{}
	for _, e := range running.SafeEntries() {
		safeRun[e.Key] = e.Value
	}
	safeFile := map[string]string{}
	for _, e := range file.SafeEntries() {
		safeFile[e.Key] = e.Value
	}

	seen := map[string]bool{}
	var order []string
	for _, line := range strings.Split(strings.TrimRight(string(running.Marshal()), "\n"), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			k = strings.TrimSpace(k)
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}
	for _, line := range strings.Split(strings.TrimRight(string(file.Marshal()), "\n"), "\n") {
		if k, _, ok := strings.Cut(line, "="); ok {
			k = strings.TrimSpace(k)
			if !seen[k] {
				seen[k] = true
				order = append(order, k)
			}
		}
	}

	out := make([]EntryState, 0, len(order))
	for _, k := range order {
		out = append(out, EntryState{
			Key:             k,
			Value:           safeRun[k],
			FileValue:       safeFile[k],
			RestartRequired: rawRun[k] != rawFile[k],
		})
	}
	return out
}

func (c Config) Validate() error {
	if c.BufferPages < 1 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "buffer_pages must be >= 1")
	}
	if c.MaxInflight < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_inflight_queries must be >= 0")
	}
	if c.MaxOpenDatabases < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_open_databases must be >= 0")
	}
	if c.MaxTotalBufferPages < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_total_buffer_pages must be >= 0")
	}
	if c.MaxTotalBufferPages > 0 && c.MaxTotalBufferPages < c.BufferPages {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_total_buffer_pages must be >= buffer_pages, or 0 for unbounded")
	}
	if c.TaskWorkers < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "task_workers must be >= 0")
	}
	if c.MaxQueryQueue < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_query_queue must be >= 0")
	}
	if c.MaxConnections < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_connections must be >= 0")
	}
	if c.MaxConnectionsPerUser < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_connections_per_user must be >= 0")
	}
	if c.MaxConnectionsPerDatabase < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_connections_per_database must be >= 0")
	}
	if c.MaxConnectionsPerRealm < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_connections_per_realm must be >= 0")
	}
	if c.IdleTimeoutMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "idle_timeout_ms must be >= 0")
	}
	if c.StatementTimeoutMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "statement_timeout_ms must be >= 0")
	}
	if c.TransactionTimeoutMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "transaction_timeout_ms must be >= 0")
	}
	if c.LockTimeoutMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "lock_timeout_ms must be >= 0")
	}
	if c.IdleTransactionTimeoutMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "idle_transaction_timeout_ms must be >= 0")
	}
	if c.WalRetentionMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "wal_retention_ms must be >= 0")
	}
	if c.DiskWatermarkCheckMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "disk_watermark_check_ms must be >= 0")
	}
	if c.DiskWatermarkWarnPercent < 0 || c.DiskWatermarkWarnPercent > 100 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "disk_watermark_warn_percent must be in [0, 100]")
	}
	if c.DiskWatermarkRejectPercent < 0 || c.DiskWatermarkRejectPercent > 100 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "disk_watermark_reject_percent must be in [0, 100]")
	}
	if warn, reject := c.DiskWatermarkThresholds(); warn >= reject {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "disk_watermark_warn_percent must be less than disk_watermark_reject_percent")
	}
	if c.ReplicaLagCheckMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "replica_lag_check_ms must be >= 0")
	}
	if c.ReplicaLagWarnEntries < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "replica_lag_warn_entries must be >= 0")
	}
	if c.DrainTimeoutMS < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "shutdown_drain_ms must be >= 0")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "tls_cert and tls_key must be set together")
	}
	if c.TLSClientCA != "" && c.TLSCert == "" {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "tls_client_ca requires tls_cert and tls_key")
	}
	if c.TLSClientCRL != "" && c.TLSClientCA == "" {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "tls_client_crl requires tls_client_ca")
	}
	if c.TokenRevocations != "" && c.TokenKeyset == "" {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "token_revocations requires token_verify_keyset")
	}
	if c.TokenAudience != "" && c.TokenKeyset == "" {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "token_audience requires token_verify_keyset")
	}
	if len(c.TokenIdentitySourceHints) != 0 && c.TokenKeyset == "" {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "token_identity_source_hint requires token_verify_keyset")
	}
	if len(c.TokenIdentitySourceHints) > 64 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "token_identity_source_hint has too many entries")
	}
	for id, source := range c.TokenIdentitySourceHints {
		if id == 0 || source != "oidc" {
			return nerr.New(nerr.InvalidArgument, "config.Validate", "token_identity_source_hint must contain non-zero key ids mapped to oidc")
		}
	}
	if c.EmbeddedAuthBrokerEnabled() {
		if c.TokenKeyset == "" {
			return nerr.New(nerr.InvalidArgument, "config.Validate", "embedded auth broker requires token_verify_keyset")
		}
		if c.AuthBrokerListen != "" && strings.TrimSpace(c.AuthBrokerListen) == "" {
			return nerr.New(nerr.InvalidArgument, "config.Validate", "auth_broker_listen must not be blank")
		}
		if c.RaftBind != "" {
			return nerr.New(nerr.InvalidArgument, "config.Validate", "embedded auth broker is supported only for non-HA deployments")
		}
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return nerr.New(nerr.InvalidArgument, "config.Validate", "log_level must be debug, info, warn, or error")
	}
	if (c.RaftBind != "" || c.NodeID != "") && (c.RaftBind == "" || c.NodeID == "") {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "node_id and raft_bind must be set together")
	}
	return nil
}

// parseTokenIdentitySourceHints parses a bounded comma-separated list such as
// "7:oidc,9:oidc". Only the OIDC source is accepted: arbitrary audit labels
// must never be introduced through configuration.
func parseTokenIdentitySourceHints(raw string) (map[uint32]string, error) {
	const maxHints = 64
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	parts := strings.Split(raw, ",")
	if len(parts) > maxHints {
		return nil, nerr.New(nerr.InvalidArgument, "config.Load", "token_identity_source_hint has too many entries")
	}
	hints := make(map[uint32]string, len(parts))
	for _, part := range parts {
		idText, source, ok := strings.Cut(strings.TrimSpace(part), ":")
		id64, err := strconv.ParseUint(strings.TrimSpace(idText), 10, 32)
		source = strings.ToLower(strings.TrimSpace(source))
		if !ok || err != nil || id64 == 0 || source != "oidc" {
			return nil, nerr.New(nerr.InvalidArgument, "config.Load", "token_identity_source_hint entries must be key-id:oidc")
		}
		id := uint32(id64)
		if _, exists := hints[id]; exists {
			return nil, nerr.New(nerr.InvalidArgument, "config.Load", "token_identity_source_hint contains a duplicate key id")
		}
		hints[id] = source
	}
	return hints, nil
}
