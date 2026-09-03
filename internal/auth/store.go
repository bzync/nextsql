// Package auth stores login credentials. Passwords are never persisted in plaintext.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"os"
	"sort"
	"strings"
	"sync"

	"golang.org/x/crypto/argon2"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	fileMagic     = "NSAU"
	fileVersionV1 = 1 // legacy: every record is PBKDF2-HMAC-SHA256, no Algo/Memory/Threads fields
	fileVersionV2 = 2 // legacy: adds Algo/Memory/Threads; no RealmID (every record is deployment-wide)
	fileVersion   = 3 // current: adds a 16-byte RealmID per record (M2-4b-1); Decode reads v1, v2, or v3
	saltSize      = 16
	hashSize      = 32
	defaultIter   = 100_000 // legacy PBKDF2 iteration count; new records use Argon2id instead
	maxUserLen    = 128
	maxPassLen    = 1024
	maxUsers      = 4096

	// algoPBKDF2 marks a legacy record (RFC 8018 PBKDF2-HMAC-SHA256, Iter
	// only). algoArgon2id marks a current record (Iter is Argon2id's time
	// cost, Memory is its KiB memory cost, Threads is its parallelism).
	algoPBKDF2   byte = 0
	algoArgon2id byte = 1

	// Argon2id defaults for new/rehashed records, per the golang.org/x/crypto/
	// argon2 package documentation's recommended parameters.
	argon2Time      uint32 = 1
	argon2MemoryKiB uint32 = 64 * 1024
	argon2Threads   uint8  = 4

	// Decoded password records are persistent, untrusted input. Bound every
	// Argon2 parameter before calling argon2.IDKey so a corrupt auth file
	// cannot turn one login into an unbounded allocation or CPU request.
	maxArgon2Time      uint32 = 10
	maxArgon2MemoryKiB uint32 = 256 * 1024
	maxArgon2Threads   uint8  = 32
)

type record struct {
	Algo    byte
	Salt    [saltSize]byte
	Iter    uint32
	Memory  uint32
	Threads uint8
	Hash    [hashSize]byte
}

// userKey is the composite (Realm, Name) key each user record is stored
// under (M2-4b-1). hosting.ID{} (the zero value) means "deployment-wide" —
// every record from a file written before this change decodes with an
// implicit zero Realm, and every method that does not take a realm
// parameter operates at hosting.ID{}, so a non-hosted deployment (which
// never resolves any other realm) sees byte-identical behavior to before.
type userKey struct {
	Realm hosting.ID
	Name  string
}

// Store is a versioned on-disk user directory. Verify is constant-time on the hash.
type Store struct {
	mu    sync.Mutex
	path  string
	users map[userKey]record
}

func Create(path string) (*Store, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "auth.Create", "auth file exists")
	}
	s := &Store{path: path, users: make(map[userKey]record)}
	if err := s.persist(); err != nil {
		return nil, err
	}
	return s, nil
}

func Open(path string) (*Store, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "auth.Open", "read", err)
	}
	users, err := Decode(raw)
	if err != nil {
		return nil, err
	}
	return &Store{path: path, users: users}, nil
}

// ReadPasswordFile loads a password from a file. The value is never logged.
func ReadPasswordFile(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", nerr.Wrap(nerr.IO, "auth.ReadPasswordFile", "read", err)
	}
	pw := strings.TrimRight(string(raw), "\r\n")
	if err := validatePassword(pw); err != nil {
		return "", err
	}
	return pw, nil
}

func OpenOrCreate(path string) (*Store, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return Create(path)
	}
	return Open(path)
}

func (s *Store) Path() string { return s.path }

func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.users)
}

// Has reports whether the deployment-wide (hosting.ID{}) user exists. See HasInRealm.
func (s *Store) Has(user string) bool { return s.HasInRealm(hosting.ID{}, user) }

// HasInRealm reports whether user exists in realm, falling back to a
// deployment-wide (hosting.ID{}) entry of the same name when realm is not
// hosting.ID{} and has no entry of its own (see userKey).
func (s *Store) HasInRealm(realm hosting.ID, user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	name := normalize(user)
	if _, ok := s.users[userKey{Realm: realm, Name: name}]; ok {
		return true
	}
	if realm == (hosting.ID{}) {
		return false
	}
	_, ok := s.users[userKey{Name: name}]
	return ok
}

// Delete removes the deployment-wide (hosting.ID{}) user. See DeleteInRealm.
func (s *Store) Delete(user string) error { return s.DeleteInRealm(hosting.ID{}, user) }

