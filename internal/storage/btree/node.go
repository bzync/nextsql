package btree

import (
	"bytes"
	"fmt"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

const (
	headerSlot    = 0
	headerVersion = 1
	headerSize    = 28

	// MaxKeySize keeps separators and keys well below a page.
	MaxKeySize = 2048
)

// maxPayload is the largest record that still fits with a node header.
// maxLeafRecord is half of that so a full leaf plus one insert can always split.
var (
	maxPayload    = format.LogicalPageSize - format.PageHeaderSize - headerSize - 2*format.SlotSize
	maxLeafRecord = (format.LogicalPageSize - format.PageHeaderSize - headerSize - 3*format.SlotSize) / 2
)

type nodeHeader struct {
	prev     format.PageID
	next     format.PageID
	leftmost format.PageID
}

type leafEntry struct {
	key   []byte
	value []byte
}

type internalEntry struct {
	key   []byte
	child format.PageID
}

func encodeHeader(h nodeHeader) []byte {
	buf := make([]byte, headerSize)
	buf[0] = headerVersion
	encoding.PutU64(buf, 4, uint64(h.prev))
	encoding.PutU64(buf, 12, uint64(h.next))
	encoding.PutU64(buf, 20, uint64(h.leftmost))
	return buf
}

func decodeHeader(rec []byte) (nodeHeader, error) {
	if len(rec) != headerSize {
		return nodeHeader{}, nerr.New(nerr.Corruption, "btree.decodeHeader", "wrong header size")
	}
	if rec[0] != headerVersion {
		return nodeHeader{}, nerr.New(nerr.InvalidFormat, "btree.decodeHeader", "unsupported node header version")
	}
	return nodeHeader{
		prev:     format.PageID(encoding.U64(rec, 4)),
		next:     format.PageID(encoding.U64(rec, 12)),
		leftmost: format.PageID(encoding.U64(rec, 20)),
	}, nil
}

func initNode(p *page.Page, h nodeHeader) error {
	if p.SlotCount() != 0 {
		return nerr.New(nerr.Internal, "btree.initNode", "page already has slots")
	}
	slot, err := p.Insert(encodeHeader(h))
	if err != nil {
		return err
	}
	if slot != headerSlot {
		return nerr.New(nerr.Internal, "btree.initNode", "node header is not slot 0")
	}
	return nil
}

func readHeader(p *page.Page) (nodeHeader, error) {
	rec, err := p.GetView(headerSlot)
	if err != nil {
		return nodeHeader{}, nerr.Wrap(nerr.Corruption, "btree.readHeader", "missing node header", err)
	}
	return decodeHeader(rec)
}

func writeHeader(p *page.Page, h nodeHeader) error {
	var buf [headerSize]byte
	buf[0] = headerVersion
	encoding.PutU64(buf[:], 4, uint64(h.prev))
	encoding.PutU64(buf[:], 12, uint64(h.next))
	encoding.PutU64(buf[:], 20, uint64(h.leftmost))
	return p.Update(headerSlot, buf[:])
}

// maxLeafKey returns a view of the greatest key on a leaf. The page must stay pinned.
func maxLeafKey(p *page.Page) ([]byte, error) {
	var max []byte
	err := forEachSlot(p, func(_ uint16, rec []byte) error {
		k, _, err := decodeLeaf(rec)
		if err != nil {
			return err
		}
		if max == nil || compare(k, max) > 0 {
			max = k
		}
		return nil
	})
	return max, err
}

// maxInternalKey returns a view of the greatest separator. The page must stay pinned.
func maxInternalKey(p *page.Page) ([]byte, error) {
	var max []byte
	err := forEachSlot(p, func(_ uint16, rec []byte) error {
		k, _, err := decodeInternal(rec)
		if err != nil {
			return err
		}
		if max == nil || compare(k, max) > 0 {
			max = k
		}
		return nil
	})
	return max, err
}

func encodeLeaf(key, value []byte) ([]byte, error) {
	return encodeLeafInto(nil, key, value)
}

func encodeLeafInto(buf, key, value []byte) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	n := 4 + len(key) + len(value)
	if n > maxLeafRecord {
		return nil, nerr.New(nerr.InvalidArgument, "btree.encodeLeaf", "record exceeds page capacity")
	}
	if cap(buf) < n {
		buf = make([]byte, n)
	} else {
		buf = buf[:n]
	}
	encoding.PutU16(buf, 0, uint16(len(key)))
	encoding.PutU16(buf, 2, uint16(len(value)))
	copy(buf[4:], key)
	copy(buf[4+len(key):], value)
	return buf, nil
}

