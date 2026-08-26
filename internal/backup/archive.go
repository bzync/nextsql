package backup

import (
	"os"
	"path/filepath"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
	"github.com/bzync/nextsql/internal/wal"
)

// ArchiveEntry is one recycled WAL segment stored for PITR.
type ArchiveEntry struct {
	ID         uint64
	FirstLSN   format.LSN
	LastLSN    format.LSN
	ArchivedAt int64
	Name       string
	SHA256     [32]byte
	SealedSize uint64
}

// DirArchiver copies recycled WAL segments into an encrypted archive directory.
type DirArchiver struct {
	Dir  string
	Keys crypto.KeyProvider
	dek  *crypto.DEK
	wrap []byte
	gen  uint64
}

func NewDirArchiver(dir string, keys crypto.KeyProvider) (*DirArchiver, error) {
	if dir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "backup.NewDirArchiver", "archive directory is required")
	}
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "backup.NewDirArchiver", "nil key provider")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.NewDirArchiver", "mkdir", err)
	}
	dek, wrap, err := newBackupDEK(keys)
	if err != nil {
		return nil, err
	}
	return &DirArchiver{Dir: dir, Keys: keys, dek: dek, wrap: wrap, gen: 1}, nil
}

func (a *DirArchiver) Archive(path string, first, last format.LSN) error {
	if a == nil || a.dek == nil {
		return nerr.New(nerr.InvalidArgument, "backup.DirArchiver.Archive", "nil archiver")
	}
	id, ok := wal.ParseSegmentFileName(filepath.Base(path))
	if !ok {
		return nerr.New(nerr.InvalidFormat, "backup.DirArchiver.Archive", "not a WAL segment")
	}
	segFirst, segLast, err := segmentLSNRange(path)
	if err != nil {
		return err
	}
	if first == 0 {
		first = segFirst
	}
	if last == 0 {
		last = segLast
	}
	name := wal.SegmentFileName(id)
	dst := filepath.Join(a.Dir, name)
	_ = os.Remove(dst)
	plain, sealed, sum, next, err := sealFile(a.dek, name, path, dst, a.gen)
	if err != nil {
		return err
	}
	_ = plain
	a.gen = next
	ent := ArchiveEntry{
		ID:         id,
		FirstLSN:   first,
		LastLSN:    last,
		ArchivedAt: time.Now().UnixNano(),
		Name:       name,
		SHA256:     sum,
		SealedSize: sealed,
	}
	return a.appendIndex(ent)
}

func (a *DirArchiver) appendIndex(ent ArchiveEntry) error {
	ents, wrap, err := readArchiveIndex(a.Dir)
	if err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return err
	}
	if wrap == nil {
		wrap = a.wrap
	}
	replaced := false
	for i, e := range ents {
		if e.ID == ent.ID {
			ents[i] = ent
			replaced = true
			break
		}
	}
	if !replaced {
		ents = append(ents, ent)
	}
	return writeArchiveIndex(a.Dir, wrap, ents)
}

func readArchiveIndex(dir string) ([]ArchiveEntry, []byte, error) {
	raw, err := os.ReadFile(filepath.Join(dir, archiveIndex))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil, nerr.New(nerr.NotFound, "backup.readArchiveIndex", "no archive index")
		}
		return nil, nil, nerr.Wrap(nerr.IO, "backup.readArchiveIndex", "read", err)
	}
	if len(raw) < 16 {
		return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "truncated index")
	}
	if encoding.U32(raw, 0) != ArchiveMagic {
		return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "bad archive magic")
	}
	if encoding.U16(raw, 4) != CurrentVersion {
		return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "unsupported archive version")
	}
	if err := checksum.Verify(raw, len(raw)-4); err != nil {
		return nil, nil, nerr.Wrap(nerr.Corruption, "backup.readArchiveIndex", "checksum", err)
	}
	wrapLen := int(encoding.U16(raw, 8))
	n := int(encoding.U16(raw, 10))
	if wrapLen <= 0 || wrapLen > maxWrapBlob || n < 0 || n > maxArchiveEnt {
		return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "index counts exceed limit")
	}
	off := 12
	if off+wrapLen > len(raw)-4 {
		return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "truncated wrap")
	}
	wrap := append([]byte(nil), raw[off:off+wrapLen]...)
	off += wrapLen
	ents := make([]ArchiveEntry, 0, n)
	end := len(raw) - 4
	for i := 0; i < n; i++ {
		if off+8+8+8+8+2 > end {
			return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "truncated entry")
		}
		var e ArchiveEntry
		e.ID = encoding.U64(raw, off)
		off += 8
		e.FirstLSN = format.LSN(encoding.U64(raw, off))
		off += 8
		e.LastLSN = format.LSN(encoding.U64(raw, off))
		off += 8
		e.ArchivedAt = int64(encoding.U64(raw, off))
		off += 8
		e.SealedSize = encoding.U64(raw, off)
		off += 8
		nl := int(encoding.U16(raw, off))
		off += 2
		if nl == 0 || nl > maxNameLen || off+nl+32 > end {
			return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "invalid entry name")
		}
		e.Name = string(raw[off : off+nl])
		off += nl
		copy(e.SHA256[:], raw[off:off+32])
		off += 32
		ents = append(ents, e)
	}
	if off != end {
		return nil, nil, nerr.New(nerr.InvalidFormat, "backup.readArchiveIndex", "trailing index bytes")
	}
	return ents, wrap, nil
}