// DeleteInRealm removes user from realm only; it never falls back to (and
// never deletes) a deployment-wide entry of the same name.
func (s *Store) DeleteInRealm(realm hosting.ID, user string) error {
	name, err := validateUser(user)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey{Realm: realm, Name: name}
	if _, ok := s.users[key]; !ok {
		return nerr.New(nerr.NotFound, "auth.Delete", "unknown user")
	}
	delete(s.users, key)
	return s.persist()
}

// Users lists deployment-wide (hosting.ID{}) user names.
func (s *Store) Users() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.users))
	for key := range s.users {
		if key.Realm == (hosting.ID{}) {
			out = append(out, key.Name)
		}
	}
	return out
}

// UserInfo is a read-only, hash-free summary of one stored user, for
// introspection surfaces such as system.users.
type UserInfo struct {
	Name string
	Algo string // "argon2id" or "pbkdf2"; never the hash or salt.
}

// Snapshot returns every deployment-wide (hosting.ID{}) user's name and
// password-hash algorithm. See SnapshotInRealm.
func (s *Store) Snapshot() []UserInfo { return s.SnapshotInRealm(hosting.ID{}) }

// SnapshotInRealm returns realm's users' names and password-hash
// algorithms, sorted by name. It never includes the hash, salt, or any
// other secret material. Realm-scoped only: it does not also return
// deployment-wide entries when realm is not hosting.ID{} (unlike
// VerifyInRealm/HasInRealm's shadow fallback), since this lists what a
// realm's own principal namespace actually contains, not who could
// authenticate into it.
func (s *Store) SnapshotInRealm(realm hosting.ID) []UserInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]UserInfo, 0, len(s.users))
	for key, rec := range s.users {
		if key.Realm != realm {
			continue
		}
		algo := "pbkdf2"
		if rec.Algo == algoArgon2id {
			algo = "argon2id"
		}
		out = append(out, UserInfo{Name: key.Name, Algo: algo})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Upsert creates or updates the deployment-wide (hosting.ID{}) user. See UpsertInRealm.
func (s *Store) Upsert(user, password string) error {
	return s.UpsertInRealm(hosting.ID{}, user, password)
}

// UpsertInRealm creates or updates user within realm.
func (s *Store) UpsertInRealm(realm hosting.ID, user, password string) error {
	name, err := validateUser(user)
	if err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	rec, err := hashPasswordArgon2id(password)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.users == nil {
		s.users = make(map[userKey]record)
	}
	key := userKey{Realm: realm, Name: name}
	if _, ok := s.users[key]; !ok && len(s.users) >= maxUsers {
		return nerr.New(nerr.InvalidArgument, "auth.Upsert", "too many users")
	}
	s.users[key] = rec
	return s.persist()
}

// Verify authenticates against the deployment-wide (hosting.ID{}) user. See VerifyInRealm.
func (s *Store) Verify(user, password string) error {
	return s.VerifyInRealm(hosting.ID{}, user, password)
}

// VerifyInRealm authenticates user/password against realm's own entry; if
// realm is not hosting.ID{} and has no entry of its own, it falls back to
// the deployment-wide (hosting.ID{}) entry of the same name — a realm's own
// principal shadows a same-named deployment-wide one when both exist.
func (s *Store) VerifyInRealm(realm hosting.ID, user, password string) error {
	name, err := validateUser(user)
	if err != nil {
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	if err := validatePassword(password); err != nil {
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	foundRealm := realm
	s.mu.Lock()
	rec, ok := s.users[userKey{Realm: realm, Name: name}]
	if !ok && realm != (hosting.ID{}) {
		rec, ok = s.users[userKey{Name: name}]
		foundRealm = hosting.ID{}
	}
	s.mu.Unlock()
	if !ok {
		// Use the current algorithm and cost so missing users are not a cheap
		// username-enumeration path relative to an existing Argon2id user.
		dummy := record{
			Algo: algoArgon2id, Iter: argon2Time,
			Memory: argon2MemoryKiB, Threads: argon2Threads,
		}
		_ = checkPassword(password, dummy)
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	if err := checkPassword(password, rec); err != nil {
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	if rec.Algo == algoPBKDF2 {
		s.rehash(foundRealm, name, password)
	}
	return nil
}

// rehash transparently upgrades a legacy PBKDF2 record to Argon2id after a
// successful login, using the already-confirmed-correct password. Best
// effort: a persist failure here must not fail a login that already
// verified correctly, and the record is only replaced if it is still the
// same legacy record (avoids clobbering a concurrent delete/re-upsert). realm
// is the key the record was actually found under (VerifyInRealm's own
// fallback resolution), not necessarily the realm that was queried.
func (s *Store) rehash(realm hosting.ID, name, password string) {
	newRec, err := hashPasswordArgon2id(password)
	if err != nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	key := userKey{Realm: realm, Name: name}
	cur, ok := s.users[key]
	if !ok || cur.Algo != algoPBKDF2 {
		return
	}
	s.users[key] = newRec
	_ = s.persist()
}

func validateUser(user string) (string, error) {
	name := normalize(user)
	if name == "" || len(name) > maxUserLen {
		return "", nerr.New(nerr.InvalidArgument, "auth", "invalid user name")
	}
	return name, nil
}

func validatePassword(password string) error {
	if password == "" || len(password) > maxPassLen {
		return nerr.New(nerr.InvalidArgument, "auth", "invalid password")
	}
	return nil
}

func normalize(user string) string { return strings.ToLower(strings.TrimSpace(user)) }

// hashPasswordPBKDF2 builds a legacy-format record. It exists so tests can
// construct and decode-round-trip pre-Argon2id records; production code
// paths (Upsert, rehash) always use hashPasswordArgon2id for new hashes.
func hashPasswordPBKDF2(password string, iter uint32) (record, error) {
	var rec record
	rec.Algo = algoPBKDF2
	rec.Iter = iter
	if _, err := rand.Read(rec.Salt[:]); err != nil {
		return record{}, nerr.Wrap(nerr.Internal, "auth.hashPasswordPBKDF2", "rand", err)
	}
	sum := pbkdf2SHA256([]byte(password), rec.Salt[:], int(iter), hashSize)
	copy(rec.Hash[:], sum)
	return rec, nil
}

func hashPasswordArgon2id(password string) (record, error) {
	var rec record
	rec.Algo = algoArgon2id
	rec.Iter = argon2Time
	rec.Memory = argon2MemoryKiB
	rec.Threads = argon2Threads
	if _, err := rand.Read(rec.Salt[:]); err != nil {
		return record{}, nerr.Wrap(nerr.Internal, "auth.hashPasswordArgon2id", "rand", err)
	}
	sum := argon2.IDKey([]byte(password), rec.Salt[:], rec.Iter, rec.Memory, rec.Threads, hashSize)
	copy(rec.Hash[:], sum)
	return rec, nil
}

func checkPassword(password string, rec record) error {
	if err := validateRecord(rec); err != nil {
		return err
	}
	var got []byte
	switch rec.Algo {
	case algoPBKDF2:
		got = pbkdf2SHA256([]byte(password), rec.Salt[:], int(rec.Iter), hashSize)
	case algoArgon2id:
		got = argon2.IDKey([]byte(password), rec.Salt[:], rec.Iter, rec.Memory, rec.Threads, hashSize)
	default:
		return nerr.New(nerr.InvalidFormat, "auth.checkPassword", "unsupported password hash algorithm")
	}
	if subtle.ConstantTimeCompare(got, rec.Hash[:]) != 1 {
		return nerr.New(nerr.Unauthorized, "auth.checkPassword", "mismatch")
	}
	return nil
}

func validateRecord(rec record) error {
	switch rec.Algo {
	case algoPBKDF2:
		if rec.Iter < 1 || rec.Iter > 10_000_000 || rec.Memory != 0 || rec.Threads != 0 {
			return nerr.New(nerr.InvalidFormat, "auth.passwordRecord", "invalid PBKDF2 parameters")
		}
	case algoArgon2id:
		// argon2.IDKey requires memory >= 8*parallelism. Keep that library
		// precondition explicit so malformed input returns an error, not a panic.
		minMemory := uint32(rec.Threads) * 8
		if rec.Iter < 1 || rec.Iter > maxArgon2Time ||
			rec.Threads < 1 || rec.Threads > maxArgon2Threads ||
			rec.Memory < minMemory || rec.Memory > maxArgon2MemoryKiB {
			return nerr.New(nerr.InvalidFormat, "auth.passwordRecord", "invalid Argon2id parameters")
		}
	default:
		return nerr.New(nerr.InvalidFormat, "auth.passwordRecord", "unsupported password hash algorithm")
	}
	return nil
}

// pbkdf2SHA256 is RFC 8018 PBKDF2-HMAC-SHA256. It is not a custom primitive.
func pbkdf2SHA256(password, salt []byte, iter, keyLen int) []byte {
	if iter < 1 || keyLen < 1 {
		return nil
	}
	hLen := sha256.Size
	nblock := (keyLen + hLen - 1) / hLen
	out := make([]byte, 0, nblock*hLen)
	var block [4]byte
	for i := 1; i <= nblock; i++ {
		block[0] = byte(i >> 24)
		block[1] = byte(i >> 16)
		block[2] = byte(i >> 8)
		block[3] = byte(i)
		mac := hmac.New(sha256.New, password)
		mac.Write(salt)
		mac.Write(block[:])
		u := mac.Sum(nil)
		t := append([]byte(nil), u...)
		for j := 1; j < iter; j++ {
			mac.Reset()
			mac.Write(u)
			u = mac.Sum(u[:0])
			for k := range t {
				t[k] ^= u[k]
			}
		}
		out = append(out, t...)
	}
	return out[:keyLen]
}

func (s *Store) persist() error {
	raw, err := Encode(s.users)
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "auth.persist", "write", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "auth.persist", "rename", err)
	}
	return nil
}

// Encode always writes the current (v3) format: every record carries a
// 16-byte RealmID (hosting.ID{} for a deployment-wide entry) plus the v2
// algorithm byte and Argon2id Memory/Threads fields (zero for a legacy
// PBKDF2 record). Decode still reads v1 and v2 files (see decodeV1/decodeV2).
func Encode(users map[userKey]record) ([]byte, error) {
	if len(users) > maxUsers {
		return nil, nerr.New(nerr.InvalidArgument, "auth.Encode", "too many users")
	}
	n := 4 + 2 + 4
	keys := make([]userKey, 0, len(users))
	for key, rec := range users {
		if err := validateRecord(rec); err != nil {
			return nil, err
		}
		n += len(key.Realm) + 2 + len(key.Name) + 1 + saltSize + 4 + 4 + 1 + hashSize
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].Realm != keys[j].Realm {
			return string(keys[i].Realm[:]) < string(keys[j].Realm[:])
		}
		return keys[i].Name < keys[j].Name
	})
	buf := make([]byte, n)
	copy(buf[0:4], fileMagic)
	encoding.PutU16(buf, 4, fileVersion)
	encoding.PutU32(buf, 6, uint32(len(users)))
	off := 10
	for _, key := range keys {
		rec := users[key]
		if len(key.Name) > maxUserLen {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Encode", "user name too long")
		}
		copy(buf[off:], key.Realm[:])
		off += len(key.Realm)
		encoding.PutU16(buf, off, uint16(len(key.Name)))
		off += 2
		copy(buf[off:], key.Name)
		off += len(key.Name)
		buf[off] = rec.Algo
		off++
		copy(buf[off:], rec.Salt[:])
		off += saltSize
		encoding.PutU32(buf, off, rec.Iter)
		off += 4
		encoding.PutU32(buf, off, rec.Memory)
		off += 4
		buf[off] = rec.Threads
		off++
		copy(buf[off:], rec.Hash[:])
		off += hashSize
	}
	return buf[:off], nil
}

// Decode parses an auth file. Malformed input returns a controlled error.
// The legacy v1 (PBKDF2-only) and v2 (adds Algo/Memory/Threads) formats
// decode with every record implicitly deployment-wide (userKey.Realm ==
// hosting.ID{}); the current v3 format carries an explicit RealmID per
// record. Encode always writes v3, so any store that persists after this
// change upgrades its file format.
func Decode(raw []byte) (map[userKey]record, error) {
	if len(raw) < 10 {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated auth file")
	}
	if string(raw[0:4]) != fileMagic {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "bad auth magic")
	}
	count := encoding.U32(raw, 6)
	if count > maxUsers {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "user count exceeds limit")
	}
	switch encoding.U16(raw, 4) {
	case fileVersionV1:
		return decodeV1(raw, count)
	case fileVersionV2:
		return decodeV2(raw, count)
	case fileVersion:
		return decodeV3(raw, count)
	default:
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "unsupported auth version")
	}
}

