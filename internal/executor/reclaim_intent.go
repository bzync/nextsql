package executor

import (
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const (
	reclaimMagic   uint32 = 0x4952534e // NSRI
	reclaimVersion uint16 = 1
	reclaimHeader         = 60
	maxReclaimIDs         = 10_000_000
)

func (db *DB) reclaimIntentPath() string { return db.path + ".reclaim" }

func (db *DB) writeReclaimIntent(ids []format.PageID) error {
	if len(ids) == 0 {
		return nil
	}
	dek, err := db.keys.Current()
	if err != nil {
		return err
	}
	id := db.Eng.Identity()
	hdr := make([]byte, reclaimHeader)
	encoding.PutU32(hdr, 0, reclaimMagic)
	encoding.PutU16(hdr, 4, reclaimVersion)
	encoding.PutU32(hdr, 8, uint32(dek.Version))
	copy(hdr[12:28], id.Database[:])
	copy(hdr[28:44], id.File[:])
	encoding.PutU32(hdr, 44, uint32(len(ids)))
	plain := make([]byte, 8*len(ids))
	for i, pageID := range ids {
		if err := pageID.UserData(); err != nil {
			return err
		}
		encoding.PutU64(plain, i*8, uint64(pageID))
	}
	nonce, ciphertext, err := crypto.SealBytesRandom(dek, hdr[:48], plain)
	if err != nil {
		return err
	}
	copy(hdr[48:60], nonce)
	path := db.reclaimIntentPath()
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nerr.Wrap(nerr.IO, "executor.reclaimIntent", "open temporary intent", err)
	}
	buf := append(hdr, ciphertext...)
	if _, err := f.Write(buf); err != nil {
		_ = f.Close()
		return nerr.Wrap(nerr.IO, "executor.reclaimIntent", "write intent", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nerr.Wrap(nerr.IO, "executor.reclaimIntent", "sync intent", err)
	}
	if err := f.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "executor.reclaimIntent", "close intent", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return nerr.Wrap(nerr.IO, "executor.reclaimIntent", "install intent", err)
	}
	return diskio.SyncDir(filepath.Dir(path))
}

func (db *DB) readReclaimIntent() ([]format.PageID, error) {
	buf, err := os.ReadFile(db.reclaimIntentPath())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "executor.reclaimIntent", "read intent", err)
	}
	return decodeReclaimIntent(buf, db.Eng.Identity(), db.keys)
}

func decodeReclaimIntent(buf []byte, id format.Identity, keys crypto.KeyProvider) ([]format.PageID, error) {
	if len(buf) < reclaimHeader || encoding.U32(buf, 0) != reclaimMagic || encoding.U16(buf, 4) != reclaimVersion {
		return nil, nerr.New(nerr.InvalidFormat, "executor.reclaimIntent", "invalid reclaim intent header")
	}
	count := int(encoding.U32(buf, 44))
	if count < 0 || count > maxReclaimIDs || len(buf) != reclaimHeader+count*8+format.AuthTagSize {
		return nil, nerr.New(nerr.InvalidFormat, "executor.reclaimIntent", "invalid reclaim intent length")
	}
	if string(buf[12:28]) != string(id.Database[:]) || string(buf[28:44]) != string(id.File[:]) {
		return nil, nerr.New(nerr.Corruption, "executor.reclaimIntent", "reclaim intent identity mismatch")
	}
	dek, err := keys.Key(format.KeyVersion(encoding.U32(buf, 8)))
	if err != nil {
		return nil, err
	}
	plain, err := crypto.OpenBytes(dek, buf[48:60], buf[:48], buf[60:])
	if err != nil {
		return nil, err
	}
	ids := make([]format.PageID, count)
	for i := range ids {
		ids[i] = format.PageID(encoding.U64(plain, i*8))
		if err := ids[i].UserData(); err != nil {
			return nil, err
		}
	}
	return ids, nil
}

func (db *DB) clearReclaimIntent() error {
	err := os.Remove(db.reclaimIntentPath())
	if err != nil && !os.IsNotExist(err) {
		return nerr.Wrap(nerr.IO, "executor.reclaimIntent", "remove intent", err)
	}
	return diskio.SyncDir(filepath.Dir(db.reclaimIntentPath()))
}

func (db *DB) replayReclaimIntent() error {
	ids, err := db.readReclaimIntent()
	if err != nil || len(ids) == 0 {
		return err
	}
	reachable := make(map[format.PageID]struct{})
	db.mu.RLock()
	trees := make([]interface {
		OwnedPages() ([]format.PageID, error)
	}, 0, 1+len(db.heaps)+len(db.vecs)+len(db.idxs))
	trees = append(trees, db.CatTree)
	for _, tr := range db.heaps {
		trees = append(trees, tr)
	}
	for _, tr := range db.vecs {
		trees = append(trees, tr)
	}
	for _, tr := range db.idxs {
		trees = append(trees, tr)
	}
	db.mu.RUnlock()
	for _, tr := range trees {
		pages, err := tr.OwnedPages()
		if err != nil {
			return err
		}
		for _, id := range pages {
			reachable[id] = struct{}{}
		}
	}
	free := make(map[format.PageID]struct{})
	for _, id := range db.Eng.Alloc.State().Free {
		free[id] = struct{}{}
	}
	var pending []format.PageID
	for _, id := range ids {
		if _, ok := reachable[id]; ok {
			return nerr.New(nerr.Corruption, "executor.reclaimIntent", "intent names a live page")
		}
		if _, ok := free[id]; !ok {
			pending = append(pending, id)
		}
	}
	if len(pending) > 0 {
		if err := db.Eng.ReclaimPages(pending); err != nil {
			return err
		}
	}
	return db.clearReclaimIntent()
}
