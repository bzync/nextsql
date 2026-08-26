package page

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestEncodeDecode(t *testing.T) {
	p := New(7, format.PageTypeSlotted)
	p.SetLSN(42)
	p.SetTxnMeta(9)
	slot, err := p.Insert([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	p.Finalize()
	if err := VerifyChecksum(p.Bytes()); err != nil {
		t.Fatal(err)
	}
	got, err := Parse(p.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID() != 7 || got.LSN() != 42 || got.TxnMeta() != 9 {
		t.Fatalf("header %+v", got)
	}
	rec, err := got.Get(slot)
	if err != nil {
		t.Fatal(err)
	}
	if string(rec) != "hello" {
		t.Fatalf("record %q", rec)
	}
	if _, err := ParseID(p.Bytes(), 8); err == nil {
		t.Fatal("expected page id mismatch")
	}
}

func TestInvalidFormatVersion(t *testing.T) {
	p := New(1, format.PageTypeSlotted)
	encoding.PutU16(p.Bytes(), offVersion, 99)
	if _, err := Parse(p.Bytes()); err == nil {
		t.Fatal("expected unsupported version")
	}
}

func TestTruncatedPage(t *testing.T) {
	p := New(1, format.PageTypeSlotted)
	if _, err := Parse(p.Bytes()[:100]); err == nil {
		t.Fatal("expected truncated page")
	}
	if _, err := Wrap(p.Bytes()[:100]); err == nil {
		t.Fatal("expected truncated wrap")
	}
}

func TestInvalidPageID(t *testing.T) {
	p := New(3, format.PageTypeSlotted)
	if _, err := ParseID(p.Bytes(), 4); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("expected id mismatch, got %v", err)
	}
}

func TestCheckID(t *testing.T) {
	p := New(3, format.PageTypeSlotted)
	if err := CheckID(p.Bytes(), 3); err != nil {
		t.Fatal(err)
	}
	if err := CheckID(p.Bytes(), 4); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("expected id mismatch, got %v", err)
	}
	if IDOf(p.Bytes()) != 3 {
		t.Fatalf("IDOf %d", IDOf(p.Bytes()))
	}
}

func TestDeleteUpdateCompact(t *testing.T) {
	p := New(1, format.PageTypeSlotted)
	a, _ := p.Insert([]byte("aaa"))
	b, _ := p.Insert([]byte("bbbb"))
	c, _ := p.Insert([]byte("ccccc"))
	if err := p.Delete(b); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Get(b); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("deleted slot still readable: %v", err)
	}
	if err := p.Update(a, []byte("AA")); err != nil {
		t.Fatal(err)
	}
	if err := p.Update(c, []byte("CCCCCCCC")); err != nil {
		t.Fatal(err)
	}
	p.Compact()
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	gotA, _ := p.Get(a)
	gotC, _ := p.Get(c)
	if string(gotA) != "AA" || string(gotC) != "CCCCCCCC" {
		t.Fatalf("after compact %q %q", gotA, gotC)
	}
	// Deleted slot is reused.
	d, err := p.Insert([]byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if d != b {
		t.Fatalf("expected reuse of slot %d, got %d", b, d)
	}
}

func TestPageFull(t *testing.T) {
	p := New(1, format.PageTypeSlotted)
	payload := bytes.Repeat([]byte("x"), 200)
	for {
		if _, err := p.Insert(payload); err != nil {
			if !nerr.HasCode(err, nerr.PageFull) {
				t.Fatalf("unexpected %v", err)
			}
			break
		}
	}
	if p.LiveSlots() == 0 {
		t.Fatal("page should hold at least one record")
	}
}

func TestUpdateGrowPreservesSlots(t *testing.T) {
	p := New(1, format.PageTypeSlotted)
	const n = 20
	slots := make([]uint16, n)
	for i := 0; i < n; i++ {
		s, err := p.Insert(bytes.Repeat([]byte("a"), 40))
		if err != nil {
			t.Fatal(err)
		}
		slots[i] = s
	}
	grew := 0
	for size := 48; size < 800; size += 8 {
		err := p.Update(slots[3], bytes.Repeat([]byte("b"), size))
		if nerr.HasCode(err, nerr.PageFull) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		grew++
		if err := p.Validate(); err != nil {
			t.Fatalf("validate after grow %d: %v", size, err)
		}
		for _, s := range slots {
			if _, err := p.Get(s); err != nil {
				t.Fatalf("slot %d unreadable after grow %d: %v", s, size, err)
			}
		}
	}
	if grew == 0 {
		t.Fatal("expected at least one successful grow")
	}
}

func TestCorruptSlotFailsClosed(t *testing.T) {
	p := New(1, format.PageTypeSlotted)
	if _, err := p.Insert([]byte("rec")); err != nil {
		t.Fatal(err)
	}
	encoding.PutU16(p.Bytes(), format.PageHeaderSize, 1) // offset inside header
	if _, err := Parse(p.Bytes()); err == nil {
		t.Fatal("expected corrupt slot")
	}
}

func TestNewInRejectsWrongSize(t *testing.T) {
	if _, err := NewIn(make([]byte, 8), 1, format.PageTypeSlotted); err == nil {
		t.Fatal("expected size error")
	}
}
