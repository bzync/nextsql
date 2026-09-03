package security

import (
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"math"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

// AuditKeyset holds the Ed25519 keys that sign the audit hash chain. Its
// on-disk shape (current/retired flags, overlap rotation, atomic
// last-known-good reload, verify-only export) is the same lifecycle
// internal/auth/tokenkeys.go uses for short-lived-credential signing keys.
const (
	auditKeysMagic   = "NSAK"
	auditKeysVersion = 1
	maxAuditKeys     = 64
	maxAuditKeyBytes = 8 << 10

	akFlagRetired    = 1 << 0
	akFlagCurrent    = 1 << 1
	akFlagHasPrivate = 1 << 2
)

type auditKey struct {
	ID      uint32
	Created time.Time
	Retired bool
	Current bool
	Public  ed25519.PublicKey
	Seed    []byte // 32 bytes on a signer keyset; nil on a verify-only keyset
}

// AuditKeyInfo is a key-material-free summary for listing.
type AuditKeyInfo struct {
	ID         uint32
	Created    time.Time
	Retired    bool
	Current    bool
	HasPrivate bool
}

// AuditKeyset is the set of Ed25519 keys that sign and verify the audit hash
// chain. A signer keyset holds private seeds; a verifier keeps a
// verify-only copy (see PublicOnly). Rotation adds a new current key and
// retires the previous one after an overlap window, so log entries signed
// by the previous key still verify.
type AuditKeyset struct {
	mu   sync.Mutex
	path string
	keys []auditKey
}

// CreateAuditKeyset writes a new signer keyset with one current key. The
// new key's id is 1.
func CreateAuditKeyset(path string) (*AuditKeyset, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "security.CreateAuditKeyset", "keyset file exists")
	}
	ks := &AuditKeyset{path: path}
	if _, err := ks.AddKey(); err != nil {
		return nil, err
	}
	return ks, nil
}

// OpenAuditKeyset loads a keyset (signer or verify-only).
func OpenAuditKeyset(path string) (*AuditKeyset, error) {
	raw, err := readAuditKeysetFile(path)
	if err != nil {
		return nil, err
	}
	keys, err := decodeAuditKeys(raw)
	if err != nil {
		return nil, err
	}
	return &AuditKeyset{path: path, keys: keys}, nil
}

func (ks *AuditKeyset) Path() string { return ks.path }

// AddKey generates a new Ed25519 key, makes it the sole current key, and
// persists. The previous current key remains valid for verification until
// it is retired.
func (ks *AuditKeyset) AddKey() (uint32, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return 0, nerr.Wrap(nerr.Internal, "security.AuditKeyset.AddKey", "generate", err)
	}
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if len(ks.keys) >= maxAuditKeys {
		return 0, nerr.New(nerr.InvalidArgument, "security.AuditKeyset.AddKey", "too many keys")
	}
	candidate := cloneAuditKeys(ks.keys)
	var maxID uint32
	for i := range candidate {
		candidate[i].Current = false
		if candidate[i].ID > maxID {
			maxID = candidate[i].ID
		}
	}
	if maxID == ^uint32(0) {
		return 0, nerr.New(nerr.InvalidArgument, "security.AuditKeyset.AddKey", "key id space exhausted")
	}
	id := maxID + 1
	candidate = append(candidate, auditKey{
		ID:      id,
		Created: time.Now().UTC(),
		Current: true,
		Public:  pub,
		Seed:    append([]byte(nil), priv.Seed()...),
	})
	if err := ks.persistKeysLocked(candidate); err != nil {
		return 0, err
	}
	ks.keys = candidate
	return id, nil
}

