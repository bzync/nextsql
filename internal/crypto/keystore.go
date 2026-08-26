package crypto

import (
	"os"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	keystoreMagic   = "NSKS"
	keystoreVersion = 1
	shredMagic      = "NSSH"
	maxWrapBlob     = 1 << 16
	maxDomainKeys   = 256
)

type keystore struct {
	Identity      format.Identity
	KEKVersion    format.KeyVersion
	MasterVersion format.KeyVersion
	NonceHigh     uint64
	WrappedKEK    []byte
	WrappedMaster []byte
	Domains       []persistedDomain
	Shredded      bool
}

type persistedDomain struct {
	Domain  byte
	Current format.KeyVersion
	Keys    []persistedKey
}

type persistedKey struct {
	Version format.KeyVersion
	Flags   byte
	Wrap    []byte
}

func encodeKeystore(ks keystore) ([]byte, error) {
	if len(ks.WrappedKEK) > maxWrapBlob || len(ks.WrappedMaster) > maxWrapBlob {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.encodeKeystore", "wrapped key too large")
	}
	if len(ks.Domains) > len(AllDomains)+2 {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.encodeKeystore", "too many domains")
	}
	n := 4 + 2 + 2 + 32 + 4 + 4 + 8 + 2 + len(ks.WrappedKEK) + 2 + len(ks.WrappedMaster) + 2
	for _, d := range ks.Domains {
		n += 1 + 4 + 2
		if len(d.Keys) > maxDomainKeys {
			return nil, nerr.New(nerr.InvalidFormat, "crypto.encodeKeystore", "too many key versions")
		}
		for _, k := range d.Keys {
			if len(k.Wrap) > maxWrapBlob {
				return nil, nerr.New(nerr.InvalidFormat, "crypto.encodeKeystore", "wrapped key too large")
			}
			n += 4 + 1 + 2 + len(k.Wrap)
		}
	}
	n += 4
	buf := make([]byte, n)
	copy(buf[0:4], keystoreMagic)
	encoding.PutU16(buf, 4, keystoreVersion)
	encoding.PutU16(buf, 6, 0)
	copy(buf[8:24], ks.Identity.Database[:])
	copy(buf[24:40], ks.Identity.File[:])
	encoding.PutU32(buf, 40, uint32(ks.KEKVersion))
	encoding.PutU32(buf, 44, uint32(ks.MasterVersion))
	encoding.PutU64(buf, 48, ks.NonceHigh)
	encoding.PutU16(buf, 56, uint16(len(ks.WrappedKEK)))
	off := 58
	copy(buf[off:], ks.WrappedKEK)
	off += len(ks.WrappedKEK)
	encoding.PutU16(buf, off, uint16(len(ks.WrappedMaster)))
	off += 2
	copy(buf[off:], ks.WrappedMaster)
	off += len(ks.WrappedMaster)
	encoding.PutU16(buf, off, uint16(len(ks.Domains)))
	off += 2
	for _, d := range ks.Domains {
		buf[off] = d.Domain
		off++
		encoding.PutU32(buf, off, uint32(d.Current))
		off += 4
		encoding.PutU16(buf, off, uint16(len(d.Keys)))
		off += 2
		for _, k := range d.Keys {
			encoding.PutU32(buf, off, uint32(k.Version))
			off += 4
			buf[off] = k.Flags
			off++
			encoding.PutU16(buf, off, uint16(len(k.Wrap)))
			off += 2
			copy(buf[off:], k.Wrap)
			off += len(k.Wrap)
		}
	}
	if off+4 != len(buf) {
		return nil, nerr.New(nerr.Internal, "crypto.encodeKeystore", "encoded length mismatch")
	}
	checksum.Write(buf, off)
	return buf, nil
}

