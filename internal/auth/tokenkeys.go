package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	tokenKeysMagic   = "NSTK"
	tokenKeysVersion = 1
	maxTokenKeys     = 64

	tkFlagRetired    = 1 << 0
	tkFlagCurrent    = 1 << 1
	tkFlagHasPrivate = 1 << 2
)

type tokenKey struct {
	ID      uint32
	Created time.Time
	Retired bool
	Current bool
	Public  ed25519.PublicKey
	Seed    []byte // 32 bytes on an issuer keyset; nil on a verify-only keyset
}

// TokenKeyInfo is a key-material-free summary for listing.
type TokenKeyInfo struct {
	ID         uint32
	Created    time.Time
	Retired    bool
	Current    bool
	HasPrivate bool
}

// TokenKeyset is the set of Ed25519 keys that sign and verify short-lived
// credentials. An issuer keyset holds private seeds; a server keeps a
// verify-only copy (see PublicOnly). Rotation adds a new current key and
// retires the previous one after an overlap window.
type TokenKeyset struct {
	mu   sync.Mutex
	path string
	keys []tokenKey
}

// CreateTokenKeyset writes a new issuer keyset with one current key and returns
// it. The new key's id is 1.
func CreateTokenKeyset(path string) (*TokenKeyset, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "auth.CreateTokenKeyset", "keyset file exists")
	}
	ks := &TokenKeyset{path: path}
	if _, err := ks.AddKey(); err != nil {
		return nil, err
	}
	return ks, nil
}

// OpenTokenKeyset loads a keyset (issuer or verify-only).
func OpenTokenKeyset(path string) (*TokenKeyset, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "auth.OpenTokenKeyset", "read", err)
	}
	keys, err := decodeTokenKeys(raw)
	if err != nil {
		return nil, err
	}
	return &TokenKeyset{path: path, keys: keys}, nil
}

func (ks *TokenKeyset) Path() string { return ks.path }

// AddKey generates a new Ed25519 key, makes it the sole current key, and
// persists. The previous current key remains valid for verification until it
// is retired.
func (ks *TokenKeyset) AddKey() (uint32, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return 0, nerr.Wrap(nerr.Internal, "auth.TokenKeyset.AddKey", "generate", err)
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if len(ks.keys) >= maxTokenKeys {
		return 0, nerr.New(nerr.InvalidArgument, "auth.TokenKeyset.AddKey", "too many keys")
	}
	var maxID uint32
	for i := range ks.keys {
		ks.keys[i].Current = false
		if ks.keys[i].ID > maxID {
			maxID = ks.keys[i].ID
		}
	}
	id := maxID + 1
	ks.keys = append(ks.keys, tokenKey{
		ID:      id,
		Created: time.Now().UTC(),
		Current: true,
		Public:  pub,
		Seed:    append([]byte(nil), priv.Seed()...),
	})
	if err := ks.persistLocked(); err != nil {
		return 0, err
	}
	return id, nil
}

// Retire marks a key as no longer usable for verification. The current key
// cannot be retired while it is the only non-retired key.
func (ks *TokenKeyset) Retire(id uint32) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	idx := -1
	live := 0
	for i := range ks.keys {
		if !ks.keys[i].Retired {
			live++
		}
		if ks.keys[i].ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return nerr.New(nerr.NotFound, "auth.TokenKeyset.Retire", "unknown key id")
	}
	if ks.keys[idx].Retired {
		return nil
	}
	if live <= 1 {
		return nerr.New(nerr.InvalidArgument, "auth.TokenKeyset.Retire", "cannot retire the last active key")
	}
	ks.keys[idx].Retired = true
	if ks.keys[idx].Current {
		ks.keys[idx].Current = false
		// Promote the newest non-retired key to current.
		newest := -1
		for i := range ks.keys {
			if ks.keys[i].Retired {
				continue
			}
			if newest < 0 || ks.keys[i].ID > ks.keys[newest].ID {
				newest = i
			}
		}
		if newest >= 0 {
			ks.keys[newest].Current = true
		}
	}
	return ks.persistLocked()
}