func decodeV1(raw []byte, count uint32) (map[userKey]record, error) {
	users := make(map[userKey]record, count)
	off := 10
	for i := uint32(0); i < count; i++ {
		nl, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated user name")
		}
		if int(nl) > maxUserLen {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "user name too long")
		}
		nameb, err := encoding.ReadBytes(raw, off+2, int(nl))
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated user name")
		}
		off += 2 + int(nl)
		salt, err := encoding.ReadBytes(raw, off, saltSize)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated salt")
		}
		off += saltSize
		iter, err := encoding.ReadU32(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated iteration count")
		}
		off += 4
		sum, err := encoding.ReadBytes(raw, off, hashSize)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated hash")
		}
		off += hashSize
		name := normalize(string(nameb))
		if name == "" {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "empty user name")
		}
		key := userKey{Name: name}
		if _, dup := users[key]; dup {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "duplicate user")
		}
		var rec record
		rec.Algo = algoPBKDF2
		copy(rec.Salt[:], salt)
		rec.Iter = iter
		copy(rec.Hash[:], sum)
		if err := validateRecord(rec); err != nil {
			return nil, err
		}
		users[key] = rec
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "trailing auth bytes")
	}
	return users, nil
}

func decodeV2(raw []byte, count uint32) (map[userKey]record, error) {
	users := make(map[userKey]record, count)
	off := 10
	for i := uint32(0); i < count; i++ {
		nl, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated user name")
		}
		if int(nl) > maxUserLen {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "user name too long")
		}
		nameb, err := encoding.ReadBytes(raw, off+2, int(nl))
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated user name")
		}
		off += 2 + int(nl)
		algoB, err := encoding.ReadBytes(raw, off, 1)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated algorithm")
		}
		off++
		salt, err := encoding.ReadBytes(raw, off, saltSize)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated salt")
		}
		off += saltSize
		iter, err := encoding.ReadU32(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated iteration count")
		}
		off += 4
		memory, err := encoding.ReadU32(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated memory cost")
		}
		off += 4
		threadsB, err := encoding.ReadBytes(raw, off, 1)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated thread count")
		}
		off++
		sum, err := encoding.ReadBytes(raw, off, hashSize)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated hash")
		}
		off += hashSize
		name := normalize(string(nameb))
		if name == "" {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "empty user name")
		}
		key := userKey{Name: name}
		if _, dup := users[key]; dup {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "duplicate user")
		}
		algo := algoB[0]
		if algo != algoPBKDF2 && algo != algoArgon2id {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "unsupported password hash algorithm")
		}
		var rec record
		rec.Algo = algo
		copy(rec.Salt[:], salt)
		rec.Iter = iter
		rec.Memory = memory
		rec.Threads = threadsB[0]
		copy(rec.Hash[:], sum)
		if err := validateRecord(rec); err != nil {
			return nil, err
		}
		users[key] = rec
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "trailing auth bytes")
	}
	return users, nil
}

