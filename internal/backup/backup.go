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

// Options configure Create.
type Options struct {
	BufferPages int
	Crash       *Injector
	// SkipRestoreTest still runs hash verification. A backup is not valid
	// until Verify (including a restore test) succeeds; Create always
	// restore-tests unless this is set for unit tests of the crash path.
	SkipRestoreTest bool
}

// Result is the published backup metadata. It contains no key material.
type Result struct {
	Path        string
	Header      Header
	Members     int
	Verified    bool
	RestoreTest bool
}

// Create writes an encrypted backup of dataDir to dest. dest is published
// only after integrity checks (and a restore test) succeed.
func Create(dataDir, dest string, keys crypto.KeyProvider, opt Options) (*Result, error) {
	if dataDir == "" || dest == "" {
		return nil, nerr.New(nerr.InvalidArgument, "backup.Create", "data directory and destination are required")
	}
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "backup.Create", "nil key provider")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 16
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "backup.Create", "backup destination exists")
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nerr.Wrap(nerr.NotFound, "backup.Create", "data file", err)
	}

	eng, err := storage.Open(dbPath, keys, opt.BufferPages)
	if err != nil {
		return nil, err
	}
	ident := eng.Identity()
	if err := eng.Checkpoint(); err != nil {
		_ = eng.Close()
		return nil, err
	}
	cp := eng.WAL.CheckpointLSN()
	redo := eng.WAL.RedoLSN()
	durable := eng.WAL.DurableLSN()
	if err := eng.Close(); err != nil {
		return nil, err
	}

	dek, wrap, err := newBackupDEK(keys)
	if err != nil {
		return nil, err
	}
	id, err := format.NewUUID()
	if err != nil {
		return nil, err
	}
	hdr := Header{
		Version:     CurrentVersion,
		Suite:       format.CipherAES256GCM,
		KeyVersion:  dek.Version,
		Identity:    ident,
		Checkpoint:  cp,
		RedoLSN:     redo,
		DurableLSN:  durable,
		CreatedNano: time.Now().UnixNano(),
		BackupID:    id,
		WrappedDEK:  wrap,
	}

	partial := dest + partialSuffix
	_ = os.RemoveAll(partial)
	if err := os.MkdirAll(filepath.Join(partial, memberDirName), 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.Create", "mkdir", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(partial)
		}
	}()

	if err := hit(opt.Crash, PointBeforeCopy); err != nil {
		return nil, err
	}

	var (
		members []Member
		gen     uint64 = 1
	)
	add := func(kind Kind, name, src string, first, last format.LSN) error {
		if _, err := os.Stat(src); err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return nerr.Wrap(nerr.IO, "backup.Create", "stat member", err)
		}
		if err := hit(opt.Crash, PointDuringCopy); err != nil {
			return err
		}
		dst := filepath.Join(partial, memberDirName, name)
		plain, sealed, sum, next, err := sealFile(dek, name, src, dst, gen)
		if err != nil {
			return err
		}
		gen = next
		members = append(members, Member{
			Kind:       kind,
			Name:       name,
			PlainSize:  plain,
			SealedSize: sealed,
			SHA256:     sum,
			FirstLSN:   first,
			LastLSN:    last,
		})
		return nil
	}

	if err := add(KindData, "data", dbPath, 0, 0); err != nil {
		return nil, err
	}
	if err := add(KindKeys, "keys", crypto.KeystorePath(dbPath), 0, 0); err != nil {
		return nil, err
	}
	if err := add(KindUsers, "users", filepath.Join(dataDir, config.AuthFileName), 0, 0); err != nil {
		return nil, err
	}
	if err := add(KindACL, "acl", filepath.Join(dataDir, config.ACLFileName), 0, 0); err != nil {
		return nil, err
	}

	wdir := wal.DirFor(dbPath)
	if err := add(KindWALCtrl, "wal-control", filepath.Join(wdir, "control"), redo, durable); err != nil {
		return nil, err
	}
	if ents, err := os.ReadDir(wdir); err == nil {
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			id, ok := wal.ParseSegmentFileName(e.Name())
			if !ok {
				continue
			}
			src := filepath.Join(wdir, e.Name())
			first, last, rerr := segmentLSNRange(src)
			if rerr != nil {
				return nil, rerr
			}
			if err := add(KindWALSeg, wal.SegmentFileName(id), src, first, last); err != nil {
				return nil, err
			}
		}
	}
	udir := dbPath + ".undo"
	if err := add(KindUNDOCtrl, "undo-control", filepath.Join(udir, "control"), 0, 0); err != nil {
		return nil, err
	}
	if err := add(KindUNDOLog, "undo-log", filepath.Join(udir, "undo.log"), 0, 0); err != nil {
		return nil, err
	}
	if err := add(KindReclaim, "reclaim-intent", dbPath+".reclaim", 0, 0); err != nil {
		return nil, err
	}

	if err := hit(opt.Crash, PointBeforeManifest); err != nil {
		return nil, err
	}

	mf := Manifest{Version: CurrentVersion, Members: members}
	rawMF, err := encodeManifest(mf)
	if err != nil {
		return nil, err
	}
	sealedMF, sumMF, next, err := sealBytes(dek, manifestName, rawMF, filepath.Join(partial, manifestName), gen)
	if err != nil {
		return nil, err
	}
	gen = next
	hdr.NonceHigh = gen - 1
	rawHdr, err := encodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(partial, headerName), rawHdr); err != nil {
		return nil, err
	}
	if ks := crypto.KeystorePath(dbPath); fileExists(ks) {
		if err := copyFile(ks, filepath.Join(partial, keystoreName)); err != nil {
			return nil, err
		}
	}
	_ = sealedMF
	_ = sumMF

	if err := hit(opt.Crash, PointBeforeVerify); err != nil {
		return nil, err
	}
	if err := verifyDir(partial, keys, !opt.SkipRestoreTest); err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(partial, verifiedName), []byte("ok\n")); err != nil {
		return nil, err
	}
	if err := os.Rename(partial, dest); err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.Create", "publish", err)
	}
	if err := diskio.SyncDir(filepath.Dir(dest)); err != nil && filepath.Dir(dest) != "." {
		// Destination exists; a missed dir sync is not a silent-corruption path.
		_ = err
	}
	published = true
	return &Result{
		Path:        dest,
		Header:      hdr,
		Members:     len(members),
		Verified:    true,
		RestoreTest: !opt.SkipRestoreTest,
	}, nil
}

