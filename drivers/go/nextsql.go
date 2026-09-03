// Package nextsql is the official NextSQL Go driver.
// Encryption keys are supplied through Config.KeyProvider, never through a URL.
package nextsql

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"net"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

// ReadConsistency selects how a read observes replicated state. The wire byte
// values match the server's executor.ReadConsistency ordering.
type ReadConsistency uint8

const (
	// Strong observes every acknowledged write; served only by the leader
	// behind a Raft read barrier.
	Strong ReadConsistency = iota
	// Bounded serves from any member within MaxStaleness of the leader.
	Bounded
	// Stale serves local applied state with no freshness bound.
	Stale
)

// Config is the only supported way to open a connection. Do not put keys in URLs.
type Config struct {
	Address  string
	Database string
	// Realm selects which hosted realm this connection targets (M2-2).
	// Optional: an empty Realm sends the exact same Hello a pre-realm
	// client sends and connects to the server's configured default. Set it
	// only when the server hosts more than one realm.
	Realm       string
	User        string
	Password    string
	KeyProvider crypto.KeyProvider // reserved for client-held keys; never place keys in a URL
	// FieldKeys is independent from KeyProvider. It supplies client-only keys
	// for ENCRYPTED CLIENT columns and is never sent to nextsqld.
	FieldKeys     FieldKeyProvider
	TLS           *tls.Config
	InsecureNoTLS bool

	// Nodes lists every cluster member address. OpenCluster uses it to route
	// eligible reads to a follower and writes to the leader. Address stays the
	// single-node entry point and is used when Nodes is empty.
	Nodes []string
	// ReadConsistency is the mode a Cluster applies to routed reads (and that
	// OpenContext sets on a plain Conn). The zero value is Strong.
	ReadConsistency ReadConsistency
	// MaxStaleness bounds a Bounded read. Zero selects the server default.
	MaxStaleness time.Duration
}

// FieldKey is one AES-256 key used by ENCRYPTED CLIENT fields. ID is public
// envelope metadata. Material must not be logged or placed in a URL.
type FieldKey = clientenc.Key

// FieldKeyProvider supports online rotation (CurrentFieldKey changes while old
// ids remain resolvable) and revocation (FieldKey refuses a retired id).
type FieldKeyProvider = clientenc.KeyProvider

// MemoryFieldKeyring is a bounded in-process provider useful for applications
// whose secret manager loads keys into memory. It does not persist keys.
type MemoryFieldKeyring struct {
	mu      sync.RWMutex
	current string
	keys    map[string]FieldKey
}

// GenerateFieldKey creates an AES-256 field key with cryptographic randomness.
func GenerateFieldKey(id string) (FieldKey, error) {
	k := FieldKey{ID: id}
	if _, err := rand.Read(k.Material[:]); err != nil {
		return FieldKey{}, nerr.Wrap(nerr.Crypto, "nextsql.GenerateFieldKey", "random key", err)
	}
	// Let the format validator enforce the public key-id contract without
	// exposing key material or duplicating the rules here.
	kr, err := NewMemoryFieldKeyring(k)
	if err != nil {
		return FieldKey{}, err
	}
	_ = kr
	return k, nil
}

// NewMemoryFieldKeyring installs current and any overlap keys. The total is
// bounded to 64 so an attacker-controlled key id cannot grow client memory.
func NewMemoryFieldKeyring(current FieldKey, overlap ...FieldKey) (*MemoryFieldKeyring, error) {
	all := append([]FieldKey{current}, overlap...)
	if len(all) > 64 {
		return nil, nerr.New(nerr.InvalidArgument, "nextsql.NewMemoryFieldKeyring", "too many field keys")
	}
	r := &MemoryFieldKeyring{current: current.ID, keys: make(map[string]FieldKey, len(all))}
	for _, k := range all {
		if k.ID == "" || len(k.ID) > clientenc.MaxKeyIDBytes {
			return nil, nerr.New(nerr.InvalidArgument, "nextsql.NewMemoryFieldKeyring", "invalid field key id")
		}
		if _, exists := r.keys[k.ID]; exists {
			return nil, nerr.New(nerr.InvalidArgument, "nextsql.NewMemoryFieldKeyring", "duplicate field key id")
		}
		// Inspecting a temporary encryption is unnecessary; enforce the same
		// closed ASCII id and non-zero-material contract locally.
		for i := range k.ID {
			c := k.ID[i]
			if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '.' || c == '_' || c == '-') {
				return nil, nerr.New(nerr.InvalidArgument, "nextsql.NewMemoryFieldKeyring", "invalid field key id")
			}
		}
		var any byte
		for _, b := range k.Material {
			any |= b
		}
		if any == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "nextsql.NewMemoryFieldKeyring", "empty field key material")
		}
		r.keys[k.ID] = k
	}
	return r, nil
}

