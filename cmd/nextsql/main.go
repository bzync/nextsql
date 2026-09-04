package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
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
	case "setup":
		return setupCmd(args[1:])
	case "lifecycle":
		return lifecycleCmd(args[1:])
	case "hosting":
		return hostingCmd(args[1:])
	case "realm":
		return realmCmd(args[1:])
	case "database":
		return databaseCmd(args[1:])
	case "exec":
		return execSQL(args[1:])
	case "backup":
		return backupCmd(args[1:])
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
	case "audit":
		return auditCmd(args[1:])
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

// realmCmd and databaseCmd are the M2-1 slice of the multi-database hosting
// cross-cutting track (docs/design-multidatabase-dbaas.md §11.2): a
// registered realm/database creation primitive, independent of the wire
// protocol and the bounded DatabaseManager that later M2 increments add.
// nextsqld does not yet open or serve anything created here — see the
// design doc for the full sequence.
func realmCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql realm", "expected create")
	}
	switch args[0] {
	case "create":
		return createRealm(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql realm", "unknown realm command")
	}
}

func databaseCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql database", "expected create, suspend, resume, or drop")
	}
	switch args[0] {
	case "create":
		return createDatabase(args[1:])
	case "suspend":
		return setDatabaseState(args[1:], hosting.StateSuspended)
	case "resume":
		return setDatabaseState(args[1:], hosting.StateActive)
	case "drop":
		return dropDatabase(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql database", "unknown database command")
	}
}

func createRealm(args []string) error {
	const op = "nextsql realm create"
	fs := flag.NewFlagSet("realm create", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realmName := fs.String("realm", "", "new realm name")
	databaseName := fs.String("database", "", "new realm's first database name")
	databaseKeyFile := fs.String("database-key-file", "", "root unlock key file for the new database (created if missing)")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages for the new database")
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
		*realmName = settings.Realm
	}
	if settings.Supplied["database"] {
		*databaseName = settings.Database
	}
	if *realmName == "" || *databaseName == "" {
		return cli.LocalMissing(op, "--realm and --database are required")
	}
	if *databaseKeyFile == "" {
		return cli.LocalMissing(op, "--database-key-file is required")
	}
	if *bufferPages < 1 {
		return nerr.New(nerr.InvalidArgument, op, "--buffer-pages must be positive")
	}

	m := reg.Manifest()
	ident, alreadyActive, err := resolveManagedDatabaseIdentity(m, *realmName, *databaseName)
	if err != nil {
		return err
	}
	if alreadyActive {
		fmt.Printf("realm %s database %s already active\n", *realmName, *databaseName)
		return nil
	}
	root, err := ensureDatabaseKeyFile(*databaseKeyFile)
	if err != nil {
		return err
	}
	defer root.Zero()

	realm, db, created, err := reg.CreateRealm(*realmName, *databaseName, ident, *databaseKeyFile)
	if err != nil {
		auditLocal(settings.DataDir, security.ActionRealmCreate, *realmName+"/"+*databaseName, err)
		return err
	}
	if err := activateManagedDatabase(reg, settings.DataDir, realm.ID, db.ID, ident, root, *bufferPages); err != nil {
		auditLocal(settings.DataDir, security.ActionRealmCreate, *realmName+"/"+*databaseName, err)
		return err
	}
	auditLocal(settings.DataDir, security.ActionRealmCreate, *realmName+"/"+*databaseName, nil)
	verb := "created"
	if !created {
		verb = "resumed"
	}
	fmt.Printf("realm %s %s database %s %s %s\n", realm.Name, realm.ID.String(), db.Name, db.ID.String(), verb)
	return nil
}

