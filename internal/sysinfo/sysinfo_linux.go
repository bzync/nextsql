//go:build linux

package sysinfo

import (
	"os"
	"strconv"
	"strings"
)

// totalRAMBytes reads MemTotal from /proc/meminfo. Returns 0 if unreadable.
func totalRAMBytes() uint64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}

// filesystemType resolves the filesystem of the mount that contains probe by
// finding the longest mount point prefix in /proc/self/mountinfo. Returns ""
// if it cannot be determined.
func filesystemType(probe string) string {
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return ""
	}
	bestLen := -1
	best := ""
	for _, line := range strings.Split(string(data), "\n") {
		sep := strings.Index(line, " - ")
		if sep < 0 {
			continue
		}
		left := strings.Fields(line[:sep])
		right := strings.Fields(line[sep+3:])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		mp := left[4]
		if mp != "/" && probe != mp && !strings.HasPrefix(probe, mp+"/") {
			continue
		}
		if mp == "/" && probe != "/" && !strings.HasPrefix(probe, "/") {
			continue
		}
		if len(mp) > bestLen {
			bestLen = len(mp)
			best = right[0]
		}
	}
	return best
}
