package wal

import (
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	changeMagic      uint32 = 0x4443534e // NSCD
	ChangeVersion    uint16 = 1
	changeHeaderSize        = 28

	MaxChangeNameBytes   = 255
	MaxChangeTenantBytes = 255
	MaxChangeKeyBytes    = 8 << 10
	changeFlagBefore     = 1 << 0
	changeFlagAfter      = 1 << 1
)

// ChangeOperation is a logical row mutation. It is deliberately native and
// independent of the physical B+Tree WAL records used by recovery.
type ChangeOperation uint8

const (
	ChangeInsert ChangeOperation = iota + 1
	ChangeUpdate
	ChangeDelete
)

func (o ChangeOperation) String() string {
	switch o {
	case ChangeInsert:
		return "INSERT"
	case ChangeUpdate:
		return "UPDATE"
	case ChangeDelete:
		return "DELETE"
	default:
		return "INVALID"
	}
}

func (o ChangeOperation) valid() bool {
	return o >= ChangeInsert && o <= ChangeDelete
}

// Change is the v1 logical change envelope. Key is the primary key
// after INSERT/UPDATE and before DELETE. OldKey is present only when UPDATE
// changes the primary key. Tenant and OldTenant follow the same convention.
// Before/After contain opt-in NSRW row images. They are absent by default.
type Change struct {
	Operation ChangeOperation
	TableID   uint32
	Table     string
	Tenant    string
	OldTenant string
	Key       []byte
	OldKey    []byte
	Before    []byte
	After     []byte
}

func (c Change) validate() error {
	if !c.Operation.valid() {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "invalid operation")
	}
	if c.TableID == 0 || c.Table == "" || len(c.Table) > MaxChangeNameBytes {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "invalid table identity")
	}
	if len(c.Tenant) > MaxChangeTenantBytes || len(c.OldTenant) > MaxChangeTenantBytes {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "tenant identity is too large")
	}
	if len(c.Key) == 0 || len(c.Key) > MaxChangeKeyBytes || len(c.OldKey) > MaxChangeKeyBytes {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "invalid primary key")
	}
	if c.Operation != ChangeUpdate && (len(c.OldKey) != 0 || c.OldTenant != "") {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "old identity is only valid for UPDATE")
	}
	if (c.Operation == ChangeInsert && len(c.Before) != 0) || (c.Operation == ChangeDelete && len(c.After) != 0) {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "row image does not match operation")
	}
	if len(c.Before) > 0xffff || len(c.After) > 0xffff {
		return nerr.New(nerr.InvalidArgument, "wal.Change", "row image is too large")
	}
	return nil
}

// EncodedSize returns the exact version-1 body size after validation.
func (c Change) EncodedSize() (int, error) {
	if err := c.validate(); err != nil {
		return 0, err
	}
	n := changeHeaderSize + len(c.Table) + len(c.Tenant) + len(c.OldTenant) + len(c.Key) + len(c.OldKey) + len(c.Before) + len(c.After)
	if n > format.LogicalPageSize {
		return 0, nerr.New(nerr.InvalidArgument, "wal.Change", "logical change exceeds WAL record limit")
	}
	return n, nil
}

func encodeChange(c Change) ([]byte, error) {
	n, err := c.EncodedSize()
	if err != nil {
		return nil, err
	}
	buf := make([]byte, n)
	encoding.PutU32(buf, 0, changeMagic)
	encoding.PutU16(buf, 4, ChangeVersion)
	buf[6] = byte(c.Operation)
	if len(c.Before) != 0 {
		buf[7] |= changeFlagBefore
	}
	if len(c.After) != 0 {
		buf[7] |= changeFlagAfter
	}
	encoding.PutU32(buf, 8, c.TableID)
	encoding.PutU16(buf, 12, uint16(len(c.Table)))
	encoding.PutU16(buf, 14, uint16(len(c.Tenant)))
	encoding.PutU16(buf, 16, uint16(len(c.OldTenant)))
	encoding.PutU16(buf, 18, uint16(len(c.Key)))
	encoding.PutU16(buf, 20, uint16(len(c.OldKey)))
	encoding.PutU16(buf, 22, uint16(len(c.Before)))
	encoding.PutU16(buf, 24, uint16(len(c.After)))
	// bytes 26..27 remain reserved.
	off := changeHeaderSize
	for _, part := range []string{c.Table, c.Tenant, c.OldTenant} {
		copy(buf[off:], part)
		off += len(part)
	}
	copy(buf[off:], c.Key)
	off += len(c.Key)
	copy(buf[off:], c.OldKey)
	off += len(c.OldKey)
	copy(buf[off:], c.Before)
	off += len(c.Before)
	copy(buf[off:], c.After)
	return buf, nil
}

