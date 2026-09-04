package setup

import "strings"

// This file is the decision logic for the installer *lifecycle* surface —
// detecting an existing installation and deciding whether this binary may
// upgrade it in place. Like the rest of the package it performs no I/O: the
// command layer stats the filesystem, parses the config, checks the
// deployment lock, and reads on-disk headers, then hands the observations
// here for classification so the CLI, container init, and the eventual GUI
// installer share one verdict.

// InstallStatus classifies what an installer or automation run finds at a
// data directory before it does anything.
type InstallStatus string

const (
	// InstallNone: neither a config file nor an initialized database.
	InstallNone InstallStatus = "none"
	// InstallConfigOnly: a nextsql.conf is present but the database has not
	// been initialized (no nextsql.db). `nextsql setup --skip-init` leaves
	// this state; a full `nextsql setup` completes it.
	InstallConfigOnly InstallStatus = "config-only"
	// InstallInitialized: an initialized database is present and no NextSQL
	// process is holding the deployment lock.
	InstallInitialized InstallStatus = "initialized"
	// InstallRunning: a NextSQL process holds the deployment lock. Offline
	// lifecycle operations (upgrade, repair) must not proceed.
	InstallRunning InstallStatus = "running"
)

// DetectInput is what the command layer observed on disk.
type DetectInput struct {
	ConfigPresent   bool
	DataFilePresent bool
	KeystorePresent bool
	LockHeld        bool
}

// ClassifyInstall reduces the observations to a single status. A held
// deployment lock always wins; otherwise an initialized database (data file
// plus keystore) outranks a lone config file.
func ClassifyInstall(in DetectInput) InstallStatus {
	switch {
	case in.LockHeld:
		return InstallRunning
	case in.DataFilePresent && in.KeystorePresent:
		return InstallInitialized
	case in.ConfigPresent:
		return InstallConfigOnly
	default:
		return InstallNone
	}
}

// FormatDirection describes how one on-disk format family compares to what
// this binary supports.
type FormatDirection string

const (
	FormatOK      FormatDirection = "ok"      // within the supported window
	FormatTooOld  FormatDirection = "too-old" // predates MinReadable: needs backup/restore migration
	FormatTooNew  FormatDirection = "too-new" // exceeds MaxReadable: needs a newer nextsqld
	FormatAbsent  FormatDirection = "absent"  // file not present
	FormatDamaged FormatDirection = "damaged" // wrong magic, checksum, or a read error
)

// FamilyCheck is one on-disk format family's preflight result.
type FamilyCheck struct {
	Family    string          `json:"family"`
	Version   uint16          `json:"version"`
	Direction FormatDirection `json:"direction"`
	Detail    string          `json:"detail,omitempty"`
}

// UpgradeVerdict is the preflight conclusion for an in-place upgrade.
type UpgradeVerdict string

const (
	// UpgradeReady: every present header is within this binary's window.
	UpgradeReady UpgradeVerdict = "ready"
	// UpgradeNotInitialized: nothing to upgrade (no page/superblock).
	UpgradeNotInitialized UpgradeVerdict = "not-initialized"
	// UpgradeServerRunning: a NextSQL process holds the deployment lock.
	UpgradeServerRunning UpgradeVerdict = "server-running"
	// UpgradeBlockedTooNew: at least one header is newer than this binary
	// supports — install a newer nextsqld.
	UpgradeBlockedTooNew UpgradeVerdict = "blocked-too-new"
	// UpgradeBlockedTooOld: at least one header predates this binary's
	// minimum — migrate via backup/restore into a fresh database.
	UpgradeBlockedTooOld UpgradeVerdict = "blocked-too-old"
	// UpgradeBlockedDamaged: a header is missing, has the wrong magic, or
	// failed to decode.
	UpgradeBlockedDamaged UpgradeVerdict = "blocked-damaged"
)

// UpgradeInput carries the command layer's observations for AssessUpgrade.
type UpgradeInput struct {
	// Initialized is true when the page/superblock family is present and
	// readable — i.e. there is a database to upgrade at all.
	Initialized bool
	LockHeld    bool
	Families    []FamilyCheck
}

// UpgradeAssessment is the full preflight result, rendered as text or JSON.
type UpgradeAssessment struct {
	Verdict  UpgradeVerdict `json:"verdict"`
	Summary  string         `json:"summary"`
	Blocking []string       `json:"blocking,omitempty"`
	Families []FamilyCheck  `json:"families"`
}

