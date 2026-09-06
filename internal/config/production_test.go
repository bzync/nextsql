package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseDeploymentProfile(t *testing.T) {
	cases := map[string]string{
		"":            ProfileDeveloper,
		"developer":   ProfileDeveloper,
		"PRODUCTION":  ProfileProduction,
		" production": ProfileProduction,
	}
	for in, want := range cases {
		got, err := ParseDeploymentProfile(in)
		if err != nil {
			t.Errorf("ParseDeploymentProfile(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseDeploymentProfile(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseDeploymentProfile("staging"); err == nil {
		t.Error("ParseDeploymentProfile(staging) should fail")
	}
}

func TestKeyOnDataVolume(t *testing.T) {
	data := "/var/lib/nextsql"
	if !KeyOnDataVolume(data, "/var/lib/nextsql/root.key") {
		t.Error("key inside data dir should count as on-volume")
	}
	if !KeyOnDataVolume(data, "/var/lib/nextsql/keys/root.key") {
		t.Error("key in a subdirectory of data dir should count as on-volume")
	}
	if KeyOnDataVolume(data, "/etc/nextsql/root.key") {
		t.Error("key off the data volume must not count as on-volume")
	}
	if KeyOnDataVolume(data, "/var/lib/nextsql-keys/root.key") {
		t.Error("sibling directory must not count as on-volume")
	}
	if KeyOnDataVolume("", "/etc/nextsql/root.key") || KeyOnDataVolume(data, "") {
		t.Error("empty path is not on-volume")
	}
}

func TestKeyOnDataVolumeRelative(t *testing.T) {
	root := t.TempDir()
	data := filepath.Join(root, "data")
	if err := os.MkdirAll(data, 0o755); err != nil {
		t.Fatal(err)
	}
	if !KeyOnDataVolume(data, filepath.Join(data, "root.key")) {
		t.Error("absolute key inside data dir")
	}
	off := filepath.Join(root, "keys", "root.key")
	if KeyOnDataVolume(data, off) {
		t.Error("absolute key beside data dir")
	}
}

func TestApplyProductionDefaultsFillsZerosOnly(t *testing.T) {
	c := Default()
	c.DataDir = "/var/lib/nextsql"
	c.KeyFile = "/etc/nextsql/root.key"
	c.StatementTimeoutMS = 9_000
	c.ApplyProductionDefaults()
	if c.DeploymentProfile != ProfileProduction {
		t.Fatalf("profile = %q", c.DeploymentProfile)
	}
	if c.DiskWatermarkCheckMS != ProductionDiskWatermarkCheckMS {
		t.Errorf("watermark = %d", c.DiskWatermarkCheckMS)
	}
	if c.ReplicaLagCheckMS != ProductionReplicaLagCheckMS {
		t.Errorf("replica lag = %d", c.ReplicaLagCheckMS)
	}
	if c.StatementTimeoutMS != 9_000 {
		t.Errorf("explicit statement timeout overwritten: %d", c.StatementTimeoutMS)
	}
	if c.IdleTimeoutMS != ProductionIdleTimeoutMS {
		t.Errorf("idle = %d", c.IdleTimeoutMS)
	}
	if c.DrainTimeoutMS != DefaultDrainTimeoutMS {
		t.Errorf("drain = %d", c.DrainTimeoutMS)
	}
	if c.MaxConnections != ProductionMaxConnections {
		t.Errorf("max connections = %d", c.MaxConnections)
	}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := c.CheckProduction(); err != nil {
		t.Fatal(err)
	}
}

func TestCheckProductionRejectsKeyOnDataVolume(t *testing.T) {
	c := Default()
	c.DataDir = "/var/lib/nextsql"
	c.KeyFile = "/var/lib/nextsql/root.key"
	c.ApplyProductionDefaults()
	err := c.CheckProduction()
	if err == nil || !strings.Contains(err.Error(), "off the data volume") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckProductionRejectsMissingWatermark(t *testing.T) {
	c := Default()
	c.DataDir = "/var/lib/nextsql"
	c.KeyFile = "/etc/nextsql/root.key"
	c.DeploymentProfile = ProfileProduction
	c.DrainTimeoutMS = DefaultDrainTimeoutMS
	c.StatementTimeoutMS = ProductionStatementTimeoutMS
	c.IdleTimeoutMS = ProductionIdleTimeoutMS
	err := c.CheckProduction()
	if err == nil || !strings.Contains(err.Error(), "disk_watermark_check_ms") {
		t.Fatalf("err = %v", err)
	}
}

func TestCheckProductionNoopForDeveloper(t *testing.T) {
	c := Default()
	c.DataDir = "/var/lib/nextsql"
	c.KeyFile = "/var/lib/nextsql/root.key" // would fail production
	c.DeploymentProfile = ProfileDeveloper
	if err := c.CheckProduction(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadDeploymentProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.conf")
	body := "data_dir=/var/lib/nextsql\nkey_file=/etc/nextsql/root.key\ndeployment_profile=production\ndisk_watermark_check_ms=60000\nstatement_timeout_ms=30000\nidle_timeout_ms=60000\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DeploymentProfile != ProfileProduction {
		t.Fatalf("profile = %q", cfg.DeploymentProfile)
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := cfg.CheckProduction(); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRejectsUnknownDeploymentProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.conf")
	if err := os.WriteFile(path, []byte("deployment_profile=staging\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected unknown profile error")
	}
}
