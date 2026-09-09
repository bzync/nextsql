package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/setup"
	"github.com/bzync/nextsql/internal/storage/integrity"
	"github.com/bzync/nextsql/internal/sysinfo"
	"github.com/bzync/nextsql/internal/undo"
	"github.com/bzync/nextsql/internal/upgrade"
	"github.com/bzync/nextsql/internal/version"
	"github.com/bzync/nextsql/internal/wal"
)

// setupCmd is the non-interactive installer backbone (P28). It detects the
// host's hardware, sizes a buffer pool from a resource preset, writes a
// validated config file with secure defaults, initializes the database
// through the same path as `nextsql init`, and verifies the result — all
// scriptable, with machine-readable output and the standard exit codes.
func setupCmd(args []string) error {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "directory for nextsql.db (created if missing)")
	keyFile := fs.String("key-file", "", "root unlock key file (created if missing; keep it off the data volume)")
	instanceKeyFile := fs.String("instance-key-file", "", "deployment registry root key file (default KEY-FILE.instance)")
	preset := fs.String("preset", "", "resource preset: conservative | balanced | high-performance | custom (default balanced)")
	profile := fs.String("profile", "", "deployment profile: developer | production (default developer)")
	bufferPages := fs.Int("buffer-pages", 0, "explicit buffer pool pages (overrides the preset)")
	listen := fs.String("listen", config.DefaultListenAddr, "listen address; a non-loopback address requires --tls-cert/--tls-key")
	logLevel := fs.String("log-level", config.DefaultLogLevel, "log level: debug | info | warn | error")
	tlsCert := fs.String("tls-cert", "", "TLS 1.3 certificate (PEM) for a remote listen address")
	tlsKey := fs.String("tls-key", "", "TLS 1.3 private key (PEM) for a remote listen address")
	user := fs.String("user", "", "bootstrap administrator user (recommended)")
	passwordFile := fs.String("password-file", "", "password file for --user (never a URL)")
	databaseName := fs.String("database", "", "name the deployment's database and create it; unset initializes the deployment only (keys and administrator), with no database")
	configIn := fs.String("config-in", "", "load defaults from this key=value config file before applying flags")
	configOut := fs.String("config-out", "", "where to write the generated config (default DATA-DIR/nextsql.conf)")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object instead of text")
	dryRun := fs.Bool("dry-run", false, "compute and print the plan without creating or writing anything")
	force := fs.Bool("force", false, "overwrite an existing config file")
	skipInit := fs.Bool("skip-init", false, "generate the config only; do not initialize the database")
	keepFailed := fs.Bool("keep-failed", false, "on failure, leave a partial install in place instead of rolling it back")
	recoveryKeyOut := fs.String("recovery-key-out", "", "also generate a recovery key (a second, independent unlock key for the same database) and write it here; must not exist")
	instanceRecoveryKeyOut := fs.String("instance-recovery-key-out", "", "recovery key for the deployment-registry keystore (default RECOVERY-KEY-OUT.instance)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql setup", "--data-dir and --key-file are required")
	}
	if (*user == "") != (*passwordFile == "") {
		return nerr.New(nerr.InvalidArgument, "nextsql setup", "--user and --password-file must be given together")
	}
	// A deployment has two independently sealed keystores and losing either
	// root is fatal on its own (see `nextsql key --keystore`), so recovery is
	// requested for both or neither: --instance-recovery-key-out only
	// relocates the second file, it never selects one keystore alone.
	if *instanceRecoveryKeyOut != "" && *recoveryKeyOut == "" {
		return cli.Validation("nextsql setup",
			"--instance-recovery-key-out requires --recovery-key-out: a deployment's two keystores get recovery keys together or not at all")
	}

	presetVal, err := setup.ParsePreset(*preset)
	if err != nil {
		return err
	}
	profileVal, err := config.ParseDeploymentProfile(*profile)
	if err != nil {
		return cli.Validation("nextsql setup", err.Error())
	}
	if profileVal == config.ProfileProduction && *skipInit {
		return cli.Validation("nextsql setup", "production profile cannot use --skip-init: initialize the database with --user/--password-file")
	}
	if *recoveryKeyOut != "" && *skipInit {
		return cli.Validation("nextsql setup",
			"--recovery-key-out cannot be used with --skip-init: no keystore is created for a recovery key to seal, so run `nextsql key add-recovery` after the database exists")
	}
	// A deployment can be initialized without a database (`nextsql init`'s
	// own rule: no --database, no database). Two things then have nothing to
	// act on, and both say so rather than half-working: a recovery key has no
	// keystore to seal, and a production install would write a configuration
	// for a server that refuses to start.
	if *recoveryKeyOut != "" && !*skipInit && *databaseName == "" {
		return cli.Validation("nextsql setup",
			"--recovery-key-out requires --database: without one no database (and so no keystore) is created for a recovery key to seal")
	}
	if profileVal == config.ProfileProduction && *databaseName == "" {
		return cli.Validation("nextsql setup",
			"production profile requires --database: a deployment with no database cannot serve, and nextsqld fails closed on one")
	}

	base := config.Default()
	if *configIn != "" {
		loaded, err := config.Load(*configIn)
		if err != nil {
			return err
		}
		base = loaded
	}

	confPath := *configOut
	if confPath == "" {
		confPath = filepath.Join(*dataDir, "nextsql.conf")
	}

	info, err := sysinfo.Detect(*dataDir)
	if err != nil {
		return err
	}

	plan, err := setup.BuildPlan(setup.Params{
		Base:            base,
		Info:            info,
		Preset:          presetVal,
		Profile:         profileVal,
		DataDir:         *dataDir,
		KeyFile:         *keyFile,
		InstanceKeyFile: *instanceKeyFile,
		ListenAddr:      *listen,
		LogLevel:        *logLevel,
		TLSCert:         *tlsCert,
		TLSKey:          *tlsKey,
		BufferPages:     *bufferPages,
		AdminUser:       *user,
		ConfigPath:      confPath,
		RunInit:         !*skipInit,
	})
	if err != nil {
		if errors.Is(err, setup.ErrInsecureRemote) || nerr.HasCode(err, nerr.InvalidArgument) {
			return cli.Validation("nextsql setup", err.Error())
		}
		return err
	}

	// Stat, never mutate: this is the same generate-vs-import disclosure for
	// both --dry-run and a real run, computed before `nextsql init` (which
	// would otherwise generate a missing key file itself) ever runs. A
	// pre-existing file is always imported/reused, never regenerated or
	// overwritten — this only makes that already-true behavior explicit to
	// the operator instead of leaving it implicit (`TODO.md` Phase 28
	// "Installer UX" — generate/import root unlock key).
	keyFileExists := statExists(*keyFile)
	instanceKeyExists := statExists(plan.InstanceKeyFile)

	// Resolved before anything is written, so --dry-run refuses exactly what
	// a real run would: the GUI treats the dry run as the authoritative
	// validator, and an install that fails only after creating a database
	// would leave the operator with a rollback instead of an answer.
	recoveryExports, err := planRecoveryExports(*recoveryKeyOut, *instanceRecoveryKeyOut, plan)
	if err != nil {
		return err
	}

	result := setupResult{
		NextSQLVersion:          version.String,
		Phase:                   version.Phase,
		Hardware:                plan.Info,
		Recommendation:          plan.Recommendation,
		ConfigPath:              plan.ConfigPath,
		ListenAddr:              plan.ListenAddr,
		TLS:                     plan.TLS,
		DataDir:                 plan.DataDir,
		KeyFile:                 plan.KeyFile,
		KeyFileExists:           keyFileExists,
		InstanceKey:             plan.InstanceKeyFile,
		InstanceKeyExists:       instanceKeyExists,
		AdminUser:               plan.AdminUser,
		Profile:                 plan.Profile,
		RecoveryKeyFile:         exportPathFor(recoveryExports, keystoreDatabase),
		InstanceRecoveryKeyFile: exportPathFor(recoveryExports, keystoreInstance),
		Warnings: append(append(append([]string{}, plan.Warnings...),
			keyFileAdvisories(*keyFile, keyFileExists, plan.InstanceKeyFile, instanceKeyExists)...),
			recoveryKeyAdvisories(recoveryExports, plan.RunInit)...),
		DryRun: *dryRun,
	}

	if !*dryRun && profileVal == config.ProfileProduction && *user == "" {
		return cli.Validation("nextsql setup", "production profile requires --user and --password-file")
	}

	if *dryRun {
		result.Plan = "dry-run: nothing was created, written, or initialized"
		return emitSetup(result, *jsonOut)
	}

	// Refuse to clobber an existing config unless it is semantically
	// identical to what we would write, or --force is given.
	if _, statErr := os.Stat(confPath); statErr == nil {
		if !*force {
			existing, loadErr := config.Load(confPath)
			if loadErr != nil || !reflect.DeepEqual(existing, plan.Config) {
				return nerr.New(nerr.AlreadyExists, "nextsql setup",
					"config file "+confPath+" already exists; pass --force to overwrite")
			}
		}
	} else if !os.IsNotExist(statErr) {
		return nerr.Wrap(nerr.IO, "nextsql setup", "stat config", statErr)
	}

	// Transactional rollback: observe every path this run might create
	// *before* touching anything, so a failure undoes only what we made and
	// never a pre-existing data directory or an operator-supplied key.
	rb := setup.NewInstallRollback()
	rollbackPaths := installArtifactPaths(*dataDir, *keyFile, plan.InstanceKeyFile)
	for _, ex := range recoveryExports {
		rollbackPaths = append(rollbackPaths, ex.Out)
	}
	for _, p := range append(rollbackPaths, *dataDir, confPath) {
		_, statErr := os.Stat(p)
		rb.Observe(p, statErr == nil)
	}

	failed := func(err error) error {
		result.RolledBack, result.RollbackKept = runInstallRollback(rb, *dataDir, *keepFailed)
		if *jsonOut {
			_ = emitSetup(result, true)
		}
		return err
	}

	if err := os.MkdirAll(*dataDir, 0o755); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql setup", "mkdir data-dir", err)
	}
	rb.Track(*dataDir)

	if plan.RunInit {
		if _, err := os.Stat(filepath.Join(*dataDir, config.DataFileName)); err == nil {
			return nerr.New(nerr.AlreadyExists, "nextsql setup",
				"data directory already contains an initialized database; pass --skip-init to only regenerate the config, "+
					"or use `nextsql registry` for upgrade/repair")
		} else if !os.IsNotExist(err) {
			return nerr.Wrap(nerr.IO, "nextsql setup", "stat database", err)
		}

		initArgs := []string{
			"--no-env",
			"--data-dir", *dataDir,
			"--key-file", *keyFile,
			"--buffer-pages", strconv.Itoa(plan.Config.BufferPages),
		}
		if *databaseName != "" {
			initArgs = append(initArgs, "--database", *databaseName)
		}
		if plan.InstanceKeyFile != "" {
			initArgs = append(initArgs, "--instance-key-file", plan.InstanceKeyFile)
		}
		if *user != "" {
			initArgs = append(initArgs, "--user", *user, "--password-file", *passwordFile)
		}
		out, initErr := captureStdout(func() error { return initDB(initArgs) })
		// init may have created some of these before failing; track whatever
		// now exists so a partial init is cleaned up too.
		for _, p := range installArtifactPaths(*dataDir, *keyFile, plan.InstanceKeyFile) {
			if _, err := os.Stat(p); err == nil {
				rb.Track(p)
			}
		}
		if initErr != nil {
			return failed(initErr)
		}
		result.InitOutput = strings.TrimSpace(out)
		// Initialized means "a database exists now". A deployment-only run
		// initialized the deployment, not a database, and must not claim
		// otherwise — the Setup wizard and `nextsql lifecycle detect` both
		// read this field.
		result.Initialized = *databaseName != ""
		result.DatabaseName = *databaseName
	}

	// Sealing a recovery key needs the keystores `nextsql init` just made, so
	// this runs after init and before the config is written: a failure here
	// rolls the whole install back rather than handing over a database the
	// operator believes is recoverable and is not.
	if len(recoveryExports) > 0 {
		for _, ex := range recoveryExports {
			rb.Track(ex.Out) // tracked before the write, so a partial file is cleaned up too
			if err := exportRecoveryKey(ex); err != nil {
				return failed(err)
			}
			warnIfUnderDataDir("nextsql setup", ex.Out, *dataDir)
		}
		result.RecoveryKeysCreated = true
	}

	if err := writeConfigFile(confPath, plan.Config); err != nil {
		return failed(err)
	}
	rb.Track(confPath)
	result.ConfigWritten = true

	// A deployment-only install (no --database) creates nothing to open, so
	// there is no health to check; reporting a health result for it would be
	// an assertion about a database that does not exist.
	if plan.RunInit && *databaseName != "" {
		health, err := verifySetupHealth(*dataDir, *keyFile, plan.Config.BufferPages)
		if err != nil {
			return failed(err)
		}
		result.Health = &health
		if !health.OK {
			return failed(nerr.New(nerr.InvalidFormat, "nextsql setup", "post-install health check failed"))
		}
	}

	return emitSetup(result, *jsonOut)
}

