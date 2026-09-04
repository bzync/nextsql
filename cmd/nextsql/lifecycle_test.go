package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/setup"
)

// freshInstall runs `nextsql setup` into a temp dir and returns its paths.
func freshInstall(t *testing.T) (dataDir, keyFile, confPath string) {
	t.Helper()
	dataDir = filepath.Join(t.TempDir(), "data")
	keyFile = filepath.Join(t.TempDir(), "root.key")
	if err := setupCmd(smallSetupArgs(dataDir, keyFile)); err != nil {
		t.Fatalf("setupCmd: %v", err)
	}
	return dataDir, keyFile, filepath.Join(dataDir, "nextsql.conf")
}

func TestLifecycleDetectNoInstall(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")
	if err := lifecycleCmd([]string{"detect", "--data-dir", dir}); err != nil {
		t.Fatalf("detect on empty dir should succeed: %v", err)
	}
}

func TestLifecycleDetectInitialized(t *testing.T) {
	dataDir, _, _ := freshInstall(t)
	// Exercise the JSON path so the struct stays encodable.
	if err := lifecycleCmd([]string{"detect", "--data-dir", dataDir, "--json"}); err != nil {
		t.Fatalf("detect: %v", err)
	}
}

func TestLifecycleDetectRequiresDataDir(t *testing.T) {
	err := lifecycleCmd([]string{"detect"})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d; err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecyclePreflightReady(t *testing.T) {
	dataDir, _, _ := freshInstall(t)
	if err := lifecycleCmd([]string{"preflight", "--data-dir", dataDir}); err != nil {
		t.Fatalf("preflight on a freshly written data dir should be ready: %v", err)
	}
}

func TestLifecyclePreflightNotInitialized(t *testing.T) {
	dir := t.TempDir() // exists, but empty
	err := lifecycleCmd([]string{"preflight", "--data-dir", dir})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleBackupConfig(t *testing.T) {
	_, _, confPath := freshInstall(t)
	outDir := t.TempDir()
	if err := lifecycleCmd([]string{"backup-config", "--config", confPath, "--out", outDir}); err != nil {
		t.Fatalf("backup-config: %v", err)
	}
	entries, _ := os.ReadDir(outDir)
	var found string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "nextsql.conf.bak-") {
			found = e.Name()
		}
	}
	if found == "" {
		t.Fatalf("no backup file written to %s (entries: %v)", outDir, entries)
	}
	orig, _ := os.ReadFile(confPath)
	got, _ := os.ReadFile(filepath.Join(outDir, found))
	if string(orig) != string(got) {
		t.Error("backup bytes differ from the source config")
	}
}

func TestLifecycleBackupConfigMissingSource(t *testing.T) {
	err := lifecycleCmd([]string{"backup-config", "--config", filepath.Join(t.TempDir(), "absent.conf")})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleUpgradeDryRunMutatesNothing(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	before, _ := os.ReadDir(filepath.Dir(confPath))

	if err := lifecycleCmd([]string{
		"upgrade", "--data-dir", dataDir, "--key-file", keyFile, "--dry-run",
	}); err != nil {
		t.Fatalf("dry-run upgrade should succeed on a fresh install: %v", err)
	}

	after, _ := os.ReadDir(filepath.Dir(confPath))
	if len(after) != len(before) {
		t.Fatalf("dry-run upgrade changed the data directory: %d -> %d entries", len(before), len(after))
	}
}

func TestLifecycleUpgradeAppliesAndVerifies(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)

	if err := lifecycleCmd([]string{
		"upgrade", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8", "--json",
	}); err != nil {
		t.Fatalf("upgrade on a fresh install should apply cleanly: %v", err)
	}

	// A verified config backup sits next to the source.
	entries, _ := os.ReadDir(filepath.Dir(confPath))
	var backup string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "nextsql.conf.bak-") {
			backup = e.Name()
		}
	}
	if backup == "" {
		t.Fatalf("upgrade did not leave a config backup (entries: %v)", entries)
	}

	// Idempotent: a second run also succeeds.
	if err := lifecycleCmd([]string{
		"upgrade", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8",
	}); err != nil {
		t.Fatalf("second upgrade run should also succeed: %v", err)
	}
}

