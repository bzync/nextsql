package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/migrate"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/upgrade"
	"github.com/bzync/nextsql/internal/version"
	"github.com/bzync/nextsql/internal/xport"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "nextsql: %v\n", err)
		os.Exit(cli.Code(err))
	}
}

func run(args []string) error {
	if len(args) == 0 {
		printUsage(os.Stdout)
		return nil
	}
	switch args[0] {
	case "version", "-version", "--version":
		fmt.Printf("nextsql %s (phase %d)\n", version.String, version.Phase)
		return nil
	case "help", "-h", "--help":
		printUsage(os.Stdout)
		return nil
	case "init":
		return initDB(args[1:])
	case "exec":
		return execSQL(args[1:])
	case "backup":
		return backupDB(args[1:])
	case "restore":
		return restoreDB(args[1:])
	case "verify":
		return verifyBackup(args[1:])
	case "export":
		return exportDB(args[1:])
	case "import":
		return importDB(args[1:])
	case "diagnose":
		return diagnoseDB(args[1:])
	case "status":
		return statusDB(args[1:])
	case "cluster":
		return clusterCmd(args[1:])
	case "migrate":
		return migrateCmd(args[1:])
	default:
		printUsage(os.Stderr)
		return nerr.New(nerr.InvalidArgument, "nextsql", "unknown command")
	}
}

func initDB(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory for nextsql.db")
	keyFile := fs.String("key-file", "", "root unlock key file (created if missing; never pass a key in a URL)")
	user := fs.String("user", "", "optional bootstrap user")
	passwordFile := fs.String("password-file", "", "password file for --user")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql init", "--data-dir and --key-file are required")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql init", "mkdir", err)
	}
	if _, err := os.Stat(*keyFile); os.IsNotExist(err) {
		if _, err := crypto.CreateKeyFile(*keyFile, 1); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "created root key file %s (mode 0600); keep it off the data volume\n", *keyFile)
	}
	root, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	dbPath := filepath.Join(*dataDir, config.DataFileName)
	ident, err := format.NewIdentity()
	if err != nil {
		return err
	}
	env, err := crypto.CreateEnvelope(crypto.KeystorePath(dbPath), ident, root)
	if err != nil {
		return err
	}
	db, err := executor.CreateWithIdentity(dbPath, ident, env, *bufferPages)
	if err != nil {
		_ = env.Close()
		return err
	}
	ident = db.Eng.Identity()
	if err := db.Close(); err != nil {
		_ = env.Close()
		return err
	}
	_ = env.Close()
	if *user != "" {
		if *passwordFile == "" {
			return nerr.New(nerr.InvalidArgument, "nextsql init", "--password-file is required with --user")
		}
		pw, err := auth.ReadPasswordFile(*passwordFile)
		if err != nil {
			return err
		}
		store, err := auth.OpenOrCreate(filepath.Join(*dataDir, config.AuthFileName))
		if err != nil {
			return err
		}
		if err := store.Upsert(*user, pw); err != nil {
			return err
		}
		acl, err := security.OpenOrCreateACL(filepath.Join(*dataDir, config.ACLFileName))
		if err != nil {
			return err
		}
		if err := acl.Grant(*user, security.PrivAdmin, security.ScopeCluster, ""); err != nil {
			return err
		}
		if err := acl.Grant(*user, security.PrivConnect, security.ScopeDatabase, ""); err != nil {
			return err
		}
	}
	fmt.Printf("initialized %s\ndatabase %s\nfile %s\n", dbPath, ident.DatabaseString(), ident.FileString())
	return nil
}

