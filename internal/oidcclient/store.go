package oidcclient

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// credentialVersion is the on-disk schema tag for a stored credential.
const credentialVersion = 1

const maxStoredCredentialBytes = 1 << 20 // 1 MiB

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
	GrantType    string    `json:"grant_type,omitempty"`
	// ClientSecretFile is a path, never secret material. It is retained only
	// for non-interactive client-credentials renewal.
	ClientSecretFile string `json:"client_secret_file,omitempty"`
}

// Valid reports whether the credential is structurally usable.
func (c *Credential) Valid() error {
	if c == nil {
		return nerr.New(nerr.NotFound, "oidcclient.Credential", "no stored credential")
	}
	if c.Version != credentialVersion {
		return nerr.New(nerr.InvalidFormat, "oidcclient.Credential", "unsupported stored-credential version")
	}
	if strings.TrimSpace(c.IdP) == "" || strings.TrimSpace(c.Host) == "" {
		return nerr.New(nerr.InvalidFormat, "oidcclient.Credential", "stored credential has no identity-provider or host key")
	}
	if strings.TrimSpace(c.Credential) == "" || strings.TrimSpace(c.Principal) == "" {
		return nerr.New(nerr.InvalidFormat, "oidcclient.Credential", "stored credential is incomplete")
	}
	switch c.GrantType {
	case "", "authorization_code", "client_credentials":
	default:
		return nerr.New(nerr.InvalidFormat, "oidcclient.Credential", "stored credential has an unknown grant type")
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
	out := sanitizeLegacy(s)
	if len(out) > 48 {
		return out[:48]
	}
	return out
}

func sanitizeLegacy(s string) string {
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
	sum := sha256.Sum256([]byte(idp + "\x00" + host))
	name := sanitize(idp) + "@" + sanitize(host) + "-" + fmt.Sprintf("%x", sum[:8]) + ".json"
	return filepath.Join(s.Dir, name)
}

// legacyPath is the pre-hash filename used by the initial, undocumented
// client implementation. Load/Delete retain compatibility without writing new
// credentials to this collision-prone name.
func (s *Store) legacyPath(idp, host string) string {
	return filepath.Join(s.Dir, sanitizeLegacy(idp)+"@"+sanitizeLegacy(host)+".json")
}

// Path is the file a credential for (idp, host) is stored at.
func (s *Store) Path(idp, host string) string { return s.path(idp, host) }

// Load returns the stored credential for (idp, host), or a NotFound error.
func (s *Store) Load(idp, host string) (*Credential, error) {
	path := s.path(idp, host)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		path = s.legacyPath(idp, host)
		info, err = os.Lstat(path)
	}
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nerr.New(nerr.NotFound, "oidcclient.Store.Load", "no stored credential for this identity provider and host; run `nextsql login`")
		}
		return nil, nerr.Wrap(nerr.IO, "oidcclient.Store.Load", "inspect credential file", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, nerr.New(nerr.InvalidFormat, "oidcclient.Store.Load", "credential path is not a regular file")
	}
	if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
		return nil, nerr.New(nerr.Forbidden, "oidcclient.Store.Load", "credential file permissions are too broad; require mode 0600")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "oidcclient.Store.Load", "open credential file", err)
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxStoredCredentialBytes+1))
	closeErr := f.Close()
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "oidcclient.Store.Load", "read credential file", err)
	}
	if closeErr != nil {
		return nil, nerr.Wrap(nerr.IO, "oidcclient.Store.Load", "close credential file", closeErr)
	}
	if len(raw) > maxStoredCredentialBytes {
		return nil, nerr.New(nerr.Exhausted, "oidcclient.Store.Load", "credential file exceeds 1 MiB")
	}
	var c Credential
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, "oidcclient.Store.Load", "decode credential file", err)
	}
	if err := c.Valid(); err != nil {
		return nil, err
	}
	if c.IdP != idp || c.Host != host {
		return nil, nerr.New(nerr.InvalidFormat, "oidcclient.Store.Load", "credential identity does not match its filename")
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
	dirInfo, err := os.Lstat(s.Dir)
	if err != nil {
		return nerr.Wrap(nerr.IO, op, "inspect credentials directory", err)
	}
	if !dirInfo.IsDir() || dirInfo.Mode()&os.ModeSymlink != 0 {
		return nerr.New(nerr.Forbidden, op, "credentials path must be a real directory")
	}
	if runtime.GOOS != "windows" && dirInfo.Mode().Perm()&0o077 != 0 {
		if err := os.Chmod(s.Dir, 0o700); err != nil {
			return nerr.Wrap(nerr.Forbidden, op, "restrict credentials directory to mode 0700", err)
		}
	}
	raw, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return nerr.Wrap(nerr.Internal, op, "encode credential", err)
	}
	raw = append(raw, '\n')
	final := s.path(c.IdP, c.Host)
	tmp, err := os.CreateTemp(s.Dir, ".credential-*.tmp")
	if err != nil {
		return nerr.Wrap(nerr.IO, op, "create temporary credential file", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		_ = tmp.Close()
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return nerr.Wrap(nerr.IO, op, "restrict temporary credential file", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		return nerr.Wrap(nerr.IO, op, "write credential file", err)
	}
	if err := tmp.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, op, "sync credential file", err)
	}
	if err := tmp.Close(); err != nil {
		return nerr.Wrap(nerr.IO, op, "close credential file", err)
	}
	if err := os.Rename(tmpName, final); err != nil {
		return nerr.Wrap(nerr.IO, op, "commit credential file", err)
	}
	committed = true
	if dir, err := os.Open(s.Dir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

// Delete removes the stored credential for (idp, host). Absent is not an error.
func (s *Store) Delete(idp, host string) error {
	for _, path := range []string{s.path(idp, host), s.legacyPath(idp, host)} {
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return nerr.Wrap(nerr.IO, "oidcclient.Store.Delete", "remove credential file", err)
		}
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
	seen := make(map[string]bool)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(s.Dir, e.Name())
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() > maxStoredCredentialBytes {
			continue
		}
		if runtime.GOOS != "windows" && info.Mode().Perm()&0o077 != 0 {
			continue
		}
		f, err := os.Open(path)
		if err != nil {
			continue
		}
		raw, readErr := io.ReadAll(io.LimitReader(f, maxStoredCredentialBytes+1))
		_ = f.Close()
		if readErr != nil || len(raw) > maxStoredCredentialBytes {
			continue
		}
		var c Credential
		if json.Unmarshal(raw, &c) != nil || c.Valid() != nil {
			continue
		}
		newName := filepath.Base(s.path(c.IdP, c.Host))
		oldName := filepath.Base(s.legacyPath(c.IdP, c.Host))
		key := c.IdP + "\x00" + c.Host
		if (e.Name() == newName || e.Name() == oldName) && !seen[key] {
			out = append(out, c)
			seen[key] = true
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
