package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/diskspace"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/buffer"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/version"
)

// serviceStop is closed by the Windows service manager when a stop is
// requested. It is nil in the foreground (systemd / console) path.
var serviceStop <-chan struct{}

func main() {
	if handled, err := runAsWindowsService(); handled {
		if err != nil {
			fmt.Fprintf(os.Stderr, "nextsqld: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "nextsqld: %v\n", err)
		os.Exit(1)
	}
}

func serveContext() (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	if serviceStop == nil {
		return ctx, stop
	}
	ctx2, cancel := context.WithCancel(ctx)
	go func() {
		select {
		case <-serviceStop:
			cancel()
		case <-ctx.Done():
		}
	}()
	return ctx2, func() { stop(); cancel() }
}

func run() error {
	fs := flag.NewFlagSet("nextsqld", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "optional key=value config file")
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	keyFile := fs.String("key-file", "", "DEK file from nextsql init (never pass a key in a URL)")
	instanceKeyFile := fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	authFile := fs.String("auth-file", "", "user store (default: DATA-DIR/nextsql.users)")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	listen := fs.String("listen", config.DefaultListenAddr, "listen address")
	logLevel := fs.String("log-level", config.DefaultLogLevel, "debug|info|warn|error")
	tlsCert := fs.String("tls-cert", "", "TLS certificate (PEM)")
	tlsKey := fs.String("tls-key", "", "TLS private key (PEM)")
	tlsClientCA := fs.String("tls-client-ca", "", "PEM CA for required mTLS client certificates")
	tlsClientCRL := fs.String("tls-client-crl", "", "PEM CRL bundle for required fail-closed mTLS revocation checks")
	tlsOCSPMode := fs.String("tls-ocsp-mode", "", "disabled|optional|enforce OCSP certificate status checking")
	tlsOCSPResponder := fs.String("tls-ocsp-responder", "", "override OCSP responder URL")
	authBrokerConfig := fs.String("auth-broker-config", "", "embedded OIDC broker config (default: DATA-DIR/nextsql-auth-broker.conf)")
	authBrokerListen := fs.String("auth-broker-listen", "", "host the OIDC broker on this separate HTTP(S) listener (single-node only)")
	user := fs.String("user", "", "bootstrap or update this user")
	passwordFile := fs.String("password-file", "", "password file for --user (never a URL)")
	requireClientKey := fs.Bool("require-client-key", false, "do not load --key-file; first client must unlock")
	auditFile := fs.String("audit-file", "", "audit log path (default: DATA-DIR/nextsql.audit)")
	auditSigningKeyset := fs.String("audit-signing-keyset", "", "NSAK Ed25519 keyset used to sign new audit records")
	walArchive := fs.String("wal-archive", "", "encrypted WAL archive directory for PITR")
	nodeID := fs.String("node-id", "", "Raft node id (required with --raft-bind)")
	raftBind := fs.String("raft-bind", "", "Raft bind address (enables HA)")
	raftJoin := fs.String("raft-join", "", "Raft peers as id=addr,id=addr (min 3 voters)")
	raftBootstrap := fs.Bool("raft-bootstrap", false, "bootstrap this node with --raft-join")
	raftHeartbeatMS := fs.Int("raft-heartbeat-ms", 0, "Raft leader-contact interval in ms (0 = built-in default; also sets the follower-read freshness window)")
	raftElectionMS := fs.Int("raft-election-ms", 0, "Raft election timeout in ms (0 = built-in default; must be >= --raft-heartbeat-ms)")
	raftLeaderLeaseMS := fs.Int("raft-leader-lease-ms", 0, "Raft leader lease in ms (0 = built-in default; must be <= --raft-heartbeat-ms)")
	raftCommitTimeoutMS := fs.Int("raft-commit-timeout-ms", 0, "Raft commit batching timeout in ms (0 = built-in default)")
	production := fs.Bool("production", false, "force the production deployment profile and refuse to start unless the live-production preflight passes")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	envSettings, err := cli.Resolve(fs, os.Args[1:])
	if err != nil {
		return err
	}

	cfg := config.Default()
	if *cfgPath != "" {
		loaded, err := config.Load(*cfgPath)
		if err != nil {
			return err
		}
		cfg = loaded
	}
	serverPass := ""
	applyDotenvSettings(&cfg, envSettings, user, passwordFile, &serverPass)
	if set["data-dir"] {
		cfg.DataDir = *dataDir
	}
	if set["key-file"] {
		cfg.KeyFile = *keyFile
	}
	if set["instance-key-file"] {
		cfg.InstanceKeyFile = *instanceKeyFile
	}
	if set["auth-file"] {
		cfg.AuthFile = *authFile
	}
	if set["buffer-pages"] {
		cfg.BufferPages = *bufferPages
	}
	if set["listen"] {
		cfg.ListenAddr = *listen
	}
	if set["log-level"] {
		cfg.LogLevel = *logLevel
	}
	if set["tls-cert"] {
		cfg.TLSCert = *tlsCert
	}
	if set["tls-key"] {
		cfg.TLSKey = *tlsKey
	}
	if set["tls-client-ca"] {
		cfg.TLSClientCA = *tlsClientCA
	}
	if set["tls-client-crl"] {
		cfg.TLSClientCRL = *tlsClientCRL
	}
	if set["tls-ocsp-mode"] {
		cfg.TLSOCSPMode = *tlsOCSPMode
	}
	if set["tls-ocsp-responder"] {
		cfg.TLSOCSPResponder = *tlsOCSPResponder
	}
	if set["auth-broker-config"] {
		cfg.AuthBrokerConfig = *authBrokerConfig
	}
	if set["auth-broker-listen"] {
		cfg.AuthBrokerListen = *authBrokerListen
	}
	if set["require-client-key"] {
		cfg.RequireClientKey = *requireClientKey
	}
	if set["audit-file"] {
		cfg.AuditFile = *auditFile
	}
	if set["audit-signing-keyset"] {
		cfg.AuditSigningKeyset = *auditSigningKeyset
	}
	if set["wal-archive"] {
		cfg.WalArchive = *walArchive
	}
	if set["node-id"] {
		cfg.NodeID = *nodeID
	}
	if set["raft-bind"] {
		cfg.RaftBind = *raftBind
	}
	if set["raft-join"] {
		cfg.RaftJoin = *raftJoin
	}
	if set["raft-bootstrap"] {
		cfg.RaftBootstrap = *raftBootstrap
	}
	if set["raft-heartbeat-ms"] {
		cfg.RaftHeartbeatMS = *raftHeartbeatMS
	}
	if set["raft-election-ms"] {
		cfg.RaftElectionMS = *raftElectionMS
	}
	if set["raft-leader-lease-ms"] {
		cfg.RaftLeaderLeaseMS = *raftLeaderLeaseMS
	}
	if set["raft-commit-timeout-ms"] {
		cfg.RaftCommitTimeoutMS = *raftCommitTimeoutMS
	}
	if *production {
		cfg.ApplyProductionDefaults()
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := cfg.CheckProduction(); err != nil {
		return err
	}
	if cfg.DataDir == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--data-dir is required (or set it in --config)")
	}
	if !cfg.RequireClientKey && cfg.KeyFile == "" && cfg.InstanceKeyFile == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--key-file (or, for a manifest-bootstrapped deployment, --instance-key-file) is required unless --require-client-key is set")
	}
	// Apply the storage preallocation runway before any database is opened
	// or created: it is process-wide, matching nextsql.conf's own per-node
	// scope, and every database this node serves shares it.
	file.SetCapacityAhead(cfg.PreallocAheadPages)
	dataDirLock, err := hosting.AcquireDataDirLock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer dataDirLock.Close()

	log, logRing := logging.NewWithRing(cfg.LogLevel, os.Stderr)
	dbPath := filepath.Join(cfg.DataDir, config.DataFileName)
	ksPath := crypto.KeystorePath(dbPath)
	// bufBudget (M2-3b-2) is shared across every database this process opens
	// — the primary plus every dbmanager-opened secondary — so the total
	// buffer-pool memory committed at once is bounded process-wide, not just
	// per database. cfg.MaxTotalBufferPages == 0 (default) makes it
	// unbounded, matching pre-M2-3b-2 behavior exactly.
	bufBudget := buffer.NewBudget(cfg.MaxTotalBufferPages)
	// taskPool (M2-3b-3a) is the shared, fixed-size worker set every open
	// database's task execution submits claimed tasks to — either directly
	// via a dedicated TaskRuntime (the legacy/non-hosted primary), or via
	// the single CentralScheduler covering every dbmanager-open database at
	// once (M2-3b-3b) — so task-execution goroutine count no longer scales
	// with the number of open databases, unlike before either landed. A nil
	// parent context (not the signal-aware ctx created below) is
	// deliberate: taskPool's lifecycle is driven only by this defer, which
	// is registered here — before every other close-related defer below,
	// including srv.Close() and the dbMgr/db cleanup defer further down —
	// specifically so it *runs last* (defers run LIFO). Every TaskRuntime
	// and CentralScheduler submitting to taskPool must already be closed
	// before taskPool.Close() runs, or its worker goroutines could exit out
	// from under a still-open submitter's pending submission — see
	// TaskPool.Close's own doc comment.
	taskPool, err := executor.NewTaskPool(nil, cfg.TaskWorkers)
	if err != nil {
		return err
	}
	defer func() { _ = taskPool.Close() }()

	var (
		db              *executor.DB
		env             *crypto.Envelope
		keys            crypto.KeyProvider
		cluster         *replication.Cluster
		hostingRegistry *hosting.Registry
		hostedRealm     hosting.Realm
		hostedDatabase  hosting.Database
		backgroundWait  func()
	)
	defer func() {
		// serveContext has been canceled by its later-registered defer before
		// this cleanup runs. Wait for every primary-database maintenance loop
		// to observe it before closing the handle it uses.
		if backgroundWait != nil {
			backgroundWait()
		}
		if db != nil {
			_ = db.Close()
		}
		if env != nil {
			_ = env.Close()
		}
		if hostingRegistry != nil {
			_ = hostingRegistry.Close()
		}
	}()
	hostingRegistry, hostedRealm, hostedDatabase, err = openHostedDefault(cfg)
	if err != nil {
		return err
	}
	// A deployment initialized without `--database` has keys and an
	// administrator but no database yet, and there is nothing to serve.
	// Answer with the command that creates one instead of the bare
	// "no such file or directory" the open would produce three frames later.
	if err := requireInitializedDatabase(cfg, dbPath, hostingRegistry); err != nil {
		return err
	}
	// The deployment's one database always lives at DATA-DIR/nextsql.db and
	// is opened eagerly here. (Multi-database hosting once deferred that open
	// for a managed-layout default and served it lazily; openHostedDefault
	// now refuses such a deployment outright rather than half-serving it.)
	// RequireClientKey keeps its own deferred-open path regardless.
	if !cfg.RequireClientKey && cfg.KeyFile == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--key-file is required to open DATA-DIR/nextsql.db")
	}
	if !cfg.RequireClientKey {
		var opened *crypto.Envelope
		keys, opened, err = openKeys(cfg.KeyFile, ksPath)
		if err != nil {
			return err
		}
		env = opened
		db, err = executor.OpenWith(dbPath, keys, cfg.BufferPages, storage.OpenOptions{Budget: bufBudget})
		if err != nil {
			return err
		}
		if err := validateHostedDatabase(hostingRegistry, hostedDatabase, db); err != nil {
			return err
		}
		applyHostedStorageCap(db, hostedRealm, hostedDatabase)
		applyOps(db, cfg)
		if err := installArchiver(db, keys, cfg.WalArchive); err != nil {
			return err
		}
	}

	users, err := auth.OpenOrCreate(cfg.UsersFile())
	if err != nil {
		return err
	}
	acl, err := security.OpenOrCreateACL(cfg.ACLFile())
	if err != nil {
		return err
	}
	if *user != "" {
		pw := serverPass
		if *passwordFile != "" {
			var err error
			pw, err = auth.ReadPasswordFile(*passwordFile)
			if err != nil {
				return err
			}
		} else if pw != "" {
			fmt.Fprintln(os.Stderr, "using NEXTSQL_SERVER_PASS from the environment; prefer NEXTSQL_SERVER_PASSWORD_FILE")
		}
		if pw == "" {
			return nerr.New(nerr.InvalidArgument, "nextsqld", "--password-file, NEXTSQL_SERVER_PASSWORD_FILE, or NEXTSQL_SERVER_PASS is required with the bootstrap user")
		}
		if err := users.Upsert(*user, pw); err != nil {
			return err
		}
		if err := acl.Grant(*user, security.PrivAdmin, security.ScopeCluster, ""); err != nil {
			return err
		}
		if err := acl.Grant(*user, security.PrivConnect, security.ScopeDatabase, ""); err != nil {
			return err
		}
	}
	if users.Count() == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "no users configured; pass --user/--password-file or set NEXTSQL_SERVER_USER with its password")
	}

	audit, err := security.OpenAudit(cfg.AuditPath())
	if err != nil {
		return err
	}
	defer func() { _ = audit.Close() }()
	var auditSigningKeys *security.AuditKeyset
	if cfg.AuditSigningKeyset == "" && audit.SigningRequired() {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "existing audit chain requires --audit-signing-keyset")
	}
	if cfg.AuditSigningKeyset != "" {
		auditSigningKeys, err = security.OpenAuditKeyset(cfg.AuditSigningKeyset)
		if err != nil {
			return err
		}
		if err := auditSigningKeys.ValidateSigner(); err != nil {
			return err
		}
		if audit.SigningRequired() {
			report, err := security.VerifyFile(cfg.AuditPath(), auditSigningKeys)
			if err != nil {
				return err
			}
			if !report.Verified {
				return nerr.New(nerr.InvalidFormat, "nextsqld", fmt.Sprintf("audit signature verification failed at line %d: %s", report.FirstBadLine, report.Problem))
			}
		}
		if err := audit.SetSigningKeys(auditSigningKeys); err != nil {
			return err
		}
	}
	defer func() {
		if cluster != nil {
			_ = cluster.Shutdown()
		}
	}()

	if db != nil && !cfg.RequireClientKey {
		cluster, err = startCluster(db, keys, cfg, audit)
		if err != nil {
			return err
		}
	}

	reg := security.NewRegistry()
	srv := protocol.NewServer(db, users)
	defer func() { _ = srv.Close() }()
	srv.ACL = acl
	srv.Audit = audit
	srv.Registry = reg
	srv.Log = log
	srv.RequireClientKey = cfg.RequireClientKey
	srv.DrainTimeout = time.Duration(cfg.DrainTimeoutMS) * time.Millisecond
	if hostingRegistry != nil {
		srv.Database = hostedDatabase.Name
		srv.Realm = hostedRealm.Name
		srv.HostingRegistry = hostingRegistry
		if db != nil {
			db.SetDatabaseName(hostedDatabase.Name)
		}
	}
	if cfg.MaxResultRows > 0 || cfg.MaxFrameBytes > 0 || cfg.MaxStatementBytes > 0 || cfg.MaxParameters > 0 || cfg.MaxPrepared > 0 || cfg.MaxResultBytes > 0 || cfg.MaxConnections > 0 || cfg.MaxConnectionsPerUser > 0 || cfg.MaxConnectionsPerDatabase > 0 || cfg.MaxConnectionsPerRealm > 0 || cfg.IdleTimeoutMS > 0 || cfg.StatementTimeoutMS > 0 || cfg.TransactionTimeoutMS > 0 || cfg.IdleTransactionTimeoutMS > 0 {
		lim := srv.Limits
		if cfg.MaxResultRows > 0 {
			lim.Query.ResultRows = cfg.MaxResultRows
		}
		if cfg.MaxFrameBytes > 0 {
			lim.MaxPacket = cfg.MaxFrameBytes
		}
		if cfg.MaxStatementBytes > 0 {
			lim.MaxSQL = cfg.MaxStatementBytes
		}
		if cfg.MaxParameters > 0 {
			lim.MaxParams = cfg.MaxParameters
		}
		if cfg.MaxPrepared > 0 {
			lim.MaxPrepared = cfg.MaxPrepared
		}
		if cfg.MaxResultBytes > 0 {
			lim.MaxResultBytes = int64(cfg.MaxResultBytes)
			lim.Query.ResultBytes = int64(cfg.MaxResultBytes)
		}
		if lim.MaxSQL > lim.MaxPacket {
			return nerr.New(nerr.InvalidArgument, "nextsqld", "max_statement_bytes cannot exceed max_frame_bytes")
		}
		if cfg.MaxConnections > 0 {
			lim.MaxSessions = cfg.MaxConnections
		}
		if cfg.MaxConnectionsPerUser > 0 {
			lim.MaxSessionsPerUser = cfg.MaxConnectionsPerUser
		}
		if cfg.MaxConnectionsPerDatabase > 0 {
			lim.MaxSessionsPerDatabase = cfg.MaxConnectionsPerDatabase
		}
		if cfg.MaxConnectionsPerRealm > 0 {
			lim.MaxSessionsPerRealm = cfg.MaxConnectionsPerRealm
		}
		if cfg.IdleTimeoutMS > 0 {
			lim.Idle = time.Duration(cfg.IdleTimeoutMS) * time.Millisecond
		}
		if cfg.StatementTimeoutMS > 0 {
			lim.Query.Time = time.Duration(cfg.StatementTimeoutMS) * time.Millisecond
		}
		if cfg.TransactionTimeoutMS > 0 {
			lim.TxnTimeout = time.Duration(cfg.TransactionTimeoutMS) * time.Millisecond
		}
		if cfg.IdleTransactionTimeoutMS > 0 {
			lim.IdleTxn = time.Duration(cfg.IdleTransactionTimeoutMS) * time.Millisecond
		}
		srv.Limits = lim
	}
	if db != nil {
		db.SetDrainFunc(func(timeout time.Duration) {
			if timeout <= 0 {
				timeout = srv.DrainTimeout
			}
			srv.Drain(timeout)
		})
		// env is nil for an embedded/CLI bare crypto.KeyProvider (no
		// persistent .keys keystore) — system.key_versions then correctly
		// reports "not attached" (see DB.KeyStatus's doc comment) rather
		// than this being wired to something that would always error.
		if env != nil {
			db.SetKeyStatusSource(func() ([]crypto.KeyStatus, bool) {
				st, err := env.KeyStatus()
				return st, err == nil
			})
		}
		// cfg is fully settled by this point (every cfg.Field = ... mutation
		// above happens during flag/dotenv parsing, well before here), so
		// this closure can safely capture it directly rather than a
		// snapshot copy. system.config compares the running cfg against a
		// fresh read of the on-disk nextsql.conf (config.DiffState) so
		// file_value / restart_required reflect a persisted-but-not-applied
		// SET CONFIG write, or a startup flag that overrode the file. When
		// there is no config file, running and file are the same.
		configFilePath := strings.TrimSpace(*cfgPath)
		db.SetConfigSource(func() ([]config.EntryState, bool) {
			fileCfg := cfg
			if configFilePath != "" {
				if loaded, err := config.Load(configFilePath); err == nil {
					fileCfg = loaded
				}
			}
			return config.DiffState(cfg, fileCfg), true
		})
		// SET CONFIG persists one setting to the on-disk nextsql.conf. Only
		// wired when the server was started from a config file — otherwise
		// there is nothing to persist to and SET CONFIG fails Unavailable.
		// The write targets the file's own config (not the running cfg), so
		// a startup flag override is never baked into the file as a side
		// effect. Persist-only: nothing is hot-reloaded, so restart_required
		// is true whenever the new file value differs from the running one.
		if configFilePath != "" {
			db.SetConfigWriter(func(key, value string, reset bool) (executor.ConfigWriteResult, error) {
				fileCfg, err := config.Load(configFilePath)
				if err != nil {
					return executor.ConfigWriteResult{}, err
				}
				next, err := fileCfg.WithSetting(key, value, reset)
				if err != nil {
					return executor.ConfigWriteResult{}, err
				}
				if err := config.WriteFile(configFilePath, next, "SET CONFIG"); err != nil {
					return executor.ConfigWriteResult{}, err
				}
				fileVal := next.Setting(key)
				runVal := cfg.Setting(key)
				return executor.ConfigWriteResult{
					Key:             key,
					FileValue:       fileVal,
					RunningValue:    runVal,
					RestartRequired: fileVal != runVal,
				}, nil
			})
		}
		// audit and auditSigningKeys (nil if signing isn't configured —
		// security.TailEvents tolerates a nil verifiers keyset exactly like
		// VerifyFile already does) are both settled above, before this
		// point. Re-reads the file from disk on every query by design —
		// system.audit_log/audit_verify reflect what is actually durable
		// right now, not a startup snapshot.
		db.SetAuditSource(func(maxEvents int) (security.TailReport, bool) {
			tr, err := security.TailEvents(audit.Path(), maxEvents, auditSigningKeys)
			if err != nil {
				// A read/open failure (e.g. the file was removed out from
				// under a running server) is real operational information,
				// not "no audit log configured" — surface it through the
				// same report.Problem field a chain-integrity finding would
				// use, rather than silently degrading to "not attached".
				return security.TailReport{VerifyReport: security.VerifyReport{Problem: err.Error()}}, true
			}
			return tr, true
		})
		// Route this DB's own query/txn/rows/fk/maintenance/cdc counters into
		// the process-wide registry (metrics.Default()), the same one the
		// crypto/storage/replication hooks already write to and the one
		// system.metrics reads — so a single Snapshot is internally coherent
		// (QPS/TPS/EncryptPct all computed against one born time). Safe here:
		// no connection has been accepted yet, so db.metrics (a fresh
		// metrics.New() from executor.Open) has recorded nothing to lose.
		// Same legacy/non-hosted db scope as the sources above — under M2
		// multi-database hosting a dbMgr-opened database keeps its own
		// registry and system.metrics reports "not attached" for it, the
		// identical wiring-scope caveat system.tls/config/key_versions carry.
		db.SetMetrics(metrics.Default())
		db.SetMetricsSource(func() *metrics.Registry { return metrics.Default() })
		// backup_dir (M5) — where BACKUP DATABASE writes and system.backups /
		// VERIFY BACKUP read from. Wired only when configured; the closures
		// wrap internal/backup against this node's own data dir + live
		// engine (kept out of the executor package to avoid an import
		// cycle). Same legacy/non-hosted db scope as above.
		if bd := strings.TrimSpace(cfg.BackupDir); bd != "" {
			wireBackupOps(db, bd)
		}
		// system.server_log reads a bounded tail of the same in-memory ring
		// every log line already flows through (logRing wraps the stderr JSON
		// handler). Re-read per query so the tail is always current. Same
		// legacy/non-hosted db scope as the sources above.
		db.SetServerLogSource(logRing.Snapshot)
	}
	if cfg.LockTimeoutMS > 0 && db != nil {
		db.SetLockWaitTimeout(time.Duration(cfg.LockTimeoutMS) * time.Millisecond)
	}
	ctx, stop := serveContext()
	defer stop()
	if db != nil {
		backgroundWait = startDatabaseBackground(ctx, db, cfg, log)
	}
	if auditSigningKeys != nil {
		auditReload := make(chan os.Signal, 1)
		signal.Notify(auditReload, syscall.SIGHUP)
		defer signal.Stop(auditReload)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-auditReload:
					err := auditSigningKeys.Reload()
					recordErr := audit.RecordChecked(security.Event{
						Actor: "system", Action: security.ActionSecuritySet,
						Object: "audit.signing.reload", Outcome: security.Outcome(err),
					})
					if err != nil {
						log.Error("audit signing keyset reload failed; retaining last known-good signer", "error", err)
						continue
					}
					if recordErr != nil {
						log.Error("audit signing reload event could not be persisted", "error", recordErr)
						continue
					}
					log.Info("audit signing keyset reloaded")
				}
			}
		}()
	}
	newTaskRuntime := func(openedDB *executor.DB) (*executor.TaskRuntime, error) {
		runtime, err := executor.StartTaskRuntime(ctx, openedDB, taskPool, executor.TaskRuntimeConfig{
			ACL: acl, Audit: audit, Limits: srv.Limits.Query,
			OnError: func(err error) { log.Error("task runtime", "error", err) },
		})
		if err != nil {
			return nil, err
		}
		return runtime, nil
	}
	if db != nil {
		// One deployment, one database, one task runtime. (Multi-database
		// hosting once routed several databases through a single
		// CentralScheduler here; that feature was removed.)
		runtime, err := newTaskRuntime(db)
		if err != nil {
			return err
		}
		srv.SetTaskRuntime(runtime)
	}
	if env != nil {
		env.OnRevoke(func(crypto.RevokeEvent) {
			n := reg.TerminateAll()
			audit.Record(security.Event{Action: security.ActionSessionKill, Object: "key-revoke", Outcome: "success"})
			log.Info("sessions terminated after key revocation", "count", n)
		})
	}
	if cfg.RequireClientKey {
		var unlockMu sync.Mutex
		srv.Unlock = func(root *crypto.DEK) error {
			unlockMu.Lock()
			defer unlockMu.Unlock()
			if srv.DatabaseHandle() != nil {
				if env != nil {
					return env.VerifyRoot(root)
				}
				return nil
			}
			opened, err := crypto.OpenEnvelope(ksPath, root)
			if err != nil {
				return err
			}
			openedDB, err := executor.OpenWith(dbPath, opened, cfg.BufferPages, storage.OpenOptions{Budget: bufBudget})
			if err != nil {
				_ = opened.Close()
				return err
			}
			if err := validateHostedDatabase(hostingRegistry, hostedDatabase, openedDB); err != nil {
				_ = openedDB.Close()
				_ = opened.Close()
				return err
			}
			applyHostedStorageCap(openedDB, hostedRealm, hostedDatabase)
			var (
				openedCluster  *replication.Cluster
				openedTasks    *executor.TaskRuntime
				waitBackground func()
				stopBackground context.CancelFunc
				published      bool
			)
			defer func() {
				if published {
					return
				}
				if openedTasks != nil {
					_ = openedTasks.Close()
				}
				if openedCluster != nil {
					_ = openedCluster.Shutdown()
				}
				if stopBackground != nil {
					stopBackground()
					waitBackground()
				}
				_ = openedDB.Close()
				_ = opened.Close()
			}()
			opened.OnRevoke(func(crypto.RevokeEvent) {
				n := reg.TerminateAll()
				audit.Record(security.Event{Action: security.ActionSessionKill, Object: "key-revoke", Outcome: "success"})
				log.Info("sessions terminated after key revocation", "count", n)
			})
			applyOps(openedDB, cfg)
			backgroundCtx, cancelBackground := context.WithCancel(ctx)
			stopBackground = cancelBackground
			if err := installArchiver(openedDB, opened, cfg.WalArchive); err != nil {
				return err
			}
			openedCluster, err = startCluster(openedDB, opened, cfg, audit)
			if err != nil {
				return err
			}
			// AttachCluster writes DB cluster state without its own monitor
			// synchronization, so start the complete database background only
			// after that write is complete.
			waitBackground = startDatabaseBackground(backgroundCtx, openedDB, cfg, log)
			openedTasks, err = newTaskRuntime(openedDB)
			if err != nil {
				return err
			}
			env = opened
			db = openedDB
			cluster = openedCluster
			backgroundWait = waitBackground
			srv.SetTaskRuntime(openedTasks)
			srv.SetDatabase(openedDB)
			published = true
			return nil
		}
	}
	var tlsReloader *security.ServerTLSReloader
	if cfg.TLSCert != "" {
		var reloaderOpts []security.ReloaderOption
		if cfg.TLSOCSPMode != "" && cfg.TLSOCSPMode != "disabled" {
			reloaderOpts = append(reloaderOpts, security.WithOCSP(security.OCSPConfig{
				Mode:         security.OCSPMode(cfg.TLSOCSPMode),
				ResponderURL: cfg.TLSOCSPResponder,
			}))
		}
		tlsReloader, err = security.NewServerTLSReloader(cfg.TLSCert, cfg.TLSKey, cfg.TLSClientCA, cfg.TLSClientCRL, reloaderOpts...)
		if err != nil {
			return err
		}
		srv.TLS = tlsReloader.Config()
		srv.RequireServiceIdentity = tlsReloader.MTLS()
		if db != nil {
			db.SetTLSStatusSource(tlsReloader.Status)
		}
	} else if security.RequireTLS(cfg.ListenAddr) {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "TLS 1.3 is required for non-loopback listen addresses")
	}
	if tlsReloader != nil {
		reloadSignals := make(chan os.Signal, 1)
		signal.Notify(reloadSignals, syscall.SIGHUP)
		defer signal.Stop(reloadSignals)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-reloadSignals:
					err := tlsReloader.Reload()
					audit.Record(security.Event{Actor: "system", Action: security.ActionSecuritySet, Object: "tls.reload", Outcome: security.Outcome(err)})
					if err != nil {
						log.Error("TLS reload failed; retaining last known-good configuration", "error", err)
						continue
					}
					if tlsReloader.MTLS() {
						sessions := reg.TerminateAll()
						connections := srv.TerminateConnections()
						audit.Record(security.Event{Actor: "system", Action: security.ActionSessionKill, Object: "mtls-reload", Outcome: "success"})
						log.Info("TLS certificate, trust, and revocation configuration reloaded", "sessions_terminated", sessions, "connections_terminated", connections)
					} else {
						log.Info("TLS certificate configuration reloaded")
					}
				}
			}
		}()
	}

	var tokenVerifier *auth.TokenVerifier
	if cfg.TokenKeyset != "" {
		keyset, err := auth.OpenTokenKeyset(cfg.TokenKeyset)
		if err != nil {
			return err
		}
		var revocations *auth.TokenRevocations
		if cfg.TokenRevocations != "" {
			revocations, err = auth.OpenOrCreateTokenRevocations(cfg.TokenRevocations)
			if err != nil {
				return err
			}
		}
		tokenVerifier = auth.NewTokenVerifier(keyset, revocations, cfg.TokenAudience)
		srv.Tokens = tokenVerifier
		srv.TokenIdentitySourceHints = cfg.TokenIdentitySourceHints
		if !cfg.EmbeddedAuthBrokerEnabled() {
			tokenReload := make(chan os.Signal, 1)
			signal.Notify(tokenReload, syscall.SIGHUP)
			defer signal.Stop(tokenReload)
			go func() {
				for {
					select {
					case <-ctx.Done():
						return
					case <-tokenReload:
						err := tokenVerifier.Reload()
						audit.Record(security.Event{Actor: "system", Action: security.ActionSecuritySet, Object: "token.reload", Outcome: security.Outcome(err)})
						if err != nil {
							log.Error("short-lived credential keyset reload failed; retaining last known-good configuration", "error", err)
							continue
						}
						log.Info("short-lived credential keyset and revocation list reloaded")
					}
				}
			}()
		}
	}

	var embeddedBroker *authbroker.Broker
	var embeddedHTTP *authbroker.HTTPServer
	if cfg.EmbeddedAuthBrokerEnabled() {
		embeddedBroker, embeddedHTTP, err = startEmbeddedAuthBroker(cfg, users, acl, hostingRegistry, authbroker.Options{Logger: log})
		if err != nil {
			return err
		}
		defer embeddedHTTP.Close()
		brokerReload := make(chan os.Signal, 1)
		signal.Notify(brokerReload, syscall.SIGHUP)
		defer signal.Stop(brokerReload)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-brokerReload:
					// Preflight both candidate files, then reload the verifier before
					// publishing the issuer. A newly current broker key is therefore
					// accepted before it can mint.
					verifyKeys, err := auth.OpenTokenKeyset(cfg.TokenKeyset)
					if err == nil {
						err = embeddedBroker.ValidateReloadWithKeysetValidator(func(keys *auth.TokenKeyset) error {
							return verifyEmbeddedBrokerKeyset(keys, auth.NewTokenVerifier(verifyKeys, nil, cfg.TokenAudience), cfg.TokenAudience, time.Now())
						})
					}
					if err != nil {
						log.Error("embedded broker reload preflight failed; retaining last-known-good verifier and issuer", "error", err)
						continue
					}
					err = tokenVerifier.Reload()
					audit.Record(security.Event{Actor: "system", Action: security.ActionSecuritySet, Object: "token.reload", Outcome: security.Outcome(err)})
					if err != nil {
						log.Error("embedded broker reload blocked by short-lived credential reload failure; retaining last-known-good issuer", "error", err)
						continue
					}
					if err := embeddedBroker.ReloadWithKeysetValidator(func(keys *auth.TokenKeyset) error {
						verifyKeys, err := auth.OpenTokenKeyset(cfg.TokenKeyset)
						if err != nil {
							return err
						}
						return verifyEmbeddedBrokerKeyset(keys, auth.NewTokenVerifier(verifyKeys, nil, cfg.TokenAudience), cfg.TokenAudience, time.Now())
					}); err != nil {
						continue
					}
					log.Info("short-lived credential verifier and embedded broker reloaded")
				}
			}
		}()
		log.Info("embedded authentication broker configured",
			"listen", embeddedHTTP.Addr().String(),
			"tls", embeddedHTTP.TLS(),
			"config", cfg.EmbeddedAuthBrokerConfigPath(),
		)
	}

	log.Info("listening",
		"version", version.String,
		"phase", version.Phase,
		"data", dbPath,
		"listen", cfg.ListenAddr,
		"tls", srv.TLS != nil,
		"mtls", srv.RequireServiceIdentity,
		"short_lived_credentials", srv.Tokens != nil,
		"embedded_auth_broker", embeddedHTTP != nil,
		"require_client_key", cfg.RequireClientKey,
		"raft", cfg.RaftBind,
		"node", cfg.NodeID,
		"realm", hostedRealm.Name,
		"database", hostedDatabase.Name,
	)

	protoErr := make(chan error, 1)
	go func() { protoErr <- srv.ListenAndServe(ctx, cfg.ListenAddr) }()
	var embeddedErr chan error
	if embeddedHTTP != nil {
		embeddedErr = make(chan error, 1)
		go func() { embeddedErr <- embeddedHTTP.Serve() }()
	}
	var runErr error
	select {
	case <-ctx.Done():
		// srv.ListenAndServe's own ctx.Done() handler drains (bounded by
		// srv.DrainTimeout) or hard-closes; wait for it to actually finish
		// instead of racing it with the unconditional srv.Close() below.
		runErr = <-protoErr
	case runErr = <-protoErr:
		stop()
	case err := <-embeddedErr:
		runErr = err
		stop()
	}
	if embeddedHTTP != nil {
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		shutdownErr := embeddedHTTP.Shutdown(shutCtx)
		cancel()
		if runErr == nil {
			runErr = shutdownErr
		}
	}
	_ = srv.Close()
	if runErr != nil {
		return runErr
	}
	log.Info("shutting down")
	return nil
}

