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
	bufferPages := fs.Int("buffer-pages", 0, "explicit buffer pool pages (overrides the preset)")
	listen := fs.String("listen", config.DefaultListenAddr, "listen address; a non-loopback address requires --tls-cert/--tls-key")
	logLevel := fs.String("log-level", config.DefaultLogLevel, "log level: debug | info | warn | error")
	tlsCert := fs.String("tls-cert", "", "TLS 1.3 certificate (PEM) for a remote listen address")
	tlsKey := fs.String("tls-key", "", "TLS 1.3 private key (PEM) for a remote listen address")
	user := fs.String("user", "", "bootstrap administrator user (recommended)")
	passwordFile := fs.String("password-file", "", "password file for --user (never a URL)")
	realmName := fs.String("realm", "default", "bootstrap subscription realm name")
	databaseName := fs.String("database", "default", "bootstrap logical database name")
	configIn := fs.String("config-in", "", "load defaults from this key=value config file before applying flags")
	configOut := fs.String("config-out", "", "where to write the generated config (default DATA-DIR/nextsql.conf)")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object instead of text")
	dryRun := fs.Bool("dry-run", false, "compute and print the plan without creating or writing anything")
	force := fs.Bool("force", false, "overwrite an existing config file")
	skipInit := fs.Bool("skip-init", false, "generate the config only; do not initialize the database")
	keepFailed := fs.Bool("keep-failed", false, "on failure, leave a partial install in place instead of rolling it back")
	if err := fs.Parse(args); err != nil {
		return err
	}

	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql setup", "--data-dir and --key-file are required")
	}
	if (*user == "") != (*passwordFile == "") {
		return nerr.New(nerr.InvalidArgument, "nextsql setup", "--user and --password-file must be given together")
	}

	presetVal, err := setup.ParsePreset(*preset)
	if err != nil {
		return err
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

	result := setupResult{
		NextSQLVersion: version.String,
		Phase:          version.Phase,
		Hardware:       plan.Info,
		Recommendation: plan.Recommendation,
		ConfigPath:     plan.ConfigPath,
		ListenAddr:     plan.ListenAddr,
		TLS:            plan.TLS,
		DataDir:        plan.DataDir,
		KeyFile:        plan.KeyFile,
		InstanceKey:    plan.InstanceKeyFile,
		AdminUser:      plan.AdminUser,
		Warnings:       plan.Warnings,
		DryRun:         *dryRun,
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
					"or use `nextsql hosting` for upgrade/repair")
		} else if !os.IsNotExist(err) {
			return nerr.Wrap(nerr.IO, "nextsql setup", "stat database", err)
		}

		initArgs := []string{
			"--no-env",
			"--data-dir", *dataDir,
			"--key-file", *keyFile,
			"--buffer-pages", strconv.Itoa(plan.Config.BufferPages),
			"--realm", *realmName,
			"--database", *databaseName,
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
		result.Initialized = true
	}

	if err := writeConfigFile(confPath, plan.Config); err != nil {
		return failed(err)
	}
	rb.Track(confPath)
	result.ConfigWritten = true

	if plan.RunInit {
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
	InstanceKey    string               `json:"instance_key_file"`
	AdminUser      string               `json:"admin_user,omitempty"`
	Initialized    bool                 `json:"initialized"`
	InitOutput     string               `json:"init_output,omitempty"`
	Health         *setupHealth         `json:"health,omitempty"`
	Warnings       []string             `json:"warnings"`
	DryRun         bool                 `json:"dry_run"`
	Plan           string               `json:"plan,omitempty"`
	RolledBack     []string             `json:"rolled_back,omitempty"`
	RollbackKept   []string             `json:"rollback_kept,omitempty"`
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
	fmt.Fprintf(w, "  preset        %s\n", r.Recommendation.Preset)
	fmt.Fprintf(w, "  buffer pool   %d pages (%s)\n", r.Recommendation.BufferPages, humanIEC(r.Recommendation.BufferBytes))
	fmt.Fprintf(w, "  rationale     %s\n", r.Recommendation.Rationale)
	fmt.Fprintf(w, "\nserver\n")
	fmt.Fprintf(w, "  listen        %s%s\n", r.ListenAddr, tlsSuffix(r.TLS))
	fmt.Fprintf(w, "  data-dir      %s\n", r.DataDir)
	fmt.Fprintf(w, "  key-file      %s\n", r.KeyFile)
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
