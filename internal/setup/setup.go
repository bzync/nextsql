// Package setup turns detected hardware plus a resource preset into a
// validated server configuration and an ordered installation plan. It is the
// automation backbone under `nextsql setup` (P28): every knob the GUI
// installer will expose is computed here so the CLI, container init, and GUI
// share one code path and one set of secure defaults.
//
// The package performs no I/O of its own beyond what sysinfo already did — it
// takes an sysinfo.Info in and returns a plan out. The command layer executes
// the plan (writing the config file, running init, verifying health).
package setup

import (
	"errors"
	"net"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/sysinfo"
)

// Preset selects how aggressively the buffer pool is sized against physical
// RAM. Custom leaves sizing to an explicit --buffer-pages value.
type Preset string

const (
	PresetConservative    Preset = "conservative"
	PresetBalanced        Preset = "balanced"
	PresetHighPerformance Preset = "high-performance"
	PresetCustom          Preset = "custom"
)

// DefaultPreset is applied when the operator names none.
const DefaultPreset = PresetBalanced

// ramFraction is the share of physical RAM each preset targets for the
// buffer pool. High-performance stays at half: NextSQL also needs headroom
// for the OS page cache (WAL, catalog, sort/hash spill files) and per-query
// working memory, so committing the whole machine to page frames is slower,
// not faster.
var ramFraction = map[Preset]float64{
	PresetConservative:    0.10,
	PresetBalanced:        0.25,
	PresetHighPerformance: 0.50,
}

// maxRAMFraction caps a derived buffer pool regardless of preset, so an
// explicit or rounding artifact can never starve the rest of the process.
const maxRAMFraction = 0.75

// ParsePreset validates a preset name, returning DefaultPreset for "".
func ParsePreset(s string) (Preset, error) {
	switch Preset(strings.ToLower(strings.TrimSpace(s))) {
	case "":
		return DefaultPreset, nil
	case PresetConservative:
		return PresetConservative, nil
	case PresetBalanced:
		return PresetBalanced, nil
	case PresetHighPerformance:
		return PresetHighPerformance, nil
	case PresetCustom:
		return PresetCustom, nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "setup.ParsePreset",
			"preset must be conservative, balanced, high-performance, or custom")
	}
}

// Recommendation is the buffer-pool sizing decision plus the reasoning
// behind it, surfaced verbatim in `nextsql setup` output.
type Recommendation struct {
	Preset      Preset  `json:"preset"`
	BufferPages int     `json:"buffer_pages"`
	BufferBytes uint64  `json:"buffer_bytes"`
	RAMFraction float64 `json:"ram_fraction"`
	Rationale   string  `json:"rationale"`
}

// Recommend sizes the buffer pool. An explicitBufferPages > 0 wins outright.
// Otherwise a RAM-backed preset scales with detected memory; if RAM is
// undetected (RAMBytes == 0) or the preset is Custom with no explicit value,
// it falls back to config.DefaultBufferPages.
func Recommend(info sysinfo.Info, preset Preset, explicitBufferPages int) Recommendation {
	const pageSize = format.LogicalPageSize

	if explicitBufferPages > 0 {
		return Recommendation{
			Preset:      preset,
			BufferPages: explicitBufferPages,
			BufferBytes: uint64(explicitBufferPages) * pageSize,
			Rationale:   "explicit --buffer-pages override",
		}
	}

	frac, scaled := ramFraction[preset]
	if !scaled || info.RAMBytes == 0 {
		reason := "custom preset without --buffer-pages: using the built-in default"
		if scaled {
			reason = "physical RAM could not be detected on this platform: using the built-in default"
		}
		return Recommendation{
			Preset:      preset,
			BufferPages: config.DefaultBufferPages,
			BufferBytes: uint64(config.DefaultBufferPages) * pageSize,
			Rationale:   reason,
		}
	}

	target := uint64(float64(info.RAMBytes) * frac)
	if ceiling := uint64(float64(info.RAMBytes) * maxRAMFraction); target > ceiling {
		target = ceiling
	}
	pages := int(target / pageSize)
	if pages < config.DefaultBufferPages {
		pages = config.DefaultBufferPages
	}
	return Recommendation{
		Preset:      preset,
		BufferPages: pages,
		BufferBytes: uint64(pages) * pageSize,
		RAMFraction: frac,
		Rationale: preset.describe() +
			": buffer pool sized to " + strconv.FormatFloat(frac*100, 'g', -1, 64) + "% of detected RAM",
	}
}

func (p Preset) describe() string {
	switch p {
	case PresetConservative:
		return "conservative preset"
	case PresetBalanced:
		return "balanced preset"
	case PresetHighPerformance:
		return "high-performance preset"
	default:
		return "custom preset"
	}
}

// ErrInsecureRemote is returned by BuildPlan when a non-loopback listen
// address is requested without a TLS certificate and key. The command layer
// maps it to the validation exit code.
var ErrInsecureRemote = errors.New("a non-loopback listen address requires --tls-cert and --tls-key")

// Params drives BuildPlan. Base is the starting config (Default(), or a
// config loaded from --config-in); the remaining fields override it.
type Params struct {
	Base            config.Config
	Info            sysinfo.Info
	Preset          Preset
	DataDir         string
	KeyFile         string
	InstanceKeyFile string
	ListenAddr      string
	LogLevel        string
	TLSCert         string
	TLSKey          string
	BufferPages     int
	AdminUser       string
	ConfigPath      string
	RunInit         bool
}

