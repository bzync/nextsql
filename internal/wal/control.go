package wal

import (
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const (
	// ControlMagic is ASCII 'N','S','W','C'.
	ControlMagic uint32 = 0x4357534E

	controlVersion    = 1
	controlHeaderSize = 104
	controlName       = "control"
	controlTmpName    = "control.tmp"
)

type controlFile struct {
	NextLSN       format.LSN
	DurableLSN    format.LSN
	Checkpoint    format.LSN
	RedoLSN       format.LSN
	NextSegment   uint64
	NextTxn       format.TxnID
	NonceHigh     uint64
	WrappedWALDEK []byte
	Identity      format.Identity
}

func encodeControl(c controlFile) []byte {
	wrapLen := len(c.WrappedWALDEK)
	buf := make([]byte, controlHeaderSize+wrapLen)
	encoding.PutU32(buf, 0, ControlMagic)
	encoding.PutU16(buf, 4, controlVersion)
	copy(buf[8:24], c.Identity.Database[:])
	copy(buf[24:40], c.Identity.File[:])
	encoding.PutU64(buf, 40, uint64(c.NextLSN))
	encoding.PutU64(buf, 48, uint64(c.DurableLSN))
	encoding.PutU64(buf, 56, uint64(c.Checkpoint))
	encoding.PutU64(buf, 64, uint64(c.RedoLSN))
	encoding.PutU64(buf, 72, c.NextSegment)
	encoding.PutU64(buf, 80, uint64(c.NextTxn))
	encoding.PutU64(buf, 88, c.NonceHigh)
	encoding.PutU16(buf, 96, uint16(wrapLen))
	copy(buf[controlHeaderSize:], c.WrappedWALDEK)
	checksum.Write(buf[:controlHeaderSize], 100)
	return buf
}

func decodeControl(buf []byte) (controlFile, error) {
	if len(buf) < controlHeaderSize {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "wal.decodeControl", "truncated control file")
	}
	if encoding.U32(buf, 0) != ControlMagic {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "wal.decodeControl", "bad control magic")
	}
	if encoding.U16(buf, 4) != controlVersion {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "wal.decodeControl", "unsupported control version")
	}
	if err := checksum.Verify(buf[:controlHeaderSize], 100); err != nil {
		return controlFile{}, nerr.Wrap(nerr.Corruption, "wal.decodeControl", "checksum", err)
	}
	wrapLen := int(encoding.U16(buf, 96))
	if wrapLen < 0 || controlHeaderSize+wrapLen > len(buf) {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "wal.decodeControl", "truncated wrapped WAL DEK")
	}
	c := controlFile{
		NextLSN:       format.LSN(encoding.U64(buf, 40)),
		DurableLSN:    format.LSN(encoding.U64(buf, 48)),
		Checkpoint:    format.LSN(encoding.U64(buf, 56)),
		RedoLSN:       format.LSN(encoding.U64(buf, 64)),
		NextSegment:   encoding.U64(buf, 72),
		NextTxn:       format.TxnID(encoding.U64(buf, 80)),
		NonceHigh:     encoding.U64(buf, 88),
		WrappedWALDEK: append([]byte(nil), buf[controlHeaderSize:controlHeaderSize+wrapLen]...),
	}
	copy(c.Identity.Database[:], buf[8:24])
	copy(c.Identity.File[:], buf[24:40])
	if c.NextLSN == 0 {
		return controlFile{}, nerr.New(nerr.Corruption, "wal.decodeControl", "invalid next LSN")
	}
	if c.NextTxn == 0 {
		return controlFile{}, nerr.New(nerr.Corruption, "wal.decodeControl", "invalid next txn id")
	}
	if c.NonceHigh == 0 {
		return controlFile{}, nerr.New(nerr.Corruption, "wal.decodeControl", "invalid WAL nonce high water")
	}
	if len(c.WrappedWALDEK) == 0 {
		return controlFile{}, nerr.New(nerr.Corruption, "wal.decodeControl", "missing wrapped WAL DEK")
	}
	return c, nil
}

func writeControlAtomic(dir string, c controlFile) error {
	raw := encodeControl(c)
	tmp := filepath.Join(dir, controlTmpName)
	final := filepath.Join(dir, controlName)
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return nerr.Wrap(nerr.IO, "wal.writeControl", "create", err)
	}
	if err := diskio.WriteFullAt(f, raw, 0); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := diskio.Sync(f); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "wal.writeControl", "close", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "wal.writeControl", "rename", err)
	}
	return diskio.SyncDir(dir)
}

func readControl(dir string) (controlFile, error) {
	raw, err := os.ReadFile(filepath.Join(dir, controlName))
	if err != nil {
		return controlFile{}, nerr.Wrap(nerr.IO, "wal.readControl", "read", err)
	}
	return decodeControl(raw)
}
