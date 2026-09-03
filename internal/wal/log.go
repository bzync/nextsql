package wal

import (
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/maintenance"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const (
	DefaultSegmentSize = 128 << 20
	nonceBatch         = 65536
	pageLSNOffset      = 16
)

// ErrHoldBlocksRotation is returned by an append that would need to rotate
// segments while a commit record is held pending replication (AppendHeld).
// A held record's bytes are tied to a specific segment offset — rotating
// out from under it would strand it at the wrong physical position. This
// bounds a hold to at most one segment's worth of unrelated concurrent
// writes, which is expected to comfortably outlast one replication round
// trip; callers should surface this as a retryable condition.
var ErrHoldBlocksRotation = nerr.New(nerr.Unavailable, "wal.rotate", "cannot rotate segment while a commit record is held pending replication")

// Archiver is the PITR hook. Implementations may copy a recycled segment
// elsewhere. The log never deletes a segment that has not been archived
// when an Archiver is installed; without one, fully-recycled segments
// stay on disk.
type Archiver interface {
	Archive(path string, first, last format.LSN) error
}

// Options configure a WAL.
type Options struct {
	SegmentSize int64
	Crash       *Injector
	Archiver    Archiver
}

// Log is the write-ahead log. Records are encrypted with a WAL DEK that is
// distinct from the page DEK and wrapped under the page key provider.
type Log struct {
	mu sync.Mutex
	cv *sync.Cond

	dir   string
	ident format.Identity
	keys  crypto.KeyProvider
	dek   *crypto.DEK

	nextLSN    format.LSN
	durableLSN format.LSN
	checkpoint format.LSN
	redoLSN    format.LSN
	nextSeg    uint64
	nextTxn    format.TxnID
	nonceCur   uint64
	nonceLim   uint64
	nonceHigh  uint64
	wrapped    []byte

	buf     []byte
	bufLast format.LSN

	seg     *os.File
	segID   uint64
	segOff  int64
	syncOff int64
	written int64

	flushing bool
	flushErr error

	segmentSize int64
	crash       *Injector
	archiver    Archiver

	retentionPins map[uint64]format.LSN
	nextPin       uint64

	// held is a single-slot durability barrier: a committing transaction's
	// CommitRec may be appended via AppendHeld and kept out of the durable
	// prefix flushLocked ever writes until ReleaseHold decides its fate.
	// Only one record may be held at a time — callers serialize this
	// through Engine.replMu, one replicated commit in flight at a time.
	held         bool
	heldOffset   int
	heldLen      int
	heldLSN      format.LSN
	heldPrevLast format.LSN
}

func (o Options) withDefaults() Options {
	if o.SegmentSize <= 0 {
		o.SegmentSize = DefaultSegmentSize
	}
	return o
}

// Create initializes an empty WAL directory next to a new data file.
func Create(dir string, pageKeys crypto.KeyProvider, ident format.Identity, opt Options) (*Log, error) {
	if pageKeys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "wal.Create", "nil key provider")
	}
	opt = opt.withDefaults()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, nerr.Wrap(nerr.IO, "wal.Create", "mkdir", err)
	}
	if _, err := os.Stat(filepath.Join(dir, controlName)); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "wal.Create", "WAL control file exists")
	}
	kek, err := crypto.WrapParent(pageKeys)
	if err != nil {
		return nil, err
	}
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	wrapped, err := crypto.WrapDEK(kek, dek, crypto.DomainWAL)
	if err != nil {
		return nil, err
	}
	l := &Log{
		dir:         dir,
		ident:       ident,
		keys:        pageKeys,
		dek:         dek,
		nextLSN:     1,
		durableLSN:  0,
		redoLSN:     1,
		nextSeg:     1,
		nextTxn:     1,
		nonceCur:    1,
		nonceLim:    nonceBatch,
		nonceHigh:   nonceBatch,
		wrapped:     wrapped,
		buf:         make([]byte, 0, 8<<20),
		segmentSize: opt.SegmentSize,
		crash:       opt.Crash,
		archiver:    opt.Archiver,
	}
	l.cv = sync.NewCond(&l.mu)
	if err := l.writeControlLocked(); err != nil {
		return nil, err
	}
	if err := l.openNewSegmentLocked(1, 1); err != nil {
		return nil, err
	}
	if err := diskio.SyncDir(dir); err != nil {
		_ = l.Close()
		return nil, err
	}
	return l, nil
}

