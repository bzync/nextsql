// Package admin is NextSQL Admin: the single unified binary for installing,
// operating, and (eventually) developing against NextSQL — formerly three
// separate products (Installer, Manager, Studio). It owns the one process
// HTTP listener, TLS termination, mode selection, and the shared embedded
// frontend shell; the actual per-mode behavior lives in the setup, ops, and
// studio subpackages, each of which exposes only an http.Handler (setup and
// ops never bind a network socket themselves). See docs/design-admin.md.
package admin

import (
	"time"

	"github.com/bzync/nextsql/internal/admin/ops"
	"github.com/bzync/nextsql/internal/admin/setup"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// Mode is which lifecycle phase this process is serving.
type Mode string

const (
	// ModeSetup is the first-run wizard (formerly the standalone GUI
	// installer): single-operator token auth, drives `nextsql setup` as a
	// subprocess. Chosen automatically when no initialized installation is
	// found at DataDirHint.
	ModeSetup Mode = "setup"
	// ModeOperate is operational administration (formerly the standalone
	// Manager): operator NSQL-credential sessions against a running
	// nextsqld. Chosen automatically once an installation exists.
	ModeOperate Mode = "operate"
)

// Defaults that depend on the resolved Mode.
const (
	DefaultSetupListen   = "127.0.0.1:0" // ephemeral port; Addr() reports what bound
	DefaultOperateListen = "127.0.0.1:7220"

	DefaultServerAddr = ops.DefaultServerAddr
)

// Config is the resolved NextSQL Admin configuration. The command layer
// builds it from flags; New resolves the mode (auto-detecting when Mode is
// empty), applies mode-dependent defaults, and validates.
type Config struct {
	// Mode overrides auto-detection. Empty means detect via
	// `nextsql lifecycle detect --json` against DataDirHint.
	Mode Mode

	// Listen is Admin's one HTTP listener, shared by whichever mode is
	// active. A non-loopback address requires ListenTLSCert/ListenTLSKey.
	// Empty means the mode-dependent default (DefaultSetupListen or
	// DefaultOperateListen).
	Listen        string
	ListenTLSCert string
	ListenTLSKey  string

	// NextSQLBin is the path to the `nextsql` binary Admin drives for mode
	// detection (always) and for Setup mode's subprocess calls. Resolved by
	// the command layer (next to its own executable, then PATH) before New
	// is called.
	NextSQLBin string

	// DataDirHint is the data directory probed for mode auto-detection. Empty
	// means setup.DefaultDataDir() — the same OS-appropriate default the
	// Setup wizard itself suggests.
	DataDirHint string

	// RunTimeout bounds each `nextsql setup` subprocess invocation in Setup
	// mode (and the one `nextsql lifecycle detect` call mode detection
	// makes).
	RunTimeout time.Duration

	// Operations-mode fields (see ops.Config) — ignored in Setup mode.
	ServerAddr      string
	ServerTLSCA     string
	ServerTLSName   string
	ClientCert      string
	ClientKey       string
	InsecureServer  bool
	MaxSessions     int
	IdleTimeout     time.Duration
	SessionLifetime time.Duration

	LogLevel string
}

func (c Config) withDefaults() Config {
	if c.RunTimeout <= 0 {
		c.RunTimeout = setup.DefaultRunTimeout
	}
	if c.DataDirHint == "" {
		c.DataDirHint = setup.DefaultDataDir()
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	return c
}

// listenDefault picks the mode-dependent listener default.
func (c Config) listenDefault(mode Mode) string {
	if c.Listen != "" {
		return c.Listen
	}
	if mode == ModeSetup {
		return DefaultSetupListen
	}
	return DefaultOperateListen
}

func (c Config) validate(mode Mode) error {
	if mode != ModeSetup && mode != ModeOperate {
		return nerr.New(nerr.InvalidArgument, "admin.Config", "mode must be setup or operate")
	}
	if security.RequireTLS(c.Listen) && (c.ListenTLSCert == "" || c.ListenTLSKey == "") {
		return nerr.New(nerr.InvalidArgument, "admin.Config",
			"a non-loopback --listen address requires --tls-cert and --tls-key")
	}
	if mode == ModeSetup && c.NextSQLBin == "" {
		return nerr.New(nerr.InvalidArgument, "admin.Config", "--nextsql-bin is required for Setup mode")
	}
	return nil
}

func (c Config) setupConfig() setup.Config {
	return setup.Config{
		NextSQLBin: c.NextSQLBin,
		RunTimeout: c.RunTimeout,
		TLS:        c.ListenTLSCert != "",
		LogLevel:   c.LogLevel,
	}
}

func (c Config) opsConfig() ops.Config {
	return ops.Config{
		ServerAddr:      c.ServerAddr,
		ServerTLSCA:     c.ServerTLSCA,
		ServerTLSName:   c.ServerTLSName,
		ClientCert:      c.ClientCert,
		ClientKey:       c.ClientKey,
		InsecureServer:  c.InsecureServer,
		MaxSessions:     c.MaxSessions,
		IdleTimeout:     c.IdleTimeout,
		SessionLifetime: c.SessionLifetime,
		TLS:             c.ListenTLSCert != "",
		LogLevel:        c.LogLevel,
	}
}
