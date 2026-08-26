package file

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	diskio "github.com/bzync/nextsql/internal/storage/io"
	"github.com/bzync/nextsql/internal/storage/page"
)

var physBufPool = sync.Pool{
	New: func() any {
		b := make([]byte, format.PhysicalPageSize)
		return &b
	},
}

// Manager owns a single database file and the encrypted page I/O path.
type Manager struct {
	path string
	f    *os.File
	keys crypto.KeyProvider
	mu   sync.Mutex
	sb   Superblock

	genCursor uint64
	genLimit  uint64
}

func Create(path string, id format.Identity, keys crypto.KeyProvider) (*Manager, error) {
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "file.Create", "nil key provider")
	}
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "file.Create", "database file exists")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return nil, nerr.Wrap(nerr.IO, "file.Create", "mkdir", err)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "file.Create", "create", err)
	}
	dek, err := keys.Current()
	if err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	sb := newSuperblock(id, dek.Version)
	m := &Manager{
		path:      path,
		f:         f,
		keys:      keys,
		sb:        sb,
		genCursor: 1,
		genLimit:  sb.NextNonceGeneration,
	}
	if err := m.writeSuperblockLocked(true); err != nil {
		_ = f.Close()
		_ = os.Remove(path)
		return nil, err
	}
	if err := diskio.SyncDir(filepath.Dir(path)); err != nil && filepath.Dir(path) != "." {
		_ = f.Close()
		return nil, err
	}
	return m, nil
}

func Open(path string, keys crypto.KeyProvider) (*Manager, error) {
	if keys == nil {
		return nil, nerr.New(nerr.InvalidArgument, "file.Open", "nil key provider")
	}
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "file.Open", "open", err)
	}
	raw := make([]byte, format.PhysicalPageSize)
	if err := diskio.ReadFullAt(f, raw, 0); err != nil {
		_ = f.Close()
		return nil, err
	}
	sb, auth, err := decodeSuperblock(raw)
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	dek, err := keys.Key(sb.KeyVersion)
	if err != nil {
		_ = f.Close()
		return nil, nerr.Wrap(nerr.Crypto, "file.Open", "key for file version", err)
	}
	if err := crypto.VerifyFileAuthTag(dek, sb.Identity, auth); err != nil {
		_ = f.Close()
		return nil, err
	}
	m := &Manager{path: path, f: f, keys: keys, sb: sb}
	// Reserve a fresh nonce batch so crash-replay cannot reuse a generation.
	if err := m.reserveNonceBatchLocked(); err != nil {
		_ = f.Close()
		return nil, err
	}
	return m, nil
}

func (m *Manager) Path() string { return m.path }

func (m *Manager) Keys() crypto.KeyProvider { return m.keys }

// AdoptCurrentKey updates the superblock key version and file-auth tag.
func (m *Manager) AdoptCurrentKey() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dek, err := m.keys.Current()
	if err != nil {
		return err
	}
	m.sb.KeyVersion = dek.Version
	return m.writeSuperblockLocked(true)
}

// AdvanceNonceTo raises the durable nonce high-water without handing out generations.
func (m *Manager) AdvanceNonceTo(n uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if n <= m.sb.NextNonceGeneration {
		return nil
	}
	m.sb.NextNonceGeneration = n
	return m.writeSuperblockLocked(true)
}

// Reencrypt re-seals every allocated page under the current DEK.
func (m *Manager) Reencrypt() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dek, err := m.keys.Current()
	if err != nil {
		return err
	}
	next := m.sb.NextPageID
	for id := format.FirstAllocPageID; id < next; id++ {
		raw := make([]byte, format.PhysicalPageSize)
		if err := diskio.ReadFullAt(m.f, raw, format.PhysicalOffset(id)); err != nil {
			return err
		}
		if zeroPage(raw) {
			continue
		}
		logical, err := crypto.OpenPage(m.keys, id, raw)
		if err != nil {
			return err
		}
		gen, err := m.nextGenerationLocked()
		if err != nil {
			return err
		}
		physical, err := crypto.SealPage(dek, id, gen, logical)
		if err != nil {
			return err
		}
		if err := diskio.WriteFullAt(m.f, physical, format.PhysicalOffset(id)); err != nil {
			return err
		}
	}
	m.sb.KeyVersion = dek.Version
	return m.writeSuperblockLocked(true)
}

func zeroPage(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

func (m *Manager) Identity() format.Identity {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sb.Identity
}

func (m *Manager) Superblock() Superblock {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sb
}

func (m *Manager) ReadLogical(id format.PageID) ([]byte, error) {
	dst := make([]byte, format.LogicalPageSize)
	if err := m.ReadLogicalInto(id, dst); err != nil {
		return nil, err
	}
	return dst, nil
}

// ReadLogicalInto decrypts page id into dst. dst must be one logical page.
// The file lock is not held across ReadAt or AEAD so concurrent misses can
// decrypt in parallel. os.File.ReadAt is safe for concurrent use.
func (m *Manager) ReadLogicalInto(id format.PageID, dst []byte) error {
	if err := id.UserData(); err != nil {
		return err
	}
	if len(dst) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidArgument, "file.ReadLogical", "logical destination has wrong size")
	}
	m.mu.Lock()
	f := m.f
	m.mu.Unlock()
	if f == nil {
		return nerr.New(nerr.IO, "file.ReadLogical", "file is closed")
	}
	rawp := physBufPool.Get().(*[]byte)
	raw := (*rawp)[:format.PhysicalPageSize]
	err := diskio.ReadFullAt(f, raw, format.PhysicalOffset(id))
	if err != nil {
		physBufPool.Put(rawp)
		return err
	}
	err = crypto.OpenPageInto(m.keys, id, raw, dst)
	physBufPool.Put(rawp)
	if err != nil {
		return err
	}
	if err := page.VerifyChecksum(dst); err != nil {
		return err
	}
	return page.CheckID(dst, id)
}