func createDatabase(args []string) error {
	const op = "nextsql database create"
	fs := flag.NewFlagSet("database create", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realmName := fs.String("realm", "", "existing realm name")
	databaseName := fs.String("name", "", "new database name")
	databaseKeyFile := fs.String("database-key-file", "", "root unlock key file for the new database (created if missing)")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages for the new database")
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
		*realmName = settings.Realm
	}
	if *realmName == "" || *databaseName == "" {
		return cli.LocalMissing(op, "--realm and --name are required")
	}
	if *databaseKeyFile == "" {
		return cli.LocalMissing(op, "--database-key-file is required")
	}
	if *bufferPages < 1 {
		return nerr.New(nerr.InvalidArgument, op, "--buffer-pages must be positive")
	}

	m := reg.Manifest()
	realmID, err := resolveRealmID(m, *realmName)
	if err != nil {
		return err
	}
	ident, alreadyActive, err := resolveManagedDatabaseIdentity(m, *realmName, *databaseName)
	if err != nil {
		return err
	}
	if alreadyActive {
		fmt.Printf("database %s already active\n", *databaseName)
		return nil
	}
	root, err := ensureDatabaseKeyFile(*databaseKeyFile)
	if err != nil {
		return err
	}
	defer root.Zero()

	db, created, err := reg.CreateDatabase(realmID, *databaseName, ident, *databaseKeyFile)
	if err != nil {
		auditLocal(settings.DataDir, security.ActionDatabaseCreate, *realmName+"/"+*databaseName, err)
		return err
	}
	if err := activateManagedDatabase(reg, settings.DataDir, realmID, db.ID, ident, root, *bufferPages); err != nil {
		auditLocal(settings.DataDir, security.ActionDatabaseCreate, *realmName+"/"+*databaseName, err)
		return err
	}
	auditLocal(settings.DataDir, security.ActionDatabaseCreate, *realmName+"/"+*databaseName, nil)
	verb := "created"
	if !created {
		verb = "resumed"
	}
	fmt.Printf("database %s %s %s\n", db.Name, db.ID.String(), verb)
	return nil
}

// resolveManagedDatabaseIdentity looks for realmName/databaseName already
// registered in m. When found and StateActive, alreadyActive is true (the
// caller should treat this as a successful no-op — createRealm/
// createDatabase are not re-run). When found and StateProvisioning (a
// crash-and-retry case), it returns that record's own already-durable
// Identity rather than generating a new one, so the retried
// Registry.CreateRealm/CreateDatabase call recognizes it as the same
// resumable attempt instead of a name collision. When not found, it
// generates a fresh identity for a first-time create.
func resolveManagedDatabaseIdentity(m hosting.Manifest, realmName, databaseName string) (ident format.Identity, alreadyActive bool, err error) {
	realmName = strings.ToLower(strings.TrimSpace(realmName))
	databaseName = strings.ToLower(strings.TrimSpace(databaseName))
	for _, realm := range m.Realms {
		if realm.Name != realmName {
			continue
		}
		for _, db := range realm.Databases {
			if db.Name != databaseName {
				continue
			}
			if db.State == hosting.StateActive {
				return format.Identity{}, true, nil
			}
			return db.Identity, false, nil
		}
	}
	ident, err = format.NewIdentity()
	return ident, false, err
}

// ensureDatabaseKeyFile creates path (mode 0600) with a fresh root key if it
// does not exist yet, matching nextsql init's own create-if-missing
// convention, then reads and returns it either way.
func ensureDatabaseKeyFile(path string) (*crypto.DEK, error) {
	const op = "nextsql database create"
	if _, err := os.Stat(path); os.IsNotExist(err) {
		createdRoot, err := crypto.CreateKeyFile(path, 1)
		if err != nil {
			return nil, err
		}
		createdRoot.Zero()
		fmt.Fprintf(os.Stderr, "created root key file %s (mode 0600); keep it off the data volume\n", path)
	} else if err != nil {
		return nil, nerr.Wrap(nerr.IO, op, "stat database key file", err)
	}
	return crypto.ReadKeyFile(path)
}

// activateManagedDatabase physically creates (or resumes creating) the
// managed database file for realmID/databaseID at its ID-based path
// (hosting.ManagedDatabasePath), then publishes it StateActive. Mirrors
// the create/verify/publish sequence nextsql init already uses for the
// single bootstrap default database (see createOrResumeDatabase),
// generalized to any additional managed database.
func activateManagedDatabase(reg *hosting.Registry, dataDir string, realmID, databaseID hosting.ID, ident format.Identity, root *crypto.DEK, bufferPages int) error {
	path := hosting.ManagedDatabasePath(dataDir, realmID, databaseID)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql database create", "mkdir", err)
	}
	db, env, err := createOrResumeDatabase(path, ident, root, bufferPages)
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		_ = env.Close()
		return err
	}
	_ = env.Close()
	return reg.SetDatabaseState(realmID, databaseID, hosting.StateActive)
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

