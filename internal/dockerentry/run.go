package dockerentry

import (
	"errors"
	"fmt"
	"os"
)

// Main is the container PID 1. It bootstraps the data directory if needed,
// optionally waits for Raft peers, then execs nextsqld so the server is PID 1
// and receives Docker/Podman stop signals directly.
func Main() int {
	return run(LoadEnv(os.Getenv), DefaultRuntime())
}

func run(env Env, rt Runtime) int {
	// Validate the server flags before mutating anything so a split TLS pair
	// or a bad mTLS combination fails fast, ahead of first-start setup.
	args, err := env.serverArgs()
	if err != nil {
		return exitErr(rt, err)
	}
	if err := bootstrap(env, rt); err != nil {
		return exitErr(rt, err)
	}
	// nextsqld does not auto-load a config file; pass the one `nextsql setup`
	// generated (deployment profile, resource sizing, operational timeouts).
	// It goes first so the explicit data-dir/key-file/listen/TLS flags already
	// in args win over it.
	if cfg := env.configPath(); exists(cfg) {
		args = append([]string{"--config", cfg}, args...)
	}
	if err := waitPeers(env, rt); err != nil {
		return exitErr(rt, err)
	}
	argv := append([]string{rt.NextSQLd}, args...)
	if err := rt.Exec(rt.NextSQLd, argv, rt.Environ()); err != nil {
		return exitErr(rt, fail("%v", err))
	}
	return 0
}

