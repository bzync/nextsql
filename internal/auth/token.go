// Signed short-lived NextSQL credentials.
//
// A short-lived credential is an Ed25519-signed set of claims that a client
// presents in place of a password: same wire path, same native principal, same
// RBAC. It is bounded by an explicit expiry, may be narrowed to an audience,
// database, realm, and a subset of the principal's roles, and can be revoked
// before expiry either individually (by token id) or in bulk (per principal,
// issued at or before a cutoff).
//
// Wire form: "NSSC1." followed by base64url (no padding) of
//
//	claims-region || ed25519-signature(64)
//
// The signature covers exactly the claims region. Verification never allocates
// from an unchecked length and rejects anything malformed with a typed error.
package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	tokenPrefix     = "NSSC1."
	tokenMagic      = "NSSC"
	tokenVersion    = 1
	tokenSigSize    = ed25519.SignatureSize // 64
	tokenIDSize     = 16
	tokenHeaderSize = 4 + 2 + 4 + tokenIDSize + 8 + 8 + 8 // magic..expiresAt

	maxTokenPrincipal = 128
	maxTokenAudience  = 128
	maxTokenScope     = 128 // database, realm
	maxTokenRoles     = 16
	maxTokenRoleLen   = 128
	maxTokenBlob      = 4096

	// DefaultMaxTokenLifetime bounds how far a credential's expiry may sit
	// past its issued-at. A verifier may lower it but never raise it past the
	// hard ceiling.
	DefaultMaxTokenLifetime = 24 * time.Hour
	maxTokenLifetimeCeiling = 30 * 24 * time.Hour
	defaultTokenSkew        = 60 * time.Second
)

// TokenClaims is the verified content of a short-lived credential.
type TokenClaims struct {
	KeyID     uint32
	TokenID   [tokenIDSize]byte
	IssuedAt  time.Time
	NotBefore time.Time
	ExpiresAt time.Time
	Principal string
	Audience  string // "" = unrestricted
	Database  string // "" = any database the principal may reach
	Realm     string // "" = any realm
	Roles     []string
}

// LooksLikeToken reports whether s is shaped like a short-lived credential so
// the auth path can route it to token verification instead of password
// verification. It does not validate the signature.
func LooksLikeToken(s string) bool {
	return strings.HasPrefix(s, tokenPrefix)
}