func execSQL(args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.String("addr", config.DefaultListenAddr, "server address")
	fs.String("user", "", "user name")
	fs.String("password-file", "", "password file (never a URL)")
	fs.String("database", "", "database name")
	fs.String("tls-ca", "", "PEM CA / server certificate")
	fs.Bool("insecure", false, "allow plaintext on loopback only")
	fs.String("tenant", "", "optional SET TENANT after connect")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	fs.String("data-dir", "", "not valid with exec (local commands only)")
	fs.String("key-file", "", "not valid with exec (local commands only)")
	sqlText := fs.String("c", "", "SQL to execute")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	if err := cli.CheckServerMode(s); err != nil {
		return err
	}
	query, err := execSQLText(fs, *sqlText)
	if err != nil {
		return err
	}
	if query == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql exec", "SQL is required (-c or a single positional argument)")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer conn.Close()
	res, err := conn.Exec(context.Background(), query)
	if err != nil {
		return err
	}
	if len(res.Columns) > 0 {
		fmt.Println(strings.Join(res.Columns, "\t"))
		for _, row := range res.Rows {
			cols := make([]string, len(row))
			for i, v := range row {
				cols[i] = v.String()
			}
			fmt.Println(strings.Join(cols, "\t"))
		}
	}
	if res.Affected != 0 {
		fmt.Printf("affected %d\n", res.Affected)
	}
	return nil
}

func execSQLText(fs *flag.FlagSet, cFlag string) (string, error) {
	explicit := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "c" {
			explicit = true
		}
	})
	rest := fs.Args()
	if explicit {
		return cFlag, nil
	}
	switch len(rest) {
	case 0:
		return "", nil
	case 1:
		return rest[0], nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "nextsql exec", "expected a single SQL argument")
	}
}

func backupDB(args []string) error {
	fs := flag.NewFlagSet("backup", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	keyFile := fs.String("key-file", "", "root unlock key file")
	out := fs.String("out", "", "destination backup directory (must not exist)")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql backup", "--data-dir and --key-file are required")
	}
	if *out == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql backup", "--out is required")
	}
	keys, env, err := openEnvelope(*dataDir, *keyFile)
	if err != nil {
		return err
	}
	if env != nil {
		defer env.Close()
	}
	res, err := backup.Create(*dataDir, *out, keys, backup.Options{BufferPages: *bufferPages})
	auditLocal(*dataDir, security.ActionBackup, *out, err)
	if err != nil {
		return err
	}
	fmt.Printf("backup %s\ndatabase %s\ncheckpoint_lsn %d\ndurable_lsn %d\nmembers %d\nverified restore-test ok\n",
		res.Path, res.Header.Identity.DatabaseString(), res.Header.Checkpoint, res.Header.DurableLSN, res.Members)
	return nil
}

func restoreDB(args []string) error {
	fs := flag.NewFlagSet("restore", flag.ContinueOnError)
	from := fs.String("from", "", "verified backup directory")
	dataDir := fs.String("data-dir", "", "empty destination data directory")
	keyFile := fs.String("key-file", "", "root unlock key file")
	archive := fs.String("wal-archive", "", "optional archived WAL directory for PITR")
	untilLSN := fs.Uint64("until-lsn", 0, "stop redo at this LSN (0 = backup or archive tip)")
	until := fs.String("until", "", "stop redo at this RFC3339 timestamp")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql restore", "--data-dir and --key-file are required")
	}
	if *from == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql restore", "--from is required")
	}
	keys, env, err := openEnvelopeForBackup(*from, *keyFile)
	if err != nil {
		return err
	}
	if env != nil {
		defer env.Close()
	}
	opt := backup.RestoreOptions{BufferPages: *bufferPages, ArchiveDir: *archive, UntilLSN: format.LSN(*untilLSN)}
	if *until != "" {
		ts, err := parseUntil(*until)
		if err != nil {
			return err
		}
		opt.UntilTime = ts
	}
	res, err := backup.Restore(*from, *dataDir, keys, opt)
	auditLocal(*dataDir, security.ActionRestore, *from, err)
	if err != nil {
		return err
	}
	fmt.Printf("restored %s\ndatabase %s\nuntil_lsn %d\nmembers %d\n",
		res.DataDir, res.Header.Identity.DatabaseString(), res.UntilLSN, res.Members)
	return nil
}