// installArtifactPaths is every filesystem path `nextsql init` may create for
// a single-pair deployment: the primary database and its sidecars, the
// deployment-registry database, the deployment lock, the auth/ACL files, and
// the two external key files. `nextsql lifecycle uninstall` enumerates the
// same set (with per-path labels) independently.
func installArtifactPaths(dataDir, keyFile, instanceKeyFile string) []string {
	dbPath := filepath.Join(dataDir, config.DataFileName)
	regPath := hosting.Path(dataDir)
	paths := []string{
		dbPath,
		crypto.KeystorePath(dbPath),
		wal.DirFor(dbPath),
		undo.DirFor(dbPath),
		integrity.PathFor(dbPath),
		filepath.Join(dataDir, config.AuthFileName),
		filepath.Join(dataDir, config.ACLFileName),
		regPath,
		crypto.KeystorePath(regPath),
		wal.DirFor(regPath),
		undo.DirFor(regPath),
		hosting.LockPath(dataDir),
	}
	if keyFile != "" {
		paths = append(paths, keyFile)
	}
	if instanceKeyFile != "" {
		paths = append(paths, instanceKeyFile)
	} else if keyFile != "" {
		paths = append(paths, keyFile+".instance")
	}
	return paths
}

// runInstallRollback removes the paths a failed setup run created, newest
// first. The data directory itself is removed only if it comes out empty
// (os.Remove, not RemoveAll) so an operator's pre-populated directory is
// never destroyed. Returns the paths removed and, when --keep-failed was
// given, the paths deliberately left behind.
func runInstallRollback(rb *setup.InstallRollback, dataDir string, keep bool) (removed, kept []string) {
	plan := rb.Plan()
	if keep {
		if len(plan) > 0 {
			fmt.Fprintf(os.Stderr, "setup failed; --keep-failed left %d partial path(s) in place (see `nextsql lifecycle uninstall`)\n", len(plan))
		}
		return nil, plan
	}
	for _, p := range plan {
		var err error
		if p == dataDir {
			err = os.Remove(p) // empty-only: never destroy a pre-populated dir
		} else {
			err = os.RemoveAll(p)
		}
		if err == nil || os.IsNotExist(err) {
			removed = append(removed, p)
		}
	}
	if len(removed) > 0 {
		fmt.Fprintf(os.Stderr, "setup failed; rolled back %d created path(s)\n", len(removed))
	}
	return removed, nil
}

