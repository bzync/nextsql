// Package profile defines NextSQL Admin's connection profiles: the named
// nextsqld servers an Operations/Studio session may sign in to or switch to.
//
// The set of reachable servers is decided by whoever runs the nextsql-admin
// process, never by the browser. The implicit "default" profile comes from
// the --server-addr / --tls-* flags; any others come from an operator-owned,
// versioned JSON file named by --profiles. The browser only ever picks a
// profile by ID, so an authenticated (or unauthenticated) web client cannot
// make Admin dial an arbitrary host or read an arbitrary local file — the
// Admin listener may be served to remote browsers over TLS, which makes that
// a real capability boundary, not a formality.
//
// A profile holds no secret. Passwords are typed per sign-in, or — only on a
// switch, only when the operator asks — kept in the OS credential store by
// internal/admin/credential, bound to the principal that saved them.
package profile

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

const (
	// FormatVersion is the only profile-file version this build reads. A
	// file declaring any other version is refused rather than half-read.
	FormatVersion = 1

	// DefaultID names the implicit profile built from the --server-addr /
	// --tls-* flags. A profiles file may not redefine it.
	DefaultID = "default"

	// MaxProfiles bounds how many profiles one file may declare, so the
	// pre-auth listing and the switcher stay small.
	MaxProfiles = 32

	// MaxFileBytes caps the profile file read. Real files are a few KiB.
	MaxFileBytes = 64 << 10

	maxNameBytes    = 64
	maxAddressBytes = 255
	maxUserBytes    = 128
	maxPathBytes    = 4096
)

// Environments are the environment labels a profile may declare. They are
// the same four labels Studio's per-viewer environment tag offers, so a
// profile-declared environment drives the same production banner and
// read-only safety default.
var Environments = []string{"development", "test", "staging", "production"}

var (
	idPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)
	databasePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// Profile is one nextsqld server Admin may connect to. Paths are absolute
// once loaded (a relative path in the file is resolved against the file's
// own directory).
type Profile struct {
	ID          string
	Name        string
	Environment string
	Address     string

	TLSCA         string
	TLSServerName string
	TLSClientCert string
	TLSClientKey  string
	Insecure      bool

	// User and Database are sign-in hints only. User pre-fills the switch
	// form; Database is sent in the Hello (empty selects the server's own).
	User     string
	Database string
}

// Public is what an unauthenticated login page may learn about a profile:
// enough to choose one, and nothing about where it points.
type Public struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Environment string `json:"environment,omitempty"`
}

// Detail is what an authenticated session may learn about a profile. It
// adds the target address and TLS posture so an operator can see where a
// password would be sent before sending it, but never a local file path.
type Detail struct {
	Public
	Address  string `json:"address"`
	User     string `json:"user,omitempty"`
	Database string `json:"database,omitempty"`
	TLS      bool   `json:"tls"`
	MTLS     bool   `json:"mtls"`
}

// DisplayName is the profile's name, falling back to its ID.
func (p Profile) DisplayName() string {
	if p.Name != "" {
		return p.Name
	}
	return p.ID
}

// Public returns the pre-auth view of p.
func (p Profile) Public() Public {
	return Public{ID: p.ID, Name: p.DisplayName(), Environment: p.Environment}
}

// Detail returns the authenticated view of p.
func (p Profile) Detail() Detail {
	return Detail{
		Public:   p.Public(),
		Address:  p.Address,
		User:     p.User,
		Database: p.Database,
		TLS:      !p.Insecure,
		MTLS:     p.TLSClientCert != "",
	}
}

// ServerName is the TLS server name Admin verifies for p.
func (p Profile) ServerName() string {
	if p.TLSServerName != "" {
		return p.TLSServerName
	}
	if h, _, err := net.SplitHostPort(p.Address); err == nil && h != "" {
		return h
	}
	return "localhost"
}

