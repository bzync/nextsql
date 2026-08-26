package undo

import (
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/maintenance"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const (
	controlMagic      uint32 = 0x4355534E // NSUC
	controlVersion           = 1
	controlHeaderSize        = 72
	controlName              = "control"
	controlTmpName           = "control.tmp"
	logName                  = "undo.log"
	logTmpName               = "undo.log.compact"
	nonceBatch               = 65536
)

// DirFor returns the UNDO directory sibling of a data file.
func DirFor(dbPath string) string { return dbPath + ".undo" }

type controlFile struct {
	Identity       format.Identity
	NextID         format.UndoID
	NonceHigh      uint64
	WrappedUNDODEK []byte
}

// Log is an append-only UNDO log encrypted with a dedicated UNDO DEK.
type Log struct {
	mu        sync.Mutex
	dir       string
	ident     format.Identity
	keys      crypto.KeyProvider
	dek       *crypto.DEK
	wrapped   []byte
	nextID    format.UndoID
	nonceCur  uint64
	nonceLim  uint64
	nonceHigh uint64
	file      *os.File
	recs      map[format.UndoID]Record
	byTxn     map[format.TxnID]format.UndoID
	wbuf      []byte
}

func Create(dir string, pageKeys crypto.KeyProvider, ident format.Identity) (*Log, error) {
	if pageKeys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "undo.Create", "nil key provider")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "undo.Create", "mkdir", err)
	}
	if _, err := os.Stat(filepath.Join(dir, controlName)); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "undo.Create", "UNDO control file exists")
	}
	kek, err := crypto.WrapParent(pageKeys)
	if err != nil {
		return nil, err
	}
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	wrapped, err := crypto.WrapDEK(kek, dek, crypto.DomainUNDO)
	if err != nil {
		return nil, err
	}
	l := &Log{
		dir:       dir,
		ident:     ident,
		keys:      pageKeys,
		dek:       dek,
		wrapped:   wrapped,
		nextID:    1,
		nonceCur:  1,
		nonceLim:  nonceBatch,
		nonceHigh: nonceBatch,
		recs:      make(map[format.UndoID]Record),
		byTxn:     make(map[format.TxnID]format.UndoID),
	}
	if err := l.writeControl(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, logName), os.O_CREATE|os.O_RDWR|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "undo.Create", "open log", err)
	}
	l.file = f
	if err := diskio.SyncDir(dir); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

func Open(dir string, pageKeys crypto.KeyProvider, ident format.Identity) (*Log, error) {
	if pageKeys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "undo.Open", "nil key provider")
	}
	ctrl, err := readControl(dir)
	if err != nil {
		return nil, err
	}
	if ctrl.Identity.Database != ident.Database || ctrl.Identity.File != ident.File {
		return nil, nerr.New(nerr.Corruption, "undo.Open", "UNDO identity does not match data file")
	}
	kek, err := crypto.WrapParent(pageKeys)
	if err != nil {
		return nil, err
	}
	dek, err := crypto.UnwrapDEK(kek, ctrl.WrappedUNDODEK, crypto.DomainUNDO)
	if err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(dir, logName), os.O_RDWR, 0o600)
	if err != nil {
		if os.IsNotExist(err) {
			f, err = os.OpenFile(filepath.Join(dir, logName), os.O_CREATE|os.O_RDWR, 0o600)
		}
		if err != nil {
			return nil, nerr.Wrap(nerr.IO, "undo.Open", "open log", err)
		}
	}
	l := &Log{
		dir:       dir,
		ident:     ident,
		keys:      pageKeys,
		dek:       dek,
		wrapped:   append([]byte(nil), ctrl.WrappedUNDODEK...),
		nextID:    ctrl.NextID,
		nonceHigh: ctrl.NonceHigh,
		file:      f,
		recs:      make(map[format.UndoID]Record),
		byTxn:     make(map[format.TxnID]format.UndoID),
	}
	if l.nextID == 0 {
		l.nextID = 1
	}
	l.nonceCur = l.nonceHigh + 1
	l.nonceLim = l.nonceCur + nonceBatch - 1
	l.nonceHigh = l.nonceLim
	if err := l.writeControl(); err != nil {
		_ = f.Close()
		return nil, err
	}
	if err := l.replay(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return l, nil
}