// CurrentFieldKey implements FieldKeyProvider.
func (r *MemoryFieldKeyring) CurrentFieldKey(_ context.Context, _, _, _ string) (FieldKey, error) {
	if r == nil {
		return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.MemoryFieldKeyring", "field keyring unavailable")
	}
	r.mu.RLock()
	k, ok := r.keys[r.current]
	r.mu.RUnlock()
	if !ok {
		return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.MemoryFieldKeyring", "current field key unavailable")
	}
	return k, nil
}

// FieldKey implements FieldKeyProvider.
func (r *MemoryFieldKeyring) FieldKey(_ context.Context, _, _, _, id string) (FieldKey, error) {
	if r == nil {
		return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.MemoryFieldKeyring", "field keyring unavailable")
	}
	r.mu.RLock()
	k, ok := r.keys[id]
	r.mu.RUnlock()
	if !ok {
		return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.MemoryFieldKeyring", "field key unavailable or revoked")
	}
	return k, nil
}

// Rotate makes key current while retaining existing keys for overlap reads.
func (r *MemoryFieldKeyring) Rotate(key FieldKey) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "nextsql.MemoryFieldKeyring.Rotate", "nil keyring")
	}
	probe, err := NewMemoryFieldKeyring(key)
	if err != nil {
		return err
	}
	_ = probe
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.keys[key.ID]; !exists && len(r.keys) >= 64 {
		return nerr.New(nerr.Exhausted, "nextsql.MemoryFieldKeyring.Rotate", "field key limit reached")
	}
	r.keys[key.ID] = key
	r.current = key.ID
	return nil
}

// Revoke removes an old key. The current key must be rotated first.
func (r *MemoryFieldKeyring) Revoke(id string) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "nextsql.MemoryFieldKeyring.Revoke", "nil keyring")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if id == r.current {
		return nerr.New(nerr.Conflict, "nextsql.MemoryFieldKeyring.Revoke", "cannot revoke current field key")
	}
	delete(r.keys, id)
	return nil
}

const (
	fieldKeyringMagic   = "NSFK"
	fieldKeyringVersion = 1
	maxFieldKeyringKeys = 64

	fkFlagCurrent = 1 << 0
	fkFlagRevoked = 1 << 1
)

type fieldKeyRecord struct {
	ID       string
	Created  time.Time
	Current  bool
	Revoked  bool
	Material [clientenc.KeySize]byte
}

// FieldKeyInfo is a material-free summary of one FileFieldKeyring record.
type FieldKeyInfo struct {
	ID      string
	Created time.Time
	Current bool
	Revoked bool
}

// FileFieldKeyring is a durable, atomic, file-backed FieldKeyProvider. Unlike
// MemoryFieldKeyring, rotation and revocation persist across process
// restarts: the on-disk "NSFK1" format is a versioned, 0600, atomically
// written record set with exactly one live current key, mirroring the
// internal/auth token-keyset lifecycle. A revoked key's material is
// overwritten with zeros on disk and its id can never be reused.
//
// FileFieldKeyring is the reference durable implementation for a
// local/self-hosted deployment. Production applications with an existing
// secret manager or KMS should still prefer implementing FieldKeyProvider
// directly against that system.
type FileFieldKeyring struct {
	mu   sync.RWMutex
	path string
	keys []fieldKeyRecord
}

// CreateFileFieldKeyring writes a new keyring file with one current key. It
// fails if path already exists.
func CreateFileFieldKeyring(path string, current FieldKey) (*FileFieldKeyring, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "nextsql.CreateFileFieldKeyring", "keyring file exists")
	} else if !os.IsNotExist(err) {
		return nil, nerr.Wrap(nerr.IO, "nextsql.CreateFileFieldKeyring", "stat", err)
	}
	if err := validateFieldKeyForKeyring(current); err != nil {
		return nil, err
	}
	kr := &FileFieldKeyring{
		path: path,
		keys: []fieldKeyRecord{{ID: current.ID, Created: time.Now().UTC(), Current: true, Material: current.Material}},
	}
	if err := kr.persistLocked(); err != nil {
		return nil, err
	}
	return kr, nil
}

// OpenFileFieldKeyring loads an existing keyring file.
func OpenFileFieldKeyring(path string) (*FileFieldKeyring, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "nextsql.OpenFileFieldKeyring", "read", err)
	}
	keys, err := decodeFieldKeyring(raw)
	if err != nil {
		return nil, err
	}
	return &FileFieldKeyring{path: path, keys: keys}, nil
}

// Path returns the backing file path.
func (kr *FileFieldKeyring) Path() string { return kr.path }

// CurrentFieldKey implements FieldKeyProvider.
func (kr *FileFieldKeyring) CurrentFieldKey(_ context.Context, _, _, _ string) (FieldKey, error) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	for i := range kr.keys {
		if kr.keys[i].Current && !kr.keys[i].Revoked {
			return FieldKey{ID: kr.keys[i].ID, Material: kr.keys[i].Material}, nil
		}
	}
	return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.FileFieldKeyring", "current field key unavailable")
}