// setupResult is the full outcome, rendered as text or as one JSON object.
type setupResult struct {
	NextSQLVersion string               `json:"nextsql_version"`
	Phase          int                  `json:"phase"`
	Hardware       sysinfo.Info         `json:"hardware"`
	Recommendation setup.Recommendation `json:"recommendation"`
	ConfigPath     string               `json:"config_path"`
	ConfigWritten  bool                 `json:"config_written"`
	ListenAddr     string               `json:"listen_addr"`
	TLS            bool                 `json:"tls"`
	DataDir        string               `json:"data_dir"`
	KeyFile        string               `json:"key_file"`
	// KeyFileExists reports whether --key-file already existed on disk at
	// plan time: true means it will be imported and reused as-is, false
	// means a new root unlock key will be generated there. Never flips a
	// generate into an overwrite either way — this is disclosure, not a
	// switch; see keyFileAdvisories.
	KeyFileExists     bool   `json:"key_file_exists"`
	InstanceKey       string `json:"instance_key_file"`
	InstanceKeyExists bool   `json:"instance_key_exists"`
	// RecoveryKeyFile/InstanceRecoveryKeyFile are where this run will write
	// (or, after a successful run, did write) a recovery key for each of the
	// deployment's two keystores. Empty means none was requested — the
	// pre-existing behavior, where the root unlock key is the only way in.
	// RecoveryKeysCreated is true only once both files exist and each has
	// been verified to actually unlock its keystore from disk.
	RecoveryKeyFile         string `json:"recovery_key_file,omitempty"`
	InstanceRecoveryKeyFile string `json:"instance_recovery_key_file,omitempty"`
	RecoveryKeysCreated     bool   `json:"recovery_keys_created"`
	AdminUser               string `json:"admin_user,omitempty"`
	Profile                 string `json:"profile"`
	Initialized             bool   `json:"initialized"`
	// DatabaseName is the deployment's database, empty when this run made a
	// deployment with no database (no --database).
	DatabaseName string       `json:"database_name,omitempty"`
	InitOutput   string       `json:"init_output,omitempty"`
	Health       *setupHealth `json:"health,omitempty"`
	Warnings     []string     `json:"warnings"`
	DryRun       bool         `json:"dry_run"`
	Plan         string       `json:"plan,omitempty"`
	RolledBack   []string     `json:"rolled_back,omitempty"`
	RollbackKept []string     `json:"rollback_kept,omitempty"`
}