func startEmbeddedAuthBroker(cfg config.Config, users *auth.Store, acl *security.ACL, hostingRegistry *hosting.Registry, opts authbroker.Options) (*authbroker.Broker, *authbroker.HTTPServer, error) {
	const op = "nextsqld.startEmbeddedAuthBroker"
	brokerCfg, err := authbroker.LoadConfig(cfg.EmbeddedAuthBrokerConfigPath())
	if err != nil {
		return nil, nil, err
	}
	if cfg.AuthBrokerListen != "" {
		brokerCfg.Listen = cfg.AuthBrokerListen
	}
	if cfg.TokenAudience != "" && brokerCfg.DeploymentAudience != cfg.TokenAudience {
		return nil, nil, nerr.New(nerr.InvalidArgument, op, "embedded broker deployment_audience must match token_audience")
	}

	// Prove at startup that the configured broker signing authority is present
	// in nextsqld's verify keyset. This prevents a healthy-looking embedded
	// endpoint from issuing credentials the co-located SQL server cannot verify.
	issuerKeys, err := auth.OpenTokenKeyset(brokerCfg.IssuingKeyset)
	if err != nil {
		return nil, nil, err
	}
	verifyKeys, err := auth.OpenTokenKeyset(cfg.TokenKeyset)
	if err != nil {
		return nil, nil, err
	}
	if err := verifyEmbeddedBrokerKeyset(issuerKeys, auth.NewTokenVerifier(verifyKeys, nil, cfg.TokenAudience), brokerCfg.DeploymentAudience, time.Now()); err != nil {
		return nil, nil, nerr.Wrap(nerr.InvalidArgument, op, "broker issuing key is not accepted by token_verify_keyset", err)
	}

	if users == nil || acl == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, op, "user store and ACL are required")
	}
	opts.RoleMembership = func(realmName, principal string) ([]string, error) {
		var realmID hosting.ID
		if hostingRegistry != nil && realmName != "" {
			realm, err := hostingRegistry.LookupRealm(realmName)
			if err != nil {
				// Unknown realm: no roles, not an error — mirrors the
				// !users.HasInRealm(...) case below.
				return nil, nil
			}
			realmID = realm.ID
		}
		if !users.HasInRealm(realmID, principal) {
			return nil, nil
		}
		return acl.RolesForInRealm(realmID, principal), nil
	}
	if brokerCfg.JITProvisioning {
		maxUsers := brokerCfg.MaxPrincipals
		if maxUsers <= 0 {
			maxUsers = 1000
		}
		opts.UserProvisioner = func(realmName, principal string, roles []string) error {
			var realmID hosting.ID
			if hostingRegistry != nil && realmName != "" {
				realm, err := hostingRegistry.LookupRealm(realmName)
				if err != nil {
					return err
				}
				realmID = realm.ID
			}
			existing := users.SnapshotInRealm(realmID)
			if len(existing) >= maxUsers {
				return nerr.New(nerr.Exhausted, "nextsqld.jit", "maximum principal limit reached")
			}
			var randPass [32]byte
			if _, err := io.ReadFull(rand.Reader, randPass[:]); err != nil {
				return nerr.Wrap(nerr.Internal, "nextsqld.jit", "generate salt", err)
			}
			passHex := hex.EncodeToString(randPass[:])
			if err := users.UpsertInRealm(realmID, principal, passHex); err != nil {
				return err
			}
			if err := acl.AddUserInRealm(realmID, principal); err != nil {
				return err
			}
			for _, r := range roles {
				if err := acl.GrantRoleInRealm(realmID, r, principal); err != nil {
					return err
				}
			}
			return nil
		}
	}
	broker, err := authbroker.New(brokerCfg, opts)
	if err != nil {
		return nil, nil, err
	}
	httpServer, err := authbroker.NewHTTPServer(brokerCfg, broker.Handler())
	if err != nil {
		return nil, nil, err
	}
	return broker, httpServer, nil
}

