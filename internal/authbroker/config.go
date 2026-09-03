// Package authbroker implements the NextSQL authentication broker: a small
// standalone HTTPS service that runs an OpenID Connect token exchange and mints
// an ordinary `NSSC1.` short-lived credential (`internal/auth`). It is the only
// component that speaks OIDC — `nextsqld` keeps verifying `NSSC1.` credentials
// offline, unchanged.
//
// The broker validates a client-supplied IdP token against a cached JWKS
// (`internal/oidc`), turns the verified claims into a native principal and a
// no-escalation role set through an `NSIP` identity policy
// (`internal/auth.IdentityPolicy`), and signs a credential with a private
// `NSTK` key whose public half sits in every server's `token_verify_keyset`.
//
// This file is the operator configuration surface. It is plain text like
// `nextsqld.conf`, not a versioned persistent format.
package authbroker

import (
	"bufio"
	"os"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidc"
)

// Defaults for the minted credential and skew.
const (
	DefaultCredentialTTL = time.Hour
	MaxCredentialTTL     = 12 * time.Hour
)

// IdPProfile is one named external identity provider.
type IdPProfile struct {
	Name                string
	Issuer              string
	ClientID            string
	AccessTokenAudience string // empty disables JWT client-credentials exchange
	JWKSURI             string // optional; discovered from Issuer when empty
	AllowedAlgs         []string
	JWKSSoftTTL         time.Duration
	JWKSHardTTL         time.Duration
	GroupClaim          string // informational; the NSIP policy owns the real group claim
	Skew                time.Duration
}

// Config is the parsed broker configuration.
type Config struct {
	Listen             string
	TLSCert            string
	TLSKey             string
	IdentityPolicy     string
	IssuingKeyset      string
	DeploymentAudience string
	CredentialTTL      time.Duration
	LogLevel           string
	Profiles           []IdPProfile
}

// Default returns a config with only the non-file defaults populated.
func Default() Config {
	return Config{
		Listen:        "127.0.0.1:8645",
		CredentialTTL: DefaultCredentialTTL,
		LogLevel:      "info",
	}
}

// Profile returns the named profile.
func (c Config) Profile(name string) (IdPProfile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return IdPProfile{}, false
}

// LoadConfig reads a broker configuration file. The format is line-based
// `key = value`, with `[idp "name"]` starting a per-provider section; keys
// before the first section are global. Unknown keys are rejected.
func LoadConfig(path string) (Config, error) {
	f, err := os.Open(path)
	if err != nil {
		return Config{}, nerr.Wrap(nerr.IO, "authbroker.LoadConfig", "open", err)
	}
	defer f.Close()

	cfg := Default()
	var cur *IdPProfile // nil => global section
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		raw := strings.TrimSpace(sc.Text())
		if raw == "" || strings.HasPrefix(raw, "#") {
			continue
		}
		if name, ok := parseSectionHeader(raw); ok {
			if name == "" {
				return Config{}, nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "empty idp section name")
			}
			if _, dup := cfg.Profile(name); dup {
				return Config{}, nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "duplicate idp section")
			}
			cfg.Profiles = append(cfg.Profiles, IdPProfile{Name: name})
			cur = &cfg.Profiles[len(cfg.Profiles)-1]
			continue
		}
		k, v, ok := strings.Cut(raw, "=")
		if !ok {
			return Config{}, nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "expected key = value")
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if cur == nil {
			if err := applyGlobal(&cfg, k, v); err != nil {
				return Config{}, err
			}
		} else if err := applyProfile(cur, k, v); err != nil {
			return Config{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, nerr.Wrap(nerr.IO, "authbroker.LoadConfig", "read", err)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func parseSectionHeader(s string) (string, bool) {
	if !strings.HasPrefix(s, "[") || !strings.HasSuffix(s, "]") {
		return "", false
	}
	inner := strings.TrimSpace(s[1 : len(s)-1])
	inner = strings.TrimPrefix(inner, "idp")
	inner = strings.TrimSpace(inner)
	inner = strings.Trim(inner, `"`)
	return strings.TrimSpace(inner), true
}

func applyGlobal(cfg *Config, k, v string) error {
	switch k {
	case "listen":
		cfg.Listen = v
	case "tls_cert":
		cfg.TLSCert = v
	case "tls_key":
		cfg.TLSKey = v
	case "identity_policy":
		cfg.IdentityPolicy = v
	case "issuing_keyset":
		cfg.IssuingKeyset = v
	case "deployment_audience":
		cfg.DeploymentAudience = v
	case "log_level":
		cfg.LogLevel = v
	case "oidc_credential_ttl":
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "oidc_credential_ttl must be a positive duration")
		}
		cfg.CredentialTTL = d
	default:
		return nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "unknown global key")
	}
	return nil
}

