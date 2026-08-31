package oidcclient

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// ClientConfig is the parsed `~/.config/nextsql/config.toml` identity-provider
// section set. Only a small, explicit TOML subset is supported: `[idp.name]`
// section headers, `key = "value"`, bare `key = value`, and single-line string
// arrays `key = ["a", "b"]`. Unknown keys are rejected.
type ClientConfig struct {
	Profiles []ConfigProfile
}

// ConfigProfile is one `[idp.name]` block, before secret-file resolution.
type ConfigProfile struct {
	Name             string
	Issuer           string
	ClientID         string
	ClientSecretFile string
	BrokerURL        string
	Scopes           []string
}

// Profile returns the named profile.
func (c ClientConfig) Profile(name string) (ConfigProfile, bool) {
	for _, p := range c.Profiles {
		if p.Name == name {
			return p, true
		}
	}
	return ConfigProfile{}, false
}

// DefaultConfigPath is `~/.config/nextsql/config.toml`.
func DefaultConfigPath() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", nerr.Wrap(nerr.IO, "oidcclient.DefaultConfigPath", "resolve user config dir", err)
	}
	return filepath.Join(base, "nextsql", "config.toml"), nil
}

// LoadClientConfig parses the client config file at path.
func LoadClientConfig(path string) (ClientConfig, error) {
	const op = "oidcclient.LoadClientConfig"
	f, err := os.Open(path)
	if err != nil {
		return ClientConfig{}, nerr.Wrap(nerr.IO, op, "open client config", err)
	}
	defer f.Close()

	var cfg ClientConfig
	var cur *ConfigProfile
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(stripComment(sc.Text()))
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name, ok := parseIdPSection(line)
			if !ok {
				return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "only [idp.<name>] sections are supported")
			}
			if _, dup := cfg.Profile(name); dup {
				return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "duplicate idp section: "+name)
			}
			cfg.Profiles = append(cfg.Profiles, ConfigProfile{Name: name})
			cur = &cfg.Profiles[len(cfg.Profiles)-1]
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "expected key = value")
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		if cur == nil {
			return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "key outside any [idp.<name>] section: "+k)
		}
		if err := applyClientKey(cur, k, v); err != nil {
			return ClientConfig{}, err
		}
	}
	if err := sc.Err(); err != nil {
		return ClientConfig{}, nerr.Wrap(nerr.IO, op, "read client config", err)
	}
	for _, p := range cfg.Profiles {
		if p.Issuer == "" || p.ClientID == "" || p.BrokerURL == "" {
			return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "idp profile "+p.Name+" needs issuer, client_id, and broker_url")
		}
		if !isHTTPS(p.Issuer) {
			return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "idp profile "+p.Name+" issuer must be an https URL")
		}
		if !isHTTPS(p.BrokerURL) && !isLoopbackHTTP(p.BrokerURL) {
			return ClientConfig{}, nerr.New(nerr.InvalidFormat, op, "idp profile "+p.Name+" broker_url must be an https URL")
		}
	}
	return cfg, nil
}

func applyClientKey(p *ConfigProfile, k, v string) error {
	switch k {
	case "issuer":
		p.Issuer = unquote(v)
	case "client_id":
		p.ClientID = unquote(v)
	case "client_secret_file":
		p.ClientSecretFile = unquote(v)
	case "broker_url":
		p.BrokerURL = strings.TrimRight(unquote(v), "/")
	case "scopes":
		list, err := parseStringArray(v)
		if err != nil {
			return err
		}
		p.Scopes = list
	default:
		return nerr.New(nerr.InvalidFormat, "oidcclient.LoadClientConfig", "unknown key in idp profile: "+k)
	}
	return nil
}

// Resolve turns a parsed ConfigProfile into an IdPProfile, reading the client
// secret file if one is configured.
func (p ConfigProfile) Resolve() (IdPProfile, error) {
	out := IdPProfile{
		Name:      p.Name,
		Issuer:    p.Issuer,
		ClientID:  p.ClientID,
		BrokerURL: p.BrokerURL,
		Scopes:    append([]string(nil), p.Scopes...),
	}
	if p.ClientSecretFile != "" {
		raw, err := os.ReadFile(p.ClientSecretFile)
		if err != nil {
			return IdPProfile{}, nerr.Wrap(nerr.IO, "oidcclient.ConfigProfile.Resolve", "read client_secret_file", err)
		}
		out.ClientSecret = strings.TrimSpace(string(raw))
	}
	return out, nil
}

func stripComment(s string) string {
	// A '#' inside a quoted value is not a comment; the config values here never
	// legitimately contain '#', so a simple split is enough and stays safe.
	if i := strings.IndexByte(s, '#'); i >= 0 {
		if !strings.Contains(s[:i], `"`) {
			return s[:i]
		}
	}
	return s
}

func parseIdPSection(line string) (string, bool) {
	inner := strings.TrimSpace(line[1 : len(line)-1])
	if !strings.HasPrefix(inner, "idp.") {
		return "", false
	}
	name := strings.TrimSpace(strings.TrimPrefix(inner, "idp."))
	name = strings.Trim(name, `"`)
	if name == "" {
		return "", false
	}
	return name, true
}

func unquote(v string) string {
	v = strings.TrimSpace(v)
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		return v[1 : len(v)-1]
	}
	return v
}

func parseStringArray(v string) ([]string, error) {
	v = strings.TrimSpace(v)
	if !strings.HasPrefix(v, "[") || !strings.HasSuffix(v, "]") {
		return nil, nerr.New(nerr.InvalidFormat, "oidcclient.LoadClientConfig", "scopes must be a [\"a\", \"b\"] array")
	}
	body := strings.TrimSpace(v[1 : len(v)-1])
	if body == "" {
		return nil, nil
	}
	var out []string
	for _, part := range strings.Split(body, ",") {
		s := unquote(strings.TrimSpace(part))
		if s != "" {
			out = append(out, s)
		}
	}
	return out, nil
}