// FieldKey implements FieldKeyProvider.
func (kr *FileFieldKeyring) FieldKey(_ context.Context, _, _, _, id string) (FieldKey, error) {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	for i := range kr.keys {
		if kr.keys[i].ID == id {
			if kr.keys[i].Revoked {
				return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.FileFieldKeyring", "field key unavailable or revoked")
			}
			return FieldKey{ID: kr.keys[i].ID, Material: kr.keys[i].Material}, nil
		}
	}
	return FieldKey{}, nerr.New(nerr.Crypto, "nextsql.FileFieldKeyring", "field key unavailable or revoked")
}

// Rotate makes key current, retaining every other live key for overlap
// reads, and persists atomically. Reusing a previously revoked key id fails
// closed: a revoked id can never resolve again.
func (kr *FileFieldKeyring) Rotate(key FieldKey) error {
	if err := validateFieldKeyForKeyring(key); err != nil {
		return err
	}
	kr.mu.Lock()
	defer kr.mu.Unlock()
	idx := -1
	for i := range kr.keys {
		if kr.keys[i].ID == key.ID {
			idx = i
			break
		}
	}
	if idx < 0 {
		if len(kr.keys) >= maxFieldKeyringKeys {
			return nerr.New(nerr.Exhausted, "nextsql.FileFieldKeyring.Rotate", "field key limit reached")
		}
		kr.keys = append(kr.keys, fieldKeyRecord{ID: key.ID, Created: time.Now().UTC()})
		idx = len(kr.keys) - 1
	} else if kr.keys[idx].Revoked {
		return nerr.New(nerr.Conflict, "nextsql.FileFieldKeyring.Rotate", "cannot reuse a revoked field key id")
	}
	for i := range kr.keys {
		kr.keys[i].Current = false
	}
	kr.keys[idx].Current = true
	kr.keys[idx].Material = key.Material
	return kr.persistLocked()
}

// Revoke destroys id's material on disk and marks it permanently refused.
// The current key cannot be revoked directly; rotate away from it first.
func (kr *FileFieldKeyring) Revoke(id string) error {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	idx := -1
	for i := range kr.keys {
		if kr.keys[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nerr.New(nerr.NotFound, "nextsql.FileFieldKeyring.Revoke", "unknown field key id")
	}
	if kr.keys[idx].Current {
		return nerr.New(nerr.Conflict, "nextsql.FileFieldKeyring.Revoke", "cannot revoke the current field key")
	}
	if kr.keys[idx].Revoked {
		return nil
	}
	kr.keys[idx].Revoked = true
	kr.keys[idx].Material = [clientenc.KeySize]byte{}
	return kr.persistLocked()
}

// Reload re-reads the keyring file. On any error the in-memory keyring is
// left unchanged (last known good).
func (kr *FileFieldKeyring) Reload() error {
	kr.mu.Lock()
	defer kr.mu.Unlock()
	raw, err := os.ReadFile(kr.path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "nextsql.FileFieldKeyring.Reload", "read", err)
	}
	keys, err := decodeFieldKeyring(raw)
	if err != nil {
		return err
	}
	kr.keys = keys
	return nil
}

// List returns material-free summaries, oldest first.
func (kr *FileFieldKeyring) List() []FieldKeyInfo {
	kr.mu.RLock()
	defer kr.mu.RUnlock()
	out := make([]FieldKeyInfo, 0, len(kr.keys))
	for i := range kr.keys {
		out = append(out, FieldKeyInfo{ID: kr.keys[i].ID, Created: kr.keys[i].Created, Current: kr.keys[i].Current, Revoked: kr.keys[i].Revoked})
	}
	return out
}

func (kr *FileFieldKeyring) persistLocked() error {
	raw, err := encodeFieldKeyring(kr.keys)
	if err != nil {
		return err
	}
	return atomicWriteFieldKeyring(kr.path, raw)
}

func encodeFieldKeyring(keys []fieldKeyRecord) ([]byte, error) {
	if len(keys) > maxFieldKeyringKeys {
		return nil, nerr.New(nerr.InvalidArgument, "nextsql.encodeFieldKeyring", "too many field keys")
	}
	n := 4 + 2 + 2
	for i := range keys {
		n += 1 + len(keys[i].ID) + 8 + 1 + clientenc.KeySize
	}
	buf := make([]byte, n)
	copy(buf[0:4], fieldKeyringMagic)
	encoding.PutU16(buf, 4, fieldKeyringVersion)
	encoding.PutU16(buf, 6, uint16(len(keys)))
	off := 8
	for i := range keys {
		if len(keys[i].ID) == 0 || len(keys[i].ID) > clientenc.MaxKeyIDBytes {
			return nil, nerr.New(nerr.InvalidFormat, "nextsql.encodeFieldKeyring", "invalid field key id length")
		}
		buf[off] = byte(len(keys[i].ID))
		off++
		copy(buf[off:], keys[i].ID)
		off += len(keys[i].ID)
		encoding.PutU64(buf, off, uint64(keys[i].Created.Unix()))
		off += 8
		var flags byte
		if keys[i].Current {
			flags |= fkFlagCurrent
		}
		if keys[i].Revoked {
			flags |= fkFlagRevoked
		}
		buf[off] = flags
		off++
		copy(buf[off:], keys[i].Material[:])
		off += clientenc.KeySize
	}
	return buf[:off], nil
}

