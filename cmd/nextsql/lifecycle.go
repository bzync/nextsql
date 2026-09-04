package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/setup"
	"github.com/bzync/nextsql/internal/storage/integrity"
	"github.com/bzync/nextsql/internal/sysinfo"
	"github.com/bzync/nextsql/internal/undo"
	"github.com/bzync/nextsql/internal/upgrade"
	"github.com/bzync/nextsql/internal/upgrade/compat"
	"github.com/bzync/nextsql/internal/version"
	"github.com/bzync/nextsql/internal/wal"
)

// lifecycleCmd is the non-interactive installer *lifecycle* backbone (P28):
// detect an existing installation, decide whether this binary may upgrade it
// in place, take a verified config backup, apply an in-place upgrade, repair
// a damaged install, and remove one. Every OS installer and the eventual
// Manager GUI drive these same code paths.
func lifecycleCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql lifecycle",
			"expected detect, preflight, backup-config, upgrade, repair, or uninstall")
	}
	switch args[0] {
	case "detect":
		return lifecycleDetect(args[1:])
	case "preflight":
		return lifecyclePreflight(args[1:])
	case "backup-config":
		return lifecycleBackupConfig(args[1:])
	case "upgrade":
		return lifecycleUpgrade(args[1:])
	case "repair":
		return lifecycleRepair(args[1:])
	case "uninstall":
		return lifecycleUninstall(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql lifecycle", "unknown lifecycle command")
	}
}

// lockHeldByServer reports whether a NextSQL process currently holds the
// deployment lock for dataDir. known is false when that could not be
// determined (e.g. the directory does not exist or the probe itself failed).
func lockHeldByServer(dataDir string) (held, known bool) {
	if st, err := os.Stat(dataDir); err != nil || !st.IsDir() {
		return false, false
	}
	l, err := hosting.AcquireDataDirLock(dataDir)
	if err != nil {
		if nerr.HasCode(err, nerr.Unavailable) {
			return true, true
		}
		return false, false
	}
	_ = l.Close()
	return false, true
}

// clusterMembership is the offline evidence that a data directory belongs to
// a Raft-clustered node. Every field is derived without a key: a raft/ state
// directory, the key-free cluster status file, and node_id + raft_bind in the
// config. It feeds setup.PlanRollingUpgrade so `lifecycle upgrade` routes a
// clustered node through the documented rolling procedure.
type clusterMembership struct {
	Clustered      bool              `json:"clustered"`
	RaftDirPresent bool              `json:"raft_dir_present"`
	StatusPresent  bool              `json:"status_file_present"`
	ConfiguredHA   bool              `json:"configured_ha"`
	NodeID         string            `json:"node_id,omitempty"`
	LastKnownState string            `json:"last_known_state,omitempty"`
	LastKnownRole  setup.ClusterRole `json:"last_known_role,omitempty"`
	Voters         int               `json:"voters,omitempty"`
}

func detectClusterMembership(dataDir string, cfg *config.Config) clusterMembership {
	m := clusterMembership{LastKnownRole: setup.ClusterRoleUnknown}
	if st, err := os.Stat(filepath.Join(dataDir, "raft")); err == nil && st.IsDir() {
		m.RaftDirPresent = true
	}
	if st, err := replication.ReadStatusFile(dataDir); err == nil {
		m.StatusPresent = true
		m.NodeID = st.NodeID
		m.LastKnownState = st.State
		m.Voters = st.Voters
		m.LastKnownRole = roleFromState(st.State)
	}
	if cfg != nil && cfg.NodeID != "" && cfg.RaftBind != "" {
		m.ConfiguredHA = true
		if m.NodeID == "" {
			m.NodeID = cfg.NodeID
		}
	}
	m.Clustered = m.RaftDirPresent || m.StatusPresent || m.ConfiguredHA
	return m
}

func roleFromState(state string) setup.ClusterRole {
	switch strings.ToLower(strings.TrimSpace(state)) {
	case "leader":
		return setup.ClusterRoleLeader
	case "follower", "candidate":
		return setup.ClusterRoleFollower
	default:
		return setup.ClusterRoleUnknown
	}
}

// ---- detect -------------------------------------------------------------

type detectResult struct {
	NextSQLVersion string `json:"nextsql_version"`
	Phase          int    `json:"phase"`

	DataDir         string `json:"data_dir"`
	ConfigPath      string `json:"config_path"`
	ConfigPresent   bool   `json:"config_present"`
	ConfigParseOK   bool   `json:"config_parse_ok"`
	ConfigParseErr  string `json:"config_parse_error,omitempty"`
	DataFilePresent bool   `json:"data_file_present"`
	KeystorePresent bool   `json:"keystore_present"`

	ResolvedDataDir    string `json:"resolved_data_dir,omitempty"`
	ResolvedKeyFile    string `json:"resolved_key_file,omitempty"`
	ResolvedInstance   string `json:"resolved_instance_key_file,omitempty"`
	ResolvedListenAddr string `json:"resolved_listen_addr,omitempty"`
	ResolvedTLS        bool   `json:"resolved_tls,omitempty"`

	ServerRunning     bool   `json:"server_running"`
	ServerRunKnown    bool   `json:"server_running_known"`
	FormatDatabaseID  string `json:"format_database_id,omitempty"`
	HeadersCompatible bool   `json:"headers_compatible"`

	Cluster *clusterMembership `json:"cluster,omitempty"`

	Status  setup.InstallStatus `json:"status"`
	Summary string              `json:"summary"`
}