// AssessUpgrade decides whether this binary may open the data directory in
// place. The precedence is deliberate: a running server blocks everything;
// an uninitialized directory has nothing to assess; a damaged header is
// reported before a merely out-of-window one; and "too new" outranks "too
// old" because the fix (install a newer binary) also resolves the old files.
func AssessUpgrade(in UpgradeInput) UpgradeAssessment {
	a := UpgradeAssessment{Families: in.Families}

	if in.LockHeld {
		a.Verdict = UpgradeServerRunning
		a.Blocking = []string{"a NextSQL process is using this data directory; stop it before an offline upgrade or repair"}
		a.Summary = "a NextSQL process holds the deployment lock"
		return a
	}
	if !in.Initialized {
		a.Verdict = UpgradeNotInitialized
		a.Summary = "no initialized database found at this data directory"
		return a
	}

	var damaged, tooNew, tooOld []string
	for _, f := range in.Families {
		switch f.Direction {
		case FormatDamaged:
			damaged = append(damaged, f.Family)
		case FormatTooNew:
			tooNew = append(tooNew, f.Family)
		case FormatTooOld:
			tooOld = append(tooOld, f.Family)
		}
	}

	switch {
	case len(damaged) > 0:
		a.Verdict = UpgradeBlockedDamaged
		a.Blocking = []string{"unreadable header(s): " + strings.Join(damaged, ", ") +
			" — run `nextsql diagnose` and restore from a verified backup"}
		a.Summary = "on-disk headers did not decode cleanly"
	case len(tooNew) > 0:
		a.Verdict = UpgradeBlockedTooNew
		a.Blocking = []string{"header(s) newer than this binary: " + strings.Join(tooNew, ", ") +
			" — install a newer nextsqld"}
		a.Summary = "the data directory was written by a newer NextSQL"
	case len(tooOld) > 0:
		a.Verdict = UpgradeBlockedTooOld
		a.Blocking = []string{"header(s) older than this binary supports: " + strings.Join(tooOld, ", ") +
			" — migrate with `nextsql export` / `nextsql import` into a freshly initialized database"}
		a.Summary = "the data directory predates this binary's supported format window"
	default:
		a.Verdict = UpgradeReady
		a.Summary = "every on-disk header is within this binary's supported format window"
	}
	return a
}

// OK reports whether the assessment permits an in-place upgrade.
func (a UpgradeAssessment) OK() bool { return a.Verdict == UpgradeReady }

// ---- in-place upgrade plan -------------------------------------------

// UpgradeStep is one ordered action the in-place upgrade runner performs.
// Mutates flags whether the step changes anything on disk — the runner uses
// it to decide what a --dry-run may skip, and the eventual GUI installer
// uses the same list for staged progress.
type UpgradeStep struct {
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Mutates bool   `json:"mutates"`
}

// UpgradePlanInput is what the command layer knows before it starts.
type UpgradePlanInput struct {
	// ConfigPresent is true when a nextsql.conf exists to be backed up.
	ConfigPresent bool
}

// UpgradePlan returns the ordered steps an in-place upgrade takes so the
// dry-run output and the GUI's staged progress come from one source. The
// engine open is always a mutation (it runs WAL recovery); the config
// backup is only listed when there is a config to copy.
func UpgradePlan(in UpgradePlanInput) []UpgradeStep {
	steps := []UpgradeStep{
		{Name: "acquire-deployment-lock", Detail: "take the exclusive data-directory lock for the whole operation", Mutates: false},
		{Name: "preflight", Detail: "check every on-disk header against this binary's supported format window", Mutates: false},
	}
	if in.ConfigPresent {
		steps = append(steps, UpgradeStep{
			Name:    "backup-config",
			Detail:  "copy nextsql.conf to a timestamped backup and confirm it reloads identically",
			Mutates: true,
		})
	}
	steps = append(steps,
		UpgradeStep{Name: "open-engine", Detail: "open the encrypted store with this binary, running WAL recovery", Mutates: true},
		UpgradeStep{Name: "re-verify", Detail: "re-read the headers and confirm the catalog loads cleanly", Mutates: false},
	)
	return steps
}

// UpgradeOutcome is the conclusion of an in-place upgrade attempt.
type UpgradeOutcome string

const (
	// UpgradeApplied: the engine opened, recovery ran, and re-verification
	// passed under this binary.
	UpgradeApplied UpgradeOutcome = "applied"
	// UpgradeDryRun: preflight was ready and the plan was printed; nothing
	// was backed up, opened, or mutated.
	UpgradeDryRun UpgradeOutcome = "dry-run"
	// UpgradeBlocked: preflight refused the upgrade; nothing was mutated.
	UpgradeBlocked UpgradeOutcome = "blocked"
	// UpgradeFailedVerify: the config backup failed, the engine could not be
	// opened, or the headers were still not compatible afterwards. Any
	// config backup already written is retained for rollback.
	UpgradeFailedVerify UpgradeOutcome = "failed-verify"
)