func decodeFieldKeyring(raw []byte) ([]fieldKeyRecord, error) {
	bad := func(msg string) ([]fieldKeyRecord, error) {
		return nil, nerr.New(nerr.InvalidFormat, "nextsql.decodeFieldKeyring", msg)
	}
	if len(raw) < 8 {
		return bad("truncated keyring")
	}
	if string(raw[0:4]) != fieldKeyringMagic {
		return bad("bad keyring magic")
	}
	if encoding.U16(raw, 4) != fieldKeyringVersion {
		return bad("unsupported keyring version")
	}
	count := encoding.U16(raw, 6)
	if int(count) > maxFieldKeyringKeys {
		return bad("key count exceeds limit")
	}
	keys := make([]fieldKeyRecord, 0, count)
	seen := make(map[string]struct{}, count)
	off := 8
	current := 0
	for i := 0; i < int(count); i++ {
		if off >= len(raw) {
			return bad("truncated id length")
		}
		idLen := int(raw[off])
		off++
		if idLen < 1 || idLen > clientenc.MaxKeyIDBytes {
			return bad("invalid field key id length")
		}
		idBytes, err := encoding.ReadBytes(raw, off, idLen)
		if err != nil {
			return bad("truncated field key id")
		}
		off += idLen
		id := string(idBytes)
		if !validFieldKeyringID(id) {
			return bad("invalid field key id")
		}
		created, err := encoding.ReadU64(raw, off)
		if err != nil {
			return bad("truncated created time")
		}
		off += 8
		if off >= len(raw) {
			return bad("truncated flags")
		}
		flags := raw[off]
		off++
		material, err := encoding.ReadBytes(raw, off, clientenc.KeySize)
		if err != nil {
			return bad("truncated field key material")
		}
		off += clientenc.KeySize
		if _, dup := seen[id]; dup {
			return bad("duplicate field key id")
		}
		seen[id] = struct{}{}
		rec := fieldKeyRecord{
			ID:      id,
			Created: time.Unix(int64(created), 0).UTC(),
			Current: flags&fkFlagCurrent != 0,
			Revoked: flags&fkFlagRevoked != 0,
		}
		copy(rec.Material[:], material)
		if rec.Current && rec.Revoked {
			return bad("current field key cannot be revoked")
		}
		if rec.Revoked {
			if rec.Material != ([clientenc.KeySize]byte{}) {
				return bad("revoked field key retains material")
			}
		} else {
			var any byte
			for _, b := range rec.Material {
				any |= b
			}
			if any == 0 {
				return bad("empty field key material")
			}
		}
		if rec.Current {
			current++
		}
		keys = append(keys, rec)
	}
	if off != len(raw) {
		return bad("trailing keyring bytes")
	}
	if len(keys) == 0 {
		return bad("keyring has no keys")
	}
	if current != 1 {
		return bad("keyring must have exactly one current key")
	}
	return keys, nil
}

func validFieldKeyringID(id string) bool {
	if len(id) < 1 || len(id) > clientenc.MaxKeyIDBytes {
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

func validateFieldKeyForKeyring(key FieldKey) error {
	if !validFieldKeyringID(key.ID) {
		return nerr.New(nerr.InvalidArgument, "nextsql.FileFieldKeyring", "invalid field key id")
	}
	var any byte
	for _, b := range key.Material {
		any |= b
	}
	if any == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql.FileFieldKeyring", "empty field key material")
	}
	return nil
}

func atomicWriteFieldKeyring(path string, raw []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "nextsql.FileFieldKeyring", "write", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "nextsql.FileFieldKeyring", "rename", err)
	}
	return nil
}

// Conn is one authenticated session.
type Conn struct {
	cfg    Config
	mu     sync.Mutex
	raw    net.Conn
	secret uint64
	lim    protocol.Limits
	busy   bool
}

type Result struct {
	Affected int64
	Columns  []string
	Rows     [][]types.Value
}

type Rows struct {
	c        *Conn
	cols     []string
	types    []types.Type
	batch    [][]types.Value
	i        int
	done     bool
	closed   bool
	err      error
	affected int64
	stop     func() bool
}

type Stmt struct {
	c  *Conn
	id uint32
}

func Open(cfg Config) (*Conn, error) {
	return OpenContext(context.Background(), cfg)
}