// makeClustered turns a fresh install into something the offline cluster
// detection recognizes: a raft/ state directory plus a key-free cluster
// status file recording the node's last-known role and voter count.
func makeClustered(t *testing.T, dataDir, nodeID, state string, voters int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dataDir, "raft"), 0o700); err != nil {
		t.Fatalf("mkdir raft: %v", err)
	}
	body := fmt.Sprintf(`{"node_id":%q,"state":%q,"voters":%d,"applied_lsn":0,"has_leader":true,"apply_backlog":0,"last_contact_ms":0,"healthy":true}`+"\n",
		nodeID, state, voters)
	if err := os.WriteFile(filepath.Join(dataDir, "nextsql.cluster.json"), []byte(body), 0o600); err != nil {
		t.Fatalf("write cluster status: %v", err)
	}
}

func TestLifecycleUpgradeClusteredNodeBlocksWithoutAck(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	makeClustered(t, dataDir, "n2", "follower", 3)
	before, _ := os.ReadDir(filepath.Dir(confPath))

	err := lifecycleCmd([]string{"upgrade", "--data-dir", dataDir, "--key-file", keyFile})
	if got := cli.Code(err); got != cli.ExitValidation {
		t.Fatalf("exit = %d, want %d (ExitValidation / cluster not acknowledged); err = %v", got, cli.ExitValidation, err)
	}
	after, _ := os.ReadDir(filepath.Dir(confPath))
	if len(after) != len(before) {
		t.Fatalf("blocked clustered upgrade mutated the data directory: %d -> %d entries", len(before), len(after))
	}
}

func TestLifecycleUpgradeClusteredNodeProceedsWithAck(t *testing.T) {
	dataDir, keyFile, _ := freshInstall(t)
	makeClustered(t, dataDir, "n2", "follower", 3)

	if err := lifecycleCmd([]string{
		"upgrade", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8", "--cluster-node",
	}); err != nil {
		t.Fatalf("acknowledged clustered upgrade should apply: %v", err)
	}
}

func TestLifecycleUpgradeClusteredDryRunNeedsNoAck(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	makeClustered(t, dataDir, "n1", "leader", 3)
	before, _ := os.ReadDir(filepath.Dir(confPath))

	if err := lifecycleCmd([]string{
		"upgrade", "--data-dir", dataDir, "--key-file", keyFile, "--dry-run",
	}); err != nil {
		t.Fatalf("dry-run clustered upgrade should succeed without --cluster-node: %v", err)
	}
	after, _ := os.ReadDir(filepath.Dir(confPath))
	if len(after) != len(before) {
		t.Fatalf("dry-run clustered upgrade mutated the data directory: %d -> %d entries", len(before), len(after))
	}
}

func TestDetectClusterMembership(t *testing.T) {
	dir := t.TempDir()
	if cm := detectClusterMembership(dir, nil); cm.Clustered {
		t.Fatalf("empty dir must not look clustered: %+v", cm)
	}
	if err := os.MkdirAll(filepath.Join(dir, "raft"), 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"node_id":"n1","state":"Leader","voters":3}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, "nextsql.cluster.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cm := detectClusterMembership(dir, nil)
	if !cm.Clustered || !cm.RaftDirPresent || !cm.StatusPresent {
		t.Fatalf("clustered dir not recognized: %+v", cm)
	}
	if cm.NodeID != "n1" || cm.Voters != 3 || cm.LastKnownRole != setup.ClusterRoleLeader {
		t.Fatalf("status file not parsed: %+v", cm)
	}
}