// ---- uninstall plan -------------------------------------------------

// UninstallCategory groups an install artifact by how dangerous removing it
// is. The uninstaller only ever deletes a category the operator opted into.
type UninstallCategory string

const (
	// UninstallSafe: installer-generated files that can be regenerated from
	// the running database — the config and its timestamped backups.
	// Removed by a plain `--confirm`.
	UninstallSafe UninstallCategory = "safe"
	// UninstallData: the encrypted database and its sidecars (keystore, WAL,
	// UNDO, isolated-page registry, auth/ACL/audit files). Removing this
	// destroys all data. Needs `--purge-data`.
	UninstallData UninstallCategory = "data"
	// UninstallKeys: the external root / instance unlock key files. Removing
	// these makes any surviving or backed-up copy of the database
	// permanently unreadable. Needs `--purge-keys`, which itself requires
	// `--purge-data`.
	UninstallKeys UninstallCategory = "keys"
)

// UninstallArtifact is one path the uninstaller knows about.
type UninstallArtifact struct {
	Path     string            `json:"path"`
	Kind     string            `json:"kind"`
	Category UninstallCategory `json:"category"`
}

// UninstallInput is what the command layer resolved and observed on disk.
// Only artifacts that actually exist should be passed.
type UninstallInput struct {
	ConfigPath    string              // present nextsql.conf, or ""
	ConfigBackups []string            // present nextsql.conf.bak-* siblings
	DataArtifacts []UninstallArtifact // present data-dir files/dirs
	KeyArtifacts  []UninstallArtifact // present external key files
	// KeyPathsResolved is true when the command layer knows where the
	// unlock keys live (from --key-file or a parseable config). When it is
	// false a --purge-keys request cannot be honored and is blocked rather
	// than silently skipped.
	KeyPathsResolved bool
	LockHeld         bool
	PurgeData        bool
	PurgeKeys        bool
}

// UninstallDecision is the resolved plan: every known artifact lands in
// exactly one of Remove / Preserve, and Blocking lists the reasons the
// command layer must refuse to act at all (nothing is deleted while any
// blocking reason stands).
type UninstallDecision struct {
	Remove   []UninstallArtifact `json:"remove"`
	Preserve []UninstallArtifact `json:"preserve"`
	Blocking []string            `json:"blocking,omitempty"`
}

// PlanUninstall classifies every artifact into remove / preserve for the
// requested purge flags. A data or key artifact is never placed in Remove
// without its matching flag; the flag-dependency and running-server errors
// are reported as Blocking reasons rather than silently downgraded.
func PlanUninstall(in UninstallInput) UninstallDecision {
	var d UninstallDecision

	if in.LockHeld {
		d.Blocking = append(d.Blocking,
			"a NextSQL process is using this data directory; stop it before uninstalling")
	}
	if in.PurgeKeys && !in.PurgeData {
		d.Blocking = append(d.Blocking,
			"--purge-keys requires --purge-data: deleting the unlock keys while the database survives leaves it permanently unreadable")
	}
	if in.PurgeKeys && in.PurgeData && !in.KeyPathsResolved {
		d.Blocking = append(d.Blocking,
			"--purge-keys was requested but the unlock key paths could not be resolved (no parseable config and no --key-file); pass --key-file explicitly")
	}

	// Safe: the config and its backups are always removed.
	if in.ConfigPath != "" {
		d.Remove = append(d.Remove, UninstallArtifact{Path: in.ConfigPath, Kind: "config", Category: UninstallSafe})
	}
	for _, b := range in.ConfigBackups {
		d.Remove = append(d.Remove, UninstallArtifact{Path: b, Kind: "config-backup", Category: UninstallSafe})
	}

	for _, a := range in.DataArtifacts {
		if in.PurgeData {
			d.Remove = append(d.Remove, a)
		} else {
			d.Preserve = append(d.Preserve, a)
		}
	}
	for _, a := range in.KeyArtifacts {
		if in.PurgeData && in.PurgeKeys {
			d.Remove = append(d.Remove, a)
		} else {
			d.Preserve = append(d.Preserve, a)
		}
	}
	return d
}

// OK reports whether the command layer may proceed to delete.
func (d UninstallDecision) OK() bool { return len(d.Blocking) == 0 }

// UninstallOutcome is the conclusion of an uninstall attempt.
type UninstallOutcome string