func verifyBackup(args []string) error {
	fs := flag.NewFlagSet("verify", flag.ContinueOnError)
	from := fs.String("from", "", "backup directory")
	keyFile := fs.String("key-file", "", "root unlock key file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyFile == "" {
		return cli.LocalMissing("nextsql verify", "--key-file is required")
	}
	if *from == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql verify", "--from is required")
	}
	keys, env, err := openEnvelopeForBackup(*from, *keyFile)
	if err != nil {
		return err
	}
	if env != nil {
		defer env.Close()
	}
	if err := backup.Verify(*from, keys, true); err != nil {
		return err
	}
	hdr, err := backup.ReadHeader(*from)
	if err != nil {
		return err
	}
	fmt.Printf("verified %s\ndatabase %s\ndurable_lsn %d\n", *from, hdr.Identity.DatabaseString(), hdr.DurableLSN)
	return nil
}

func exportDB(args []string) error {
	fs := flag.NewFlagSet("export", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	keyFile := fs.String("key-file", "", "root unlock key file")
	out := fs.String("out", "", "destination export directory (must not exist)")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql export", "--data-dir and --key-file are required")
	}
	if *out == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql export", "--out is required")
	}
	root, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	keys, env, err := openEnvelope(*dataDir, *keyFile)
	if err != nil {
		return err
	}
	if env != nil {
		defer env.Close()
	}
	res, err := xport.Export(*dataDir, *out, keys, xport.Options{BufferPages: *bufferPages, Root: root})
	auditLocal(*dataDir, security.ActionExport, *out, err)
	if err != nil {
		return err
	}
	fmt.Printf("export %s\ndatabase %s\ntables %d\nrows %d\nverified import-test ok\n",
		res.Path, res.Header.Identity.DatabaseString(), res.Tables, res.Rows)
	return nil
}

func importDB(args []string) error {
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	from := fs.String("from", "", "verified export directory")
	dataDir := fs.String("data-dir", "", "destination data directory")
	keyFile := fs.String("key-file", "", "root unlock key file")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql import", "--data-dir and --key-file are required")
	}
	if *from == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql import", "--from is required")
	}
	root, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	destKeys, destEnv, err := openOrCreateDestEnvelope(*dataDir, *keyFile, root)
	if err != nil {
		return err
	}
	if destEnv != nil {
		defer destEnv.Close()
	}
	res, err := xport.Import(*from, *dataDir, destKeys, xport.ImportOptions{BufferPages: *bufferPages, Root: root})
	auditLocal(*dataDir, security.ActionImport, *from, err)
	if err != nil {
		return err
	}
	fmt.Printf("imported %s\ndatabase %s\ntables %d\nrows %d\n",
		res.DataDir, res.Header.Identity.DatabaseString(), res.Tables, res.Rows)
	return nil
}

func diagnoseDB(args []string) error {
	fs := flag.NewFlagSet("diagnose", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory containing nextsql.db")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return cli.LocalMissing("nextsql diagnose", "--data-dir is required")
	}
	rep, err := upgrade.Inspect(*dataDir)
	if err != nil {
		return err
	}
	fmt.Printf("nextsql %s (phase %d)\n", version.String, version.Phase)
	upgrade.WriteReport(os.Stdout, rep)
	if !rep.OK {
		return nerr.New(nerr.InvalidFormat, "nextsql diagnose", "incompatible or damaged headers")
	}
	return nil
}

func statusDB(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	local := fs.Bool("local", false, "inspect the data directory instead of dialing nextsqld")
	fs.String("addr", config.DefaultListenAddr, "server address")
	fs.String("user", "", "user name")
	fs.String("password-file", "", "password file (never a URL)")
	fs.String("database", "", "database name")
	fs.String("tls-ca", "", "PEM CA / server certificate")
	fs.Bool("insecure", false, "allow plaintext on loopback only")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	fs.String("data-dir", "", "directory containing nextsql.db")
	fs.String("key-file", "", "root unlock key file")
	fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	if *local && s.Explicit["addr"] {
		return nerr.New(nerr.InvalidArgument, "nextsql status", "use either --local or --addr, not both")
	}
	if *local {
		return statusLocal(s)
	}
	return statusServer(s)
}

func statusServer(s cli.Settings) error {
	if err := cli.CheckServerMode(s); err != nil {
		return err
	}
	s.Tenant = ""
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer conn.Close()
	fmt.Printf("mode server\naddr %s\nuser %s\ndatabase %s\nok\n", s.Addr, s.User, s.Database)
	return nil
}

