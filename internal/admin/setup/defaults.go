package setup

import (
	"os"
	"path/filepath"
	"runtime"
)

// Defaults is what the wizard prefills the data-directory/key-file fields
// with before the operator has typed anything. They are only suggestions —
// every value is re-validated (and, for capacity/permissions, re-detected)
// by the /api/v1/plan dry-run once the operator confirms or edits them.
type Defaults struct {
	DataDir string `json:"dataDir"`
	KeyFile string `json:"keyFile"`
	// ConfigOut is the suggested `nextsql setup --config-out` path. It is
	// deliberately *not* DataDir/nextsql.conf (nextsql setup's own default
	// when --config-out is omitted): the tarball/.deb/.run installers keep
	// config separate from data (/etc/nextsql vs /var/lib/nextsql, or
	// $XDG_CONFIG_HOME/nextsql vs $XDG_DATA_HOME/nextsql per-user), and any
	// systemd unit they install points --config at that separate path, not
	// at the data directory. Suggesting the same split here means a wizard
	// launched by one of those installers (see packaging/linux/tarball/
	// install.sh) writes its config where the already-installed unit
	// expects it, so "start automatically at boot" (service.go) can find a
	// matching config and actually offer to enable it instead of refusing
	// with "points at a different configuration" every time.
	ConfigOut      string `json:"configOut"`
	RecoveryKeyOut string `json:"recoveryKeyOut"`
	Elevated       bool   `json:"elevated"` // running as root/Administrator
	OS             string `json:"os"`
}

// DefaultDataDir is detectDefaults().DataDir, exported so the parent
// internal/admin package can probe it (via `nextsql lifecycle detect`) to
// decide whether this host needs Setup mode or Operations mode when the
// operator did not pass an explicit --mode.
func DefaultDataDir() string { return detectDefaults().DataDir }

// detectDefaults mirrors packaging/linux/tarball/install.sh's own
// --user/--system split so a first-run wizard suggests the same paths a
// tarball/.deb install would use.
func detectDefaults() Defaults {
	d := Defaults{OS: runtime.GOOS}

	switch runtime.GOOS {
	case "windows":
		// No root/Administrator distinction here in M1 (see
		// docs/design-admin.md non-goals) — always suggest a
		// per-user location, which needs no elevation.
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base, _ = os.UserHomeDir()
		}
		d.DataDir = filepath.Join(base, "NextSQL", "data")
		d.KeyFile = filepath.Join(base, "NextSQL", "root.key")
		d.RecoveryKeyOut = filepath.Join(base, "NextSQL", "recovery.key")
		d.ConfigOut = filepath.Join(base, "NextSQL", "nextsql.conf")
	default:
		if os.Geteuid() == 0 {
			d.Elevated = true
			d.DataDir = "/var/lib/nextsql"
			d.KeyFile = "/etc/nextsql/root.key"
			d.RecoveryKeyOut = "/etc/nextsql/recovery.key"
			d.ConfigOut = "/etc/nextsql/nextsql.conf"
			return d
		}
		home, _ := os.UserHomeDir()
		dataHome := os.Getenv("XDG_DATA_HOME")
		if dataHome == "" {
			dataHome = filepath.Join(home, ".local", "share")
		}
		configHome := os.Getenv("XDG_CONFIG_HOME")
		if configHome == "" {
			configHome = filepath.Join(home, ".config")
		}
		d.DataDir = filepath.Join(dataHome, "nextsql")
		d.KeyFile = filepath.Join(configHome, "nextsql", "root.key")
		d.RecoveryKeyOut = filepath.Join(configHome, "nextsql", "recovery.key")
		d.ConfigOut = filepath.Join(configHome, "nextsql", "nextsql.conf")
	}
	return d
}
