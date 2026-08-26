package backup

import (
	"crypto/sha256"
	"io"
	"os"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	diskio "github.com/bzync/nextsql/internal/storage/io"
)

const memberHeaderSize = 4 + 2 + 8 + 4 + 8 // magic, ver, plain, chunk, genBase

func memberAAD(name string, chunk uint32) []byte {
	// AAD binds the member name and chunk index so sealed pieces cannot be swapped.
	buf := make([]byte, 2+len(name)+4)
	encoding.PutU16(buf, 0, uint16(len(name)))
	copy(buf[2:], name)
	encoding.PutU32(buf, 2+len(name), chunk)
	return buf
}

func sealFile(dek *crypto.DEK, name string, srcPath, dstPath string, genBase uint64) (plainSize, sealedSize uint64, sum [32]byte, nextGen uint64, err error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "open source", err)
	}
	defer in.Close()
	st, err := in.Stat()
	if err != nil {
		return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "stat", err)
	}
	out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "create member", err)
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dstPath)
		}
	}()

	hdr := make([]byte, memberHeaderSize)
	encoding.PutU32(hdr, 0, MemberMagic)
	encoding.PutU16(hdr, 4, CurrentVersion)
	encoding.PutU64(hdr, 6, uint64(st.Size()))
	encoding.PutU32(hdr, 14, defaultChunk)
	encoding.PutU64(hdr, 18, genBase)
	h := sha256.New()
	if _, err := out.Write(hdr); err != nil {
		return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "write header", err)
	}
	h.Write(hdr)

	buf := make([]byte, defaultChunk)
	var chunk uint32
	var wrote int64
	gen := genBase
	for {
		n, rerr := io.ReadFull(in, buf)
		if n > 0 {
			if gen == 0 {
				return 0, 0, sum, 0, nerr.New(nerr.Internal, "backup.sealFile", "generation 0 is reserved")
			}
			nonce, ct, serr := crypto.SealBytes(dek, gen, memberAAD(name, chunk), buf[:n])
			if serr != nil {
				return 0, 0, sum, 0, serr
			}
			rec := make([]byte, 4+len(nonce)+len(ct))
			encoding.PutU32(rec, 0, uint32(len(ct)))
			copy(rec[4:], nonce)
			copy(rec[4+len(nonce):], ct)
			if _, err := out.Write(rec); err != nil {
				return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "write chunk", err)
			}
			h.Write(rec)
			wrote += int64(n)
			chunk++
			gen++
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "read", rerr)
		}
	}
	if wrote != st.Size() {
		return 0, 0, sum, 0, nerr.New(nerr.Corruption, "backup.sealFile", "source size changed during copy")
	}
	if err := diskio.Sync(out); err != nil {
		return 0, 0, sum, 0, err
	}
	stOut, err := out.Stat()
	if err != nil {
		return 0, 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealFile", "stat member", err)
	}
	ok = true
	copy(sum[:], h.Sum(nil))
	return uint64(st.Size()), uint64(stOut.Size()), sum, gen, nil
}

func sealBytes(dek *crypto.DEK, name string, plain []byte, dstPath string, genBase uint64) (sealedSize uint64, sum [32]byte, nextGen uint64, err error) {
	tmp, err := os.CreateTemp("", "nextsql-backup-")
	if err != nil {
		return 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealBytes", "temp", err)
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(plain); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealBytes", "write temp", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return 0, sum, 0, nerr.Wrap(nerr.IO, "backup.sealBytes", "close temp", err)
	}
	defer os.Remove(tmpName)
	_, sealed, sum, next, err := sealFile(dek, name, tmpName, dstPath, genBase)
	return sealed, sum, next, err
}

func openMember(dek *crypto.DEK, name, srcPath, dstPath string) (plainSize uint64, err error) {
	in, err := os.Open(srcPath)
	if err != nil {
		return 0, nerr.Wrap(nerr.IO, "backup.openMember", "open", err)
	}
	defer in.Close()
	hdr := make([]byte, memberHeaderSize)
	if _, err := io.ReadFull(in, hdr); err != nil {
		return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "truncated member header")
	}
	if encoding.U32(hdr, 0) != MemberMagic {
		return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "bad member magic")
	}
	if encoding.U16(hdr, 4) != CurrentVersion {
		return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "unsupported member version")
	}
	wantPlain := encoding.U64(hdr, 6)
	chunkSize := encoding.U32(hdr, 14)
	if chunkSize == 0 || chunkSize > maxChunkSize {
		return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "invalid chunk size")
	}
	if encoding.U64(hdr, 18) == 0 {
		return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "generation 0 is reserved")
	}

	out, err := os.OpenFile(dstPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, nerr.Wrap(nerr.IO, "backup.openMember", "create", err)
	}
	ok := false
	defer func() {
		_ = out.Close()
		if !ok {
			_ = os.Remove(dstPath)
		}
	}()

	var (
		got   uint64
		chunk uint32
	)
	lenBuf := make([]byte, 4)
	nonce := make([]byte, 12)
	for got < wantPlain {
		if _, err := io.ReadFull(in, lenBuf); err != nil {
			return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "truncated chunk length")
		}
		ctLen := encoding.U32(lenBuf, 0)
		if ctLen == 0 || ctLen > maxChunkSize+16+16 {
			return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "chunk length exceeds limit")
		}
		if _, err := io.ReadFull(in, nonce); err != nil {
			return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "truncated nonce")
		}
		ct := make([]byte, ctLen)
		if _, err := io.ReadFull(in, ct); err != nil {
			return 0, nerr.New(nerr.InvalidFormat, "backup.openMember", "truncated ciphertext")
		}
		plain, oerr := crypto.OpenBytes(dek, nonce, memberAAD(name, chunk), ct)
		if oerr != nil {
			return 0, oerr
		}
		if _, err := out.Write(plain); err != nil {
			return 0, nerr.Wrap(nerr.IO, "backup.openMember", "write", err)
		}
		got += uint64(len(plain))
		chunk++
	}
	if got != wantPlain {
		return 0, nerr.New(nerr.Corruption, "backup.openMember", "decrypted size mismatch")
	}
	if err := diskio.Sync(out); err != nil {
		return 0, err
	}
	ok = true
	return wantPlain, nil
}

func fileSHA256(path string) ([32]byte, int64, error) {
	var sum [32]byte
	f, err := os.Open(path)
	if err != nil {
		return sum, 0, nerr.Wrap(nerr.IO, "backup.fileSHA256", "open", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return sum, 0, nerr.Wrap(nerr.IO, "backup.fileSHA256", "read", err)
	}
	copy(sum[:], h.Sum(nil))
	return sum, n, nil
}