func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nil
	}
	ferr := l.flushBufLocked()
	err := l.file.Close()
	l.file = nil
	if ferr != nil {
		return ferr
	}
	if err != nil {
		return nerr.Wrap(nerr.IO, "undo.Close", "close", err)
	}
	return nil
}

func (l *Log) CrashClose() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = l.file.Close()
		l.file = nil
	}
}

// Append persists an undo record and returns its id. The record is also
// indexed in memory for version-chain walks.
func (l *Log) Append(r Record) (format.UndoID, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return 0, nerr.New(nerr.Internal, "undo.Append", "log is closed")
	}
	id := l.nextID
	l.nextID++
	r.ID = id
	if r.Prev == 0 {
		r.Prev = l.byTxn[r.Txn]
	}
	if err := l.writeRecordLocked(r); err != nil {
		l.nextID = id
		return 0, err
	}
	l.recs[id] = copyRec(r)
	l.byTxn[r.Txn] = id
	return id, nil
}

func (l *Log) Get(id format.UndoID) (Record, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	r, ok := l.recs[id]
	if !ok {
		return Record{}, nerr.New(nerr.NotFound, "undo.Get", "undo record not found")
	}
	return copyRec(r), nil
}

// ForgetTxn drops in-memory undo for a committed transaction when no
// snapshot still needs the version chain. Disk records stay until vacuum.
func (l *Log) ForgetTxn(txn format.TxnID) {
	if l == nil || txn == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	head := l.byTxn[txn]
	for head != 0 {
		r, ok := l.recs[head]
		delete(l.recs, head)
		if !ok {
			break
		}
		head = r.Prev
	}
	delete(l.byTxn, txn)
}

// Head returns the newest undo id for txn, or 0.
func (l *Log) Head(txn format.TxnID) format.UndoID {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.byTxn[txn]
}

// Chain walks newest → oldest undo records for a transaction.
func (l *Log) Chain(head format.UndoID) []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	var out []Record
	for head != 0 {
		r, ok := l.recs[head]
		if !ok {
			break
		}
		out = append(out, copyRec(r))
		head = r.Prev
	}
	return out
}

func (l *Log) writeRecordLocked(r Record) error {
	buf, err := l.sealRecordLocked(r)
	if err != nil {
		return err
	}
	l.wbuf = append(l.wbuf, buf...)
	if len(l.wbuf) >= 64<<10 {
		return l.flushBufLocked()
	}
	return nil
}

func (l *Log) sealRecordLocked(r Record) ([]byte, error) {
	if l.nonceCur > l.nonceLim {
		l.nonceLim = l.nonceHigh + nonceBatch
		l.nonceHigh = l.nonceLim
		if err := l.writeControlLocked(); err != nil {
			return nil, err
		}
	}
	gen := l.nonceCur
	l.nonceCur++
	payload := encodeRecord(r)
	hdr := make([]byte, HeaderSize)
	encoding.PutU32(hdr, 0, Magic)
	encoding.PutU16(hdr, 4, CurrentVersion)
	encoding.PutU16(hdr, 6, uint16(l.dek.Suite))
	encoding.PutU32(hdr, 8, uint32(l.dek.Version))
	encoding.PutU64(hdr, 12, uint64(r.ID))
	aad := append([]byte(nil), hdr[:20]...)
	nonce, ct, err := crypto.SealBytes(l.dek, gen, aad, payload)
	if err != nil {
		return nil, err
	}
	encoding.PutU32(hdr, 20, uint32(len(ct)))
	copy(hdr[24:36], nonce)
	checksum.Write(hdr, 36)
	buf := make([]byte, HeaderSize+len(ct))
	copy(buf, hdr)
	copy(buf[HeaderSize:], ct)
	return buf, nil
}

