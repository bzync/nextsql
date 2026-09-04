// Package sysinfo reports a point-in-time snapshot of the host's compute and
// storage capacity — CPU count, physical RAM, and the free/total space and
// filesystem type of the volume that will hold a data directory.
//
// It exists for the installer/automation surface (P28): `nextsql setup` uses
// it to size a buffer pool from a resource preset and to warn about an
// undersized or unsuitable data volume before any mutation. It is advisory
// only — nothing in the engine's hot path consults it, and an undetected
// value (RAM 0, filesystem "") is reported as unknown rather than guessed.
package sysinfo

import (
	"os"
	"path/filepath"
	"runtime"

	"github.com/bzync/nextsql/internal/diskspace"
	"github.com/bzync/nextsql/internal/nerr"
)

// Info is a hardware/capacity snapshot. Zero values mean "not detected on
// this platform" (RAMBytes == 0, Filesystem == "") rather than "zero".
type Info struct {
	GOOS       string `json:"goos"`
	GOARCH     string `json:"goarch"`
	NumCPU     int    `json:"num_cpu"`
	GOMAXPROCS int    `json:"gomaxprocs"`
	// RAMBytes is total physical memory. 0 when the platform has no
	// supported probe.
	RAMBytes uint64 `json:"ram_bytes"`
	// MeasuredPath is the path the disk figures below were measured
	// against — the requested data directory, or its nearest existing
	// ancestor when it does not exist yet.
	MeasuredPath   string `json:"measured_path"`
	DiskTotalBytes uint64 `json:"disk_total_bytes"`
	DiskFreeBytes  uint64 `json:"disk_free_bytes"`
	// Filesystem is the volume's filesystem type (e.g. "ext4", "xfs",
	// "zfs", "tmpfs"). "" when undetected.
	Filesystem string `json:"filesystem"`
}

// Detect gathers an Info for the volume that will hold dataDir. dataDir need
// not exist yet: the disk and filesystem figures are taken from its nearest
// existing ancestor. An empty dataDir measures the current directory.
func Detect(dataDir string) (Info, error) {
	if dataDir == "" {
		dataDir = "."
	}
	probe, err := nearestExisting(dataDir)
	if err != nil {
		return Info{}, err
	}
	info := Info{
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		NumCPU:       runtime.NumCPU(),
		GOMAXPROCS:   runtime.GOMAXPROCS(0),
		RAMBytes:     totalRAMBytes(),
		MeasuredPath: probe,
		Filesystem:   filesystemType(probe),
	}
	usage, err := diskspace.Stat(probe)
	if err != nil {
		return Info{}, nerr.Wrap(nerr.IO, "sysinfo.Detect", "disk", err)
	}
	info.DiskTotalBytes = usage.TotalBytes
	info.DiskFreeBytes = usage.FreeBytes
	return info, nil
}

// nearestExisting walks up from path until it finds a directory that exists,
// returning an absolute path. It errors only if even the filesystem root is
// somehow unreadable.
func nearestExisting(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", nerr.Wrap(nerr.InvalidArgument, "sysinfo.Detect", "path", err)
	}
	probe := abs
	for {
		if _, err := os.Stat(probe); err == nil {
			return probe, nil
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			return probe, nil
		}
		probe = parent
	}
}