// statExists reports whether path names an existing filesystem entry. It
// deliberately collapses every stat error other than "not found" to false —
// callers only use this for advisory disclosure text, never as a security or
// correctness gate (the actual create-if-missing logic in `nextsql init`
// re-stats and handles its own errors independently).
func statExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

// keyFileAdvisories makes the already-true generate-vs-import behavior of
// `nextsql init` explicit instead of leaving it implicit: a key file that
// exists is always imported and reused, one that doesn't is always
// generated fresh — nothing here changes that, it only tells the operator
// which one is about to happen before they confirm.
func keyFileAdvisories(keyFile string, keyExists bool, instanceKeyFile string, instanceExists bool) []string {
	var w []string
	if keyFile != "" {
		if keyExists {
			w = append(w, "an existing root unlock key file was found at "+keyFile+": it will be imported and reused, not regenerated or overwritten")
		} else {
			w = append(w, "no key file found at "+keyFile+": a new root unlock key will be generated there")
		}
	}
	if instanceKeyFile != "" {
		if instanceExists {
			w = append(w, "an existing deployment-registry key file was found at "+instanceKeyFile+": it will be imported and reused")
		} else {
			w = append(w, "no deployment-registry key file found at "+instanceKeyFile+": a new one will be generated there")
		}
	}
	return w
}