// Plan is the fully-resolved, validated result of BuildPlan: the config to
// persist, where to persist it, whether to initialize the database, and any
// non-fatal advisories the operator should see first.
type Plan struct {
	Info            sysinfo.Info   `json:"hardware"`
	Recommendation  Recommendation `json:"recommendation"`
	Config          config.Config  `json:"-"`
	ConfigPath      string         `json:"config_path"`
	ListenAddr      string         `json:"listen_addr"`
	TLS             bool           `json:"tls"`
	DataDir         string         `json:"data_dir"`
	KeyFile         string         `json:"key_file"`
	InstanceKeyFile string         `json:"instance_key_file"`
	AdminUser       string         `json:"admin_user,omitempty"`
	RunInit         bool           `json:"run_init"`
	Warnings        []string       `json:"warnings"`
}

// BuildPlan resolves Params into a validated Plan. It returns ErrInsecureRemote
// for a remote listen address without TLS, and a wrapped config-validation
// error if the resulting config is otherwise invalid. It performs no I/O.
func BuildPlan(p Params) (Plan, error) {
	cfg := p.Base

	cfg.DataDir = p.DataDir
	cfg.KeyFile = p.KeyFile
	cfg.InstanceKeyFile = p.InstanceKeyFile
	if cfg.InstanceKeyFile == "" && cfg.KeyFile != "" {
		cfg.InstanceKeyFile = cfg.KeyFile + ".instance"
	}
	if p.ListenAddr != "" {
		cfg.ListenAddr = p.ListenAddr
	}
	if cfg.ListenAddr == "" {
		cfg.ListenAddr = config.DefaultListenAddr
	}
	if p.LogLevel != "" {
		cfg.LogLevel = p.LogLevel
	}
	cfg.TLSCert = p.TLSCert
	cfg.TLSKey = p.TLSKey

	rec := Recommend(p.Info, p.Preset, p.BufferPages)
	cfg.BufferPages = rec.BufferPages

	loopback := isLoopbackAddr(cfg.ListenAddr)
	hasTLS := cfg.TLSCert != "" && cfg.TLSKey != ""
	if !loopback && !hasTLS {
		return Plan{}, ErrInsecureRemote
	}

	if err := cfg.Validate(); err != nil {
		return Plan{}, nerr.Wrap(nerr.InvalidArgument, "setup.BuildPlan", "invalid configuration", err)
	}

	warnings := advisories(p.Info, cfg, rec, p.AdminUser, loopback)

	return Plan{
		Info:            p.Info,
		Recommendation:  rec,
		Config:          cfg,
		ConfigPath:      p.ConfigPath,
		ListenAddr:      cfg.ListenAddr,
		TLS:             hasTLS,
		DataDir:         cfg.DataDir,
		KeyFile:         cfg.KeyFile,
		InstanceKeyFile: cfg.InstanceKeyFile,
		AdminUser:       p.AdminUser,
		RunInit:         p.RunInit,
		Warnings:        warnings,
	}, nil
}

func advisories(info sysinfo.Info, cfg config.Config, rec Recommendation, adminUser string, loopback bool) []string {
	var w []string
	if info.RAMBytes == 0 {
		w = append(w, "physical RAM was not detected; buffer pool left at the built-in default — set --buffer-pages explicitly for a tuned deployment")
	}
	switch info.Filesystem {
	case "tmpfs", "ramfs":
		w = append(w, "data volume filesystem is "+info.Filesystem+": contents will not survive a reboot — choose a persistent --data-dir for production")
	case "overlay":
		w = append(w, "data volume filesystem is overlay (typical of a container's writable layer): use a mounted volume so data outlives the container")
	}
	if info.DiskFreeBytes > 0 {
		needed := rec.BufferBytes * 4
		if needed < 2<<30 {
			needed = 2 << 30
		}
		if info.DiskFreeBytes < needed {
			w = append(w, "data volume has "+humanBytes(info.DiskFreeBytes)+" free; at least "+humanBytes(needed)+" is recommended for WAL, checkpoints, and growth headroom")
		}
	}
	if adminUser == "" {
		w = append(w, "no --user/--password-file given: the database will initialize without a bootstrap administrator — create one before exposing the server")
	}
	if !loopback {
		w = append(w, "listen address "+cfg.ListenAddr+" is not loopback: ensure the host firewall restricts access to trusted networks")
	}
	return w
}

// isLoopbackAddr reports whether host:port binds only to the loopback
// interface. A bare port, an empty host, or "0.0.0.0"/"::" all count as
// non-loopback (they expose every interface).
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	return ip.IsLoopback()
}

func humanBytes(n uint64) string {
	const (
		kib = 1 << 10
		mib = 1 << 20
		gib = 1 << 30
	)
	var val float64
	var unit string
	switch {
	case n >= gib:
		val, unit = float64(n)/gib, "GiB"
	case n >= mib:
		val, unit = float64(n)/mib, "MiB"
	case n >= kib:
		val, unit = float64(n)/kib, "KiB"
	default:
		return strconv.FormatUint(n, 10) + " B"
	}
	return strconv.FormatFloat(val, 'f', 1, 64) + " " + unit
}
