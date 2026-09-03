//go:build !windows

package diskspace

import (
	"golang.org/x/sys/unix"

	"github.com/bzync/nextsql/internal/nerr"
)

func stat(path string) (total, free uint64, err error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, nerr.Wrap(nerr.IO, "diskspace.Stat", "statfs", err)
	}
	total = uint64(st.Blocks) * uint64(st.Bsize)
	// Bavail (available to an unprivileged process), not Bfree (which
	// includes space the OS reserves for root) — matches what this
	// process could actually still write.
	free = uint64(st.Bavail) * uint64(st.Bsize)
	return total, free, nil
}