// recoveryExport is one keystore's recovery-key export, fully resolved
// before the run touches the filesystem: which keystore, the root unlock key
// that must open it, the keystore file itself, and where the exported key
// goes.
type recoveryExport struct {
	Keystore     string
	KeyFile      string
	KeystorePath string
	Out          string
}

// planRecoveryExports resolves --recovery-key-out into one export per
// keystore, or nothing at all when the flag was not given. A deployment's
// database and registry keystores are sealed under separate roots and losing
// either is fatal alone, so both get a recovery key — the instance path
// defaults beside the first exactly as --instance-key-file defaults beside
// --key-file.
//
// It refuses up front rather than after `nextsql init`: an output path that
// already exists, or any two of the four key paths naming the same file
// (which would either clobber a key or seal a "recovery" key that is the
// root key, and SetRecoveryKey rejects the latter outright).
func planRecoveryExports(out, instanceOut string, plan setup.Plan) ([]recoveryExport, error) {
	if out == "" {
		return nil, nil
	}
	if instanceOut == "" {
		instanceOut = out + ".instance"
	}
	dbPath := filepath.Join(plan.DataDir, config.DataFileName)
	exports := []recoveryExport{
		{
			Keystore:     keystoreDatabase,
			KeyFile:      plan.KeyFile,
			KeystorePath: crypto.KeystorePath(dbPath),
			Out:          out,
		},
		{
			Keystore:     keystoreInstance,
			KeyFile:      plan.InstanceKeyFile,
			KeystorePath: hosting.KeyStorePath(hosting.Path(plan.DataDir)),
			Out:          instanceOut,
		},
	}
	seen := map[string]string{}
	for _, pair := range []struct{ label, path string }{
		{"--key-file", plan.KeyFile},
		{"--instance-key-file", plan.InstanceKeyFile},
		{"--recovery-key-out", out},
		{"--instance-recovery-key-out", instanceOut},
	} {
		if pair.path == "" {
			continue
		}
		abs, err := filepath.Abs(pair.path)
		if err != nil {
			abs = pair.path
		}
		if prior, dup := seen[abs]; dup {
			return nil, cli.Validation("nextsql setup",
				prior+" and "+pair.label+" name the same file ("+pair.path+"); each key must be its own file")
		}
		seen[abs] = pair.label
	}
	for _, ex := range exports {
		if statExists(ex.Out) {
			return nil, nerr.New(nerr.AlreadyExists, "nextsql setup",
				"recovery key file "+ex.Out+" already exists; setup never overwrites key material — choose another path, "+
					"or manage the existing key with `nextsql key verify-recovery`")
		}
	}
	return exports, nil
}

