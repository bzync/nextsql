//go:build unix

package profile

import (
	"io/fs"
	"os"
	"syscall"

	"github.com/bzync/nextsql/internal/nerr"
)

// checkOwnership refuses a profile file another local user could rewrite to
// redirect operators' sign-ins to a server of their choosing.
func checkOwnership(info fs.FileInfo) error {
	const op = "profile.Load"
	if info.Mode().Perm()&0o022 != 0 {
		return nerr.New(nerr.InvalidArgument, op,
			"profile file must not be writable by group or others (chmod go-w)")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if uid := uint32(os.Geteuid()); st.Uid != uid && st.Uid != 0 {
		return nerr.New(nerr.InvalidArgument, op,
			"profile file must be owned by the user running nextsql-admin (or root)")
	}
	return nil
}
