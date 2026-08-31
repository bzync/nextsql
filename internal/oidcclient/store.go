package oidcclient

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// credentialVersion is the on-disk schema tag for a stored credential.
const credentialVersion = 1

// Credential is a stored broker-minted credential plus everything needed to
// renew it silently. It is written mode 0600.
type Credential struct {
	Version      int       `json:"version"`
	IdP          string    `json:"idp"`
	Host         string    `json:"host"`
	Issuer       string    `json:"issuer"`
	ClientID     string    `json:"client_id"`
	BrokerURL    string    `json:"broker_url"`
	Scopes       []string  `json:"scopes,omitempty"`
	Principal    string    `json:"principal"`
	Roles        []string  `json:"roles,omitempty"`
	Database     string    `json:"database,omitempty"`
	Realm        string    `json:"realm,omitempty"`
	Credential   string    `json:"credential"`
	TokenID      string    `json:"token_id,omitempty"`
	ExpiresAt    time.Time `json:"expires_at"`
	ObtainedAt   time.Time `json:"obtained_at"`
	RefreshToken string    `json:"refresh_token,omitempty"`
}

// Valid reports whether the credential is structurally usable.
func (c *Credential) Valid() error {
	if c == nil {
		return nerr.New(nerr.NotFound, "oidcclient.Credential", "no stored credential")
	}
	if c.Version != credentialVersion {
		return nerr.New(nerr.InvalidFormat, "oidcclient.Credential", "unsupported stored-credential version")
	}
	if strings.TrimSpace(c.Credential) == "" || strings.TrimSpace(c.Principal) == "" {
		return nerr.New(nerr.InvalidFormat, "oidcclient.Credential", "stored credential is incomplete")
	}
	return nil
}

// Fresh reports whether the credential is still valid at now with skew to spare.
func (c *Credential) Fresh(now time.Time, skew time.Duration) bool {
	return c.Valid() == nil && now.Add(skew).Before(c.ExpiresAt)
}

// Store is a directory of stored credentials, one JSON file per (IdP, host).
type Store struct {
	Dir string
}

// DefaultStore returns the store under the user's config directory
// (`~/.config/nextsql/credentials` on Linux).
func DefaultStore() (*Store, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "oidcclient.DefaultStore", "resolve user config dir", err)
	}
	return &Store{Dir: filepath.Join(base, "nextsql", "credentials")}, nil
}

func sanitize(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '.':
			b.WriteRune(r)
		case r == ':':
			b.WriteRune('_')
		default:
			b.WriteRune('_')
		}
	}
	out := b.String()
	if out == "" {
		out = "_"
	}
	return out
}

func (s *Store) path(idp, host string) string {
	return filepath.Join(s.Dir, sanitize(idp)+"@"+sanitize(host)+".json")
}

// Path is the file a credential for (idp, host) is stored at.
func (s *Store) Path(idp, host string) string { return s.path(idp, host) }

// Load returns the stored credential for (idp, host), or a NotFound error.
func (s *Store) Load(idp, host string) (*Credential, error) {
	raw, err := os.ReadFile(s.path(idp, host))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nerr.New(nerr.NotFound, "oidcclient.Store.Load", "no stored credential for this identity provider and host; run `nextsql login`")
		}
		return nil, nerr.Wrap(nerr.IO, "oidcclient.Store.Load", "read credential file", err)
	}
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, "oidcclient.Store.Load", "decode credential file", err)
	}
	if err := c.Valid(); err != nil {
		return nil, err
	}
	return &c, nil
}

// Save writes the credential atomically with mode 0600.
func (s *Store) Save(c *Credential) error {
	const op = "oidcclient.Store.Save"
	if err := c.Valid(); err != nil {
		return err
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nerr.Wrap(nerr.IO, op, "create credentials directory", err)
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nerr.Wrap(nerr.Internal, op, "encode credential", err)
	}
	raw = append(raw, '\n')
	final := s.path(c.IdP, c.Host)
	tmp := final + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, op, "write credential file", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, op, "commit credential file", err)
	}
	return nil
}

// Delete removes the stored credential for (idp, host). Absent is not an error.
func (s *Store) Delete(idp, host string) error {
	if err := os.Remove(s.path(idp, host)); err != nil && !os.IsNotExist(err) {
		return nerr.Wrap(nerr.IO, "oidcclient.Store.Delete", "remove credential file", err)
	}
	return nil
}

// List returns every stored credential, sorted by IdP then host.
func (s *Store) List() ([]Credential, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, nerr.Wrap(nerr.IO, "oidcclient.Store.List", "read credentials directory", err)
	}
	var out []Credential
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(s.Dir, e.Name()))
		if err != nil {
			continue
		}
		var c Credential
		if json.Unmarshal(raw, &c) == nil && c.Valid() == nil {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IdP != out[j].IdP {
			return out[i].IdP < out[j].IdP
		}
		return out[i].Host < out[j].Host
	})
	return out, nil
}
