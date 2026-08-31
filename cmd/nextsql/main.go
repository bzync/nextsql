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
	"github.com/bzync/nextsql/internal/hosting"
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
	case "hosting":
		return hostingCmd(args[1:])
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
	case "token":
		return tokenCmd(args[1:])
	case "login":
		return loginCmd(args[1:])
	case "logout":
		return logoutCmd(args[1:])
	case "whoami":
		return whoamiCmd(args[1:])
	default:
		printUsage(os.Stderr)
		return nerr.New(nerr.InvalidArgument, "nextsql", "unknown command")
	}
}

func hostingCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting", "expected adopt, migrate-tenant, set-realm-cap, set-realm-root, set-database-cap, or show")
	}
	switch args[0] {
	case "adopt":
		return adoptLegacyDatabase(args[1:])
	case "migrate-tenant":
		return migrateLegacyTenant(args[1:])
	case "set-realm-cap":
		return setRealmStorageCap(args[1:])
	case "set-realm-root":
		return setRealmRootAuth(args[1:])
	case "set-database-cap":
		return setDatabaseStorageCap(args[1:])
	case "show":
		return showHostingRegistry(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql hosting", "unknown hosting command")
	}
}

// openHostingRegistryForCLI resolves the deployment registry root key and opens
// the registry for a hosting subcommand. The instance key file defaults to
// KEY-FILE.instance, matching "nextsql hosting adopt". When lock is true it also
// takes the exclusive data-directory lock (a running nextsqld or another
// offline command fails closed) and returns it for the caller to Close; a
// registry write must never race the server that owns the same registry.
func openHostingRegistryForCLI(op string, fs *flag.FlagSet, args []string, lock bool) (*hosting.Registry, *hosting.DataDirLock, cli.Settings, error) {
	settings, err := cli.Resolve(fs, args)
	if err != nil {
		return nil, nil, settings, err
	}
	if settings.DataDir == "" || settings.KeyFile == "" {
		return nil, nil, settings, cli.LocalMissing(op, "--data-dir and --key-file are required")
	}
	instanceKeyFile := settings.InstanceKeyFile
	if instanceKeyFile == "" {
		instanceKeyFile = settings.KeyFile + ".instance"
	}
	var ddl *hosting.DataDirLock
	if lock {
		ddl, err = hosting.AcquireDataDirLock(settings.DataDir)
		if err != nil {
			return nil, nil, settings, err
		}
	}
	registryPath := hosting.Path(settings.DataDir)
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		if ddl != nil {
			_ = ddl.Close()
		}
		return nil, nil, settings, err
	}
	defer root.Zero()
	reg, err := hosting.Open(registryPath, root)
	if err != nil {
		if ddl != nil {
			_ = ddl.Close()
		}
		return nil, nil, settings, err
	}
	return reg, ddl, settings, nil
}

func resolveRealmID(m hosting.Manifest, name string) (hosting.ID, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, realm := range m.Realms {
		if realm.Name == name {
			return realm.ID, nil
		}
	}
	return hosting.ID{}, nerr.New(nerr.NotFound, "nextsql hosting", "unknown realm")
}

func resolveDatabaseID(m hosting.Manifest, realmID hosting.ID, name string) (hosting.ID, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	for _, realm := range m.Realms {
		if realm.ID != realmID {
			continue
		}
		for _, db := range realm.Databases {
			if db.Name == name {
				return db.ID, nil
			}
		}
	}
	return hosting.ID{}, nerr.New(nerr.NotFound, "nextsql hosting", "unknown database in realm")
}

func setRealmStorageCap(args []string) error {
	const op = "nextsql hosting set-realm-cap"
	fs := flag.NewFlagSet("hosting set-realm-cap", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realm := fs.String("realm", "", "realm name")
	capBytes := fs.Uint64("cap-bytes", 0, "realm-wide storage cap in bytes (0 clears the cap)")
	confirm := fs.Bool("confirm", false, "confirm the registry change")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, ddl, settings, err := openHostingRegistryForCLI(op, fs, args, true)
	if err != nil {
		return err
	}
	defer reg.Close()
	defer ddl.Close()
	if settings.Supplied["realm"] {
		*realm = settings.Realm
	}
	if *realm == "" {
		return cli.LocalMissing(op, "--realm is required")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, op, "--confirm is required")
	}
	realmID, err := resolveRealmID(reg.Manifest(), *realm)
	if err != nil {
		return err
	}
	if err := reg.SetRealmStorageCap(realmID, *capBytes); err != nil {
		return err
	}
	fmt.Printf("realm %s cap_bytes %d\n", strings.ToLower(strings.TrimSpace(*realm)), *capBytes)
	return nil
}