func TestLifecycleUpgradeRequiresKeyFile(t *testing.T) {
	dataDir, _, _ := freshInstall(t)
	err := lifecycleCmd([]string{"upgrade", "--data-dir", dataDir})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleUpgradeNotInitialized(t *testing.T) {
	dir := t.TempDir() // exists, empty
	keyFile := filepath.Join(t.TempDir(), "root.key")
	err := lifecycleCmd([]string{"upgrade", "--data-dir", dir, "--key-file", keyFile})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleUpgradeServerRunningBlocks(t *testing.T) {
	dataDir, keyFile, _ := freshInstall(t)
	// Simulate a running server by holding the deployment lock.
	lock, err := hosting.AcquireDataDirLock(dataDir)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Close()

	err = lifecycleCmd([]string{"upgrade", "--data-dir", dataDir, "--key-file", keyFile})
	if got := cli.Code(err); got != cli.ExitConnect {
		t.Fatalf("exit = %d, want %d (ExitConnect / server-running); err = %v", got, cli.ExitConnect, err)
	}
}

func TestLifecycleRepairHealthyIsNoOp(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	before, _ := os.ReadFile(confPath)

	if err := lifecycleCmd([]string{
		"repair", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8",
	}); err != nil {
		t.Fatalf("repair on a healthy install should succeed: %v", err)
	}
	after, _ := os.ReadFile(confPath)
	if string(before) != string(after) {
		t.Error("repair rewrote a healthy config")
	}
}

func TestLifecycleRepairRegeneratesMissingConfig(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	if err := os.Remove(confPath); err != nil {
		t.Fatal(err)
	}

	if err := lifecycleCmd([]string{
		"repair", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8",
	}); err != nil {
		t.Fatalf("repair should regenerate a missing config: %v", err)
	}
	cfg, err := config.Load(confPath)
	if err != nil {
		t.Fatalf("regenerated config does not load: %v", err)
	}
	if cfg.DataDir != dataDir || cfg.KeyFile != keyFile {
		t.Errorf("regenerated config has wrong paths: %+v", cfg)
	}
}

func TestLifecycleRepairBacksUpUnparseableConfig(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	if err := os.WriteFile(confPath, []byte("this is not = a valid\nconfig ]["), 0o640); err != nil {
		t.Fatal(err)
	}

	if err := lifecycleCmd([]string{
		"repair", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8",
	}); err != nil {
		t.Fatalf("repair should recover from an unparseable config: %v", err)
	}
	// The broken original was backed up.
	entries, _ := os.ReadDir(dataDir)
	var sawBackup bool
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "nextsql.conf.bak-") {
			sawBackup = true
		}
	}
	if !sawBackup {
		t.Errorf("unparseable config was not backed up (entries: %v)", entries)
	}
	if _, err := config.Load(confPath); err != nil {
		t.Errorf("config still unparseable after repair: %v", err)
	}
}

func TestLifecycleRepairDryRunChangesNothing(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	if err := os.Remove(confPath); err != nil {
		t.Fatal(err)
	}

	if err := lifecycleCmd([]string{
		"repair", "--data-dir", dataDir, "--key-file", keyFile, "--dry-run",
	}); err != nil {
		t.Fatalf("dry-run repair: %v", err)
	}
	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		t.Error("dry-run repair regenerated the config")
	}
}

func TestLifecycleRepairFixPerms(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	if err := os.Chmod(confPath, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(keyFile, 0o644); err != nil {
		t.Fatal(err)
	}

	if err := lifecycleCmd([]string{
		"repair", "--data-dir", dataDir, "--key-file", keyFile, "--buffer-pages", "8", "--fix-perms",
	}); err != nil {
		t.Fatalf("repair --fix-perms: %v", err)
	}
	if st, _ := os.Stat(confPath); st.Mode().Perm() != 0o640 {
		t.Errorf("config perm = %#o, want 0640", st.Mode().Perm())
	}
	if st, _ := os.Stat(keyFile); st.Mode().Perm() != 0o600 {
		t.Errorf("key perm = %#o, want 0600", st.Mode().Perm())
	}
}

func TestLifecycleRepairRequiresKeyFile(t *testing.T) {
	dataDir, _, _ := freshInstall(t)
	err := lifecycleCmd([]string{"repair", "--data-dir", dataDir})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleRepairNotInitialized(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "root.key")
	err := lifecycleCmd([]string{"repair", "--data-dir", dir, "--key-file", keyFile})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleRepairServerRunningBlocks(t *testing.T) {
	dataDir, keyFile, _ := freshInstall(t)
	lock, err := hosting.AcquireDataDirLock(dataDir)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Close()

	err = lifecycleCmd([]string{"repair", "--data-dir", dataDir, "--key-file", keyFile})
	if got := cli.Code(err); got != cli.ExitConnect {
		t.Fatalf("exit = %d, want %d (ExitConnect); err = %v", got, cli.ExitConnect, err)
	}
}

func TestLifecycleUninstallDryRunKeepsEverything(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)

	if err := lifecycleCmd([]string{"uninstall", "--data-dir", dataDir}); err != nil {
		t.Fatalf("dry-run uninstall: %v", err)
	}
	// Nothing removed without --confirm.
	for _, p := range []string{confPath, filepath.Join(dataDir, "nextsql.db"), keyFile, keyFile + ".instance"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("dry-run uninstall removed %s: %v", p, err)
		}
	}
}

