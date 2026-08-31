package hosting

import (
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/nerr"
)

const LockFileName = "nextsql.lock"

// DataDirLock is an advisory deployment lock. Every current nextsqld process
// and every hosting bootstrap/adoption command holds the same exclusive lock,
// so offline layout changes fail closed while the server is running.
type DataDirLock struct {
	file *os.File
}

// LockPath returns the stable advisory lock path. The file remains in place;
// the kernel lock, not file existence, determines ownership.
func LockPath(dataDir string) string { return filepath.Join(dataDir, LockFileName) }

// AcquireDataDirLock acquires the deployment lock without waiting.
func AcquireDataDirLock(dataDir string) (*DataDirLock, error) {
	if dataDir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "hosting.Lock", "data directory is required")
	}
	f, err := os.OpenFile(LockPath(dataDir), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.Lock", "open deployment lock", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "hosting.Lock", "chmod deployment lock", err)
	}
	busy, err := platformTryLock(f)
	if err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.IO, "hosting.Lock", "acquire deployment lock", err)
	}
	if busy {
		_ = f.Close()
		return nil, nerr.New(nerr.Unavailable, "hosting.Lock", "data directory is in use by another NextSQL process")
	}
	return &DataDirLock{file: f}, nil
}

// Close releases the deployment lock.
func (l *DataDirLock) Close() error {
	if l == nil || l.file == nil {
		return nil
	}
	f := l.file
	l.file = nil
	if err := platformUnlock(f); err != nil {
		_ = f.Close()
		return nerr.Wrap(nerr.IO, "hosting.Lock", "release deployment lock", err)
	}
	if err := f.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.Lock", "close deployment lock", err)
	}
	return nil
}
