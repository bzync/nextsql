package xport

import (
	"crypto/sha256"
	"io"
	"os"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const (
	payloadHeaderSize = 4 + 2 + 8 + 4 + 8 // magic, ver, plain, chunk, genBase
	nonceLen          = 12
)

func payloadAAD(chunk uint32) []byte {
	buf := make([]byte, 2+len(payloadName)+4)
	encoding.PutU16(buf, 0, uint16(len(payloadName)))
	copy(buf[2:], payloadName)
	encoding.PutU32(buf, 2+len(payloadName), chunk)
	return buf
}

type sealer struct {
	out   *os.File
	dek   *crypto.DEK
	buf   []byte
	gen   uint64
	chunk uint32
	plain uint64
}

func newSealer(path string, dek *crypto.DEK, genBase uint64) (*sealer, error) {
	if dek == nil {
		return nil, nerr.New(nerr.InvalidArgument, "xport.newSealer", "nil DEK")
	}
	if genBase == 0 {
		return nil, nerr.New(nerr.Internal, "xport.newSealer", "generation 0 is reserved")
	}
	out, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "xport.newSealer", "create payload", err)
	}
	hdr := make([]byte, payloadHeaderSize)
	encoding.PutU32(hdr, 0, PayloadMagic)
	encoding.PutU16(hdr, 4, CurrentVersion)
	encoding.PutU32(hdr, 14, defaultChunk)
	encoding.PutU64(hdr, 18, genBase)
	if _, err := out.Write(hdr); err != nil {
		_ = out.Close()
		_ = os.Remove(path)
		return nil, nerr.Wrap(nerr.IO, "xport.newSealer", "write header", err)
	}
	return &sealer{out: out, dek: dek, gen: genBase, buf: make([]byte, 0, defaultChunk)}, nil
}

func (s *sealer) Write(p []byte) error {
	if s == nil || s.out == nil {
		return nerr.New(nerr.Internal, "xport.sealer.Write", "closed")
	}
	s.buf = append(s.buf, p...)
	s.plain += uint64(len(p))
	for len(s.buf) >= defaultChunk {
		if err := s.flush(defaultChunk); err != nil {
			return err
		}
	}
	return nil
}

func (s *sealer) flush(n int) error {
	if n <= 0 {
		return nil
	}
	if s.gen == 0 {
		return nerr.New(nerr.Internal, "xport.sealer.flush", "generation 0 is reserved")
	}
	plain := s.buf[:n]
	nonce, ct, err := crypto.SealBytes(s.dek, s.gen, payloadAAD(s.chunk), plain)
	if err != nil {
		return err
	}
	rec := make([]byte, 4+len(nonce)+len(ct))
	encoding.PutU32(rec, 0, uint32(len(ct)))
	copy(rec[4:], nonce)
	copy(rec[4+len(nonce):], ct)
	if _, err := s.out.Write(rec); err != nil {
		return nerr.Wrap(nerr.IO, "xport.sealer.flush", "write chunk", err)
	}
	s.buf = append(s.buf[:0], s.buf[n:]...)
	s.chunk++
	s.gen++
	return nil
}

func (s *sealer) Close() (plain, sealed uint64, sum [32]byte, err error) {
	if s == nil || s.out == nil {
		return 0, 0, sum, nerr.New(nerr.Internal, "xport.sealer.Close", "closed")
	}
	defer func() {
		_ = s.out.Close()
		s.out = nil
	}()
	if len(s.buf) > 0 {
		if err := s.flush(len(s.buf)); err != nil {
			return 0, 0, sum, err
		}
	}
	// Patch plaintext size into the payload header.
	if _, err := s.out.Seek(6, io.SeekStart); err != nil {
		return 0, 0, sum, nerr.Wrap(nerr.IO, "xport.sealer.Close", "seek", err)
	}
	var sz [8]byte
	encoding.PutU64(sz[:], 0, s.plain)
	if _, err := s.out.Write(sz[:]); err != nil {
		return 0, 0, sum, nerr.Wrap(nerr.IO, "xport.sealer.Close", "patch size", err)
	}
	if err := diskio.Sync(s.out); err != nil {
		return 0, 0, sum, err
	}
	st, err := s.out.Stat()
	if err != nil {
		return 0, 0, sum, nerr.Wrap(nerr.IO, "xport.sealer.Close", "stat", err)
	}
	// Recompute SHA-256 over the finished file (header now has the size).
	if _, err := s.out.Seek(0, io.SeekStart); err != nil {
		return 0, 0, sum, nerr.Wrap(nerr.IO, "xport.sealer.Close", "rewind", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, s.out); err != nil {
		return 0, 0, sum, nerr.Wrap(nerr.IO, "xport.sealer.Close", "hash", err)
	}
	copy(sum[:], h.Sum(nil))
	return s.plain, uint64(st.Size()), sum, nil
}

func openPayload(dek *crypto.DEK, srcPath string) ([]byte, error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "xport.openPayload", "open", err)
	}
	defer in.Close()
	hdr := make([]byte, payloadHeaderSize)
	if _, err := io.ReadFull(in, hdr); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "truncated payload header")
	}
	if encoding.U32(hdr, 0) != PayloadMagic {
		return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "bad payload magic")
	}
	if encoding.U16(hdr, 4) != CurrentVersion {
		return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "unsupported payload version")
	}
	wantPlain := encoding.U64(hdr, 6)
	chunkSize := encoding.U32(hdr, 14)
	if chunkSize == 0 || chunkSize > maxChunkSize {
		return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "invalid chunk size")
	}
	if encoding.U64(hdr, 18) == 0 {
		return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "generation 0 is reserved")
	}
	if wantPlain > 1<<32 {
		return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "payload exceeds size limit")
	}
	var (
		out   []byte
		got   uint64
		chunk uint32
	)
	lenBuf := make([]byte, 4)
	nonce := make([]byte, nonceLen)
	for got < wantPlain {
		if _, err := io.ReadFull(in, lenBuf); err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "truncated chunk length")
		}
		ctLen := encoding.U32(lenBuf, 0)
		if ctLen == 0 || ctLen > maxChunkSize+16+16 {
			return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "chunk length exceeds limit")
		}
		if _, err := io.ReadFull(in, nonce); err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "truncated nonce")
		}
		ct := make([]byte, ctLen)
		if _, err := io.ReadFull(in, ct); err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "xport.openPayload", "truncated ciphertext")
		}
		plain, oerr := crypto.OpenBytes(dek, nonce, payloadAAD(chunk), ct)
		if oerr != nil {
			return nil, oerr
		}
		out = append(out, plain...)
		got += uint64(len(plain))
		chunk++
	}
	if got != wantPlain {
		return nil, nerr.New(nerr.Corruption, "xport.openPayload", "decrypted size mismatch")
	}
	return out, nil
}

func fileSHA256(path string) ([32]byte, int64, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, 0, nerr.Wrap(nerr.IO, "xport.fileSHA256", "open", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return sum, 0, nerr.Wrap(nerr.IO, "xport.fileSHA256", "read", err)
	}
	copy(sum[:], h.Sum(nil))
	return sum, n, nil
}
