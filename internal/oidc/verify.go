package oidc

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	// DefaultSkew is the allowed clock skew when checking exp / iat / nbf.
	DefaultSkew = 120 * time.Second
	// MaxSkew is the hard ceiling on the configurable skew.
	MaxSkew = 300 * time.Second
)

// VerifiedToken is the trusted content of a validated ID or access token.
type VerifiedToken struct {
	Issuer   string
	Subject  string
	Audience []string
	IssuedAt time.Time
	Expiry   time.Time
	Nonce    string
	JTI      string
	// Claims is the full decoded claim set (numbers as json.Number) for the
	// identity policy to map. It is trusted only after Verify returns nil.
	Claims map[string]any
}

// replayKey is the identity used to reject a second exchange of the same token.
func (t VerifiedToken) replayKey() string {
	if t.JTI != "" {
		return "jti:" + t.JTI
	}
	return "sub:" + t.Subject + "|iat:" + strconv.FormatInt(t.IssuedAt.Unix(), 10)
}

// IDTokenConfig configures verification for one IdP profile.
type IDTokenConfig struct {
	Issuer      string   // must equal the token iss exactly
	ClientID    string   // must appear in aud; azp, if present, must equal it
	AllowedAlgs []string // asymmetric algs only; empty -> DefaultAllowedAlgs
	Skew        time.Duration
	Now         func() time.Time
}

// IDTokenVerifier validates compact JWT ID tokens for one issuer against a
// JWKS cache and the profile's expectations. It never trusts a token
// algorithm, issuer, audience, or expiry that it did not check.
type IDTokenVerifier struct {
	cfg     IDTokenConfig
	jwks    *JWKSCache
	allowed map[string]struct{}
	skew    time.Duration
	now     func() time.Time
}

// AccessTokenConfig configures JWT access-token verification for an OAuth2
// client-credentials profile. Audience is the protected-resource identifier,
// while ClientID binds the token to the configured confidential client through
// its client_id or azp claim.
type AccessTokenConfig struct {
	Issuer      string
	ClientID    string
	Audience    string
	AllowedAlgs []string
	Skew        time.Duration
	Now         func() time.Time
}

// AccessTokenVerifier validates JWT access tokens for one confidential client.
// Opaque access tokens require RFC 7662 introspection and are not accepted by
// this verifier.
type AccessTokenVerifier struct {
	base     *IDTokenVerifier
	audience string
}