// Open loads an existing WAL. Callers must run recovery before appending.
func Open(dir string, pageKeys crypto.KeyProvider, ident format.Identity, opt Options) (*Log, error) {
	if pageKeys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "wal.Open", "nil key provider")
	}
	opt = opt.withDefaults()
	ctrl, err := readControl(dir)
	if err != nil {
		return nil, err
	}
	if ctrl.Identity.Database != ident.Database || ctrl.Identity.File != ident.File {
		return nil, nerr.New(nerr.Corruption, "wal.Open", "WAL identity does not match data file")
	}
	kek, err := crypto.WrapParent(pageKeys)
	if err != nil {
		return nil, err
	}
	dek, err := crypto.UnwrapDEK(kek, ctrl.WrappedWALDEK, crypto.DomainWAL)
	if err != nil {
		return nil, err
	}
	l := &Log{
		dir:         dir,
		ident:       ident,
		keys:        pageKeys,
		dek:         dek,
		nextLSN:     ctrl.NextLSN,
		durableLSN:  ctrl.DurableLSN,
		checkpoint:  ctrl.Checkpoint,
		redoLSN:     ctrl.RedoLSN,
		nextSeg:     ctrl.NextSegment,
		nextTxn:     ctrl.NextTxn,
		nonceHigh:   ctrl.NonceHigh,
		wrapped:     append([]byte(nil), ctrl.WrappedWALDEK...),
		buf:         make([]byte, 0, 8<<20),
		segmentSize: opt.SegmentSize,
		crash:       opt.Crash,
		archiver:    opt.Archiver,
	}
	l.cv = sync.NewCond(&l.mu)
	if l.redoLSN == 0 {
		l.redoLSN = 1
	}
	if l.nextSeg == 0 {
		l.nextSeg = 1
	}
	// Reserve a fresh nonce batch so crash-replay cannot reuse a generation.
	if err := l.reserveNonceLocked(); err != nil {
		return nil, err
	}
	ids, err := listSegments(dir)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		if err := l.openNewSegmentLocked(l.nextSeg, l.nextLSN); err != nil {
			return nil, err
		}
		return l, nil
	}
	last := ids[len(ids)-1]
	f, hdr, size, err := openSegment(dir, last, ident)
	if err != nil {
		return nil, err
	}
	l.seg = f
	l.segID = hdr.ID
	l.segOff = size
	l.syncOff = size
	if last > l.nextSeg {
		l.nextSeg = last
	}
	return l, nil
}

func DirFor(dbPath string) string { return dbPath + ".wal" }

func (l *Log) Dir() string { return l.dir }

func (l *Log) Identity() format.Identity {
	if l == nil {
		return format.Identity{}
	}
	return l.ident
}

func (l *Log) NextLSN() format.LSN {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nextLSN
}

func (l *Log) DurableLSN() format.LSN {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.durableLSN
}

func (l *Log) RedoLSN() format.LSN {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.redoLSN == 0 {
		return 1
	}
	return l.redoLSN
}

func (l *Log) CheckpointLSN() format.LSN {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.checkpoint
}

// BytesWritten is durable WAL payload bytes flushed this process.
func (l *Log) BytesWritten() int64 {
	if l == nil {
		return 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.written
}

func (l *Log) NextTxn() format.TxnID {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.nextTxn
}

func (l *Log) AllocTxn() format.TxnID {
	l.mu.Lock()
	defer l.mu.Unlock()
	id := l.nextTxn
	l.nextTxn++
	return id
}

// InstallRecords appends already-assigned records (replica apply).
// Records with LSN < NextLSN are skipped. A gap fails closed.
func (l *Log) InstallRecords(recs []Record) error {
	if l == nil {
		return nerr.New(nerr.InvalidArgument, "wal.InstallRecords", "nil log")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, rec := range recs {
		if rec.LSN == 0 {
			return nerr.New(nerr.InvalidArgument, "wal.InstallRecords", "LSN 0 is reserved")
		}
		if rec.LSN < l.nextLSN {
			continue
		}
		if rec.LSN != l.nextLSN {
			return nerr.New(nerr.Corruption, "wal.InstallRecords", "LSN gap in replicated records")
		}
		if _, err := l.appendLocked(rec); err != nil {
			return err
		}
	}
	return l.flushLocked()
}

func (l *Log) SetCrash(c *Injector) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.crash = c
}

func (l *Log) SetArchiver(a Archiver) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.archiver = a
}

// PinRetention protects every segment that may contain records at or after
// oldest. The returned release function is idempotent. Pins are process-local
// subscription state; callers must reconstruct them when subscriptions resume
// after restart.
func (l *Log) PinRetention(oldest format.LSN) (func(), error) {
	if l == nil || oldest == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "wal.PinRetention", "non-zero retention horizon is required")
	}
	l.mu.Lock()
	if l.retentionPins == nil {
		l.retentionPins = make(map[uint64]format.LSN)
	}
	l.nextPin++
	if l.nextPin == 0 {
		l.nextPin++
	}
	id := l.nextPin
	l.retentionPins[id] = oldest
	l.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.retentionPins, id)
			l.mu.Unlock()
		})
	}, nil
}

func (l *Log) retentionHorizonLocked(requested format.LSN) format.LSN {
	horizon := requested
	for _, pin := range l.retentionPins {
		if pin != 0 && (horizon == 0 || pin < horizon) {
			horizon = pin
		}
	}
	return horizon
}