func TestLifecycleUninstallConfirmRemovesConfigKeepsDataAndKeys(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)
	// Leave a config backup lying around too.
	if err := lifecycleCmd([]string{"backup-config", "--config", confPath}); err != nil {
		t.Fatalf("backup-config: %v", err)
	}

	if err := lifecycleCmd([]string{"uninstall", "--data-dir", dataDir, "--confirm"}); err != nil {
		t.Fatalf("uninstall --confirm: %v", err)
	}

	if _, err := os.Stat(confPath); !os.IsNotExist(err) {
		t.Errorf("config still present after uninstall --confirm: %v", err)
	}
	entries, _ := os.ReadDir(filepath.Dir(confPath))
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "nextsql.conf.bak-") {
			t.Errorf("config backup %s survived uninstall --confirm", e.Name())
		}
	}
	// Data and keys preserved.
	for _, p := range []string{filepath.Join(dataDir, "nextsql.db"), keyFile, keyFile + ".instance"} {
		if _, err := os.Stat(p); err != nil {
			t.Errorf("uninstall --confirm removed a preserved artifact %s: %v", p, err)
		}
	}
}

func TestLifecycleUninstallPurgeData(t *testing.T) {
	dataDir, keyFile, _ := freshInstall(t)

	if err := lifecycleCmd([]string{"uninstall", "--data-dir", dataDir, "--purge-data", "--confirm"}); err != nil {
		t.Fatalf("uninstall --purge-data --confirm: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "nextsql.db")); !os.IsNotExist(err) {
		t.Errorf("--purge-data left the database: %v", err)
	}
	// Keys still preserved without --purge-keys.
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("--purge-data removed the root key: %v", err)
	}
}

func TestLifecycleUninstallPurgeKeysRequiresPurgeData(t *testing.T) {
	dataDir, keyFile, _ := freshInstall(t)

	err := lifecycleCmd([]string{"uninstall", "--data-dir", dataDir, "--purge-keys", "--confirm"})
	if got := cli.Code(err); got != cli.ExitValidation {
		t.Fatalf("exit = %d, want %d (ExitValidation); err = %v", got, cli.ExitValidation, err)
	}
	// Refused → nothing deleted.
	if _, err := os.Stat(keyFile); err != nil {
		t.Errorf("blocked uninstall still removed the key: %v", err)
	}
}

func TestLifecycleUninstallPurgeAll(t *testing.T) {
	dataDir, keyFile, confPath := freshInstall(t)

	if err := lifecycleCmd([]string{
		"uninstall", "--data-dir", dataDir, "--purge-data", "--purge-keys", "--confirm", "--json",
	}); err != nil {
		t.Fatalf("uninstall --purge-data --purge-keys --confirm: %v", err)
	}
	for _, p := range []string{confPath, filepath.Join(dataDir, "nextsql.db"), keyFile, keyFile + ".instance"} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("full purge left %s behind: %v", p, err)
		}
	}
}

func TestLifecycleUninstallServerRunningBlocks(t *testing.T) {
	dataDir, _, confPath := freshInstall(t)
	lock, err := hosting.AcquireDataDirLock(dataDir)
	if err != nil {
		t.Fatalf("acquire lock: %v", err)
	}
	defer lock.Close()

	err = lifecycleCmd([]string{"uninstall", "--data-dir", dataDir, "--confirm"})
	if got := cli.Code(err); got != cli.ExitConnect {
		t.Fatalf("exit = %d, want %d (ExitConnect); err = %v", got, cli.ExitConnect, err)
	}
	if _, statErr := os.Stat(confPath); statErr != nil {
		t.Errorf("blocked uninstall still removed the config: %v", statErr)
	}
}

func TestLifecycleUninstallRequiresDataDir(t *testing.T) {
	err := lifecycleCmd([]string{"uninstall"})
	if got := cli.Code(err); got != cli.ExitLocal {
		t.Fatalf("exit = %d, want %d (ExitLocal); err = %v", got, cli.ExitLocal, err)
	}
}

func TestLifecycleUninstallNothingThere(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nope")
	if err := lifecycleCmd([]string{"uninstall", "--data-dir", dir, "--confirm"}); err != nil {
		t.Fatalf("uninstall of a nonexistent install should be a clean no-op: %v", err)
	}
}

func TestLifecycleUnknownSubcommand(t *testing.T) {
	if err := lifecycleCmd([]string{"frobnicate"}); err == nil {
		t.Fatal("expected an error for an unknown lifecycle subcommand")
	}
}