func decodeV3(raw []byte, count uint32) (map[userKey]record, error) {
	users := make(map[userKey]record, count)
	off := 10
	for i := uint32(0); i < count; i++ {
		realmB, err := encoding.ReadBytes(raw, off, 16)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated realm")
		}
		off += 16
		nl, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated user name")
		}
		if int(nl) > maxUserLen {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "user name too long")
		}
		nameb, err := encoding.ReadBytes(raw, off+2, int(nl))
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated user name")
		}
		off += 2 + int(nl)
		algoB, err := encoding.ReadBytes(raw, off, 1)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated algorithm")
		}
		off++
		salt, err := encoding.ReadBytes(raw, off, saltSize)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated salt")
		}
		off += saltSize
		iter, err := encoding.ReadU32(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated iteration count")
		}
		off += 4
		memory, err := encoding.ReadU32(raw, off)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated memory cost")
		}
		off += 4
		threadsB, err := encoding.ReadBytes(raw, off, 1)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated thread count")
		}
		off++
		sum, err := encoding.ReadBytes(raw, off, hashSize)
		if err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated hash")
		}
		off += hashSize
		name := normalize(string(nameb))
		if name == "" {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "empty user name")
		}
		var realm hosting.ID
		copy(realm[:], realmB)
		key := userKey{Realm: realm, Name: name}
		if _, dup := users[key]; dup {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "duplicate user")
		}
		algo := algoB[0]
		if algo != algoPBKDF2 && algo != algoArgon2id {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "unsupported password hash algorithm")
		}
		var rec record
		rec.Algo = algo
		copy(rec.Salt[:], salt)
		rec.Iter = iter
		rec.Memory = memory
		rec.Threads = threadsB[0]
		copy(rec.Hash[:], sum)
		if err := validateRecord(rec); err != nil {
			return nil, err
		}
		users[key] = rec
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "trailing auth bytes")
	}
	return users, nil
}
