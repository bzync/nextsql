//go:build !linux

package diskio

import "os"

// DataSync falls back to a full fsync where fdatasync is unavailable.
func DataSync(f *os.File) error { return Sync(f) }

// Preallocate is a no-op on platforms without fallocate.
func Preallocate(*os.File, int64) error { return nil }