func lifecycleDetect(args []string) error {
	fs := flag.NewFlagSet("lifecycle detect", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory to inspect")
	configPath := fs.String("config", "", "config file to read (default DATA-DIR/nextsql.conf)")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return cli.LocalMissing("nextsql lifecycle detect", "--data-dir is required")
	}

	confPath := *configPath
	if confPath == "" {
		confPath = filepath.Join(*dataDir, "nextsql.conf")
	}

	r := detectResult{
		NextSQLVersion: version.String,
		Phase:          version.Phase,
		DataDir:        *dataDir,
		ConfigPath:     confPath,
	}

	var parsedCfg *config.Config
	if _, err := os.Stat(confPath); err == nil {
		r.ConfigPresent = true
		cfg, err := config.Load(confPath)
		if err != nil {
			r.ConfigParseErr = err.Error()
		} else {
			parsedCfg = &cfg
			r.ConfigParseOK = true
			r.ResolvedDataDir = cfg.DataDir
			r.ResolvedKeyFile = cfg.KeyFile
			r.ResolvedInstance = cfg.InstanceRootFile()
			r.ResolvedListenAddr = cfg.ListenAddr
			r.ResolvedTLS = cfg.TLSCert != "" && cfg.TLSKey != ""
		}
	} else if !os.IsNotExist(err) {
		return nerr.Wrap(nerr.IO, "nextsql lifecycle detect", "stat config", err)
	}

	if cm := detectClusterMembership(*dataDir, parsedCfg); cm.Clustered {
		r.Cluster = &cm
	}

	dataFile := filepath.Join(*dataDir, config.DataFileName)
	if _, err := os.Stat(dataFile); err == nil {
		r.DataFilePresent = true
	}
	if _, err := os.Stat(dataFile + ".keys"); err == nil {
		r.KeystorePresent = true
	}

	if r.DataFilePresent {
		held, known := lockHeldByServer(*dataDir)
		r.ServerRunning, r.ServerRunKnown = held, known
		if rep, err := upgrade.Inspect(*dataDir); err == nil {
			r.HeadersCompatible = rep.OK
			if rep.HasIdent {
				r.FormatDatabaseID = rep.Identity.DatabaseString()
			}
		}
	}

	r.Status = setup.ClassifyInstall(setup.DetectInput{
		ConfigPresent:   r.ConfigPresent,
		DataFilePresent: r.DataFilePresent,
		KeystorePresent: r.KeystorePresent,
		LockHeld:        r.ServerRunning,
	})
	r.Summary = detectSummary(r)

	if *jsonOut {
		return emitJSON(r)
	}
	w := os.Stdout
	fmt.Fprintf(w, "nextsql %s (phase %d)\n\n", r.NextSQLVersion, r.Phase)
	fmt.Fprintf(w, "data directory   %s\n", r.DataDir)
	fmt.Fprintf(w, "status           %s — %s\n", r.Status, r.Summary)
	fmt.Fprintf(w, "\nconfig file      %s\n", r.ConfigPath)
	if r.ConfigPresent {
		if r.ConfigParseOK {
			fmt.Fprintf(w, "  parses         yes\n")
			fmt.Fprintf(w, "  data-dir       %s\n", r.ResolvedDataDir)
			fmt.Fprintf(w, "  key-file       %s\n", r.ResolvedKeyFile)
			fmt.Fprintf(w, "  instance-key   %s\n", r.ResolvedInstance)
			fmt.Fprintf(w, "  listen         %s%s\n", r.ResolvedListenAddr, tlsSuffix(r.ResolvedTLS))
		} else {
			fmt.Fprintf(w, "  parses         NO — %s\n", r.ConfigParseErr)
		}
	} else {
		fmt.Fprintf(w, "  present        no\n")
	}
	fmt.Fprintf(w, "\ndatabase\n")
	fmt.Fprintf(w, "  data file      %s\n", presentWord(r.DataFilePresent))
	fmt.Fprintf(w, "  keystore       %s\n", presentWord(r.KeystorePresent))
	if r.DataFilePresent {
		fmt.Fprintf(w, "  headers        %s\n", compatWord(r.HeadersCompatible))
		if r.FormatDatabaseID != "" {
			fmt.Fprintf(w, "  database id    %s\n", r.FormatDatabaseID)
		}
		if r.ServerRunKnown {
			fmt.Fprintf(w, "  server running %t\n", r.ServerRunning)
		} else {
			fmt.Fprintf(w, "  server running unknown\n")
		}
	}
	if r.Cluster != nil {
		fmt.Fprintf(w, "\ncluster\n")
		fmt.Fprintf(w, "  raft node      yes")
		if r.Cluster.NodeID != "" {
			fmt.Fprintf(w, " (%s)", r.Cluster.NodeID)
		}
		fmt.Fprintf(w, "\n")
		if r.Cluster.LastKnownState != "" {
			fmt.Fprintf(w, "  last known     %s", r.Cluster.LastKnownState)
			if r.Cluster.Voters > 0 {
				fmt.Fprintf(w, ", %d voters", r.Cluster.Voters)
			}
			fmt.Fprintf(w, "\n")
		}
		fmt.Fprintf(w, "  upgrade path   in-place upgrade needs the rolling procedure — `nextsql lifecycle upgrade --cluster-node` per drained node\n")
	}
	return nil
}

func detectSummary(r detectResult) string {
	switch r.Status {
	case setup.InstallNone:
		return "no NextSQL installation found here"
	case setup.InstallConfigOnly:
		return "a config file is present but the database has not been initialized"
	case setup.InstallInitialized:
		if r.DataFilePresent && !r.HeadersCompatible {
			return "an initialized database is present but its headers are not compatible with this binary"
		}
		return "an initialized database is present and no server is running"
	case setup.InstallRunning:
		return "a NextSQL process is using this data directory"
	default:
		return string(r.Status)
	}
}

// ---- preflight ---------------------------------------------------------

type preflightResult struct {
	NextSQLVersion string                  `json:"nextsql_version"`
	Phase          int                     `json:"phase"`
	DataDir        string                  `json:"data_dir"`
	Assessment     setup.UpgradeAssessment `json:"assessment"`
}

func lifecyclePreflight(args []string) error {
	fs := flag.NewFlagSet("lifecycle preflight", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory to upgrade in place")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return cli.LocalMissing("nextsql lifecycle preflight", "--data-dir is required")
	}

	if st, err := os.Stat(*dataDir); err != nil || !st.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return nerr.Wrap(nerr.IO, "nextsql lifecycle preflight", "stat data-dir", err)
		}
		return preflightExit(setup.UpgradeNotInitialized)
	}
	held, _ := lockHeldByServer(*dataDir)
	assessment, err := assessInPlaceUpgrade(*dataDir, held)
	if err != nil {
		return err
	}

	res := preflightResult{
		NextSQLVersion: version.String,
		Phase:          version.Phase,
		DataDir:        *dataDir,
		Assessment:     assessment,
	}

	if *jsonOut {
		if err := emitJSON(res); err != nil {
			return err
		}
	} else {
		w := os.Stdout
		fmt.Fprintf(w, "nextsql %s (phase %d)\n\n", res.NextSQLVersion, res.Phase)
		fmt.Fprintf(w, "data directory   %s\n", res.DataDir)
		fmt.Fprintf(w, "verdict          %s — %s\n\n", assessment.Verdict, assessment.Summary)
		fmt.Fprintf(w, "on-disk formats\n")
		for _, f := range assessment.Families {
			line := fmt.Sprintf("  %-14s v%d  %s", f.Family, f.Version, f.Direction)
			if f.Detail != "" {
				line += " (" + f.Detail + ")"
			}
			fmt.Fprintln(w, line)
		}
		if len(assessment.Blocking) > 0 {
			fmt.Fprintf(w, "\nblocking\n")
			for _, b := range assessment.Blocking {
				fmt.Fprintf(w, "  - %s\n", b)
			}
		}
	}

	return preflightExit(assessment.Verdict)
}