// verifier returns the public key for id if it exists and is not retired.
func (ks *TokenKeyset) verifier(id uint32) (ed25519.PublicKey, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.keys {
		if ks.keys[i].ID == id {
			if ks.keys[i].Retired {
				return nil, nerr.New(nerr.Unauthorized, "auth.TokenKeyset", "signing key retired")
			}
			return ks.keys[i].Public, nil
		}
	}
	return nil, nerr.New(nerr.Unauthorized, "auth.TokenKeyset", "unknown signing key")
}

// signer returns the current key's private key and id.
func (ks *TokenKeyset) signer() (ed25519.PrivateKey, uint32, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.keys {
		if ks.keys[i].Current && !ks.keys[i].Retired {
			if len(ks.keys[i].Seed) != ed25519.SeedSize {
				return nil, 0, nerr.New(nerr.InvalidArgument, "auth.TokenKeyset", "keyset has no private material for the current key")
			}
			return ed25519.NewKeyFromSeed(ks.keys[i].Seed), ks.keys[i].ID, nil
		}
	}
	return nil, 0, nerr.New(nerr.NotFound, "auth.TokenKeyset", "no current signing key")
}

// List returns key-material-free summaries, oldest id first.
func (ks *TokenKeyset) List() []TokenKeyInfo {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	out := make([]TokenKeyInfo, 0, len(ks.keys))
	for i := range ks.keys {
		out = append(out, TokenKeyInfo{
			ID: ks.keys[i].ID, Created: ks.keys[i].Created,
			Retired: ks.keys[i].Retired, Current: ks.keys[i].Current,
			HasPrivate: len(ks.keys[i].Seed) == ed25519.SeedSize,
		})
	}
	return out
}

// PublicOnly returns a copy with every private seed stripped, suitable for
// distribution to verifying servers. It is not attached to a file.
func (ks *TokenKeyset) PublicOnly() *TokenKeyset {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	cp := &TokenKeyset{keys: make([]tokenKey, len(ks.keys))}
	for i := range ks.keys {
		cp.keys[i] = ks.keys[i]
		cp.keys[i].Seed = nil
		cp.keys[i].Public = append(ed25519.PublicKey(nil), ks.keys[i].Public...)
	}
	return cp
}

// WritePublic writes a verify-only keyset (no private seeds) to path.
func (ks *TokenKeyset) WritePublic(path string) error {
	pub := ks.PublicOnly()
	pub.mu.Lock()
	raw, err := encodeTokenKeys(pub.keys)
	pub.mu.Unlock()
	if err != nil {
		return err
	}
	return atomicWrite(path, raw)
}

// Reload re-reads the keyset file. On any error the in-memory keyset is left
// unchanged (last known good).
func (ks *TokenKeyset) Reload() error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.path == "" {
		return nerr.New(nerr.InvalidArgument, "auth.TokenKeyset.Reload", "keyset is not attached to a file")
	}
	raw, err := os.ReadFile(ks.path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "auth.TokenKeyset.Reload", "read", err)
	}
	keys, err := decodeTokenKeys(raw)
	if err != nil {
		return err
	}
	ks.keys = keys
	return nil
}

func (ks *TokenKeyset) persistLocked() error {
	if ks.path == "" {
		return nil
	}
	raw, err := encodeTokenKeys(ks.keys)
	if err != nil {
		return err
	}
	return atomicWrite(ks.path, raw)
}

func atomicWrite(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "auth.atomicWrite", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "auth.atomicWrite", "rename", err)
	}
	return nil
}