// Append adds rec to the in-memory group-commit buffer. The record is not
// durable until Flush returns successfully for its LSN.
func (l *Log) Append(rec Record) (format.LSN, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.appendLocked(rec)
}

// AppendPageImage logs a full logical page and stamps the page LSN into body.
func (l *Log) AppendPageImage(txn format.TxnID, prev format.LSN, id format.PageID, page []byte) (format.LSN, []byte, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if len(page) != format.LogicalPageSize {
		return 0, nil, nerr.New(nerr.InvalidArgument, "wal.AppendPageImage", "page image has wrong size")
	}
	lsn := l.nextLSN
	body := append([]byte(nil), page...)
	encodingPutLSN(body, lsn)
	rec := Record{Type: RecPageImage, TxnID: txn, PrevLSN: prev, PageID: id, Body: body}
	got, err := l.appendLocked(rec)
	if err != nil {
		return 0, nil, err
	}
	return got, body, nil
}

const pageImagePhysSize = HeaderSize + payloadHeaderSize + format.LogicalPageSize + format.AuthTagSize

// AppendPageImages writes many page images. Encryption of the payloads runs
// concurrently into a single buffer; LSN assignment and the WAL buffer stay ordered.
func (l *Log) AppendPageImages(txn format.TxnID, prev format.LSN, ids []format.PageID, images [][]byte) ([]format.LSN, format.LSN, error) {
	if l == nil {
		return nil, prev, nerr.New(nerr.InvalidArgument, "wal.AppendPageImages", "nil log")
	}
	if len(ids) != len(images) {
		return nil, prev, nerr.New(nerr.InvalidArgument, "wal.AppendPageImages", "ids/images length")
	}
	if len(ids) == 0 {
		return nil, prev, nil
	}
	type job struct {
		rec Record
		gen uint64
		err error
	}
	jobs := make([]job, len(ids))
	l.mu.Lock()
	p := prev
	for i := range ids {
		if len(images[i]) != format.LogicalPageSize {
			l.mu.Unlock()
			return nil, prev, nerr.New(nerr.InvalidArgument, "wal.AppendPageImages", "page image has wrong size")
		}
		lsn := l.nextLSN
		encodingPutLSN(images[i], lsn)
		jobs[i].rec = Record{Type: RecPageImage, TxnID: txn, PrevLSN: p, PageID: ids[i], Body: images[i], LSN: lsn}
		gen, err := l.nextNonceLocked()
		if err != nil {
			l.mu.Unlock()
			return nil, prev, err
		}
		jobs[i].gen = gen
		p = lsn
		l.nextLSN = lsn + 1
	}
	dek := l.dek
	l.mu.Unlock()

	out := make([]byte, len(jobs)*pageImagePhysSize)
	workers := runtime.GOMAXPROCS(0)
	if workers > 16 {
		workers = 16
	}
	if n := len(jobs); n < workers {
		workers = n
	}
	if workers < 1 {
		workers = 1
	}
	var wg sync.WaitGroup
	ch := make(chan int, len(jobs))
	for i := range jobs {
		ch <- i
	}
	close(ch)
	wg.Add(workers)
	for w := 0; w < workers; w++ {
		go func() {
			defer wg.Done()
			var payload []byte
			for i := range ch {
				payload = encodePayloadInto(payload, jobs[i].rec)
				slot := out[i*pageImagePhysSize : (i+1)*pageImagePhysSize]
				got, err := encodePhysicalInto(dek, jobs[i].rec.LSN, jobs[i].gen, payload, slot[:0])
				if err != nil {
					jobs[i].err = err
					continue
				}
				if len(got) != pageImagePhysSize {
					jobs[i].err = nerr.New(nerr.Internal, "wal.AppendPageImages", "unexpected physical size")
				}
			}
		}()
	}
	wg.Wait()

	l.mu.Lock()
	defer l.mu.Unlock()
	lsns := make([]format.LSN, len(jobs))
	for i := range jobs {
		if jobs[i].err != nil {
			return nil, prev, jobs[i].err
		}
		lsns[i] = jobs[i].rec.LSN
	}
	if l.segOff+int64(len(l.buf))+int64(len(out)) > l.segmentSize && (l.segOff > SegmentHeaderSize || len(l.buf) > 0) {
		if err := l.rotateLocked(); err != nil {
			return nil, prev, err
		}
	}
	l.buf = append(l.buf, out...)
	l.bufLast = jobs[len(jobs)-1].rec.LSN
	return lsns, p, nil
}

func encodingPutLSN(page []byte, lsn format.LSN) {
	if len(page) < pageLSNOffset+8 {
		return
	}
	encoding.PutU64(page, pageLSNOffset, uint64(lsn))
}

