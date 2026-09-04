// Package installgui is the NextSQL GUI Installer backend: a loopback HTTP
// service that serves an embedded first-run wizard UI plus a small JSON API.
//
// It never touches the storage engine directly and never links against
// internal/crypto, internal/storage, internal/setup, or internal/sysinfo —
// enforced by imports_test.go. Every effect (hardware detection, resource
// sizing, database initialization) happens by shelling out to the already
// fully-tested `nextsql setup` CLI command and treating its JSON stdout as
// the API response; the installer holds no key material and no credentials
// of its own beyond the single per-run token it generates for itself. See
// docs/design-installer-gui.md.
package installgui

import (
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// Defaults for the installer's own loopback listener.
const (
	DefaultListen = "127.0.0.1:0" // ephemeral port; Addr() reports what bound
	DefaultRunTimeout = 5 * time.Minute

	tokenCookie = "nsi_token"
	tokenHeader = "X-Installer-Token"
	tokenParam  = "token"
)

// Config is the resolved installer configuration.
type Config struct {
	// Listen is the installer's own HTTP listener. A non-loopback address
	// requires ListenTLSCert/ListenTLSKey, same rule as every other NextSQL
	// listener — though there is normally no reason to run this off loopback.
	Listen        string
	ListenTLSCert string
	ListenTLSKey  string

	// NextSQLBin is the path to the `nextsql` binary this installer drives.
	// Resolved by the command layer (next to its own executable, then PATH)
	// before New is called; New only requires it to be non-empty.
	NextSQLBin string

	// RunTimeout bounds each `nextsql setup` subprocess invocation.
	RunTimeout time.Duration

	LogLevel string
}

func (c Config) withDefaults() Config {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
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
		return nerr.New(nerr.InvalidArgument, "installgui.Config", "NextSQLBin is required")
	}
	if security.RequireTLS(c.Listen) && (c.ListenTLSCert == "" || c.ListenTLSKey == "") {
		return nerr.New(nerr.InvalidArgument, "installgui.Config",
			"a non-loopback --listen address requires --tls-cert and --tls-key")
	}
	return nil
}