func decodeKeystore(raw []byte) (keystore, error) {
	if len(raw) >= 4 && string(raw[0:4]) == shredMagic {
		return keystore{Shredded: true}, nil
	}
	if len(raw) < 62 {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated keystore")
	}
	if string(raw[0:4]) != keystoreMagic {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "bad keystore magic")
	}
	if encoding.U16(raw, 4) != keystoreVersion {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "unsupported keystore version")
	}
	if err := checksum.Verify(raw, len(raw)-4); err != nil {
		return keystore{}, nerr.Wrap(nerr.Corruption, "crypto.decodeKeystore", "checksum", err)
	}
	var ks keystore
	copy(ks.Identity.Database[:], raw[8:24])
	copy(ks.Identity.File[:], raw[24:40])
	ks.KEKVersion = format.KeyVersion(encoding.U32(raw, 40))
	ks.MasterVersion = format.KeyVersion(encoding.U32(raw, 44))
	ks.NonceHigh = encoding.U64(raw, 48)
	off := 56
	kekLen, err := encoding.ReadU16(raw, off)
	if err != nil {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated KEK wrap")
	}
	off += 2
	if int(kekLen) > maxWrapBlob {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "KEK wrap exceeds limit")
	}
	ks.WrappedKEK, err = encoding.ReadBytes(raw, off, int(kekLen))
	if err != nil {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated KEK wrap")
	}
	off += int(kekLen)
	mstLen, err := encoding.ReadU16(raw, off)
	if err != nil {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated master wrap")
	}
	off += 2
	if int(mstLen) > maxWrapBlob {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "master wrap exceeds limit")
	}
	ks.WrappedMaster, err = encoding.ReadBytes(raw, off, int(mstLen))
	if err != nil {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated master wrap")
	}
	off += int(mstLen)
	nd, err := encoding.ReadU16(raw, off)
	if err != nil {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated domain count")
	}
	off += 2
	if int(nd) > len(AllDomains)+8 {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "domain count exceeds limit")
	}
	ks.Domains = make([]persistedDomain, 0, nd)
	for i := uint16(0); i < nd; i++ {
		if off >= len(raw)-4 {
			return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated domain")
		}
		var d persistedDomain
		d.Domain = raw[off]
		off++
		cur, err := encoding.ReadU32(raw, off)
		if err != nil {
			return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated domain current")
		}
		d.Current = format.KeyVersion(cur)
		off += 4
		nk, err := encoding.ReadU16(raw, off)
		if err != nil {
			return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated version count")
		}
		off += 2
		if int(nk) > maxDomainKeys {
			return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "version count exceeds limit")
		}
		for j := uint16(0); j < nk; j++ {
			ver, err := encoding.ReadU32(raw, off)
			if err != nil {
				return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated key version")
			}
			off += 4
			if off >= len(raw)-4 {
				return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated key flags")
			}
			fl := raw[off]
			off++
			wl, err := encoding.ReadU16(raw, off)
			if err != nil {
				return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated wrap length")
			}
			off += 2
			if int(wl) > maxWrapBlob {
				return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "wrap exceeds limit")
			}
			wrap, err := encoding.ReadBytes(raw, off, int(wl))
			if err != nil {
				return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "truncated wrap")
			}
			off += int(wl)
			d.Keys = append(d.Keys, persistedKey{Version: format.KeyVersion(ver), Flags: fl, Wrap: wrap})
		}
		ks.Domains = append(ks.Domains, d)
	}
	if off != len(raw)-4 {
		return keystore{}, nerr.New(nerr.InvalidFormat, "crypto.decodeKeystore", "trailing keystore bytes")
	}
	return ks, nil
}

func readKeystore(path string) (keystore, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return keystore{}, nerr.Wrap(nerr.IO, "crypto.readKeystore", "read", err)
	}
	return decodeKeystore(raw)
}

func writeShredded(path string) error {
	buf := make([]byte, 16)
	copy(buf[0:4], shredMagic)
	encoding.PutU16(buf, 4, 1)
	checksum.Write(buf, 12)
	return writeAtomic(path, buf)
}

func writeAtomic(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "crypto.writeAtomic", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "crypto.writeAtomic", "rename", err)
	}
	return nil
}
