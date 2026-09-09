//go:build linux

package file

import (
	"os"
	"syscall"
)

// allocatedBytes reports the blocks actually reserved for fi, distinguishing a
// real fallocate reservation from a merely sparse file. Reported as unsupported
// where the platform does not expose block counts.
func allocatedBytes(fi os.FileInfo) (int64, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return st.Blocks * 512, true
}