func verifyEmbeddedBrokerKeyset(issuerKeys *auth.TokenKeyset, verifier *auth.TokenVerifier, audience string, now time.Time) error {
	if issuerKeys == nil || verifier == nil {
		return nerr.New(nerr.InvalidArgument, "nextsqld.verifyEmbeddedBrokerKeyset", "issuer and verifier keysets are required")
	}
	probe, _, _, err := issuerKeys.Mint(auth.TokenMintRequest{
		Principal: "embedded-broker-probe",
		Audience:  audience,
		TTL:       time.Minute,
	}, now)
	if err != nil {
		return err
	}
	if _, err := verifier.Verify(probe); err != nil {
		return err
	}
	return nil
}

func applyDotenvSettings(cfg *config.Config, settings cli.Settings, user, passwordFile, serverPass *string) {
	if cfg == nil {
		return
	}
	if settings.Supplied["data-dir"] {
		cfg.DataDir = settings.DataDir
	}
	if settings.Supplied["key-file"] {
		cfg.KeyFile = settings.KeyFile
	}
	if settings.Supplied["instance-key-file"] {
		cfg.InstanceKeyFile = settings.InstanceKeyFile
	}
	if settings.Supplied["buffer-pages"] {
		cfg.BufferPages = settings.BufferPages
	}
	if settings.Supplied["addr"] {
		cfg.ListenAddr = settings.Addr
	}
	if user != nil {
		if settings.Explicit["user"] {
			*user = settings.User
		} else if settings.Supplied["server-user"] {
			*user = settings.ServerUser
		}
	}
	if passwordFile != nil {
		if settings.Explicit["password-file"] {
			*passwordFile = settings.PasswordFile
		} else if settings.Supplied["server-password-file"] {
			*passwordFile = settings.ServerPassFile
		}
	}
	if serverPass != nil && !settings.Explicit["password-file"] && settings.Supplied["server-pass"] {
		*serverPass = settings.ServerPass
	}
}

