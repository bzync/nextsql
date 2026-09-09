package diskio

import (
	"io"
	"os"
	"sync/atomic"

	"github.com/bzync/nextsql/internal/nerr"
)

// Fault describes one attempted operation at the shared durable I/O
// boundary, so a test can fail the data file without failing the log, or
// fail a directory fsync without failing the writes inside it.
type Fault struct {
	// Op is "read", "write", "sync", "datasync" or "syncdir".
	Op string
	// Path is the file or directory the operation targets.
	Path string
	// Off is the byte offset of a positional read or write, -1 for a
	// sequential write at the file's current offset, and 0 for a barrier.
	Off int64
}

// ShortWrite, returned from a fault hook for a write, makes the seam write
// the first Consumed bytes and then report failure — a genuine partial
// write that leaves a torn record on disk, not a write rejected outright.
type ShortWrite struct {
	Consumed int
	Err      error
}

func (e *ShortWrite) Error() string {
	if e.Err == nil {
		return "short write"
	}
	return e.Err.Error()
}

func (e *ShortWrite) Unwrap() error { return e.Err }

type faultFn func(Fault) error

// fault is read on every durable read and write, so it is an atomic pointer
// rather than a mutex: an uninstalled seam costs one atomic load.
var fault atomic.Pointer[faultFn]

// SetFaultForTest installs a process-local test seam for the shared durable
// I/O boundary. Production code never installs it. The returned restore must
// be deferred so faults cannot leak between tests.
func SetFaultForTest(fn func(Fault) error) (restore func()) {
	var next *faultFn
	if fn != nil {
		f := faultFn(fn)
		next = &f
	}
	prev := fault.Swap(next)
	return func() { fault.Store(prev) }
}

func inject(op, path string, off int64) error {
	fn := fault.Load()
	if fn == nil {
		return nil
	}
	return (*fn)(Fault{Op: op, Path: path, Off: off})
}

func name(f *os.File) string {
	if f == nil {
		return ""
	}
	return f.Name()
}

// applyShortWrite performs the truncated write a *ShortWrite fault asks for,
// so the file really does hold a partial record. Any other injected error
// consumes nothing.
func applyShortWrite(f *os.File, buf []byte, off int64, err error) int {
	sw, ok := err.(*ShortWrite)
	if !ok || sw.Consumed <= 0 {
		return 0
	}
	n := sw.Consumed
	if n > len(buf) {
		n = len(buf)
	}
	written, _ := f.WriteAt(buf[:n], off)
	return written
}

func ReadFullAt(f *os.File, buf []byte, off int64) error {
	if err := inject("read", name(f), off); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.ReadFullAt", "injected read", err)
	}
	n, err := f.ReadAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil {
		err = io.ErrUnexpectedEOF
	}
	return nerr.Wrap(nerr.IO, "diskio.ReadFullAt", "short read", err)
}

func WriteFullAt(f *os.File, buf []byte, off int64) error {
	if err := inject("write", name(f), off); err != nil {
		applyShortWrite(f, buf, off, err)
		return nerr.Wrap(nerr.IO, "diskio.WriteFullAt", "injected write", err)
	}
	n, err := f.WriteAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil {
		err = io.ErrShortWrite
	}
	return nerr.Wrap(nerr.IO, "diskio.WriteFullAt", "short write", err)
}

// WriteAt performs a single positional write at the shared durable I/O
// boundary, returning the byte count so a caller that must distinguish a
// partial write from one that consumed nothing (the write-ahead log) can do
// so. An injected fault consumes nothing unless it is a *ShortWrite, which
// consumes the prefix it names.
func WriteAt(f *os.File, buf []byte, off int64) (int, error) {
	if err := inject("write", name(f), off); err != nil {
		n := applyShortWrite(f, buf, off, err)
		return n, nerr.Wrap(nerr.IO, "diskio.WriteAt", "injected write", err)
	}
	return f.WriteAt(buf, off)
}

// Write appends buf to f at its current offset, at the shared durable I/O
// boundary: the sequential form used by the backup and restore copiers, where
// a full destination device must surface as a failure rather than a truncated
// member file.
func Write(f *os.File, buf []byte) (int, error) {
	if err := inject("write", name(f), -1); err != nil {
		n := 0
		if sw, ok := err.(*ShortWrite); ok && sw.Consumed > 0 {
			n = sw.Consumed
			if n > len(buf) {
				n = len(buf)
			}
			n, _ = f.Write(buf[:n])
		}
		return n, nerr.Wrap(nerr.IO, "diskio.Write", "injected write", err)
	}
	n, err := f.Write(buf)
	if err != nil {
		return n, nerr.Wrap(nerr.IO, "diskio.Write", "write", err)
	}
	if n != len(buf) {
		return n, nerr.Wrap(nerr.IO, "diskio.Write", "short write", io.ErrShortWrite)
	}
	return n, nil
}

func Sync(f *os.File) error {
	if err := inject("sync", name(f), 0); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.Sync", "injected sync", err)
	}
	if err := f.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.Sync", "fsync", err)
	}
	return nil
}

func SyncDir(path string) error {
	if err := inject("syncdir", path, 0); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.SyncDir", "injected fsync", err)
	}
	d, err := os.Open(path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "diskio.SyncDir", "open", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.SyncDir", "fsync", err)
	}
	return nil
}
