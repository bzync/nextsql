package page

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	offMagic   = 0
	offVersion = 4
	offType    = 6
	offPageID  = 8
	offLSN     = 16
	// LSNOffset is the byte offset of the page LSN field.
	LSNOffset    = offLSN
	offTxn       = 24
	offSlotCount = 32
	offLower     = 34
	offUpper     = 36
	offFlags     = 38
	offChecksum  = 40
	offReserved  = 44
)

// Page is a 16 KiB slotted page. buf is always format.LogicalPageSize.
type Page struct {
	buf []byte
}

func New(id format.PageID, typ format.PageType) *Page {
	buf := make([]byte, format.LogicalPageSize)
	p := &Page{buf: buf}
	p.initHeader(id, typ)
	return p
}

// NewIn initializes buf in place. buf must be exactly one logical page.
func NewIn(buf []byte, id format.PageID, typ format.PageType) (*Page, error) {
	if len(buf) != format.LogicalPageSize {
		return nil, nerr.New(nerr.InvalidArgument, "page.NewIn", "buffer is not a logical page")
	}
	p := &Page{buf: buf}
	p.initHeader(id, typ)
	return p, nil
}

func (p *Page) initHeader(id format.PageID, typ format.PageType) {
	clear(p.buf)
	format.PutMagic(p.buf, offMagic)
	encoding.PutU16(p.buf, offVersion, format.CurrentFormatVersion)
	encoding.PutU16(p.buf, offType, uint16(typ))
	encoding.PutU64(p.buf, offPageID, uint64(id))
	encoding.PutU16(p.buf, offLower, format.PageHeaderSize)
	encoding.PutU16(p.buf, offUpper, format.LogicalPageSize)
}

// Parse copies buf and validates the page.
func Parse(buf []byte) (*Page, error) {
	if len(buf) != format.LogicalPageSize {
		return nil, nerr.New(nerr.InvalidFormat, "page.Parse", "logical page has wrong size")
	}
	cp := make([]byte, format.LogicalPageSize)
	copy(cp, buf)
	p := &Page{buf: cp}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// Wrap aliases buf without copying. Caller owns lifetime.
func Wrap(buf []byte) (*Page, error) {
	if len(buf) != format.LogicalPageSize {
		return nil, nerr.New(nerr.InvalidFormat, "page.Wrap", "logical page has wrong size")
	}
	p := &Page{buf: buf}
	if err := p.Validate(); err != nil {
		return nil, err
	}
	return p, nil
}

// WrapCached aliases a buffer-pool frame that was already validated
// when first installed. Cache hits must not re-walk slots: a concurrent
// writer may be mutating the same bytes under a higher-level lock.
func WrapCached(buf []byte) (*Page, error) {
	if len(buf) != format.LogicalPageSize {
		return nil, nerr.New(nerr.InvalidFormat, "page.WrapCached", "logical page has wrong size")
	}
	return &Page{buf: buf}, nil
}

func ParseID(buf []byte, id format.PageID) (*Page, error) {
	p, err := Parse(buf)
	if err != nil {
		return nil, err
	}
	if p.ID() != id {
		return nil, nerr.New(nerr.Corruption, "page.ParseID", "page id mismatch")
	}
	return p, nil
}

func (p *Page) Bytes() []byte { return p.buf }

func (p *Page) CloneBytes() []byte {
	out := make([]byte, len(p.buf))
	copy(out, p.buf)
	return out
}

func (p *Page) ID() format.PageID {
	return format.PageID(encoding.U64(p.buf, offPageID))
}

func (p *Page) Type() format.PageType {
	return format.PageType(encoding.U16(p.buf, offType))
}

func (p *Page) Version() uint16 { return encoding.U16(p.buf, offVersion) }

func (p *Page) LSN() format.LSN { return format.LSN(encoding.U64(p.buf, offLSN)) }

func (p *Page) SetLSN(lsn format.LSN) { StampLSN(p.buf, lsn) }

// StampLSN writes the LSN field without touching the rest of the page.
func StampLSN(buf []byte, lsn format.LSN) {
	if len(buf) < offLSN+8 {
		return
	}
	encoding.PutU64(buf, offLSN, uint64(lsn))
}

// LSNOf reads the LSN field from a logical page buffer.
func LSNOf(buf []byte) format.LSN {
	if len(buf) < offLSN+8 {
		return 0
	}
	return format.LSN(encoding.U64(buf, offLSN))
}

// IDOf reads the page ID from a logical page buffer.
func IDOf(buf []byte) format.PageID {
	if len(buf) < offPageID+8 {
		return 0
	}
	return format.PageID(encoding.U64(buf, offPageID))
}

// CheckID verifies magic, format version, and page ID without copying
// or walking slots. AEAD + checksum already authenticated the bytes.
func CheckID(buf []byte, want format.PageID) error {
	if len(buf) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidFormat, "page.CheckID", "logical page has wrong size")
	}
	if !format.HasMagic(buf, offMagic) {
		return nerr.New(nerr.InvalidFormat, "page.CheckID", "bad page magic")
	}
	if encoding.U16(buf, offVersion) != format.CurrentFormatVersion {
		return nerr.New(nerr.InvalidFormat, "page.CheckID", "unsupported page format version")
	}
	if IDOf(buf) != want {
		return nerr.New(nerr.Corruption, "page.CheckID", "page id mismatch")
	}
	return nil
}