func exitErr(rt Runtime, err error) int {
	if rt.Stderr != nil {
		fmt.Fprintln(rt.Stderr, err.Error())
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return exitFail
}

func bootstrap(env Env, rt Runtime) error {
	if !exists(env.dbPath()) {
		if env.SeedFrom != "" {
			if err := restoreFromSeed(env, rt); err != nil {
				return err
			}
		} else if err := setupDataDir(env, rt); err != nil {
			return err
		}
	}
	if env.SeedTo != "" && !exists(env.seedToVerifiedPath()) {
		rt.logf("producing seed backup at %s", env.SeedTo)
		if err := rt.Run(rt.NextSQL, []string{
			"backup",
			"--data-dir", env.DataDir,
			"--key-file", env.KeyFile,
			"--out", env.SeedTo,
		}); err != nil {
			return fail("backup: %v", err)
		}
	}
	return nil
}

// setupDataDir performs the first-start non-interactive initialization through
// `nextsql setup` — the same hardened backbone the OS installers use. It sizes
// the buffer pool from a resource preset, applies the deployment profile
// (developer by default; NEXTSQL_PROFILE=production writes
// deployment_profile=production plus the production operational defaults and
// runs the fail-closed security preflight), initializes the database, and
// writes nextsql.conf into the data volume for nextsqld to load on every
// subsequent start.
func setupDataDir(env Env, rt Runtime) error {
	if env.ServerUser == "" {
		return usage("first start requires NEXTSQL_SERVER_USER")
	}
	pwFile, cleanup, err := bootstrapPasswordFile(env)
	if err != nil {
		return err
	}
	defer cleanup()

	args := []string{
		"setup",
		"--data-dir", env.DataDir,
		"--key-file", env.KeyFile,
		"--user", env.ServerUser,
		"--password-file", pwFile,
		"--config-out", env.configPath(),
		"--listen", env.Listen,
	}
	if env.Profile != "" {
		args = append(args, "--profile", env.Profile)
	}
	if env.Preset != "" {
		args = append(args, "--preset", env.Preset)
	}
	if env.BufferPages != "" {
		args = append(args, "--buffer-pages", env.BufferPages)
	}
	// serverArgs has already rejected a half-set TLS pair by the time we run.
	if env.TLSCert != "" && env.TLSKey != "" {
		args = append(args, "--tls-cert", env.TLSCert, "--tls-key", env.TLSKey)
	}
	if err := rt.Run(rt.NextSQL, args); err != nil {
		return fail("setup: %v", err)
	}
	return nil
}

// bootstrapPasswordFile resolves the bootstrap administrator password to a
// file path for the one `nextsql setup` call. A readable
// NEXTSQL_SERVER_PASSWORD_FILE is used as-is; an env-only NEXTSQL_SERVER_PASS
// is written to a private (0600) temp file that the returned cleanup removes
// — `nextsql setup` requires --password-file because its init step runs with
// --no-env and never reads NEXTSQL_SERVER_PASS.
func bootstrapPasswordFile(env Env) (path string, cleanup func(), err error) {
	noop := func() {}
	if passwordFileReadable(env.PasswordFile) {
		return env.PasswordFile, noop, nil
	}
	if env.ServerPass == "" {
		return "", noop, usage("first start requires NEXTSQL_SERVER_PASSWORD_FILE or NEXTSQL_SERVER_PASS")
	}
	f, err := os.CreateTemp("", "nextsql-bootstrap-*")
	if err != nil {
		return "", noop, fail("bootstrap password: %v", err)
	}
	name := f.Name()
	remove := func() { _ = os.Remove(name) }
	if _, err := f.WriteString(env.ServerPass + "\n"); err != nil {
		_ = f.Close()
		remove()
		return "", noop, fail("bootstrap password: %v", err)
	}
	if err := f.Close(); err != nil {
		remove()
		return "", noop, fail("bootstrap password: %v", err)
	}
	return name, remove, nil
}

func restoreFromSeed(env Env, rt Runtime) error {
	rt.logf("waiting for seed backup at %s", env.SeedFrom)
	if err := waitForFile(env.seedVerifiedPath(), "seed backup", rt); err != nil {
		return err
	}
	rt.logf("restoring from seed backup %s", env.SeedFrom)

	seedCopy, err := os.MkdirTemp("", "nextsql-seed-*")
	if err != nil {
		return fail("seed copy: %v", err)
	}
	defer os.RemoveAll(seedCopy)
	if err := copyTree(env.SeedFrom, seedCopy); err != nil {
		return fail("seed copy: %v", err)
	}

	restoreTmp, err := os.MkdirTemp("", "nextsql-restore-*")
	if err != nil {
		return fail("restore: %v", err)
	}
	// nextsql restore --data-dir refuses an existing directory; the volume
	// mount point for --data-dir already exists, so restore into a path that
	// does not, then relocate the contents.
	if err := os.Remove(restoreTmp); err != nil {
		return fail("restore: %v", err)
	}
	if err := rt.Run(rt.NextSQL, []string{
		"restore",
		"--from", seedCopy,
		"--data-dir", restoreTmp,
		"--key-file", env.KeyFile,
	}); err != nil {
		os.RemoveAll(restoreTmp)
		return fail("restore: %v", err)
	}
	if err := relocateDirContents(restoreTmp, env.DataDir); err != nil {
		os.RemoveAll(restoreTmp)
		return fail("restore: %v", err)
	}
	_ = os.Remove(restoreTmp)
	return nil
}

func waitPeers(env Env, rt Runtime) error {
	if env.RaftBootstrap == "" {
		return nil
	}
	for _, peer := range env.joinPeers() {
		rt.logf("waiting for raft peer %s", peer)
		if err := waitForTCP(peer, rt); err != nil {
			return err
		}
	}
	return nil
}

func waitForFile(path, label string, rt Runtime) error {
	// Match the historical shell: 300 failed polls, 1s apart, then fail closed.
	tries := 0
	for {
		if exists(path) {
			return nil
		}
		tries++
		if tries > waitAttempts {
			return fail("timed out waiting for %s (%s)", label, path)
		}
		rt.sleep()
	}
}

func waitForTCP(address string, rt Runtime) error {
	tries := 0
	for {
		conn, err := rt.Dial("tcp", address, dialTimeout)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		tries++
		if tries > waitAttempts {
			return fail("timed out waiting for %s to accept connections", address)
		}
		rt.sleep()
	}
}