func (l *Log) appendLocked(rec Record) (format.LSN, error) {
	if l.seg == nil {
		return 0, nerr.New(nerr.Internal, "wal.Append", "log is closed")
	}
	lsn := l.nextLSN
	rec.LSN = lsn
	payload := encodePayload(rec)
	gen, err := l.nextNonceLocked()
	if err != nil {
		return 0, err
	}
	phys, err := encodePhysical(l.dek, lsn, gen, payload)
	if err != nil {
		return 0, err
	}
	if l.segOff+int64(len(l.buf))+int64(len(phys)) > l.segmentSize && (l.segOff > SegmentHeaderSize || len(l.buf) > 0) {
		if err := l.rotateLocked(); err != nil {
			return 0, err
		}
	}
	l.buf = append(l.buf, phys...)
	l.bufLast = lsn
	l.nextLSN = lsn + 1
	return lsn, nil
}

// AppendHeld appends rec (typically a transaction's CommitRec) but keeps it
// out of the durable prefix flushLocked will write until ReleaseHold
// resolves it — so a caller can replicate rec (and whatever it depends on)
// to Raft quorum before deciding whether it ever becomes durable. Only one
// record may be held at a time; callers must serialize appends through
// Engine.replMu the same way commitAndReplicate already does.
func (l *Log) AppendHeld(rec Record) (format.LSN, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.held {
		return 0, nerr.New(nerr.Internal, "wal.AppendHeld", "a record is already held")
	}
	if l.seg == nil {
		return 0, nerr.New(nerr.Internal, "wal.AppendHeld", "log is closed")
	}
	lsn := l.nextLSN
	rec.LSN = lsn
	payload := encodePayload(rec)
	gen, err := l.nextNonceLocked()
	if err != nil {
		return 0, err
	}
	phys, err := encodePhysical(l.dek, lsn, gen, payload)
	if err != nil {
		return 0, err
	}
	if l.segOff+int64(len(l.buf))+int64(len(phys)) > l.segmentSize && (l.segOff > SegmentHeaderSize || len(l.buf) > 0) {
		if err := l.rotateLocked(); err != nil {
			return 0, err
		}
	}
	prevLast := l.bufLast
	offset := len(l.buf)
	l.buf = append(l.buf, phys...)
	l.bufLast = lsn
	l.nextLSN = lsn + 1
	l.held = true
	l.heldOffset = offset
	l.heldLen = len(phys)
	l.heldLSN = lsn
	l.heldPrevLast = prevLast
	return lsn, nil
}

// ReleaseHold resolves the record previously appended via AppendHeld. If
// commit is true, its bytes stay exactly where they are and become
// flushable like any other record on the next Flush. If commit is false,
// its bytes are spliced back out of the buffer — they never reach disk —
// and its LSN becomes a permanent gap (tolerated the same way an aborted
// transaction's LSNs already are); any unrelated records appended after it
// while it was held are left untouched and still pending their own flush.
// A no-op if nothing is currently held.
func (l *Log) ReleaseHold(commit bool) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.held {
		return nil
	}
	if !commit {
		l.buf = append(l.buf[:l.heldOffset], l.buf[l.heldOffset+l.heldLen:]...)
		if l.bufLast == l.heldLSN {
			l.bufLast = l.heldPrevLast
		}
	}
	l.held = false
	l.heldOffset = 0
	l.heldLen = 0
	l.heldLSN = 0
	l.heldPrevLast = 0
	l.cv.Broadcast()
	return nil
}

// Flush group-commits until lsn is durable. Commit must not be acknowledged
// until this returns nil.
func (l *Log) Flush(lsn format.LSN) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	for l.durableLSN < lsn {
		if l.held && l.heldLSN <= lsn {
			// The target LSN needs bytes flushLocked won't write while
			// held. Wait for ReleaseHold's broadcast rather than spinning
			// on flushLocked calls that can make no further progress.
			l.cv.Wait()
			continue
		}
		if l.flushing {
			l.cv.Wait()
			if l.flushErr != nil && l.durableLSN < lsn {
				return l.flushErr
			}
			continue
		}
		l.flushing = true
		l.flushErr = l.flushLocked()
		l.flushing = false
		l.cv.Broadcast()
		if l.flushErr != nil {
			return l.flushErr
		}
	}
	return nil
}

func (l *Log) flushLocked() error {
	toFlush := len(l.buf)
	last := l.bufLast
	if l.held {
		toFlush = l.heldOffset
		last = l.heldPrevLast
	}
	if toFlush == 0 {
		return nil
	}
	if err := l.hit(PointBeforeWALWrite); err != nil {
		return err
	}
	buf := l.buf[:toFlush]
	n, err := l.seg.WriteAt(buf, l.segOff)
	if n < len(buf) && err == nil {
		err = io.ErrShortWrite
	}
	if err != nil {
		if n > 0 {
			l.segOff += int64(n)
			l.buf = l.buf[n:]
			if l.held {
				l.heldOffset -= n
			}
		}
		return nerr.Wrap(nerr.IO, "wal.Flush", "write", err)
	}
	l.segOff += int64(len(buf))
	l.written += int64(len(buf))
	l.buf = l.buf[len(buf):]
	if l.held {
		l.heldOffset = 0
	}
	if err := l.hit(PointAfterWALWriteBeforeSync); err != nil {
		return err
	}
	if err := diskio.DataSync(l.seg); err != nil {
		return err
	}
	l.syncOff = l.segOff
	l.durableLSN = last
	return nil
}