func exportPathFor(exports []recoveryExport, keystore string) string {
	for _, ex := range exports {
		if ex.Keystore == keystore {
			return ex.Out
		}
	}
	return ""
}

// recoveryKeyAdvisories states, before the operator confirms, either what
// recovery keys this run will write or that the root unlock key will be the
// only way in. The second half is the more important one: it is the default,
// and it is silent otherwise.
func recoveryKeyAdvisories(exports []recoveryExport, runInit bool) []string {
	if len(exports) == 0 {
		if !runInit {
			return nil
		}
		return []string{"no --recovery-key-out given: the root unlock key will be the only way to open this deployment — losing it means total, unrecoverable data loss. Add a second unlock path later with `nextsql key add-recovery`"}
	}
	var w []string
	for _, ex := range exports {
		w = append(w, "a recovery key for the "+ex.Keystore+" keystore will be generated at "+ex.Out+
			" and verified against that keystore; store it offline, separately from "+ex.KeyFile)
	}
	return w
}

// exportRecoveryKey seals a freshly generated recovery key into one keystore
// and then proves the exported file opens it, in the same order and with the
// same write-before-install ordering as `nextsql key add-recovery`.
//
// Verification is deliberately done twice and the second time from disk: the
// live envelope check catches a wrap that does not unwrap, while reopening
// the keystore file with the bytes that were actually written is the same
// operation the operator will perform in a disaster. An exported recovery
// key that has never opened its keystore is a backup nobody has restored.
func exportRecoveryKey(ex recoveryExport) error {
	root, err := crypto.ReadKeyFile(ex.KeyFile)
	if err != nil {
		return err
	}
	env, err := crypto.OpenEnvelope(ex.KeystorePath, root)
	if err != nil {
		return err
	}
	if env.HasRecoveryKey() {
		_ = env.Close()
		return nerr.New(nerr.AlreadyExists, "nextsql setup",
			"the "+ex.Keystore+" keystore already has a recovery key configured; supersede it deliberately with `nextsql key add-recovery --replace`")
	}
	rec, err := crypto.CreateRecoveryKeyFile(ex.Out, env.NextRecoveryKeyVersion())
	if err != nil {
		_ = env.Close()
		return err
	}
	if err := env.SetRecoveryKey(rec); err != nil {
		_ = os.Remove(ex.Out)
		_ = env.Close()
		return err
	}
	if err := env.VerifyRecoveryKey(rec); err != nil {
		_ = env.Close()
		return err
	}
	_ = env.Close()

	reread, err := crypto.ReadRecoveryKeyFile(ex.Out)
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql setup", "re-read the exported recovery key for "+ex.Keystore, err)
	}
	verified, err := crypto.OpenEnvelopeWithRecovery(ex.KeystorePath, reread)
	if err != nil {
		return nerr.Wrap(nerr.Crypto, "nextsql setup",
			"the exported recovery key for "+ex.Keystore+" did not open its keystore", err)
	}
	return verified.Close()
}