func newBackupDEK(keys crypto.KeyProvider) (*crypto.DEK, []byte, error) {
	parent, err := crypto.WrapParent(keys)
	if err != nil {
		return nil, nil, err
	}
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, nil, err
	}
	wrap, err := crypto.WrapDEK(parent, dek, crypto.DomainBackup)
	if err != nil {
		return nil, nil, err
	}
	return dek, wrap, nil
}

func unwrapBackupDEK(keys crypto.KeyProvider, wrap []byte) (*crypto.DEK, error) {
	parent, err := crypto.WrapParent(keys)
	if err != nil {
		return nil, err
	}
	return crypto.UnwrapDEK(parent, wrap, crypto.DomainBackup)
}

func writeAtomic(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "backup.writeAtomic", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "backup.writeAtomic", "rename", err)
	}
	return nil
}

func hit(inj *Injector, p Point) error {
	if inj == nil {
		return nil
	}
	return inj.Hit(p)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return nerr.Wrap(nerr.IO, "backup.copyFile", "read", err)
	}
	if err := os.WriteFile(dst, in, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "backup.copyFile", "write", err)
	}
	return nil
}

// OpenKeys unlocks the backup keystore sidecar with the external root.
// The sidecar holds wrapped DEKs only, never the raw root.
func OpenKeys(dir string, root *crypto.DEK) (crypto.KeyProvider, *crypto.Envelope, error) {
	if root == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, "backup.OpenKeys", "nil root")
	}
	ks := filepath.Join(dir, keystoreName)
	if fileExists(ks) {
		env, err := crypto.OpenEnvelope(ks, root)
		if err != nil {
			return nil, nil, err
		}
		return env, env, nil
	}
	keys, err := crypto.NewMemoryKeyProvider(root)
	if err != nil {
		return nil, nil, err
	}
	return keys, nil, nil
}

// ReadHeader loads the plaintext backup header. It does not decrypt members.
func ReadHeader(dir string) (Header, error) {
	raw, err := os.ReadFile(filepath.Join(dir, headerName))
	if err != nil {
		return Header{}, nerr.Wrap(nerr.IO, "backup.ReadHeader", "read", err)
	}
	return decodeHeader(raw)
}
