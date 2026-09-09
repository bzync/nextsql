// Package clientenc implements the portable envelope used by ENCRYPTED CLIENT
// columns. Encryption and decryption happen only in clients; the server may
// validate the bounded envelope header but never receives a field key.
package clientenc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"io"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"golang.org/x/crypto/hkdf"
)

const (
	Prefix               = "NSCE1."
	DeterministicPrefix  = "NSCE2."
	Version              = 1
	DeterministicVersion = 2
	KeySize              = 32
	MaxKeyIDBytes        = 64
	MaxPlaintext         = 1 << 20
	nonceSize            = 12
	headerFixed          = 3 + 6 + nonceSize // version, suite, key length, logical type, nonce
	suiteAESGCM          = 1
	suiteAESSIV          = 2
	deterministicKeyInfo = "NextSQL NSCE2 AES-SIV v2"
)

type Mode uint8

const (
	ModeRandomized Mode = iota
	ModeDeterministic
)

// Key is one AES-256 field key. ID is public envelope metadata; Material must
// never be logged or placed in a connection URL.
type Key struct {
	ID       string
	Material [KeySize]byte
}

// KeyProvider supplies the current key for new writes and resolves a key id
// for reads. A provider revokes a key by refusing to resolve its id.
type KeyProvider interface {
	CurrentFieldKey(ctx context.Context, database, table, column string) (Key, error)
	FieldKey(ctx context.Context, database, table, column, keyID string) (Key, error)
}

// Header is the non-secret, server-readable portion of a client ciphertext.
type Header struct {
	KeyID       string
	LogicalType types.Type
	Mode        Mode
}

// Encrypt seals one non-NULL SQL value with randomized AES-256-GCM and binds
// it to the database/table/column names. NULL is deliberately passed through
// by driver helpers and is not accepted here.
func Encrypt(ctx context.Context, provider KeyProvider, database, table, column string, value types.Value) (string, error) {
	if provider == nil {
		return "", nerr.New(nerr.InvalidArgument, "clientenc.Encrypt", "field key provider is required")
	}
	if value.Null {
		return "", nerr.New(nerr.InvalidArgument, "clientenc.Encrypt", "NULL is not an encrypted payload")
	}
	if !SupportedType(value.Typ) {
		return "", nerr.New(nerr.InvalidArgument, "clientenc.Encrypt", "unsupported client-encrypted type")
	}
	// The server cannot enforce plaintext DECIMAL constraints, so the client
	// envelope boundary must reject scale loss and precision overflow.
	if value.Typ.Kind == types.KindDecimal {
		coerced, err := types.Coerce(value, value.Typ)
		if err != nil {
			return "", err
		}
		value = coerced
	}
	plain, err := types.EncodeScalar(value)
	if err != nil {
		return "", err
	}
	if len(plain) > MaxPlaintext {
		return "", nerr.New(nerr.Exhausted, "clientenc.Encrypt", "plaintext exceeds field limit")
	}
	key, err := provider.CurrentFieldKey(ctx, database, table, column)
	if err != nil {
		return "", nerr.Wrap(nerr.Crypto, "clientenc.Encrypt", "field key unavailable", err)
	}
	if err := validateKey(key); err != nil {
		return "", err
	}
	gcm, err := newGCM(key.Material)
	if err != nil {
		return "", err
	}
	header := makeHeader(key.ID, value.Typ)
	if _, err := rand.Read(header[len(header)-nonceSize:]); err != nil {
		return "", nerr.Wrap(nerr.Crypto, "clientenc.Encrypt", "random nonce", err)
	}
	aad, err := associatedData(database, table, column, header[:len(header)-nonceSize])
	if err != nil {
		return "", err
	}
	body := gcm.Seal(header, header[len(header)-nonceSize:], plain, aad)
	return Prefix + base64.RawURLEncoding.EncodeToString(body), nil
}

