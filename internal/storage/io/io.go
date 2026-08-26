package diskio

import (
	"io"
	"os"

	"github.com/bzync/nextsql/internal/nerr"
)

func ReadFullAt(f *os.File, buf []byte, off int64) error {
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
	n, err := f.WriteAt(buf, off)
	if n == len(buf) {
		return nil
	}
	if err == nil {
		err = io.ErrShortWrite
	}
	return nerr.Wrap(nerr.IO, "diskio.WriteFullAt", "short write", err)
}

func Sync(f *os.File) error {
	if err := f.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "diskio.Sync", "fsync", err)
	}
	return nil
}

func SyncDir(path string) error {
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
