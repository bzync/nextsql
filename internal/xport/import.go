package xport

import (
	"context"
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// ImportOptions select dest open/create behaviour.
type ImportOptions struct {
	BufferPages int
	// Root unlocks the export keystore. Required when the export has a keystore.
	Root *crypto.DEK
}

// ImportResult is the imported dest location. No key material.
type ImportResult struct {
	DataDir string
	Header  Header
	Tables  int
	Rows    uint64
}

// Import materializes a verified logical export into destDir. Tables are
// recreated with CREATE TABLE / INSERT / CREATE INDEX so dest pages are
// encrypted under destKeys, not replayed as source ciphertext.
func Import(src, destDir string, destKeys crypto.KeyProvider, opt ImportOptions) (*ImportResult, error) {
	if src == "" || destDir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "xport.Import", "source and destination are required")
	}
	if destKeys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "xport.Import", "nil dest key provider")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 16
	}
	if _, err := os.Stat(filepath.Join(src, verifiedName)); err != nil {
		return nil, nerr.New(nerr.Corruption, "xport.Import", "export is not verified; a successful write is not a valid export")
	}

	hdr, dumps, err := loadDump(src, destKeys, opt.Root)
	if err != nil {
		return nil, err
	}

	db, created, err := openOrCreateDest(destDir, destKeys, opt.BufferPages)
	if err != nil {
		return nil, err
	}
	ok := false
	defer func() {
		_ = db.Close()
		if !ok && created {
			_ = os.RemoveAll(destDir)
		}
	}()

	n, err := applyDump(db, dumps)
	if err != nil {
		return nil, err
	}
	if err := db.Close(); err != nil {
		return nil, err
	}
	ok = true
	return &ImportResult{
		DataDir: destDir,
		Header:  hdr,
		Tables:  len(dumps),
		Rows:    n,
	}, nil
}

func openOrCreateDest(destDir string, keys crypto.KeyProvider, bufferPages int) (*executor.DB, bool, error) {
	dbPath := filepath.Join(destDir, config.DataFileName)
	if _, err := os.Stat(dbPath); err == nil {
		db, err := executor.Open(dbPath, keys, bufferPages)
		return db, false, err
	}
	if err := os.MkdirAll(destDir, 0o700); err != nil {
		return nil, false, nerr.Wrap(nerr.IO, "xport.openOrCreateDest", "mkdir", err)
	}
	if env, ok := keys.(*crypto.Envelope); ok {
		db, err := executor.CreateWithIdentity(dbPath, envIdent(env), env, bufferPages)
		return db, true, err
	}
	db, err := executor.Create(dbPath, keys, bufferPages)
	return db, true, err
}

func envIdent(env *crypto.Envelope) format.Identity {
	if env == nil {
		var z format.Identity
		return z
	}
	return env.Identity()
}

func applyDump(db *executor.DB, dumps []tableDump) (uint64, error) {
	s := db.Session()
	if _, err := s.Exec("BEGIN"); err != nil {
		return 0, err
	}
	parents := make(map[string]*catalog.Table, len(dumps))
	for i := range dumps {
		parents[dumps[i].Table.Name] = dumps[i].Table
	}
	ordered, err := orderTablesByFK(dumps)
	if err != nil {
		_, _ = s.Exec("ROLLBACK")
		return 0, err
	}
	var rows uint64
	for _, d := range ordered {
		if _, ok := db.Cat.Get(d.Table.Name); ok {
			_, _ = s.Exec("ROLLBACK")
			return 0, nerr.New(nerr.AlreadyExists, "xport.applyDump", "table already exists")
		}
		ddl, err := createTableSQLWithParents(d.Table, parents)
		if err != nil {
			_, _ = s.Exec("ROLLBACK")
			return 0, err
		}
		if _, err := s.Exec(ddl); err != nil {
			_, _ = s.Exec("ROLLBACK")
			return 0, err
		}
		if len(d.Rows) > 0 {
			ins, err := insertSQL(d.Table)
			if err != nil {
				_, _ = s.Exec("ROLLBACK")
				return 0, err
			}
			for _, row := range d.Rows {
				params := make([]executor.Param, len(row))
				for i, v := range row {
					params[i] = executor.Param{Value: v}
				}
				if _, err := s.ExecContext(context.Background(), ins, params); err != nil {
					_, _ = s.Exec("ROLLBACK")
					return 0, err
				}
				rows++
			}
		}
		for _, idx := range d.Table.Indexes {
			ix, err := createIndexSQL(d.Table, idx)
			if err != nil {
				_, _ = s.Exec("ROLLBACK")
				return 0, err
			}
			if _, err := s.Exec(ix); err != nil {
				_, _ = s.Exec("ROLLBACK")
				return 0, err
			}
		}
	}
	if _, err := s.Exec("COMMIT"); err != nil {
		return 0, err
	}
	return rows, nil
}

