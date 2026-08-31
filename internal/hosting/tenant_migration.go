package hosting

import (
	"bytes"
	"os"
	"path/filepath"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	TenantMigrationFileName = "nextsql.tenant-migration"

	tenantMigrationMagic      = "NSLM"
	tenantMigrationVersion    = 1
	tenantMigrationHeaderSize = 20
	maxTenantMigrationBytes   = 4096
	maxTenantIdentityBytes    = 1024
)

// TenantMigrationState is the durable publication state of one exact offline
// legacy-tenant migration.
type TenantMigrationState uint8

const (
	TenantMigrationProvisioning TenantMigrationState = iota + 1
	TenantMigrationComplete
)

// TenantMigrationIntent binds a resumable destination to one source identity
// and one historical tenant. Tenant is encrypted on disk.
type TenantMigrationIntent struct {
	Source      format.Identity
	Destination format.Identity
	Tenant      string
	Realm       string
	Database    string
	State       TenantMigrationState
	Tables      uint32
	Rows        uint64
}

// TenantMigrationPath returns the encrypted intent path inside a destination
// deployment.
func TenantMigrationPath(dataDir string) string {
	return filepath.Join(dataDir, TenantMigrationFileName)
}

// EnsureTenantMigrationIntent creates an encrypted provisioning intent or
// validates an exact retry. It never accepts a changed source, tenant, or
// destination identity.
func EnsureTenantMigrationIntent(path string, keys crypto.KeyProvider, expected TenantMigrationIntent) (TenantMigrationIntent, bool, error) {
	if err := validateTenantMigrationIntent(expected, false); err != nil {
		return TenantMigrationIntent{}, false, err
	}
	if keys == nil {
		return TenantMigrationIntent{}, false, nerr.New(nerr.InvalidArgument, "hosting.EnsureTenantMigrationIntent", "nil key provider")
	}
	if _, err := os.Stat(path); err == nil {
		current, err := ReadTenantMigrationIntent(path, keys)
		if err != nil {
			return TenantMigrationIntent{}, false, err
		}
		if !sameTenantMigration(current, expected) {
			return TenantMigrationIntent{}, false, nerr.New(nerr.Conflict, "hosting.EnsureTenantMigrationIntent", "existing migration intent does not match source, tenant, or destination")
		}
		return current, false, nil
	} else if !os.IsNotExist(err) {
		return TenantMigrationIntent{}, false, nerr.Wrap(nerr.IO, "hosting.EnsureTenantMigrationIntent", "stat intent", err)
	}
	expected.State = TenantMigrationProvisioning
	expected.Tables = 0
	expected.Rows = 0
	if err := writeTenantMigrationIntent(path, keys, expected); err != nil {
		return TenantMigrationIntent{}, false, err
	}
	return expected, true, nil
}

// CompleteTenantMigrationIntent durably records verified counts before the
// caller publishes the destination database ACTIVE.
func CompleteTenantMigrationIntent(path string, keys crypto.KeyProvider, expected TenantMigrationIntent, tables uint32, rows uint64) error {
	if keys == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.CompleteTenantMigrationIntent", "nil key provider")
	}
	current, err := ReadTenantMigrationIntent(path, keys)
	if err != nil {
		return err
	}
	if !sameTenantMigration(current, expected) {
		return nerr.New(nerr.Conflict, "hosting.CompleteTenantMigrationIntent", "migration intent identity changed")
	}
	if current.State == TenantMigrationComplete {
		if current.Tables != tables || current.Rows != rows {
			return nerr.New(nerr.Conflict, "hosting.CompleteTenantMigrationIntent", "completed migration counts changed")
		}
		return nil
	}
	current.State = TenantMigrationComplete
	current.Tables = tables
	current.Rows = rows
	return writeTenantMigrationIntent(path, keys, current)
}

// ReadTenantMigrationIntent authenticates and decodes one bounded intent.
func ReadTenantMigrationIntent(path string, keys crypto.KeyProvider) (TenantMigrationIntent, error) {
	if keys == nil {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidArgument, "hosting.ReadTenantMigrationIntent", "nil key provider")
	}
	raw, err := readBounded(path, maxTenantMigrationBytes)
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	if len(raw) < tenantMigrationHeaderSize+12+16 {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidFormat, "hosting.ReadTenantMigrationIntent", "truncated migration intent")
	}
	header := raw[:tenantMigrationHeaderSize]
	if string(header[:4]) != tenantMigrationMagic || encoding.U16(header, 4) != tenantMigrationVersion {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidFormat, "hosting.ReadTenantMigrationIntent", "unsupported migration intent")
	}
	if format.CipherSuite(encoding.U16(header, 6)) != format.CipherAES256GCM {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidFormat, "hosting.ReadTenantMigrationIntent", "unsupported migration cipher")
	}
	plainLen := int(encoding.U32(header, 12))
	cipherLen := int(encoding.U32(header, 16))
	if plainLen < 1 || cipherLen != plainLen+16 || tenantMigrationHeaderSize+12+cipherLen != len(raw) {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidFormat, "hosting.ReadTenantMigrationIntent", "migration intent length mismatch")
	}
	key, err := keys.Key(format.KeyVersion(encoding.U32(header, 8)))
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	plain, err := crypto.OpenBytes(key, raw[tenantMigrationHeaderSize:tenantMigrationHeaderSize+12], header, raw[tenantMigrationHeaderSize+12:])
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	intent, err := decodeTenantMigrationIntent(plain)
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	if err := validateTenantMigrationIntent(intent, true); err != nil {
		return TenantMigrationIntent{}, nerr.Wrap(nerr.InvalidFormat, "hosting.ReadTenantMigrationIntent", "invalid migration intent", err)
	}
	return intent, nil
}