func writeArchiveIndex(dir string, wrap []byte, ents []ArchiveEntry) error {
	if len(ents) > maxArchiveEnt {
		return nerr.New(nerr.InvalidFormat, "backup.writeArchiveIndex", "too many archive entries")
	}
	n := 12 + len(wrap)
	for _, e := range ents {
		if len(e.Name) == 0 || len(e.Name) > maxNameLen {
			return nerr.New(nerr.InvalidFormat, "backup.writeArchiveIndex", "invalid entry name")
		}
		n += 8 + 8 + 8 + 8 + 8 + 2 + len(e.Name) + 32
	}
	n += 4
	buf := make([]byte, n)
	encoding.PutU32(buf, 0, ArchiveMagic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU16(buf, 8, uint16(len(wrap)))
	encoding.PutU16(buf, 10, uint16(len(ents)))
	off := 12
	copy(buf[off:], wrap)
	off += len(wrap)
	for _, e := range ents {
		encoding.PutU64(buf, off, e.ID)
		off += 8
		encoding.PutU64(buf, off, uint64(e.FirstLSN))
		off += 8
		encoding.PutU64(buf, off, uint64(e.LastLSN))
		off += 8
		encoding.PutU64(buf, off, uint64(e.ArchivedAt))
		off += 8
		encoding.PutU64(buf, off, e.SealedSize)
		off += 8
		encoding.PutU16(buf, off, uint16(len(e.Name)))
		off += 2
		copy(buf[off:], e.Name)
		off += len(e.Name)
		copy(buf[off:], e.SHA256[:])
		off += 32
	}
	checksum.Write(buf, off)
	path := filepath.Join(dir, archiveIndex)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "backup.writeArchiveIndex", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "backup.writeArchiveIndex", "rename", err)
	}
	return diskio.SyncDir(dir)
}

func segmentLSNRange(path string) (first, last format.LSN, err error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, 0, nerr.Wrap(nerr.IO, "backup.segmentLSNRange", "open", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return 0, 0, nerr.Wrap(nerr.IO, "backup.segmentLSNRange", "stat", err)
	}
	if st.Size() < wal.SegmentHeaderSize {
		return 0, 0, nerr.New(nerr.InvalidFormat, "backup.segmentLSNRange", "truncated segment")
	}
	hdr := make([]byte, wal.SegmentHeaderSize)
	if err := diskio.ReadFullAt(f, hdr, 0); err != nil {
		return 0, 0, err
	}
	first = format.LSN(encoding.U64(hdr, 16))
	last = first
	off := int64(wal.SegmentHeaderSize)
	recHdr := make([]byte, wal.HeaderSize)
	for off+wal.HeaderSize <= st.Size() {
		if err := diskio.ReadFullAt(f, recHdr, off); err != nil {
			break
		}
		lsn := format.LSN(encoding.U64(recHdr, 12))
		ctLen := encoding.U32(recHdr, 20)
		if lsn != 0 {
			if first == 0 || lsn < first {
				first = lsn
			}
			if lsn > last {
				last = lsn
			}
		}
		need := int64(wal.HeaderSize) + int64(ctLen)
		if need <= 0 || off+need > st.Size() {
			break
		}
		off += need
	}
	return first, last, nil
}

func applyArchive(archiveDir, walDir string, keys crypto.KeyProvider, until format.LSN) error {
	if archiveDir == "" {
		return nil
	}
	ents, wrap, err := readArchiveIndex(archiveDir)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil
		}
		return err
	}
	parent, err := crypto.WrapParent(keys)
	if err != nil {
		return err
	}
	dek, err := crypto.UnwrapDEK(parent, wrap, crypto.DomainBackup)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(walDir, 0o700); err != nil {
		return nerr.Wrap(nerr.IO, "backup.applyArchive", "mkdir wal", err)
	}
	for _, e := range ents {
		if until != 0 && e.FirstLSN > until {
			continue
		}
		dst := filepath.Join(walDir, e.Name)
		if _, err := os.Stat(dst); err == nil {
			// Archived copy is taken at recycle/checkpoint and may be a
			// longer prefix of the same segment than the base backup.
			_ = os.Remove(dst)
		}
		src := filepath.Join(archiveDir, e.Name)
		sum, size, err := fileSHA256(src)
		if err != nil {
			return err
		}
		if sum != e.SHA256 || uint64(size) != e.SealedSize {
			return nerr.New(nerr.Corruption, "backup.applyArchive", "archived segment checksum mismatch")
		}
		if _, err := openMember(dek, e.Name, src, dst); err != nil {
			return err
		}
	}
	return nil
}

// ResolveUntilTime maps a restore timestamp onto the latest LSN covered by
// the base backup or an archived segment whose archive time is <= until.
func ResolveUntilTime(hdr Header, archiveDir string, until time.Time) (format.LSN, error) {
	target := until.UTC().UnixNano()
	best := format.LSN(0)
	if hdr.CreatedNano <= target {
		best = hdr.DurableLSN
	}
	if archiveDir == "" {
		if best == 0 {
			return 0, nerr.New(nerr.NotFound, "backup.ResolveUntilTime", "no backup or archive at or before the requested time")
		}
		return best, nil
	}
	ents, _, err := readArchiveIndex(archiveDir)
	if err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return 0, err
	}
	for _, e := range ents {
		if e.ArchivedAt <= target && e.LastLSN > best {
			best = e.LastLSN
		}
	}
	if best == 0 {
		return 0, nerr.New(nerr.NotFound, "backup.ResolveUntilTime", "no backup or archive at or before the requested time")
	}
	return best, nil
}
