package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/security"
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
	user := fs.String("user", "", "bootstrap or update this user")
	passwordFile := fs.String("password-file", "", "password file for --user (never a URL)")
	requireClientKey := fs.Bool("require-client-key", false, "do not load --key-file; first client must unlock")
	auditFile := fs.String("audit-file", "", "audit log path (default: DATA-DIR/nextsql.audit)")
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
	if set["require-client-key"] {
		cfg.RequireClientKey = *requireClientKey
	}
	if set["audit-file"] {
		cfg.AuditFile = *auditFile
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
	if !cfg.RequireClientKey && cfg.KeyFile == "" {
		return nerr.New(nerr.InvalidArgument, "nextsqld", "--key-file is required unless --require-client-key is set")
	}
	dataDirLock, err := hosting.AcquireDataDirLock(cfg.DataDir)
	if err != nil {
		return err
	}
	defer dataDirLock.Close()

	log := logging.New(cfg.LogLevel, os.Stderr)
	dbPath := filepath.Join(cfg.DataDir, config.DataFileName)
	ksPath := crypto.KeystorePath(dbPath)

	var (
		db              *executor.DB
		env             *crypto.Envelope
		keys            crypto.KeyProvider
		cluster         *replication.Cluster
		hostingRegistry *hosting.Registry
		hostedRealm     hosting.Realm
		hostedDatabase  hosting.Database
	)
	defer func() {
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
	if !cfg.RequireClientKey {
		var opened *crypto.Envelope
		keys, opened, err = openKeys(cfg.KeyFile, ksPath)
		if err != nil {
			return err
		}
		env = opened
		db, err = executor.Open(dbPath, keys, cfg.BufferPages)
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
	if hostingRegistry != nil {
		srv.Database = hostedDatabase.Name
	}
	if cfg.MaxResultRows > 0 {
		lim := srv.Limits
		lim.Query.ResultRows = cfg.MaxResultRows
		srv.Limits = lim
	}
	ctx, stop := serveContext()
	defer stop()
	newTaskRuntime := func(openedDB *executor.DB) (*executor.TaskRuntime, error) {
		runtime, err := executor.StartTaskRuntime(ctx, openedDB, executor.TaskRuntimeConfig{
			ACL: acl, Audit: audit, Limits: srv.Limits.Query,
			OnError: func(err error) { log.Error("task runtime", "error", err) },
		})
		if err != nil {
			return nil, err
		}
		return runtime, nil
	}
	if db != nil {
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
			openedDB, err := executor.Open(dbPath, opened, cfg.BufferPages)
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
			openedCluster, err = startCluster(openedDB, opened, cfg, audit)
			if err != nil {
				return err
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
		verifier := auth.NewTokenVerifier(keyset, revocations, cfg.TokenAudience)
		srv.Tokens = verifier
		tokenReload := make(chan os.Signal, 1)
		signal.Notify(tokenReload, syscall.SIGHUP)
		defer signal.Stop(tokenReload)
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case <-tokenReload:
					err := verifier.Reload()
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

	log.Info("listening",
		"version", version.String,
		"phase", version.Phase,
		"data", dbPath,
		"listen", cfg.ListenAddr,
		"tls", srv.TLS != nil,
		"mtls", srv.RequireServiceIdentity,
		"short_lived_credentials", srv.Tokens != nil,
		"require_client_key", cfg.RequireClientKey,
		"raft", cfg.RaftBind,
		"node", cfg.NodeID,
		"realm", hostedRealm.Name,
		"database", hostedDatabase.Name,
	)

	if err := srv.ListenAndServe(ctx, cfg.ListenAddr); err != nil {
		return err
	}
	log.Info("shutting down")
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
	if database.Layout != hosting.LayoutLegacyDefault {
		_ = registry.Close()
		return nil, hosting.Realm{}, hosting.Database{}, nerr.New(nerr.Unavailable, "nextsqld", "default database layout is not supported by the single-database runtime")
	}
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