// Retire marks a key as no longer usable for verification. The current key
// cannot be retired while it is the only non-retired key.
func (ks *AuditKeyset) Retire(id uint32) error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	candidate := cloneAuditKeys(ks.keys)
	idx := -1
	live := 0
	for i := range candidate {
		if !candidate[i].Retired {
			live++
		}
		if candidate[i].ID == id {
			idx = i
		}
	}
	if idx < 0 {
		return nerr.New(nerr.NotFound, "security.AuditKeyset.Retire", "unknown key id")
	}
	if candidate[idx].Retired {
		return nil
	}
	if live <= 1 {
		return nerr.New(nerr.InvalidArgument, "security.AuditKeyset.Retire", "cannot retire the last active key")
	}
	candidate[idx].Retired = true
	for i := range candidate[idx].Seed {
		candidate[idx].Seed[i] = 0
	}
	candidate[idx].Seed = nil
	if candidate[idx].Current {
		candidate[idx].Current = false
		newest := -1
		for i := range candidate {
			if candidate[i].Retired {
				continue
			}
			if newest < 0 || candidate[i].ID > candidate[newest].ID {
				newest = i
			}
		}
		if newest >= 0 {
			candidate[newest].Current = true
		}
	}
	if err := ks.persistKeysLocked(candidate); err != nil {
		return err
	}
	ks.keys = candidate
	return nil
}

// verifier returns the public key for id if it exists and is not retired.
func (ks *AuditKeyset) verifier(id uint32) (ed25519.PublicKey, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.keys {
		if ks.keys[i].ID == id {
			if ks.keys[i].Retired {
				return nil, nerr.New(nerr.Unauthorized, "security.AuditKeyset", "signing key retired")
			}
			return ks.keys[i].Public, nil
		}
	}
	return nil, nerr.New(nerr.Unauthorized, "security.AuditKeyset", "unknown signing key")
}

// signer returns the current key's private key and id.
func (ks *AuditKeyset) signer() (ed25519.PrivateKey, uint32, error) {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	for i := range ks.keys {
		if ks.keys[i].Current && !ks.keys[i].Retired {
			if len(ks.keys[i].Seed) != ed25519.SeedSize {
				return nil, 0, nerr.New(nerr.InvalidArgument, "security.AuditKeyset", "keyset has no private material for the current key")
			}
			return ed25519.NewKeyFromSeed(ks.keys[i].Seed), ks.keys[i].ID, nil
		}
	}
	return nil, 0, nerr.New(nerr.NotFound, "security.AuditKeyset", "no current signing key")
}

// ValidateSigner confirms that the keyset has exactly one active current key
// with matching private material. It exposes no key bytes.
func (ks *AuditKeyset) ValidateSigner() error {
	if ks == nil {
		return nerr.New(nerr.InvalidArgument, "security.AuditKeyset", "nil signing keyset")
	}
	priv, _, err := ks.signer()
	if len(priv) > 0 {
		for i := range priv {
			priv[i] = 0
		}
	}
	return err
}

// sign produces an Ed25519 signature over hash using the current key.
func (ks *AuditKeyset) sign(hash []byte) ([]byte, uint32, error) {
	priv, id, err := ks.signer()
	if err != nil {
		return nil, 0, err
	}
	return ed25519.Sign(priv, hash), id, nil
}

// verify checks an Ed25519 signature over hash against the (possibly
// retired) key id. A retired key still verifies old signatures; only
// signing with it is refused.
func (ks *AuditKeyset) verify(id uint32, hash, sig []byte) error {
	ks.mu.Lock()
	var pub ed25519.PublicKey
	for i := range ks.keys {
		if ks.keys[i].ID == id {
			pub = ks.keys[i].Public
			break
		}
	}
	ks.mu.Unlock()
	if pub == nil {
		return nerr.New(nerr.Unauthorized, "security.AuditKeyset", "unknown signing key")
	}
	if !ed25519.Verify(pub, hash, sig) {
		return nerr.New(nerr.Crypto, "security.AuditKeyset", "signature verification failed")
	}
	return nil
}