func OpenContext(ctx context.Context, cfg Config) (*Conn, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}
	d := net.Dialer{}
	raw, err := d.DialContext(ctx, "tcp", cfg.Address)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "nextsql.Open", "dial", err)
	}
	if cfg.TLS != nil {
		tc := cfg.TLS.Clone()
		if tc.MinVersion < tls.VersionTLS13 {
			tc.MinVersion = tls.VersionTLS13
		}
		if tc.ServerName == "" {
			host, _, _ := net.SplitHostPort(cfg.Address)
			tc.ServerName = host
		}
		tlsConn := tls.Client(raw, tc)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			_ = raw.Close()
			return nil, nerr.Wrap(nerr.Protocol, "nextsql.Open", "tls handshake", err)
		}
		raw = tlsConn
	}
	c := &Conn{cfg: cfg, raw: raw, lim: protocol.DefaultLimits()}
	if err := c.handshake(); err != nil {
		_ = raw.Close()
		return nil, err
	}
	if cfg.ReadConsistency != Strong {
		if err := c.SetReadConsistency(ctx, cfg.ReadConsistency, cfg.MaxStaleness); err != nil {
			_ = raw.Close()
			return nil, err
		}
	}
	return c, nil
}

func validateConfig(cfg Config) error {
	if cfg.Address == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "address is required")
	}
	addr := strings.ToLower(cfg.Address)
	if strings.Contains(addr, "://") || strings.Contains(addr, "key=") || strings.Contains(addr, "password=") {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "keys and credentials must not be passed in a URL")
	}
	if cfg.TLS == nil && !cfg.InsecureNoTLS {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "TLS is required for remote connections")
	}
	if cfg.InsecureNoTLS && security.RequireTLS(cfg.Address) {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "plaintext is only allowed on loopback")
	}
	if cfg.User == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql.Open", "user is required")
	}
	return nil
}

func (c *Conn) handshake() error {
	payload, err := protocol.EncodeHello(protocol.Hello{
		Version:  protocol.Version,
		Database: c.cfg.Database,
		User:     c.cfg.User,
		Realm:    c.cfg.Realm,
	}, c.lim)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeHello, payload, c.lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeHelloOK {
		return unexpected(typ, body, c.lim)
	}
	ok, err := protocol.DecodeHelloOK(body)
	if err != nil {
		return err
	}
	c.secret = ok.Secret
	authPayload, err := protocol.EncodeAuth(protocol.Auth{Password: c.cfg.Password}, c.lim)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeAuth, authPayload, c.lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err = c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeAuthOK {
		return unexpected(typ, body, c.lim)
	}
	if ok.AuthMethod == protocol.AuthPasswordKey {
		if c.cfg.KeyProvider == nil {
			return nerr.New(nerr.Unauthorized, "nextsql.Open", "server requires a client-held key")
		}
		dek, err := c.cfg.KeyProvider.Current()
		if err != nil {
			return err
		}
		mat, err := crypto.EncodeUnlockMaterial(dek)
		if err != nil {
			return err
		}
		if err := protocol.WriteFrame(c.raw, protocol.TypeUnlock, mat, c.lim.MaxPacket); err != nil {
			return err
		}
		typ, body, err = c.read()
		if err != nil {
			return err
		}
		if typ != protocol.TypeUnlockOK {
			return unexpected(typ, body, c.lim)
		}
	}
	typ, body, err = c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeReady {
		return unexpected(typ, body, c.lim)
	}
	return nil
}

func (c *Conn) read() (protocol.Type, []byte, error) {
	return protocol.ReadFrame(c.raw, c.lim.MaxPacket)
}

func (c *Conn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return nil
	}
	_ = protocol.WriteFrame(c.raw, protocol.TypeTerminate, nil, c.lim.MaxPacket)
	err := c.raw.Close()
	c.raw = nil
	return err
}