func (p *Page) TxnMeta() format.TxnID { return format.TxnID(encoding.U64(p.buf, offTxn)) }

func (p *Page) SetTxnMeta(id format.TxnID) { encoding.PutU64(p.buf, offTxn, uint64(id)) }

func (p *Page) Flags() uint16 { return encoding.U16(p.buf, offFlags) }

func (p *Page) SlotCount() int { return int(encoding.U16(p.buf, offSlotCount)) }

func (p *Page) Lower() int { return int(encoding.U16(p.buf, offLower)) }

func (p *Page) Upper() int { return int(encoding.U16(p.buf, offUpper)) }

func (p *Page) FreeSpace() int {
	return p.Upper() - p.Lower()
}

func (p *Page) Finalize() {
	FinalizeBuf(p.buf)
}

// FinalizeBuf writes the page checksum in place.
func FinalizeBuf(buf []byte) {
	if len(buf) < offChecksum+4 {
		return
	}
	encoding.PutU32(buf, offChecksum, 0)
	checksum.Write(buf, offChecksum)
}

func VerifyChecksum(buf []byte) error {
	if len(buf) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidFormat, "page.VerifyChecksum", "logical page has wrong size")
	}
	return checksum.Verify(buf, offChecksum)
}

func (p *Page) slotOff(i int) int {
	return format.PageHeaderSize + i*format.SlotSize
}

func (p *Page) slot(i int) (offset, length uint16) {
	o := p.slotOff(i)
	return encoding.U16(p.buf, o), encoding.U16(p.buf, o+2)
}

func (p *Page) setSlot(i int, offset, length uint16) {
	o := p.slotOff(i)
	encoding.PutU16(p.buf, o, offset)
	encoding.PutU16(p.buf, o+2, length)
}

// Append inserts at the next slot with no hole scan. Use on sequentially
// filled pages that have no deleted slots.
func (p *Page) Append(record []byte) (uint16, error) {
	if len(record) > format.MaxRecordSize {
		return 0, nerr.New(nerr.InvalidArgument, "page.Append", "record exceeds maximum size")
	}
	n := p.SlotCount()
	need := len(record) + format.SlotSize
	if p.FreeSpace() < need {
		return 0, nerr.New(nerr.PageFull, "page.Append", "page is full")
	}
	upper := p.Upper() - len(record)
	copy(p.buf[upper:upper+len(record)], record)
	encoding.PutU16(p.buf, offUpper, uint16(upper))
	p.setSlot(n, uint16(upper), uint16(len(record)))
	encoding.PutU16(p.buf, offSlotCount, uint16(n+1))
	encoding.PutU16(p.buf, offLower, uint16(p.slotOff(n+1)))
	return uint16(n), nil
}

func (p *Page) Insert(record []byte) (uint16, error) {
	if len(record) > format.MaxRecordSize {
		return 0, nerr.New(nerr.InvalidArgument, "page.Insert", "record exceeds maximum size")
	}
	needSlot := true
	reuse := -1
	n := p.SlotCount()
	for i := 0; i < n; i++ {
		off, length := p.slot(i)
		if length == 0 && off == 0 {
			reuse = i
			needSlot = false
			break
		}
	}
	need := len(record)
	if needSlot {
		need += format.SlotSize
	}
	if p.FreeSpace() < need {
		p.Compact()
		if p.FreeSpace() < need {
			return 0, nerr.New(nerr.PageFull, "page.Insert", "page is full")
		}
	}
	upper := p.Upper() - len(record)
	copy(p.buf[upper:upper+len(record)], record)
	encoding.PutU16(p.buf, offUpper, uint16(upper))

	var idx int
	if reuse >= 0 {
		idx = reuse
		p.setSlot(idx, uint16(upper), uint16(len(record)))
	} else {
		idx = n
		p.setSlot(idx, uint16(upper), uint16(len(record)))
		encoding.PutU16(p.buf, offSlotCount, uint16(n+1))
		encoding.PutU16(p.buf, offLower, uint16(p.slotOff(n+1)))
	}
	return uint16(idx), nil
}