// assessInPlaceUpgrade reads the plaintext superblock / WAL-control /
// UNDO-control / envelope headers under dataDir (no root key required) and
// reduces them to an upgrade verdict. lockHeld is passed straight to
// setup.AssessUpgrade. Shared by `preflight` and `upgrade`.
func assessInPlaceUpgrade(dataDir string, lockHeld bool) (setup.UpgradeAssessment, error) {
	rep, err := upgrade.Inspect(dataDir)
	if err != nil {
		return setup.UpgradeAssessment{}, err
	}

	var families []setup.FamilyCheck
	var initialized bool
	for _, f := range rep.Files {
		fc := setup.FamilyCheck{Family: string(f.Family), Version: f.Version}
		switch {
		case !f.Present:
			fc.Direction = setup.FormatAbsent
		case f.Err != "" || !f.MagicOK:
			fc.Direction = setup.FormatDamaged
			fc.Detail = f.Err
		case f.Compat:
			fc.Direction = setup.FormatOK
		default:
			fc.Direction, fc.Detail = classifyOutOfWindow(f.Family, f.Version)
		}
		if f.Family == compat.FamilyPage {
			initialized = f.Present && f.MagicOK
		}
		families = append(families, fc)
	}
	// The envelope version travels in the superblock, not its own file;
	// surface it as its own line so a cipher-envelope bump is legible.
	if rep.HasIdent {
		fc := setup.FamilyCheck{Family: string(compat.FamilyEnvelope), Version: rep.Envelope}
		if compat.Compatible(compat.FamilyEnvelope, rep.Envelope) {
			fc.Direction = setup.FormatOK
		} else {
			fc.Direction, fc.Detail = classifyOutOfWindow(compat.FamilyEnvelope, rep.Envelope)
		}
		families = append(families, fc)
	}

	return setup.AssessUpgrade(setup.UpgradeInput{
		Initialized: initialized,
		LockHeld:    lockHeld,
		Families:    families,
	}), nil
}

func classifyOutOfWindow(family compat.Family, v uint16) (setup.FormatDirection, string) {
	s, ok := compat.Lookup(family)
	if !ok {
		return setup.FormatDamaged, "unknown format family"
	}
	switch {
	case v > s.MaxReadable:
		return setup.FormatTooNew, fmt.Sprintf("this binary reads up to v%d", s.MaxReadable)
	case v < s.MinReadable:
		return setup.FormatTooOld, fmt.Sprintf("this binary reads from v%d", s.MinReadable)
	default:
		// Compat==false for a non-version reason (page size, etc.).
		return setup.FormatDamaged, "header rejected by the format check"
	}
}

func preflightExit(v setup.UpgradeVerdict) error {
	switch v {
	case setup.UpgradeReady:
		return nil
	case setup.UpgradeNotInitialized:
		return cli.LocalMissing("nextsql lifecycle preflight", "no initialized database at this data directory")
	case setup.UpgradeServerRunning:
		return nerr.New(nerr.Unavailable, "nextsql lifecycle preflight",
			"a NextSQL process is using this data directory; stop it before an offline upgrade")
	default:
		return cli.Validation("nextsql lifecycle preflight", "this binary cannot open the data directory in place")
	}
}

// ---- backup-config ----------------------------------------------------

type backupConfigResult struct {
	Source   string `json:"source"`
	Backup   string `json:"backup"`
	Bytes    int    `json:"bytes"`
	Verified bool   `json:"verified"`
}

func lifecycleBackupConfig(args []string) error {
	fs := flag.NewFlagSet("lifecycle backup-config", flag.ContinueOnError)
	src := fs.String("config", "", "config file to back up")
	outDir := fs.String("out", "", "directory for the backup copy (default: alongside the source)")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *src == "" {
		return cli.LocalMissing("nextsql lifecycle backup-config", "--config is required")
	}

	res, err := backupConfigFile("nextsql lifecycle backup-config", *src, *outDir)
	if err != nil {
		if res.Backup != "" && *jsonOut {
			_ = emitJSON(res)
		}
		return err
	}

	if *jsonOut {
		return emitJSON(res)
	}
	fmt.Printf("config backed up\n  source   %s\n  backup   %s\n  bytes    %d\n  verified reloads to an identical config\n",
		res.Source, res.Backup, res.Bytes)
	return nil
}