func setDatabaseStorageCap(args []string) error {
	const op = "nextsql hosting set-database-cap"
	fs := flag.NewFlagSet("hosting set-database-cap", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realm := fs.String("realm", "", "realm name")
	database := fs.String("database", "", "logical database name")
	capBytes := fs.Uint64("cap-bytes", 0, "per-database storage cap in bytes (0 clears the cap)")
	realmSecretFile := fs.String("realm-secret-file", "", "realm-root delegation secret file; authorises as realm root instead of deployment admin")
	confirm := fs.Bool("confirm", false, "confirm the registry change")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, ddl, settings, err := openHostingRegistryForCLI(op, fs, args, true)
	if err != nil {
		return err
	}
	defer reg.Close()
	defer ddl.Close()
	if settings.Supplied["realm"] {
		*realm = settings.Realm
	}
	if settings.Supplied["database"] {
		*database = settings.Database
	}
	if *realm == "" || *database == "" {
		return cli.LocalMissing(op, "--realm and --database are required")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, op, "--confirm is required")
	}
	m := reg.Manifest()
	realmID, err := resolveRealmID(m, *realm)
	if err != nil {
		return err
	}
	databaseID, err := resolveDatabaseID(m, realmID, *database)
	if err != nil {
		return err
	}
	if *realmSecretFile != "" {
		secret, err := readRealmRootSecret(op, *realmSecretFile)
		if err != nil {
			return err
		}
		defer wipe(secret)
		if err := reg.SetDatabaseStorageCapAsRealmRoot(realmID, databaseID, *capBytes, secret); err != nil {
			return err
		}
	} else if err := reg.SetDatabaseStorageCap(realmID, databaseID, *capBytes); err != nil {
		return err
	}
	fmt.Printf("realm %s database %s cap_bytes %d\n",
		strings.ToLower(strings.TrimSpace(*realm)), strings.ToLower(strings.TrimSpace(*database)), *capBytes)
	return nil
}

func setRealmRootAuth(args []string) error {
	const op = "nextsql hosting set-realm-root"
	fs := flag.NewFlagSet("hosting set-realm-root", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realm := fs.String("realm", "", "realm name")
	secretFile := fs.String("secret-file", "", "realm-root delegation secret file (>= 16 bytes)")
	clear := fs.Bool("clear", false, "remove the realm-root delegation for this realm")
	confirm := fs.Bool("confirm", false, "confirm the registry change")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, ddl, settings, err := openHostingRegistryForCLI(op, fs, args, true)
	if err != nil {
		return err
	}
	defer reg.Close()
	defer ddl.Close()
	if settings.Supplied["realm"] {
		*realm = settings.Realm
	}
	if *realm == "" {
		return cli.LocalMissing(op, "--realm is required")
	}
	if *clear == (*secretFile != "") {
		return nerr.New(nerr.InvalidArgument, op, "provide exactly one of --secret-file or --clear")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, op, "--confirm is required")
	}
	realmID, err := resolveRealmID(reg.Manifest(), *realm)
	if err != nil {
		return err
	}
	var secret []byte
	if !*clear {
		secret, err = readRealmRootSecret(op, *secretFile)
		if err != nil {
			return err
		}
		defer wipe(secret)
	}
	if err := reg.SetRealmRootAuth(realmID, secret); err != nil {
		return err
	}
	action := "set"
	if *clear {
		action = "cleared"
	}
	fmt.Printf("realm %s realm_root %s\n", strings.ToLower(strings.TrimSpace(*realm)), action)
	return nil
}

func readRealmRootSecret(op, path string) ([]byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, op, "read realm-root secret file", err)
	}
	secret := bytesTrimSpace(raw)
	if len(secret) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, op, "realm-root secret file is empty")
	}
	return secret, nil
}

func bytesTrimSpace(b []byte) []byte {
	start, end := 0, len(b)
	for start < end && (b[start] == ' ' || b[start] == '\t' || b[start] == '\n' || b[start] == '\r') {
		start++
	}
	for end > start && (b[end-1] == ' ' || b[end-1] == '\t' || b[end-1] == '\n' || b[end-1] == '\r') {
		end--
	}
	return b[start:end]
}

func wipe(b []byte) {
	for i := range b {
		b[i] = 0
	}
}

