// Package diskspace reports total/free bytes for the filesystem holding a
// path — the underlying volume, not NextSQL's own page allocator (see
// storage.Engine.SetStorageCapBytes/StorageCapBytes for that, a logical,
// per-database cap independent of the physical disk).
package diskspace

// Usage is a point-in-time filesystem capacity snapshot.
type Usage struct {
	TotalBytes uint64
	FreeBytes  uint64
}

// UsedFraction returns the fraction of the filesystem in use, in [0, 1].
// 0 total bytes (an unreadable or zero-sized filesystem) reports 0 rather
// than dividing by zero.
func (u Usage) UsedFraction() float64 {
	if u.TotalBytes == 0 {
		return 0
	}
	used := u.TotalBytes - u.FreeBytes
	return float64(used) / float64(u.TotalBytes)
}

// Stat reports total and free bytes for the filesystem containing path.
// Free is space available to this process (not reserved for privileged
// use), matching how an operator's own "df" reasoning about headroom
// works. Implemented per-OS: see diskspace_unix.go / diskspace_windows.go.
func Stat(path string) (Usage, error) {
	total, free, err := stat(path)
	if err != nil {
		return Usage{}, err
	}
	return Usage{TotalBytes: total, FreeBytes: free}, nil
}
