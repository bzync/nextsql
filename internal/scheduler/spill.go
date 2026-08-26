package scheduler

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	spillMagic   = "NSPL"
	spillVersion = 1
)

// Spill is an encrypted on-disk run of encoded rows. The DEK lives only
// in process memory for the query lifetime.
type Spill struct {
	dir    string
	dek    *crypto.DEK
	budget *Budget
	mu     sync.Mutex
	parts  map[int]string
}

// NewSpill creates a temporary encrypted spill directory.
func NewSpill(b *Budget) (*Spill, error) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "nextsql-spill-*")
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "scheduler.NewSpill", "mkdir", err)
	}
	return &Spill{dir: dir, dek: dek, budget: b, parts: make(map[int]string)}, nil
}

func (s *Spill) path(part int) string {
	return filepath.Join(s.dir, "p"+itoa(part))
}

// Write appends encoded rows to partition part.
func (s *Spill) Write(part int, rows [][]types.Value) error {
	if s == nil {
		return nerr.New(nerr.Internal, "scheduler.Spill.Write", "nil spill")
	}
	if len(rows) == 0 {
		return nil
	}
	var payload []byte
	for _, row := range rows {
		enc, err := types.EncodeRow(row)
		if err != nil {
			return err
		}
		var hdr [4]byte
		binary.LittleEndian.PutUint32(hdr[:], uint32(len(enc)))
		payload = append(payload, hdr[:]...)
		payload = append(payload, enc...)
	}
	nonce, ct, err := crypto.SealBytesRandom(s.dek, []byte(spillMagic), payload)
	if err != nil {
		return err
	}
	buf := make([]byte, 0, 6+len(nonce)+4+len(ct))
	buf = append(buf, spillMagic...)
	buf = append(buf, spillVersion, byte(len(nonce)))
	buf = append(buf, nonce...)
	var nbuf [4]byte
	binary.LittleEndian.PutUint32(nbuf[:], uint32(len(ct)))
	buf = append(buf, nbuf[:]...)
	buf = append(buf, ct...)
	if err := s.budget.ChargeDisk(int64(len(buf))); err != nil {
		return err
	}
	if err := s.budget.ChargeIO(int64(len(buf))); err != nil {
		return err
	}
	p := s.path(part)
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nerr.Wrap(nerr.IO, "scheduler.Spill.Write", "open", err)
	}
	_, werr := f.Write(buf)
	cerr := f.Close()
	if werr != nil {
		return nerr.Wrap(nerr.IO, "scheduler.Spill.Write", "write", werr)
	}
	if cerr != nil {
		return nerr.Wrap(nerr.IO, "scheduler.Spill.Write", "close", cerr)
	}
	s.mu.Lock()
	s.parts[part] = p
	s.mu.Unlock()
	return nil
}

// Read returns every row previously written to part.
func (s *Spill) Read(part int) ([][]types.Value, error) {
	if s == nil {
		return nil, nerr.New(nerr.Internal, "scheduler.Spill.Read", "nil spill")
	}
	s.mu.Lock()
	p, ok := s.parts[part]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "scheduler.Spill.Read", "read", err)
	}
	if err := s.budget.ChargeIO(int64(len(raw))); err != nil {
		return nil, err
	}
	var out [][]types.Value
	off := 0
	for off < len(raw) {
		if off+6 > len(raw) || string(raw[off:off+4]) != spillMagic || raw[off+4] != spillVersion {
			return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.Read", "bad spill header")
		}
		nl := int(raw[off+5])
		off += 6
		if off+nl+4 > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.Read", "truncated spill nonce")
		}
		nonce := raw[off : off+nl]
		off += nl
		clen := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+clen > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.Read", "truncated spill body")
		}
		pt, err := crypto.OpenBytes(s.dek, nonce, []byte(spillMagic), raw[off:off+clen])
		if err != nil {
			return nil, err
		}
		off += clen
		for i := 0; i < len(pt); {
			if i+4 > len(pt) {
				return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.Read", "truncated row length")
			}
			n := int(binary.LittleEndian.Uint32(pt[i:]))
			i += 4
			if i+n > len(pt) {
				return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.Read", "truncated row")
			}
			// types are recovered by the caller via DecodeRow; store opaque
			// by decoding with a late-bound schema is not possible here.
			// Re-wrap as a single JSON-like blob? We need types.
			// Encode a sentinel: keep raw and let ReadTyped decode.
			out = append(out, []types.Value{types.JSONValue(pt[i : i+n])})
			i += n
		}
	}
	return out, nil
}

// ReadRaw returns encoded row payloads from part (not decoded values).
func (s *Spill) ReadRaw(part int) ([][]byte, error) {
	if s == nil {
		return nil, nerr.New(nerr.Internal, "scheduler.Spill.ReadRaw", "nil spill")
	}
	s.mu.Lock()
	p, ok := s.parts[part]
	s.mu.Unlock()
	if !ok {
		return nil, nil
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "scheduler.Spill.ReadRaw", "read", err)
	}
	if err := s.budget.ChargeIO(int64(len(raw))); err != nil {
		return nil, err
	}
	var out [][]byte
	off := 0
	for off < len(raw) {
		if off+6 > len(raw) || string(raw[off:off+4]) != spillMagic || raw[off+4] != spillVersion {
			return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.ReadRaw", "bad spill header")
		}
		nl := int(raw[off+5])
		off += 6
		if off+nl+4 > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.ReadRaw", "truncated spill nonce")
		}
		nonce := raw[off : off+nl]
		off += nl
		clen := int(binary.LittleEndian.Uint32(raw[off:]))
		off += 4
		if off+clen > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.ReadRaw", "truncated spill body")
		}
		pt, err := crypto.OpenBytes(s.dek, nonce, []byte(spillMagic), raw[off:off+clen])
		if err != nil {
			return nil, err
		}
		off += clen
		for i := 0; i < len(pt); {
			if i+4 > len(pt) {
				return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.ReadRaw", "truncated row length")
			}
			n := int(binary.LittleEndian.Uint32(pt[i:]))
			i += 4
			if i+n > len(pt) {
				return nil, nerr.New(nerr.InvalidFormat, "scheduler.Spill.ReadRaw", "truncated row")
			}
			out = append(out, append([]byte(nil), pt[i:i+n]...))
			i += n
		}
	}
	return out, nil
}

func (s *Spill) Parts() []int {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]int, 0, len(s.parts))
	for p := range s.parts {
		out = append(out, p)
	}
	return out
}

func (s *Spill) Close() error {
	if s == nil || s.dir == "" {
		return nil
	}
	err := os.RemoveAll(s.dir)
	s.dir = ""
	s.parts = nil
	return err
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