func showHostingRegistry(args []string) error {
	const op = "nextsql hosting show"
	fs := flag.NewFlagSet("hosting show", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	reg, _, _, err := openHostingRegistryForCLI(op, fs, args, false)
	if err != nil {
		return err
	}
	defer reg.Close()
	m := reg.Manifest()
	fmt.Printf("deployment %s generation %d\n", m.DeploymentID.String(), m.Generation)
	for _, realm := range m.Realms {
		realmRoot := "unset"
		if realm.RealmRootAuthHash != ([32]byte{}) {
			realmRoot = "delegated"
		}
		fmt.Printf("realm %s %s state %d cap_bytes %d realm_root %s\n",
			realm.Name, realm.ID.String(), realm.State, realm.StorageCapBytes, realmRoot)
		for _, db := range realm.Databases {
			fmt.Printf("  database %s %s state %d layout %d cap_bytes %d\n",
				db.Name, db.ID.String(), db.State, db.Layout, db.StorageCapBytes)
		}
	}
	return nil
}

// migrateLegacyTenant provisions one isolated destination deployment while it
// remains PROVISIONING, copies and verifies one historical row tenant, then
// publishes the destination ACTIVE. Both deployments are exclusively locked
// for the entire operation.
func migrateLegacyTenant(args []string) error {
	fs := flag.NewFlagSet("hosting migrate-tenant", flag.ContinueOnError)
	sourceDataDir := fs.String("source-data-dir", "", "offline source data directory")
	sourceKeyFile := fs.String("source-key-file", "", "source root unlock key file")
	tenant := fs.String("tenant", "", "exact legacy tenant UUID or string")
	destDataDir := fs.String("data-dir", "", "new or resumable isolated destination data directory")
	destKeyFile := fs.String("key-file", "", "independent destination root unlock key file")
	instanceKeyFile := fs.String("instance-key-file", "", "independent destination registry root key file (default KEY-FILE.instance)")
	realmName := fs.String("realm", "default", "destination subscription realm name")
	databaseName := fs.String("database", "default", "destination logical database name")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages per opened database")
	batchRows := fs.Int("batch-rows", 256, "rows per destination transaction (1-4096)")
	confirm := fs.Bool("confirm", false, "confirm offline source-to-isolated-database migration")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *sourceDataDir == "" || *sourceKeyFile == "" || *destDataDir == "" || *destKeyFile == "" {
		return cli.LocalMissing("nextsql hosting migrate-tenant", "--source-data-dir, --source-key-file, --data-dir, and --key-file are required")
	}
	if *tenant == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "--tenant is required")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "--confirm is required")
	}
	if *batchRows < 1 || *batchRows > 4096 {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "--batch-rows must be between 1 and 4096")
	}
	if *bufferPages < 1 {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "--buffer-pages must be positive")
	}
	if *instanceKeyFile == "" {
		*instanceKeyFile = *destKeyFile + ".instance"
	}

	var err error
	*sourceDataDir, err = filepath.Abs(filepath.Clean(*sourceDataDir))
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql hosting migrate-tenant", "resolve source data directory", err)
	}
	*destDataDir, err = filepath.Abs(filepath.Clean(*destDataDir))
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql hosting migrate-tenant", "resolve destination data directory", err)
	}
	if deploymentPathsOverlap(*sourceDataDir, *destDataDir) {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "source and destination data directories must be separate and non-nested")
	}
	if samePath(*sourceKeyFile, *destKeyFile) || samePath(*sourceKeyFile, *instanceKeyFile) || samePath(*destKeyFile, *instanceKeyFile) {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "source, destination, and registry root key files must be independent")
	}
	sourceInfo, err := os.Stat(*sourceDataDir)
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql hosting migrate-tenant", "stat source data directory", err)
	}
	if !sourceInfo.IsDir() {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "source data directory is not a directory")
	}
	if err := os.MkdirAll(*destDataDir, 0o700); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql hosting migrate-tenant", "create destination data directory", err)
	}

	firstPath, secondPath := *sourceDataDir, *destDataDir
	if secondPath < firstPath {
		firstPath, secondPath = secondPath, firstPath
	}
	firstLock, err := hosting.AcquireDataDirLock(firstPath)
	if err != nil {
		return err
	}
	defer firstLock.Close()
	secondLock, err := hosting.AcquireDataDirLock(secondPath)
	if err != nil {
		return err
	}
	defer secondLock.Close()

	sourceRoot, err := crypto.ReadKeyFile(*sourceKeyFile)
	if err != nil {
		return err
	}
	defer sourceRoot.Zero()
	sourcePath := filepath.Join(*sourceDataDir, config.DataFileName)
	sourceEnvelope, err := crypto.OpenEnvelope(crypto.KeystorePath(sourcePath), sourceRoot)
	if err != nil {
		return err
	}
	defer sourceEnvelope.Close()
	sourceDB, err := executor.Open(sourcePath, sourceEnvelope, *bufferPages)
	if err != nil {
		return err
	}
	sourceOpen := true
	defer func() {
		if sourceOpen {
			_ = sourceDB.Close()
		}
	}()

	destPath := filepath.Join(*destDataDir, config.DataFileName)
	if err := preflightHostingBootstrap(*destDataDir, destPath); err != nil {
		return err
	}
	registryPath := hosting.Path(*destDataDir)
	registryFile, err := fileExistsChecked(registryPath)
	if err != nil {
		return err
	}
	registryKeys, err := fileExistsChecked(hosting.KeyStorePath(registryPath))
	if err != nil {
		return err
	}
	destFile, err := fileExistsChecked(destPath)
	if err != nil {
		return err
	}
	destKeys, err := fileExistsChecked(crypto.KeystorePath(destPath))
	if err != nil {
		return err
	}
	partialDestination := registryFile || registryKeys || destFile || destKeys
	if err := ensureMigrationRootFile(*destKeyFile, partialDestination, "destination database"); err != nil {
		return err
	}
	if err := ensureMigrationRootFile(*instanceKeyFile, partialDestination, "destination registry"); err != nil {
		return err
	}
	destRoot, err := crypto.ReadKeyFile(*destKeyFile)
	if err != nil {
		return err
	}
	defer destRoot.Zero()
	instanceRoot, err := crypto.ReadKeyFile(*instanceKeyFile)
	if err != nil {
		return err
	}
	defer instanceRoot.Zero()
	if sourceRoot.Equal(destRoot) || sourceRoot.Equal(instanceRoot) || destRoot.Equal(instanceRoot) {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting migrate-tenant", "source, destination, and registry roots must contain independent keys")
	}

	registry, destIdentity, err := prepareHostingBootstrap(*destDataDir, destPath, destRoot, instanceRoot, *realmName, *databaseName)
	if err != nil {
		return err
	}
	defer registry.Close()
	defaultRealm, defaultDatabase, err := registry.Default()
	if err != nil {
		return err
	}
	if defaultDatabase.State != hosting.StateProvisioning && defaultDatabase.State != hosting.StateActive {
		return nerr.New(nerr.Conflict, "nextsql hosting migrate-tenant", "destination migration is not resumable from its lifecycle state")
	}
	destDB, destEnvelope, err := createOrResumeDatabase(destPath, destIdentity, destRoot, *bufferPages)
	if err != nil {
		return err
	}
	destOpen := true
	defer func() {
		if destOpen {
			_ = destDB.Close()
		}
		_ = destEnvelope.Close()
	}()
	intent := hosting.TenantMigrationIntent{
		Source: sourceDB.Eng.Identity(), Destination: destDB.Eng.Identity(),
		Tenant: *tenant, Realm: defaultRealm.Name, Database: defaultDatabase.Name,
	}
	intentPath := hosting.TenantMigrationPath(*destDataDir)
	intentKeys := destEnvelope.Provider(crypto.DomainTemp)
	currentIntent, _, err := hosting.EnsureTenantMigrationIntent(intentPath, intentKeys, intent)
	if err != nil {
		return err
	}
	if defaultDatabase.State == hosting.StateActive && currentIntent.State != hosting.TenantMigrationComplete {
		return nerr.New(nerr.Conflict, "nextsql hosting migrate-tenant", "ACTIVE destination has an incomplete migration intent")
	}

	var result *xport.LegacyTenantResult
	var migrateErr error
	if defaultDatabase.State == hosting.StateActive {
		result, migrateErr = xport.VerifyLegacyTenantMigration(sourceDB, destDB, *tenant)
	} else {
		result, migrateErr = xport.MigrateLegacyTenant(sourceDB, destDB, *tenant, xport.LegacyTenantOptions{BatchRows: *batchRows})
	}
	auditLocal(*sourceDataDir, security.ActionExport, defaultDatabase.ID.String(), migrateErr)
	auditLocal(*destDataDir, security.ActionImport, sourceDB.Eng.Identity().DatabaseString(), migrateErr)
	if migrateErr != nil {
		return migrateErr
	}
	if err := destDB.Close(); err != nil {
		return err
	}
	destOpen = false
	if err := sourceDB.Close(); err != nil {
		return err
	}
	sourceOpen = false
	if err := hosting.CompleteTenantMigrationIntent(intentPath, intentKeys, intent, uint32(result.Tables), result.Rows); err != nil {
		return err
	}
	if defaultDatabase.State == hosting.StateProvisioning {
		if err := registry.SetDatabaseState(defaultRealm.ID, defaultDatabase.ID, hosting.StateActive); err != nil {
			return err
		}
	}
	fmt.Printf("migrated legacy tenant\nsource_database %s\ndestination_database %s\nrealm %s %s\ndatabase_name %s\ntables %d\nrows %d\n",
		intent.Source.DatabaseString(), intent.Destination.DatabaseString(), defaultRealm.Name, defaultRealm.ID.String(), defaultDatabase.Name, result.Tables, result.Rows)
	return nil
}

