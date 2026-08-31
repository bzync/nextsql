package auth

import (
	"os"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	tokenRevMagic   = "NSTR"
	tokenRevVersion = 1
	maxRevokedIDs   = 65536
	maxRevCutoffs   = 4096
)

type revokedID struct {
	ID      [tokenIDSize]byte
	Expires time.Time // entry may be pruned once passed
}

type revCutoff struct {
	Principal string
	Before    time.Time // tokens for Principal issued at/before this are rejected
}

// TokenRevocations is the fail-closed revocation set for short-lived
// credentials: individual token ids (kept only until they would have expired
// anyway) and per-principal "issued at or before" cutoffs for bulk revocation.
type TokenRevocations struct {
	mu      sync.Mutex
	path    string
	ids     map[[tokenIDSize]byte]time.Time
	cutoffs map[string]time.Time
}

// CreateTokenRevocations writes a new empty revocation set.
func CreateTokenRevocations(path string) (*TokenRevocations, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "auth.CreateTokenRevocations", "revocation file exists")
	}
	r := &TokenRevocations{path: path, ids: map[[tokenIDSize]byte]time.Time{}, cutoffs: map[string]time.Time{}}
	if err := r.persistLocked(); err != nil {
		return nil, err
	}
	return r, nil
}

// OpenTokenRevocations loads a revocation set.
func OpenTokenRevocations(path string) (*TokenRevocations, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "auth.OpenTokenRevocations", "read", err)
	}
	ids, cutoffs, err := decodeRevocations(raw)
	if err != nil {
		return nil, err
	}
	return &TokenRevocations{path: path, ids: ids, cutoffs: cutoffs}, nil
}

// OpenOrCreateTokenRevocations opens an existing revocation set or creates one.
func OpenOrCreateTokenRevocations(path string) (*TokenRevocations, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CreateTokenRevocations(path)
	}
	return OpenTokenRevocations(path)
}

func (r *TokenRevocations) Path() string { return r.path }

// Revoke rejects a single credential by token id until its expiry passes.
func (r *TokenRevocations) Revoke(id [tokenIDSize]byte, expires time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pruneLocked(time.Now())
	if _, ok := r.ids[id]; !ok && len(r.ids) >= maxRevokedIDs {
		return nerr.New(nerr.Exhausted, "auth.TokenRevocations.Revoke", "revocation list is full")
	}
	if expires.IsZero() {
		expires = time.Now().Add(maxTokenLifetimeCeiling)
	}
	r.ids[id] = expires.UTC()
	return r.persistLocked()
}

// RevokePrincipal rejects every credential for principal issued at or before
// the cutoff. A later cutoff replaces an earlier one.
func (r *TokenRevocations) RevokePrincipal(principal string, before time.Time) error {
	name := normToken(principal)
	if name == "" || len(name) > maxTokenPrincipal {
		return nerr.New(nerr.InvalidArgument, "auth.TokenRevocations.RevokePrincipal", "invalid principal")
	}
	if before.IsZero() {
		before = time.Now()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cutoffs[name]; !ok && len(r.cutoffs) >= maxRevCutoffs {
		return nerr.New(nerr.Exhausted, "auth.TokenRevocations.RevokePrincipal", "cutoff list is full")
	}
	if cur, ok := r.cutoffs[name]; !ok || before.After(cur) {
		r.cutoffs[name] = before.UTC()
	}
	return r.persistLocked()
}

// IsRevoked reports whether claims have been revoked.
func (r *TokenRevocations) IsRevoked(claims *TokenClaims) bool {
	if r == nil || claims == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if exp, ok := r.ids[claims.TokenID]; ok && time.Now().Before(exp) {
		return true
	}
	if cut, ok := r.cutoffs[normToken(claims.Principal)]; ok {
		if !claims.IssuedAt.After(cut) {
			return true
		}
	}
	return false
}

// Prune drops entries that can no longer match any live credential.
func (r *TokenRevocations) Prune() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	before := len(r.ids)
	r.pruneLocked(time.Now())
	if len(r.ids) == before {
		return nil
	}
	return r.persistLocked()
}

func (r *TokenRevocations) pruneLocked(now time.Time) {
	for id, exp := range r.ids {
		if !now.Before(exp) {
			delete(r.ids, id)
		}
	}
}

// Reload re-reads the file. On any error the in-memory set is left unchanged.
func (r *TokenRevocations) Reload() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.path == "" {
		return nerr.New(nerr.InvalidArgument, "auth.TokenRevocations.Reload", "not attached to a file")
	}
	raw, err := os.ReadFile(r.path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "auth.TokenRevocations.Reload", "read", err)
	}
	ids, cutoffs, err := decodeRevocations(raw)
	if err != nil {
		return err
	}
	r.ids, r.cutoffs = ids, cutoffs
	return nil
}