// EncryptDeterministic seals a value with RFC 5297 AES-SIV. Equal plaintexts
// under the same key and exact database/table/column context produce equal
// ciphertexts; callers must treat that equality/frequency leakage as public.
func EncryptDeterministic(ctx context.Context, provider KeyProvider, database, table, column string, value types.Value) (string, error) {
	if provider == nil || value.Null || !SupportedType(value.Typ) {
		return "", nerr.New(nerr.InvalidArgument, "clientenc.EncryptDeterministic", "provider and a supported non-NULL value are required")
	}
	if value.Typ.Kind == types.KindDecimal {
		coerced, err := types.Coerce(value, value.Typ)
		if err != nil {
			return "", err
		}
		value = coerced
	}
	plain, err := types.EncodeScalar(value)
	if err != nil {
		return "", err
	}
	if len(plain) > MaxPlaintext {
		return "", nerr.New(nerr.Exhausted, "clientenc.EncryptDeterministic", "plaintext exceeds field limit")
	}
	key, err := provider.CurrentFieldKey(ctx, database, table, column)
	if err != nil {
		return "", nerr.Wrap(nerr.Crypto, "clientenc.EncryptDeterministic", "field key unavailable", err)
	}
	if err := validateKey(key); err != nil {
		return "", err
	}
	header := makeHeader(key.ID, value.Typ)[:headerFixed+len(key.ID)-nonceSize]
	header[0], header[1] = DeterministicVersion, suiteAESSIV
	aad, err := associatedDataWithPrefix(DeterministicPrefix, database, table, column, header)
	if err != nil {
		return "", err
	}
	derived, err := deterministicKey(key.Material)
	if err != nil {
		return "", err
	}
	siv, err := NewAESSIV(derived[:])
	if err != nil {
		return "", err
	}
	body := append(header, siv.Seal(plain, aad)...)
	return DeterministicPrefix + base64.RawURLEncoding.EncodeToString(body), nil
}

// Decrypt authenticates and decodes one client ciphertext. Wrong context,
// wrong/revoked keys, and any tampering fail closed with no partial plaintext.
func Decrypt(ctx context.Context, provider KeyProvider, database, table, column, ciphertext string) (types.Value, error) {
	if provider == nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "clientenc.Decrypt", "field key provider is required")
	}
	body, hdr, headerLen, err := parse(ciphertext)
	if err != nil {
		return types.Value{}, err
	}
	if hdr.Mode != ModeRandomized {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "clientenc.Decrypt", "deterministic ciphertext requires DecryptDeterministic")
	}
	key, err := provider.FieldKey(ctx, database, table, column, hdr.KeyID)
	if err != nil {
		return types.Value{}, nerr.New(nerr.Crypto, "clientenc.Decrypt", "field key unavailable or revoked")
	}
	if err := validateKey(key); err != nil || key.ID != hdr.KeyID {
		return types.Value{}, nerr.New(nerr.Crypto, "clientenc.Decrypt", "field key unavailable or revoked")
	}
	gcm, err := newGCM(key.Material)
	if err != nil {
		return types.Value{}, err
	}
	nonce := body[headerLen-nonceSize : headerLen]
	aad, err := associatedData(database, table, column, body[:headerLen-nonceSize])
	if err != nil {
		return types.Value{}, err
	}
	plain, err := gcm.Open(nil, nonce, body[headerLen:], aad)
	if err != nil {
		return types.Value{}, nerr.New(nerr.Crypto, "clientenc.Decrypt", "ciphertext authentication failed")
	}
	if len(plain) > MaxPlaintext {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "clientenc.Decrypt", "plaintext exceeds field limit")
	}
	v, next, err := types.DecodeScalar(plain, 0, hdr.LogicalType)
	if err != nil || next != len(plain) {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "clientenc.Decrypt", "invalid encrypted value")
	}
	return v, nil
}