// backupConfigFile copies src to a timestamped sibling (or into outDir when
// set), mode 0640 via tmp+rename, then reloads the copy and confirms it
// parses back to a config identical to the source. A source that does not
// exist or does not parse is refused before anything is written. On a
// verification failure the copy is left in place and its path is returned in
// the result so a caller mid-upgrade can still point at it for rollback.
// Shared by `lifecycle backup-config` and `lifecycle upgrade`.
func backupConfigFile(op, src, outDir string) (backupConfigResult, error) {
	body, err := os.ReadFile(src)
	if err != nil {
		if os.IsNotExist(err) {
			return backupConfigResult{}, cli.LocalMissing(op, "config file "+src+" does not exist")
		}
		return backupConfigResult{}, nerr.Wrap(nerr.IO, op, "read config", err)
	}
	// Refuse to "back up" a file that does not parse — that is exactly the
	// moment an operator most needs to know.
	want, err := config.Load(src)
	if err != nil {
		return backupConfigResult{}, nerr.Wrap(nerr.InvalidArgument, op, "source config does not parse", err)
	}

	dir := outDir
	if dir == "" {
		dir = filepath.Dir(src)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return backupConfigResult{}, nerr.Wrap(nerr.IO, op, "mkdir out", err)
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	dest := filepath.Join(dir, filepath.Base(src)+".bak-"+stamp)

	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return backupConfigResult{}, nerr.Wrap(nerr.IO, op, "write backup", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return backupConfigResult{}, nerr.Wrap(nerr.IO, op, "rename backup", err)
	}

	got, loadErr := config.Load(dest)
	res := backupConfigResult{
		Source:   src,
		Backup:   dest,
		Bytes:    len(body),
		Verified: loadErr == nil && reflect.DeepEqual(got, want),
	}
	if !res.Verified {
		return res, nerr.New(nerr.InvalidFormat, op,
			"backup written to "+dest+" but it did not reload to an identical config")
	}
	return res, nil
}

// ---- upgrade --------------------------------------------------------

type upgradeResult struct {
	NextSQLVersion string `json:"nextsql_version"`
	Phase          int    `json:"phase"`
	DataDir        string `json:"data_dir"`
	DryRun         bool   `json:"dry_run"`

	Outcome    setup.UpgradeOutcome    `json:"outcome"`
	Assessment setup.UpgradeAssessment `json:"assessment"`
	Steps      []setup.UpgradeStep     `json:"steps"`

	Cluster        *clusterMembership            `json:"cluster,omitempty"`
	RollingUpgrade *setup.ClusterUpgradeGuidance `json:"rolling_upgrade,omitempty"`

	ConfigBackup       *backupConfigResult `json:"config_backup,omitempty"`
	EngineOpened       bool                `json:"engine_opened"`
	HeadersCompatAfter bool                `json:"headers_compatible_after"`
	Tables             int                 `json:"tables"`
	DurableLSN         uint64              `json:"durable_lsn"`
	Summary            string              `json:"summary"`
}

// lifecycleUpgrade is the mutating half of the lifecycle backbone: hold the
// deployment lock, preflight the on-disk formats, back up the config, then
// open the encrypted store once with this binary — which runs WAL recovery
// and confirms the catalog decodes under the new format code — and
// re-verify. It never deletes anything; on any failure the config backup is
// left in place for rollback and its path is reported.
func lifecycleUpgrade(args []string) error {
	fs := flag.NewFlagSet("lifecycle upgrade", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory to upgrade in place")
	keyFile := fs.String("key-file", "", "root unlock key file")
	configPath := fs.String("config", "", "config file to back up first (default DATA-DIR/nextsql.conf; skipped if absent)")
	bufferPages := fs.Int("buffer-pages", config.DefaultBufferPages, "buffer pool pages for the verification open")
	clusterNode := fs.Bool("cluster-node", false, "acknowledge this is a drained Raft cluster node (leadership already transferred if it was the leader); required to upgrade a clustered node")
	dryRun := fs.Bool("dry-run", false, "assess and print the plan; back up nothing and open nothing")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql lifecycle upgrade", "--data-dir and --key-file are required")
	}

	confPath := *configPath
	if confPath == "" {
		confPath = filepath.Join(*dataDir, "nextsql.conf")
	}
	configPresent := false
	if _, err := os.Stat(confPath); err == nil {
		configPresent = true
	} else if !os.IsNotExist(err) {
		return nerr.Wrap(nerr.IO, "nextsql lifecycle upgrade", "stat config", err)
	}

	res := upgradeResult{
		NextSQLVersion: version.String,
		Phase:          version.Phase,
		DataDir:        *dataDir,
		DryRun:         *dryRun,
		Steps:          setup.UpgradePlan(setup.UpgradePlanInput{ConfigPresent: configPresent}),
	}

	if st, err := os.Stat(*dataDir); err != nil || !st.IsDir() {
		if err != nil && !os.IsNotExist(err) {
			return nerr.Wrap(nerr.IO, "nextsql lifecycle upgrade", "stat data-dir", err)
		}
		res.Outcome = setup.UpgradeBlocked
		res.Assessment = setup.UpgradeAssessment{
			Verdict: setup.UpgradeNotInitialized,
			Summary: "no data directory at this path",
		}
		return finishUpgrade(res, *jsonOut, upgradeExit(setup.UpgradeNotInitialized))
	}

	// Hold the deployment lock for the whole operation so no server can
	// start between preflight and the verification open. A lock we cannot
	// take means a server is already running.
	lock, err := hosting.AcquireDataDirLock(*dataDir)
	if err != nil {
		if nerr.HasCode(err, nerr.Unavailable) {
			res.Outcome = setup.UpgradeBlocked
			res.Assessment = setup.UpgradeAssessment{
				Verdict: setup.UpgradeServerRunning,
				Summary: "a NextSQL process holds the deployment lock",
			}
			return finishUpgrade(res, *jsonOut, upgradeExit(setup.UpgradeServerRunning))
		}
		return nerr.Wrap(nerr.IO, "nextsql lifecycle upgrade", "acquire deployment lock", err)
	}
	defer lock.Close()

	// We hold the lock, so no server is running: assess with lockHeld=false.
	assessment, err := assessInPlaceUpgrade(*dataDir, false)
	if err != nil {
		return err
	}
	res.Assessment = assessment
	if !assessment.OK() {
		res.Outcome = setup.UpgradeBlocked
		return finishUpgrade(res, *jsonOut, upgradeExit(assessment.Verdict))
	}

	// Rolling-cluster upgrade integration: a node that has been part of a
	// Raft cluster must go through the per-node rolling procedure. The
	// offline upgrade of one stopped node is safe on its own — WAL recovery
	// only, and the Raft log replays on restart — so --cluster-node is a
	// sequencing acknowledgment (node already drained; leadership already
	// transferred if it was the leader), not a technical gate.
	var clusterCfg *config.Config
	if configPresent {
		if c, cerr := config.Load(confPath); cerr == nil {
			clusterCfg = &c
		}
	}
	cm := detectClusterMembership(*dataDir, clusterCfg)
	guidance := setup.PlanRollingUpgrade(setup.ClusterUpgradeInput{
		Clustered:     cm.Clustered,
		LastKnownRole: cm.LastKnownRole,
		Voters:        cm.Voters,
		Acknowledged:  *clusterNode,
		DryRun:        *dryRun,
	})
	if cm.Clustered {
		res.Cluster = &cm
	}
	if guidance.Clustered {
		res.RollingUpgrade = &guidance
	}
	if !guidance.Proceed {
		res.Outcome = setup.UpgradeBlocked
		res.Summary = "this data directory is a Raft cluster node; follow the rolling-upgrade procedure and re-run with --cluster-node"
		return finishUpgrade(res, *jsonOut, cli.Validation("nextsql lifecycle upgrade",
			"clustered node not acknowledged: drain this node (and transfer leadership if it is the leader), then re-run with --cluster-node"))
	}

	if *dryRun {
		res.Outcome = setup.UpgradeDryRun
		res.Summary = "dry run: preflight is ready; nothing was backed up, opened, or mutated"
		return finishUpgrade(res, *jsonOut, nil)
	}

	// Step: back up the config (only when one exists).
	if configPresent {
		bc, berr := backupConfigFile("nextsql lifecycle upgrade", confPath, "")
		if bc.Backup != "" {
			res.ConfigBackup = &bc
		}
		if berr != nil {
			res.Outcome = setup.UpgradeFailedVerify
			res.Summary = "config backup failed before any engine open: " + berr.Error()
			return finishUpgrade(res, *jsonOut, berr)
		}
	}

	// Step: open the encrypted store with this binary. This runs WAL
	// recovery — the actual in-place mutation — and confirms the catalog
	// decodes under the new format code while the operator is watching.
	keys, env, err := openEnvelope(*dataDir, *keyFile)
	if err != nil {
		res.Outcome = setup.UpgradeFailedVerify
		return finishUpgrade(res, *jsonOut, err)
	}
	if env != nil {
		defer env.Close()
	}
	db, err := executor.Open(filepath.Join(*dataDir, config.DataFileName), keys, *bufferPages)
	if err != nil {
		res.Outcome = setup.UpgradeFailedVerify
		res.Summary = "the engine did not open under this binary: " + err.Error()
		return finishUpgrade(res, *jsonOut,
			nerr.Wrap(nerr.InvalidFormat, "nextsql lifecycle upgrade", "open engine", err))
	}
	res.EngineOpened = true
	res.Tables = len(db.Cat.List())
	if db.Eng != nil && db.Eng.WAL != nil {
		res.DurableLSN = uint64(db.Eng.WAL.DurableLSN())
	}
	_ = db.Close()

	// Step: re-verify the plaintext headers still read cleanly.
	post, err := assessInPlaceUpgrade(*dataDir, false)
	if err != nil {
		return err
	}
	res.HeadersCompatAfter = post.OK()
	if !post.OK() {
		res.Outcome = setup.UpgradeFailedVerify
		res.Assessment = post
		res.Summary = "headers were still not compatible after the open: " + post.Summary
		return finishUpgrade(res, *jsonOut,
			nerr.New(nerr.InvalidFormat, "nextsql lifecycle upgrade", "post-upgrade verification failed"))
	}

	res.Outcome = setup.UpgradeApplied
	res.Summary = fmt.Sprintf("engine opened cleanly under nextsql %s; %d tables, durable LSN %d",
		version.String, res.Tables, res.DurableLSN)
	return finishUpgrade(res, *jsonOut, nil)
}

func finishUpgrade(res upgradeResult, jsonOut bool, retErr error) error {
	if jsonOut {
		_ = emitJSON(res)
		return retErr
	}
	w := os.Stdout
	fmt.Fprintf(w, "nextsql %s (phase %d)\n\n", res.NextSQLVersion, res.Phase)
	fmt.Fprintf(w, "data directory   %s\n", res.DataDir)
	fmt.Fprintf(w, "outcome          %s\n", res.Outcome)
	if res.Assessment.Verdict != "" {
		fmt.Fprintf(w, "preflight        %s — %s\n", res.Assessment.Verdict, res.Assessment.Summary)
	}
	if len(res.Steps) > 0 {
		fmt.Fprintf(w, "\nplan\n")
		for _, s := range res.Steps {
			mark := " "
			if s.Mutates {
				mark = "*"
			}
			fmt.Fprintf(w, "  [%s] %-24s %s\n", mark, s.Name, s.Detail)
		}
		fmt.Fprintf(w, "  (* mutates on-disk state)\n")
	}
	if res.Cluster != nil {
		fmt.Fprintf(w, "\ncluster          Raft node")
		if res.Cluster.NodeID != "" {
			fmt.Fprintf(w, " %q", res.Cluster.NodeID)
		}
		if res.Cluster.LastKnownState != "" {
			fmt.Fprintf(w, " (last known %s", res.Cluster.LastKnownState)
			if res.Cluster.Voters > 0 {
				fmt.Fprintf(w, ", %d voters", res.Cluster.Voters)
			}
			fmt.Fprintf(w, ")")
		}
		fmt.Fprintf(w, "\n")
	}
	if res.RollingUpgrade != nil {
		g := res.RollingUpgrade
		for _, wn := range g.Warnings {
			fmt.Fprintf(w, "  warning        %s\n", wn)
		}
		if len(g.Steps) > 0 {
			fmt.Fprintf(w, "\nrolling-upgrade procedure (one node at a time)\n")
			for _, s := range g.Steps {
				fmt.Fprintf(w, "  %d. %-22s %s\n", s.Order, s.Name, s.Detail)
			}
		}
		for _, b := range g.Blocking {
			fmt.Fprintf(w, "\nblocked          %s\n", b)
		}
	}
	if len(res.Assessment.Blocking) > 0 {
		fmt.Fprintf(w, "\nblocking\n")
		for _, b := range res.Assessment.Blocking {
			fmt.Fprintf(w, "  - %s\n", b)
		}
	}
	if res.ConfigBackup != nil {
		fmt.Fprintf(w, "\nconfig backup    %s (verified=%t)\n", res.ConfigBackup.Backup, res.ConfigBackup.Verified)
	}
	if res.EngineOpened {
		fmt.Fprintf(w, "engine open      ok — %d tables, durable LSN %d\n", res.Tables, res.DurableLSN)
		fmt.Fprintf(w, "re-verify        %s\n", compatWord(res.HeadersCompatAfter))
	}
	if res.Summary != "" {
		fmt.Fprintf(w, "\n%s\n", res.Summary)
	}
	return retErr
}

func upgradeExit(v setup.UpgradeVerdict) error {
	switch v {
	case setup.UpgradeReady:
		return nil
	case setup.UpgradeNotInitialized:
		return cli.LocalMissing("nextsql lifecycle upgrade", "no initialized database at this data directory")
	case setup.UpgradeServerRunning:
		return nerr.New(nerr.Unavailable, "nextsql lifecycle upgrade",
			"a NextSQL process is using this data directory; stop it before an in-place upgrade")
	default:
		return cli.Validation("nextsql lifecycle upgrade", "this binary cannot open the data directory in place")
	}
}

// ---- repair -------------------------------------------------------

type repairResult struct {
	NextSQLVersion string `json:"nextsql_version"`
	Phase          int    `json:"phase"`
	DataDir        string `json:"data_dir"`
	DryRun         bool   `json:"dry_run"`

	Outcome      setup.RepairOutcome      `json:"outcome"`
	ConfigState  setup.ConfigState        `json:"config_state"`
	ConfigAction setup.RepairConfigAction `json:"config_action"`
	Steps        []setup.RepairStep       `json:"steps"`
	PermIssues   []string                 `json:"permission_issues,omitempty"`

	ConfigBackup  *backupConfigResult `json:"config_backup,omitempty"`
	ConfigWritten string              `json:"config_written,omitempty"`
	PermsFixed    []string            `json:"permissions_fixed,omitempty"`
	Health        *setupHealth        `json:"health,omitempty"`
	Blocking      []string            `json:"blocking,omitempty"`
	Summary       string              `json:"summary"`
}

// lifecycleRepair reconciles a damaged installation without touching data or
// keys: it regenerates a missing or unparseable nextsql.conf (backing up an
// unparseable one first), optionally tightens file permissions, and always
// opens the encrypted store once to run WAL recovery and confirm health. An
// existing, parseable config is left alone unless --force-config.
func lifecycleRepair(args []string) error {
	fs := flag.NewFlagSet("lifecycle repair", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory of the installation to repair")
	keyFile := fs.String("key-file", "", "root unlock key file")
	instanceKeyFile := fs.String("instance-key-file", "", "deployment-registry key file (default KEY-FILE.instance)")
	configPath := fs.String("config", "", "config file (default DATA-DIR/nextsql.conf)")
	preset := fs.String("preset", "", "resource preset for a regenerated config: conservative | balanced | high-performance | custom")
	bufferPages := fs.Int("buffer-pages", 0, "explicit buffer pool pages for a regenerated config / verification open")
	listen := fs.String("listen", config.DefaultListenAddr, "listen address for a regenerated config")
	tlsCert := fs.String("tls-cert", "", "TLS certificate for a regenerated remote listen address")
	tlsKey := fs.String("tls-key", "", "TLS key for a regenerated remote listen address")
	logLevel := fs.String("log-level", config.DefaultLogLevel, "log level for a regenerated config")
	forceConfig := fs.Bool("force-config", false, "rewrite the config even if it already parses (backs the current one up first)")
	fixPerms := fs.Bool("fix-perms", false, "tighten the config to 0640 and each unlock key file to 0600")
	dryRun := fs.Bool("dry-run", false, "report the plan; change nothing")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" || *keyFile == "" {
		return cli.LocalMissing("nextsql lifecycle repair", "--data-dir and --key-file are required")
	}

	presetVal, err := setup.ParsePreset(*preset)
	if err != nil {
		return err
	}

	confPath := *configPath
	if confPath == "" {
		confPath = filepath.Join(*dataDir, "nextsql.conf")
	}
	instKey := *instanceKeyFile
	if instKey == "" {
		instKey = *keyFile + ".instance"
	}

	res := repairResult{
		NextSQLVersion: version.String,
		Phase:          version.Phase,
		DataDir:        *dataDir,
		DryRun:         *dryRun,
	}

	// There must be a database to repair — repair is not setup.
	if _, err := os.Stat(filepath.Join(*dataDir, config.DataFileName)); err != nil {
		if os.IsNotExist(err) {
			res.Outcome = setup.RepairBlocked
			res.Blocking = []string{"no nextsql.db at this data directory — use `nextsql setup` to create one"}
			res.Summary = "nothing to repair"
			return finishRepair(res, *jsonOut,
				cli.LocalMissing("nextsql lifecycle repair", "no initialized database at this data directory"))
		}
		return nerr.Wrap(nerr.IO, "nextsql lifecycle repair", "stat database", err)
	}

	// Refuse while a server holds the deployment lock: we may rewrite the
	// config and will open the engine.
	if held, _ := lockHeldByServer(*dataDir); held {
		res.Outcome = setup.RepairBlocked
		res.Blocking = []string{"a NextSQL process is using this data directory; stop it before repairing"}
		res.Summary = "nothing was changed"
		return finishRepair(res, *jsonOut,
			nerr.New(nerr.Unavailable, "nextsql lifecycle repair",
				"a NextSQL process is using this data directory; stop it first"))
	}

	// Classify the config.
	switch _, statErr := os.Stat(confPath); {
	case statErr == nil:
		if _, loadErr := config.Load(confPath); loadErr == nil {
			res.ConfigState = setup.ConfigStateOK
		} else {
			res.ConfigState = setup.ConfigStateUnparseable
		}
	case os.IsNotExist(statErr):
		res.ConfigState = setup.ConfigStateAbsent
	default:
		return nerr.Wrap(nerr.IO, "nextsql lifecycle repair", "stat config", statErr)
	}
	res.ConfigAction = setup.PlanConfigRepair(res.ConfigState, *forceConfig)

	// Detect permission drift on the config and the resolved key files.
	res.PermIssues = permIssues(map[string]os.FileMode{
		confPath: 0o640,
		*keyFile: 0o600,
		instKey:  0o600,
	})

	res.Steps = setup.RepairPlan(setup.RepairPlanInput{
		ConfigAction: res.ConfigAction,
		FixPerms:     *fixPerms,
		PermIssues:   res.PermIssues,
	})

	if *dryRun {
		res.Outcome = setup.RepairDryRun
		res.Summary = "dry run: nothing was changed"
		return finishRepair(res, *jsonOut, nil)
	}

	mutated := false

	// Step: reconcile the config.
	if res.ConfigAction != setup.RepairConfigKeep {
		if res.ConfigAction == setup.RepairConfigBackupThenRegenerate {
			bc, berr := backupConfigFile("nextsql lifecycle repair", confPath, "")
			if bc.Backup != "" {
				res.ConfigBackup = &bc
			}
			// An unparseable source cannot be verified-copied; fall back to a
			// raw copy so we still never destroy the operator's file.
			if berr != nil && res.ConfigState == setup.ConfigStateUnparseable {
				raw, rerr := rawBackup(confPath)
				if rerr != nil {
					res.Outcome = setup.RepairFailed
					res.Summary = "could not back up the unparseable config: " + rerr.Error()
					return finishRepair(res, *jsonOut, rerr)
				}
				res.ConfigBackup = &backupConfigResult{Source: confPath, Backup: raw, Verified: false}
			} else if berr != nil {
				res.Outcome = setup.RepairFailed
				res.Summary = "config backup failed: " + berr.Error()
				return finishRepair(res, *jsonOut, berr)
			}
		}

		info, derr := sysinfo.Detect(*dataDir)
		if derr != nil {
			return derr
		}
		plan, perr := setup.BuildPlan(setup.Params{
			Base:            config.Default(),
			Info:            info,
			Preset:          presetVal,
			DataDir:         *dataDir,
			KeyFile:         *keyFile,
			InstanceKeyFile: instKey,
			ListenAddr:      *listen,
			LogLevel:        *logLevel,
			TLSCert:         *tlsCert,
			TLSKey:          *tlsKey,
			BufferPages:     *bufferPages,
			ConfigPath:      confPath,
		})
		if perr != nil {
			res.Outcome = setup.RepairFailed
			return finishRepair(res, *jsonOut, cli.Validation("nextsql lifecycle repair", perr.Error()))
		}
		if werr := writeConfigFile(confPath, plan.Config); werr != nil {
			res.Outcome = setup.RepairFailed
			return finishRepair(res, *jsonOut, werr)
		}
		res.ConfigWritten = confPath
		mutated = true
	}

	// Step: tighten permissions.
	if *fixPerms && len(res.PermIssues) > 0 {
		for path, want := range map[string]os.FileMode{confPath: 0o640, *keyFile: 0o600, instKey: 0o600} {
			if st, err := os.Stat(path); err == nil && st.Mode().Perm()&^want != 0 {
				if err := os.Chmod(path, want); err != nil {
					res.Outcome = setup.RepairFailed
					res.Summary = "chmod " + path + ": " + err.Error()
					return finishRepair(res, *jsonOut,
						nerr.Wrap(nerr.IO, "nextsql lifecycle repair", "chmod", err))
				}
				res.PermsFixed = append(res.PermsFixed, path)
				mutated = true
			}
		}
		sort.Strings(res.PermsFixed)
	}

	// Step: open the engine once (runs WAL recovery) and confirm health.
	vbuf := *bufferPages
	if vbuf <= 0 {
		if loaded, lerr := config.Load(confPath); lerr == nil && loaded.BufferPages > 0 {
			vbuf = loaded.BufferPages
		} else {
			vbuf = config.DefaultBufferPages
		}
	}
	health, herr := verifySetupHealth(*dataDir, *keyFile, vbuf)
	if herr != nil {
		res.Outcome = setup.RepairFailed
		res.Summary = "engine verification errored: " + herr.Error()
		return finishRepair(res, *jsonOut, herr)
	}
	res.Health = &health
	if !health.OK {
		res.Outcome = setup.RepairFailed
		res.Summary = "the encrypted store did not open cleanly under this binary — run `nextsql diagnose`"
		return finishRepair(res, *jsonOut,
			nerr.New(nerr.InvalidFormat, "nextsql lifecycle repair", "engine health check failed"))
	}

	if mutated {
		res.Outcome = setup.RepairRepaired
		res.Summary = fmt.Sprintf("repaired; engine healthy (%d tables, durable LSN %d)", health.Tables, health.DurableLSN)
	} else {
		res.Outcome = setup.RepairHealthy
		res.Summary = fmt.Sprintf("nothing needed repair; engine healthy (%d tables, durable LSN %d)", health.Tables, health.DurableLSN)
	}
	return finishRepair(res, *jsonOut, nil)
}

// permIssues returns a human-readable line for each path present on disk
// whose permission bits are looser than the wanted mask.
func permIssues(want map[string]os.FileMode) []string {
	var issues []string
	for path, mask := range want {
		st, err := os.Stat(path)
		if err != nil {
			continue
		}
		if extra := st.Mode().Perm() &^ mask; extra != 0 {
			issues = append(issues, fmt.Sprintf("%s is %#o, want %#o", path, st.Mode().Perm(), mask))
		}
	}
	sort.Strings(issues)
	return issues
}

// rawBackup copies src verbatim to a timestamped sibling. Used only for an
// unparseable config, which backupConfigFile refuses.
func rawBackup(src string) (string, error) {
	body, err := os.ReadFile(src)
	if err != nil {
		return "", nerr.Wrap(nerr.IO, "nextsql lifecycle repair", "read config", err)
	}
	dest := src + ".bak-" + time.Now().UTC().Format("20060102T150405Z")
	tmp := dest + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return "", nerr.Wrap(nerr.IO, "nextsql lifecycle repair", "write backup", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return "", nerr.Wrap(nerr.IO, "nextsql lifecycle repair", "rename backup", err)
	}
	return dest, nil
}

func finishRepair(res repairResult, jsonOut bool, retErr error) error {
	if jsonOut {
		_ = emitJSON(res)
		return retErr
	}
	w := os.Stdout
	fmt.Fprintf(w, "nextsql %s (phase %d)\n\n", res.NextSQLVersion, res.Phase)
	fmt.Fprintf(w, "data directory   %s\n", res.DataDir)
	fmt.Fprintf(w, "outcome          %s\n", res.Outcome)
	if res.ConfigState != "" {
		fmt.Fprintf(w, "config           %s → %s\n", res.ConfigState, res.ConfigAction)
	}
	if len(res.Steps) > 0 {
		fmt.Fprintf(w, "\nplan\n")
		for _, s := range res.Steps {
			mark := " "
			if s.Mutates {
				mark = "*"
			}
			fmt.Fprintf(w, "  [%s] %-28s %s\n", mark, s.Name, s.Detail)
		}
		fmt.Fprintf(w, "  (* changes on-disk state)\n")
	}
	if len(res.PermIssues) > 0 {
		fmt.Fprintf(w, "\npermission drift\n")
		for _, p := range res.PermIssues {
			fmt.Fprintf(w, "  - %s\n", p)
		}
	}
	if len(res.Blocking) > 0 {
		fmt.Fprintf(w, "\nblocking\n")
		for _, b := range res.Blocking {
			fmt.Fprintf(w, "  - %s\n", b)
		}
	}
	if res.ConfigBackup != nil {
		fmt.Fprintf(w, "\nconfig backup    %s (verified=%t)\n", res.ConfigBackup.Backup, res.ConfigBackup.Verified)
	}
	if res.ConfigWritten != "" {
		fmt.Fprintf(w, "config written   %s\n", res.ConfigWritten)
	}
	for _, p := range res.PermsFixed {
		fmt.Fprintf(w, "perms fixed      %s\n", p)
	}
	if res.Health != nil {
		status := "FAILED"
		if res.Health.OK {
			status = "ok"
		}
		fmt.Fprintf(w, "engine health    %s (tables=%d durable_lsn=%d)\n", status, res.Health.Tables, res.Health.DurableLSN)
	}
	if res.Summary != "" {
		fmt.Fprintf(w, "\n%s\n", res.Summary)
	}
	return retErr
}

// ---- uninstall -----------------------------------------------------

type uninstallResult struct {
	NextSQLVersion string `json:"nextsql_version"`
	Phase          int    `json:"phase"`
	DataDir        string `json:"data_dir"`
	Confirmed      bool   `json:"confirmed"`
	PurgeData      bool   `json:"purge_data"`
	PurgeKeys      bool   `json:"purge_keys"`

	Outcome  setup.UninstallOutcome    `json:"outcome"`
	Remove   []setup.UninstallArtifact `json:"remove"`
	Preserve []setup.UninstallArtifact `json:"preserve"`
	Blocking []string                  `json:"blocking,omitempty"`
	Removed  []string                  `json:"removed,omitempty"`
	Failed   []string                  `json:"failed,omitempty"`
	Summary  string                    `json:"summary"`
}

// lifecycleUninstall removes a NextSQL installation. It preserves the
// encrypted database and the external unlock keys unless the operator
// explicitly opts into `--purge-data` / `--purge-keys`, refuses to run while
// a server holds the deployment lock, and is a dry run until `--confirm`.
func lifecycleUninstall(args []string) error {
	fs := flag.NewFlagSet("lifecycle uninstall", flag.ContinueOnError)
	dataDir := fs.String("data-dir", "", "data directory of the installation to remove")
	configPath := fs.String("config", "", "config file (default DATA-DIR/nextsql.conf)")
	keyFile := fs.String("key-file", "", "root unlock key file (default: resolved from the config)")
	instanceKeyFile := fs.String("instance-key-file", "", "deployment-registry key file (default KEY-FILE.instance)")
	purgeData := fs.Bool("purge-data", false, "also delete the encrypted database and its sidecars — DESTROYS ALL DATA")
	purgeKeys := fs.Bool("purge-keys", false, "also delete the external unlock keys (requires --purge-data)")
	confirm := fs.Bool("confirm", false, "actually delete; without it this is a dry run")
	jsonOut := fs.Bool("json", false, "emit a single machine-readable JSON object")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *dataDir == "" {
		return cli.LocalMissing("nextsql lifecycle uninstall", "--data-dir is required")
	}

	confPath := *configPath
	if confPath == "" {
		confPath = filepath.Join(*dataDir, "nextsql.conf")
	}

	res := uninstallResult{
		NextSQLVersion: version.String,
		Phase:          version.Phase,
		DataDir:        *dataDir,
		Confirmed:      *confirm,
		PurgeData:      *purgeData,
		PurgeKeys:      *purgeKeys,
	}

	in := setup.UninstallInput{PurgeData: *purgeData, PurgeKeys: *purgeKeys}

	// Config + its timestamped backups.
	if _, err := os.Stat(confPath); err == nil {
		in.ConfigPath = confPath
	} else if !os.IsNotExist(err) {
		return nerr.Wrap(nerr.IO, "nextsql lifecycle uninstall", "stat config", err)
	}
	confDir := filepath.Dir(confPath)
	if entries, err := os.ReadDir(confDir); err == nil {
		prefix := filepath.Base(confPath) + ".bak-"
		for _, e := range entries {
			if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
				in.ConfigBackups = append(in.ConfigBackups, filepath.Join(confDir, e.Name()))
			}
		}
		sort.Strings(in.ConfigBackups)
	}

	// Resolve key paths: explicit flags win, otherwise the parsed config.
	var cfg config.Config
	cfgOK := false
	if in.ConfigPath != "" {
		if loaded, lerr := config.Load(confPath); lerr == nil {
			cfg, cfgOK = loaded, true
		}
	}
	rootKey := *keyFile
	if rootKey == "" && cfgOK {
		rootKey = cfg.KeyFile
	}
	instKey := *instanceKeyFile
	if instKey == "" {
		if *keyFile != "" {
			instKey = *keyFile + ".instance"
		} else if cfgOK {
			instKey = cfg.InstanceRootFile()
		}
	}
	in.KeyPathsResolved = rootKey != "" || instKey != ""

	// Data-dir artifacts that actually exist. Covers the primary database and
	// its sidecars, the deployment-registry database, and the deployment lock.
	dbPath := filepath.Join(*dataDir, config.DataFileName)
	regPath := hosting.Path(*dataDir)
	dataCandidates := []struct{ path, kind string }{
		{dbPath, "database"},
		{crypto.KeystorePath(dbPath), "keystore"},
		{wal.DirFor(dbPath), "wal-dir"},
		{undo.DirFor(dbPath), "undo-dir"},
		{integrity.PathFor(dbPath), "isolated-sidecar"},
		{filepath.Join(*dataDir, config.AuthFileName), "auth-file"},
		{filepath.Join(*dataDir, config.ACLFileName), "acl-file"},
		{regPath, "registry"},
		{crypto.KeystorePath(regPath), "registry-keystore"},
		{wal.DirFor(regPath), "registry-wal-dir"},
		{undo.DirFor(regPath), "registry-undo-dir"},
		{hosting.LockPath(*dataDir), "deployment-lock"},
	}
	if cfgOK {
		if p := cfg.AuditPath(); p != "" {
			dataCandidates = append(dataCandidates, struct{ path, kind string }{p, "audit-file"})
		}
	}
	for _, c := range dataCandidates {
		if _, err := os.Stat(c.path); err == nil {
			in.DataArtifacts = append(in.DataArtifacts,
				setup.UninstallArtifact{Path: c.path, Kind: c.kind, Category: setup.UninstallData})
		}
	}

	for _, c := range []struct{ path, kind string }{
		{rootKey, "root-key"},
		{instKey, "instance-key"},
	} {
		if c.path == "" {
			continue
		}
		if _, err := os.Stat(c.path); err == nil {
			in.KeyArtifacts = append(in.KeyArtifacts,
				setup.UninstallArtifact{Path: c.path, Kind: c.kind, Category: setup.UninstallKeys})
		}
	}

	held, _ := lockHeldByServer(*dataDir)
	in.LockHeld = held

	decision := setup.PlanUninstall(in)
	res.Remove = decision.Remove
	res.Preserve = decision.Preserve
	res.Blocking = decision.Blocking

	if !decision.OK() {
		res.Outcome = setup.UninstallBlocked
		res.Summary = "nothing was deleted"
		if held {
			return finishUninstall(res, *jsonOut,
				nerr.New(nerr.Unavailable, "nextsql lifecycle uninstall",
					"a NextSQL process is using this data directory; stop it first"))
		}
		return finishUninstall(res, *jsonOut,
			cli.Validation("nextsql lifecycle uninstall", "requested purge flags are inconsistent"))
	}

	if len(decision.Remove) == 0 {
		res.Outcome = setup.UninstallRemoved
		res.Summary = "no NextSQL installation artifacts found at this data directory"
		return finishUninstall(res, *jsonOut, nil)
	}

	if !*confirm {
		res.Outcome = setup.UninstallPlanned
		res.Summary = fmt.Sprintf("dry run: %d artifact(s) would be removed, %d preserved — re-run with --confirm",
			len(decision.Remove), len(decision.Preserve))
		return finishUninstall(res, *jsonOut, nil)
	}

	for _, a := range decision.Remove {
		if err := os.RemoveAll(a.Path); err != nil {
			res.Failed = append(res.Failed, a.Path+": "+err.Error())
		} else {
			res.Removed = append(res.Removed, a.Path)
		}
	}
	if len(res.Failed) > 0 {
		res.Outcome = setup.UninstallPartial
		res.Summary = fmt.Sprintf("removed %d artifact(s); %d could not be removed", len(res.Removed), len(res.Failed))
		return finishUninstall(res, *jsonOut,
			nerr.New(nerr.InvalidFormat, "nextsql lifecycle uninstall", "some artifacts could not be removed"))
	}
	res.Outcome = setup.UninstallRemoved
	res.Summary = fmt.Sprintf("removed %d artifact(s); %d preserved", len(res.Removed), len(decision.Preserve))
	return finishUninstall(res, *jsonOut, nil)
}