// List returns key-material-free summaries, oldest id first.
func (ks *AuditKeyset) List() []AuditKeyInfo {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	out := make([]AuditKeyInfo, 0, len(ks.keys))
	for i := range ks.keys {
		out = append(out, AuditKeyInfo{
			ID: ks.keys[i].ID, Created: ks.keys[i].Created,
			Retired: ks.keys[i].Retired, Current: ks.keys[i].Current,
			HasPrivate: len(ks.keys[i].Seed) == ed25519.SeedSize,
		})
	}
	return out
}

// PublicOnly returns a copy with every private seed stripped, suitable for
// distribution to a verifier. It is not attached to a file.
func (ks *AuditKeyset) PublicOnly() *AuditKeyset {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	cp := &AuditKeyset{keys: make([]auditKey, len(ks.keys))}
	for i := range ks.keys {
		cp.keys[i] = ks.keys[i]
		cp.keys[i].Seed = nil
		cp.keys[i].Public = append(ed25519.PublicKey(nil), ks.keys[i].Public...)
	}
	return cp
}

// WritePublic writes a verify-only keyset (no private seeds) to path.
func (ks *AuditKeyset) WritePublic(path string) error {
	pub := ks.PublicOnly()
	pub.mu.Lock()
	raw, err := encodeAuditKeys(pub.keys)
	pub.mu.Unlock()
	if err != nil {
		return err
	}
	return atomicWriteAudit(path, raw)
}

// Reload re-reads the keyset file. On any error the in-memory keyset is
// left unchanged (last known good).
func (ks *AuditKeyset) Reload() error {
	ks.mu.Lock()
	defer ks.mu.Unlock()
	if ks.path == "" {
		return nerr.New(nerr.InvalidArgument, "security.AuditKeyset.Reload", "keyset is not attached to a file")
	}
	raw, err := readAuditKeysetFile(ks.path)
	if err != nil {
		return err
	}
	keys, err := decodeAuditKeys(raw)
	if err != nil {
		return err
	}
	if auditKeysHaveCurrentPrivate(ks.keys) && !auditKeysHaveCurrentPrivate(keys) {
		return nerr.New(nerr.InvalidFormat, "security.AuditKeyset.Reload", "signing keyset reload has no private current key")
	}
	ks.keys = keys
	return nil
}

func (ks *AuditKeyset) persistLocked() error {
	return ks.persistKeysLocked(ks.keys)
}

func (ks *AuditKeyset) persistKeysLocked(keys []auditKey) error {
	if ks.path == "" {
		return nil
	}
	raw, err := encodeAuditKeys(keys)
	if err != nil {
		return err
	}
	return atomicWriteAudit(ks.path, raw)
}

func atomicWriteAudit(path string, raw []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "create temp", err)
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "chmod", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "write", err)
	}
	if err := tmp.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "sync", err)
	}
	if err := tmp.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "close", err)
	}
	if err := os.Rename(tmpName, path); err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "rename", err)
	}
	d, err := os.Open(dir)
	if err == nil {
		err = d.Sync()
		_ = d.Close()
	}
	if err != nil {
		return nerr.Wrap(nerr.IO, "security.atomicWriteAudit", "sync directory", err)
	}
	ok = true
	return nil
}