// findManifestDatabase returns the full realm/database record for an
// already-resolved pair. resolveRealmID/resolveDatabaseID confirm a name
// resolves to an ID; callers that also need Layout/State (e.g. dropDatabase)
// use this instead of re-walking the manifest inline.
func findManifestDatabase(m hosting.Manifest, realmID, databaseID hosting.ID) (hosting.Realm, hosting.Database, bool) {
	for _, realm := range m.Realms {
		if realm.ID != realmID {
			continue
		}
		for _, db := range realm.Databases {
			if db.ID == databaseID {
				return realm, db, true
			}
		}
	}
	return hosting.Realm{}, hosting.Database{}, false
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

// setDatabaseState is M3-1's CLI surface for the two lifecycle transitions
// dbmanager's routing path (internal/hosting.Lookup) now actually enforces:
// suspend blocks every future connection to the database until resumed,
// resume restores it. Follows the exact same offline pattern as
// set-realm-cap/set-database-cap (openHostingRegistryForCLI's exclusive
// data-dir lock, so it fails Unavailable against a running nextsqld — a
// state edit is an overwrite, applied on the next restart, same as a cap
// edit; a live control-plane op to suspend/resume without a restart is the
// same documented follow-on as live cap changes). Rename and drop/
// tombstone are separate, still-open M3 items — not this increment's
// scope.
func setDatabaseState(args []string, target hosting.State) error {
	verb, past, action := "suspend", "suspended", security.ActionDatabaseSuspend
	if target == hosting.StateActive {
		verb, past, action = "resume", "resumed", security.ActionDatabaseResume
	}
	op := "nextsql database " + verb
	fs := flag.NewFlagSet("database "+verb, flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realm := fs.String("realm", "", "realm name")
	database := fs.String("database", "", "logical database name")
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
	object := strings.ToLower(strings.TrimSpace(*realm)) + "/" + strings.ToLower(strings.TrimSpace(*database))
	if err := reg.SetDatabaseState(realmID, databaseID, target); err != nil {
		auditLocal(settings.DataDir, action, object, err)
		return err
	}
	auditLocal(settings.DataDir, action, object, nil)
	fmt.Printf("realm %s database %s %s\n", strings.ToLower(strings.TrimSpace(*realm)), strings.ToLower(strings.TrimSpace(*database)), past)
	return nil
}

// dropDatabase is M3-3's CLI surface for the physical half of the delete
// lifecycle (docs/design-multidatabase-dbaas.md §16 M3-3). The
// StateDeleting/StateTombstoned states and Lookup's fail-closed handling of
// both already existed (state machine landed with M0/M1; Lookup's
// enforcement landed with M3-1, log #112) — the remaining gap was
// exclusively that nothing ever reclaimed a tombstoned managed database's
// on-disk files. Follows the exact same offline pattern as suspend/resume
// (openHostingRegistryForCLI's exclusive data-dir lock, so it fails
// Unavailable against a running nextsqld — there is no live connection to
// evict because the server cannot be up while this runs). Scoped to
// realm-managed (LayoutManaged) databases, never the deployment's default
// realm/database: LayoutLegacyDefault lives directly at DATA-DIR/nextsql.db
// with no per-ID directory to safely reclaim, and every tool that omits
// --realm/--database assumes that path exists; a declarative-manifest
// deployment's default database is LayoutManaged but is still rejected by
// the explicit default-pair check for the same reason. Idempotent: a
// database already StateTombstoned (e.g. a prior run that reclaimed the
// files but crashed before the final state write) reports success without
// erroring; a prior run that crashed after StateDeleting but before
// reclaiming files resumes cleanly (os.RemoveAll is idempotent, and
// CanTransition treats StateDeleting -> StateDeleting as a valid no-op).
// Rename (M3-2), realm-level delete, and reclaiming an *open* database's
// live buffer/task-pool footprint remain separate, still-open M3 items.
func dropDatabase(args []string) error {
	const op = "nextsql database drop"
	fs := flag.NewFlagSet("database drop", flag.ContinueOnError)
	fs.String("data-dir", "", "deployment data directory")
	fs.String("key-file", "", "database root unlock key file (used to locate KEY-FILE.instance)")
	fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	realm := fs.String("realm", "", "realm name")
	database := fs.String("database", "", "logical database name")
	confirm := fs.Bool("confirm", false, "confirm the irreversible delete")
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
	object := strings.ToLower(strings.TrimSpace(*realm)) + "/" + strings.ToLower(strings.TrimSpace(*database))
	if realmID == m.DefaultRealm && databaseID == m.DefaultDatabase {
		derr := nerr.New(nerr.InvalidArgument, op, "cannot drop the deployment default database")
		auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, derr)
		return derr
	}
	_, dbRec, found := findManifestDatabase(m, realmID, databaseID)
	if !found {
		return nerr.New(nerr.NotFound, op, "unknown database in realm")
	}
	if dbRec.Layout != hosting.LayoutManaged {
		lerr := nerr.New(nerr.InvalidArgument, op, "drop is only supported for realm-managed databases; the legacy default-layout database is out of scope")
		auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, lerr)
		return lerr
	}
	if dbRec.State == hosting.StateTombstoned {
		fmt.Printf("realm %s database %s already dropped\n",
			strings.ToLower(strings.TrimSpace(*realm)), strings.ToLower(strings.TrimSpace(*database)))
		return nil
	}
	if err := reg.SetDatabaseState(realmID, databaseID, hosting.StateDeleting); err != nil {
		auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, err)
		return err
	}
	dbPath := hosting.ManagedDatabasePath(settings.DataDir, realmID, databaseID)
	dir := filepath.Dir(dbPath)
	if _, statErr := os.Stat(dir); statErr == nil {
		if rmErr := os.RemoveAll(dir); rmErr != nil {
			rmErr = nerr.Wrap(nerr.IO, op, "remove managed database files", rmErr)
			auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, rmErr)
			return rmErr
		}
	} else if !os.IsNotExist(statErr) {
		statErr = nerr.Wrap(nerr.IO, op, "stat managed database directory", statErr)
		auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, statErr)
		return statErr
	}
	if err := reg.SetDatabaseState(realmID, databaseID, hosting.StateTombstoned); err != nil {
		auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, err)
		return err
	}
	auditLocal(settings.DataDir, security.ActionDatabaseDrop, object, nil)
	fmt.Printf("realm %s database %s dropped\n", strings.ToLower(strings.TrimSpace(*realm)), strings.ToLower(strings.TrimSpace(*database)))
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
	fs.String("hosting-manifest", "", "declarative multi-realm bootstrap manifest (or NEXTSQL_HOSTING_MANIFEST_FILE)")
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
	manifestPath := settings.HostingManifest
	if manifestPath != "" {
		if *dataDir == "" || (*keyFile == "" && *instanceKeyFile == "") {
			return cli.LocalMissing("nextsql init", "--data-dir and --key-file (or --instance-key-file) are required")
		}
	} else if *dataDir == "" || *keyFile == "" {
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
	if manifestPath != "" {
		return initFromManifest(manifestPath, *dataDir, *keyFile, *instanceKeyFile, *bufferPages, *user, *passwordFile, serverPass)
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
	if err := bootstrapDeploymentUser(*dataDir, *user, *passwordFile, serverPass); err != nil {
		return err
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

// bootstrapDeploymentUser upserts the optional deployment-wide bootstrap
// user with cluster ADMIN and database-wide CONNECT. A blank user is a
// no-op. Shared by the single-pair and declarative-manifest init paths.
func bootstrapDeploymentUser(dataDir, user, passwordFile, serverPass string) error {
	if user == "" {
		return nil
	}
	pw := serverPass
	if passwordFile != "" {
		var err error
		pw, err = auth.ReadPasswordFile(passwordFile)
		if err != nil {
			return err
		}
	} else if pw != "" {
		fmt.Fprintln(os.Stderr, "using NEXTSQL_SERVER_PASS from the environment; prefer NEXTSQL_SERVER_PASSWORD_FILE")
	}
	if pw == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql init", "--password-file, NEXTSQL_SERVER_PASSWORD_FILE, or NEXTSQL_SERVER_PASS is required with the bootstrap user")
	}
	store, err := auth.OpenOrCreate(filepath.Join(dataDir, config.AuthFileName))
	if err != nil {
		return err
	}
	if err := store.Upsert(user, pw); err != nil {
		return err
	}
	acl, err := security.OpenOrCreateACL(filepath.Join(dataDir, config.ACLFileName))
	if err != nil {
		return err
	}
	if err := acl.Grant(user, security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		return err
	}
	return acl.Grant(user, security.PrivConnect, security.ScopeDatabase, "")
}

// initFromManifest is the declarative multi-realm bootstrap path: it
// validates the whole manifest (and every referenced key file) before any
// mutation, creates any missing per-database root key file, publishes one
// registry generation containing every declared realm/database, then
// physically creates and activates each managed database. Re-running with
// an identical manifest is idempotent — already-ACTIVE databases are left
// untouched — and a partial run resumes cleanly. The deployment registry
// root is --instance-key-file, or KEY-FILE.instance when only --key-file is
// given (--key-file itself is never used as a database key here; each
// database's key comes from the manifest).
func initFromManifest(manifestPath, dataDir, keyFile, instanceKeyFile string, bufferPages int, user, passwordFile, serverPass string) error {
	const op = "nextsql init"
	if instanceKeyFile == "" {
		instanceKeyFile = keyFile + ".instance"
	}
	if _, err := os.Stat(instanceKeyFile); os.IsNotExist(err) {
		createdRoot, err := crypto.CreateKeyFile(instanceKeyFile, 1)
		if err != nil {
			return err
		}
		createdRoot.Zero()
		fmt.Fprintf(os.Stderr, "created deployment registry key file %s (mode 0600); keep it off the data volume\n", instanceKeyFile)
	} else if err != nil {
		return nerr.Wrap(nerr.IO, op, "stat deployment registry key", err)
	}
	instanceRoot, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		return err
	}
	defer instanceRoot.Zero()

	createdKeys, err := hosting.EnsureBootstrapManifestKeyFiles(manifestPath)
	if err != nil {
		return err
	}
	for _, p := range createdKeys {
		fmt.Fprintf(os.Stderr, "created database root key file %s (mode 0600); keep it off the data volume\n", p)
	}
	bootstrap, err := hosting.LoadDeploymentBootstrap(manifestPath)
	if err != nil {
		return err
	}

	reg, _, err := hosting.EnsureManifest(hosting.Path(dataDir), instanceRoot, func(deployment hosting.ID) (hosting.Manifest, error) {
		return bootstrap.RegistryManifest(deployment, hosting.StateProvisioning)
	})
	if err != nil {
		return err
	}
	defer reg.Close()

	m := reg.Manifest()
	for _, realm := range m.Realms {
		for _, db := range realm.Databases {
			if db.State == hosting.StateActive {
				fmt.Printf("realm %s database %s already active\n", realm.Name, db.Name)
				continue
			}
			if db.State != hosting.StateProvisioning {
				return nerr.New(nerr.Conflict, op, "managed database "+realm.Name+"/"+db.Name+" is not resumable from its current state")
			}
			dbRoot, err := crypto.ReadKeyFile(db.KeyRef)
			if err != nil {
				return err
			}
			err = activateManagedDatabase(reg, dataDir, realm.ID, db.ID, db.Identity, dbRoot, bufferPages)
			dbRoot.Zero()
			if err != nil {
				return err
			}
			fmt.Printf("realm %s database %s %s\n", realm.Name, db.Name, db.ID.String())
		}
	}

	if err := bootstrapDeploymentUser(dataDir, user, passwordFile, serverPass); err != nil {
		return err
	}
	defaultRealm, defaultDatabase, err := reg.Default()
	if err != nil {
		return err
	}
	fmt.Printf("initialized deployment %s\ndefault realm %s %s\ndefault database %s\n",
		m.DeploymentID.String(), defaultRealm.Name, defaultRealm.ID.String(), defaultDatabase.Name)
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
	fs.String("realm", "", "hosted realm name (default: the deployment's default realm)")
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
	jsonOut := fs.Bool("json", false, "print the result as a single JSON object instead of tab-separated text")
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
	printResult(res, *jsonOut)
	return nil
}

// printResult renders a query result either as tab-separated text
// (printTabularResult) or, with jsonOut, as a single JSON object
// (printJSONResult) — shared by `nextsql exec` and the `nextsql cluster`
// admin subcommands.
func printResult(res *nextsql.Result, jsonOut bool) {
	if jsonOut {
		printJSONResultTo(os.Stdout, res)
		return
	}
	printTabularResult(res)
}

// printTabularResult renders a query result as machine-readable
// tab-separated output: an optional header/row block followed by an
// "affected N" line.
func printTabularResult(res *nextsql.Result) {
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
}

// printJSONResultTo renders a query result as a single JSON object
// {"columns": [...], "rows": [[...]], "affected": N} on one line — a
// structured counterpart to printTabularResult's TSV for scripts that would
// rather decode JSON than parse tab-separated text positionally. Cell
// values are stringified the same way the TSV path renders them
// (types.Value.String()); no attempt is made to reproduce native JSON
// types per SQL type, keeping the shape stable across every result kind.
func printJSONResultTo(w io.Writer, res *nextsql.Result) {
	rows := make([][]string, len(res.Rows))
	for i, row := range res.Rows {
		cols := make([]string, len(row))
		for j, v := range row {
			cols[j] = v.String()
		}
		rows[i] = cols
	}
	out := struct {
		Columns  []string   `json:"columns"`
		Rows     [][]string `json:"rows"`
		Affected int64      `json:"affected"`
	}{Columns: res.Columns, Rows: rows, Affected: res.Affected}
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(out)
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

// backupCmd dispatches `nextsql backup ...`. A bare-word first argument
// ("list"/"prune") is a retention-management subcommand; anything else
// (starting with "-", or no arguments) is the original, flag-first
// `nextsql backup --data-dir ... --out ...` invocation, preserved exactly
// as before for backward compatibility.
func backupCmd(args []string) error {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "list":
			return backupList(args[1:])
		case "prune":
			return backupPrune(args[1:])
		default:
			return nerr.New(nerr.InvalidArgument, "nextsql backup", "unknown backup command")
		}
	}
	return backupDB(args)
}

