package setup

import "testing"

func TestClassifyInstall(t *testing.T) {
	cases := []struct {
		name string
		in   DetectInput
		want InstallStatus
	}{
		{"empty", DetectInput{}, InstallNone},
		{"config only", DetectInput{ConfigPresent: true}, InstallConfigOnly},
		{"data without keystore is not initialized", DetectInput{DataFilePresent: true}, InstallNone},
		{"initialized", DetectInput{ConfigPresent: true, DataFilePresent: true, KeystorePresent: true}, InstallInitialized},
		{"lock wins over everything", DetectInput{DataFilePresent: true, KeystorePresent: true, LockHeld: true}, InstallRunning},
		{"lock wins even with nothing else", DetectInput{LockHeld: true}, InstallRunning},
	}
	for _, c := range cases {
		if got := ClassifyInstall(c.in); got != c.want {
			t.Errorf("%s: ClassifyInstall = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestAssessUpgradeReady(t *testing.T) {
	a := AssessUpgrade(UpgradeInput{
		Initialized: true,
		Families: []FamilyCheck{
			{Family: "page", Version: 1, Direction: FormatOK},
			{Family: "wal_control", Version: 1, Direction: FormatOK},
			{Family: "wal_control", Version: 1, Direction: FormatAbsent}, // absent is not blocking
		},
	})
	if !a.OK() || a.Verdict != UpgradeReady {
		t.Fatalf("verdict = %q, want ready", a.Verdict)
	}
	if len(a.Blocking) != 0 {
		t.Errorf("unexpected blocking reasons: %v", a.Blocking)
	}
}

func TestAssessUpgradeServerRunningOutranksFormat(t *testing.T) {
	a := AssessUpgrade(UpgradeInput{
		Initialized: true,
		LockHeld:    true,
		Families:    []FamilyCheck{{Family: "page", Version: 9, Direction: FormatTooNew}},
	})
	if a.Verdict != UpgradeServerRunning {
		t.Fatalf("verdict = %q, want server-running", a.Verdict)
	}
}

func TestAssessUpgradeNotInitialized(t *testing.T) {
	a := AssessUpgrade(UpgradeInput{Initialized: false})
	if a.Verdict != UpgradeNotInitialized || a.OK() {
		t.Fatalf("verdict = %q, want not-initialized", a.Verdict)
	}
}

func TestAssessUpgradeDamagedOutranksVersionSkew(t *testing.T) {
	a := AssessUpgrade(UpgradeInput{
		Initialized: true,
		Families: []FamilyCheck{
			{Family: "page", Version: 0, Direction: FormatDamaged},
			{Family: "wal_control", Version: 9, Direction: FormatTooNew},
		},
	})
	if a.Verdict != UpgradeBlockedDamaged {
		t.Fatalf("verdict = %q, want blocked-damaged", a.Verdict)
	}
	if len(a.Blocking) == 0 {
		t.Error("expected a blocking reason naming the damaged family")
	}
}

func TestAssessUpgradeTooNewOutranksTooOld(t *testing.T) {
	a := AssessUpgrade(UpgradeInput{
		Initialized: true,
		Families: []FamilyCheck{
			{Family: "page", Version: 99, Direction: FormatTooNew},
			{Family: "catalog", Version: 1, Direction: FormatTooOld},
		},
	})
	if a.Verdict != UpgradeBlockedTooNew {
		t.Fatalf("verdict = %q, want blocked-too-new", a.Verdict)
	}
}

func TestAssessUpgradeTooOld(t *testing.T) {
	a := AssessUpgrade(UpgradeInput{
		Initialized: true,
		Families:    []FamilyCheck{{Family: "catalog", Version: 0, Direction: FormatTooOld}},
	})
	if a.Verdict != UpgradeBlockedTooOld {
		t.Fatalf("verdict = %q, want blocked-too-old", a.Verdict)
	}
}

func stepNames(steps []UpgradeStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}

func TestUpgradePlanIncludesConfigBackupOnlyWhenPresent(t *testing.T) {
	with := stepNames(UpgradePlan(UpgradePlanInput{ConfigPresent: true}))
	want := []string{"acquire-deployment-lock", "preflight", "backup-config", "open-engine", "re-verify"}
	if len(with) != len(want) {
		t.Fatalf("steps = %v, want %v", with, want)
	}
	for i := range want {
		if with[i] != want[i] {
			t.Fatalf("steps = %v, want %v", with, want)
		}
	}

	without := stepNames(UpgradePlan(UpgradePlanInput{ConfigPresent: false}))
	for _, n := range without {
		if n == "backup-config" {
			t.Fatalf("no config present but plan still backs one up: %v", without)
		}
	}
}

func TestUpgradePlanOpenEngineAlwaysMutates(t *testing.T) {
	for _, s := range UpgradePlan(UpgradePlanInput{ConfigPresent: true}) {
		switch s.Name {
		case "open-engine", "backup-config":
			if !s.Mutates {
				t.Errorf("step %q should be marked as mutating", s.Name)
			}
		case "acquire-deployment-lock", "preflight", "re-verify":
			if s.Mutates {
				t.Errorf("step %q should not be marked as mutating", s.Name)
			}
		}
	}
}

func removePaths(d UninstallDecision) map[string]UninstallCategory {
	m := make(map[string]UninstallCategory)
	for _, a := range d.Remove {
		m[a.Path] = a.Category
	}
	return m
}

func TestPlanUninstallDefaultKeepsDataAndKeys(t *testing.T) {
	d := PlanUninstall(UninstallInput{
		ConfigPath:    "/etc/nextsql/nextsql.conf",
		ConfigBackups: []string{"/etc/nextsql/nextsql.conf.bak-20260101T000000Z"},
		DataArtifacts: []UninstallArtifact{{Path: "/var/lib/nextsql/nextsql.db", Kind: "database", Category: UninstallData}},
		KeyArtifacts:  []UninstallArtifact{{Path: "/etc/nextsql/root.key", Kind: "root-key", Category: UninstallKeys}},
	})
	if !d.OK() {
		t.Fatalf("unexpected blocking: %v", d.Blocking)
	}
	rm := removePaths(d)
	if len(rm) != 2 {
		t.Fatalf("default uninstall should remove only config + backup, got %v", rm)
	}
	for _, a := range d.Remove {
		if a.Category != UninstallSafe {
			t.Errorf("default uninstall removed a non-safe artifact: %+v", a)
		}
	}
	var sawData, sawKeys bool
	for _, a := range d.Preserve {
		sawData = sawData || a.Category == UninstallData
		sawKeys = sawKeys || a.Category == UninstallKeys
	}
	if !sawData || !sawKeys {
		t.Fatalf("data and keys must be preserved by default; preserve = %+v", d.Preserve)
	}
}

func TestPlanUninstallPurgeData(t *testing.T) {
	d := PlanUninstall(UninstallInput{
		DataArtifacts: []UninstallArtifact{{Path: "/db", Kind: "database", Category: UninstallData}},
		KeyArtifacts:  []UninstallArtifact{{Path: "/k", Kind: "root-key", Category: UninstallKeys}},
		PurgeData:     true,
	})
	if !d.OK() {
		t.Fatalf("unexpected blocking: %v", d.Blocking)
	}
	rm := removePaths(d)
	if rm["/db"] != UninstallData {
		t.Errorf("--purge-data should remove the database, got %v", rm)
	}
	if _, ok := rm["/k"]; ok {
		t.Errorf("--purge-data alone must not remove keys")
	}
}

func TestPlanUninstallPurgeKeysRequiresPurgeData(t *testing.T) {
	d := PlanUninstall(UninstallInput{
		KeyArtifacts: []UninstallArtifact{{Path: "/k", Kind: "root-key", Category: UninstallKeys}},
		PurgeKeys:    true,
	})
	if d.OK() {
		t.Fatal("--purge-keys without --purge-data must be blocked")
	}
	if _, ok := removePaths(d)["/k"]; ok {
		t.Error("a blocked plan must not schedule the key for removal-execution")
	}
}

func TestPlanUninstallPurgeDataAndKeys(t *testing.T) {
	d := PlanUninstall(UninstallInput{
		DataArtifacts:    []UninstallArtifact{{Path: "/db", Kind: "database", Category: UninstallData}},
		KeyArtifacts:     []UninstallArtifact{{Path: "/k", Kind: "root-key", Category: UninstallKeys}},
		KeyPathsResolved: true,
		PurgeData:        true,
		PurgeKeys:        true,
	})
	if !d.OK() {
		t.Fatalf("unexpected blocking: %v", d.Blocking)
	}
	rm := removePaths(d)
	if rm["/db"] != UninstallData || rm["/k"] != UninstallKeys {
		t.Errorf("--purge-data --purge-keys should remove both, got %v", rm)
	}
}

func TestPlanUninstallPurgeKeysUnresolvedBlocks(t *testing.T) {
	d := PlanUninstall(UninstallInput{
		DataArtifacts:    []UninstallArtifact{{Path: "/db", Kind: "database", Category: UninstallData}},
		KeyPathsResolved: false,
		PurgeData:        true,
		PurgeKeys:        true,
	})
	if d.OK() {
		t.Fatal("--purge-keys with unresolved key paths must be blocked, not silently skipped")
	}
}

func TestPlanUninstallRunningServerBlocks(t *testing.T) {
	d := PlanUninstall(UninstallInput{
		ConfigPath: "/c",
		LockHeld:   true,
	})
	if d.OK() {
		t.Fatal("a running server must block the uninstall")
	}
}

func TestPlanConfigRepair(t *testing.T) {
	cases := []struct {
		state ConfigState
		force bool
		want  RepairConfigAction
	}{
		{ConfigStateAbsent, false, RepairConfigRegenerate},
		{ConfigStateAbsent, true, RepairConfigRegenerate},
		{ConfigStateUnparseable, false, RepairConfigBackupThenRegenerate},
		{ConfigStateUnparseable, true, RepairConfigBackupThenRegenerate},
		{ConfigStateOK, false, RepairConfigKeep},
		{ConfigStateOK, true, RepairConfigBackupThenRegenerate},
	}
	for _, c := range cases {
		if got := PlanConfigRepair(c.state, c.force); got != c.want {
			t.Errorf("PlanConfigRepair(%q, %v) = %q, want %q", c.state, c.force, got, c.want)
		}
	}
}

func repairStepNames(steps []RepairStep) []string {
	out := make([]string, len(steps))
	for i, s := range steps {
		out[i] = s.Name
	}
	return out
}

func TestRepairPlanShapes(t *testing.T) {
	// Healthy config, no perm issues: keep-config then verify-engine.
	got := repairStepNames(RepairPlan(RepairPlanInput{ConfigAction: RepairConfigKeep}))
	if len(got) != 2 || got[0] != "keep-config" || got[1] != "verify-engine" {
		t.Fatalf("healthy plan = %v", got)
	}

	// Absent config + perm drift, no --fix-perms: regenerate, report-permissions, verify.
	got = repairStepNames(RepairPlan(RepairPlanInput{
		ConfigAction: RepairConfigRegenerate,
		PermIssues:   []string{"nextsql.conf is 0644, want 0640"},
	}))
	want := []string{"regenerate-config", "report-permissions", "verify-engine"}
	if len(got) != len(want) {
		t.Fatalf("plan = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("plan = %v, want %v", got, want)
		}
	}

	// --fix-perms turns the report step into a mutating fix step.
	steps := RepairPlan(RepairPlanInput{
		ConfigAction: RepairConfigBackupThenRegenerate,
		FixPerms:     true,
		PermIssues:   []string{"root.key is 0644, want 0600"},
	})
	got = repairStepNames(steps)
	want = []string{"backup-and-regenerate-config", "fix-permissions", "verify-engine"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("plan = %v, want %v", got, want)
		}
	}
	for _, s := range steps {
		switch s.Name {
		case "backup-and-regenerate-config", "fix-permissions", "verify-engine":
			if !s.Mutates {
				t.Errorf("step %q should be mutating", s.Name)
			}
		}
	}
}
