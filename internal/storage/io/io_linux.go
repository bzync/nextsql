//go:build linux

package diskio

import (
	"os"
	"syscall"

	"github.com/bzync/nextsql/internal/nerr"
)

// DataSync persists file data. On Linux this is fdatasync: size changes
// are included, atime/mtime are not. WAL durability only needs the bytes.
func DataSync(f *os.File) error {
	if f == nil {
		return nerr.New(nerr.IO, "diskio.DataSync", "nil file")
	}
	if err := syscall.Fdatasync(int(f.Fd())); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.DataSync", "fdatasync", err)
	}
	return nil
}

// Preallocate reserves size bytes so later WriteAt calls do not extend
// the file. Best-effort: unsupported filesystems are ignored.
func Preallocate(f *os.File, size int64) error {
	if f == nil || size <= 0 {
		return nil
	}
	_ = syscall.Fallocate(int(f.Fd()), 0, 0, size)
	return nil
}