func statusLocal(s cli.Settings) error {
	dataDir := s.DataDir
	keyFile := s.KeyFile
	bufferPages := s.BufferPages
	if dataDir == "" || keyFile == "" {
		return cli.LocalMissing("nextsql status", "--data-dir and --key-file are required")
	}
	rep, err := upgrade.Inspect(dataDir)
	if err != nil {
		return err
	}
	fmt.Printf("nextsql %s (phase %d)\n", version.String, version.Phase)
	upgrade.WriteReport(os.Stdout, rep)
	keys, env, err := openEnvelope(dataDir, keyFile)
	if err != nil {
		return err
	}
	if env != nil {
		defer env.Close()
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	db, err := executor.Open(dbPath, keys, bufferPages)
	if err != nil && nerr.HasCode(err, nerr.NotFound) {
		eng, oerr := storage.Open(dbPath, keys, bufferPages)
		if oerr != nil {
			return err
		}
		defer eng.Close()
		fmt.Fprintf(os.Stdout, "\nopened tables 0\ncatalog uninitialized\n")
		if eng.WAL != nil {
			fmt.Fprintf(os.Stdout, "durable_lsn %d\ncheckpoint_lsn %d\nnext_lsn %d\nwal_bytes_written %d\n",
				eng.WAL.DurableLSN(), eng.WAL.CheckpointLSN(), eng.WAL.NextLSN(), eng.WAL.BytesWritten())
		}
		fmt.Fprintf(os.Stdout, "isolated_pages %d\n", len(eng.Isolated()))
		if !rep.OK {
			return nerr.New(nerr.InvalidFormat, "nextsql status", "incompatible or damaged headers")
		}
		return nil
	}
	if err != nil {
		return err
	}
	defer db.Close()
	fmt.Fprintf(os.Stdout, "\nopened tables %d\n", len(db.Cat.List()))
	if db.Eng != nil && db.Eng.WAL != nil {
		fmt.Fprintf(os.Stdout, "durable_lsn %d\ncheckpoint_lsn %d\nnext_lsn %d\nwal_bytes_written %d\n",
			db.Eng.WAL.DurableLSN(), db.Eng.WAL.CheckpointLSN(), db.Eng.WAL.NextLSN(), db.Eng.WAL.BytesWritten())
	}
	if db.Eng != nil {
		fmt.Fprintf(os.Stdout, "isolated_pages %d\n", len(db.Eng.Isolated()))
	}
	if m := db.Metrics(); m != nil {
		snap := m.Snapshot()
		fmt.Fprintf(os.Stdout, "queries %d\nerrors %d\ncommits %d\nrollbacks %d\nadmitted %d\nrejected %d\ncanceled %d\nheap_alloc %d\ngoroutines %d\n",
			snap.Queries, snap.Errors, snap.Commits, snap.Rollbacks, snap.Admitted, snap.Rejected, snap.Canceled, snap.HeapAlloc, snap.NumGoroutine)
		fmt.Fprintf(os.Stdout, "cdc_subscriptions %d\ncdc_active %d\ncdc_transactions %d\ncdc_events %d\ncdc_errors %d\ncdc_lag_lsn %d\n",
			snap.CDCSubscriptions, snap.CDCActive, snap.CDCTransactions, snap.CDCEvents, snap.CDCErrors, snap.CDCLagLSN)
	}
	if a := db.Admission(); a != nil {
		st := a.Stats()
		fmt.Fprintf(os.Stdout, "inflight %d\nqueue %d\nadmission_capacity %d\n", st.Inflight, st.Queued, st.Capacity)
	}
	if st, err := replication.ReadStatusFile(dataDir); err == nil {
		fmt.Fprintf(os.Stdout, "cluster_node %s\ncluster_state %s\ncluster_leader %s\ncluster_voters %d\n",
			st.NodeID, st.State, st.LeaderID, st.Voters)
	}
	if !rep.OK {
		return nerr.New(nerr.InvalidFormat, "nextsql status", "incompatible or damaged headers")
	}
	return nil
}

func openOrCreateDestEnvelope(dataDir, keyFile string, root *crypto.DEK) (crypto.KeyProvider, *crypto.Envelope, error) {
	dbPath := filepath.Join(dataDir, config.DataFileName)
	ks := crypto.KeystorePath(dbPath)
	if _, err := os.Stat(ks); err == nil {
		env, err := crypto.OpenEnvelope(ks, root)
		if err != nil {
			return nil, nil, err
		}
		return env, env, nil
	}
	if _, err := os.Stat(dbPath); err == nil {
		return openEnvelope(dataDir, keyFile)
	}
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, nil, nerr.Wrap(nerr.IO, "nextsql import", "mkdir", err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		return nil, nil, err
	}
	env, err := crypto.CreateEnvelope(ks, id, root)
	if err != nil {
		return nil, nil, err
	}
	return env, env, nil
}

func openEnvelope(dataDir, keyFile string) (crypto.KeyProvider, *crypto.Envelope, error) {
	root, err := crypto.ReadKeyFile(keyFile)
	if err != nil {
		return nil, nil, err
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	ks := crypto.KeystorePath(dbPath)
	if _, err := os.Stat(ks); err == nil {
		env, err := crypto.OpenEnvelope(ks, root)
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

func openEnvelopeForBackup(backupDir, keyFile string) (crypto.KeyProvider, *crypto.Envelope, error) {
	root, err := crypto.ReadKeyFile(keyFile)
	if err != nil {
		return nil, nil, err
	}
	return backup.OpenKeys(backupDir, root)
}

func parseUntil(s string) (time.Time, error) {
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		ts, err = time.Parse(time.RFC3339Nano, s)
	}
	if err != nil {
		return time.Time{}, nerr.New(nerr.InvalidArgument, "nextsql restore", "--until must be RFC3339")
	}
	return ts, nil
}

func auditLocal(dataDir, action, object string, err error) {
	if dataDir == "" {
		return
	}
	p := filepath.Join(dataDir, config.AuditFileName)
	if _, stat := os.Stat(p); stat != nil {
		return
	}
	log, oerr := security.OpenAudit(p)
	if oerr != nil {
		return
	}
	defer log.Close()
	log.Record(security.Event{Action: action, Object: object, Outcome: security.Outcome(err)})
}

func clusterCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql cluster", "expected status")
	}
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("cluster status", flag.ContinueOnError)
		dataDir := fs.String("data-dir", "", "data directory")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *dataDir == "" {
			return cli.LocalMissing("nextsql cluster status", "--data-dir is required")
		}
		st, err := replication.ReadStatusFile(*dataDir)
		if err != nil {
			return err
		}
		fmt.Printf("node %s\nstate %s\nleader %s\nleader_addr %s\napplied_lsn %d\nvoters %d\nhas_leader %t\n",
			st.NodeID, st.State, st.LeaderID, st.Leader, st.Applied, st.Voters, st.HasLeader)
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql cluster", "unknown cluster command")
	}
}

func migrateCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate", "expected status, pending, version, validate, create, up, down, force, or repair")
	}
	switch args[0] {
	case "status":
		return migrateStatus(args[1:])
	case "pending":
		return migratePending(args[1:])
	case "version":
		return migrateVersion(args[1:])
	case "validate":
		return migrateValidate(args[1:])
	case "create":
		return migrateCreate(args[1:])
	case "up":
		return migrateUp(args[1:])
	case "down":
		return migrateDown(args[1:])
	case "force":
		return migrateForce(args[1:])
	case "repair":
		return migrateRepair(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql migrate", "unknown migrate command")
	}
}

func migrateFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.String("dir", "", "migrations directory (default NEXTSQL_MIGRATIONS_DIR or ./migrations)")
	fs.String("addr", config.DefaultListenAddr, "server address")
	fs.String("user", "", "user name")
	fs.String("password-file", "", "password file (never a URL)")
	fs.String("database", "", "database name")
	fs.String("tls-ca", "", "PEM CA / server certificate")
	fs.Bool("insecure", false, "allow plaintext on loopback only")
	fs.String("tenant", "", "optional SET TENANT after connect")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	fs.String("data-dir", "", "not valid with migrate (local commands only)")
	fs.String("key-file", "", "not valid with migrate (local commands only)")
	return fs
}