// Vacuum atomically rewrites undo.log with only records still retained in
// memory. Record IDs and chain links are preserved; ciphertext receives fresh
// nonces. Callers must ensure forgotten transactions are no longer visible.
func (l *Log) Vacuum() error {
	return l.VacuumBudgeted(nil)
}

func (l *Log) VacuumBudgeted(budget *maintenance.Budget) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file == nil {
		return nerr.New(nerr.Internal, "undo.Vacuum", "log is closed")
	}
	if err := l.flushBufLocked(); err != nil {
		return err
	}
	if err := l.file.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "sync old log", err)
	}
	ids := make([]uint64, 0, len(l.recs))
	for id := range l.recs {
		ids = append(ids, uint64(id))
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	// Preflight rewrite I/O so exhaustion cannot occur after replacement starts.
	for _, rawID := range ids {
		r := l.recs[format.UndoID(rawID)]
		n := HeaderSize + len(encodeRecord(r)) + format.AuthTagSize
		units := int64((n + format.LogicalPageSize - 1) / format.LogicalPageSize)
		if err := budget.ConsumeIO(units); err != nil {
			return err
		}
	}
	tmpPath := filepath.Join(l.dir, logTmpName)
	tmp, err := os.OpenFile(tmpPath, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0o600)
	if err != nil {
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "open temporary log", err)
	}
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	for _, rawID := range ids {
		if err := budget.Check(); err != nil {
			return err
		}
		buf, err := l.sealRecordLocked(l.recs[format.UndoID(rawID)])
		if err != nil {
			return err
		}
		if err := budget.ReserveMemory(int64(len(buf))); err != nil {
			return err
		}
		_, err = tmp.Write(buf)
		budget.ReleaseMemory(int64(len(buf)))
		if err != nil {
			return nerr.Wrap(nerr.IO, "undo.Vacuum", "write temporary log", err)
		}
	}
	if err := tmp.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "sync temporary log", err)
	}
	if err := tmp.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "close temporary log", err)
	}
	if err := l.file.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "close old log", err)
	}
	l.file = nil
	if err := os.Rename(tmpPath, filepath.Join(l.dir, logName)); err != nil {
		l.file, _ = os.OpenFile(filepath.Join(l.dir, logName), os.O_RDWR|os.O_APPEND, 0o600)
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "replace log", err)
	}
	ok = true
	l.file, err = os.OpenFile(filepath.Join(l.dir, logName), os.O_RDWR|os.O_APPEND, 0o600)
	if err != nil {
		return nerr.Wrap(nerr.IO, "undo.Vacuum", "reopen log", err)
	}
	return diskio.SyncDir(l.dir)
}

// Flush writes buffered UNDO records. Commit must flush before the
// commit record so version chains survive recovery.
func (l *Log) Flush() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.flushBufLocked()
}

func (l *Log) flushBufLocked() error {
	if l.file == nil || len(l.wbuf) == 0 {
		return nil
	}
	if _, err := l.file.Write(l.wbuf); err != nil {
		return nerr.Wrap(nerr.IO, "undo.Flush", "write", err)
	}
	l.wbuf = l.wbuf[:0]
	return nil
}

func (l *Log) replay() error {
	if _, err := l.file.Seek(0, io.SeekStart); err != nil {
		return nerr.Wrap(nerr.IO, "undo.Open", "seek", err)
	}
	hdr := make([]byte, HeaderSize)
	for {
		if _, err := io.ReadFull(l.file, hdr); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return nerr.Wrap(nerr.IO, "undo.Open", "read header", err)
		}
		if encoding.U32(hdr, 0) != Magic {
			return nil // torn tail
		}
		if err := checksum.Verify(hdr, 36); err != nil {
			return nil
		}
		if encoding.U16(hdr, 4) != CurrentVersion {
			return nerr.New(nerr.InvalidFormat, "undo.Open", "unsupported undo record version")
		}
		ctLen := int(encoding.U32(hdr, 20))
		if ctLen < format.AuthTagSize || ctLen > format.LogicalPageSize {
			return nil
		}
		ct := make([]byte, ctLen)
		if _, err := io.ReadFull(l.file, ct); err != nil {
			return nil
		}
		id := format.UndoID(encoding.U64(hdr, 12))
		aad := append([]byte(nil), hdr[:20]...)
		payload, err := crypto.OpenBytes(l.dek, hdr[24:36], aad, ct)
		if err != nil {
			return nil
		}
		rec, err := decodeRecord(id, payload)
		if err != nil {
			return err
		}
		l.recs[id] = rec
		l.byTxn[rec.Txn] = id
		if id >= l.nextID {
			l.nextID = id + 1
		}
	}
}