const (
	// UninstallPlanned: `--confirm` was not given; the plan was printed and
	// nothing was deleted.
	UninstallPlanned UninstallOutcome = "planned"
	// UninstallRemoved: every artifact in the plan was removed.
	UninstallRemoved UninstallOutcome = "removed"
	// UninstallBlocked: a blocking reason stood; nothing was deleted.
	UninstallBlocked UninstallOutcome = "blocked"
	// UninstallPartial: deletion started but at least one path could not be
	// removed.
	UninstallPartial UninstallOutcome = "partial"
)

// ---- repair plan --------------------------------------------------

// ConfigState is what the command layer found the nextsql.conf to be in.
type ConfigState string

const (
	ConfigStateOK          ConfigState = "ok"          // present and parses
	ConfigStateAbsent      ConfigState = "absent"      // no file
	ConfigStateUnparseable ConfigState = "unparseable" // present, config.Load fails
)

// RepairConfigAction is what `repair` will do about the config file. An
// existing, parseable config is never rewritten unless --force-config; an
// unparseable one is backed up before being replaced.
type RepairConfigAction string

const (
	RepairConfigKeep                 RepairConfigAction = "keep"
	RepairConfigRegenerate           RepairConfigAction = "regenerate"
	RepairConfigBackupThenRegenerate RepairConfigAction = "backup-then-regenerate"
)

// PlanConfigRepair decides the config action from its observed state and the
// --force-config flag.
func PlanConfigRepair(state ConfigState, forceConfig bool) RepairConfigAction {
	switch state {
	case ConfigStateAbsent:
		return RepairConfigRegenerate
	case ConfigStateUnparseable:
		return RepairConfigBackupThenRegenerate
	default: // ok
		if forceConfig {
			return RepairConfigBackupThenRegenerate
		}
		return RepairConfigKeep
	}
}

// RepairStep is one ordered action `repair` performs. Mutates is true when
// the step changes something on disk (a --dry-run stops before those).
type RepairStep struct {
	Name    string `json:"name"`
	Detail  string `json:"detail"`
	Mutates bool   `json:"mutates"`
}

// RepairPlanInput is what the command layer resolved before acting.
type RepairPlanInput struct {
	ConfigAction RepairConfigAction
	// FixPerms is the --fix-perms flag; PermIssues is the human-readable
	// list of drifted files the command layer detected (config not 0640, a
	// key file looser than 0600).
	FixPerms   bool
	PermIssues []string
}

// RepairPlan returns the ordered steps `repair` takes: reconcile the config,
// optionally tighten permissions, then always open the engine once (which
// runs WAL recovery) to confirm health.
func RepairPlan(in RepairPlanInput) []RepairStep {
	var steps []RepairStep

	switch in.ConfigAction {
	case RepairConfigRegenerate:
		steps = append(steps, RepairStep{
			Name: "regenerate-config", Mutates: true,
			Detail: "write a fresh nextsql.conf with secure defaults (no config file was present)",
		})
	case RepairConfigBackupThenRegenerate:
		steps = append(steps, RepairStep{
			Name: "backup-and-regenerate-config", Mutates: true,
			Detail: "copy the current nextsql.conf to a timestamped backup, then write a fresh one",
		})
	default:
		steps = append(steps, RepairStep{
			Name: "keep-config", Mutates: false,
			Detail: "the existing nextsql.conf parses cleanly and is left as-is (pass --force-config to rewrite)",
		})
	}

	if len(in.PermIssues) > 0 {
		if in.FixPerms {
			steps = append(steps, RepairStep{
				Name: "fix-permissions", Mutates: true,
				Detail: "tighten the config to 0640 and each unlock key file to 0600",
			})
		} else {
			steps = append(steps, RepairStep{
				Name: "report-permissions", Mutates: false,
				Detail: "permission drift detected — pass --fix-perms to correct it",
			})
		}
	}

	steps = append(steps, RepairStep{
		Name: "verify-engine", Mutates: true,
		Detail: "open the encrypted store once (runs WAL recovery) and report table count and durable LSN",
	})
	return steps
}

// RepairOutcome is the conclusion of a repair attempt.
type RepairOutcome string

const (
	// RepairRepaired: at least one mutating step ran and the engine verified.
	RepairRepaired RepairOutcome = "repaired"
	// RepairHealthy: nothing needed fixing; the engine verified.
	RepairHealthy RepairOutcome = "healthy"
	// RepairDryRun: --dry-run; the plan was printed and nothing changed.
	RepairDryRun RepairOutcome = "dry-run"
	// RepairBlocked: a server holds the lock, or there is no database to
	// repair. Nothing changed.
	RepairBlocked RepairOutcome = "blocked"
	// RepairFailed: a step failed — most importantly, the engine would not
	// open. Any config backup taken is retained.
	RepairFailed RepairOutcome = "failed"
)