// ClientTLS builds the TLS 1.3 client configuration for p, reading its CA
// (and client key pair, for mTLS) from disk. It returns nil for an insecure
// loopback profile.
func (p Profile) ClientTLS() (*tls.Config, error) {
	if p.Insecure {
		return nil, nil
	}
	if p.TLSClientCert != "" {
		return security.ClientMTLS(p.ServerName(), p.TLSCA, p.TLSClientCert, p.TLSClientKey)
	}
	return security.ClientTLS(p.ServerName(), p.TLSCA)
}

// ValidateTarget checks the address and TLS posture shared by every profile,
// including the flag-built default. It does not touch the network or disk.
func (p Profile) ValidateTarget() error {
	const op = "profile.Validate"
	if len(p.Address) > maxAddressBytes {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: address is longer than %d bytes", p.ID, maxAddressBytes))
	}
	host, port, err := net.SplitHostPort(p.Address)
	if err != nil || strings.TrimSpace(host) == "" {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: address must be host:port", p.ID))
	}
	if n, err := strconv.Atoi(port); err != nil || n < 1 || n > 65535 {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: address port must be 1-65535", p.ID))
	}
	if p.Insecure {
		if security.RequireTLS(p.Address) {
			return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: insecure (plaintext) is only allowed for a loopback address", p.ID))
		}
		if p.TLSCA != "" || p.TLSClientCert != "" || p.TLSClientKey != "" || p.TLSServerName != "" {
			return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: insecure cannot be combined with TLS settings", p.ID))
		}
		return nil
	}
	if p.TLSCA == "" {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: tls_ca is required unless insecure is set (loopback only)", p.ID))
	}
	if (p.TLSClientCert == "") != (p.TLSClientKey == "") {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: tls_client_cert and tls_client_key must be set together", p.ID))
	}
	return nil
}

// Validate checks every field of a file-declared profile.
func (p Profile) Validate() error {
	const op = "profile.Validate"
	if !idPattern.MatchString(p.ID) {
		return nerr.New(nerr.InvalidArgument, op,
			fmt.Sprintf("profile id %q must be 1-63 lowercase letters, digits, '_' or '-', starting with a letter or digit", p.ID))
	}
	if p.ID == DefaultID {
		return nerr.New(nerr.InvalidArgument, op,
			`profile id "default" is reserved for the --server-addr target`)
	}
	if err := validateLabel(p.ID, "name", p.Name, maxNameBytes); err != nil {
		return err
	}
	if err := ValidateEnvironment(p.Environment); err != nil {
		return nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: %s", p.ID, err.Error()))
	}
	if err := validateLabel(p.ID, "user", p.User, maxUserBytes); err != nil {
		return err
	}
	if p.Database != "" && !databasePattern.MatchString(p.Database) {
		return nerr.New(nerr.InvalidArgument, op,
			fmt.Sprintf("profile %q: database must be letters, digits, '_' or '-'", p.ID))
	}
	return p.ValidateTarget()
}

// ValidateEnvironment accepts "" or one of Environments.
func ValidateEnvironment(env string) error {
	if env == "" {
		return nil
	}
	for _, e := range Environments {
		if env == e {
			return nil
		}
	}
	return errors.New("environment must be one of " + strings.Join(Environments, ", "))
}

func validateLabel(id, field, v string, max int) error {
	if len(v) > max {
		return nerr.New(nerr.InvalidArgument, "profile.Validate",
			fmt.Sprintf("profile %q: %s is longer than %d bytes", id, field, max))
	}
	if !utf8.ValidString(v) || strings.IndexFunc(v, unicode.IsControl) >= 0 {
		return nerr.New(nerr.InvalidArgument, "profile.Validate",
			fmt.Sprintf("profile %q: %s must be printable UTF-8", id, field))
	}
	return nil
}

// fileJSON is the version-1 on-disk shape. Decoding is strict: an unknown
// key is an error, so a misspelled "tls_ca" (or a "password" someone hoped
// Admin would read) fails loudly instead of being ignored.
type fileJSON struct {
	Version  int           `json:"version"`
	Profiles []profileJSON `json:"profiles"`
}

