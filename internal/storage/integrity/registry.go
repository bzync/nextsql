// Package integrity implements the detect → isolate → fail safely → recover
// path for user pages. A known-corrupt page is never returned.
package integrity

import (
	"os"
	"sort"
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	magic      = "NSQI"
	version    = 1
	headerSize = 10 // magic + version + count
	entrySize  = 12 // page id + reason + flags
	maxEntries = 16384
	flagLive   = 1 << 0
)

// Reason classifies why a page was isolated. Stored values are stable.
type Reason uint16

const (
	ReasonUnknown  Reason = 0
	ReasonCrypto   Reason = 1
	ReasonChecksum Reason = 2
	ReasonFormat   Reason = 3
)

func (r Reason) String() string {
	switch r {
	case ReasonCrypto:
		return "crypto"
	case ReasonChecksum:
		return "checksum"
	case ReasonFormat:
		return "format"
	default:
		return "unknown"
	}
}

// Isolated is one quarantined page. Contents of the page are never stored.
type Isolated struct {
	PageID format.PageID
	Reason Reason
}

// Registry is the durable quarantine set next to a data file.
type Registry struct {
	mu    sync.Mutex
	path  string
	pages map[format.PageID]Reason
}

// PathFor returns the sidecar path for a data file.
func PathFor(dbPath string) string { return dbPath + ".isolated" }

// CountFile reports how many pages a sidecar currently isolates.
// Missing files are (0, false, nil). A damaged file returns an error.
func CountFile(path string) (n int, present bool, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, false, nil
		}
		return 0, true, nerr.Wrap(nerr.IO, "integrity.CountFile", "read", err)
	}
	pages, err := decodeRegistry(raw)
	if err != nil {
		return 0, true, err
	}
	return len(pages), true, nil
}

// OpenOrCreate loads path or starts empty. A missing file is empty.
// A damaged sidecar is ignored (isolation is rebuilt on the next detect).
func OpenOrCreate(path string) (*Registry, error) {
	r := &Registry{path: path, pages: make(map[format.PageID]Reason)}
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return r, nil
		}
		return nil, nerr.Wrap(nerr.IO, "integrity.Open", "read", err)
	}
	pages, err := decodeRegistry(raw)
	if err != nil {
		return r, nil
	}
	r.pages = pages
	return r, nil
}

// IsFailure reports whether err is a page-integrity failure (not I/O).
func IsFailure(err error) bool {
	return nerr.HasCode(err, nerr.Corruption) ||
		nerr.HasCode(err, nerr.Crypto) ||
		nerr.HasCode(err, nerr.InvalidFormat)
}

// ReasonOf maps a typed error to a persistable reason.
func ReasonOf(err error) Reason {
	switch {
	case nerr.HasCode(err, nerr.Crypto):
		return ReasonCrypto
	case nerr.HasCode(err, nerr.InvalidFormat):
		return ReasonFormat
	case nerr.HasCode(err, nerr.Corruption):
		return ReasonChecksum
	default:
		return ReasonUnknown
	}
}

func (r *Registry) Path() string {
	if r == nil {
		return ""
	}
	return r.path
}

// Isolate records id and persists. Isolated pages must not be served.
func (r *Registry) Isolate(id format.PageID, reason Reason) error {
	if r == nil {
		return nil
	}
	if err := id.UserData(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pages[id]; ok {
		if reason != ReasonUnknown {
			r.pages[id] = reason
		}
		return r.persistLocked()
	}
	if len(r.pages) >= maxEntries {
		return nerr.New(nerr.Exhausted, "integrity.Isolate", "too many isolated pages")
	}
	r.pages[id] = reason
	return r.persistLocked()
}

// Contains reports whether id is currently quarantined.
func (r *Registry) Contains(id format.PageID) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.pages[id]
	return ok
}

// Clear drops id after a successful repair.
func (r *Registry) Clear(id format.PageID) error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.pages[id]; !ok {
		return nil
	}
	delete(r.pages, id)
	return r.persistLocked()
}

// List returns isolated pages sorted by page id.
func (r *Registry) List() []Isolated {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Isolated, 0, len(r.pages))
	for id, reason := range r.pages {
		out = append(out, Isolated{PageID: id, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].PageID < out[j].PageID })
	return out
}

func (r *Registry) persistLocked() error {
	raw := encodeRegistry(r.pages)
	tmp := r.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "integrity.persist", "write", err)
	}
	if err := os.Rename(tmp, r.path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "integrity.persist", "rename", err)
	}
	return nil
}

func encodeRegistry(pages map[format.PageID]Reason) []byte {
	ids := make([]format.PageID, 0, len(pages))
	for id := range pages {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	buf := make([]byte, headerSize+len(ids)*entrySize+4)
	copy(buf[0:4], magic)
	encoding.PutU16(buf, 4, version)
	encoding.PutU32(buf, 6, uint32(len(ids)))
	off := headerSize
	for _, id := range ids {
		encoding.PutU64(buf, off, uint64(id))
		encoding.PutU16(buf, off+8, uint16(pages[id]))
		encoding.PutU16(buf, off+10, flagLive)
		off += entrySize
	}
	checksum.Write(buf, off)
	return buf
}

func decodeRegistry(raw []byte) (map[format.PageID]Reason, error) {
	if len(raw) < headerSize+4 {
		return nil, nerr.New(nerr.InvalidFormat, "integrity.decode", "truncated isolated-page file")
	}
	if string(raw[0:4]) != magic {
		return nil, nerr.New(nerr.InvalidFormat, "integrity.decode", "bad isolated-page magic")
	}
	if encoding.U16(raw, 4) != version {
		return nil, nerr.New(nerr.InvalidFormat, "integrity.decode", "unsupported isolated-page version")
	}
	n := int(encoding.U32(raw, 6))
	if n < 0 || n > maxEntries {
		return nil, nerr.New(nerr.InvalidFormat, "integrity.decode", "invalid isolated-page count")
	}
	need := headerSize + n*entrySize + 4
	if len(raw) != need {
		return nil, nerr.New(nerr.InvalidFormat, "integrity.decode", "isolated-page length mismatch")
	}
	if err := checksum.Verify(raw, need-4); err != nil {
		return nil, nerr.Wrap(nerr.Corruption, "integrity.decode", "checksum", err)
	}
	out := make(map[format.PageID]Reason, n)
	off := headerSize
	for i := 0; i < n; i++ {
		id := format.PageID(encoding.U64(raw, off))
		if err := id.UserData(); err != nil {
			return nil, nerr.New(nerr.Corruption, "integrity.decode", "superblock cannot be isolated")
		}
		reason := Reason(encoding.U16(raw, off+8))
		flags := encoding.U16(raw, off+10)
		off += entrySize
		if flags&flagLive == 0 {
			continue
		}
		out[id] = reason
	}
	return out, nil
}
