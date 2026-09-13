package wal

import (
	"bytes"
	"math/rand"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func testPage(rng *rand.Rand, lsn format.LSN) []byte {
	p := make([]byte, format.LogicalPageSize)
	rng.Read(p)
	encoding.PutU64(p, pageLSNOffset, uint64(lsn))
	return p
}

// equalUnmasked compares two pages ignoring the fields deltas exclude.
func equalUnmasked(a, b []byte) bool {
	for i := range a {
		if !masked(i) && a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestPageDeltaRoundTrip(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	for trial := 0; trial < 500; trial++ {
		base := testPage(rng, 100)
		cur := append([]byte(nil), base...)
		// A handful of edits of varied size, including edits that touch
		// or straddle the masked header fields.
		for e := 0; e < rng.Intn(12); e++ {
			off := rng.Intn(format.LogicalPageSize)
			n := 1 + rng.Intn(64)
			for i := off; i < off+n && i < format.LogicalPageSize; i++ {
				cur[i] = byte(rng.Intn(256))
			}
		}
		body, ok := EncodePageDelta(100, base, cur, format.LogicalPageSize)
		if !ok {
			t.Fatalf("trial %d: small edit did not encode", trial)
		}
		d, err := DecodePageDelta(body)
		if err != nil {
			t.Fatalf("trial %d: decode: %v", trial, err)
		}
		got := append([]byte(nil), base...)
		if err := ApplyPageDelta(got, d, 200); err != nil {
			t.Fatalf("trial %d: apply: %v", trial, err)
		}
		if !equalUnmasked(got, cur) {
			t.Fatalf("trial %d: applied delta differs from the page it encoded", trial)
		}
		if lsn := format.LSN(encoding.U64(got, pageLSNOffset)); lsn != 200 {
			t.Fatalf("trial %d: result stamped %d, want 200", trial, lsn)
		}
	}
}

func TestPageDeltaIdenticalPageHasNoRuns(t *testing.T) {
	rng := rand.New(rand.NewSource(2))
	base := testPage(rng, 7)
	cur := append([]byte(nil), base...)
	encoding.PutU64(cur, pageLSNOffset, 99)            // LSN differs
	copy(cur[pageChecksumOffset:], []byte{1, 2, 3, 4}) // checksum differs
	body, ok := EncodePageDelta(7, base, cur, format.LogicalPageSize)
	if !ok || len(body) != pageDeltaHeaderSize {
		t.Fatalf("a page differing only in LSN and checksum encoded %d bytes (ok=%v), want a bare header", len(body), ok)
	}
}

func TestPageDeltaFallsBackWhenLarge(t *testing.T) {
	rng := rand.New(rand.NewSource(3))
	base := testPage(rng, 1)
	cur := testPage(rng, 1)
	if _, ok := EncodePageDelta(1, base, cur, format.LogicalPageSize/2); ok {
		t.Fatal("a completely rewritten page encoded as a delta under half a page")
	}
}

func TestPageDeltaRefusesTheWrongBase(t *testing.T) {
	rng := rand.New(rand.NewSource(4))
	base := testPage(rng, 50)
	cur := append([]byte(nil), base...)
	cur[5000] ^= 0xFF
	body, _ := EncodePageDelta(50, base, cur, format.LogicalPageSize)
	d, err := DecodePageDelta(body)
	if err != nil {
		t.Fatal(err)
	}
	wrongLSN := append([]byte(nil), base...)
	encoding.PutU64(wrongLSN, pageLSNOffset, 49)
	if err := ApplyPageDelta(wrongLSN, d, 60); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("base at the wrong LSN: %v, want corruption", err)
	}
	wrongContent := append([]byte(nil), base...)
	wrongContent[9000] ^= 1
	before := append([]byte(nil), wrongContent...)
	if err := ApplyPageDelta(wrongContent, d, 60); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("base with different content: %v, want corruption", err)
	}
	if !bytes.Equal(before, wrongContent) {
		t.Fatal("a refused delta modified the page")
	}
	// The recomputed checksum of an on-disk base does not matter.
	onDisk := append([]byte(nil), base...)
	copy(onDisk[pageChecksumOffset:], []byte{9, 9, 9, 9})
	if err := ApplyPageDelta(onDisk, d, 60); err != nil {
		t.Fatalf("base whose checksum was recomputed on write: %v", err)
	}
}

func FuzzDecodePageDelta(f *testing.F) {
	rng := rand.New(rand.NewSource(5))
	base := testPage(rng, 3)
	cur := append([]byte(nil), base...)
	copy(cur[100:], []byte("changed bytes"))
	cur[16000] = 1
	body, _ := EncodePageDelta(3, base, cur, format.LogicalPageSize)
	f.Add(body)
	f.Add([]byte{})
	f.Add(make([]byte, pageDeltaHeaderSize))
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, err := DecodePageDelta(raw)
		if err != nil {
			return
		}
		for _, r := range d.Runs {
			if int(r.Offset)+len(r.Data) > format.LogicalPageSize || maskedBetween(int(r.Offset), int(r.Offset)+len(r.Data)) {
				t.Fatalf("decoder accepted run %d+%d", r.Offset, len(r.Data))
			}
		}
		page := append([]byte(nil), base...)
		encoding.PutU64(page, pageLSNOffset, uint64(d.BaseLSN))
		_ = ApplyPageDelta(page, d, d.BaseLSN+1) // must not panic
	})
}
