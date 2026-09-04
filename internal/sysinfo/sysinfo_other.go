//go:build !linux

package sysinfo

// totalRAMBytes has no portable probe outside Linux yet; callers treat 0 as
// "undetected" and fall back to a conservative default.
func totalRAMBytes() uint64 { return 0 }

// filesystemType is Linux-only for now ("" means undetected).
func filesystemType(string) string { return "" }
