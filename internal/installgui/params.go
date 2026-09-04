package installgui

import (
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// validPresets mirrors internal/setup.Preset's four values. installgui does
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

// Params is the one shape shared by /api/v1/plan (dry-run preview) and
// /api/v1/install (the real thing) — the Summary screen renders exactly what
// Install will do because both calls build their `nextsql setup` argv from
// the same Params the same way.
type Params struct {
	DataDir string `json:"dataDir"`
	KeyFile string `json:"keyFile"`

	Preset      string `json:"preset"`      // "" | conservative | balanced | high-performance | custom
	BufferPages int    `json:"bufferPages"` // only meaningful when Preset == "custom"

	AdminUser     string `json:"adminUser"`
	AdminPassword string `json:"adminPassword"` // never logged, never written to argv — see toArgs

	Realm    string `json:"realm"`
	Database string `json:"database"`
}

// Validate rejects obviously-unusable input before a subprocess is spent on
// it. It is not the security boundary — `nextsql setup` re-validates
// everything independently — but it turns a typo into an instant, specific
// error instead of a generic CLI failure message.
func (p Params) Validate() error {
	if strings.TrimSpace(p.DataDir) == "" {
		return nerr.New(nerr.InvalidArgument, "installgui.Params", "dataDir is required")
	}
	if strings.TrimSpace(p.KeyFile) == "" {
		return nerr.New(nerr.InvalidArgument, "installgui.Params", "keyFile is required")
	}
	if !validPresets[p.Preset] {
		return nerr.New(nerr.InvalidArgument, "installgui.Params", "preset must be one of: conservative, balanced, high-performance, custom")
	}
	if p.Preset == "custom" && p.BufferPages <= 0 {
		return nerr.New(nerr.InvalidArgument, "installgui.Params", "bufferPages must be positive when preset is custom")
	}
	if (p.AdminUser == "") != (p.AdminPassword == "") {
		return nerr.New(nerr.InvalidArgument, "installgui.Params", "adminUser and adminPassword must be given together")
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
	if p.Preset != "" {
		args = append(args, "--preset", p.Preset)
	}
	if p.Preset == "custom" && p.BufferPages > 0 {
		args = append(args, "--buffer-pages", strconv.Itoa(p.BufferPages))
	}
	if p.AdminUser != "" {
		args = append(args, "--user", p.AdminUser, "--password-file", passwordFile)
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	return args
}
