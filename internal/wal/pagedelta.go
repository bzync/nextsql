package wal

import (
	"crypto/sha256"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// Page deltas.
//
// Redo is physical: recovery reconstructs each page from logged page states.
// Logging a whole 16 KiB image for every page a commit touched made a
// single-row insert write ~18 KB, which is what saturated the disk under
// sustained concurrent writes. A RecPageDelta instead carries only the byte
// ranges that differ from the page's previous logged state -- its base.
//
// A delta is only as correct as its base, so it names the base by LSN and by
// digest, and recovery applies it only to a page whose LSN is exactly that
// base LSN and whose content matches the digest (ApplyPageDelta). Two header
// fields are excluded from both the diff and the digest because they change
// independently of the content: the page LSN, which each record stamps, and
// the checksum, which is recomputed whenever the page is written to the data
// file. The engine decides when a base is safe to use (see
// storage.Engine page-delta cache); this file only encodes, decodes and
// applies.
//
// Body layout, version 1:
//
//	u8   version (1)
//	u64  base LSN
//	[32] SHA-256 of the base page, LSN and checksum fields zeroed
//	u16  run count
//	runs: u16 offset | u16 length | length bytes   (sorted, non-overlapping)

const (
	pageDeltaVersion    = 1
	pageDeltaHeaderSize = 1 + 8 + sha256.Size + 2
	pageDeltaRunHeader  = 4
	// pageDeltaMergeGap joins two changed runs separated by fewer unchanged
	// bytes than this, since a separate run header would cost more.
	pageDeltaMergeGap = 8

	// pageLSNOffset is the page LSN field; format.go defines it for
	// AppendPageImage. pageChecksumOffset/Size mirror internal/storage/page.
	pageChecksumOffset = 40
	pageChecksumSize   = 4
)

// PageLSNOffset is the byte offset of the page LSN field in a logical page.
const PageLSNOffset = pageLSNOffset

// PageDeltaRec builds a RecPageDelta record for page id.
func PageDeltaRec(txn format.TxnID, prev format.LSN, id format.PageID, body []byte) Record {
	return Record{Type: RecPageDelta, TxnID: txn, PrevLSN: prev, PageID: id, Body: body}
}

// PageDelta is a decoded RecPageDelta body.
type PageDelta struct {
	BaseLSN format.LSN
	BaseSum [sha256.Size]byte
	Runs    []PageDeltaRun
}

// PageDeltaRun replaces Data at Offset.
type PageDeltaRun struct {
	Offset uint16
	Data   []byte
}

// masked reports whether byte i of a logical page is excluded from deltas.
func masked(i int) bool {
	return (i >= pageLSNOffset && i < pageLSNOffset+8) ||
		(i >= pageChecksumOffset && i < pageChecksumOffset+pageChecksumSize)
}

// PageDeltaSum is the digest a delta records for its base page.
func PageDeltaSum(page []byte) [sha256.Size]byte {
	var buf [format.LogicalPageSize]byte
	copy(buf[:], page)
	for i := pageLSNOffset; i < pageLSNOffset+8; i++ {
		buf[i] = 0
	}
	for i := pageChecksumOffset; i < pageChecksumOffset+pageChecksumSize; i++ {
		buf[i] = 0
	}
	return sha256.Sum256(buf[:])
}

// EncodePageDelta encodes cur against base, which was logged at baseLSN. It
// reports ok=false when the delta would not be smaller than maxBody, in which
// case the caller logs a full image instead. Both pages must be logical pages.
func EncodePageDelta(baseLSN format.LSN, base, cur []byte, maxBody int) ([]byte, bool) {
	if len(base) != format.LogicalPageSize || len(cur) != format.LogicalPageSize || baseLSN == 0 {
		return nil, false
	}
	type span struct{ start, end int }
	var spans []span
	for i := 0; i < format.LogicalPageSize; i++ {
		if masked(i) || base[i] == cur[i] {
			continue
		}
		j := i + 1
		for j < format.LogicalPageSize && !masked(j) && base[j] != cur[j] {
			j++
		}
		if n := len(spans); n > 0 && i-spans[n-1].end < pageDeltaMergeGap && !maskedBetween(spans[n-1].end, i) {
			spans[n-1].end = j
		} else {
			spans = append(spans, span{i, j})
		}
		i = j - 1
	}
	size := pageDeltaHeaderSize
	for _, sp := range spans {
		size += pageDeltaRunHeader + sp.end - sp.start
		if size > maxBody {
			return nil, false
		}
	}
	if len(spans) > 0xFFFF {
		return nil, false
	}
	body := make([]byte, size)
	body[0] = pageDeltaVersion
	encoding.PutU64(body, 1, uint64(baseLSN))
	sum := PageDeltaSum(base)
	copy(body[9:9+sha256.Size], sum[:])
	encoding.PutU16(body, 9+sha256.Size, uint16(len(spans)))
	off := pageDeltaHeaderSize
	for _, sp := range spans {
		encoding.PutU16(body, off, uint16(sp.start))
		encoding.PutU16(body, off+2, uint16(sp.end-sp.start))
		copy(body[off+4:], cur[sp.start:sp.end])
		off += pageDeltaRunHeader + sp.end - sp.start
	}
	return body, true
}

// maskedBetween reports whether any masked byte lies in [a, b).
func maskedBetween(a, b int) bool {
	for i := a; i < b; i++ {
		if masked(i) {
			return true
		}
	}
	return false
}

// DecodePageDelta validates and decodes a RecPageDelta body. The body is
// authenticated WAL content, but it is still decoded defensively: every run
// must lie inside the page, runs must be sorted and non-overlapping, must not
// cover a masked field, and the body must be consumed exactly.
func DecodePageDelta(body []byte) (PageDelta, error) {
	var d PageDelta
	if len(body) < pageDeltaHeaderSize {
		return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "truncated page delta")
	}
	if body[0] != pageDeltaVersion {
		return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "unsupported page delta version")
	}
	d.BaseLSN = format.LSN(encoding.U64(body, 1))
	if d.BaseLSN == 0 {
		return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "page delta has no base")
	}
	copy(d.BaseSum[:], body[9:9+sha256.Size])
	n := int(encoding.U16(body, 9+sha256.Size))
	off := pageDeltaHeaderSize
	prevEnd := 0
	d.Runs = make([]PageDeltaRun, 0, n)
	for i := 0; i < n; i++ {
		if len(body)-off < pageDeltaRunHeader {
			return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "truncated page delta run")
		}
		start := int(encoding.U16(body, off))
		length := int(encoding.U16(body, off+2))
		off += pageDeltaRunHeader
		if length == 0 || start < prevEnd || start+length > format.LogicalPageSize || len(body)-off < length {
			return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "invalid page delta run")
		}
		if maskedBetween(start, start+length) {
			return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "page delta run covers a page header field it may not change")
		}
		d.Runs = append(d.Runs, PageDeltaRun{Offset: uint16(start), Data: body[off : off+length]})
		off += length
		prevEnd = start + length
	}
	if off != len(body) {
		return d, nerr.New(nerr.InvalidFormat, "wal.DecodePageDelta", "trailing bytes after page delta runs")
	}
	return d, nil
}

// ApplyPageDelta applies a decoded delta to page, which must be the delta's
// base: its LSN must equal BaseLSN and its digest BaseSum. On success page
// holds the delta's result stamped with lsn. On any mismatch page is left
// unchanged and an error names what did not match.
func ApplyPageDelta(page []byte, d PageDelta, lsn format.LSN) error {
	if len(page) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidArgument, "wal.ApplyPageDelta", "logical page has wrong size")
	}
	if got := format.LSN(encoding.U64(page, pageLSNOffset)); got != d.BaseLSN {
		return nerr.New(nerr.Corruption, "wal.ApplyPageDelta", "page is not at the delta's base LSN")
	}
	if PageDeltaSum(page) != d.BaseSum {
		return nerr.New(nerr.Corruption, "wal.ApplyPageDelta", "page content does not match the delta's base")
	}
	for _, r := range d.Runs {
		copy(page[int(r.Offset):], r.Data)
	}
	encoding.PutU64(page, pageLSNOffset, uint64(lsn))
	return nil
}
