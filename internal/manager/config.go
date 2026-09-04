// Package manager is the NextSQL Manager backend: a loopback HTTP service
// that serves an embedded operational-administration web UI plus a JSON API.
//
// Every data operation the Manager performs goes through the official Go
// driver (drivers/go) against a running nextsqld, as the logged-in operator's
// own NSQL user, so server-side RBAC / tenant isolation / audit / redaction
// apply unchanged. The Manager holds no credentials of its own, has no
// data-directory or key access, and never imports the storage / WAL / catalog
// / crypto engine packages. See docs/design-manager.md.
package manager

import (
	"crypto/tls"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// Defaults for the Manager listener and session policy.
const (
	DefaultListen          = "127.0.0.1:7220"
	DefaultServerAddr      = "127.0.0.1:7210"
	DefaultMaxSessions     = 16
	DefaultIdleTimeout     = 15 * time.Minute
	DefaultSessionLifetime = 12 * time.Hour

	sessionCookie = "nsm_session"
	csrfHeader    = "X-NSM-CSRF"
)

// Config is the resolved Manager configuration. The command layer builds it
// from flags (and later a config file); New validates it.
type Config struct {
	// Listen is the Manager's own HTTP listener. A non-loopback address
	// requires ListenTLSCert/ListenTLSKey.
	Listen        string
	ListenTLSCert string
	ListenTLSKey  string

	// ServerAddr is the nextsqld address the Manager connects to on behalf of
	// each logged-in operator.
	ServerAddr     string
	ServerTLSCA    string
	ServerTLSName  string
	ClientCert     string
	ClientKey      string
	InsecureServer bool

	MaxSessions     int
	IdleTimeout     time.Duration
	SessionLifetime time.Duration

	LogLevel string
}

// withDefaults returns c with zero-valued fields filled in.
func (c Config) withDefaults() Config {
	if c.Listen == "" {
		c.Listen = DefaultListen
	}
	if c.ServerAddr == "" {
		c.ServerAddr = DefaultServerAddr
	}
	if c.MaxSessions <= 0 {
		c.MaxSessions = DefaultMaxSessions
	}
	if c.IdleTimeout <= 0 {
		c.IdleTimeout = DefaultIdleTimeout
	}
	if c.SessionLifetime <= 0 {
		c.SessionLifetime = DefaultSessionLifetime
	}
	if c.LogLevel == "" {
		c.LogLevel = "info"
	}
	return c
}

// validate checks the resolved config. It does not touch the network.
func (c Config) validate() error {
	if security.RequireTLS(c.Listen) && (c.ListenTLSCert == "" || c.ListenTLSKey == "") {
		return nerr.New(nerr.InvalidArgument, "manager.Config",
			"a non-loopback --listen address requires --tls-cert and --tls-key")
	}
	if (c.ClientCert == "") != (c.ClientKey == "") {
		return nerr.New(nerr.InvalidArgument, "manager.Config",
			"--tls-client-cert and --tls-client-key must be set together")
	}
	if c.ClientCert != "" && c.ServerTLSCA == "" {
		return nerr.New(nerr.InvalidArgument, "manager.Config",
			"--tls-client-cert requires --tls-ca")
	}
	if c.ServerTLSCA == "" && !c.InsecureServer {
		return nerr.New(nerr.InvalidArgument, "manager.Config",
			"connecting to nextsqld requires --tls-ca or --insecure (loopback only)")
	}
	if c.InsecureServer && security.RequireTLS(c.ServerAddr) {
		return nerr.New(nerr.InvalidArgument, "manager.Config",
			"--insecure is only allowed for a loopback --server-addr")
	}
	if c.SessionLifetime < c.IdleTimeout {
		return nerr.New(nerr.InvalidArgument, "manager.Config",
			"--session-lifetime must not be shorter than --idle-timeout")
	}
	return nil
}

// driverConfig builds the per-login nextsqld driver Config: everything except
// the operator's user/password/database/realm, which login supplies.
func (c Config) driverConfig() (nextsql.Config, error) {
	cfg := nextsql.Config{
		Address:       c.ServerAddr,
		InsecureNoTLS: c.InsecureServer,
	}
	if c.ServerTLSCA != "" {
		name := c.ServerTLSName
		if name == "" {
			name = hostOf(c.ServerAddr)
		}
		var (
			tlsCfg *tls.Config
			err    error
		)
		if c.ClientCert != "" {
			tlsCfg, err = security.ClientMTLS(name, c.ServerTLSCA, c.ClientCert, c.ClientKey)
		} else {
			tlsCfg, err = security.ClientTLS(name, c.ServerTLSCA)
		}
		if err != nil {
			return nextsql.Config{}, err
		}
		cfg.TLS = tlsCfg
		cfg.InsecureNoTLS = false
	}
	return cfg, nil
}
