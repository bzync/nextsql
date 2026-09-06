package setup

import (
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// validPresets mirrors internal/setup.Preset's four values. This package does
// not import internal/setup (see the package doc) — it just needs to reject
// an obviously-wrong value before spending a subprocess call on it; `nextsql
// setup` re-validates authoritatively regardless.
var validPresets = map[string]bool{
	"":                 true, // empty means "let nextsql setup default to balanced"
	"conservative":     true,
	"balanced":         true,
	"high-performance": true,
	"custom":           true,
}

var validProfiles = map[string]bool{
	"":           true, // empty means "let nextsql setup default to developer"
	"developer":  true,
	"production": true,
}

// Params is the one shape shared by /api/v1/plan (dry-run preview) and
// /api/v1/install (the real thing) — the Summary screen renders exactly what
// Install will do because both calls build their `nextsql setup` argv from
// the same Params the same way.
type Params struct {
	DataDir string `json:"dataDir"`
	KeyFile string `json:"keyFile"`

	// ConfigOut is `nextsql setup --config-out`: where the generated
	// nextsql.conf is written. Empty means let `nextsql setup` default to
	// DataDir/nextsql.conf. The wizard prefills this from detectDefaults'
	// ConfigOut (the same /etc-or-/var-lib, or per-user XDG, split the
	// tarball/.deb/.run installers use — see defaults.go) so a config
	// written by this wizard lands where an already-installed systemd unit
	// expects it; the operator never has to type it by hand for the common
	// case, and nothing here duplicates internal/setup's path logic — an
	// empty value still falls through to nextsql setup's own default.
	ConfigOut string `json:"configOut"`

	Preset      string `json:"preset"`      // "" | conservative | balanced | high-performance | custom
	Profile     string `json:"profile"`     // "" | developer | production
	BufferPages int    `json:"bufferPages"` // only meaningful when Preset == "custom"

	AdminUser     string `json:"adminUser"`
	AdminPassword string `json:"adminPassword"` // never logged, never written to argv — see toArgs

	Realm    string `json:"realm"`
	Database string `json:"database"`

	// SkipInit is the wizard's "Advanced" option: write the config file only,
	// do not initialize a database now (the CLI's `nextsql setup --skip-init`).
	SkipInit bool `json:"skipInit"`

	// ListenAddr, TLSCert, and TLSKey are the wizard's "Advanced" network
	// option (M3): empty ListenAddr leaves `nextsql setup` at its own
	// loopback default. This package never inspects whether an address is
	// loopback itself (that heuristic lives in internal/setup, a forbidden
	// import here — see imports_test.go); Validate only enforces that the
	// cert/key pair travels together, and `nextsql setup --dry-run` is the
	// authoritative check that a non-loopback address has both (it already
	// refuses otherwise, exit 6, writing nothing — see setup.ErrInsecureRemote).
	// TLSCert/TLSKey are filesystem paths the operator already placed on this
	// machine, exactly like KeyFile — never file content, never uploaded.
	ListenAddr string `json:"listenAddr"`
	TLSCert    string `json:"tlsCert"`
	TLSKey     string `json:"tlsKey"`

	// EnableService requests that, after a successful (non-dry-run,
	// non-skip-init) install, the server also runs `systemctl [--user]
	// enable --now nextsql` — but only against a "nextsql" unit that is
	// already installed on this host (by a packaged .deb/.tar.gz/.run
	// installer) *and* whose own --config already matches the configuration
	// this run just wrote; see service.go and server.go's post-install
	// check. This field never reaches `nextsql setup`'s argv (toArgs does
	// not emit anything for it) — enabling a service is not something
	// `nextsql setup` does or knows about; it is a separate, best-effort
	// action the server takes on top, and its outcome (or failure) never
	// changes whether the database install itself succeeded.
	EnableService bool `json:"enableService"`
}

// Validate rejects obviously-unusable input before a subprocess is spent on
// it. It is not the security boundary — `nextsql setup` re-validates
// everything independently — but it turns a typo into an instant, specific
// error instead of a generic CLI failure message.
func (p Params) Validate() error {
	if strings.TrimSpace(p.DataDir) == "" {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "dataDir is required")
	}
	if strings.TrimSpace(p.KeyFile) == "" {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "keyFile is required")
	}
	if !validPresets[p.Preset] {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "preset must be one of: conservative, balanced, high-performance, custom")
	}
	if !validProfiles[p.Profile] {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "profile must be one of: developer, production")
	}
	if p.Profile == "production" && p.SkipInit {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "production profile cannot skip initialization")
	}
	if p.Preset == "custom" && p.BufferPages <= 0 {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "bufferPages must be positive when preset is custom")
	}
	if (p.AdminUser == "") != (p.AdminPassword == "") {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "adminUser and adminPassword must be given together")
	}
	if p.SkipInit && p.AdminUser != "" {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "adminUser has no effect with skipInit: no database is initialized to create it in")
	}
	if (p.TLSCert == "") != (p.TLSKey == "") {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "tlsCert and tlsKey must be given together")
	}
	if p.SkipInit && p.EnableService {
		return nerr.New(nerr.InvalidArgument, "setup.Params", "enableService has no effect with skipInit: no database is initialized to serve")
	}
	return nil
}

// toArgs builds the `nextsql setup` argv for this Params. passwordFile is a
// caller-owned temp file path already holding AdminPassword (empty if there
// is no admin user) — the password itself never appears in argv, which a
// local `ps` listing can read; see runner.go for the temp-file lifecycle.
func (p Params) toArgs(dryRun bool, passwordFile string) []string {
	realm := p.Realm
	if realm == "" {
		realm = "default"
	}
	database := p.Database
	if database == "" {
		database = "default"
	}
	args := []string{
		"setup",
		"--json",
		"--data-dir", p.DataDir,
		"--key-file", p.KeyFile,
		"--realm", realm,
		"--database", database,
	}
	if p.ConfigOut != "" {
		args = append(args, "--config-out", p.ConfigOut)
	}
	if p.Preset != "" {
		args = append(args, "--preset", p.Preset)
	}
	if p.Profile != "" {
		args = append(args, "--profile", p.Profile)
	}
	if p.Preset == "custom" && p.BufferPages > 0 {
		args = append(args, "--buffer-pages", strconv.Itoa(p.BufferPages))
	}
	if p.AdminUser != "" {
		args = append(args, "--user", p.AdminUser, "--password-file", passwordFile)
	}
	if p.SkipInit {
		args = append(args, "--skip-init")
	}
	if p.ListenAddr != "" {
		args = append(args, "--listen", p.ListenAddr)
	}
	if p.TLSCert != "" {
		args = append(args, "--tls-cert", p.TLSCert, "--tls-key", p.TLSKey)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}