func decodeLeaf(rec []byte) (key, value []byte, err error) {
	if len(rec) < 4 {
		return nil, nil, nerr.New(nerr.Corruption, "btree.decodeLeaf", "truncated leaf record")
	}
	klen := int(encoding.U16(rec, 0))
	vlen := int(encoding.U16(rec, 2))
	if klen < 1 || 4+klen+vlen != len(rec) {
		head := rec
		if len(head) > 16 {
			head = head[:16]
		}
		return nil, nil, nerr.New(nerr.Corruption, "btree.decodeLeaf",
			fmt.Sprintf("invalid leaf record length rec=%d klen=%d vlen=%d head=%x", len(rec), klen, vlen, head))
	}
	return rec[4 : 4+klen], rec[4+klen:], nil
}

func encodeInternal(key []byte, child format.PageID) ([]byte, error) {
	if err := checkKey(key); err != nil {
		return nil, err
	}
	if err := child.UserData(); err != nil {
		return nil, err
	}
	buf := make([]byte, 2+len(key)+8)
	encoding.PutU16(buf, 0, uint16(len(key)))
	copy(buf[2:], key)
	encoding.PutU64(buf, 2+len(key), uint64(child))
	if len(buf) > maxPayload {
		return nil, nerr.New(nerr.InvalidArgument, "btree.encodeInternal", "separator exceeds page capacity")
	}
	return buf, nil
}

func decodeInternal(rec []byte) (key []byte, child format.PageID, err error) {
	if len(rec) < 2+8 {
		return nil, 0, nerr.New(nerr.Corruption, "btree.decodeInternal", "truncated internal record")
	}
	klen := int(encoding.U16(rec, 0))
	if klen < 1 || 2+klen+8 != len(rec) {
		return nil, 0, nerr.New(nerr.Corruption, "btree.decodeInternal", "invalid internal record length")
	}
	child = format.PageID(encoding.U64(rec, 2+klen))
	if err := child.UserData(); err != nil {
		return nil, 0, nerr.Wrap(nerr.Corruption, "btree.decodeInternal", "child page id", err)
	}
	return rec[2 : 2+klen], child, nil
}

func checkKey(key []byte) error {
	if len(key) == 0 {
		return nerr.New(nerr.InvalidArgument, "btree.checkKey", "key must not be empty")
	}
	if len(key) > MaxKeySize {
		return nerr.New(nerr.InvalidArgument, "btree.checkKey", "key exceeds maximum size")
	}
	return nil
}

func compare(a, b []byte) int { return bytes.Compare(a, b) }

func copyBytes(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, len(b))
	copy(out, b)
	return out
}

func forEachSlot(p *page.Page, fn func(slot uint16, rec []byte) error) error {
	for i := 1; i < p.SlotCount(); i++ {
		rec, err := p.GetView(uint16(i))
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				continue
			}
			return err
		}
		if err := fn(uint16(i), rec); err != nil {
			return err
		}
	}
	return nil
}

func collectLeaves(p *page.Page) ([]leafEntry, error) {
	ents, err := collectLeafViews(p)
	if err != nil {
		return nil, err
	}
	for i := range ents {
		ents[i].key = copyBytes(ents[i].key)
		ents[i].value = copyBytes(ents[i].value)
	}
	return ents, nil
}

