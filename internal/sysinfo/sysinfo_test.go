package sysinfo

import (
	"path/filepath"
	"runtime"
	"testing"
)

func TestDetectRealHost(t *testing.T) {
	info, err := Detect(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info.NumCPU < 1 {
		t.Errorf("NumCPU = %d, want >= 1", info.NumCPU)
	}
	if info.GOOS != runtime.GOOS || info.GOARCH != runtime.GOARCH {
		t.Errorf("GOOS/GOARCH = %s/%s, want %s/%s", info.GOOS, info.GOARCH, runtime.GOOS, runtime.GOARCH)
	}
	if info.DiskTotalBytes == 0 {
		t.Error("DiskTotalBytes = 0 on a real filesystem")
	}
	if info.DiskFreeBytes > info.DiskTotalBytes {
		t.Errorf("DiskFreeBytes (%d) > DiskTotalBytes (%d)", info.DiskFreeBytes, info.DiskTotalBytes)
	}
	if runtime.GOOS == "linux" {
		if info.RAMBytes == 0 {
			t.Error("RAMBytes = 0 on Linux, expected /proc/meminfo to be readable")
		}
		if info.Filesystem == "" {
			t.Error("Filesystem empty on Linux, expected a mountinfo match")
		}
	}
}

func TestDetectNonExistentDataDirResolvesToAncestor(t *testing.T) {
	base := t.TempDir()
	deep := filepath.Join(base, "does", "not", "exist", "yet")

	info, err := Detect(deep)
	if err != nil {
		t.Fatal(err)
	}
	if info.DiskTotalBytes == 0 {
		t.Error("expected disk figures measured against an existing ancestor")
	}
	if info.MeasuredPath != base {
		t.Errorf("MeasuredPath = %q, want the nearest existing ancestor %q", info.MeasuredPath, base)
	}
}

func TestDetectEmptyPathMeasuresWorkingDir(t *testing.T) {
	info, err := Detect("")
	if err != nil {
		t.Fatal(err)
	}
	if info.MeasuredPath == "" || !filepath.IsAbs(info.MeasuredPath) {
		t.Errorf("MeasuredPath = %q, want an absolute path", info.MeasuredPath)
	}
}
