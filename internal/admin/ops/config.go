// Package ops is the Operations mode of NextSQL Admin: the operational-
// administration backend (formerly the standalone NextSQL Manager). It
// exposes only an http.Handler and a session store — the parent
// internal/admin package owns the one process listener, TLS termination, and
// the shared embedded frontend shell; this package never binds a network
// socket itself.
//
// Every data operation Operations mode performs goes through the official Go
// driver (drivers/go) against a running nextsqld, as the logged-in operator's
// own NSQL user, so server-side RBAC / tenant isolation / audit / redaction
// apply unchanged. This mode holds no credentials of its own, has no
// data-directory or key access, and never imports the storage / WAL / catalog
// / crypto engine packages. See docs/design-admin.md.
package ops

import (
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/admin/credential"
	"github.com/bzync/nextsql/internal/admin/profile"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// Defaults for Operations mode's session policy. The listener itself
// (address/TLS) is owned by the parent internal/admin package, not here.
const (
	DefaultServerAddr      = "127.0.0.1:7210"
	DefaultMaxSessions     = 16
	DefaultIdleTimeout     = 15 * time.Minute
	DefaultSessionLifetime = 12 * time.Hour

	sessionCookie = "nsm_session"
	csrfHeader    = "X-NSM-CSRF"
)

// Config is the resolved Operations-mode configuration. The command layer
// builds it from flags (and later a config file); New validates it.
type Config struct {
	// ServerAddr is the nextsqld address of the "default" connection
	// profile — the one a sign-in uses unless it names another profile.
	ServerAddr     string
	ServerTLSCA    string
	ServerTLSName  string
	ClientCert     string
	ClientKey      string
	InsecureServer bool
	// ServerName and ServerEnvironment label the default profile.
	ServerName        string
	ServerEnvironment string
	// Profiles are the further, operator-declared nextsqld servers (from the
	// --profiles file) a session may sign in or switch to.
	Profiles []profile.Profile

	MaxSessions     int
	IdleTimeout     time.Duration
	SessionLifetime time.Duration

	// TLS reports whether the parent admin process is terminating TLS on the
	// shared listener — used only to set the Secure flag on the session
	// cookie.
	TLS bool

	LogLevel string

	// CredentialStore is the OS-backed store used only when an operator
	// switching servers explicitly asks to save that server's password. It
	// has no file fallback.
	CredentialStore credential.Store
}

// withDefaults returns c with zero-valued fields filled in.
func (c Config) withDefaults() Config {
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
	if c.CredentialStore == nil {
		c.CredentialStore = credential.OSStore{}
	}
	return c
}

// validate checks the resolved config. It does not touch the network.
func (c Config) validate() error {
	if (c.ClientCert == "") != (c.ClientKey == "") {
		return nerr.New(nerr.InvalidArgument, "ops.Config",
			"--tls-client-cert and --tls-client-key must be set together")
	}
	if c.ClientCert != "" && c.ServerTLSCA == "" {
		return nerr.New(nerr.InvalidArgument, "ops.Config",
			"--tls-client-cert requires --tls-ca")
	}
	if c.ServerTLSCA == "" && !c.InsecureServer {
		return nerr.New(nerr.InvalidArgument, "ops.Config",
			"connecting to nextsqld requires --tls-ca or --insecure (loopback only)")
	}
	if c.InsecureServer && security.RequireTLS(c.ServerAddr) {
		return nerr.New(nerr.InvalidArgument, "ops.Config",
			"--insecure is only allowed for a loopback --server-addr")
	}
	if err := c.defaultProfile().ValidateTarget(); err != nil {
		return err
	}
	if err := c.validateProfiles(); err != nil {
		return err
	}
	if c.SessionLifetime < c.IdleTimeout {
		return nerr.New(nerr.InvalidArgument, "ops.Config",
			"--session-lifetime must not be shorter than --idle-timeout")
	}
	return nil
}

// DefaultServerName labels the --server-addr profile when no --server-name
// is given.
const DefaultServerName = "Default server"

// defaultProfile is the implicit "default" connection profile built from the
// --server-addr / --tls-* flags. A CA always wins over --insecure, exactly as
// before profiles existed, so no existing flag combination changes meaning.
func (c Config) defaultProfile() profile.Profile {
	name := c.ServerName
	if name == "" {
		name = DefaultServerName
	}
	p := profile.Profile{
		ID:            profile.DefaultID,
		Name:          name,
		Environment:   c.ServerEnvironment,
		Address:       c.ServerAddr,
		TLSCA:         c.ServerTLSCA,
		TLSServerName: c.ServerTLSName,
		TLSClientCert: c.ClientCert,
		TLSClientKey:  c.ClientKey,
		Insecure:      c.InsecureServer && c.ServerTLSCA == "",
	}
	if p.Insecure {
		// A server name means nothing without TLS; the flags used to
		// tolerate the pair, so keep tolerating it rather than refusing.
		p.TLSServerName = ""
	}
	return p
}

// allProfiles returns the default profile followed by the file-declared ones.
func (c Config) allProfiles() []profile.Profile {
	out := make([]profile.Profile, 0, 1+len(c.Profiles))
	out = append(out, c.defaultProfile())
	return append(out, c.Profiles...)
}

// validateProfiles checks the file-declared profiles again (Config can be
// built programmatically, not only through profile.Load) and the default
// profile's labels.
func (c Config) validateProfiles() error {
	if len(c.ServerName) > 64 {
		return nerr.New(nerr.InvalidArgument, "ops.Config", "--server-name is longer than 64 bytes")
	}
	if err := profile.ValidateEnvironment(c.ServerEnvironment); err != nil {
		return nerr.New(nerr.InvalidArgument, "ops.Config", "--server-environment: "+err.Error())
	}
	if len(c.Profiles) > profile.MaxProfiles {
		return nerr.New(nerr.InvalidArgument, "ops.Config", "too many connection profiles")
	}
	seen := map[string]bool{profile.DefaultID: true}
	for _, p := range c.Profiles {
		if err := p.Validate(); err != nil {
			return err
		}
		if seen[p.ID] {
			return nerr.New(nerr.InvalidArgument, "ops.Config", "duplicate connection profile id "+p.ID)
		}
		seen[p.ID] = true
	}
	return nil
}

// driverConfigFor builds the driver Config for one profile: everything
// except the operator's user/password, which sign-in supplies. TLS material
// is re-read from disk on every connection so a rotated CA or client
// certificate takes effect without restarting Admin.
func driverConfigFor(p profile.Profile) (nextsql.Config, error) {
	cfg := nextsql.Config{
		Address:       p.Address,
		Database:      p.Database,
		InsecureNoTLS: p.Insecure,
	}
	tlsCfg, err := p.ClientTLS()
	if err != nil {
		return nextsql.Config{}, err
	}
	if tlsCfg != nil {
		cfg.TLS = tlsCfg
		cfg.InsecureNoTLS = false
	}
	return cfg, nil
}