func (l *Log) writeControl() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.writeControlLocked()
}

func (l *Log) writeControlLocked() error {
	c := controlFile{
		Identity:       l.ident,
		NextID:         l.nextID,
		NonceHigh:      l.nonceHigh,
		WrappedUNDODEK: l.wrapped,
	}
	buf := encodeControl(c)
	tmp := filepath.Join(l.dir, controlTmpName)
	if err := os.WriteFile(tmp, buf, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "undo.writeControl", "write", err)
	}
	f, err := os.Open(tmp)
	if err != nil {
		return nerr.Wrap(nerr.IO, "undo.writeControl", "open", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nerr.Wrap(nerr.IO, "undo.writeControl", "sync", err)
	}
	_ = f.Close()
	if err := os.Rename(tmp, filepath.Join(l.dir, controlName)); err != nil {
		return nerr.Wrap(nerr.IO, "undo.writeControl", "rename", err)
	}
	return diskio.SyncDir(l.dir)
}

func encodeControl(c controlFile) []byte {
	wrapLen := len(c.WrappedUNDODEK)
	buf := make([]byte, controlHeaderSize+wrapLen)
	encoding.PutU32(buf, 0, controlMagic)
	encoding.PutU16(buf, 4, controlVersion)
	copy(buf[8:24], c.Identity.Database[:])
	copy(buf[24:40], c.Identity.File[:])
	encoding.PutU64(buf, 40, uint64(c.NextID))
	encoding.PutU64(buf, 48, c.NonceHigh)
	encoding.PutU16(buf, 56, uint16(wrapLen))
	copy(buf[controlHeaderSize:], c.WrappedUNDODEK)
	checksum.Write(buf[:controlHeaderSize], 68)
	return buf
}

func readControl(dir string) (controlFile, error) {
	buf, err := os.ReadFile(filepath.Join(dir, controlName))
	if err != nil {
		return controlFile{}, nerr.Wrap(nerr.IO, "undo.readControl", "read", err)
	}
	if len(buf) < controlHeaderSize {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "undo.readControl", "truncated control file")
	}
	if encoding.U32(buf, 0) != controlMagic {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "undo.readControl", "bad UNDO control magic")
	}
	if encoding.U16(buf, 4) != controlVersion {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "undo.readControl", "unsupported control version")
	}
	if err := checksum.Verify(buf[:controlHeaderSize], 68); err != nil {
		return controlFile{}, nerr.Wrap(nerr.Corruption, "undo.readControl", "checksum", err)
	}
	wrapLen := int(encoding.U16(buf, 56))
	if wrapLen < 0 || controlHeaderSize+wrapLen > len(buf) {
		return controlFile{}, nerr.New(nerr.InvalidFormat, "undo.readControl", "truncated wrapped UNDO DEK")
	}
	c := controlFile{
		NextID:         format.UndoID(encoding.U64(buf, 40)),
		NonceHigh:      encoding.U64(buf, 48),
		WrappedUNDODEK: append([]byte(nil), buf[controlHeaderSize:controlHeaderSize+wrapLen]...),
	}
	copy(c.Identity.Database[:], buf[8:24])
	copy(c.Identity.File[:], buf[24:40])
	return c, nil
}

func copyRec(r Record) Record {
	r.Key = append([]byte(nil), r.Key...)
	r.Old.Payload = append([]byte(nil), r.Old.Payload...)
	return r
}