func (c *Conn) Exec(ctx context.Context, sql string, params ...types.Value) (*Result, error) {
	rows, err := c.Query(ctx, sql, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Result{Columns: rows.Columns()}
	for rows.Next() {
		out.Rows = append(out.Rows, append([]types.Value(nil), rows.Values()...))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

// ExecIdempotent executes a retryable mutation under a durable, user/tenant-
// scoped key. Reusing the key with the same typed request returns the original
// committed result; reusing it for a different request fails with conflict.
func (c *Conn) ExecIdempotent(ctx context.Context, key, sql string, params ...types.Value) (*Result, error) {
	payload, err := protocol.EncodeIdempotentQuery(protocol.IdempotentQuery{
		Key: key, SQL: sql, Params: wireParams(params),
	}, c.lim)
	if err != nil {
		return nil, err
	}
	rows, err := c.queryPayload(ctx, protocol.TypeIdempotentQuery, payload)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Result{Columns: rows.Columns()}
	for rows.Next() {
		out.Rows = append(out.Rows, append([]types.Value(nil), rows.Values()...))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

func (c *Conn) Query(ctx context.Context, sql string, params ...types.Value) (*Rows, error) {
	payload, err := protocol.EncodeQuery(protocol.Query{SQL: sql, Params: wireParams(params)}, c.lim)
	if err != nil {
		return nil, err
	}
	return c.queryPayload(ctx, protocol.TypeQuery, payload)
}

// EncryptField converts one logical value into the opaque STRING parameter an
// ENCRYPTED CLIENT column accepts. Randomized encryption intentionally provides
// no equality/search token. SQL NULL passes through and leaks only nullness.
func (c *Conn) EncryptField(ctx context.Context, table, column string, value types.Value) (types.Value, error) {
	if c == nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "nextsql.EncryptField", "nil connection")
	}
	if value.Null {
		return types.Null(types.String()), nil
	}
	ciphertext, err := clientenc.Encrypt(ctx, c.cfg.FieldKeys, c.cfg.Database, table, column, value)
	if err != nil {
		return types.Value{}, err
	}
	return types.StringValue(ciphertext), nil
}

// DecryptField authenticates one opaque result value and returns its logical
// SQL value. expected is checked against the authenticated envelope type.
func (c *Conn) DecryptField(ctx context.Context, table, column string, expected types.Type, value types.Value) (types.Value, error) {
	if c == nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "nextsql.DecryptField", "nil connection")
	}
	if !clientenc.SupportedType(expected) {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "nextsql.DecryptField", "unsupported client-encrypted type")
	}
	if value.Null {
		return types.Null(expected), nil
	}
	if value.Typ.Kind != types.KindString && value.Typ.Kind != types.KindText {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "nextsql.DecryptField", "client ciphertext is not a string")
	}
	h, err := clientenc.Inspect(value.Str)
	if err != nil {
		return types.Value{}, err
	}
	if !h.LogicalType.Equals(expected) {
		return types.Value{}, nerr.New(nerr.InvalidFormat, "nextsql.DecryptField", "encrypted logical type mismatch")
	}
	return clientenc.Decrypt(ctx, c.cfg.FieldKeys, c.cfg.Database, table, column, value.Str)
}

func (c *Conn) queryPayload(ctx context.Context, typ protocol.Type, payload []byte) (*Rows, error) {
	c.mu.Lock()
	if c.raw == nil {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Unavailable, "nextsql.Query", "connection closed")
	}
	if c.busy {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Conflict, "nextsql.Query", "connection is busy")
	}
	c.busy = true
	if err := protocol.WriteFrame(c.raw, typ, payload, c.lim.MaxPacket); err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	rows, err := c.readRows()
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		conn := c
		rows.stop = context.AfterFunc(ctx, func() { _ = conn.Cancel(context.Background()) })
	}
	return rows, nil
}

func (c *Conn) readRows() (*Rows, error) {
	typ, body, err := c.read()
	if err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	rows := &Rows{c: c}
	switch typ {
	case protocol.TypeRowDesc:
		desc, err := protocol.DecodeRowDesc(body, c.lim)
		if err != nil {
			c.busy = false
			c.mu.Unlock()
			return nil, err
		}
		rows.cols = make([]string, len(desc.Columns))
		rows.types = make([]types.Type, len(desc.Columns))
		for i, col := range desc.Columns {
			rows.cols[i] = col.Name
			rows.types[i] = col.Type
		}
		return rows, nil
	case protocol.TypeCommandComplete:
		cc, err := protocol.DecodeCommandComplete(body)
		if err != nil {
			c.busy = false
			c.mu.Unlock()
			return nil, err
		}
		rows.affected = cc.Affected
		rows.done = true
		if err := c.expectReady(); err != nil {
			c.busy = false
			c.mu.Unlock()
			return nil, err
		}
		c.busy = false
		c.mu.Unlock()
		rows.closed = true
		return rows, nil
	default:
		err := unexpected(typ, body, c.lim)
		if typ == protocol.TypeError {
			// writeErrReady sends Error then Ready; drain Ready so the session stays usable.
			_ = c.expectReady()
		}
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
}

func (c *Conn) expectReady() error {
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeReady {
		return unexpected(typ, body, c.lim)
	}
	return nil
}

// readAck reads a single control acknowledgement: TypeReady, or TypeError
// followed by TypeReady (which is drained so the session stays usable).
func (c *Conn) readAck() error {
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ == protocol.TypeReady {
		return nil
	}
	e := unexpected(typ, body, c.lim)
	if typ == protocol.TypeError {
		_ = c.expectReady()
	}
	return e
}

// NodeStatus is a server node's key-free replication health snapshot.
type NodeStatus struct {
	Role          string // leader, follower, candidate, shutdown, standalone
	HasLeader     bool
	Healthy       bool
	AppliedLSN    uint64
	LastContactMS int64
	ApplyBacklog  uint64
}

// SetReadConsistency sets this connection's read-consistency mode for
// subsequent statements. maxStaleness applies only to Bounded (0 = server
// default).
func (c *Conn) SetReadConsistency(ctx context.Context, mode ReadConsistency, maxStaleness time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return nerr.New(nerr.Unavailable, "nextsql.SetReadConsistency", "connection closed")
	}
	if c.busy {
		return nerr.New(nerr.Conflict, "nextsql.SetReadConsistency", "connection is busy")
	}
	var ms uint64
	if maxStaleness > 0 {
		// Keep a sub-millisecond bound positive so it is not read as "server
		// default" (0). Real staleness bounds are seconds.
		if ms = uint64(maxStaleness.Milliseconds()); ms == 0 {
			ms = 1
		}
	}
	payload := protocol.EncodeSetReadConsistency(protocol.SetReadConsistency{Mode: uint8(mode), MaxStalenessMS: ms})
	if err := protocol.WriteFrame(c.raw, protocol.TypeSetReadConsistency, payload, c.lim.MaxPacket); err != nil {
		return err
	}
	return c.readAck()
}