func ensureMigrationRootFile(path string, partial bool, label string) error {
	exists, err := fileExistsChecked(path)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	if partial {
		return nerr.New(nerr.NotFound, "nextsql hosting migrate-tenant", label+" root key file is missing")
	}
	root, err := crypto.CreateKeyFile(path, 1)
	if err != nil {
		return err
	}
	root.Zero()
	fmt.Fprintf(os.Stderr, "created %s root key file %s (mode 0600); keep it off the data volume\n", label, path)
	return nil
}

func samePath(a, b string) bool {
	aa, errA := filepath.Abs(filepath.Clean(a))
	bb, errB := filepath.Abs(filepath.Clean(b))
	return errA == nil && errB == nil && aa == bb
}

func deploymentPathsOverlap(a, b string) bool {
	if a == b {
		return true
	}
	inside := func(parent, child string) bool {
		rel, err := filepath.Rel(parent, child)
		return err == nil && rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
	}
	return inside(a, b) || inside(b, a)
}

// adoptLegacyDatabase explicitly registers the existing nextsql.db as the
// deployment default. It preserves the database identity and legacy layout;
// it never discovers or adopts sibling files.
func adoptLegacyDatabase(args []string) error {
	fs := flag.NewFlagSet("hosting adopt", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "existing single-database data directory")
	keyFile := fs.String("key-file", "", "existing database root unlock key file")
	instanceKeyFile := fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realmName := fs.String("realm", "default", "adopted subscription realm name")
	databaseName := fs.String("database", "default", "adopted logical database name")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages used for recovery verification")
	confirm := fs.Bool("confirm", false, "confirm offline deployment adoption")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	settings, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	*dataDir = settings.DataDir
	*keyFile = settings.KeyFile
	*instanceKeyFile = settings.InstanceKeyFile
	*bufferPages = settings.BufferPages
	*confirm = settings.HostingConfirm
	if settings.Supplied["realm"] {
		*realmName = settings.Realm
	}
	if settings.Supplied["database"] {
		*databaseName = settings.Database
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql hosting adopt", "--data-dir and --key-file are required")
	}
	if !*confirm {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting adopt", "--confirm is required")
	}
	st, err := os.Stat(*dataDir)
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql hosting adopt", "stat data directory", err)
	}
	if !st.IsDir() {
		return nerr.New(nerr.InvalidArgument, "nextsql hosting adopt", "data directory is not a directory")
	}
	dataDirLock, err := hosting.AcquireDataDirLock(*dataDir)
	if err != nil {
		return err
	}
	defer dataDirLock.Close()

	report, err := upgrade.Inspect(*dataDir)
	if err != nil {
		return err
	}
	if !report.OK || !report.HasIdent || !report.Keystore {
		return nerr.New(nerr.Corruption, "nextsql hosting adopt", "legacy database preflight failed; run nextsql diagnose")
	}
	dbPath := filepath.Join(*dataDir, config.DataFileName)
	databaseRoot, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	defer databaseRoot.Zero()
	databaseEnvelope, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), databaseRoot)
	if err != nil {
		return err
	}
	if databaseEnvelope.Identity() != report.Identity {
		_ = databaseEnvelope.Close()
		return nerr.New(nerr.Corruption, "nextsql hosting adopt", "database and keystore identities do not match")
	}
	db, err := executor.Open(dbPath, databaseEnvelope, *bufferPages)
	if err != nil {
		_ = databaseEnvelope.Close()
		return err
	}
	identity := db.Eng.Identity()
	if err := db.Close(); err != nil {
		_ = databaseEnvelope.Close()
		return err
	}
	if err := databaseEnvelope.Close(); err != nil {
		return err
	}

	registryPath := hosting.Path(*dataDir)
	registryFilePresent, err := fileExistsChecked(registryPath)
	if err != nil {
		return err
	}
	registryKeysPresent, err := fileExistsChecked(hosting.KeyStorePath(registryPath))
	if err != nil {
		return err
	}
	registryPresent := registryFilePresent || registryKeysPresent
	if *instanceKeyFile == "" {
		*instanceKeyFile = *keyFile + ".instance"
	}
	instanceRootPresent, err := fileExistsChecked(*instanceKeyFile)
	if err != nil {
		return err
	}
	if !instanceRootPresent {
		if registryPresent {
			return nerr.New(nerr.NotFound, "nextsql hosting adopt", "deployment registry root key file is missing")
		}
		createdRoot, err := crypto.CreateKeyFile(*instanceKeyFile, 1)
		if err != nil {
			return err
		}
		createdRoot.Zero()
		fmt.Fprintf(os.Stderr, "created deployment registry key file %s (mode 0600); keep it off the data volume\n", *instanceKeyFile)
	}
	instanceRoot, err := crypto.ReadKeyFile(*instanceKeyFile)
	if err != nil {
		return err
	}
	defer instanceRoot.Zero()
	registry, _, err := hosting.EnsureBootstrap(registryPath, instanceRoot, hosting.Bootstrap{
		RealmName:        *realmName,
		DatabaseName:     *databaseName,
		DatabaseIdentity: identity,
		DatabaseState:    hosting.StateProvisioning,
	})
	if err != nil {
		return err
	}
	defer registry.Close()
	realm, database, err := registry.Default()
	if err != nil {
		return err
	}
	if database.Layout != hosting.LayoutLegacyDefault {
		return nerr.New(nerr.Conflict, "nextsql hosting adopt", "registered database is not in the legacy default layout")
	}
	switch database.State {
	case hosting.StateProvisioning:
		if err := registry.SetDatabaseState(realm.ID, database.ID, hosting.StateActive); err != nil {
			return err
		}
	case hosting.StateActive:
		// Exact reruns are idempotent after database recovery verification.
	default:
		return nerr.New(nerr.Conflict, "nextsql hosting adopt", "database adoption is not resumable from its current state")
	}
	manifest := registry.Manifest()
	fmt.Printf("adopted %s\ndeployment %s\nrealm %s %s\ndatabase_name %s\ndatabase %s\nfile %s\n",
		dbPath, manifest.DeploymentID.String(), realm.Name, realm.ID.String(), database.Name,
		identity.DatabaseString(), identity.FileString())
	return nil
}

