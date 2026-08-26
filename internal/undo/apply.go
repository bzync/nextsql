package undo

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/file"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/storage/row"
)

// Apply walks newest→oldest undo records for each uncommitted txn and
// restores the previous row version on the data file. Missing pages are
// ignored (no-steal: those writes were never flushed).
func Apply(fm *file.Manager, lg *Log, uncommitted []format.TxnID) error {
	if fm == nil || lg == nil {
		return nerr.New(nerr.InvalidArgument, "undo.Apply", "nil file or undo log")
	}
	for _, tid := range uncommitted {
		for _, rec := range lg.Chain(lg.Head(tid)) {
			if err := applyOne(fm, rec); err != nil {
				return err
			}
		}
	}
	return nil
}

func applyOne(fm *file.Manager, rec Record) error {
	if rec.PageID == 0 {
		return nil
	}
	raw, err := fm.ReadLogical(rec.PageID)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) || nerr.HasCode(err, nerr.InvalidFormat) {
			return nil
		}
		return err
	}
	p, err := page.Parse(raw)
	if err != nil {
		return nil
	}
	if p.Type() != format.PageTypeBTreeLeaf && p.Type() != format.PageTypeSlotted {
		return nil
	}
	changed := false
	n := p.SlotCount()
	for i := 0; i < n; i++ {
		view, err := p.GetView(uint16(i))
		if err != nil {
			continue
		}
		k, v, ok := decodeLeafKV(view)
		if !ok || string(k) != string(rec.Key) {
			continue
		}
		ver, has, err := row.Decode(v)
		if err != nil {
			return err
		}
		switch rec.Kind {
		case KindInsert:
			if !has || ver.Xmin == rec.Txn {
				if err := p.Delete(uint16(i)); err != nil {
					return err
				}
				changed = true
			}
		case KindUpdate, KindDelete:
			if !has || ver.Xmin == rec.Txn || ver.Xmax == rec.Txn {
				neu := row.Encode(rec.Old)
				if rec.Old.Xmin == 0 && rec.Old.Payload == nil && rec.Kind == KindDelete {
					// restore unwrapped legacy value if that is what we saved
					neu = rec.Old.Payload
				}
				if rec.Old.Xmin == 0 && len(rec.Old.Payload) > 0 && !has {
					neu = rec.Old.Payload
				}
				if err := replaceLeafValue(p, uint16(i), k, neu); err != nil {
					return err
				}
				changed = true
			}
		}
		break
	}
	if !changed {
		return nil
	}
	p.Finalize()
	return fm.WriteLogical(rec.PageID, p.Bytes())
}

func decodeLeafKV(rec []byte) (key, value []byte, ok bool) {
	if len(rec) < 4 {
		return nil, nil, false
	}
	klen := int(encoding.U16(rec, 0))
	vlen := int(encoding.U16(rec, 2))
	if klen < 1 || 4+klen+vlen != len(rec) {
		return nil, nil, false
	}
	return rec[4 : 4+klen], rec[4+klen:], true
}

func replaceLeafValue(p *page.Page, slot uint16, key, value []byte) error {
	buf := make([]byte, 4+len(key)+len(value))
	encoding.PutU16(buf, 0, uint16(len(key)))
	encoding.PutU16(buf, 2, uint16(len(value)))
	copy(buf[4:], key)
	copy(buf[4+len(key):], value)
	return p.Update(slot, buf)
}
