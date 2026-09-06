// Package setup is the Setup mode of NextSQL Admin: the first-run wizard
// backend (formerly the standalone NextSQL GUI Installer). It exposes only an
// http.Handler plus a token — the parent internal/admin package owns the one
// process listener, TLS termination, and the shared embedded frontend shell;
// this package never binds a network socket itself.
//
// It never touches the storage engine directly and never links against
// internal/crypto, internal/storage, internal/setup, or internal/sysinfo —
// enforced by imports_test.go. Every database-affecting effect (hardware
// detection, resource sizing, database initialization) happens by shelling
// out to the already fully-tested `nextsql setup` CLI command and treating
// its JSON stdout as the API response; Setup mode holds no key material and
// no credentials of its own beyond the single per-run token it generates for
// itself. The one exception is the optional "start at boot" step
// (service.go), which shells out to `systemctl` instead — a read-only probe
// plus one best-effort `enable --now` call against a unit this package never
// authors itself, gated behind its own matching checks; see service.go's doc
// comments. See docs/design-admin.md.
package setup

import (
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// Defaults for Setup mode's behavior. The listener itself (address/TLS) is
// owned by the parent internal/admin package, not here.
const (
	DefaultRunTimeout = 5 * time.Minute

	tokenCookie = "nsi_token"
	tokenHeader = "X-Installer-Token"
	tokenParam  = "token"
)

// Config is the resolved Setup-mode configuration.
type Config struct {
	// NextSQLBin is the path to the `nextsql` binary this mode drives.
	// Resolved by the command layer (next to its own executable, then PATH)
	// before New is called; New only requires it to be non-empty.
	NextSQLBin string

	// RunTimeout bounds each `nextsql setup` subprocess invocation.
	RunTimeout time.Duration

	// TLS reports whether the parent admin process is terminating TLS on the
	// shared listener — used only to set the Secure flag on the token cookie.
	TLS bool

	LogLevel string
}

func (c Config) withDefaults() Config {
	if c.RunTimeout <= 0 {
		c.RunTimeout = DefaultRunTimeout
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	return c
}

func (c Config) validate() error {
	if c.NextSQLBin == "" {
		return nerr.New(nerr.InvalidArgument, "setup.Config", "NextSQLBin is required")
	}
	return nil
}