// collectLeafViews returns key/value slices that alias the page. The page
// must stay pinned until the caller is done with the entries.
func collectLeafViews(p *page.Page) ([]leafEntry, error) {
	var ents []leafEntry
	err := forEachSlot(p, func(slot uint16, rec []byte) error {
		k, v, err := decodeLeaf(rec)
		if err != nil {
			return nerr.Wrap(nerr.Corruption, "btree.collectLeafViews",
				fmt.Sprintf("page=%d type=%d slots=%d slot=%d", p.ID(), p.Type(), p.SlotCount(), slot), err)
		}
		ents = append(ents, leafEntry{key: k, value: v})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortLeaves(ents)
	return ents, nil
}

func collectInternals(p *page.Page) ([]internalEntry, error) {
	var ents []internalEntry
	err := forEachSlot(p, func(_ uint16, rec []byte) error {
		k, child, err := decodeInternal(rec)
		if err != nil {
			return err
		}
		ents = append(ents, internalEntry{key: copyBytes(k), child: child})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sortInternals(ents)
	return ents, nil
}

func sortLeaves(ents []leafEntry) {
	for i := 1; i < len(ents); i++ {
		j := i
		for j > 0 && compare(ents[j].key, ents[j-1].key) < 0 {
			ents[j], ents[j-1] = ents[j-1], ents[j]
			j--
		}
	}
}

func sortInternals(ents []internalEntry) {
	for i := 1; i < len(ents); i++ {
		j := i
		for j > 0 && compare(ents[j].key, ents[j-1].key) < 0 {
			ents[j], ents[j-1] = ents[j-1], ents[j]
			j--
		}
	}
}

func findLeaf(ents []leafEntry, key []byte) (int, bool) {
	for i, e := range ents {
		c := compare(e.key, key)
		if c == 0 {
			return i, true
		}
		if c > 0 {
			return i, false
		}
	}
	return len(ents), false
}

// findLeafSlot scans slots for key. value aliases the page and is only
// valid while the page stays pinned.
func findLeafSlot(p *page.Page, key []byte) (slot uint16, value []byte, found bool, err error) {
	err = forEachSlot(p, func(s uint16, rec []byte) error {
		k, v, decErr := decodeLeaf(rec)
		if decErr != nil {
			return decErr
		}
		if compare(k, key) == 0 {
			slot = s
			value = v
			found = true
		}
		return nil
	})
	return slot, value, found, err
}

func childForKey(hdr nodeHeader, ents []internalEntry, key []byte) format.PageID {
	child := hdr.leftmost
	for _, e := range ents {
		if compare(key, e.key) >= 0 {
			child = e.child
			continue
		}
		break
	}
	return child
}

func rebuildLeaf(id format.PageID, hdr nodeHeader, ents []leafEntry) (*page.Page, error) {
	p := page.New(id, format.PageTypeBTreeLeaf)
	if err := initNode(p, hdr); err != nil {
		return nil, err
	}
	for _, e := range ents {
		rec, err := encodeLeaf(e.key, e.value)
		if err != nil {
			return nil, err
		}
		if _, err := p.Insert(rec); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func rebuildInternal(id format.PageID, hdr nodeHeader, ents []internalEntry) (*page.Page, error) {
	p := page.New(id, format.PageTypeBTreeInternal)
	if err := initNode(p, hdr); err != nil {
		return nil, err
	}
	for _, e := range ents {
		rec, err := encodeInternal(e.key, e.child)
		if err != nil {
			return nil, err
		}
		if _, err := p.Insert(rec); err != nil {
			return nil, err
		}
	}
	return p, nil
}

func overwrite(dst *page.Page, src *page.Page) error {
	if dst.ID() != src.ID() {
		return nerr.New(nerr.Internal, "btree.overwrite", "page id mismatch")
	}
	copy(dst.Bytes(), src.Bytes())
	return dst.Validate()
}

func leafRecordSize(e leafEntry) int {
	return 4 + len(e.key) + len(e.value)
}

func leafPageBytes(ents []leafEntry) int {
	n := format.PageHeaderSize + format.SlotSize*(1+len(ents)) + headerSize
	for _, e := range ents {
		n += leafRecordSize(e)
	}
	return n
}

func leafFits(ents []leafEntry) bool {
	for _, e := range ents {
		if leafRecordSize(e) > maxLeafRecord {
			return false
		}
	}
	return leafPageBytes(ents) <= format.LogicalPageSize
}

func internalRecordSize(e internalEntry) int {
	return 2 + len(e.key) + 8
}

func internalPageBytes(ents []internalEntry) int {
	n := format.PageHeaderSize + format.SlotSize*(1+len(ents)) + headerSize
	for _, e := range ents {
		n += internalRecordSize(e)
	}
	return n
}

func internalFits(hdr nodeHeader, ents []internalEntry) bool {
	_ = hdr
	for _, e := range ents {
		if err := checkKey(e.key); err != nil {
			return false
		}
		if internalRecordSize(e) > maxPayload {
			return false
		}
	}
	return internalPageBytes(ents) <= format.LogicalPageSize
}