func normToken(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// encodeTokenClaims serializes the signed region (everything but the signature).
func encodeTokenClaims(c *TokenClaims) ([]byte, error) {
	principal := normToken(c.Principal)
	audience := normToken(c.Audience)
	database := normToken(c.Database)
	realm := normToken(c.Realm)
	if principal == "" || len(principal) > maxTokenPrincipal {
		return nil, nerr.New(nerr.InvalidArgument, "auth.token", "invalid principal")
	}
	if len(audience) > maxTokenAudience || len(database) > maxTokenScope || len(realm) > maxTokenScope {
		return nil, nerr.New(nerr.InvalidArgument, "auth.token", "scope value too long")
	}
	roles, err := normTokenRoles(c.Roles)
	if err != nil {
		return nil, err
	}
	n := tokenHeaderSize
	n += 2 + len(principal) + 2 + len(audience) + 2 + len(database) + 2 + len(realm) + 2
	for _, r := range roles {
		n += 2 + len(r)
	}
	buf := make([]byte, n)
	copy(buf[0:4], tokenMagic)
	encoding.PutU16(buf, 4, tokenVersion)
	encoding.PutU32(buf, 6, c.KeyID)
	copy(buf[10:10+tokenIDSize], c.TokenID[:])
	off := 10 + tokenIDSize
	encoding.PutU64(buf, off, uint64(c.IssuedAt.Unix()))
	encoding.PutU64(buf, off+8, uint64(c.NotBefore.Unix()))
	encoding.PutU64(buf, off+16, uint64(c.ExpiresAt.Unix()))
	off += 24
	off = putTokenStr(buf, off, principal)
	off = putTokenStr(buf, off, audience)
	off = putTokenStr(buf, off, database)
	off = putTokenStr(buf, off, realm)
	encoding.PutU16(buf, off, uint16(len(roles)))
	off += 2
	for _, r := range roles {
		off = putTokenStr(buf, off, r)
	}
	return buf[:off], nil
}

func putTokenStr(buf []byte, off int, s string) int {
	encoding.PutU16(buf, off, uint16(len(s)))
	off += 2
	copy(buf[off:], s)
	return off + len(s)
}

func normTokenRoles(in []string) ([]string, error) {
	if len(in) > maxTokenRoles {
		return nil, nerr.New(nerr.InvalidArgument, "auth.token", "too many roles")
	}
	out := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	for _, r := range in {
		r = normToken(r)
		if r == "" || len(r) > maxTokenRoleLen {
			return nil, nerr.New(nerr.InvalidArgument, "auth.token", "invalid role name")
		}
		if _, dup := seen[r]; dup {
			continue
		}
		seen[r] = struct{}{}
		out = append(out, r)
	}
	return out, nil
}

// decodeTokenClaims parses the signed region. It never reads past raw.
func decodeTokenClaims(raw []byte) (*TokenClaims, error) {
	bad := func(msg string) (*TokenClaims, error) {
		return nil, nerr.New(nerr.InvalidFormat, "auth.token", msg)
	}
	if len(raw) < tokenHeaderSize {
		return bad("truncated credential")
	}
	if string(raw[0:4]) != tokenMagic {
		return bad("bad credential magic")
	}
	if encoding.U16(raw, 4) != tokenVersion {
		return bad("unsupported credential version")
	}
	c := &TokenClaims{KeyID: encoding.U32(raw, 6)}
	copy(c.TokenID[:], raw[10:10+tokenIDSize])
	off := 10 + tokenIDSize
	c.IssuedAt = time.Unix(int64(encoding.U64(raw, off)), 0).UTC()
	c.NotBefore = time.Unix(int64(encoding.U64(raw, off+8)), 0).UTC()
	c.ExpiresAt = time.Unix(int64(encoding.U64(raw, off+16)), 0).UTC()
	off += 24
	var err error
	if c.Principal, off, err = readTokenStr(raw, off, maxTokenPrincipal); err != nil {
		return bad("truncated principal")
	}
	if c.Audience, off, err = readTokenStr(raw, off, maxTokenAudience); err != nil {
		return bad("truncated audience")
	}
	if c.Database, off, err = readTokenStr(raw, off, maxTokenScope); err != nil {
		return bad("truncated database scope")
	}
	if c.Realm, off, err = readTokenStr(raw, off, maxTokenScope); err != nil {
		return bad("truncated realm scope")
	}
	nRoles, err := encoding.ReadU16(raw, off)
	if err != nil || int(nRoles) > maxTokenRoles {
		return bad("truncated role count")
	}
	off += 2
	c.Roles = make([]string, 0, nRoles)
	for i := 0; i < int(nRoles); i++ {
		var role string
		if role, off, err = readTokenStr(raw, off, maxTokenRoleLen); err != nil {
			return bad("truncated role")
		}
		c.Roles = append(c.Roles, role)
	}
	if off != len(raw) {
		return bad("trailing credential bytes")
	}
	if normToken(c.Principal) == "" {
		return bad("empty principal")
	}
	return c, nil
}

func readTokenStr(raw []byte, off, max int) (string, int, error) {
	n, err := encoding.ReadU16(raw, off)
	if err != nil || int(n) > max {
		return "", off, nerr.New(nerr.InvalidFormat, "auth.token", "bad string")
	}
	b, err := encoding.ReadBytes(raw, off+2, int(n))
	if err != nil {
		return "", off, nerr.New(nerr.InvalidFormat, "auth.token", "truncated string")
	}
	return string(b), off + 2 + int(n), nil
}

// encodeToken produces the "NSSC1." wire string from a signed region and its
// detached signature.
func encodeToken(signed, sig []byte) string {
	blob := make([]byte, 0, len(signed)+len(sig))
	blob = append(blob, signed...)
	blob = append(blob, sig...)
	return tokenPrefix + base64.RawURLEncoding.EncodeToString(blob)
}

// splitToken decodes the wire string into its signed region and signature.
func splitToken(token string) (signed, sig []byte, err error) {
	if !strings.HasPrefix(token, tokenPrefix) {
		return nil, nil, nerr.New(nerr.InvalidFormat, "auth.token", "not a short-lived credential")
	}
	body := token[len(tokenPrefix):]
	if len(body) == 0 || base64.RawURLEncoding.DecodedLen(len(body)) > maxTokenBlob {
		return nil, nil, nerr.New(nerr.InvalidFormat, "auth.token", "credential too large")
	}
	blob, derr := base64.RawURLEncoding.DecodeString(body)
	if derr != nil {
		return nil, nil, nerr.New(nerr.InvalidFormat, "auth.token", "credential is not valid base64url")
	}
	if len(blob) < tokenHeaderSize+tokenSigSize {
		return nil, nil, nerr.New(nerr.InvalidFormat, "auth.token", "truncated credential")
	}
	cut := len(blob) - tokenSigSize
	return blob[:cut], blob[cut:], nil
}

func newTokenID() ([tokenIDSize]byte, error) {
	var id [tokenIDSize]byte
	if _, err := rand.Read(id[:]); err != nil {
		return id, nerr.Wrap(nerr.Internal, "auth.token", "rand", err)
	}
	return id, nil
}