func openHostedDefault(cfg config.Config) (*hosting.Registry, hosting.Realm, hosting.Database, error) {
	path := hosting.Path(cfg.DataDir)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		if _, keyErr := os.Stat(hosting.KeyStorePath(path)); keyErr == nil {
			return nil, hosting.Realm{}, hosting.Database{}, nerr.New(nerr.Unavailable, "nextsqld", "deployment registry publication is incomplete")
		} else if !os.IsNotExist(keyErr) {
			return nil, hosting.Realm{}, hosting.Database{}, nerr.Wrap(nerr.IO, "nextsqld", "stat deployment registry keys", keyErr)
		}
		return nil, hosting.Realm{}, hosting.Database{}, nil
	} else if err != nil {
		return nil, hosting.Realm{}, hosting.Database{}, nerr.Wrap(nerr.IO, "nextsqld", "stat deployment registry", err)
	}
	keyFile := cfg.InstanceRootFile()
	if keyFile == "" {
		return nil, hosting.Realm{}, hosting.Database{}, nerr.New(nerr.InvalidArgument, "nextsqld", "--instance-key-file is required for an initialized deployment registry")
	}
	root, err := crypto.ReadKeyFile(keyFile)
	if err != nil {
		return nil, hosting.Realm{}, hosting.Database{}, err
	}
	defer root.Zero()
	registry, err := hosting.Open(path, root)
	if err != nil {
		return nil, hosting.Realm{}, hosting.Database{}, err
	}
	realm, database, err := registry.Default()
	if err != nil {
		_ = registry.Close()
		return nil, hosting.Realm{}, hosting.Database{}, err
	}
	if realm.State != hosting.StateActive || database.State != hosting.StateActive {
		_ = registry.Close()
		return nil, hosting.Realm{}, hosting.Database{}, nerr.New(nerr.Unavailable, "nextsqld", "default realm/database is not active")
	}
	// Multi-realm/multi-database hosting was removed: a deployment serves
	// exactly the one database at DATA-DIR/nextsql.db. A registry written by
	// a release that could hold more than that is refused here rather than
	// served with its other databases silently unreachable — the data is
	// still on disk, and 0.0.1 can still read it to export.
	if err := requireSingleDatabaseDeployment(registry, realm, database); err != nil {
		_ = registry.Close()
		return nil, hosting.Realm{}, hosting.Database{}, err
	}
	return registry, realm, database, nil
}

