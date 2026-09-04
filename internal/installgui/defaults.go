package installgui

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
	DataDir  string `json:"dataDir"`
	KeyFile  string `json:"keyFile"`
	Elevated bool   `json:"elevated"` // running as root/Administrator
	OS       string `json:"os"`
}

// detectDefaults mirrors packaging/linux/tarball/install.sh's own
// --user/--system split so a first-run wizard suggests the same paths a
// tarball/.deb install would use.
func detectDefaults() Defaults {
	d := Defaults{OS: runtime.GOOS}

	switch runtime.GOOS {
	case "windows":
		// No root/Administrator distinction here in M1 (see
		// docs/design-installer-gui.md non-goals) — always suggest a
		// per-user location, which needs no elevation.
		base := os.Getenv("LOCALAPPDATA")
		if base == "" {
			base, _ = os.UserHomeDir()
		}
		d.DataDir = filepath.Join(base, "NextSQL", "data")
		d.KeyFile = filepath.Join(base, "NextSQL", "root.key")
	default:
		if os.Geteuid() == 0 {
			d.Elevated = true
			d.DataDir = "/var/lib/nextsql"
			d.KeyFile = "/etc/nextsql/root.key"
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
	}
	return d
}