// DecodeChange validates and decodes a RecChange body. Returned byte slices do
// not alias body.
func DecodeChange(body []byte) (Change, error) {
	if len(body) < changeHeaderSize {
		return Change{}, nerr.New(nerr.InvalidFormat, "wal.DecodeChange", "truncated change header")
	}
	if encoding.U32(body, 0) != changeMagic {
		return Change{}, nerr.New(nerr.InvalidFormat, "wal.DecodeChange", "bad change magic")
	}
	if encoding.U16(body, 4) != ChangeVersion {
		return Change{}, nerr.New(nerr.InvalidFormat, "wal.DecodeChange", "unsupported change version")
	}
	if body[7] & ^byte(changeFlagBefore|changeFlagAfter) != 0 || encoding.U16(body, 26) != 0 {
		return Change{}, nerr.New(nerr.InvalidFormat, "wal.DecodeChange", "unsupported change flags")
	}
	lens := [...]int{
		int(encoding.U16(body, 12)), int(encoding.U16(body, 14)),
		int(encoding.U16(body, 16)), int(encoding.U16(body, 18)),
		int(encoding.U16(body, 20)), int(encoding.U16(body, 22)),
		int(encoding.U16(body, 24)),
	}
	total := changeHeaderSize
	for _, n := range lens {
		total += n
	}
	if total != len(body) {
		return Change{}, nerr.New(nerr.InvalidFormat, "wal.DecodeChange", "change length mismatch")
	}
	off := changeHeaderSize
	takeString := func(n int) string {
		s := string(body[off : off+n])
		off += n
		return s
	}
	takeBytes := func(n int) []byte {
		b := append([]byte(nil), body[off:off+n]...)
		off += n
		return b
	}
	c := Change{
		Operation: ChangeOperation(body[6]),
		TableID:   encoding.U32(body, 8),
		Table:     takeString(lens[0]),
		Tenant:    takeString(lens[1]),
		OldTenant: takeString(lens[2]),
		Key:       takeBytes(lens[3]),
		OldKey:    takeBytes(lens[4]),
		Before:    takeBytes(lens[5]),
		After:     takeBytes(lens[6]),
	}
	if (len(c.Before) != 0) != (body[7]&changeFlagBefore != 0) || (len(c.After) != 0) != (body[7]&changeFlagAfter != 0) {
		return Change{}, nerr.New(nerr.InvalidFormat, "wal.DecodeChange", "row image flag mismatch")
	}
	if err := c.validate(); err != nil {
		return Change{}, nerr.Wrap(nerr.InvalidFormat, "wal.DecodeChange", "invalid change", err)
	}
	return c, nil
}

// ChangeRec constructs one versioned logical change WAL record.
func ChangeRec(txn format.TxnID, prev format.LSN, change Change) (Record, error) {
	if txn == 0 {
		return Record{}, nerr.New(nerr.InvalidArgument, "wal.ChangeRec", "transaction id is required")
	}
	body, err := encodeChange(change)
	if err != nil {
		return Record{}, err
	}
	return Record{Type: RecChange, TxnID: txn, PrevLSN: prev, Body: body}, nil
}

func cloneChange(c Change) Change {
	c.Key = append([]byte(nil), c.Key...)
	c.OldKey = append([]byte(nil), c.OldKey...)
	c.Before = append([]byte(nil), c.Before...)
	c.After = append([]byte(nil), c.After...)
	return c
}

// CloneChange copies the variable-sized fields for bounded transaction staging.
func CloneChange(c Change) Change { return cloneChange(c) }