func fileExistsChecked(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, nerr.Wrap(nerr.IO, "nextsql", "stat file", err)
}

func initDB(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory for nextsql.db")
	keyFile := fs.String("key-file", "", "root unlock key file (created if missing; never pass a key in a URL)")
	user := fs.String("user", "", "optional bootstrap user")
	passwordFile := fs.String("password-file", "", "password file for --user")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages")
	realmName := fs.String("realm", "default", "bootstrap subscription realm name")
	databaseName := fs.String("database", "default", "bootstrap logical database name")
	instanceKeyFile := fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	fs.String("env-file", "", "load only this dotenv file")
	fs.Bool("no-env", false, "do not load .env files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	settings, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	*dataDir = settings.DataDir
	*keyFile = settings.KeyFile
	*instanceKeyFile = settings.InstanceKeyFile
	*bufferPages = settings.BufferPages
	serverPass := ""
	if settings.Explicit["user"] {
		*user = settings.User
	} else if settings.Supplied["server-user"] {
		*user = settings.ServerUser
	}
	if settings.Explicit["password-file"] {
		*passwordFile = settings.PasswordFile
	} else if settings.Supplied["server-password-file"] {
		*passwordFile = settings.ServerPassFile
	}
	if !settings.Explicit["password-file"] && settings.Supplied["server-pass"] {
		serverPass = settings.ServerPass
	}
	if settings.Supplied["realm"] {
		*realmName = settings.Realm
	}
	if settings.Supplied["database"] {
		*databaseName = settings.Database
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql init", "--data-dir and --key-file are required")
	}
	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql init", "mkdir", err)
	}
	dataDirLock, err := hosting.AcquireDataDirLock(*dataDir)
	if err != nil {
		return err
	}
	defer dataDirLock.Close()
	dbPath := filepath.Join(*dataDir, config.DataFileName)
	if err := preflightHostingBootstrap(*dataDir, dbPath); err != nil {
		return err
	}
	if _, err := os.Stat(*keyFile); os.IsNotExist(err) {
		createdRoot, err := crypto.CreateKeyFile(*keyFile, 1)
		if err != nil {
			return err
		}
		createdRoot.Zero()
		fmt.Fprintf(os.Stderr, "created root key file %s (mode 0600); keep it off the data volume\n", *keyFile)
	}
	root, err := crypto.ReadKeyFile(*keyFile)
	if err != nil {
		return err
	}
	defer root.Zero()
	if *instanceKeyFile == "" {
		*instanceKeyFile = *keyFile + ".instance"
	}
	if _, err := os.Stat(*instanceKeyFile); os.IsNotExist(err) {
		createdRoot, err := crypto.CreateKeyFile(*instanceKeyFile, 1)
		if err != nil {
			return err
		}
		createdRoot.Zero()
		fmt.Fprintf(os.Stderr, "created deployment registry key file %s (mode 0600); keep it off the data volume\n", *instanceKeyFile)
	}
	instanceRoot, err := crypto.ReadKeyFile(*instanceKeyFile)
	if err != nil {
		return err
	}
	defer instanceRoot.Zero()
	registry, ident, err := prepareHostingBootstrap(*dataDir, dbPath, root, instanceRoot, *realmName, *databaseName)
	if err != nil {
		return err
	}
	defer registry.Close()
	defaultRealm, defaultDatabase, err := registry.Default()
	if err != nil {
		return err
	}
	if defaultDatabase.State == hosting.StateActive {
		return nerr.New(nerr.AlreadyExists, "nextsql init", "database is already initialized")
	}
	if defaultDatabase.State != hosting.StateProvisioning {
		return nerr.New(nerr.Conflict, "nextsql init", "database bootstrap is not resumable from its current state")
	}
	db, env, err := createOrResumeDatabase(dbPath, ident, root, *bufferPages)
	if err != nil {
		return err
	}
	ident = db.Eng.Identity()
	if err := db.Close(); err != nil {
		_ = env.Close()
		return err
	}
	_ = env.Close()
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
			return nerr.New(nerr.InvalidArgument, "nextsql init", "--password-file, NEXTSQL_SERVER_PASSWORD_FILE, or NEXTSQL_SERVER_PASS is required with the bootstrap user")
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
	if err := registry.SetDatabaseState(defaultRealm.ID, defaultDatabase.ID, hosting.StateActive); err != nil {
		return err
	}
	manifest := registry.Manifest()
	fmt.Printf("initialized %s\ndatabase %s\nfile %s\ndeployment %s\nrealm %s %s\ndatabase_name %s\n",
		dbPath, ident.DatabaseString(), ident.FileString(), manifest.DeploymentID.String(),
		defaultRealm.Name, defaultRealm.ID.String(), defaultDatabase.Name)
	return nil
}