func (l *Log) rotateLocked() error {
	if l.held {
		return ErrHoldBlocksRotation
	}
	if err := l.hit(PointBeforeRotation); err != nil {
		return err
	}
	if err := l.flushLocked(); err != nil {
		return err
	}
	if l.seg != nil {
		_ = l.seg.Close()
		l.seg = nil
	}
	next := l.segID + 1
	if next < l.nextSeg+1 {
		next = l.nextSeg + 1
	}
	if err := l.openNewSegmentLocked(next, l.nextLSN); err != nil {
		return err
	}
	if err := l.hit(PointAfterRotationBeforeSync); err != nil {
		return err
	}
	return l.writeControlLocked()
}

func (l *Log) openNewSegmentLocked(id uint64, start format.LSN) error {
	f, err := createSegment(l.dir, segmentHeader{ID: id, StartLSN: start, Identity: l.ident}, l.segmentSize)
	if err != nil {
		return err
	}
	l.seg = f
	l.segID = id
	l.segOff = SegmentHeaderSize
	l.syncOff = SegmentHeaderSize
	if id > l.nextSeg {
		l.nextSeg = id
	}
	return nil
}

func (l *Log) nextNonceLocked() (uint64, error) {
	if l.nonceCur >= l.nonceLim {
		if err := l.reserveNonceLocked(); err != nil {
			return 0, err
		}
	}
	g := l.nonceCur
	l.nonceCur++
	return g, nil
}

func (l *Log) reserveNonceLocked() error {
	start := l.nonceHigh
	if start == 0 {
		start = 1
	}
	l.nonceHigh = start + nonceBatch
	if err := l.writeControlLocked(); err != nil {
		l.nonceHigh = start
		return err
	}
	l.nonceCur = start
	if l.nonceCur == 0 {
		l.nonceCur = 1
	}
	l.nonceLim = l.nonceHigh
	return nil
}

func (l *Log) writeControlLocked() error {
	return writeControlAtomic(l.dir, controlFile{
		NextLSN:       l.nextLSN,
		DurableLSN:    l.durableLSN,
		Checkpoint:    l.checkpoint,
		RedoLSN:       l.redoLSN,
		NextSegment:   l.nextSeg,
		NextTxn:       l.nextTxn,
		NonceHigh:     l.nonceHigh,
		WrappedWALDEK: l.wrapped,
		Identity:      l.ident,
	})
}

func (l *Log) hit(p Point) error {
	if l.crash == nil {
		return nil
	}
	return l.crash.Hit(p)
}

// InstallCheckpoint records a durable checkpoint after dirty pages are flushed.
func (l *Log) InstallCheckpoint(lsn, redo format.LSN) error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if err := l.hit(PointAfterCheckpointRecordBeforeControl); err != nil {
		return err
	}
	l.checkpoint = lsn
	if redo == 0 {
		redo = l.nextLSN
	}
	l.redoLSN = redo
	return l.writeControlLocked()
}

// Recycle offers fully-replayed segments to the archival hook.
func (l *Log) Recycle() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.archiver == nil {
		return nil
	}
	ids, err := listSegments(l.dir)
	if err != nil {
		return err
	}
	for _, id := range ids {
		path := filepath.Join(l.dir, segmentName(id))
		if err := l.archiver.Archive(path, 0, l.redoLSN); err != nil {
			return err
		}
	}
	return nil
}

// DiscardCheckpointedSegments removes closed WAL segments made obsolete by an
// installed checkpoint. It is intended for disposable workloads that do not
// require PITR retention. Production callers should normally use an Archiver
// and an explicit retention policy instead.
func (l *Log) DiscardCheckpointedSegments() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.checkpoint == 0 || l.redoLSN == 0 || l.seg == nil {
		return nil
	}
	ids, err := listSegments(l.dir)
	if err != nil {
		return err
	}
	removed := false
	horizon := l.retentionHorizonLocked(l.redoLSN)
	for i, id := range ids {
		if id >= l.segID || i+1 >= len(ids) {
			continue
		}
		// A closed segment is obsolete only when its successor starts no later
		// than redoLSN. Do not infer this from the current active segment: this
		// method may be called after more records and rotations have occurred.
		// In that case, segments newer than the installed checkpoint are still
		// required for recovery even though they are closed.
		next, hdr, _, err := openSegment(l.dir, ids[i+1], l.ident)
		if err != nil {
			return err
		}
		if err := next.Close(); err != nil {
			return nerr.Wrap(nerr.IO, "wal.DiscardCheckpointedSegments", "close successor segment", err)
		}
		if hdr.StartLSN > horizon {
			continue
		}
		if err := os.Remove(filepath.Join(l.dir, segmentName(id))); err != nil && !os.IsNotExist(err) {
			return nerr.Wrap(nerr.IO, "wal.DiscardCheckpointedSegments", "remove segment", err)
		}
		removed = true
	}
	if removed {
		return diskio.SyncDir(l.dir)
	}
	return nil
}