func (p *Page) Get(slot uint16) ([]byte, error) {
	view, err := p.GetView(slot)
	if err != nil {
		return nil, err
	}
	out := make([]byte, len(view))
	copy(out, view)
	return out, nil
}

func (p *Page) GetView(slot uint16) ([]byte, error) {
	i := int(slot)
	if i < 0 || i >= p.SlotCount() {
		return nil, nerr.New(nerr.NotFound, "page.Get", "slot out of range")
	}
	off, length := p.slot(i)
	if length == 0 && off == 0 {
		return nil, nerr.New(nerr.NotFound, "page.Get", "slot deleted")
	}
	end := int(off) + int(length)
	if int(off) < p.Upper() || end > format.LogicalPageSize {
		return nil, nerr.New(nerr.Corruption, "page.Get", "slot points outside record area")
	}
	return p.buf[off:end], nil
}

func (p *Page) Delete(slot uint16) error {
	i := int(slot)
	if i < 0 || i >= p.SlotCount() {
		return nerr.New(nerr.NotFound, "page.Delete", "slot out of range")
	}
	off, length := p.slot(i)
	if length == 0 && off == 0 {
		return nerr.New(nerr.NotFound, "page.Delete", "slot deleted")
	}
	p.setSlot(i, 0, 0)
	return nil
}

func (p *Page) Update(slot uint16, record []byte) error {
	i := int(slot)
	if i < 0 || i >= p.SlotCount() {
		return nerr.New(nerr.NotFound, "page.Update", "slot out of range")
	}
	off, length := p.slot(i)
	if length == 0 && off == 0 {
		return nerr.New(nerr.NotFound, "page.Update", "slot deleted")
	}
	if len(record) <= int(length) {
		copy(p.buf[off:int(off)+len(record)], record)
		p.setSlot(i, off, uint16(len(record)))
		return nil
	}
	if !p.canReplace(i, len(record)) {
		return nerr.New(nerr.PageFull, "page.Update", "page is full")
	}
	if err := p.Delete(slot); err != nil {
		return err
	}
	p.Compact()
	newSlot, err := p.Insert(record)
	if err != nil {
		return err
	}
	if newSlot != slot {
		noff, nlen := p.slot(int(newSlot))
		p.setSlot(i, noff, nlen)
		p.setSlot(int(newSlot), 0, 0)
	}
	return nil
}

// canReplace reports whether slot can grow to newLen without adding a slot.
func (p *Page) canReplace(slot, newLen int) bool {
	live := 0
	old := 0
	n := p.SlotCount()
	for i := 0; i < n; i++ {
		off, length := p.slot(i)
		if length == 0 && off == 0 {
			continue
		}
		live += int(length)
		if i == slot {
			old = int(length)
		}
	}
	used := format.PageHeaderSize + format.SlotSize*n + live - old + newLen
	return used <= format.LogicalPageSize
}

// Compact packs live records to the end of the page. Slot indexes are preserved.
func (p *Page) Compact() {
	n := p.SlotCount()
	tmp := make([]byte, format.LogicalPageSize)
	upper := format.LogicalPageSize
	for i := 0; i < n; i++ {
		off, length := p.slot(i)
		if length == 0 && off == 0 {
			continue
		}
		upper -= int(length)
		copy(tmp[upper:upper+int(length)], p.buf[off:int(off)+int(length)])
		p.setSlot(i, uint16(upper), length)
	}
	copy(p.buf[upper:], tmp[upper:])
	encoding.PutU16(p.buf, offUpper, uint16(upper))
	encoding.PutU16(p.buf, offLower, uint16(p.slotOff(n)))
}

func (p *Page) LiveSlots() int {
	n := 0
	for i := 0; i < p.SlotCount(); i++ {
		off, length := p.slot(i)
		if !(length == 0 && off == 0) {
			n++
		}
	}
	return n
}