func preflightHostingBootstrap(dataDir, dbPath string) error {
	registryPath := hosting.Path(dataDir)
	_, registryErr := os.Stat(registryPath)
	_, registryKeyErr := os.Stat(hosting.KeyStorePath(registryPath))
	if registryErr == nil || registryKeyErr == nil {
		return nil
	}
	if !os.IsNotExist(registryErr) {
		return nerr.Wrap(nerr.IO, "nextsql init", "stat deployment registry", registryErr)
	}
	if !os.IsNotExist(registryKeyErr) {
		return nerr.Wrap(nerr.IO, "nextsql init", "stat deployment registry keys", registryKeyErr)
	}
	_, dbErr := os.Stat(dbPath)
	_, dbKeyErr := os.Stat(crypto.KeystorePath(dbPath))
	if dbErr == nil || dbKeyErr == nil {
		return nerr.New(nerr.AlreadyExists, "nextsql init", "legacy database exists without a deployment registry; explicit migration/adoption is required")
	}
	if !os.IsNotExist(dbErr) {
		return nerr.Wrap(nerr.IO, "nextsql init", "stat database", dbErr)
	}
	if !os.IsNotExist(dbKeyErr) {
		return nerr.Wrap(nerr.IO, "nextsql init", "stat database keys", dbKeyErr)
	}
	return nil
}

