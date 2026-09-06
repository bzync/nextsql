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
	if err := bootstrap(env, rt); err != nil {
		return exitErr(rt, err)
	}
	args, err := env.serverArgs()
	if err != nil {
		return exitErr(rt, err)
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
		} else if err := initDataDir(env, rt); err != nil {
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

func initDataDir(env Env, rt Runtime) error {
	if env.ServerUser == "" {
		return usage("first start requires NEXTSQL_SERVER_USER")
	}
	args := []string{
		"init",
		"--data-dir", env.DataDir,
		"--key-file", env.KeyFile,
		"--user", env.ServerUser,
	}
	switch {
	case passwordFileReadable(env.PasswordFile):
		args = append(args, "--password-file", env.PasswordFile)
	case env.ServerPass != "":
		// nextsql init reads NEXTSQL_SERVER_PASS from the inherited environment.
	default:
		return usage("first start requires NEXTSQL_SERVER_PASSWORD_FILE or NEXTSQL_SERVER_PASS")
	}
	if err := rt.Run(rt.NextSQL, args); err != nil {
		return fail("init: %v", err)
	}
	return nil
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
