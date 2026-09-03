package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/authbroker"
	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/dbmanager"
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
	if err := cfg.Validate(); err != nil {
		return err
	}
	if cfg.DataDir == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--data-dir is required (or set it in --config)")
	}
	if !cfg.RequireClientKey && cfg.KeyFile == "" && cfg.InstanceKeyFile == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--key-file (or, for a manifest-bootstrapped deployment, --instance-key-file) is required unless --require-client-key is set")
	}
	dataDirLock, err := hosting.AcquireDataDirLock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer dataDirLock.Close()

	log := logging.New(cfg.LogLevel, os.Stderr)
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
		dbMgr           *dbmanager.Manager
		secondaryMu     sync.Mutex
		secondaryEnvs   []*crypto.Envelope
	)
	defer func() {
		if dbMgr != nil {
			_ = dbMgr.Close()
		}
		secondaryMu.Lock()
		for _, e := range secondaryEnvs {
			_ = e.Close()
		}
		secondaryMu.Unlock()
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
	// The primary database is eager-opened at DATA-DIR/nextsql.db only for a
	// legacy single-database deployment (no registry) or a registry whose
	// default is LayoutLegacyDefault. A manifest-bootstrapped deployment whose
	// default is LayoutManaged starts with no primary handle: dbMgr (set up
	// below, since hostingRegistry != nil) opens and serves it lazily on the
	// first connection, exactly as it already does every non-default managed
	// database. RequireClientKey keeps its own deferred-open path regardless.
	eagerPrimary := hostingRegistry == nil || hostedDatabase.Layout == hosting.LayoutLegacyDefault
	if cfg.RequireClientKey && !eagerPrimary {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "require_client_key is not supported with a manifest-bootstrapped (managed-layout default) deployment")
	}
	if !cfg.RequireClientKey && eagerPrimary && cfg.KeyFile == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--key-file is required for this deployment (its default database is a legacy DATA-DIR/nextsql.db)")
	}
	if !cfg.RequireClientKey && eagerPrimary {
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
	if cfg.MaxResultRows > 0 || cfg.MaxConnections > 0 || cfg.MaxConnectionsPerUser > 0 || cfg.MaxConnectionsPerDatabase > 0 || cfg.MaxConnectionsPerRealm > 0 || cfg.IdleTimeoutMS > 0 || cfg.StatementTimeoutMS > 0 || cfg.TransactionTimeoutMS > 0 || cfg.IdleTransactionTimeoutMS > 0 {
		lim := srv.Limits
		if cfg.MaxResultRows > 0 {
			lim.Query.ResultRows = cfg.MaxResultRows
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
	}
	if cfg.LockTimeoutMS > 0 && db != nil {
		db.SetLockWaitTimeout(time.Duration(cfg.LockTimeoutMS) * time.Millisecond)
	}
	ctx, stop := serveContext()
	defer stop()
	if db != nil {
		startWALRetentionUpdater(ctx, db, cfg.WalArchive, cfg.WalRetentionMS, log)
		if cfg.DiskWatermarkCheckMS > 0 {
			warn, reject := cfg.DiskWatermarkThresholds()
			startDiskWatermarkMonitor(ctx, db, cfg.DataDir, cfg.DiskWatermarkCheckMS, warn, reject, log)
		}
		if cfg.ReplicaLagCheckMS > 0 {
			startReplicaLagMonitor(ctx, db, cfg.ReplicaLagCheckMS, cfg.ReplicaLagWarnThreshold(), log)
		}
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
	if db != nil && hostingRegistry == nil {
		// Only when there is no hosting registry at all: once one exists,
		// the primary is scheduled by the single CentralScheduler set up
		// below instead (M2-3b-3b), the same as every dbmanager-opened
		// secondary — not its own dedicated TaskRuntime.
		runtime, err := newTaskRuntime(db)
		if err != nil {
			return err
		}
		srv.SetTaskRuntime(runtime)
	}
	// dbMgr (M2-3a) lets a connection's Hello.Realm/Hello.Database route to
	// a database other than the primary one, bounded to a small fixed
	// number of distinct open databases with no eviction (M2-3b territory).
	// Only meaningful with a hosting registry to look additional databases
	// up in; a legacy/non-hosted deployment leaves srv.Databases nil, so
	// every connection uses the pre-M2-3a DatabaseHandle() path unchanged.
	if hostingRegistry != nil {
		opener := func(realm hosting.Realm, database hosting.Database) (*executor.DB, func() error, error) {
			if database.Layout != hosting.LayoutManaged {
				return nil, nil, nerr.New(nerr.Unavailable, "nextsqld", "only managed-layout databases can be opened on demand")
			}
			if realm.State != hosting.StateActive || database.State != hosting.StateActive {
				return nil, nil, nerr.New(nerr.Unavailable, "nextsqld", "realm or database is not active")
			}
			// database.KeyRef is the standalone root key file for this managed
			// database (nextsql database create's own --database-key-file),
			// distinct from the deployment's --key-file. Mirrors the primary
			// database's own open path (openKeys): the root key does not
			// encrypt the database file directly, it unlocks an envelope
			// keystore (crypto.KeystorePath) placed next to the database
			// file, which activateManagedDatabase/createOrResumeDatabase
			// already created at provisioning time.
			secRoot, err := crypto.ReadKeyFile(database.KeyRef)
			if err != nil {
				return nil, nil, err
			}
			secPath := hosting.ManagedDatabasePath(cfg.DataDir, realm.ID, database.ID)
			secEnv, err := crypto.OpenEnvelope(crypto.KeystorePath(secPath), secRoot)
			if err != nil {
				return nil, nil, err
			}
			secDB, err := executor.OpenWith(secPath, secEnv, cfg.BufferPages, storage.OpenOptions{Budget: bufBudget})
			if err != nil {
				_ = secEnv.Close()
				return nil, nil, err
			}
			if err := validateHostedDatabase(hostingRegistry, database, secDB); err != nil {
				_ = secDB.Close()
				_ = secEnv.Close()
				return nil, nil, err
			}
			secDB.SetDatabaseName(database.Name)
			applyHostedStorageCap(secDB, realm, database)
			applyOps(secDB, cfg)
			// No installArchiver, no startCluster: secondary databases are
			// single-node only in M2-3a (no PITR archiving, no Raft — running
			// multiple independent Raft groups in one process is out of
			// scope). Not a regression: nextsqld opened nothing beyond the
			// primary at all before M2-3a.
			startWALRetentionUpdater(ctx, secDB, cfg.WalArchive, cfg.WalRetentionMS, log)
			if cfg.DiskWatermarkCheckMS > 0 {
				warn, reject := cfg.DiskWatermarkThresholds()
				startDiskWatermarkMonitor(ctx, secDB, cfg.DataDir, cfg.DiskWatermarkCheckMS, warn, reject, log)
			}
			if cfg.ReplicaLagCheckMS > 0 {
				startReplicaLagMonitor(ctx, secDB, cfg.ReplicaLagCheckMS, cfg.ReplicaLagWarnThreshold(), log)
			}
			secondaryMu.Lock()
			secondaryEnvs = append(secondaryEnvs, secEnv)
			secondaryMu.Unlock()
			// No task runtime to close here any more (M2-3b-3b): the single
			// CentralScheduler set up below covers every dbmanager-open
			// database, primary and secondary alike, and its own Close
			// (deferred right after it's started, below) already
			// guarantees no claim it submitted against secDB is still
			// in flight by the time Manager.release calls this cleanup —
			// see CentralScheduler.Close's doc comment. The envelope closes
			// last, since the database's own final checkpoint/flush needs
			// its key material still available.
			cleanup := func() error {
				dbErr := secDB.Close()
				_ = secEnv.Close()
				return dbErr
			}
			return secDB, cleanup, nil
		}
		dbMgr = dbmanager.New(cfg.MaxOpenDatabases, hostingRegistry.Lookup, opener)
		if db != nil {
			if err := dbMgr.Preload(hostedRealm, hostedDatabase, db); err != nil {
				return err
			}
		}
		srv.SetDatabaseManager(dbMgr)
		// CentralScheduler (M2-3b-3b) is the single poll loop covering every
		// database dbMgr currently has open — primary and every secondary —
		// instead of each getting its own TaskRuntime. dbMgr.Snapshot's
		// ref-holding is what makes this safe against M2-3b-1 eviction: a
		// database with a claim still in flight can't be evicted mid-tick
		// (see Snapshot's own doc comment), so the Opener's cleanup above
		// needs no task-runtime-specific ordering of its own any more.
		// Deliberately scoped out: the REQUIRE CLIENT KEY lazy-open path's
		// own primary-only TaskRuntime (below, in srv.Unlock) is untouched —
		// combining REQUIRE CLIENT KEY with hosting is a narrow, rare
		// deployment shape, and once that primary is later Preloaded into
		// dbMgr there, it becomes redundantly (but not incorrectly — claims
		// are transactionally exclusive) polled by both. Not attempted here;
		// flagged rather than silently left.
		centralSched, err := executor.StartCentralScheduler(ctx, taskPool, func() []executor.DBRef {
			handles := dbMgr.Snapshot()
			refs := make([]executor.DBRef, len(handles))
			for i, h := range handles {
				refs[i] = executor.DBRef{DB: h.DB, Release: h.Release}
			}
			return refs
		}, executor.TaskRuntimeConfig{
			ACL: acl, Audit: audit, Limits: srv.Limits.Query,
			OnError: func(err error) { log.Error("task scheduler", "error", err) },
		})
		if err != nil {
			return err
		}
		// Registered here — after dbMgr exists but before the earlier master
		// dbMgr/secondary-cleanup defer runs (defers are LIFO: this one,
		// registered later, runs first) — so CentralScheduler always closes,
		// draining every in-flight claim, before dbMgr force-closes any
		// database out from under it during final shutdown.
		defer func() { _ = centralSched.Close() }()
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
				openedCluster *replication.Cluster
				openedTasks   *executor.TaskRuntime
				published     bool
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
				_ = openedDB.Close()
				_ = opened.Close()
			}()
			opened.OnRevoke(func(crypto.RevokeEvent) {
				n := reg.TerminateAll()
				audit.Record(security.Event{Action: security.ActionSessionKill, Object: "key-revoke", Outcome: "success"})
				log.Info("sessions terminated after key revocation", "count", n)
			})
			applyOps(openedDB, cfg)
			if err := installArchiver(openedDB, opened, cfg.WalArchive); err != nil {
				return err
			}
			startWALRetentionUpdater(ctx, openedDB, cfg.WalArchive, cfg.WalRetentionMS, log)
			if cfg.DiskWatermarkCheckMS > 0 {
				warn, reject := cfg.DiskWatermarkThresholds()
				startDiskWatermarkMonitor(ctx, openedDB, cfg.DataDir, cfg.DiskWatermarkCheckMS, warn, reject, log)
			}
			openedCluster, err = startCluster(openedDB, opened, cfg, audit)
			if err != nil {
				return err
			}
			// Started only after startCluster returns, unlike the WAL
			// retention/disk watermark monitors above: this one reads
			// DB.ClusterHealth (DB.gate), which AttachCluster sets with no
			// synchronization of its own — starting it any earlier would
			// race that write from this goroutine against the monitor's own
			// background goroutine.
			if cfg.ReplicaLagCheckMS > 0 {
				startReplicaLagMonitor(ctx, openedDB, cfg.ReplicaLagCheckMS, cfg.ReplicaLagWarnThreshold(), log)
			}
			openedTasks, err = newTaskRuntime(openedDB)
			if err != nil {
				return err
			}
			env = opened
			db = openedDB
			cluster = openedCluster
			srv.SetTaskRuntime(openedTasks)
			srv.SetDatabase(openedDB)
			if dbMgr != nil {
				if err := dbMgr.Preload(hostedRealm, hostedDatabase, openedDB); err != nil {
					return err
				}
			}
			published = true
			return nil
		}
	}
	var tlsReloader *security.ServerTLSReloader
	if cfg.TLSCert != "" {
		tlsReloader, err = security.NewServerTLSReloader(cfg.TLSCert, cfg.TLSKey, cfg.TLSClientCA, cfg.TLSClientCRL)
		if err != nil {
			return err
		}
		srv.TLS = tlsReloader.Config()
		srv.RequireServiceIdentity = tlsReloader.MTLS()
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
	// A LayoutLegacyDefault default is eager-opened below (DATA-DIR/nextsql.db
	// under --key-file). A LayoutManaged default — produced by a declarative
	// manifest bootstrap, where every database including the default has its
	// own key file — is served lazily through dbmanager exactly like every
	// other managed database (M2-5), so the eager open is skipped and the
	// process starts with no primary DB handle at all. Both are valid; the
	// registry decode already rejected any other layout value.
	return registry, realm, database, nil
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
func startWALRetentionUpdater(ctx context.Context, db *executor.DB, archiveDir string, retentionMS int, log *slog.Logger) {
	if db == nil || archiveDir == "" || retentionMS <= 0 {
		return
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
	go func() {
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
func startDiskWatermarkMonitor(ctx context.Context, db *executor.DB, dataDir string, checkMS int, warnPercent, rejectPercent float64, log *slog.Logger) {
	if db == nil || dataDir == "" || checkMS <= 0 {
		return
	}
	interval := time.Duration(checkMS) * time.Millisecond
	tick := func() {
		if err := diskWatermarkTick(db, dataDir, warnPercent, rejectPercent, log); err != nil {
			log.Warn("disk watermark: check failed", "error", err)
		}
	}
	go func() {
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
func startReplicaLagMonitor(ctx context.Context, db *executor.DB, checkMS int, warnEntries uint64, log *slog.Logger) {
	if db == nil || checkMS <= 0 {
		return
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
	go func() {
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