// NodeStatus asks the connected server for its replication health.
func (c *Conn) NodeStatus(ctx context.Context) (NodeStatus, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return NodeStatus{}, nerr.New(nerr.Unavailable, "nextsql.NodeStatus", "connection closed")
	}
	if c.busy {
		return NodeStatus{}, nerr.New(nerr.Conflict, "nextsql.NodeStatus", "connection is busy")
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeNodeStatus, nil, c.lim.MaxPacket); err != nil {
		return NodeStatus{}, err
	}
	typ, body, err := c.read()
	if err != nil {
		return NodeStatus{}, err
	}
	if typ != protocol.TypeNodeStatusResp {
		e := unexpected(typ, body, c.lim)
		if typ == protocol.TypeError {
			_ = c.expectReady()
		}
		return NodeStatus{}, e
	}
	ns, err := protocol.DecodeNodeStatus(body, c.lim)
	if err != nil {
		return NodeStatus{}, err
	}
	if err := c.expectReady(); err != nil {
		return NodeStatus{}, err
	}
	return NodeStatus{
		Role:          ns.Role,
		HasLeader:     ns.HasLeader,
		Healthy:       ns.Healthy,
		AppliedLSN:    ns.AppliedLSN,
		LastContactMS: ns.LastContactMS,
		ApplyBacklog:  ns.ApplyBacklog,
	}, nil
}

func (c *Conn) Prepare(ctx context.Context, sql string) (*Stmt, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil {
		return nil, nerr.New(nerr.Unavailable, "nextsql.Prepare", "connection closed")
	}
	payload, err := protocol.EncodePrepare(sql, c.lim)
	if err != nil {
		return nil, err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypePrepare, payload, c.lim.MaxPacket); err != nil {
		return nil, err
	}
	typ, body, err := c.read()
	if err != nil {
		return nil, err
	}
	if typ != protocol.TypePrepareOK {
		return nil, unexpected(typ, body, c.lim)
	}
	id, err := protocol.DecodePrepareOK(body)
	if err != nil {
		return nil, err
	}
	if err := c.expectReady(); err != nil {
		return nil, err
	}
	return &Stmt{c: c, id: id}, nil
}

