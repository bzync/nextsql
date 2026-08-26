package crypto

import (
	"os"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	keyFileMagic   = "NSKY"
	keyFileVersion = 1
	keyFileSize    = 4 + 2 + 4 + AES256KeySize + 4
	keyFileSumOff  = 4 + 2 + 4 + AES256KeySize
)

// WriteKeyFile writes a DEK to path with mode 0600. The file is not a connection URL and must stay off the data volume in production.
func WriteKeyFile(path string, dek *DEK) error {
	if dek == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.WriteKeyFile", "nil DEK")
	}
	buf := make([]byte, keyFileSize)
	copy(buf[0:4], keyFileMagic)
	encoding.PutU16(buf, 4, keyFileVersion)
	encoding.PutU32(buf, 6, uint32(dek.Version))
	copy(buf[10:10+AES256KeySize], dek.keyBytes())
	checksum.Write(buf, keyFileSumOff)
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "crypto.WriteKeyFile", "write", err)
	}
	return nil
}

func ReadKeyFile(path string) (*DEK, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "crypto.ReadKeyFile", "read", err)
	}
	if len(buf) != keyFileSize {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.ReadKeyFile", "wrong key file size")
	}
	if string(buf[0:4]) != keyFileMagic {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.ReadKeyFile", "bad key file magic")
	}
	if encoding.U16(buf, 4) != keyFileVersion {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.ReadKeyFile", "unsupported key file version")
	}
	if err := checksum.Verify(buf, keyFileSumOff); err != nil {
		return nil, nerr.Wrap(nerr.Corruption, "crypto.ReadKeyFile", "checksum", err)
	}
	ver := format.KeyVersion(encoding.U32(buf, 6))
	return DEKFromBytes(ver, buf[10:10+AES256KeySize])
}

func LoadProvider(path string) (*MemoryKeyProvider, error) {
	dek, err := ReadKeyFile(path)
	if err != nil {
		return nil, err
	}
	return NewMemoryKeyProvider(dek)
}

func CreateKeyFile(path string, version format.KeyVersion) (*DEK, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "crypto.CreateKeyFile", "key file exists")
	}
	dek, err := GenerateDEK(version)
	if err != nil {
		return nil, err
	}
	if err := WriteKeyFile(path, dek); err != nil {
		return nil, err
	}
	return dek, nil
}