// DecryptDeterministic authenticates and decodes an NSCE2 value.
func DecryptDeterministic(ctx context.Context, provider KeyProvider, database, table, column, ciphertext string) (types.Value, error) {
	if provider == nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "clientenc.DecryptDeterministic", "field key provider is required")
	}
	body, hdr, headerLen, err := parse(ciphertext)
	if err != nil {
		return types.Value{}, err
	}
	if hdr.Mode != ModeDeterministic {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "clientenc.DecryptDeterministic", "randomized ciphertext requires Decrypt")
	}
	key, err := provider.FieldKey(ctx, database, table, column, hdr.KeyID)
	if err != nil || validateKey(key) != nil || key.ID != hdr.KeyID {
		return types.Value{}, nerr.New(nerr.Crypto, "clientenc.DecryptDeterministic", "field key unavailable or revoked")
	}
	aad, err := associatedDataWithPrefix(DeterministicPrefix, database, table, column, body[:headerLen])
	if err != nil {
		return types.Value{}, err
	}
	derived, err := deterministicKey(key.Material)
	if err != nil {
		return types.Value{}, err
	}
	siv, err := NewAESSIV(derived[:])
	if err != nil {
		return types.Value{}, err
	}
	plain, err := siv.Open(body[headerLen:], aad)
	if err != nil {
		return types.Value{}, nerr.New(nerr.Crypto, "clientenc.DecryptDeterministic", "ciphertext authentication failed")
	}
	if len(plain) > MaxPlaintext {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "clientenc.DecryptDeterministic", "plaintext exceeds field limit")
	}
	v, next, err := types.DecodeScalar(plain, 0, hdr.LogicalType)
	if err != nil || next != len(plain) {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "clientenc.DecryptDeterministic", "invalid encrypted value")
	}
	return v, nil
}

// Inspect validates the bounded, versioned envelope structure without a key.
// It does not authenticate the ciphertext and must not be treated as decrypt.
func Inspect(ciphertext string) (Header, error) {
	_, hdr, _, err := parse(ciphertext)
	return hdr, err
}

// ValidateForColumn is the server-side structural gate used before a value is
// persisted. Authentication remains exclusively client-side.
func ValidateForColumn(ciphertext string, logical types.Type) error {
	return ValidateForColumnMode(ciphertext, logical, ModeRandomized)
}

func ValidateForColumnMode(ciphertext string, logical types.Type, mode Mode) error {
	h, err := Inspect(ciphertext)
	if err != nil {
		return err
	}
	if !h.LogicalType.Equals(logical) {
		return nerr.New(nerr.InvalidArgument, "clientenc.ValidateForColumn", "ciphertext logical type does not match column")
	}
	if h.Mode != mode {
		return nerr.New(nerr.InvalidArgument, "clientenc.ValidateForColumn", "ciphertext mode does not match column")
	}
	return nil
}

// SupportedType is the deliberately small client-encryption logical-type surface. Vector and
// geospatial values remain out because their large/specialized server storage
// paths would violate the opaque-scalar contract.
func SupportedType(t types.Type) bool {
	switch t.Kind {
	case types.KindDecimal:
		return t.Precision >= 1 && t.Precision <= 38 && t.Scale <= t.Precision && t.VecElem == 0
	case types.KindUUID, types.KindString, types.KindText, types.KindBlob, types.KindTimestampTZ, types.KindJSON, types.KindBool,
		types.KindInt8, types.KindInt16, types.KindInt32, types.KindInt64,
		types.KindUint8, types.KindUint16, types.KindUint32, types.KindUint64,
		types.KindDate, types.KindTime, types.KindTimestamp, types.KindFloat32, types.KindFloat64, types.KindInterval:
		return t.Precision == 0 && t.Scale == 0 && t.VecElem == 0
	default:
		return false
	}
}