// PruneArchivedBefore removes closed segments ending before both the installed
// redo point, oldest PITR horizon, and every active CDC retention pin. Every
// candidate must be successfully offered to the archiver immediately before
// deletion. A missing horizon, archiver, or checkpoint fails closed.
func (l *Log) PruneArchivedBefore(oldest format.LSN, budget *maintenance.Budget) (int, error) {
	if l == nil || oldest == 0 {
		return 0, nerr.New(nerr.InvalidArgument, "wal.PruneArchivedBefore", "non-zero PITR horizon is required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.archiver == nil {
		return 0, nerr.New(nerr.Unavailable, "wal.PruneArchivedBefore", "WAL archiver is not configured")
	}
	if l.checkpoint == 0 || l.redoLSN == 0 || l.seg == nil {
		return 0, nerr.New(nerr.Unavailable, "wal.PruneArchivedBefore", "durable checkpoint is required")
	}
	oldest = l.retentionHorizonLocked(oldest)
	ids, err := listSegments(l.dir)
	if err != nil {
		return 0, err
	}
	removed := 0
	for i, id := range ids {
		if id >= l.segID || i+1 >= len(ids) {
			continue
		}
		if err := budget.Check(); err != nil {
			return removed, err
		}
		cur, curHdr, curSize, err := openSegment(l.dir, id, l.ident)
		if err != nil {
			return removed, err
		}
		if err := cur.Close(); err != nil {
			return removed, nerr.Wrap(nerr.IO, "wal.PruneArchivedBefore", "close candidate segment", err)
		}
		next, nextHdr, _, err := openSegment(l.dir, ids[i+1], l.ident)
		if err != nil {
			return removed, err
		}
		if err := next.Close(); err != nil {
			return removed, nerr.Wrap(nerr.IO, "wal.PruneArchivedBefore", "close successor segment", err)
		}
		if nextHdr.StartLSN > l.redoLSN || nextHdr.StartLSN > oldest {
			continue
		}
		units := (curSize + format.LogicalPageSize - 1) / format.LogicalPageSize
		if err := budget.ConsumeIO(units); err != nil {
			return removed, err
		}
		path := filepath.Join(l.dir, segmentName(id))
		if err := l.archiver.Archive(path, curHdr.StartLSN, nextHdr.StartLSN-1); err != nil {
			return removed, err
		}
		if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
			return removed, nerr.Wrap(nerr.IO, "wal.PruneArchivedBefore", "remove archived segment", err)
		}
		removed++
	}
	if removed > 0 {
		if err := diskio.SyncDir(l.dir); err != nil {
			return removed, err
		}
	}
	return removed, nil
}

// AdvanceAfterRecovery sets the next LSN/txn after redo.
func (l *Log) AdvanceAfterRecovery(next format.LSN, nextTxn format.TxnID) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if next > l.nextLSN {
		l.nextLSN = next
	}
	if nextTxn > l.nextTxn {
		l.nextTxn = nextTxn
	}
	if l.durableLSN+1 < next {
		l.durableLSN = next - 1
	}
}