func prepareHostingBootstrap(dataDir, dbPath string, databaseRoot, instanceRoot *crypto.DEK, realmName, databaseName string) (*hosting.Registry, format.Identity, error) {
	registryPath := hosting.Path(dataDir)
	var ident format.Identity
	if _, err := os.Stat(registryPath); err == nil {
		existing, err := hosting.Open(registryPath, instanceRoot)
		if err != nil {
			return nil, ident, err
		}
		_, db, err := existing.Default()
		_ = existing.Close()
		if err != nil {
			return nil, ident, err
		}
		ident = db.Identity
	} else if !os.IsNotExist(err) {
		return nil, ident, nerr.Wrap(nerr.IO, "nextsql init", "stat deployment registry", err)
	} else {
		_, keyErr := os.Stat(hosting.KeyStorePath(registryPath))
		registryKeysExist := keyErr == nil
		if keyErr != nil && !os.IsNotExist(keyErr) {
			return nil, ident, nerr.Wrap(nerr.IO, "nextsql init", "stat deployment registry keys", keyErr)
		}
		_, dbErr := os.Stat(dbPath)
		_, dbKeyErr := os.Stat(crypto.KeystorePath(dbPath))
		dbExists := dbErr == nil
		dbKeysExist := dbKeyErr == nil
		if dbErr != nil && !os.IsNotExist(dbErr) {
			return nil, ident, nerr.Wrap(nerr.IO, "nextsql init", "stat database", dbErr)
		}
		if dbKeyErr != nil && !os.IsNotExist(dbKeyErr) {
			return nil, ident, nerr.Wrap(nerr.IO, "nextsql init", "stat database keys", dbKeyErr)
		}
		if !registryKeysExist && (dbExists || dbKeysExist) {
			return nil, ident, nerr.New(nerr.AlreadyExists, "nextsql init", "legacy database exists without a deployment registry; explicit migration/adoption is required")
		}
		if dbKeysExist {
			env, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), databaseRoot)
			if err != nil {
				return nil, ident, err
			}
			ident = env.Identity()
			_ = env.Close()
		} else {
			var err error
			ident, err = format.NewIdentity()
			if err != nil {
				return nil, ident, err
			}
		}
	}
	registry, _, err := hosting.EnsureBootstrap(registryPath, instanceRoot, hosting.Bootstrap{
		RealmName:        realmName,
		DatabaseName:     databaseName,
		DatabaseIdentity: ident,
		DatabaseState:    hosting.StateProvisioning,
	})
	if err != nil {
		return nil, ident, err
	}
	return registry, ident, nil
}