// requireInitializedDatabase reports whether this data directory holds a
// database at all. `nextsql init` without `--database` provisions the
// deployment only — root key, and the administrator in the deployment auth
// store — so the data directory legitimately exists, and is legitimately
// unservable, until a database is named. Distinguishing that from "wrong
// --data-dir" is the whole point: both would otherwise surface as a missing
// file deep inside the storage layer.
func requireInitializedDatabase(cfg config.Config, dbPath string, registry *hosting.Registry) error {
	if _, err := os.Stat(dbPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return nerr.Wrap(nerr.IO, "nextsqld", "stat database", err)
	}
	if registry != nil {
		// A registry without its database is damage, not a pending install:
		// the registry is only ever written once the database exists.
		return nerr.New(nerr.Corruption, "nextsqld",
			"deployment registry exists but "+dbPath+" is missing; restore the data directory or recover from a backup")
	}
	keyFile := cfg.KeyFile
	if keyFile == "" {
		keyFile = "KEY-FILE"
	}
	return nerr.New(nerr.Unavailable, "nextsqld",
		"this deployment has no database yet (no "+dbPath+"): create one with `nextsql init --data-dir "+
			cfg.DataDir+" --key-file "+keyFile+" --database NAME`")
}

// requireSingleDatabaseDeployment fails closed on a registry that describes
// anything this build can no longer serve: more than one realm, more than one
// database in that realm, or a default database that does not live at
// DATA-DIR/nextsql.db (the managed layout a declarative multi-realm bootstrap
// used to produce). Refusing to start is deliberate — the alternative is a
// server that comes up looking healthy while some of the operator's databases
// have quietly become unreachable.
func requireSingleDatabaseDeployment(registry *hosting.Registry, realm hosting.Realm, database hosting.Database) error {
	return requireSingleDatabaseDeploymentManifest(registry.Manifest(), realm, database)
}