type profileJSON struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Environment   string `json:"environment"`
	Address       string `json:"address"`
	TLSCA         string `json:"tls_ca"`
	TLSServerName string `json:"tls_server_name"`
	TLSClientCert string `json:"tls_client_cert"`
	TLSClientKey  string `json:"tls_client_key"`
	Insecure      bool   `json:"insecure"`
	User          string `json:"user"`
	Database      string `json:"database"`
}

// Parse decodes and validates a profile file's bytes. Relative TLS paths are
// resolved against baseDir. It never reads the TLS files themselves.
func Parse(data []byte, baseDir string) ([]Profile, error) {
	const op = "profile.Parse"
	if len(data) > MaxFileBytes {
		return nil, nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile file is larger than %d bytes", MaxFileBytes))
	}
	// The version is checked before the strict decode so a newer file is
	// reported as a version mismatch, not as a confusing unknown field.
	var head struct {
		Version *int `json:"version"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "profile file is not valid JSON", err)
	}
	if head.Version == nil {
		return nil, nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile file must declare \"version\": %d", FormatVersion))
	}
	if *head.Version != FormatVersion {
		return nil, nerr.New(nerr.InvalidArgument, op,
			fmt.Sprintf("profile file version %d is not supported (this nextsql-admin reads version %d)", *head.Version, FormatVersion))
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var f fileJSON
	if err := dec.Decode(&f); err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "profile file does not match the version 1 format", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, nerr.New(nerr.InvalidArgument, op, "profile file has trailing data after the JSON object")
	}
	if len(f.Profiles) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, op, "profile file declares no profiles")
	}
	if len(f.Profiles) > MaxProfiles {
		return nil, nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile file declares more than %d profiles", MaxProfiles))
	}

	out := make([]Profile, 0, len(f.Profiles))
	seen := make(map[string]bool, len(f.Profiles))
	for _, pj := range f.Profiles {
		p := Profile{
			ID:            pj.ID,
			Name:          strings.TrimSpace(pj.Name),
			Environment:   pj.Environment,
			Address:       strings.TrimSpace(pj.Address),
			TLSServerName: strings.TrimSpace(pj.TLSServerName),
			Insecure:      pj.Insecure,
			User:          strings.TrimSpace(pj.User),
			Database:      strings.TrimSpace(pj.Database),
		}
		var err error
		for _, field := range []struct {
			name string
			in   string
			out  *string
		}{
			{"tls_ca", pj.TLSCA, &p.TLSCA},
			{"tls_client_cert", pj.TLSClientCert, &p.TLSClientCert},
			{"tls_client_key", pj.TLSClientKey, &p.TLSClientKey},
		} {
			if *field.out, err = resolvePath(baseDir, field.in); err != nil {
				return nil, nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile %q: %s %s", pj.ID, field.name, err.Error()))
			}
		}
		if err := p.Validate(); err != nil {
			return nil, err
		}
		if seen[p.ID] {
			return nil, nerr.New(nerr.InvalidArgument, op, fmt.Sprintf("profile id %q is declared more than once", p.ID))
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	return out, nil
}

func resolvePath(baseDir, p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", nil
	}
	if len(p) > maxPathBytes || strings.ContainsRune(p, 0) {
		return "", errors.New("is not a usable path")
	}
	if !filepath.IsAbs(p) {
		p = filepath.Join(baseDir, p)
	}
	return filepath.Clean(p), nil
}

// Load reads, permission-checks, and parses the profile file at path. The
// file decides where operators' passwords are sent, so on Unix it must be a
// regular file owned by the current user (or root) and not writable by group
// or others — the same rule OpenSSH applies to its client config.
func Load(path string) ([]Profile, error) {
	const op = "profile.Load"
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "resolve profile file path", err)
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "open profile file", err)
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "stat profile file", err)
	}
	if !info.Mode().IsRegular() {
		return nil, nerr.New(nerr.InvalidArgument, op, "profile file must be a regular file")
	}
	if err := checkOwnership(info); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxFileBytes+1))
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "read profile file", err)
	}
	return Parse(data, filepath.Dir(abs))
}