type setupHealth struct {
	OK               bool   `json:"ok"`
	FormatCompatible bool   `json:"format_compatible"`
	Tables           int    `json:"tables"`
	DurableLSN       uint64 `json:"durable_lsn"`
}

// verifySetupHealth confirms the freshly initialized data directory has
// compatible on-disk headers and opens cleanly through the engine.
func verifySetupHealth(dataDir, keyFile string, bufferPages int) (setupHealth, error) {
	rep, err := upgrade.Inspect(dataDir)
	if err != nil {
		return setupHealth{}, err
	}
	h := setupHealth{FormatCompatible: rep.OK}

	keys, env, err := openEnvelope(dataDir, keyFile)
	if err != nil {
		return setupHealth{}, err
	}
	if env != nil {
		defer env.Close()
	}
	db, err := executor.Open(filepath.Join(dataDir, config.DataFileName), keys, bufferPages)
	if err != nil {
		return h, nil // format check already recorded; leave OK false
	}
	defer db.Close()
	h.Tables = len(db.Cat.List())
	if db.Eng != nil && db.Eng.WAL != nil {
		h.DurableLSN = uint64(db.Eng.WAL.DurableLSN())
	}
	h.OK = rep.OK
	return h, nil
}

// captureStdout runs fn with os.Stdout redirected to a pipe and returns
// everything it wrote. Used to fold `nextsql init`'s own stdout into the
// setup result instead of interleaving it with setup's output.
func captureStdout(fn func() error) (string, error) {
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		return "", nerr.Wrap(nerr.IO, "nextsql setup", "pipe", err)
	}
	os.Stdout = w
	done := make(chan string, 1)
	go func() {
		b, _ := io.ReadAll(r)
		done <- string(b)
	}()
	callErr := fn()
	_ = w.Close()
	os.Stdout = old
	out := <-done
	_ = r.Close()
	return out, callErr
}

// renderConfig is the exact byte content writeConfigFile would persist,
// used to compare against an existing file before refusing to overwrite.
func renderConfig(c config.Config) []byte {
	header := "# NextSQL server configuration\n" +
		"# Generated by `nextsql setup` on " + time.Now().UTC().Format(time.RFC3339) + "\n" +
		"# Edit and restart nextsqld to apply. Keys are never stored here.\n\n"
	return append([]byte(header), c.Marshal()...)
}

func writeConfigFile(path string, c config.Config) error {
	// renderConfig embeds a timestamp; write once and reuse those bytes.
	body := renderConfig(c)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql setup", "write config", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "nextsql setup", "rename config", err)
	}
	return nil
}

// recoveryKeyVerb distinguishes a planned export from a completed, verified
// one. It never says "created" before the file has actually been written and
// proven to open its keystore.
func recoveryKeyVerb(created, dryRun bool) string {
	switch {
	case created:
		return "created and verified"
	case dryRun:
		return "would be created"
	default:
		return "not created"
	}
}

func keyFileVerb(exists bool) string {
	if exists {
		return "existing, will be imported"
	}
	return "new, will be generated"
}

