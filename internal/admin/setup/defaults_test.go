package setup

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestDetectDefaultsNonEmpty(t *testing.T) {
	d := detectDefaults()
	if d.DataDir == "" {
		t.Error("expected a non-empty default data dir")
	}
	if d.KeyFile == "" {
		t.Error("expected a non-empty default key file")
	}
	if d.ConfigOut == "" {
		t.Error("expected a non-empty default config-out path")
	}
	if d.OS == "" {
		t.Error("expected a non-empty OS")
	}
}

// TestDetectDefaultsConfigOutSeparateFromDataDir guards the whole reason
// ConfigOut exists: packaging/linux/tarball/install.sh (and the .deb/.run
// installers) keep the config file out of the data directory (/etc/nextsql
// vs /var/lib/nextsql, or $XDG_CONFIG_HOME/nextsql vs $XDG_DATA_HOME/nextsql
// per-user) so that a systemd unit's --config path never lives inside the
// data volume. If ConfigOut ever regressed to DataDir/nextsql.conf (nextsql
// setup's own no-flag default), viewServiceOption's expectedConfig check in
// web/app.js — and server.go's maybeEnableService — would start rejecting
// every packaged install's "start at boot" option again, silently.
func TestDetectDefaultsConfigOutSeparateFromDataDir(t *testing.T) {
	d := detectDefaults()
	inDataDir := filepath.Join(d.DataDir, "nextsql.conf")
	if d.ConfigOut == inDataDir {
		t.Fatalf("ConfigOut (%s) must not equal DataDir/nextsql.conf (%s) — it must mirror the packaged installers' config/data split, not nextsql setup's own no-flag default", d.ConfigOut, inDataDir)
	}
	if !strings.HasSuffix(d.ConfigOut, "nextsql.conf") {
		t.Fatalf("ConfigOut = %q, want it to end in nextsql.conf", d.ConfigOut)
	}
}