func (c *Conn) Cancel(_ context.Context) error {
	// Must not take c.mu: an in-flight Query holds it until Rows.Close.
	secret := c.secret
	if secret == 0 {
		return nerr.New(nerr.Unavailable, "nextsql.Cancel", "not connected")
	}
	side, err := dialRaw(c.cfg.Address, c.cfg.TLS, c.cfg.InsecureNoTLS)
	if err != nil {
		return err
	}
	defer side.Close()
	lim := protocol.DefaultLimits()
	payload, err := protocol.EncodeHello(protocol.Hello{
		Version: protocol.Version,
		Flags:   protocol.FlagCancel,
		Secret:  secret,
	}, lim)
	if err != nil {
		return err
	}
	if err := protocol.WriteFrame(side, protocol.TypeHello, payload, lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err := protocol.ReadFrame(side, lim.MaxPacket)
	if err != nil {
		return err
	}
	if typ != protocol.TypeReady {
		return unexpected(typ, body, lim)
	}
	return nil
}

func (s *Stmt) Exec(ctx context.Context, params ...types.Value) (*Result, error) {
	rows, err := s.Query(ctx, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := &Result{Columns: rows.Columns()}
	for rows.Next() {
		out.Rows = append(out.Rows, append([]types.Value(nil), rows.Values()...))
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out.Affected = rows.Affected()
	return out, nil
}

func (s *Stmt) Query(ctx context.Context, params ...types.Value) (*Rows, error) {
	c := s.c
	c.mu.Lock()
	if c.raw == nil {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Unavailable, "nextsql.Stmt", "connection closed")
	}
	if c.busy {
		c.mu.Unlock()
		return nil, nerr.New(nerr.Conflict, "nextsql.Stmt", "connection is busy")
	}
	c.busy = true
	payload, err := protocol.EncodeExecute(protocol.Execute{ID: s.id, Params: wireParams(params)}, c.lim)
	if err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeExecute, payload, c.lim.MaxPacket); err != nil {
		c.busy = false
		c.mu.Unlock()
		return nil, err
	}
	rows, err := c.readRows()
	if err != nil {
		return nil, err
	}
	if ctx != nil {
		conn := c
		rows.stop = context.AfterFunc(ctx, func() { _ = conn.Cancel(context.Background()) })
	}
	return rows, nil
}

func (s *Stmt) Close() error {
	c := s.c
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.raw == nil || s.id == 0 {
		return nil
	}
	if err := protocol.WriteFrame(c.raw, protocol.TypeCloseStmt, protocol.EncodeCloseStmt(s.id), c.lim.MaxPacket); err != nil {
		return err
	}
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	if typ != protocol.TypeCloseOK {
		return unexpected(typ, body, c.lim)
	}
	if err := c.expectReady(); err != nil {
		return err
	}
	s.id = 0
	return nil
}

func (r *Rows) Columns() []string { return append([]string(nil), r.cols...) }

func (r *Rows) ColumnTypes() []types.Type { return append([]types.Type(nil), r.types...) }

func (r *Rows) Affected() int64 { return r.affected }

func (r *Rows) Err() error { return r.err }

func (r *Rows) Values() []types.Value {
	if r.i <= 0 || r.i > len(r.batch) {
		return nil
	}
	return r.batch[r.i-1]
}

func (r *Rows) Next() bool {
	if r.closed || r.err != nil {
		return false
	}
	if r.i < len(r.batch) {
		r.i++
		return true
	}
	if r.done {
		return false
	}
	if err := r.fill(); err != nil {
		r.err = err
		r.finishLocked()
		return false
	}
	if r.i < len(r.batch) {
		r.i++
		return true
	}
	return false
}

func (r *Rows) fill() error {
	c := r.c
	if !r.done && len(r.batch) > 0 {
		if err := protocol.WriteFrame(c.raw, protocol.TypeFlowAck, nil, c.lim.MaxPacket); err != nil {
			return err
		}
	}
	typ, body, err := c.read()
	if err != nil {
		return err
	}
	switch typ {
	case protocol.TypeDataBatch:
		batch, err := protocol.DecodeDataBatch(body, c.lim)
		if err != nil {
			return err
		}
		r.batch = batch.Rows
		r.i = 0
		return nil
	case protocol.TypeCommandComplete:
		cc, err := protocol.DecodeCommandComplete(body)
		if err != nil {
			return err
		}
		r.affected = cc.Affected
		r.done = true
		r.batch = nil
		r.i = 0
		if err := c.expectReady(); err != nil {
			return err
		}
		r.finishLocked()
		return nil
	default:
		err := unexpected(typ, body, c.lim)
		if typ == protocol.TypeError {
			// Streaming errors are followed by Ready. Drain it before releasing
			// the connection so the next statement starts on a frame boundary.
			if readyErr := c.expectReady(); readyErr != nil {
				return readyErr
			}
		}
		return err
	}
}

func (r *Rows) Close() error {
	if r.closed {
		return r.err
	}
	for r.Next() {
	}
	if !r.closed && r.c != nil {
		r.finishLocked()
	}
	return r.err
}

func (r *Rows) finishLocked() {
	if r.stop != nil {
		r.stop()
		r.stop = nil
	}
	if r.c != nil && !r.closed {
		r.c.busy = false
		r.c.mu.Unlock()
	}
	r.closed = true
}

func wireParams(vals []types.Value) []executor.Param {
	if len(vals) == 0 {
		return nil
	}
	out := make([]executor.Param, len(vals))
	for i, v := range vals {
		out[i] = executor.Param{Value: v}
	}
	return out
}

func unexpected(typ protocol.Type, body []byte, lim protocol.Limits) error {
	if typ == protocol.TypeError {
		em, err := protocol.DecodeError(body, lim)
		if err != nil {
			return err
		}
		return nerr.New(nerr.Code(em.Code), "nextsql", em.Message)
	}
	return nerr.New(nerr.Protocol, "nextsql", "unexpected message type")
}

func dialRaw(addr string, tlsCfg *tls.Config, insecure bool) (net.Conn, error) {
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "nextsql.Cancel", "dial", err)
	}
	if tlsCfg != nil {
		tc := tlsCfg.Clone()
		if tc.MinVersion < tls.VersionTLS13 {
			tc.MinVersion = tls.VersionTLS13
		}
		if tc.ServerName == "" {
			host, _, _ := net.SplitHostPort(addr)
			tc.ServerName = host
		}
		tlsConn := tls.Client(raw, tc)
		if err := tlsConn.Handshake(); err != nil {
			_ = raw.Close()
			return nil, nerr.Wrap(nerr.Protocol, "nextsql.Cancel", "tls handshake", err)
		}
		return tlsConn, nil
	}
	_ = insecure
	return raw, nil
}