func parse(ciphertext string) ([]byte, Header, int, error) {
	prefix, version, suite, mode, fixed := Prefix, byte(Version), byte(suiteAESGCM), ModeRandomized, headerFixed
	if strings.HasPrefix(ciphertext, DeterministicPrefix) {
		prefix, version, suite, mode, fixed = DeterministicPrefix, DeterministicVersion, suiteAESSIV, ModeDeterministic, headerFixed-nonceSize
	} else if !strings.HasPrefix(ciphertext, Prefix) {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "invalid client ciphertext prefix")
	}
	enc := strings.TrimPrefix(ciphertext, prefix)
	if len(enc) == 0 || len(enc) > base64.RawURLEncoding.EncodedLen(MaxPlaintext+fixed+MaxKeyIDBytes+32) {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "client ciphertext length out of range")
	}
	body, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "invalid client ciphertext encoding")
	}
	if len(body) < fixed+1+16 {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "truncated client ciphertext")
	}
	if body[0] != version || body[1] != suite {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "unsupported client ciphertext version or suite")
	}
	n := int(body[2])
	if n < 1 || n > MaxKeyIDBytes {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "invalid field key id length")
	}
	headerLen := fixed + n
	if len(body) < headerLen+16 {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "truncated client ciphertext body")
	}
	keyID := string(body[3 : 3+n])
	if !validKeyID(keyID) {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "invalid field key id")
	}
	off := 3 + n
	typ := types.Type{
		Kind:      types.Kind(body[off]),
		Precision: binary.LittleEndian.Uint16(body[off+1 : off+3]),
		Scale:     binary.LittleEndian.Uint16(body[off+3 : off+5]),
		VecElem:   body[off+5],
	}
	if !SupportedType(typ) {
		return nil, Header{}, 0, nerr.New(nerr.InvalidFormat, "clientenc.Inspect", "unsupported encrypted logical type")
	}
	return body, Header{KeyID: keyID, LogicalType: typ, Mode: mode}, headerLen, nil
}

func makeHeader(keyID string, typ types.Type) []byte {
	b := make([]byte, headerFixed+len(keyID))
	b[0], b[1], b[2] = Version, suiteAESGCM, byte(len(keyID))
	copy(b[3:], keyID)
	off := 3 + len(keyID)
	b[off] = byte(typ.Kind)
	binary.LittleEndian.PutUint16(b[off+1:off+3], typ.Precision)
	binary.LittleEndian.PutUint16(b[off+3:off+5], typ.Scale)
	b[off+5] = typ.VecElem
	return b
}

func associatedData(database, table, column string, header []byte) ([]byte, error) {
	return associatedDataWithPrefix(Prefix, database, table, column, header)
}

func associatedDataWithPrefix(prefix, database, table, column string, header []byte) ([]byte, error) {
	parts := []string{database, table, column}
	n := len(prefix) + len(header) + 6
	for _, part := range parts {
		if len(part) == 0 || len(part) > 0xffff {
			return nil, nerr.New(nerr.InvalidArgument, "clientenc", "database, table, and column are required and bounded")
		}
		n += len(part)
	}
	aad := make([]byte, 0, n)
	aad = append(aad, prefix...)
	for _, part := range parts {
		var size [2]byte
		binary.LittleEndian.PutUint16(size[:], uint16(len(part)))
		aad = append(aad, size[:]...)
		aad = append(aad, part...)
	}
	return append(aad, header...), nil
}

func validateKey(key Key) error {
	if !validKeyID(key.ID) {
		return nerr.New(nerr.InvalidArgument, "clientenc", "invalid field key id")
	}
	var any byte
	for _, b := range key.Material {
		any |= b
	}
	if any == 0 {
		return nerr.New(nerr.InvalidArgument, "clientenc", "empty field key material")
	}
	return nil
}

func validKeyID(id string) bool {
	if len(id) < 1 || len(id) > MaxKeyIDBytes {
		return false
	}
	for i := range id {
		c := id[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-' {
			continue
		}
		return false
	}
	return true
}

func newGCM(key [KeySize]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "clientenc", "AES-256", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "clientenc", "AES-GCM", err)
	}
	return gcm, nil
}

// deterministicKey domain-separates the AES-SIV key from the NSCE1
// AES-256-GCM key even when an application deliberately reuses a field-key id.
func deterministicKey(material [KeySize]byte) ([KeySize]byte, error) {
	var out [KeySize]byte
	if _, err := io.ReadFull(hkdf.New(sha256.New, material[:], nil, []byte(deterministicKeyInfo)), out[:]); err != nil {
		return out, nerr.Wrap(nerr.Crypto, "clientenc", "derive deterministic field key", err)
	}
	return out, nil
}