// requireSingleDatabaseDeploymentManifest is the decision itself, split out
// so it can be exercised against manifests this build can no longer write.
func requireSingleDatabaseDeploymentManifest(m hosting.Manifest, realm hosting.Realm, database hosting.Database) error {
	const op = "nextsqld"
	const remedy = " Multi-realm/multi-database hosting was removed; run one deployment per database, " +
		"and use NextSQL 0.0.1 to export anything this deployment still holds."
	if len(m.Realms) != 1 {
		return nerr.New(nerr.Unavailable, op,
			"deployment registry describes "+strconv.Itoa(len(m.Realms))+" realms."+remedy)
	}
	if len(m.Realms[0].Databases) != 1 {
		return nerr.New(nerr.Unavailable, op,
			"deployment registry describes "+strconv.Itoa(len(m.Realms[0].Databases))+" databases in realm "+
				realm.Name+"."+remedy)
	}
	if database.Layout != hosting.LayoutLegacyDefault {
		return nerr.New(nerr.Unavailable, op,
			"deployment registry's database "+database.Name+" is not the deployment's own DATA-DIR/nextsql.db."+remedy)
	}
	return nil
}

// applyHostedStorageCap applies the realm/database data-file growth cap from the
// deployment registry to the open database. The registry cannot change while
// nextsqld holds the data-directory lock, so this runs once at open time; a cap
// edit takes effect on the next restart.
func applyHostedStorageCap(db *executor.DB, realm hosting.Realm, database hosting.Database) {
	if db == nil {
		return
	}
	db.SetStorageCapBytes(hosting.EffectiveStorageCapBytes(realm.StorageCapBytes, database.StorageCapBytes))
}