func writeTenantMigrationIntent(path string, keys crypto.KeyProvider, intent TenantMigrationIntent) error {
	if err := validateTenantMigrationIntent(intent, true); err != nil {
		return err
	}
	plain := encodeTenantMigrationIntent(intent)
	key, err := keys.Current()
	if err != nil {
		return err
	}
	header := make([]byte, tenantMigrationHeaderSize)
	copy(header[:4], tenantMigrationMagic)
	encoding.PutU16(header, 4, tenantMigrationVersion)
	encoding.PutU16(header, 6, uint16(key.Suite))
	encoding.PutU32(header, 8, uint32(key.Version))
	encoding.PutU32(header, 12, uint32(len(plain)))
	encoding.PutU32(header, 16, uint32(len(plain)+16))
	nonce, ciphertext, err := crypto.SealBytesRandom(key, header, plain)
	if err != nil {
		return err
	}
	raw := make([]byte, 0, len(header)+len(nonce)+len(ciphertext))
	raw = append(raw, header...)
	raw = append(raw, nonce...)
	raw = append(raw, ciphertext...)
	if len(raw) > maxTenantMigrationBytes {
		return nerr.New(nerr.InvalidArgument, "hosting.writeTenantMigrationIntent", "migration intent exceeds size limit")
	}
	return writeAtomicDurable(path, raw)
}

func encodeTenantMigrationIntent(intent TenantMigrationIntent) []byte {
	buf := make([]byte, 0, 128+len(intent.Tenant)+len(intent.Realm)+len(intent.Database))
	buf = append(buf, intent.Source.Database[:]...)
	buf = append(buf, intent.Source.File[:]...)
	buf = append(buf, intent.Destination.Database[:]...)
	buf = append(buf, intent.Destination.File[:]...)
	buf = append(buf, byte(intent.State))
	buf = appendTenantMigrationU32(buf, intent.Tables)
	buf = appendU64(buf, intent.Rows)
	buf = appendString(buf, intent.Tenant)
	buf = appendString(buf, intent.Realm)
	buf = appendString(buf, intent.Database)
	return buf
}

func decodeTenantMigrationIntent(raw []byte) (TenantMigrationIntent, error) {
	const fixed = 64 + 1 + 4 + 8
	if len(raw) < fixed {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidFormat, "hosting.decodeTenantMigrationIntent", "truncated intent")
	}
	var intent TenantMigrationIntent
	copy(intent.Source.Database[:], raw[:16])
	copy(intent.Source.File[:], raw[16:32])
	copy(intent.Destination.Database[:], raw[32:48])
	copy(intent.Destination.File[:], raw[48:64])
	intent.State = TenantMigrationState(raw[64])
	intent.Tables = encoding.U32(raw, 65)
	intent.Rows = encoding.U64(raw, 69)
	off := fixed
	var err error
	intent.Tenant, off, err = readString(raw, off)
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	intent.Realm, off, err = readString(raw, off)
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	intent.Database, off, err = readString(raw, off)
	if err != nil {
		return TenantMigrationIntent{}, err
	}
	if off != len(raw) {
		return TenantMigrationIntent{}, nerr.New(nerr.InvalidFormat, "hosting.decodeTenantMigrationIntent", "trailing intent bytes")
	}
	return intent, nil
}

func validateTenantMigrationIntent(intent TenantMigrationIntent, stateRequired bool) error {
	if intent.Source.Database == [16]byte{} || intent.Source.File == [16]byte{} || intent.Destination.Database == [16]byte{} || intent.Destination.File == [16]byte{} {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "zero database identity")
	}
	if intent.Source == intent.Destination {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "source and destination identities match")
	}
	if len(intent.Tenant) < 1 || len(intent.Tenant) > maxTenantIdentityBytes || bytes.IndexByte([]byte(intent.Tenant), 0) >= 0 {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "invalid tenant identity")
	}
	realm, err := normalizeName(intent.Realm, nerr.InvalidArgument)
	if err != nil || realm != intent.Realm {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "invalid realm name")
	}
	database, err := normalizeName(intent.Database, nerr.InvalidArgument)
	if err != nil || database != intent.Database {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "invalid database name")
	}
	if stateRequired && intent.State != TenantMigrationProvisioning && intent.State != TenantMigrationComplete {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "invalid migration state")
	}
	if intent.State == TenantMigrationProvisioning && (intent.Tables != 0 || intent.Rows != 0) {
		return nerr.New(nerr.InvalidArgument, "hosting.TenantMigrationIntent", "provisioning intent has completion counts")
	}
	return nil
}

func sameTenantMigration(a, b TenantMigrationIntent) bool {
	return a.Source == b.Source && a.Destination == b.Destination && a.Tenant == b.Tenant && a.Realm == b.Realm && a.Database == b.Database
}

func appendTenantMigrationU32(dst []byte, value uint32) []byte {
	var raw [4]byte
	encoding.PutU32(raw[:], 0, value)
	return append(dst, raw[:]...)
}