// Counts returns the number of revoked ids and principal cutoffs.
func (r *TokenRevocations) Counts() (ids, cutoffs int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.ids), len(r.cutoffs)
}

func (r *TokenRevocations) persistLocked() error {
	if r.path == "" {
		return nil
	}
	raw, err := encodeRevocations(r.ids, r.cutoffs)
	if err != nil {
		return err
	}
	return atomicWrite(r.path, raw)
}

func encodeRevocations(ids map[[tokenIDSize]byte]time.Time, cutoffs map[string]time.Time) ([]byte, error) {
	if len(ids) > maxRevokedIDs || len(cutoffs) > maxRevCutoffs {
		return nil, nerr.New(nerr.InvalidArgument, "auth.encodeRevocations", "revocation set exceeds limit")
	}
	n := 4 + 2 + 4 + 4
	n += len(ids) * (tokenIDSize + 8)
	for name := range cutoffs {
		n += 2 + len(name) + 8
	}
	buf := make([]byte, n)
	copy(buf[0:4], tokenRevMagic)
	encoding.PutU16(buf, 4, tokenRevVersion)
	encoding.PutU32(buf, 6, uint32(len(ids)))
	off := 10
	for id, exp := range ids {
		copy(buf[off:], id[:])
		off += tokenIDSize
		encoding.PutU64(buf, off, uint64(exp.Unix()))
		off += 8
	}
	encoding.PutU32(buf, off, uint32(len(cutoffs)))
	off += 4
	for name, before := range cutoffs {
		if len(name) > maxTokenPrincipal {
			return nil, nerr.New(nerr.InvalidFormat, "auth.encodeRevocations", "principal too long")
		}
		encoding.PutU16(buf, off, uint16(len(name)))
		off += 2
		copy(buf[off:], name)
		off += len(name)
		encoding.PutU64(buf, off, uint64(before.Unix()))
		off += 8
	}
	return buf[:off], nil
}

func decodeRevocations(raw []byte) (map[[tokenIDSize]byte]time.Time, map[string]time.Time, error) {
	bad := func(msg string) (map[[tokenIDSize]byte]time.Time, map[string]time.Time, error) {
		return nil, nil, nerr.New(nerr.InvalidFormat, "auth.decodeRevocations", msg)
	}
	if len(raw) < 10 {
		return bad("truncated revocation file")
	}
	if string(raw[0:4]) != tokenRevMagic {
		return bad("bad revocation magic")
	}
	if encoding.U16(raw, 4) != tokenRevVersion {
		return bad("unsupported revocation version")
	}
	nIDs := encoding.U32(raw, 6)
	if nIDs > maxRevokedIDs {
		return bad("revoked id count exceeds limit")
	}
	ids := make(map[[tokenIDSize]byte]time.Time, nIDs)
	off := 10
	for i := uint32(0); i < nIDs; i++ {
		b, err := encoding.ReadBytes(raw, off, tokenIDSize)
		if err != nil {
			return bad("truncated revoked id")
		}
		off += tokenIDSize
		exp, err := encoding.ReadU64(raw, off)
		if err != nil {
			return bad("truncated revoked id expiry")
		}
		off += 8
		var id [tokenIDSize]byte
		copy(id[:], b)
		ids[id] = time.Unix(int64(exp), 0).UTC()
	}
	nCut, err := encoding.ReadU32(raw, off)
	if err != nil {
		return bad("truncated cutoff count")
	}
	off += 4
	if nCut > maxRevCutoffs {
		return bad("cutoff count exceeds limit")
	}
	cutoffs := make(map[string]time.Time, nCut)
	for i := uint32(0); i < nCut; i++ {
		nl, err := encoding.ReadU16(raw, off)
		if err != nil || int(nl) > maxTokenPrincipal {
			return bad("truncated cutoff principal")
		}
		nameb, err := encoding.ReadBytes(raw, off+2, int(nl))
		if err != nil {
			return bad("truncated cutoff principal")
		}
		off += 2 + int(nl)
		before, err := encoding.ReadU64(raw, off)
		if err != nil {
			return bad("truncated cutoff time")
		}
		off += 8
		name := normToken(string(nameb))
		if name == "" {
			return bad("empty cutoff principal")
		}
		cutoffs[name] = time.Unix(int64(before), 0).UTC()
	}
	if off != len(raw) {
		return bad("trailing revocation bytes")
	}
	return ids, cutoffs, nil
}