// backupList enumerates the backups directly under --base-dir (each
// immediate subdirectory ReadHeader succeeds on — see
// backup.ListBackups), oldest first.
func backupList(args []string) error {
	fs := flag.NewFlagSet("backup list", flag.ContinueOnError)
	baseDir := fs.String("base-dir", "", "directory containing one or more backups as immediate subdirectories")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *baseDir == "" {
		return cli.LocalMissing("nextsql backup list", "--base-dir is required")
	}
	backups, err := backup.ListBackups(*baseDir)
	if err != nil {
		return err
	}
	fmt.Println("path\tcreated\tdatabase\tdurable_lsn")
	for _, b := range backups {
		fmt.Printf("%s\t%s\t%s\t%d\n", b.Path, b.Created().Format(time.RFC3339), b.Header.Identity.DatabaseString(), b.Header.DurableLSN)
	}
	return nil
}

// backupPrune applies a retention policy (--keep-count or --keep-days,
// mutually exclusive) to the backups under --base-dir and, only with
// --confirm, deletes the ones the policy selects (backup.SelectPruneCandidates
// — oldest first, always leaving at least the single newest backup no
// matter how it ages). Without --confirm it only previews what would be
// removed, so the default invocation is always safe to run.
func backupPrune(args []string) error {
	fs := flag.NewFlagSet("backup prune", flag.ContinueOnError)
	baseDir := fs.String("base-dir", "", "directory containing one or more backups as immediate subdirectories")
	keepCount := fs.Int("keep-count", 0, "keep the N newest backups, prune the rest")
	keepDays := fs.Float64("keep-days", 0, "keep backups created within this many days of now, prune the rest")
	confirm := fs.Bool("confirm", false, "actually delete; without this, only preview what would be removed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *baseDir == "" {
		return cli.LocalMissing("nextsql backup prune", "--base-dir is required")
	}
	if (*keepCount > 0) == (*keepDays > 0) {
		return nerr.New(nerr.InvalidArgument, "nextsql backup prune", "exactly one of --keep-count or --keep-days is required")
	}
	backups, err := backup.ListBackups(*baseDir)
	if err != nil {
		return err
	}
	policy := backup.RetentionPolicy{KeepCount: *keepCount, KeepFor: time.Duration(*keepDays * float64(24*time.Hour))}
	candidates := backup.SelectPruneCandidates(backups, policy, time.Now())
	if len(candidates) == 0 {
		fmt.Println("nothing to prune")
		return nil
	}
	if !*confirm {
		fmt.Printf("would prune %d of %d backups (pass --confirm to delete):\n", len(candidates), len(backups))
		for _, b := range candidates {
			fmt.Printf("%s\t%s\n", b.Path, b.Created().Format(time.RFC3339))
		}
		return nil
	}
	pruned := 0
	for _, b := range candidates {
		err := os.RemoveAll(b.Path)
		auditLocal(*baseDir, security.ActionBackupPrune, b.Path, err)
		if err != nil {
			return nerr.Wrap(nerr.IO, "nextsql backup prune", "remove "+b.Path, err)
		}
		fmt.Printf("pruned %s\n", b.Path)
		pruned++
	}
	fmt.Printf("pruned %d of %d backups\n", pruned, len(backups))
	return nil
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
		return nerr.New(nerr.InvalidArgument, "nextsql cluster", "expected status, transfer-leader, drain, maintenance, or reconcile")
	}
	switch args[0] {
	case "status":
		fs := flag.NewFlagSet("cluster status", flag.ContinueOnError)
		dataDir := fs.String("data-dir", "", "data directory")
		jsonOut := fs.Bool("json", false, "print status as a single JSON object instead of plain text")
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
		if *jsonOut {
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			return enc.Encode(st)
		}
		fmt.Printf("node %s\nstate %s\nleader %s\nleader_addr %s\napplied_lsn %d\nvoters %d\nhas_leader %t\n",
			st.NodeID, st.State, st.LeaderID, st.Leader, st.Applied, st.Voters, st.HasLeader)
		return nil
	case "transfer-leader":
		fs, jsonOut := clusterConnFlags("cluster transfer-leader")
		s, err := resolveClusterConn(fs, args[1:])
		if err != nil {
			return err
		}
		conn, err := cli.Open(context.Background(), s)
		if err != nil {
			return err
		}
		defer conn.Close()
		res, err := conn.Exec(context.Background(), `CLUSTER TRANSFER LEADER`)
		if err != nil {
			return err
		}
		printResult(res, *jsonOut)
		return nil
	case "drain":
		fs, jsonOut := clusterConnFlags("cluster drain")
		timeoutMS := fs.Int64("timeout-ms", 0, "drain deadline in milliseconds (0 = use the server's configured shutdown_drain_ms)")
		s, err := resolveClusterConn(fs, args[1:])
		if err != nil {
			return err
		}
		conn, err := cli.Open(context.Background(), s)
		if err != nil {
			return err
		}
		defer conn.Close()
		sql := "CLUSTER DRAIN"
		if *timeoutMS > 0 {
			sql = fmt.Sprintf("CLUSTER DRAIN WITH (TIMEOUT_MS = %d)", *timeoutMS)
		}
		res, err := conn.Exec(context.Background(), sql)
		if err != nil {
			return err
		}
		printResult(res, *jsonOut)
		return nil
	case "maintenance":
		if len(args) < 2 {
			return nerr.New(nerr.InvalidArgument, "nextsql cluster maintenance", "expected enable or disable")
		}
		var sql string
		switch args[1] {
		case "enable":
			sql = "CLUSTER MAINTENANCE ENABLE"
		case "disable":
			sql = "CLUSTER MAINTENANCE DISABLE"
		default:
			return nerr.New(nerr.InvalidArgument, "nextsql cluster maintenance", "expected enable or disable")
		}
		fs, jsonOut := clusterConnFlags("cluster maintenance " + args[1])
		s, err := resolveClusterConn(fs, args[2:])
		if err != nil {
			return err
		}
		conn, err := cli.Open(context.Background(), s)
		if err != nil {
			return err
		}
		defer conn.Close()
		res, err := conn.Exec(context.Background(), sql)
		if err != nil {
			return err
		}
		printResult(res, *jsonOut)
		return nil
	case "reconcile":
		if len(args) < 2 || args[1] != "confirm" {
			return nerr.New(nerr.InvalidArgument, "nextsql cluster reconcile", "expected confirm")
		}
		fs, jsonOut := clusterConnFlags("cluster reconcile confirm")
		s, err := resolveClusterConn(fs, args[2:])
		if err != nil {
			return err
		}
		conn, err := cli.Open(context.Background(), s)
		if err != nil {
			return err
		}
		defer conn.Close()
		res, err := conn.Exec(context.Background(), `CLUSTER RECONCILE CONFIRM`)
		if err != nil {
			return err
		}
		printResult(res, *jsonOut)
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql cluster", "unknown cluster command")
	}
}