func validateHostedDatabase(registry *hosting.Registry, expected hosting.Database, db *executor.DB) error {
	if registry == nil {
		return nil
	}
	if db == nil || db.Eng == nil {
		return nerr.New(nerr.Unavailable, "nextsqld", "default database is not open")
	}
	if db.Eng.Identity() != expected.Identity {
		return nerr.New(nerr.Corruption, "nextsqld", "default database identity does not match deployment registry")
	}
	return nil
}

// msDuration converts a configured millisecond value to a duration, leaving a
// zero as zero so replication.Timings resolves it to its built-in default.
func msDuration(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	return time.Duration(ms) * time.Millisecond
}

func startCluster(db *executor.DB, keys crypto.KeyProvider, cfg config.Config, audit *security.Log) (*replication.Cluster, error) {
	if cfg.RaftBind == "" {
		return nil, nil
	}
	if db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "nextsqld", "database must be open before starting Raft")
	}
	peers, err := replication.ParsePeers(cfg.RaftJoin)
	if err != nil {
		return nil, err
	}
	if len(peers) < replication.MinVotingNodes {
		return nil, nerr.New(nerr.InvalidArgument, "nextsqld", "HA requires at least 3 voting nodes in --raft-join")
	}
	cl, err := replication.Open(replication.Config{
		NodeID:    cfg.NodeID,
		Bind:      cfg.RaftBind,
		Dir:       filepath.Join(cfg.DataDir, "raft"),
		Peers:     peers,
		Bootstrap: cfg.RaftBootstrap,
		Keys:      replication.ReplKeys(keys),
		Timings: replication.Timings{
			Heartbeat:     msDuration(cfg.RaftHeartbeatMS),
			Election:      msDuration(cfg.RaftElectionMS),
			LeaderLease:   msDuration(cfg.RaftLeaderLeaseMS),
			CommitTimeout: msDuration(cfg.RaftCommitTimeoutMS),
		},
	}, db)
	if err != nil {
		return nil, err
	}
	db.AttachCluster(cl)
	_ = cl.WriteStatus(cfg.DataDir)
	if cfg.RaftBootstrap {
		go func() {
			_ = cl.JoinPeers(peers)
			_ = cl.WriteStatus(cfg.DataDir)
		}()
	}
	audit.Record(security.Event{Action: security.ActionMembership, Object: cfg.NodeID, Outcome: "start"})
	return cl, nil
}

func applyOps(db *executor.DB, cfg config.Config) {
	if db == nil {
		return
	}
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{
		MaxInflight: cfg.MaxInflight,
		MaxQueue:    cfg.MaxQueryQueue,
		QueueWait:   time.Duration(cfg.QueueWaitMS) * time.Millisecond,
	}))
}

func installArchiver(db *executor.DB, keys crypto.KeyProvider, dir string) error {
	if db == nil || dir == "" || keys == nil {
		return nil
	}
	arch, err := backup.NewDirArchiver(dir, keys)
	if err != nil {
		return err
	}
	db.Eng.SetArchiver(arch)
	return nil
}

// startDatabaseBackground starts all per-database periodic work and returns
// a wait function. Call it after canceling ctx and before closing db: a
// canceled context alone cannot prevent a ticker callback already in progress
// from using a handle that is about to be closed.
func startDatabaseBackground(ctx context.Context, db *executor.DB, cfg config.Config, log *slog.Logger) func() {
	waits := []func(){
		startCheckpointController(ctx, db, cfg.CheckpointIntervalMS, log),
		startWALRetentionUpdater(ctx, db, cfg.WalArchive, cfg.WalRetentionMS, log),
	}
	if cfg.DiskWatermarkCheckMS > 0 {
		warn, reject := cfg.DiskWatermarkThresholds()
		waits = append(waits, startDiskWatermarkMonitor(ctx, db, cfg.DataDir, cfg.DiskWatermarkCheckMS, warn, reject, log))
	}
	if cfg.ReplicaLagCheckMS > 0 {
		waits = append(waits, startReplicaLagMonitor(ctx, db, cfg.ReplicaLagCheckMS, cfg.ReplicaLagWarnThreshold(), log))
	}
	return func() {
		for _, wait := range waits {
			wait()
		}
	}
}

// startCheckpointController periodically installs a durable recovery
// boundary for one open database. Checkpoint itself flushes committed pages,
// writes the checkpoint record/control file, and preserves WAL history for
// PITR and page repair; this controller never prunes WAL. It intentionally
// does not checkpoint immediately after open: recovery has already made the
// database consistent, and an idle open should not create housekeeping WAL.
//
// A ticker invokes tick serially, so one database has at most one scheduled
// checkpoint in flight. Shutdown cancels the loop before DB.Close performs
// its final checkpoint, avoiding a close/checkpoint race.
func startCheckpointController(ctx context.Context, db *executor.DB, intervalMS int, log *slog.Logger) func() {
	if db == nil || intervalMS <= 0 {
		return func() {}
	}
	interval := time.Duration(intervalMS) * time.Millisecond
	done := make(chan struct{})
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := db.Eng.Checkpoint(); err != nil {
					// Do not turn a checkpoint failure into an unsafe success or
					// stop serving a database that remains durable through WAL.
					// The next bounded tick retries, while the error remains
					// operator-visible.
					log.Warn("checkpoint failed; WAL recovery window remains unbounded until a later checkpoint succeeds", "error", err)
				}
			}
		}
	}()
	return func() { <-done }
}

// walRetentionTick advances db's WAL pruning horizon to the newest archived
// segment's LSN at or before now-retention, so a later MAINTAIN DATABASE can
// prune local WAL history the policy no longer requires. It never prunes
// anything itself — see docs/wal.md "Retention". A zero-value backup.Header
// deliberately excludes any specific base backup from the resolution: this
// is a live retention policy, not a restore-point lookup, so only archived
// segments (never a backup that predates the archive) should ever raise the
// horizon. Returns false, nil (not an error) when nothing has been archived
// far enough back yet — there is simply nothing to advance to.
func walRetentionTick(db *executor.DB, archiveDir string, retention time.Duration, now time.Time) (bool, error) {
	horizon, err := backup.ResolveUntilTime(backup.Header{}, archiveDir, now.Add(-retention))
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return false, nil
		}
		return false, err
	}
	db.SetWALRetentionHorizon(horizon)
	return true, nil
}

