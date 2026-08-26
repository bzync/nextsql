package backup

import (
	"os"
	"path/filepath"
	"time"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
	"github.com/bzync/nextsql/internal/wal"
)

// RestoreOptions select PITR bounds. At most one of UntilLSN / UntilTime is set.
type RestoreOptions struct {
	BufferPages int
	UntilLSN    format.LSN
	UntilTime   time.Time
	ArchiveDir  string
	// SkipOpen restores files only. The caller must open the engine to redo.
	SkipOpen bool
}

// RestoreResult is the restored data-dir location. No key material.
type RestoreResult struct {
	DataDir    string
	Header     Header
	UntilLSN   format.LSN
	Members    int
	ArchiveWAL int
}

// Restore materializes a verified backup into destDir (must not exist).
// A successful copy is not enough: Verify must have marked the backup.
func Restore(src, destDir string, keys crypto.KeyProvider, opt RestoreOptions) (*RestoreResult, error) {
	if src == "" || destDir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "backup.Restore", "source and destination are required")
	}
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "backup.Restore", "nil key provider")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 16
	}
	if _, err := os.Stat(destDir); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "backup.Restore", "destination exists")
	}
	if _, err := os.Stat(filepath.Join(src, verifiedName)); err != nil {
		return nil, nerr.New(nerr.Corruption, "backup.Restore", "backup is not verified; a successful write is not a valid backup")
	}

	hdr, mf, dek, err := loadVerified(src, keys)
	if err != nil {
		return nil, err
	}

	until := opt.UntilLSN
	if !opt.UntilTime.IsZero() {
		resolved, err := ResolveUntilTime(hdr, opt.ArchiveDir, opt.UntilTime)
		if err != nil {
			return nil, err
		}
		until = resolved
	}

	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.Restore", "mkdir", err)
	}
	ok := false
	defer func() {
		if !ok {
			_ = os.RemoveAll(destDir)
		}
	}()

	dbPath := filepath.Join(destDir, config.DataFileName)
	wdir := wal.DirFor(dbPath)
	udir := dbPath + ".undo"
	if err := os.MkdirAll(wdir, 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.Restore", "mkdir wal", err)
	}
	if err := os.MkdirAll(udir, 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.Restore", "mkdir undo", err)
	}

	for _, mem := range mf.Members {
		srcMem := filepath.Join(src, memberDirName, mem.Name)
		dst, err := destFor(mem, destDir, dbPath, wdir, udir)
		if err != nil {
			return nil, err
		}
		if _, err := openMember(dek, mem.Name, srcMem, dst); err != nil {
			return nil, err
		}
	}

	if err := applyArchive(opt.ArchiveDir, wdir, keys, until); err != nil {
		return nil, err
	}
	if err := diskio.SyncDir(destDir); err != nil {
		return nil, err
	}

	if !opt.SkipOpen {
		eng, err := storage.OpenWith(dbPath, keys, opt.BufferPages, storage.OpenOptions{UntilLSN: until})
		if err != nil {
			return nil, err
		}
		if err := eng.Close(); err != nil {
			return nil, err
		}
	}

	ok = true
	nArch := 0
	if opt.ArchiveDir != "" {
		if ents, _, err := readArchiveIndex(opt.ArchiveDir); err == nil {
			nArch = len(ents)
		}
	}
	return &RestoreResult{
		DataDir:    destDir,
		Header:     hdr,
		UntilLSN:   until,
		Members:    len(mf.Members),
		ArchiveWAL: nArch,
	}, nil
}

func destFor(mem Member, destDir, dbPath, wdir, udir string) (string, error) {
	switch mem.Kind {
	case KindData:
		return dbPath, nil
	case KindKeys:
		return crypto.KeystorePath(dbPath), nil
	case KindUsers:
		return filepath.Join(destDir, config.AuthFileName), nil
	case KindACL:
		return filepath.Join(destDir, config.ACLFileName), nil
	case KindWALCtrl:
		return filepath.Join(wdir, "control"), nil
	case KindWALSeg:
		return filepath.Join(wdir, mem.Name), nil
	case KindUNDOCtrl:
		return filepath.Join(udir, "control"), nil
	case KindUNDOLog:
		return filepath.Join(udir, "undo.log"), nil
	case KindReclaim:
		return dbPath + ".reclaim", nil
	default:
		return "", nerr.New(nerr.InvalidFormat, "backup.destFor", "unknown member kind")
	}
}

func loadVerified(src string, keys crypto.KeyProvider) (Header, Manifest, *crypto.DEK, error) {
	hdr, err := ReadHeader(src)
	if err != nil {
		return Header{}, Manifest{}, nil, err
	}
	dek, err := unwrapBackupDEK(keys, hdr.WrappedDEK)
	if err != nil {
		return Header{}, Manifest{}, nil, err
	}
	tmp, err := os.MkdirTemp("", "nextsql-manifest-")
	if err != nil {
		return Header{}, Manifest{}, nil, nerr.Wrap(nerr.IO, "backup.loadVerified", "temp", err)
	}
	defer os.RemoveAll(tmp)
	plainPath := filepath.Join(tmp, "manifest")
	if _, err := openMember(dek, manifestName, filepath.Join(src, manifestName), plainPath); err != nil {
		return Header{}, Manifest{}, nil, err
	}
	raw, err := os.ReadFile(plainPath)
	if err != nil {
		return Header{}, Manifest{}, nil, nerr.Wrap(nerr.IO, "backup.loadVerified", "read manifest", err)
	}
	mf, err := decodeManifest(raw)
	if err != nil {
		return Header{}, Manifest{}, nil, err
	}
	if err := checkMemberHashes(src, mf); err != nil {
		return Header{}, Manifest{}, nil, err
	}
	return hdr, mf, dek, nil
}

func checkMemberHashes(src string, mf Manifest) error {
	if len(mf.Members) == 0 {
		return nerr.New(nerr.Corruption, "backup.checkMemberHashes", "empty manifest")
	}
	for _, mem := range mf.Members {
		path := filepath.Join(src, memberDirName, mem.Name)
		sum, size, err := fileSHA256(path)
		if err != nil {
			return err
		}
		if sum != mem.SHA256 || uint64(size) != mem.SealedSize {
			return nerr.New(nerr.Corruption, "backup.checkMemberHashes", "member checksum mismatch")
		}
	}
	return nil
}
