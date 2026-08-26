package wal

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const (
	// SegmentMagic is ASCII 'N','S','W','S'.
	SegmentMagic uint32 = 0x5357534E

	SegmentHeaderSize = 64
	segmentPrefix     = "wal-"
	segmentSuffix     = ".seg"
)

type segmentHeader struct {
	ID       uint64
	StartLSN format.LSN
	Identity format.Identity
}

func encodeSegmentHeader(h segmentHeader) []byte {
	buf := make([]byte, SegmentHeaderSize)
	encoding.PutU32(buf, 0, SegmentMagic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU64(buf, 8, h.ID)
	encoding.PutU64(buf, 16, uint64(h.StartLSN))
	copy(buf[24:40], h.Identity.Database[:])
	copy(buf[40:56], h.Identity.File[:])
	checksum.Write(buf, 60)
	return buf
}

func decodeSegmentHeader(buf []byte, want format.Identity) (segmentHeader, error) {
	if len(buf) < SegmentHeaderSize {
		return segmentHeader{}, nerr.New(nerr.InvalidFormat, "wal.decodeSegmentHeader", "truncated segment header")
	}
	if encoding.U32(buf, 0) != SegmentMagic {
		return segmentHeader{}, nerr.New(nerr.InvalidFormat, "wal.decodeSegmentHeader", "bad segment magic")
	}
	if encoding.U16(buf, 4) != CurrentVersion {
		return segmentHeader{}, nerr.New(nerr.InvalidFormat, "wal.decodeSegmentHeader", "unsupported segment version")
	}
	if err := checksum.Verify(buf[:SegmentHeaderSize], 60); err != nil {
		return segmentHeader{}, nerr.Wrap(nerr.Corruption, "wal.decodeSegmentHeader", "checksum", err)
	}
	h := segmentHeader{
		ID:       encoding.U64(buf, 8),
		StartLSN: format.LSN(encoding.U64(buf, 16)),
	}
	copy(h.Identity.Database[:], buf[24:40])
	copy(h.Identity.File[:], buf[40:56])
	if h.Identity.Database != want.Database || h.Identity.File != want.File {
		return segmentHeader{}, nerr.New(nerr.Corruption, "wal.decodeSegmentHeader", "segment identity does not match database")
	}
	if h.ID == 0 || h.StartLSN == 0 {
		return segmentHeader{}, nerr.New(nerr.Corruption, "wal.decodeSegmentHeader", "invalid segment header fields")
	}
	return h, nil
}

func segmentName(id uint64) string {
	return fmt.Sprintf("%s%016x%s", segmentPrefix, id, segmentSuffix)
}

// SegmentFileName is the on-disk name of WAL segment id.
func SegmentFileName(id uint64) string { return segmentName(id) }

// ParseSegmentFileName reports the segment id encoded in a WAL file name.
func ParseSegmentFileName(name string) (uint64, bool) { return parseSegmentName(name) }

func parseSegmentName(name string) (uint64, bool) {
	if !strings.HasPrefix(name, segmentPrefix) || !strings.HasSuffix(name, segmentSuffix) {
		return 0, false
	}
	hex := strings.TrimSuffix(strings.TrimPrefix(name, segmentPrefix), segmentSuffix)
	id, err := strconv.ParseUint(hex, 16, 64)
	if err != nil || id == 0 {
		return 0, false
	}
	return id, true
}

func listSegments(dir string) ([]uint64, error) {
	ents, err := os.ReadDir(dir)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "wal.listSegments", "readdir", err)
	}
	var ids []uint64
	for _, e := range ents {
		if e.IsDir() {
			continue
		}
		id, ok := parseSegmentName(e.Name())
		if ok {
			ids = append(ids, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids, nil
}

func createSegment(dir string, h segmentHeader, _ int64) (*os.File, error) {
	path := filepath.Join(dir, segmentName(h.ID))
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "wal.createSegment", "create", err)
	}
	if err := diskio.WriteFullAt(f, encodeSegmentHeader(h), 0); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := diskio.Sync(f); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := diskio.SyncDir(dir); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func openSegment(dir string, id uint64, ident format.Identity) (*os.File, segmentHeader, int64, error) {
	path := filepath.Join(dir, segmentName(id))
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, segmentHeader{}, 0, nerr.Wrap(nerr.IO, "wal.openSegment", "open", err)
	}
	hdr := make([]byte, SegmentHeaderSize)
	if err := diskio.ReadFullAt(f, hdr, 0); err != nil {
		_ = f.Close()
		return nil, segmentHeader{}, 0, err
	}
	h, err := decodeSegmentHeader(hdr, ident)
	if err != nil {
		_ = f.Close()
		return nil, segmentHeader{}, 0, err
	}
	if h.ID != id {
		_ = f.Close()
		return nil, segmentHeader{}, 0, nerr.New(nerr.Corruption, "wal.openSegment", "segment id mismatch")
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, segmentHeader{}, 0, nerr.Wrap(nerr.IO, "wal.openSegment", "stat", err)
	}
	return f, h, st.Size(), nil
}
