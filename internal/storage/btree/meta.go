package btree

import (
	"bytes"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	treeMetaMagic   = "NSTM"
	treeMetaVersion = 1
	treeMetaSize    = 4 + 2 + 8 + 2
)

func encodeTreeMeta(root format.PageID, height uint16) []byte {
	buf := make([]byte, treeMetaSize)
	copy(buf[0:4], treeMetaMagic)
	encoding.PutU16(buf, 4, treeMetaVersion)
	encoding.PutU64(buf, 6, uint64(root))
	encoding.PutU16(buf, 14, height)
	return buf
}

func decodeTreeMeta(rec []byte) (format.PageID, uint16, error) {
	if len(rec) < treeMetaSize {
		return 0, 0, nerr.New(nerr.InvalidFormat, "btree.decodeTreeMeta", "truncated tree meta")
	}
	if !bytes.Equal(rec[0:4], []byte(treeMetaMagic)) {
		return 0, 0, nerr.New(nerr.InvalidFormat, "btree.decodeTreeMeta", "bad tree meta magic")
	}
	if encoding.U16(rec, 4) != treeMetaVersion {
		return 0, 0, nerr.New(nerr.InvalidFormat, "btree.decodeTreeMeta", "unsupported tree meta version")
	}
	return format.PageID(encoding.U64(rec, 6)), encoding.U16(rec, 14), nil
}

func (t *Tree) writeMeta() error {
	h, err := t.eng.Pin(t.meta)
	if err != nil {
		return err
	}
	rec := encodeTreeMeta(t.root, uint16(t.height))
	p := h.Page()
	if p.SlotCount() == 0 {
		if _, err := p.Insert(rec); err != nil {
			_ = release(h, false)
			return err
		}
	} else if err := p.Update(0, rec); err != nil {
		_ = release(h, false)
		return err
	}
	return release(h, true)
}

func readTreeMeta(eng *storage.Engine, meta format.PageID) (format.PageID, uint16, error) {
	h, err := eng.Pin(meta)
	if err != nil {
		return 0, 0, err
	}
	rec, err := h.Page().Get(0)
	if err != nil {
		_ = release(h, false)
		return 0, 0, err
	}
	if err := release(h, false); err != nil {
		return 0, 0, err
	}
	return decodeTreeMeta(rec)
}