func (m *Manager) WriteLogical(id format.PageID, logical []byte) error {
	if err := id.UserData(); err != nil {
		return err
	}
	if len(logical) != format.LogicalPageSize {
		return nerr.New(nerr.InvalidArgument, "file.WriteLogical", "logical page has wrong size")
	}
	if page.IDOf(logical) != id {
		return nerr.New(nerr.Corruption, "file.WriteLogical", "page id mismatch")
	}
	page.FinalizeBuf(logical)

	m.mu.Lock()
	dek, err := m.keys.Current()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	gen, err := m.nextGenerationLocked()
	if err != nil {
		m.mu.Unlock()
		return err
	}
	f := m.f
	m.mu.Unlock()
	if f == nil {
		return nerr.New(nerr.IO, "file.WriteLogical", "file is closed")
	}

	rawp := physBufPool.Get().(*[]byte)
	physical, err := crypto.SealPageInto(dek, id, gen, logical, (*rawp)[:0])
	if err != nil {
		physBufPool.Put(rawp)
		return err
	}
	err = diskio.WriteFullAt(f, physical, format.PhysicalOffset(id))
	physBufPool.Put(rawp)
	return err
}

const capacityAhead = 16384 // ~256 MiB of physical pages

// EnsureCapacity preallocates the data file through page next (exclusive)
// plus a slack of 1024 pages so bulk Alloc does not extend on every write.
func (m *Manager) EnsureCapacity(next format.PageID) error {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	f := m.f
	m.mu.Unlock()
	if f == nil {
		return nil
	}
	end := next + capacityAhead
	return diskio.Preallocate(f, format.PhysicalOffset(end))
}

func (m *Manager) AllocState() (next format.PageID, freeHead format.PageID, freeCount uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sb.NextPageID, m.sb.FreeListHead, m.sb.FreeCount
}

func (m *Manager) SetAllocState(next, freeHead format.PageID, freeCount uint64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sb.NextPageID = next
	m.sb.FreeListHead = freeHead
	m.sb.FreeCount = freeCount
	return m.writeSuperblockLocked(true)
}

// SetAllocStateMem updates the in-memory superblock only. Flush the
// allocator to persist freelist pages and the superblock.
func (m *Manager) SetAllocStateMem(next, freeHead format.PageID, freeCount uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sb.NextPageID = next
	m.sb.FreeListHead = freeHead
	m.sb.FreeCount = freeCount
}

func (m *Manager) PrimaryTree() (root format.PageID, height uint16) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sb.PrimaryRoot, m.sb.PrimaryHeight
}

func (m *Manager) SetPrimaryTree(root format.PageID, height uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validatePrimaryTree(root, height, m.sb.NextPageID); err != nil {
		return err
	}
	m.sb.PrimaryRoot = root
	m.sb.PrimaryHeight = height
	return m.writeSuperblockLocked(true)
}

// SetPrimaryTreeMem updates the in-memory superblock only. The WAL covers
// durability until the next checkpoint writes the superblock.
func (m *Manager) SetPrimaryTreeMem(root format.PageID, height uint16) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := validatePrimaryTree(root, height, m.sb.NextPageID); err != nil {
		return err
	}
	m.sb.PrimaryRoot = root
	m.sb.PrimaryHeight = height
	return nil
}

func (m *Manager) SetCheckpoint(cp, redo format.LSN) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sb.CheckpointLSN = cp
	m.sb.RedoLSN = redo
	return m.writeSuperblockLocked(true)
}

func (m *Manager) Sync() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return diskio.Sync(m.f)
}

func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.f == nil {
		return nil
	}
	if err := diskio.Sync(m.f); err != nil {
		_ = m.f.Close()
		m.f = nil
		return err
	}
	err := m.f.Close()
	m.f = nil
	if err != nil {
		return nerr.Wrap(nerr.IO, "file.Close", "close", err)
	}
	return nil
}

// CrashClose closes the file descriptor without flushing or syncing.
func (m *Manager) CrashClose() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.f != nil {
		_ = m.f.Close()
		m.f = nil
	}
}

func (m *Manager) nextGenerationLocked() (uint64, error) {
	if m.genCursor >= m.genLimit {
		if err := m.reserveNonceBatchLocked(); err != nil {
			return 0, err
		}
	}
	g := m.genCursor
	m.genCursor++
	return g, nil
}

func (m *Manager) reserveNonceBatchLocked() error {
	// Advance the durable high-water mark first, then hand out the reserved range.
	start := m.sb.NextNonceGeneration
	m.sb.NextNonceGeneration = start + nonceBatch
	if err := m.writeSuperblockLocked(true); err != nil {
		m.sb.NextNonceGeneration = start
		return err
	}
	m.genCursor = start
	if m.genCursor == 0 {
		m.genCursor = 1
	}
	m.genLimit = m.sb.NextNonceGeneration
	return nil
}

func (m *Manager) writeSuperblockLocked(sync bool) error {
	dek, err := m.keys.Key(m.sb.KeyVersion)
	if err != nil {
		return err
	}
	tag := crypto.FileAuthTag(dek, m.sb.Identity)
	raw := encodeSuperblock(m.sb, tag[:])
	if err := diskio.WriteFullAt(m.f, raw, 0); err != nil {
		return err
	}
	if sync {
		return diskio.Sync(m.f)
	}
	return nil
}