// clusterConnFlags builds the standard live-server connection flag set
// (address, credentials, TLS, dotenv) shared by the `nextsql cluster`
// admin subcommands that must reach a running leader over the native
// protocol rather than reading a node's local data directory. The returned
// *bool reports --json, parsed once resolveClusterConn runs fs.Parse.
func clusterConnFlags(name string) (*flag.FlagSet, *bool) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
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
	fs.String("data-dir", "", "not valid here (local commands only)")
	fs.String("key-file", "", "not valid here (local commands only)")
	jsonOut := fs.Bool("json", false, "print the result as a single JSON object instead of tab-separated text")
	return fs, jsonOut
}

func resolveClusterConn(fs *flag.FlagSet, args []string) (cli.Settings, error) {
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
  nextsql setup --data-dir DIR --key-file FILE [--preset conservative|balanced|high-performance|custom]
               [--buffer-pages N] [--listen HOST:PORT [--tls-cert FILE --tls-key FILE]]
               [--user NAME --password-file FILE] [--config-in FILE] [--config-out FILE]
               [--json] [--dry-run] [--force] [--skip-init]
  nextsql lifecycle detect --data-dir DIR [--config FILE] [--json]
  nextsql lifecycle preflight --data-dir DIR [--json]
  nextsql lifecycle backup-config --config FILE [--out DIR] [--json]
  nextsql lifecycle upgrade --data-dir DIR --key-file FILE [--config FILE]
               [--buffer-pages N] [--dry-run] [--json]
  nextsql lifecycle repair --data-dir DIR --key-file FILE [--config FILE]
               [--preset P] [--buffer-pages N] [--listen HOST:PORT] [--force-config]
               [--fix-perms] [--dry-run] [--json]
  nextsql lifecycle uninstall --data-dir DIR [--config FILE] [--key-file FILE]
               [--purge-data] [--purge-keys] [--confirm] [--json]
  nextsql hosting adopt --data-dir DIR --key-file FILE [--instance-key-file FILE]
               [--realm NAME --database NAME] --confirm [--env-file PATH | --no-env]
  nextsql hosting migrate-tenant --source-data-dir DIR --source-key-file FILE --tenant VALUE
               --data-dir DIR --key-file FILE [--instance-key-file FILE]
               [--realm NAME --database NAME] [--batch-rows N] --confirm
  nextsql realm create --data-dir DIR --key-file FILE [--instance-key-file FILE]
               --realm NAME --database NAME --database-key-file FILE [--buffer-pages N]
  nextsql database create --data-dir DIR --key-file FILE [--instance-key-file FILE]
               --realm NAME --name NAME --database-key-file FILE [--buffer-pages N]
  nextsql database suspend|resume --data-dir DIR --key-file FILE [--instance-key-file FILE]
               --realm NAME --database NAME --confirm
  nextsql database drop --data-dir DIR --key-file FILE [--instance-key-file FILE]
               --realm NAME --database NAME --confirm
  nextsql login --idp NAME [--addr HOST:PORT] [--idp-config FILE]
	           [--database NAME] [--realm NAME] [--no-browser] [--timeout DURATION]
	           [--client-credentials [--client-secret-file FILE]]
  nextsql logout (--idp NAME --addr HOST:PORT | --all)
  nextsql whoami --idp NAME [--addr HOST:PORT] [--idp-config FILE] [--json]
  nextsql exec [--addr HOST:PORT] [--user NAME] [--password-file FILE | --idp NAME] [--realm NAME] [--database NAME]
               [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
               [--env-file PATH | --no-env] [--json]
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
  nextsql backup list --base-dir DIR
  nextsql backup prune --base-dir DIR (--keep-count N | --keep-days N) [--confirm]
  nextsql restore --from DIR --data-dir DIR --key-file FILE [--wal-archive DIR] [--until-lsn N | --until RFC3339]
  nextsql verify --from DIR --key-file FILE
  nextsql export --data-dir DIR --key-file FILE --out DIR
  nextsql import --from DIR --data-dir DIR --key-file FILE
  nextsql diagnose --data-dir DIR
  nextsql status [--addr HOST:PORT] [--user NAME] [--password-file FILE | --idp NAME]
                 [--database NAME]
                 [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
                 [--env-file PATH | --no-env]
  nextsql status --local [--data-dir DIR] [--key-file FILE]
  nextsql cluster status --data-dir DIR [--json]
  nextsql cluster transfer-leader [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--json]
                 [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
                 [--env-file PATH | --no-env]
  nextsql cluster drain [--timeout-ms N] [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--json]
                 [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
                 [--env-file PATH | --no-env]
  nextsql cluster maintenance enable|disable [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--json]
                 [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
                 [--env-file PATH | --no-env]
  nextsql cluster reconcile confirm [--addr HOST:PORT] [--user NAME] [--password-file FILE]
                 [--database NAME] [--json]
                 [--tls-ca FILE [--tls-server-name NAME] [--tls-client-cert FILE --tls-client-key FILE] | --insecure]
                 [--env-file PATH | --no-env]
  nextsql token keygen|rotate|retire|list-keys|export-public|mint|revoke|verify
  nextsql audit keygen|rotate|retire|list-keys|export-public --keyset FILE
  nextsql audit verify --file FILE [--keyset FILE | --pubkey FILE] [--json]
  nextsql version
  nextsql help

--key-file is the external root unlock key. It is never stored in the data directory.
Keys are never accepted in connection URLs. Use --key-file / KeyProvider.
A backup is not valid until verify (including a restore test) succeeds.
An export is not valid until verify (including an import test) succeeds.
`, version.String)
}