func applyProfile(p *IdPProfile, k, v string) error {
	switch k {
	case "issuer":
		p.Issuer = v
	case "client_id":
		p.ClientID = v
	case "access_token_audience":
		p.AccessTokenAudience = v
	case "jwks_uri":
		p.JWKSURI = v
	case "group_claim":
		p.GroupClaim = v
	case "allowed_algs":
		p.AllowedAlgs = nil
		for _, a := range strings.Split(v, ",") {
			if a = strings.TrimSpace(a); a != "" {
				p.AllowedAlgs = append(p.AllowedAlgs, a)
			}
		}
	case "jwks_soft_ttl":
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "jwks_soft_ttl must be a positive duration")
		}
		p.JWKSSoftTTL = d
	case "jwks_hard_ttl":
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "jwks_hard_ttl must be a positive duration")
		}
		p.JWKSHardTTL = d
	case "skew":
		d, err := time.ParseDuration(v)
		if err != nil || d <= 0 {
			return nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "skew must be a positive duration")
		}
		p.Skew = d
	default:
		return nerr.New(nerr.InvalidArgument, "authbroker.LoadConfig", "unknown idp key")
	}
	return nil
}

// Validate checks the whole configuration for internal consistency.
func Validate(cfg Config) error { return cfg.Validate() }

// Validate checks the configuration.
func (c Config) Validate() error {
	bad := func(msg string) error {
		return nerr.New(nerr.InvalidArgument, "authbroker.Config.Validate", msg)
	}
	if strings.TrimSpace(c.Listen) == "" {
		return bad("listen address is required")
	}
	if (c.TLSCert == "") != (c.TLSKey == "") {
		return bad("tls_cert and tls_key must be set together")
	}
	if strings.TrimSpace(c.IdentityPolicy) == "" {
		return bad("identity_policy is required")
	}
	if strings.TrimSpace(c.IssuingKeyset) == "" {
		return bad("issuing_keyset is required")
	}
	if c.CredentialTTL <= 0 || c.CredentialTTL > MaxCredentialTTL {
		return bad("oidc_credential_ttl must be within (0, 12h]")
	}
	switch strings.ToLower(c.LogLevel) {
	case "", "debug", "info", "warn", "error":
	default:
		return bad("log_level must be debug, info, warn, or error")
	}
	if len(c.Profiles) == 0 {
		return bad("at least one [idp \"name\"] section is required")
	}
	for _, p := range c.Profiles {
		if strings.TrimSpace(p.Issuer) == "" || strings.TrimSpace(p.ClientID) == "" {
			return bad("each idp section needs issuer and client_id")
		}
		if len(p.AccessTokenAudience) > 1024 {
			return bad("idp access_token_audience must not exceed 1024 bytes")
		}
		if !strings.HasPrefix(p.Issuer, "https://") {
			return bad("idp issuer must be an https URL")
		}
		if p.JWKSURI != "" && !strings.HasPrefix(p.JWKSURI, "https://") {
			return bad("idp jwks_uri must be an https URL")
		}
		for _, a := range p.AllowedAlgs {
			if !oidc.AlgIsAsymmetric(a) {
				return bad("idp allowed_algs must all be asymmetric signature algorithms")
			}
		}
		if p.JWKSSoftTTL > 0 && p.JWKSHardTTL > 0 && p.JWKSHardTTL < p.JWKSSoftTTL {
			return bad("idp jwks_hard_ttl must be at least jwks_soft_ttl")
		}
	}
	return nil
}
