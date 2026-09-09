//go:build !linux

package file

import "os"

func allocatedBytes(os.FileInfo) (int64, bool) { return 0, false }
