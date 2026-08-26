// Package auth stores login credentials. Passwords are never persisted in plaintext.
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"os"
	"strings"
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	fileMagic   = "NSAU"
	fileVersion = 1
	saltSize    = 16
	hashSize    = 32
	defaultIter = 100_000
	maxUserLen  = 128
	maxPassLen  = 1024
	maxUsers    = 4096
)

type record struct {
	Salt [saltSize]byte
	Iter uint32
	Hash [hashSize]byte
}

// Store is a versioned on-disk user directory. Verify is constant-time on the hash.
type Store struct {
	mu    sync.Mutex
	path  string
	users map[string]record
}

func Create(path string) (*Store, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "auth.Create", "auth file exists")
	}
	s := &Store{path: path, users: make(map[string]record)}
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

func (s *Store) Has(user string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	_, ok := s.users[normalize(user)]
	return ok
}

func (s *Store) Delete(user string) error {
	name, err := validateUser(user)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[name]; !ok {
		return nerr.New(nerr.NotFound, "auth.Delete", "unknown user")
	}
	delete(s.users, name)
	return s.persist()
}

func (s *Store) Users() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.users))
	for name := range s.users {
		out = append(out, name)
	}
	return out
}

func (s *Store) Upsert(user, password string) error {
	name, err := validateUser(user)
	if err != nil {
		return err
	}
	if err := validatePassword(password); err != nil {
		return err
	}
	rec, err := hashPassword(password, defaultIter)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.users == nil {
		s.users = make(map[string]record)
	}
	if _, ok := s.users[name]; !ok && len(s.users) >= maxUsers {
		return nerr.New(nerr.InvalidArgument, "auth.Upsert", "too many users")
	}
	s.users[name] = rec
	return s.persist()
}

func (s *Store) Verify(user, password string) error {
	name, err := validateUser(user)
	if err != nil {
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	s.mu.Lock()
	rec, ok := s.users[name]
	s.mu.Unlock()
	if !ok {
		// Dummy compare so missing users are not faster than bad passwords.
		var dummy record
		dummy.Iter = defaultIter
		_ = checkPassword(password, dummy)
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	if err := checkPassword(password, rec); err != nil {
		return nerr.New(nerr.Unauthorized, "auth.Verify", "authentication failed")
	}
	return nil
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

func hashPassword(password string, iter uint32) (record, error) {
	var rec record
	rec.Iter = iter
	if _, err := rand.Read(rec.Salt[:]); err != nil {
		return record{}, nerr.Wrap(nerr.Internal, "auth.hashPassword", "rand", err)
	}
	sum := pbkdf2SHA256([]byte(password), rec.Salt[:], int(iter), hashSize)
	copy(rec.Hash[:], sum)
	return rec, nil
}

func checkPassword(password string, rec record) error {
	if rec.Iter < 1 || rec.Iter > 10_000_000 {
		return nerr.New(nerr.InvalidFormat, "auth.checkPassword", "invalid iteration count")
	}
	got := pbkdf2SHA256([]byte(password), rec.Salt[:], int(rec.Iter), hashSize)
	if subtle.ConstantTimeCompare(got, rec.Hash[:]) != 1 {
		return nerr.New(nerr.Unauthorized, "auth.checkPassword", "mismatch")
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

func Encode(users map[string]record) ([]byte, error) {
	if len(users) > maxUsers {
		return nil, nerr.New(nerr.InvalidArgument, "auth.Encode", "too many users")
	}
	n := 4 + 2 + 4
	for name := range users {
		n += 2 + len(name) + saltSize + 4 + hashSize
	}
	buf := make([]byte, n)
	copy(buf[0:4], fileMagic)
	encoding.PutU16(buf, 4, fileVersion)
	encoding.PutU32(buf, 6, uint32(len(users)))
	off := 10
	for name, rec := range users {
		if len(name) > maxUserLen {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Encode", "user name too long")
		}
		encoding.PutU16(buf, off, uint16(len(name)))
		off += 2
		copy(buf[off:], name)
		off += len(name)
		copy(buf[off:], rec.Salt[:])
		off += saltSize
		encoding.PutU32(buf, off, rec.Iter)
		off += 4
		copy(buf[off:], rec.Hash[:])
		off += hashSize
	}
	return buf[:off], nil
}

// Decode parses an auth file. Malformed input returns a controlled error.
func Decode(raw []byte) (map[string]record, error) {
	if len(raw) < 10 {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "truncated auth file")
	}
	if string(raw[0:4]) != fileMagic {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "bad auth magic")
	}
	if encoding.U16(raw, 4) != fileVersion {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "unsupported auth version")
	}
	count := encoding.U32(raw, 6)
	if count > maxUsers {
		return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "user count exceeds limit")
	}
	users := make(map[string]record, count)
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
		if _, dup := users[name]; dup {
			return nil, nerr.New(nerr.InvalidFormat, "auth.Decode", "duplicate user")
		}
		var rec record
		copy(rec.Salt[:], salt)
		rec.Iter = iter
		copy(rec.Hash[:], sum)
		users[name] = rec
	}
	return users, nil
}