func resolveMigrate(fs *flag.FlagSet, args []string) (cli.Settings, error) {
	if err := fs.Parse(args); err != nil {
		return cli.Settings{}, err
	}
	s, err := cli.Resolve(fs, args)
	if err != nil {
		return cli.Settings{}, err
	}
	if err := cli.CheckServerMode(s); err != nil {
		return cli.Settings{}, err
	}
	return s, nil
}

func migrateValidate(args []string) error {
	fs := migrateFlags("migrate validate")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate validate", "unexpected arguments")
	}
	migs, err := migrate.Validate(s.MigrationsDir)
	if err != nil {
		return err
	}
	fmt.Printf("ok %s\nversions %d\n", s.MigrationsDir, len(migs))
	return nil
}

func migrateStatus(args []string) error {
	fs := migrateFlags("migrate status")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate status", "unexpected arguments")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	rep, err := migrate.Status(context.Background(), migrateConn{conn}, s.MigrationsDir)
	printMigrateReport(rep)
	return err
}

func migratePending(args []string) error {
	fs := migrateFlags("migrate pending")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate pending", "unexpected arguments")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	pend, err := migrate.Pending(context.Background(), migrateConn{conn}, s.MigrationsDir)
	if err != nil {
		return err
	}
	for _, m := range pend {
		fmt.Printf("%s %s\n", m.Version, m.Name)
	}
	return nil
}

func migrateVersion(args []string) error {
	fs := migrateFlags("migrate version")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate version", "unexpected arguments")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	ver, err := migrate.CurrentVersion(context.Background(), migrateConn{conn})
	if err != nil {
		return err
	}
	if ver == "" {
		fmt.Println("none")
		return nil
	}
	fmt.Println(ver)
	return nil
}

func migrateUp(args []string) error {
	fs := migrateFlags("migrate up")
	count := fs.Int("count", 0, "apply at most N pending files (0 = all)")
	to := fs.String("to", "", "apply through this version (inclusive)")
	dryRun := fs.Bool("dry-run", false, "plan and parse only; do not execute")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate up", "unexpected arguments")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	applied, err := migrate.Up(context.Background(), migrateConn{conn}, s.MigrationsDir, migrate.UpOptions{
		Count:  *count,
		To:     *to,
		DryRun: *dryRun,
	})
	for _, ver := range applied {
		if *dryRun {
			fmt.Printf("dry-run %s\n", ver)
		} else {
			fmt.Println(ver)
		}
	}
	return err
}

func migrateDown(args []string) error {
	fs := migrateFlags("migrate down")
	count := fs.Int("count", 0, "roll back at most N applied files (0 = all)")
	to := fs.String("to", "", "roll back through this version (inclusive)")
	dryRun := fs.Bool("dry-run", false, "plan and parse only; do not execute")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate down", "unexpected arguments")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	applied, err := migrate.Down(context.Background(), migrateConn{conn}, s.MigrationsDir, migrate.DownOptions{
		Count:  *count,
		To:     *to,
		DryRun: *dryRun,
	})
	for _, ver := range applied {
		if *dryRun {
			fmt.Printf("dry-run %s\n", ver)
		} else {
			fmt.Println(ver)
		}
	}
	return err
}

func migrateForce(args []string) error {
	fs := migrateFlags("migrate force")
	confirm := fs.Bool("confirm", false, "required; force never runs migration SQL")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate force", "expected VERSION")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate force", "--confirm is required")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	if err := migrate.Force(context.Background(), migrateConn{conn}, s.MigrationsDir, rest[0]); err != nil {
		return err
	}
	fmt.Printf("forced %s\n", rest[0])
	return nil
}

func migrateRepair(args []string) error {
	fs := migrateFlags("migrate repair")
	confirm := fs.Bool("confirm", false, "required; rewrites stored checksums only")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	if fs.NArg() != 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate repair", "unexpected arguments")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate repair", "--confirm is required")
	}
	conn, err := cli.Open(context.Background(), s)
	if err != nil {
		return err
	}
	defer migrateClose(conn, s)
	n, err := migrate.Repair(context.Background(), migrateConn{conn}, s.MigrationsDir)
	if err != nil {
		return err
	}
	fmt.Printf("repaired %d\n", n)
	return nil
}

