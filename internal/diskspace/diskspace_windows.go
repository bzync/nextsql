//go:build windows

package diskspace

import (
	"golang.org/x/sys/windows"

	"github.com/bzync/nextsql/internal/nerr"
)

func stat(path string) (total, free uint64, err error) {
	ptr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, 0, nerr.Wrap(nerr.InvalidArgument, "diskspace.Stat", "path", err)
	}
	var freeAvail, totalBytes, totalFree uint64
	if err := windows.GetDiskFreeSpaceEx(ptr, &freeAvail, &totalBytes, &totalFree); err != nil {
		return 0, 0, nerr.Wrap(nerr.IO, "diskspace.Stat", "GetDiskFreeSpaceEx", err)
	}
	return totalBytes, freeAvail, nil
}
