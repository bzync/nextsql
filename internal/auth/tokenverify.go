package auth

import (
	"crypto/ed25519"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// TokenMintRequest describes a credential to issue.
type TokenMintRequest struct {
	Principal string
	Audience  string
	Database  string
	Realm     string
	Roles     []string
	TTL       time.Duration
	NotBefore time.Time // zero = now
}

// Mint issues a signed short-lived credential from the keyset's current key.
// It returns the wire string, the token id (for later revocation), and the
// expiry.
func (ks *TokenKeyset) Mint(req TokenMintRequest, now time.Time) (token string, id [tokenIDSize]byte, expiresAt time.Time, err error) {
	if now.IsZero() {
		now = time.Now()
	}
	now = now.UTC()
	if req.TTL <= 0 || req.TTL > maxTokenLifetimeCeiling {
		return "", id, time.Time{}, nerr.New(nerr.InvalidArgument, "auth.TokenKeyset.Mint", "ttl must be within (0, 720h]")
	}
	priv, keyID, err := ks.signer()
	if err != nil {
		return "", id, time.Time{}, err
	}
	id, err = newTokenID()
	if err != nil {
		return "", id, time.Time{}, err
	}
	nbf := req.NotBefore
	if nbf.IsZero() {
		nbf = now
	}
	nbf = time.Unix(nbf.Unix(), 0).UTC()
	now = time.Unix(now.Unix(), 0).UTC()
	exp := time.Unix(now.Add(req.TTL).Unix(), 0).UTC()
	claims := &TokenClaims{
		KeyID:     keyID,
		TokenID:   id,
		IssuedAt:  now,
		NotBefore: nbf,
		ExpiresAt: exp,
		Principal: req.Principal,
		Audience:  req.Audience,
		Database:  req.Database,
		Realm:     req.Realm,
		Roles:     req.Roles,
	}
	signed, err := encodeTokenClaims(claims)
	if err != nil {
		return "", id, time.Time{}, err
	}
	sig := ed25519.Sign(priv, signed)
	return encodeToken(signed, sig), id, exp, nil
}

// TokenVerifier verifies short-lived credentials against a keyset and an
// optional revocation set. It enforces the signature, the validity window, a
// maximum lifetime, and (when configured) the audience. Database and realm
// scope are surfaced on the claims for the caller to enforce against the
// concrete deployment.
type TokenVerifier struct {
	mu          sync.Mutex
	keyset      *TokenKeyset
	revocations *TokenRevocations
	audience    string
	maxLifetime time.Duration
	skew        time.Duration
	now         func() time.Time
}

// NewTokenVerifier builds a verifier. audience "" accepts any audience;
// a non-empty audience requires the credential to carry exactly that value.
func NewTokenVerifier(keyset *TokenKeyset, revocations *TokenRevocations, audience string) *TokenVerifier {
	return &TokenVerifier{
		keyset:      keyset,
		revocations: revocations,
		audience:    normToken(audience),
		maxLifetime: DefaultMaxTokenLifetime,
		skew:        defaultTokenSkew,
		now:         time.Now,
	}
}

// SetMaxLifetime lowers the accepted credential lifetime. Values above the
// hard ceiling are clamped; non-positive values are ignored.
func (v *TokenVerifier) SetMaxLifetime(d time.Duration) {
	if d <= 0 {
		return
	}
	if d > maxTokenLifetimeCeiling {
		d = maxTokenLifetimeCeiling
	}
	v.mu.Lock()
	v.maxLifetime = d
	v.mu.Unlock()
}

// SetClock overrides the time source (tests only).
func (v *TokenVerifier) SetClock(fn func() time.Time) {
	v.mu.Lock()
	v.now = fn
	v.mu.Unlock()
}

// Audience returns the configured audience ("" = unrestricted).
func (v *TokenVerifier) Audience() string { return v.audience }

// Reload refreshes the keyset and revocation set from disk. A failure on
// either leaves both unchanged (last known good).
func (v *TokenVerifier) Reload() error {
	if v.keyset != nil {
		if err := v.keyset.Reload(); err != nil {
			return err
		}
	}
	if v.revocations != nil {
		if err := v.revocations.Reload(); err != nil {
			return err
		}
	}
	return nil
}

// Verify checks a credential and returns its claims. Every failure is a typed
// Unauthorized/InvalidFormat error with no detail that would help an attacker.
func (v *TokenVerifier) Verify(token string) (*TokenClaims, error) {
	if v == nil || v.keyset == nil {
		return nil, nerr.New(nerr.Unavailable, "auth.TokenVerifier", "short-lived credentials are not configured")
	}
	signed, sig, err := splitToken(token)
	if err != nil {
		return nil, err
	}
	claims, err := decodeTokenClaims(signed)
	if err != nil {
		return nil, err
	}
	pub, err := v.keyset.verifier(claims.KeyID)
	if err != nil {
		return nil, err
	}
	if !ed25519.Verify(pub, signed, sig) {
		return nil, nerr.New(nerr.Unauthorized, "auth.TokenVerifier", "credential signature is invalid")
	}

	v.mu.Lock()
	now := v.now().UTC()
	maxLife, skew := v.maxLifetime, v.skew
	v.mu.Unlock()

	if !claims.ExpiresAt.After(claims.IssuedAt) {
		return nil, nerr.New(nerr.InvalidFormat, "auth.TokenVerifier", "credential expiry precedes issuance")
	}
	if claims.ExpiresAt.Sub(claims.IssuedAt) > maxLife {
		return nil, nerr.New(nerr.Unauthorized, "auth.TokenVerifier", "credential lifetime exceeds the maximum")
	}
	if now.Add(skew).Before(claims.NotBefore) {
		return nil, nerr.New(nerr.Unauthorized, "auth.TokenVerifier", "credential is not yet valid")
	}
	if !now.Add(-skew).Before(claims.ExpiresAt) {
		return nil, nerr.New(nerr.Unauthorized, "auth.TokenVerifier", "credential has expired")
	}
	if v.audience != "" {
		if !strings.EqualFold(normToken(claims.Audience), v.audience) {
			return nil, nerr.New(nerr.Unauthorized, "auth.TokenVerifier", "credential audience does not match this deployment")
		}
	}
	if v.revocations != nil && v.revocations.IsRevoked(claims) {
		return nil, nerr.New(nerr.Unauthorized, "auth.TokenVerifier", "credential has been revoked")
	}
	return claims, nil
}