type migrateConn struct{ c *nextsql.Conn }

func (m migrateConn) Exec(ctx context.Context, sql string, params ...types.Value) (migrate.Result, error) {
	res, err := m.c.Exec(ctx, sql, params...)
	if err != nil {
		return migrate.Result{}, err
	}
	return migrate.Result{Columns: res.Columns, Rows: res.Rows, Affected: res.Affected}, nil
}

func migrateClose(conn *nextsql.Conn, s cli.Settings) {
	if strings.TrimSpace(s.Tenant) != "" {
		_, _ = conn.Exec(context.Background(), "RESET TENANT")
	}
	_ = conn.Close()
}

func printMigrateReport(rep migrate.Report) {
	ver := rep.Version
	if ver == "" {
		ver = "none"
	}
	fmt.Printf("version %s\n", ver)
	fmt.Printf("dirty %t\n", rep.Dirty)
	if rep.Dirty && rep.DirtyVer != "" {
		fmt.Printf("dirty_version %s\n", rep.DirtyVer)
	}
	fmt.Printf("applied %d\n", rep.Applied)
	fmt.Printf("pending %d\n", rep.Pending)
	for _, v := range rep.Mismatches {
		fmt.Printf("checksum_mismatch %s\n", v)
	}
}

func migrateCreate(args []string) error {
	fs := migrateFlags("migrate create")
	s, err := resolveMigrate(fs, args)
	if err != nil {
		return err
	}
	rest := fs.Args()
	if len(rest) != 1 {
		return nerr.New(nerr.InvalidArgument, "nextsql migrate create", "expected NAME")
	}
	up, down, err := migrate.Create(s.MigrationsDir, rest[0])
	if err != nil {
		return err
	}
	fmt.Println(up)
	fmt.Println(down)
	return nil
}

func printUsage(w *os.File) {
	fmt.Fprintf(w, `nextsql %s — NextSQL command-line client

Usage:
  nextsql init --data-dir DIR --key-file FILE [--user NAME --password-file FILE]
  nextsql exec [--addr HOST:PORT] [--user NAME] [--password-file FILE] [--database NAME]
               [--tls-ca FILE | --insecure] [--env-file PATH | --no-env] [--tenant VALUE]
               [-c SQL | SQL]
  nextsql migrate status|pending|version [--dir DIR] [--addr HOST:PORT] [--user NAME]
               [--password-file FILE] [--database NAME] [--tls-ca FILE | --insecure]
               [--env-file PATH | --no-env] [--tenant VALUE]
  nextsql migrate validate [--dir DIR] [--env-file PATH | --no-env]
  nextsql migrate create NAME [--dir DIR] [--env-file PATH | --no-env]
  nextsql migrate up [--count N] [--to VERSION] [--dry-run] [--dir DIR]
  nextsql migrate down [--count N] [--to VERSION] [--dry-run] [--dir DIR]
  nextsql migrate force VERSION --confirm [--dir DIR]
  nextsql migrate repair --confirm [--dir DIR]
  nextsql backup --data-dir DIR --key-file FILE --out DIR
  nextsql restore --from DIR --data-dir DIR --key-file FILE [--wal-archive DIR] [--until-lsn N | --until RFC3339]
  nextsql verify --from DIR --key-file FILE
  nextsql export --data-dir DIR --key-file FILE --out DIR
  nextsql import --from DIR --data-dir DIR --key-file FILE
  nextsql diagnose --data-dir DIR
  nextsql status [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--tls-ca FILE | --insecure]
                 [--env-file PATH | --no-env]
  nextsql status --local [--data-dir DIR] [--key-file FILE]
  nextsql cluster status --data-dir DIR
  nextsql version
  nextsql help

--key-file is the external root unlock key. It is never stored in the data directory.
Keys are never accepted in connection URLs. Use --key-file / KeyProvider.
A backup is not valid until verify (including a restore test) succeeds.
An export is not valid until verify (including an import test) succeeds.
`, version.String)
}