// NewIDTokenVerifier builds a verifier. It rejects a config that names a
// non-asymmetric algorithm — that is a configuration error, not a token to
// deny at runtime.
func NewIDTokenVerifier(cfg IDTokenConfig, jwks *JWKSCache) (*IDTokenVerifier, error) {
	if jwks == nil {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.NewIDTokenVerifier", "a JWKS cache is required")
	}
	if strings.TrimSpace(cfg.Issuer) == "" || strings.TrimSpace(cfg.ClientID) == "" {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.NewIDTokenVerifier", "issuer and client id are required")
	}
	algs := cfg.AllowedAlgs
	if len(algs) == 0 {
		algs = DefaultAllowedAlgs
	}
	allowed := make(map[string]struct{}, len(algs))
	for _, a := range algs {
		a = strings.TrimSpace(a)
		if !AlgIsAsymmetric(a) {
			return nil, nerr.New(nerr.InvalidArgument, "oidc.NewIDTokenVerifier", "allowed algorithm must be an asymmetric signature algorithm")
		}
		allowed[a] = struct{}{}
	}
	skew := cfg.Skew
	if skew <= 0 {
		skew = DefaultSkew
	}
	if skew > MaxSkew {
		skew = MaxSkew
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &IDTokenVerifier{cfg: cfg, jwks: jwks, allowed: allowed, skew: skew, now: now}, nil
}

// NewAccessTokenVerifier builds a verifier for asymmetric JWT access tokens.
func NewAccessTokenVerifier(cfg AccessTokenConfig, jwks *JWKSCache) (*AccessTokenVerifier, error) {
	if strings.TrimSpace(cfg.Audience) == "" {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.NewAccessTokenVerifier", "access-token audience is required")
	}
	base, err := NewIDTokenVerifier(IDTokenConfig{
		Issuer: cfg.Issuer, ClientID: cfg.ClientID, AllowedAlgs: cfg.AllowedAlgs,
		Skew: cfg.Skew, Now: cfg.Now,
	}, jwks)
	if err != nil {
		return nil, err
	}
	return &AccessTokenVerifier{base: base, audience: cfg.Audience}, nil
}

// Verify validates rawToken. When expectedNonce is non-empty the token's nonce
// claim must equal it exactly. Every failure is a typed Unauthorized /
// InvalidFormat error with no attacker-useful detail.
func (v *IDTokenVerifier) Verify(ctx context.Context, rawToken, expectedNonce string) (*VerifiedToken, error) {
	return v.verify(ctx, rawToken, tokenExpectation{
		op: "oidc.IDTokenVerifier", audience: v.cfg.ClientID,
		expectedNonce: expectedNonce,
	})
}

// Verify validates a JWT access token. It requires the configured resource
// audience and an exact client_id or azp binding to the configured OAuth2
// client. A nonce is intentionally not part of the client-credentials flow.
func (v *AccessTokenVerifier) Verify(ctx context.Context, rawToken string) (*VerifiedToken, error) {
	return v.base.verify(ctx, rawToken, tokenExpectation{
		op: "oidc.AccessTokenVerifier", audience: v.audience,
		requiredParty: v.base.cfg.ClientID,
	})
}

type tokenExpectation struct {
	op            string
	audience      string
	expectedNonce string
	requiredParty string
}

func (v *IDTokenVerifier) verify(ctx context.Context, rawToken string, want tokenExpectation) (*VerifiedToken, error) {
	deny := func(msg string) (*VerifiedToken, error) {
		return nil, nerr.New(nerr.Unauthorized, want.op, msg)
	}

	p, err := parseCompact(rawToken)
	if err != nil {
		return nil, err
	}
	// Algorithm allow-list. "none" and every MAC alg are excluded because
	// AlgIsAsymmetric is false for them and the config constructor forbids
	// them from `allowed`.
	if !AlgIsAsymmetric(p.header.Alg) {
		return deny("token algorithm is not permitted")
	}
	if _, ok := v.allowed[p.header.Alg]; !ok {
		return deny("token algorithm is not permitted for this issuer")
	}

	var claims map[string]any
	if err := strictJSON(p.payload, &claims); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, want.op, "token payload is not a JSON object")
	}

	iss, _ := claims["iss"].(string)
	if iss == "" || iss != v.cfg.Issuer {
		return deny("token issuer does not match this profile")
	}

	aud := audienceList(claims["aud"])
	if !contains(aud, want.audience) {
		return deny("token audience does not include the configured audience")
	}
	clientID, _ := claims["client_id"].(string)
	azp, _ := claims["azp"].(string)
	if want.requiredParty != "" {
		if (clientID != "" && clientID != want.requiredParty) || (azp != "" && azp != want.requiredParty) {
			return deny("access token is bound to a different client")
		}
		if clientID != want.requiredParty && azp != want.requiredParty {
			return deny("access token has no binding to the configured client")
		}
	} else if azp != "" && azp != v.cfg.ClientID {
		return deny("token authorized party is not this client")
	}

	// Signature. Select the key by kid when the token carries one; otherwise
	// require a single-key JWKS (empty kid).
	key, err := v.jwks.Key(ctx, p.header.Kid)
	if err != nil {
		return nil, err
	}
	if key.Alg != "" && key.Alg != p.header.Alg {
		return deny("token algorithm does not match the JWKS key")
	}
	if err := verifySignature(p, key.Key); err != nil {
		return nil, err
	}

	now := v.now()
	exp, ok := claimTime(claims, "exp")
	if !ok {
		return deny("token has no expiry")
	}
	if !now.Add(-v.skew).Before(exp) {
		return deny("token has expired")
	}
	if iat, ok := claimTime(claims, "iat"); ok && iat.After(now.Add(v.skew)) {
		return deny("token was issued in the future")
	}
	if nbf, ok := claimTime(claims, "nbf"); ok && nbf.After(now.Add(v.skew)) {
		return deny("token is not yet valid")
	}

	nonce, _ := claims["nonce"].(string)
	if want.expectedNonce != "" && nonce != want.expectedNonce {
		return deny("token nonce does not match the request")
	}

	sub, _ := claims["sub"].(string)
	if strings.TrimSpace(sub) == "" {
		return deny("token has no subject")
	}
	jti, _ := claims["jti"].(string)

	vt := &VerifiedToken{
		Issuer:   iss,
		Subject:  sub,
		Audience: aud,
		Expiry:   exp,
		Nonce:    nonce,
		JTI:      jti,
		Claims:   claims,
	}
	if iat, ok := claimTime(claims, "iat"); ok {
		vt.IssuedAt = iat
	}
	return vt, nil
}

// --- claim helpers ---

func audienceList(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		sort.Strings(out)
		return out
	default:
		return nil
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// claimTime reads a NumericDate claim (seconds since the epoch, per RFC 7519).
func claimTime(claims map[string]any, name string) (time.Time, bool) {
	v, ok := claims[name]
	if !ok {
		return time.Time{}, false
	}
	switch t := v.(type) {
	case json.Number:
		if f, err := t.Float64(); err == nil {
			return time.Unix(int64(f), 0).UTC(), true
		}
	case float64:
		return time.Unix(int64(t), 0).UTC(), true
	}
	return time.Time{}, false
}

// ReplayGuard rejects a token that has already been exchanged, until that
// token's own expiry. It is safe for concurrent use and bounded by pruning
// expired entries on every call.
type ReplayGuard struct {
	mu    sync.Mutex
	seen  map[string]time.Time
	nowFn func() time.Time
	limit int
}

// NewReplayGuard builds a guard.
func NewReplayGuard(now func() time.Time) *ReplayGuard {
	if now == nil {
		now = time.Now
	}
	return &ReplayGuard{seen: map[string]time.Time{}, nowFn: now, limit: 1 << 16}
}

// Observe records a verified token and returns an error if it was seen before.
func (g *ReplayGuard) Observe(t *VerifiedToken) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "oidc.ReplayGuard", "nil token")
	}
	key := t.replayKey()
	exp := t.Expiry

	g.mu.Lock()
	defer g.mu.Unlock()
	now := g.nowFn()
	for k, e := range g.seen {
		if !e.After(now) {
			delete(g.seen, k)
		}
	}
	if e, ok := g.seen[key]; ok && e.After(now) {
		return nerr.New(nerr.Unauthorized, "oidc.ReplayGuard", "token has already been exchanged")
	}
	if len(g.seen) >= g.limit {
		return nerr.New(nerr.Unavailable, "oidc.ReplayGuard", "replay cache is full")
	}
	g.seen[key] = exp
	return nil
}
