package config

import (
	"path/filepath"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// Deployment profiles written to nextsql.conf as deployment_profile=.
// Empty is treated as developer for configs that predate the field.
const (
	ProfileDeveloper  = "developer"
	ProfileProduction = "production"
)

// Production operational defaults applied by `nextsql setup --profile production`
// (and by nextsqld --production when the corresponding fields are still zero).
// They are live-server settings, not buffer-pool sizing — resource presets
// still control BufferPages independently.
const (
	ProductionDiskWatermarkCheckMS     = 60_000
	ProductionReplicaLagCheckMS        = 60_000
	ProductionStatementTimeoutMS       = 30_000
	ProductionIdleTimeoutMS            = 60_000
	ProductionIdleTransactionTimeoutMS = 300_000
	ProductionLockTimeoutMS            = 30_000
	ProductionMaxConnections           = 128
	ProductionMaxConnectionsPerUser    = 32
)

// ParseDeploymentProfile validates a profile name. Empty becomes developer
// so existing scripts and configs keep working.
func ParseDeploymentProfile(s string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", ProfileDeveloper:
		return ProfileDeveloper, nil
	case ProfileProduction:
		return ProfileProduction, nil
	default:
		return "", nerr.New(nerr.InvalidArgument, "config.ParseDeploymentProfile",
			"deployment profile must be developer or production")
	}
}

// IsProduction reports whether this config is the live-production profile.
func (c Config) IsProduction() bool {
	return strings.EqualFold(strings.TrimSpace(c.DeploymentProfile), ProfileProduction)
}

// ApplyProductionDefaults fills zero-valued operational fields with the
// production-safe defaults. Non-zero values (from --config-in or an
// explicit SET CONFIG) are left alone so an operator override wins.
func (c *Config) ApplyProductionDefaults() {
	c.DeploymentProfile = ProfileProduction
	if c.DiskWatermarkCheckMS == 0 {
		c.DiskWatermarkCheckMS = ProductionDiskWatermarkCheckMS
	}
	if c.ReplicaLagCheckMS == 0 {
		c.ReplicaLagCheckMS = ProductionReplicaLagCheckMS
	}
	if c.StatementTimeoutMS == 0 {
		c.StatementTimeoutMS = ProductionStatementTimeoutMS
	}
	if c.IdleTimeoutMS == 0 {
		c.IdleTimeoutMS = ProductionIdleTimeoutMS
	}
	if c.IdleTransactionTimeoutMS == 0 {
		c.IdleTransactionTimeoutMS = ProductionIdleTransactionTimeoutMS
	}
	if c.LockTimeoutMS == 0 {
		c.LockTimeoutMS = ProductionLockTimeoutMS
	}
	if c.MaxConnections == 0 {
		c.MaxConnections = ProductionMaxConnections
	}
	if c.MaxConnectionsPerUser == 0 {
		c.MaxConnectionsPerUser = ProductionMaxConnectionsPerUser
	}
	if c.DrainTimeoutMS == 0 {
		c.DrainTimeoutMS = DefaultDrainTimeoutMS
	}
}

// CheckProduction is the live-production preflight nextsqld runs before
// opening the store when deployment_profile=production (or --production).
// It fails closed: a production server that cannot satisfy these checks
// does not start. Encryption itself is already mandatory (a key file or
// --require-client-key); this additionally refuses a key on the data
// volume and an unbounded resource-governance posture.
func (c Config) CheckProduction() error {
	if !c.IsProduction() {
		return nil
	}
	if !c.RequireClientKey && strings.TrimSpace(c.KeyFile) == "" && strings.TrimSpace(c.InstanceKeyFile) == "" {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires --key-file (or --instance-key-file), or --require-client-key")
	}
	if c.KeyFile != "" && KeyOnDataVolume(c.DataDir, c.KeyFile) {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires the unlock key file to be kept off the data volume")
	}
	if inst := c.InstanceRootFile(); inst != "" && KeyOnDataVolume(c.DataDir, inst) {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires the instance key file to be kept off the data volume")
	}
	if c.DiskWatermarkCheckMS <= 0 {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires disk_watermark_check_ms > 0")
	}
	if c.DrainTimeoutMS <= 0 {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires shutdown_drain_ms > 0")
	}
	if c.StatementTimeoutMS <= 0 {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires statement_timeout_ms > 0")
	}
	if c.IdleTimeoutMS <= 0 {
		return nerr.New(nerr.InvalidArgument, "config.CheckProduction",
			"production profile requires idle_timeout_ms > 0")
	}
	return nil
}

// KeyOnDataVolume reports whether keyFile resolves inside dataDir. Relative
// paths are evaluated from the process working directory. A failure to
// resolve either path is treated as "not on the data volume" so a missing
// path does not itself fail production start — CheckProduction has separate
// required-key checks.
func KeyOnDataVolume(dataDir, keyFile string) bool {
	dataDir = strings.TrimSpace(dataDir)
	keyFile = strings.TrimSpace(keyFile)
	if dataDir == "" || keyFile == "" {
		return false
	}
	absData, err := filepath.Abs(dataDir)
	if err != nil {
		return false
	}
	absKey, err := filepath.Abs(keyFile)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absData, absKey)
	if err != nil {
		return false
	}
	rel = filepath.ToSlash(rel)
	return rel == "." || (!strings.HasPrefix(rel, "../") && rel != "..")
}