func createOrResumeDatabase(path string, ident format.Identity, root *crypto.DEK, bufferPages int) (*executor.DB, *crypto.Envelope, error) {
	keyPath := crypto.KeystorePath(path)
	_, dbErr := os.Stat(path)
	_, keyErr := os.Stat(keyPath)
	dbExists := dbErr == nil
	keysExist := keyErr == nil
	if dbErr != nil && !os.IsNotExist(dbErr) {
		return nil, nil, nerr.Wrap(nerr.IO, "nextsql init", "stat database", dbErr)
	}
	if keyErr != nil && !os.IsNotExist(keyErr) {
		return nil, nil, nerr.Wrap(nerr.IO, "nextsql init", "stat database keys", keyErr)
	}
	if dbExists && !keysExist {
		return nil, nil, nerr.New(nerr.Corruption, "nextsql init", "database exists without its keystore")
	}
	var (
		env *crypto.Envelope
		err error
	)
	if keysExist {
		env, err = crypto.OpenEnvelope(keyPath, root)
	} else {
		env, err = crypto.CreateEnvelope(keyPath, ident, root)
	}
	if err != nil {
		return nil, nil, err
	}
	if env.Identity() != ident {
		_ = env.Close()
		return nil, nil, nerr.New(nerr.Corruption, "nextsql init", "database keystore identity does not match deployment registry")
	}
	var db *executor.DB
	if dbExists {
		db, err = executor.Open(path, env, bufferPages)
	} else {
		db, err = executor.CreateWithIdentity(path, ident, env, bufferPages)
	}
	if err != nil {
		_ = env.Close()
		return nil, nil, err
	}
	return db, env, nil
}

func execSQL(args []string) error {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.String("addr", config.DefaultListenAddr, "server address")
	fs.String("user", "", "user name")
	fs.String("password-file", "", "password file (never a URL)")
	fs.String("idp", "", "external identity provider profile to authenticate with (see `nextsql login`)")
	fs.String("idp-config", "", "client identity-provider config file (default ~/.config/nextsql/config.toml)")
	fs.String("database", "", "database name")
	fs.String("tls-ca", "", "PEM CA / server certificate")
	fs.String("tls-server-name", "", "TLS certificate server name (default address host)")
	fs.String("tls-client-cert", "", "mTLS client certificate (PEM)")
	fs.String("tls-client-key", "", "mTLS client private key (PEM)")
	fs.Bool("insecure", false, "allow plaintext on loopback only")
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
	fs.String("idp", "", "external identity provider profile to authenticate with (see `nextsql login`)")
	fs.String("idp-config", "", "client identity-provider config file (default ~/.config/nextsql/config.toml)")
	fs.String("database", "", "database name")
	fs.String("tls-ca", "", "PEM CA / server certificate")
	fs.String("tls-server-name", "", "TLS certificate server name (default address host)")
	fs.String("tls-client-cert", "", "mTLS client certificate (PEM)")
	fs.String("tls-client-key", "", "mTLS client private key (PEM)")
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
	fs.String("dir", "", "migrations directory (default NEXTSQL_MIGRATION_DIR or ./migrations)")
	fs.String("addr", config.DefaultListenAddr, "server address")
	fs.String("user", "", "user name")
	fs.String("password-file", "", "password file (never a URL)")
	fs.String("database", "", "database name")
	fs.String("tls-ca", "", "PEM CA / server certificate")
	fs.String("tls-server-name", "", "TLS certificate server name (default address host)")
	fs.String("tls-client-cert", "", "mTLS client certificate (PEM)")
	fs.String("tls-client-key", "", "mTLS client private key (PEM)")
	fs.Bool("insecure", false, "allow plaintext on loopback only")
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

func migrateClose(conn *nextsql.Conn, _ cli.Settings) {
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
  nextsql init --data-dir DIR --key-file FILE [--instance-key-file FILE]
               [--realm NAME --database NAME] [--user NAME --password-file FILE]
               [--env-file PATH | --no-env]
  nextsql hosting adopt --data-dir DIR --key-file FILE [--instance-key-file FILE]
               [--realm NAME --database NAME] --confirm [--env-file PATH | --no-env]
  nextsql hosting migrate-tenant --source-data-dir DIR --source-key-file FILE --tenant VALUE
               --data-dir DIR --key-file FILE [--instance-key-file FILE]
               [--realm NAME --database NAME] [--batch-rows N] --confirm
  nextsql exec [--addr HOST:PORT] [--user NAME] [--password-file FILE] [--database NAME]
               [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
               [--env-file PATH | --no-env]
               [-c SQL | SQL]
  nextsql migrate status|pending|version [--dir DIR] [--addr HOST:PORT] [--user NAME]
               [--password-file FILE] [--database NAME]
               [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
               [--env-file PATH | --no-env]
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
                 [--database NAME]
                 [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
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