func encodeTokenKeys(keys []tokenKey) ([]byte, error) {
	if len(keys) > maxTokenKeys {
		return nil, nerr.New(nerr.InvalidArgument, "auth.encodeTokenKeys", "too many keys")
	}
	n := 4 + 2 + 2
	for i := range keys {
		n += 4 + 1 + 8 + ed25519.PublicKeySize
		if len(keys[i].Seed) == ed25519.SeedSize {
			n += ed25519.SeedSize
		}
	}
	buf := make([]byte, n)
	copy(buf[0:4], tokenKeysMagic)
	encoding.PutU16(buf, 4, tokenKeysVersion)
	encoding.PutU16(buf, 6, uint16(len(keys)))
	off := 8
	for i := range keys {
		if len(keys[i].Public) != ed25519.PublicKeySize {
			return nil, nerr.New(nerr.InvalidFormat, "auth.encodeTokenKeys", "bad public key size")
		}
		encoding.PutU32(buf, off, keys[i].ID)
		off += 4
		var flags byte
		if keys[i].Retired {
			flags |= tkFlagRetired
		}
		if keys[i].Current {
			flags |= tkFlagCurrent
		}
		hasPriv := len(keys[i].Seed) == ed25519.SeedSize
		if hasPriv {
			flags |= tkFlagHasPrivate
		}
		buf[off] = flags
		off++
		encoding.PutU64(buf, off, uint64(keys[i].Created.Unix()))
		off += 8
		copy(buf[off:], keys[i].Public)
		off += ed25519.PublicKeySize
		if hasPriv {
			copy(buf[off:], keys[i].Seed)
			off += ed25519.SeedSize
		}
	}
	return buf[:off], nil
}

func decodeTokenKeys(raw []byte) ([]tokenKey, error) {
	bad := func(msg string) ([]tokenKey, error) {
		return nil, nerr.New(nerr.InvalidFormat, "auth.decodeTokenKeys", msg)
	}
	if len(raw) < 8 {
		return bad("truncated keyset")
	}
	if string(raw[0:4]) != tokenKeysMagic {
		return bad("bad keyset magic")
	}
	if encoding.U16(raw, 4) != tokenKeysVersion {
		return bad("unsupported keyset version")
	}
	count := encoding.U16(raw, 6)
	if int(count) > maxTokenKeys {
		return bad("key count exceeds limit")
	}
	keys := make([]tokenKey, 0, count)
	seen := make(map[uint32]struct{}, count)
	off := 8
	current := 0
	for i := 0; i < int(count); i++ {
		id, err := encoding.ReadU32(raw, off)
		if err != nil {
			return bad("truncated key id")
		}
		off += 4
		if off >= len(raw) {
			return bad("truncated flags")
		}
		flags := raw[off]
		off++
		created, err := encoding.ReadU64(raw, off)
		if err != nil {
			return bad("truncated created time")
		}
		off += 8
		pub, err := encoding.ReadBytes(raw, off, ed25519.PublicKeySize)
		if err != nil {
			return bad("truncated public key")
		}
		off += ed25519.PublicKeySize
		k := tokenKey{
			ID:      id,
			Created: time.Unix(int64(created), 0).UTC(),
			Retired: flags&tkFlagRetired != 0,
			Current: flags&tkFlagCurrent != 0,
			Public:  append(ed25519.PublicKey(nil), pub...),
		}
		if flags&tkFlagHasPrivate != 0 {
			seed, err := encoding.ReadBytes(raw, off, ed25519.SeedSize)
			if err != nil {
				return bad("truncated private seed")
			}
			off += ed25519.SeedSize
			k.Seed = append([]byte(nil), seed...)
			if !ed25519.NewKeyFromSeed(seed).Public().(ed25519.PublicKey).Equal(k.Public) {
				return bad("private seed does not match public key")
			}
		}
		if _, dup := seen[id]; dup {
			return bad("duplicate key id")
		}
		seen[id] = struct{}{}
		if k.Current && !k.Retired {
			current++
		}
		keys = append(keys, k)
	}
	if off != len(raw) {
		return bad("trailing keyset bytes")
	}
	if len(keys) == 0 {
		return bad("keyset has no keys")
	}
	if current > 1 {
		return bad("more than one current key")
	}
	return keys, nil
}