// startWALRetentionUpdater periodically calls walRetentionTick until ctx is
// canceled. A no-op unless both retentionMS and archiveDir are set: pruning
// without an archiver would destroy the only copy of that WAL history, so
// there is nothing safe to advance the horizon toward without one. The
// check interval scales with the policy window (1/24th of it, clamped to
// [1m, 1h]) so a short test-oriented retention window still gets
// reasonably fine-grained updates without a long real-world window ticking
// needlessly often.
func startWALRetentionUpdater(ctx context.Context, db *executor.DB, archiveDir string, retentionMS int, log *slog.Logger) func() {
	if db == nil || archiveDir == "" || retentionMS <= 0 {
		return func() {}
	}
	retention := time.Duration(retentionMS) * time.Millisecond
	interval := retention / 24
	if interval < time.Minute {
		interval = time.Minute
	}
	if interval > time.Hour {
		interval = time.Hour
	}
	tick := func() {
		if _, err := walRetentionTick(db, archiveDir, retention, time.Now()); err != nil {
			log.Warn("wal retention: horizon update failed", "error", err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick() // apply once immediately rather than waiting a full interval
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick()
			}
		}
	}()
	return func() { <-done }
}

// diskWatermarkTick reads free space on the volume holding dataDir and
// applies the warn/reject hysteresis: db.SetDiskWatermarkTripped is set once
// usage reaches reject%, and only cleared once usage drops back below warn%
// (not merely below reject%), so a node hovering right at reject% doesn't
// flap between accepting and rejecting writes. Logging and the metrics
// counters are edge-triggered — once per state transition, not once per
// tick — so a steady-state warn/reject condition doesn't spam the log.
func diskWatermarkTick(db *executor.DB, dataDir string, warnPercent, rejectPercent float64, log *slog.Logger) error {
	u, err := diskspace.Stat(dataDir)
	if err != nil {
		return err
	}
	metrics.Default().SetDiskUsage(u.TotalBytes, u.FreeBytes)
	usedPercent := u.UsedFraction() * 100
	wasTripped := db.DiskWatermarkTripped()
	switch {
	case !wasTripped && usedPercent >= rejectPercent:
		db.SetDiskWatermarkTripped(true)
		metrics.Default().AddDiskWatermarkReject()
		log.Warn("disk watermark: reject threshold reached; rejecting new writes",
			"used_percent", usedPercent, "reject_percent", rejectPercent, "free_bytes", u.FreeBytes)
	case wasTripped && usedPercent < warnPercent:
		db.SetDiskWatermarkTripped(false)
		log.Info("disk watermark: usage recovered below warn threshold; writes re-enabled",
			"used_percent", usedPercent, "warn_percent", warnPercent, "free_bytes", u.FreeBytes)
	case !wasTripped && usedPercent >= warnPercent:
		metrics.Default().AddDiskWatermarkWarn()
		log.Warn("disk watermark: warn threshold reached", "used_percent", usedPercent, "warn_percent", warnPercent, "free_bytes", u.FreeBytes)
	}
	return nil
}

// startDiskWatermarkMonitor periodically calls diskWatermarkTick until ctx is
// canceled. A no-op unless checkMS > 0 (the feature defaults off).
func startDiskWatermarkMonitor(ctx context.Context, db *executor.DB, dataDir string, checkMS int, warnPercent, rejectPercent float64, log *slog.Logger) func() {
	if db == nil || dataDir == "" || checkMS <= 0 {
		return func() {}
	}
	interval := time.Duration(checkMS) * time.Millisecond
	tick := func() {
		if err := diskWatermarkTick(db, dataDir, warnPercent, rejectPercent, log); err != nil {
			log.Warn("disk watermark: check failed", "error", err)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick() // apply once immediately rather than waiting a full interval
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick()
			}
		}
	}()
	return func() { <-done }
}

// replicaLagTick reads this node's current replication apply backlog
// (replication.ReplicaHealth.ApplyBacklog — entries known committed but not
// yet applied locally, the same figure system.replica_health exposes) and
// records it as a gauge. attached is false on a single-node deployment (no
// cluster attached), in which case backlog is meaningless and left 0 —
// there is nothing to monitor.
func replicaLagTick(db *executor.DB) (backlog uint64, attached bool) {
	h, ok := db.ClusterHealth()
	if !ok {
		return 0, false
	}
	metrics.Default().SetReplicaApplyBacklog(h.ApplyBacklog)
	return h.ApplyBacklog, true
}

// replicaLagEdge computes the next warned state and whether to log a
// warning or a recovery line, given the previous state and the current
// apply backlog. Pure and side-effect free (no metrics/logging calls) so
// the warn/recover transition logic is unit-testable without a live Raft
// cluster. Edge-triggered like the disk-watermark warn line — a
// steady-state warn condition must not spam the log every tick — but with
// a single threshold, not hysteresis: unlike the disk watermark's
// warn/reject pair, nothing here gates write admission, so there is no
// flapping-state risk to guard against with an asymmetric clear line.
func replicaLagEdge(wasWarned bool, backlog, warnEntries uint64) (nowWarned, logWarn, logRecover bool) {
	switch {
	case !wasWarned && backlog >= warnEntries:
		return true, true, false
	case wasWarned && backlog < warnEntries:
		return false, false, true
	default:
		return wasWarned, false, false
	}
}

// startReplicaLagMonitor periodically calls replicaLagTick until ctx is
// canceled, logging (and counting via metrics.AddReplicaLagWarn) an
// edge-triggered warning when this node's apply backlog reaches
// warnEntries, and a recovery line when it drops back below. A no-op
// unless checkMS > 0 (the feature defaults off); also effectively idle on
// a single-node deployment, where replicaLagTick reports attached=false.
func startReplicaLagMonitor(ctx context.Context, db *executor.DB, checkMS int, warnEntries uint64, log *slog.Logger) func() {
	if db == nil || checkMS <= 0 {
		return func() {}
	}
	interval := time.Duration(checkMS) * time.Millisecond
	warned := false
	tick := func() {
		backlog, attached := replicaLagTick(db)
		if !attached {
			return
		}
		var logWarn, logRecover bool
		warned, logWarn, logRecover = replicaLagEdge(warned, backlog, warnEntries)
		switch {
		case logWarn:
			metrics.Default().AddReplicaLagWarn()
			log.Warn("replica lag: apply backlog reached warn threshold", "apply_backlog", backlog, "warn_entries", warnEntries)
		case logRecover:
			log.Info("replica lag: apply backlog recovered below warn threshold", "apply_backlog", backlog, "warn_entries", warnEntries)
		}
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick() // apply once immediately rather than waiting a full interval
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				tick()
			}
		}
	}()
	return func() { <-done }
}

func openKeys(keyFile, keystore string) (crypto.KeyProvider, *crypto.Envelope, error) {
	root, err := crypto.ReadKeyFile(keyFile)
	if err != nil {
		return nil, nil, err
	}
	if _, err := os.Stat(keystore); err == nil {
		env, err := crypto.OpenEnvelope(keystore, root)
		if err != nil {
			return nil, nil, err
		}
		return env, env, nil
	}
	keys, err := crypto.NewMemoryKeyProvider(root)
	if err != nil {
		return nil, nil, err
	}
	return keys, nil, nil
}
