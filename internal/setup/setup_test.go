package setup

import (
	"errors"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/sysinfo"
)

func TestParsePreset(t *testing.T) {
	cases := map[string]Preset{
		"":                  DefaultPreset,
		"balanced":          PresetBalanced,
		"CONSERVATIVE":      PresetConservative,
		" high-performance": PresetHighPerformance,
		"custom":            PresetCustom,
	}
	for in, want := range cases {
		got, err := ParsePreset(in)
		if err != nil {
			t.Errorf("ParsePreset(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParsePreset(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParsePreset("aggressive"); err == nil {
		t.Error("ParsePreset(aggressive) should fail")
	}
}

func TestRecommendScalesWithPreset(t *testing.T) {
	info := sysinfo.Info{RAMBytes: 16 << 30} // 16 GiB

	cons := Recommend(info, PresetConservative, 0)
	bal := Recommend(info, PresetBalanced, 0)
	perf := Recommend(info, PresetHighPerformance, 0)

	if !(cons.BufferPages < bal.BufferPages && bal.BufferPages < perf.BufferPages) {
		t.Fatalf("preset ordering broken: conservative=%d balanced=%d high-performance=%d",
			cons.BufferPages, bal.BufferPages, perf.BufferPages)
	}
	// balanced ≈ 25% of 16 GiB / 16 KiB pages
	wantBal := int((16 << 30) / 4 / format.LogicalPageSize)
	if bal.BufferPages != wantBal {
		t.Errorf("balanced BufferPages = %d, want %d", bal.BufferPages, wantBal)
	}
	if bal.BufferBytes != uint64(bal.BufferPages)*format.LogicalPageSize {
		t.Errorf("BufferBytes inconsistent with BufferPages")
	}
}

func TestRecommendUndetectedRAMFallsBackToDefault(t *testing.T) {
	r := Recommend(sysinfo.Info{RAMBytes: 0}, PresetHighPerformance, 0)
	if r.BufferPages != config.DefaultBufferPages {
		t.Errorf("BufferPages = %d, want default %d", r.BufferPages, config.DefaultBufferPages)
	}
	if !strings.Contains(r.Rationale, "RAM") {
		t.Errorf("rationale should explain the RAM fallback, got %q", r.Rationale)
	}
}

func TestRecommendExplicitOverrideWins(t *testing.T) {
	r := Recommend(sysinfo.Info{RAMBytes: 64 << 30}, PresetHighPerformance, 4096)
	if r.BufferPages != 4096 {
		t.Errorf("BufferPages = %d, want the explicit 4096", r.BufferPages)
	}
}

func TestRecommendCustomWithoutOverrideUsesDefault(t *testing.T) {
	r := Recommend(sysinfo.Info{RAMBytes: 64 << 30}, PresetCustom, 0)
	if r.BufferPages != config.DefaultBufferPages {
		t.Errorf("BufferPages = %d, want default %d", r.BufferPages, config.DefaultBufferPages)
	}
}

func TestRecommendNeverBelowDefaultOnTinyHost(t *testing.T) {
	r := Recommend(sysinfo.Info{RAMBytes: 128 << 20}, PresetConservative, 0) // 128 MiB
	if r.BufferPages < config.DefaultBufferPages {
		t.Errorf("BufferPages = %d, must not drop below default %d", r.BufferPages, config.DefaultBufferPages)
	}
}

func baseParams() Params {
	return Params{
		Base:       config.Default(),
		Info:       sysinfo.Info{RAMBytes: 8 << 30, DiskTotalBytes: 500 << 30, DiskFreeBytes: 400 << 30, Filesystem: "ext4"},
		Preset:     PresetBalanced,
		DataDir:    "/var/lib/nextsql",
		KeyFile:    "/etc/nextsql/root.key",
		ListenAddr: config.DefaultListenAddr,
		LogLevel:   "info",
		ConfigPath: "/var/lib/nextsql/nextsql.conf",
		AdminUser:  "app",
		RunInit:    true,
	}
}

func TestBuildPlanLoopbackDefault(t *testing.T) {
	plan, err := BuildPlan(baseParams())
	if err != nil {
		t.Fatal(err)
	}
	if plan.TLS {
		t.Error("plan.TLS should be false for a plaintext loopback listener")
	}
	if plan.InstanceKeyFile != "/etc/nextsql/root.key.instance" {
		t.Errorf("InstanceKeyFile = %q, want the derived .instance path", plan.InstanceKeyFile)
	}
	if plan.Config.BufferPages != plan.Recommendation.BufferPages {
		t.Error("config buffer pages should match the recommendation")
	}
	if len(plan.Warnings) != 0 {
		t.Errorf("unexpected warnings for a healthy loopback plan: %v", plan.Warnings)
	}
}

func TestBuildPlanRemoteWithoutTLSRejected(t *testing.T) {
	p := baseParams()
	p.ListenAddr = "0.0.0.0:7210"
	if _, err := BuildPlan(p); !errors.Is(err, ErrInsecureRemote) {
		t.Fatalf("err = %v, want ErrInsecureRemote", err)
	}
}

func TestBuildPlanRemoteWithTLSAccepted(t *testing.T) {
	p := baseParams()
	p.ListenAddr = "10.0.0.5:7210"
	p.TLSCert = "/etc/nextsql/tls.crt"
	p.TLSKey = "/etc/nextsql/tls.key"
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.TLS {
		t.Error("plan.TLS should be true")
	}
	if !hasWarningContaining(plan.Warnings, "firewall") {
		t.Errorf("expected a firewall advisory for a remote listener, got %v", plan.Warnings)
	}
}

func TestBuildPlanWarnsOnEphemeralFilesystem(t *testing.T) {
	p := baseParams()
	p.Info.Filesystem = "tmpfs"
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarningContaining(plan.Warnings, "tmpfs") {
		t.Errorf("expected a tmpfs advisory, got %v", plan.Warnings)
	}
}

func TestBuildPlanWarnsOnLowDiskAndMissingAdmin(t *testing.T) {
	p := baseParams()
	p.Info.DiskFreeBytes = 256 << 20 // 256 MiB
	p.AdminUser = ""
	plan, err := BuildPlan(p)
	if err != nil {
		t.Fatal(err)
	}
	if !hasWarningContaining(plan.Warnings, "free") {
		t.Errorf("expected a low-disk advisory, got %v", plan.Warnings)
	}
	if !hasWarningContaining(plan.Warnings, "administrator") {
		t.Errorf("expected a missing-admin advisory, got %v", plan.Warnings)
	}
}

func TestBuildPlanRejectsInvalidLogLevel(t *testing.T) {
	p := baseParams()
	p.LogLevel = "chatty"
	if _, err := BuildPlan(p); err == nil {
		t.Fatal("expected config validation to reject an invalid log level")
	}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