func encodeAuditKeys(keys []auditKey) ([]byte, error) {
	if len(keys) > maxAuditKeys {
		return nil, nerr.New(nerr.InvalidArgument, "security.encodeAuditKeys", "too many keys")
	}
	n := 4 + 2 + 2
	for i := range keys {
		n += 4 + 1 + 8 + ed25519.PublicKeySize
		if len(keys[i].Seed) == ed25519.SeedSize {
			n += ed25519.SeedSize
		}
	}
	buf := make([]byte, n)
	copy(buf[0:4], auditKeysMagic)
	encoding.PutU16(buf, 4, auditKeysVersion)
	encoding.PutU16(buf, 6, uint16(len(keys)))
	off := 8
	for i := range keys {
		if len(keys[i].Public) != ed25519.PublicKeySize {
			return nil, nerr.New(nerr.InvalidFormat, "security.encodeAuditKeys", "bad public key size")
		}
		encoding.PutU32(buf, off, keys[i].ID)
		off += 4
		var flags byte
		if keys[i].Retired {
			flags |= akFlagRetired
		}
		if keys[i].Current {
			flags |= akFlagCurrent
		}
		hasPriv := len(keys[i].Seed) == ed25519.SeedSize
		if hasPriv {
			flags |= akFlagHasPrivate
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

func decodeAuditKeys(raw []byte) ([]auditKey, error) {
	bad := func(msg string) ([]auditKey, error) {
		return nil, nerr.New(nerr.InvalidFormat, "security.decodeAuditKeys", msg)
	}
	if len(raw) < 8 {
		return bad("truncated keyset")
	}
	if string(raw[0:4]) != auditKeysMagic {
		return bad("bad keyset magic")
	}
	if encoding.U16(raw, 4) != auditKeysVersion {
		return bad("unsupported keyset version")
	}
	count := encoding.U16(raw, 6)
	if int(count) > maxAuditKeys {
		return bad("key count exceeds limit")
	}
	keys := make([]auditKey, 0, count)
	seen := make(map[uint32]struct{}, count)
	off := 8
	current := 0
	var previousID uint32
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
		if flags & ^byte(akFlagRetired|akFlagCurrent|akFlagHasPrivate) != 0 {
			return bad("unknown key flags")
		}
		created, err := encoding.ReadU64(raw, off)
		if err != nil {
			return bad("truncated created time")
		}
		off += 8
		if created > math.MaxInt64 {
			return bad("created time out of range")
		}
		pub, err := encoding.ReadBytes(raw, off, ed25519.PublicKeySize)
		if err != nil {
			return bad("truncated public key")
		}
		off += ed25519.PublicKeySize
		k := auditKey{
			ID:      id,
			Created: time.Unix(int64(created), 0).UTC(),
			Retired: flags&akFlagRetired != 0,
			Current: flags&akFlagCurrent != 0,
			Public:  append(ed25519.PublicKey(nil), pub...),
		}
		if id == 0 || (i > 0 && id <= previousID) {
			return bad("key ids must be non-zero and strictly increasing")
		}
		previousID = id
		if k.Current && k.Retired {
			return bad("current key cannot be retired")
		}
		if flags&akFlagHasPrivate != 0 {
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
	if current != 1 {
		return bad("keyset must have exactly one active current key")
	}
	return keys, nil
}

func cloneAuditKeys(keys []auditKey) []auditKey {
	out := make([]auditKey, len(keys))
	for i := range keys {
		out[i] = keys[i]
		out[i].Public = append(ed25519.PublicKey(nil), keys[i].Public...)
		out[i].Seed = append([]byte(nil), keys[i].Seed...)
	}
	return out
}

func auditKeysHaveCurrentPrivate(keys []auditKey) bool {
	for i := range keys {
		if keys[i].Current && !keys[i].Retired && len(keys[i].Seed) == ed25519.SeedSize {
			return true
		}
	}
	return false
}

func readAuditKeysetFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.OpenAuditKeyset", "stat", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, nerr.New(nerr.InvalidArgument, "security.OpenAuditKeyset", "keyset path must be a regular non-symlink file")
	}
	if info.Size() > maxAuditKeyBytes {
		return nil, nerr.New(nerr.InvalidFormat, "security.OpenAuditKeyset", "keyset exceeds size limit")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.OpenAuditKeyset", "open", err)
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxAuditKeyBytes+1))
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.OpenAuditKeyset", "read", err)
	}
	if len(raw) > maxAuditKeyBytes {
		return nil, nerr.New(nerr.InvalidFormat, "security.OpenAuditKeyset", "keyset exceeds size limit")
	}
	return raw, nil
}