func finishUninstall(res uninstallResult, jsonOut bool, retErr error) error {
	if jsonOut {
		_ = emitJSON(res)
		return retErr
	}
	w := os.Stdout
	fmt.Fprintf(w, "nextsql %s (phase %d)\n\n", res.NextSQLVersion, res.Phase)
	fmt.Fprintf(w, "data directory   %s\n", res.DataDir)
	fmt.Fprintf(w, "outcome          %s\n", res.Outcome)
	if len(res.Blocking) > 0 {
		fmt.Fprintf(w, "\nblocking\n")
		for _, b := range res.Blocking {
			fmt.Fprintf(w, "  - %s\n", b)
		}
	}
	if len(res.Remove) > 0 {
		verb := "would remove"
		if res.Confirmed && res.Outcome != setup.UninstallBlocked {
			verb = "remove"
		}
		fmt.Fprintf(w, "\n%s\n", verb)
		for _, a := range res.Remove {
			fmt.Fprintf(w, "  [%s] %-16s %s\n", a.Category, a.Kind, a.Path)
		}
	}
	if len(res.Preserve) > 0 {
		fmt.Fprintf(w, "\npreserve\n")
		for _, a := range res.Preserve {
			fmt.Fprintf(w, "  [%s] %-16s %s\n", a.Category, a.Kind, a.Path)
		}
	}
	if len(res.Failed) > 0 {
		fmt.Fprintf(w, "\nfailed\n")
		for _, f := range res.Failed {
			fmt.Fprintf(w, "  - %s\n", f)
		}
	}
	if res.Summary != "" {
		fmt.Fprintf(w, "\n%s\n", res.Summary)
	}
	return retErr
}

// ---- shared helpers --------------------------------------------------

func emitJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func presentWord(b bool) string {
	if b {
		return "present"
	}
	return "absent"
}

func compatWord(b bool) string {
	if b {
		return "compatible"
	}
	return "INCOMPATIBLE"
}