func orderTablesByFK(dumps []tableDump) ([]tableDump, error) {
	byName := make(map[string]tableDump, len(dumps))
	indeg := make(map[string]int, len(dumps))
	outs := make(map[string][]string, len(dumps))
	for _, d := range dumps {
		if d.Table == nil {
			return nil, nerr.New(nerr.InvalidFormat, "xport.applyDump", "invalid table")
		}
		byName[d.Table.Name] = d
		indeg[d.Table.Name] = 0
	}
	for _, d := range dumps {
		seen := make(map[string]struct{})
		for _, fk := range d.Table.ForeignKeys {
			p := fk.RefTable
			if p == "" || p == d.Table.Name {
				continue
			}
			if _, ok := byName[p]; !ok {
				continue
			}
			if _, dup := seen[p]; dup {
				continue
			}
			seen[p] = struct{}{}
			outs[p] = append(outs[p], d.Table.Name)
			indeg[d.Table.Name]++
		}
	}
	var q []string
	for _, d := range dumps {
		if indeg[d.Table.Name] == 0 {
			q = append(q, d.Table.Name)
		}
	}
	out := make([]tableDump, 0, len(dumps))
	for len(q) > 0 {
		n := q[0]
		q = q[1:]
		out = append(out, byName[n])
		for _, c := range outs[n] {
			indeg[c]--
			if indeg[c] == 0 {
				q = append(q, c)
			}
		}
	}
	if len(out) != len(dumps) {
		return nil, nerr.New(nerr.InvalidFormat, "xport.applyDump", "cyclic foreign keys")
	}
	return out, nil
}

func loadDump(src string, destKeys crypto.KeyProvider, root *crypto.DEK) (Header, []tableDump, error) {
	exportKeys, closer, err := openExportKeys(src, destKeys, root)
	if err != nil {
		return Header{}, nil, err
	}
	if closer != nil {
		defer closer()
	}
	hdr, err := ReadHeader(src)
	if err != nil {
		return Header{}, nil, err
	}
	sum, size, err := fileSHA256(filepath.Join(src, payloadName))
	if err != nil {
		return Header{}, nil, err
	}
	if uint64(size) != hdr.SealedSize || sum != hdr.PayloadSHA {
		return Header{}, nil, nerr.New(nerr.Corruption, "xport.loadDump", "payload hash mismatch")
	}
	dek, err := unwrapExportDEK(exportKeys, hdr.WrappedDEK)
	if err != nil {
		return Header{}, nil, err
	}
	plain, err := openPayload(dek, filepath.Join(src, payloadName))
	if err != nil {
		return Header{}, nil, err
	}
	if uint64(len(plain)) != hdr.PlainSize {
		return Header{}, nil, nerr.New(nerr.Corruption, "xport.loadDump", "plaintext size mismatch")
	}
	dumps, err := decodePayload(plain)
	if err != nil {
		return Header{}, nil, err
	}
	if uint32(len(dumps)) != hdr.TableCount {
		return Header{}, nil, nerr.New(nerr.Corruption, "xport.loadDump", "table count mismatch")
	}
	var rows uint64
	for _, d := range dumps {
		rows += uint64(len(d.Rows))
	}
	if rows != hdr.RowCount {
		return Header{}, nil, nerr.New(nerr.Corruption, "xport.loadDump", "row count mismatch")
	}
	return hdr, dumps, nil
}

func openExportKeys(src string, fallback crypto.KeyProvider, root *crypto.DEK) (crypto.KeyProvider, func(), error) {
	ks := filepath.Join(src, keystoreName)
	if fileExists(ks) && root != nil {
		env, err := crypto.OpenEnvelope(ks, root)
		if err != nil {
			return nil, nil, err
		}
		return env, func() { _ = env.Close() }, nil
	}
	if fallback != nil {
		return fallback, nil, nil
	}
	if fileExists(ks) {
		return nil, nil, nerr.New(nerr.InvalidArgument, "xport.openExportKeys", "root key required to open export keystore")
	}
	return nil, nil, nerr.New(nerr.InvalidArgument, "xport.openExportKeys", "nil key provider")
}
