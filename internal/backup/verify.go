package backup

import (
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
)

// Verify checks the backup inventory, decrypts the manifest, and optionally
// restore-tests into a temporary directory. A successful upload is not a
// valid backup; this function is the gate.
func Verify(src string, keys crypto.KeyProvider, restoreTest bool) error {
	if src == "" {
		return nerr.New(nerr.InvalidArgument, "backup.Verify", "source is required")
	}
	if keys == nil {
		return nerr.New(nerr.InvalidArgument, "backup.Verify", "nil key provider")
	}
	return verifyDir(src, keys, restoreTest)
}

func verifyDir(src string, keys crypto.KeyProvider, restoreTest bool) error {
	hdr, mf, dek, err := loadVerified(src, keys)
	if err != nil {
		return err
	}
	_ = hdr
	if !restoreTest {
		return nil
	}
	tmp, err := os.MkdirTemp("", "nextsql-restore-test-")
	if err != nil {
		return nerr.Wrap(nerr.IO, "backup.Verify", "temp", err)
	}
	defer os.RemoveAll(tmp)

	// Restore without requiring the verified marker (we are creating it).
	dbPath := filepath.Join(tmp, "nextsql.db")
	wdir := dbPath + ".wal"
	udir := dbPath + ".undo"
	if err := os.MkdirAll(wdir, 0o700); err != nil {
		return nerr.Wrap(nerr.IO, "backup.Verify", "mkdir wal", err)
	}
	if err := os.MkdirAll(udir, 0o700); err != nil {
		return nerr.Wrap(nerr.IO, "backup.Verify", "mkdir undo", err)
	}
	for _, mem := range mf.Members {
		srcMem := filepath.Join(src, memberDirName, mem.Name)
		dst, err := destFor(mem, tmp, dbPath, wdir, udir)
		if err != nil {
			return err
		}
		if _, err := openMember(dek, mem.Name, srcMem, dst); err != nil {
			return err
		}
	}
	eng, err := storage.Open(dbPath, keys, 8)
	if err != nil {
		return nerr.Wrap(nerr.Corruption, "backup.Verify", "restore test failed to open", err)
	}
	if err := eng.Close(); err != nil {
		return err
	}
	return nil
}