func emitSetup(r setupResult, jsonOut bool) error {
	if jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	w := os.Stdout
	fmt.Fprintf(w, "nextsql %s (phase %d)\n\n", r.NextSQLVersion, r.Phase)
	fmt.Fprintf(w, "hardware\n")
	fmt.Fprintf(w, "  cpu           %d logical (GOMAXPROCS %d)\n", r.Hardware.NumCPU, r.Hardware.GOMAXPROCS)
	if r.Hardware.RAMBytes > 0 {
		fmt.Fprintf(w, "  ram           %s\n", humanIEC(r.Hardware.RAMBytes))
	} else {
		fmt.Fprintf(w, "  ram           undetected\n")
	}
	fs := r.Hardware.Filesystem
	if fs == "" {
		fs = "unknown"
	}
	fmt.Fprintf(w, "  data volume   %s free of %s (%s)\n",
		humanIEC(r.Hardware.DiskFreeBytes), humanIEC(r.Hardware.DiskTotalBytes), fs)
	fmt.Fprintf(w, "\nresource plan\n")
	fmt.Fprintf(w, "  profile       %s\n", r.Profile)
	fmt.Fprintf(w, "  preset        %s\n", r.Recommendation.Preset)
	fmt.Fprintf(w, "  buffer pool   %d pages (%s)\n", r.Recommendation.BufferPages, humanIEC(r.Recommendation.BufferBytes))
	fmt.Fprintf(w, "  rationale     %s\n", r.Recommendation.Rationale)
	fmt.Fprintf(w, "\nserver\n")
	fmt.Fprintf(w, "  listen        %s%s\n", r.ListenAddr, tlsSuffix(r.TLS))
	fmt.Fprintf(w, "  data-dir      %s\n", r.DataDir)
	fmt.Fprintf(w, "  key-file      %s (%s)\n", r.KeyFile, keyFileVerb(r.KeyFileExists))
	if r.RecoveryKeyFile != "" {
		fmt.Fprintf(w, "  recovery key  %s (%s)\n", r.RecoveryKeyFile, recoveryKeyVerb(r.RecoveryKeysCreated, r.DryRun))
		fmt.Fprintf(w, "  registry rec. %s\n", r.InstanceRecoveryKeyFile)
	}
	fmt.Fprintf(w, "  config        %s\n", r.ConfigPath)
	if r.AdminUser != "" {
		fmt.Fprintf(w, "  admin user    %s\n", r.AdminUser)
	}
	if len(r.Warnings) > 0 {
		fmt.Fprintf(w, "\nwarnings\n")
		for _, warn := range r.Warnings {
			fmt.Fprintf(w, "  - %s\n", warn)
		}
	}
	fmt.Fprintf(w, "\n")
	if r.DryRun {
		fmt.Fprintf(w, "%s\n", r.Plan)
		return nil
	}
	if r.Initialized {
		fmt.Fprintf(w, "database initialized\n")
	}
	if r.RecoveryKeysCreated {
		fmt.Fprintf(w, "recovery keys written and verified:\n")
		fmt.Fprintf(w, "  %s  (database keystore)\n", r.RecoveryKeyFile)
		fmt.Fprintf(w, "  %s  (deployment registry keystore)\n", r.InstanceRecoveryKeyFile)
		fmt.Fprintf(w, "Move both off this host now. They are the only copies, and either one\n")
		fmt.Fprintf(w, "kept beside the root unlock key it backs up is not a backup.\n")
	}
	if r.ConfigWritten {
		fmt.Fprintf(w, "config written to %s\n", r.ConfigPath)
	}
	if r.Health != nil {
		status := "FAILED"
		if r.Health.OK {
			status = "ok"
		}
		fmt.Fprintf(w, "health check  %s (format_compatible=%t tables=%d)\n", status, r.Health.FormatCompatible, r.Health.Tables)
	}
	if len(r.RolledBack) > 0 {
		fmt.Fprintf(w, "\nrolled back %d created path(s):\n", len(r.RolledBack))
		for _, p := range r.RolledBack {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	if len(r.RollbackKept) > 0 {
		fmt.Fprintf(w, "\n--keep-failed: left %d partial path(s) in place:\n", len(r.RollbackKept))
		for _, p := range r.RollbackKept {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	return nil
}

func tlsSuffix(tls bool) string {
	if tls {
		return " (TLS)"
	}
	return " (loopback, plaintext)"
}

func humanIEC(n uint64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)
	switch {
	case n >= gib:
		return fmt.Sprintf("%.1f GiB", float64(n)/gib)
	case n >= mib:
		return fmt.Sprintf("%.1f MiB", float64(n)/mib)
	case n >= kib:
		return fmt.Sprintf("%.1f KiB", float64(n)/kib)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
