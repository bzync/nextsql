package xport

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

// Options configure Export.
type Options struct {
	BufferPages int
	Crash       *Injector
	// SkipImportTest still authenticates the sealed payload. An export is
	// not valid until Verify (including an import test) succeeds; Export
	// always import-tests unless this is set for crash-path unit tests.
	SkipImportTest bool
	// Root unlocks the export keystore on Import / Verify. Export copies
	// the source keystore and does not store the root.
	Root *crypto.DEK
}

// Result is published export metadata. It contains no key material.
type Result struct {
	Path       string
	Header     Header
	Tables     int
	Rows       uint64
	Verified   bool
	ImportTest bool
}

// Export writes an encrypted logical dump of dataDir to dest. dest is
// published only after integrity checks (and an import test) succeed.
func Export(dataDir, dest string, keys crypto.KeyProvider, opt Options) (*Result, error) {
	if dataDir == "" || dest == "" {
		return nil, nerr.New(nerr.InvalidArgument, "xport.Export", "data directory and destination are required")
	}
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "xport.Export", "nil key provider")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 16
	}
	if _, err := os.Stat(dest); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "xport.Export", "export destination exists")
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	if _, err := os.Stat(dbPath); err != nil {
		return nil, nerr.Wrap(nerr.NotFound, "xport.Export", "data file", err)
	}

	db, err := executor.Open(dbPath, keys, opt.BufferPages)
	if err != nil {
		return nil, err
	}
	ident := db.Eng.Identity()
	tables := db.Cat.List()
	sort.Slice(tables, func(i, j int) bool { return tables[i].Name < tables[j].Name })

	dek, wrap, err := newExportDEK(keys)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	id, err := format.NewUUID()
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	hdr := Header{
		Version:     CurrentVersion,
		Suite:       format.CipherAES256GCM,
		KeyVersion:  dek.Version,
		Identity:    ident,
		CreatedNano: time.Now().UnixNano(),
		ExportID:    id,
		NonceHigh:   1,
		WrappedDEK:  wrap,
	}

	partial := dest + partialSuffix
	_ = os.RemoveAll(partial)
	if err := os.MkdirAll(partial, 0o700); err != nil {
		_ = db.Close()
		return nil, nerr.Wrap(nerr.IO, "xport.Export", "mkdir", err)
	}
	published := false
	defer func() {
		if !published {
			_ = os.RemoveAll(partial)
		}
	}()

	if err := hit(opt.Crash, PointBeforeWrite); err != nil {
		_ = db.Close()
		return nil, err
	}

	se, err := newSealer(filepath.Join(partial, payloadName), dek, 1)
	if err != nil {
		_ = db.Close()
		return nil, err
	}
	var rows uint64
	sess := db.Session()
	for _, tab := range tables {
		if err := hit(opt.Crash, PointDuringWrite); err != nil {
			_ = se.out.Close()
			_ = db.Close()
			return nil, err
		}
		rec, err := encodeTableRec(tab)
		if err != nil {
			_ = se.out.Close()
			_ = db.Close()
			return nil, err
		}
		if err := se.Write(rec); err != nil {
			_ = se.out.Close()
			_ = db.Close()
			return nil, err
		}
		err = sess.ForEachVisible(tab.Name, func(row []types.Value) error {
			rrec, err := encodeRowRec(tab.Name, row)
			if err != nil {
				return err
			}
			if err := se.Write(rrec); err != nil {
				return err
			}
			rows++
			return nil
		})
		if err != nil {
			_ = se.out.Close()
			_ = db.Close()
			return nil, err
		}
	}
	if err := db.Close(); err != nil {
		_ = se.out.Close()
		return nil, err
	}
	plain, sealed, sum, err := se.Close()
	if err != nil {
		return nil, err
	}
	hdr.TableCount = uint32(len(tables))
	hdr.RowCount = rows
	hdr.PlainSize = plain
	hdr.SealedSize = sealed
	hdr.PayloadSHA = sum

	if ks := crypto.KeystorePath(dbPath); fileExists(ks) {
		if err := copyFile(ks, filepath.Join(partial, keystoreName)); err != nil {
			return nil, err
		}
	}

	raw, err := encodeHeader(hdr)
	if err != nil {
		return nil, err
	}
	if err := writeAtomic(filepath.Join(partial, headerName), raw); err != nil {
		return nil, err
	}

	if err := hit(opt.Crash, PointBeforeVerify); err != nil {
		return nil, err
	}

	if !opt.SkipImportTest {
		if err := verifyDir(partial, keys, opt.Root, true); err != nil {
			return nil, err
		}
	} else if err := verifyDir(partial, keys, opt.Root, false); err != nil {
		return nil, err
	}

	if err := writeAtomic(filepath.Join(partial, verifiedName), []byte("ok\n")); err != nil {
		return nil, err
	}
	if err := os.Rename(partial, dest); err != nil {
		return nil, nerr.Wrap(nerr.IO, "xport.Export", "publish", err)
	}
	published = true
	return &Result{
		Path:       dest,
		Header:     hdr,
		Tables:     len(tables),
		Rows:       rows,
		Verified:   true,
		ImportTest: !opt.SkipImportTest,
	}, nil
}

func newExportDEK(keys crypto.KeyProvider) (*crypto.DEK, []byte, error) {
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

func unwrapExportDEK(keys crypto.KeyProvider, wrap []byte) (*crypto.DEK, error) {
	parent, err := crypto.WrapParent(keys)
	if err != nil {
		return nil, err
	}
	return crypto.UnwrapDEK(parent, wrap, crypto.DomainBackup)
}

func writeAtomic(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "xport.writeAtomic", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "xport.writeAtomic", "rename", err)
	}
	return nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func copyFile(src, dst string) error {
	in, err := os.ReadFile(src)
	if err != nil {
		return nerr.Wrap(nerr.IO, "xport.copyFile", "read", err)
	}
	if err := os.WriteFile(dst, in, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "xport.copyFile", "write", err)
	}
	return nil
}

// OpenKeys unlocks the export keystore sidecar with the external root.
// The sidecar holds wrapped DEKs only, never the raw root.
func OpenKeys(dir string, root *crypto.DEK) (crypto.KeyProvider, *crypto.Envelope, error) {
	if root == nil {
		return nil, nil, nerr.New(nerr.InvalidArgument, "xport.OpenKeys", "nil root")
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

// ReadHeader loads the plaintext export header. It does not decrypt the payload.
func ReadHeader(dir string) (Header, error) {
	raw, err := os.ReadFile(filepath.Join(dir, headerName))
	if err != nil {
		return Header{}, nerr.Wrap(nerr.IO, "xport.ReadHeader", "read", err)
	}
	return decodeHeader(raw)
}
