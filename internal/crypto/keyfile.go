package crypto

import (
	"os"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/checksum"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	keyFileMagic = "NSKY"
	// recoveryFileMagic marks an exported recovery key. The body layout is
	// identical to a root key file, but the magic differs so the two cannot
	// be confused: handing a recovery key to --key-file (or the reverse)
	// fails with an error that says which file it actually got, instead of
	// the indistinguishable "does not unlock keystore" it would otherwise
	// produce. Recovery key files are a new artifact, so this costs nothing
	// in compatibility.
	recoveryFileMagic = "NSRK"
	keyFileVersion    = 1
	keyFileSize       = 4 + 2 + 4 + AES256KeySize + 4
	keyFileSumOff     = 4 + 2 + 4 + AES256KeySize
)

func keyFileKindName(magic string) string {
	switch magic {
	case keyFileMagic:
		return "root unlock key"
	case recoveryFileMagic:
		return "recovery key"
	default:
		return "unknown key"
	}
}

// WriteKeyFile writes a DEK to path with mode 0600. The file is not a connection URL and must stay off the data volume in production.
func WriteKeyFile(path string, dek *DEK) error {
	return writeKeyFileAs(path, dek, keyFileMagic, "crypto.WriteKeyFile")
}

// WriteRecoveryKeyFile writes an exported recovery key to path with mode 0600.
// It must be stored off the data volume and off the host that holds the root
// unlock key — a recovery key kept beside the key it backs up is not a backup.
func WriteRecoveryKeyFile(path string, dek *DEK) error {
	return writeKeyFileAs(path, dek, recoveryFileMagic, "crypto.WriteRecoveryKeyFile")
}

func writeKeyFileAs(path string, dek *DEK, magic, op string) error {
	if dek == nil {
		return nerr.New(nerr.InvalidArgument, op, "nil DEK")
	}
	buf := make([]byte, keyFileSize)
	copy(buf[0:4], magic)
	encoding.PutU16(buf, 4, keyFileVersion)
	encoding.PutU32(buf, 6, uint32(dek.Version))
	copy(buf[10:10+AES256KeySize], dek.keyBytes())
	checksum.Write(buf, keyFileSumOff)
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, op, "write", err)
	}
	return nil
}

func ReadKeyFile(path string) (*DEK, error) {
	return readKeyFileAs(path, keyFileMagic, "crypto.ReadKeyFile")
}

// ReadRecoveryKeyFile reads a file written by WriteRecoveryKeyFile.
func ReadRecoveryKeyFile(path string) (*DEK, error) {
	return readKeyFileAs(path, recoveryFileMagic, "crypto.ReadRecoveryKeyFile")
}

func readKeyFileAs(path, magic, op string) (*DEK, error) {
	buf, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, op, "read", err)
	}
	if len(buf) != keyFileSize {
		return nil, nerr.New(nerr.InvalidFormat, op, "wrong key file size")
	}
	if got := string(buf[0:4]); got != magic {
		if got == keyFileMagic || got == recoveryFileMagic {
			return nil, nerr.New(nerr.InvalidArgument, op,
				"this is a "+keyFileKindName(got)+" file, but a "+keyFileKindName(magic)+" was expected")
		}
		return nil, nerr.New(nerr.InvalidFormat, op, "bad key file magic")
	}
	if encoding.U16(buf, 4) != keyFileVersion {
		return nil, nerr.New(nerr.InvalidFormat, op, "unsupported key file version")
	}
	if err := checksum.Verify(buf, keyFileSumOff); err != nil {
		return nil, nerr.Wrap(nerr.Corruption, op, "checksum", err)
	}
	ver := format.KeyVersion(encoding.U32(buf, 6))
	return DEKFromBytes(ver, buf[10:10+AES256KeySize])
}

// CreateRecoveryKeyFile generates a recovery key at the given version and
// writes it to path, refusing to overwrite an existing file.
func CreateRecoveryKeyFile(path string, version format.KeyVersion) (*DEK, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "crypto.CreateRecoveryKeyFile", "recovery key file exists")
	}
	dek, err := GenerateDEK(version)
	if err != nil {
		return nil, err
	}
	if err := WriteRecoveryKeyFile(path, dek); err != nil {
		return nil, err
	}
	return dek, nil
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