// ClipTo drops records after until so a later Open cannot replay them.
// until == 0 is a no-op.
func (l *Log) ClipTo(until format.LSN) error {
	if until == 0 {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seg != nil {
		if err := l.flushLocked(); err != nil {
			return err
		}
		_ = l.seg.Close()
		l.seg = nil
	}
	ids, err := listSegments(l.dir)
	if err != nil {
		return err
	}
	for _, id := range ids {
		path := filepath.Join(l.dir, segmentName(id))
		f, hdr, size, err := openSegment(l.dir, id, l.ident)
		if err != nil {
			return err
		}
		if hdr.StartLSN > until {
			_ = f.Close()
			if err := os.Remove(path); err != nil {
				return nerr.Wrap(nerr.IO, "wal.ClipTo", "remove segment", err)
			}
			continue
		}
		off := int64(SegmentHeaderSize)
		keep := off
		for off < size {
			if size-off < HeaderSize {
				break
			}
			hdrBuf := make([]byte, HeaderSize)
			if err := diskio.ReadFullAt(f, hdrBuf, off); err != nil {
				_ = f.Close()
				return err
			}
			ph, err := parseHeader(hdrBuf)
			if err != nil {
				break
			}
			need := int64(HeaderSize + ph.CTLen)
			if off+need > size {
				break
			}
			if ph.LSN > until {
				break
			}
			off += need
			keep = off
		}
		if err := f.Truncate(keep); err != nil {
			_ = f.Close()
			return nerr.Wrap(nerr.IO, "wal.ClipTo", "truncate", err)
		}
		if err := diskio.Sync(f); err != nil {
			_ = f.Close()
			return err
		}
		_ = f.Close()
	}
	l.nextLSN = until + 1
	l.durableLSN = until
	if l.redoLSN > until+1 {
		l.redoLSN = until + 1
	}
	ids, err = listSegments(l.dir)
	if err != nil {
		return err
	}
	if len(ids) == 0 {
		return l.openNewSegmentLocked(1, 1)
	}
	lastID := ids[len(ids)-1]
	f, _, size, err := openSegment(l.dir, lastID, l.ident)
	if err != nil {
		return err
	}
	l.seg = f
	l.segID = lastID
	l.segOff = size
	l.syncOff = size
	if lastID >= l.nextSeg {
		l.nextSeg = lastID + 1
	}
	return l.writeControlLocked()
}

// ScanFrom reads complete authentic records starting at start.
// A torn tail is truncated. Records at or below DurableLSN that fail
// authentication are corruption.
func (l *Log) ScanFrom(start format.LSN) ([]Record, format.LSN, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.scanLocked(start, 0, 0)
}

// ScanFromLimit is the bounded CDC/diagnostic scan. maxRecords and maxBytes
// must be positive. maxBytes counts encrypted physical record bytes; one
// oversized first record is still returned because WAL records have a global
// format-level size cap.
func (l *Log) ScanFromLimit(start format.LSN, maxRecords, maxBytes int) ([]Record, format.LSN, error) {
	if maxRecords < 1 || maxBytes < 1 {
		return nil, 0, nerr.New(nerr.InvalidArgument, "wal.ScanFromLimit", "positive limits are required")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.scanLocked(start, maxRecords, maxBytes)
}

// OldestLSN is the first LSN still present in the live WAL directory.
func (l *Log) OldestLSN() (format.LSN, error) {
	if l == nil {
		return 0, nerr.New(nerr.InvalidArgument, "wal.OldestLSN", "nil log")
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	ids, err := listSegments(l.dir)
	if err != nil {
		return 0, err
	}
	if len(ids) == 0 {
		return l.nextLSN, nil
	}
	f, hdr, _, err := openSegment(l.dir, ids[0], l.ident)
	if err != nil {
		return 0, err
	}
	_ = f.Close()
	return hdr.StartLSN, nil
}

func (l *Log) scanLocked(start format.LSN, maxRecords, maxBytes int) ([]Record, format.LSN, error) {
	ids, err := listSegments(l.dir)
	if err != nil {
		return nil, 0, err
	}
	var (
		out     []Record
		last    format.LSN
		used    int
		walKeys = l.walProvider()
	)
	if start == 0 {
		start = 1
	}
	for _, id := range ids {
		f, hdr, size, err := openSegment(l.dir, id, l.ident)
		if err != nil {
			return nil, 0, err
		}
		off := int64(SegmentHeaderSize)
		for off < size {
			if size-off < HeaderSize {
				if err := truncateSeg(f, off); err != nil {
					_ = f.Close()
					return nil, 0, err
				}
				break
			}
			hdrBuf := make([]byte, HeaderSize)
			if err := diskio.ReadFullAt(f, hdrBuf, off); err != nil {
				_ = f.Close()
				return nil, 0, err
			}
			ph, err := parseHeader(hdrBuf)
			if err != nil {
				if err := l.tailOrCorrupt(f, off, 0, err); err != nil {
					_ = f.Close()
					return nil, 0, err
				}
				break
			}
			need := int64(HeaderSize + ph.CTLen)
			if off+need > size {
				if err := l.tailOrCorrupt(f, off, ph.LSN, nerr.New(nerr.InvalidFormat, "wal.ScanFrom", "truncated record body")); err != nil {
					_ = f.Close()
					return nil, 0, err
				}
				break
			}
			if ph.LSN >= start && len(out) > 0 &&
				((maxRecords > 0 && len(out) >= maxRecords) ||
					(maxBytes > 0 && used+int(need) > maxBytes)) {
				_ = f.Close()
				return out, last, nil
			}
			ct := make([]byte, ph.CTLen)
			if err := diskio.ReadFullAt(f, ct, off+HeaderSize); err != nil {
				_ = f.Close()
				return nil, 0, err
			}
			rec, err := decodePhysical(walKeys, hdrBuf, ct)
			if err != nil {
				if err := l.tailOrCorrupt(f, off, ph.LSN, err); err != nil {
					_ = f.Close()
					return nil, 0, err
				}
				break
			}
			off += need
			if rec.LSN >= start {
				out = append(out, rec)
				used += int(need)
			}
			last = rec.LSN
			_ = hdr
		}
		_ = f.Close()
	}
	return out, last, nil
}

func (l *Log) tailOrCorrupt(f *os.File, off int64, recLSN format.LSN, cause error) error {
	if recLSN != 0 && recLSN <= l.durableLSN && l.durableLSN != 0 {
		return nerr.Wrap(nerr.Corruption, "wal.ScanFrom", "authenticated prefix is damaged", cause)
	}
	return truncateSeg(f, off)
}

func truncateSeg(f *os.File, off int64) error {
	if err := f.Truncate(off); err != nil {
		return nerr.Wrap(nerr.IO, "wal.truncate", "truncate", err)
	}
	return diskio.Sync(f)
}

func (l *Log) walProvider() crypto.KeyProvider {
	p, err := crypto.NewMemoryKeyProvider(l.dek)
	if err != nil {
		return l.keys
	}
	return p
}

// Close flushes and persists the control file. Callers must never call
// this while a record is held (AppendHeld/ReleaseHold) — Engine.replMu
// already guarantees this in production; a hold still active here would
// have its bytes, and any unrelated bytes appended after it, dropped
// rather than flushed.
func (l *Log) Close() error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var first error
	if l.seg != nil {
		if err := l.flushLocked(); err != nil && first == nil {
			first = err
		}
		if err := l.writeControlLocked(); err != nil && first == nil {
			first = err
		}
		if err := l.seg.Close(); err != nil && first == nil {
			first = nerr.Wrap(nerr.IO, "wal.Close", "close", err)
		}
		l.seg = nil
	}
	return first
}

// CrashClose discards the unsynced tail and closes without flushing.
// It simulates power loss after a process crash.
func (l *Log) CrashClose() {
	if l == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.buf = nil
	l.held = false
	l.heldOffset = 0
	l.heldLen = 0
	l.heldLSN = 0
	l.heldPrevLast = 0
	if l.seg != nil {
		_ = l.seg.Truncate(l.syncOff)
		_ = l.seg.Close()
		l.seg = nil
	}
}

func LogicalInsert(txn format.TxnID, prev format.LSN, key, value []byte) Record {
	return Record{Type: RecInsert, TxnID: txn, PrevLSN: prev, Body: encodeInsertBody(key, value)}
}

func LogicalDelete(txn format.TxnID, prev format.LSN, key []byte) Record {
	return Record{Type: RecDelete, TxnID: txn, PrevLSN: prev, Body: encodeDeleteBody(key)}
}

func LogicalUpdate(txn format.TxnID, prev format.LSN, key, value []byte) Record {
	return Record{Type: RecUpdate, TxnID: txn, PrevLSN: prev, Body: encodeInsertBody(key, value)}
}

func TreeMeta(txn format.TxnID, prev format.LSN, root format.PageID, height uint16) Record {
	return Record{Type: RecTreeMeta, TxnID: txn, PrevLSN: prev, Body: encodeTreeMeta(root, height)}
}

func AllocState(txn format.TxnID, prev format.LSN, next, head format.PageID, count uint64) Record {
	return Record{Type: RecAllocState, TxnID: txn, PrevLSN: prev, Body: encodeAllocState(next, head, count)}
}

func BeginRec(txn format.TxnID) Record {
	return Record{Type: RecBegin, TxnID: txn}
}

func CommitRec(txn format.TxnID, prev format.LSN) Record {
	return Record{Type: RecCommit, TxnID: txn, PrevLSN: prev}
}

func AbortRec(txn format.TxnID, prev format.LSN) Record {
	return Record{Type: RecAbort, TxnID: txn, PrevLSN: prev}
}

func CheckpointRec(txn format.TxnID, prev format.LSN, body CheckpointBody) Record {
	return Record{Type: RecCheckpoint, TxnID: txn, PrevLSN: prev, Body: encodeCheckpoint(body)}
}

func UndoRec(txn format.TxnID, prev format.LSN, id format.UndoID, kind uint8, page format.PageID, key []byte) Record {
	return Record{Type: RecUndo, TxnID: txn, PrevLSN: prev, PageID: page, Body: encodeUndoBody(id, kind, key)}
}

func encodeUndoBody(id format.UndoID, kind uint8, key []byte) []byte {
	buf := make([]byte, 11+len(key))
	encoding.PutU64(buf, 0, uint64(id))
	buf[8] = kind
	encoding.PutU16(buf, 9, uint16(len(key)))
	copy(buf[11:], key)
	return buf
}

func DecodeUndoBody(body []byte) (format.UndoID, uint8, []byte, error) {
	if len(body) < 11 {
		return 0, 0, nil, nerr.New(nerr.InvalidFormat, "wal.DecodeUndoBody", "truncated undo body")
	}
	klen := int(encoding.U16(body, 9))
	if 11+klen != len(body) {
		return 0, 0, nil, nerr.New(nerr.InvalidFormat, "wal.DecodeUndoBody", "invalid undo body length")
	}
	return format.UndoID(encoding.U64(body, 0)), body[8], append([]byte(nil), body[11:]...), nil
}

func DecodeTreeMeta(body []byte) (format.PageID, uint16, error) { return decodeTreeMeta(body) }
func DecodeAllocState(body []byte) (format.PageID, format.PageID, uint64, error) {
	return decodeAllocState(body)
}
func DecodeCheckpoint(body []byte) (CheckpointBody, error) { return decodeCheckpoint(body) }
