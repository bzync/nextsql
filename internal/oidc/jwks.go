package oidc

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	maxJWKSBytes = 1 << 20 // 1 MiB
	maxJWKSKeys  = 64

	// DefaultJWKSSoftTTL is how long fetched keys are served without any
	// refresh attempt.
	DefaultJWKSSoftTTL = time.Hour
	// DefaultJWKSHardTTL is the outer bound: past it, keys are never served and
	// the exchange fails closed until a refresh succeeds.
	DefaultJWKSHardTTL = 24 * time.Hour
	// jwksRefreshInterval rate-limits refresh attempts (per cache / per issuer)
	// so an unknown-kid storm cannot hammer the IdP.
	jwksRefreshInterval = 5 * time.Minute
)

// jwk is one JSON Web Key. Only the fields the broker needs are decoded.
type jwk struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	Crv string `json:"crv"`
	N   string `json:"n"`
	E   string `json:"e"`
	X   string `json:"x"`
	Y   string `json:"y"`
}

// PublicKey is a verification key selected from a JWKS document.
type PublicKey struct {
	Kid string
	Alg string // "" when the JWKS did not pin one
	Key crypto.PublicKey
}

// ParseJWKS decodes a JWKS document into verification keys. It rejects an
// oversized document, an unparseable key, an unusable key type, and a document
// with no usable keys.
func ParseJWKS(raw []byte) ([]PublicKey, error) {
	bad := func(msg string) ([]PublicKey, error) {
		return nil, nerr.New(nerr.InvalidFormat, "oidc.ParseJWKS", msg)
	}
	if len(raw) == 0 || len(raw) > maxJWKSBytes {
		return bad("JWKS document length out of range")
	}
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := strictJSON(raw, &doc); err != nil {
		return bad("JWKS is not valid JSON")
	}
	if len(doc.Keys) == 0 || len(doc.Keys) > maxJWKSKeys {
		return bad("JWKS has no keys or too many keys")
	}
	out := make([]PublicKey, 0, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue // encryption key, not our concern
		}
		if k.Alg != "" && !AlgIsAsymmetric(k.Alg) {
			continue // MAC / none / unknown: never usable for verification
		}
		pub, err := k.publicKey()
		if err != nil {
			return nil, err
		}
		out = append(out, PublicKey{Kid: k.Kid, Alg: k.Alg, Key: pub})
	}
	if len(out) == 0 {
		return bad("JWKS has no usable signature keys")
	}
	return out, nil
}

func (k jwk) publicKey() (crypto.PublicKey, error) {
	bad := func(msg string) (crypto.PublicKey, error) {
		return nil, nerr.New(nerr.InvalidFormat, "oidc.ParseJWKS", msg)
	}
	switch k.Kty {
	case "RSA":
		nBytes, err := b64urlField(k.N)
		if err != nil || len(nBytes) == 0 || len(nBytes) > 1024 {
			return bad("RSA modulus is invalid")
		}
		eBytes, err := b64urlField(k.E)
		if err != nil || len(eBytes) == 0 || len(eBytes) > 8 {
			return bad("RSA exponent is invalid")
		}
		n := new(big.Int).SetBytes(nBytes)
		e := new(big.Int).SetBytes(eBytes)
		if !e.IsInt64() || e.Int64() < 2 {
			return bad("RSA exponent is out of range")
		}
		if n.BitLen() < 2048 {
			return bad("RSA key is smaller than 2048 bits")
		}
		return &rsa.PublicKey{N: n, E: int(e.Int64())}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return bad("unsupported EC curve")
		}
		xB, err := b64urlField(k.X)
		if err != nil {
			return bad("EC x coordinate is invalid")
		}
		yB, err := b64urlField(k.Y)
		if err != nil {
			return bad("EC y coordinate is invalid")
		}
		size := (curve.Params().BitSize + 7) / 8
		if len(xB) > size || len(yB) > size || len(xB) == 0 || len(yB) == 0 {
			return bad("EC coordinate length is invalid")
		}
		x := new(big.Int).SetBytes(xB)
		y := new(big.Int).SetBytes(yB)
		if !curve.IsOnCurve(x, y) {
			return bad("EC point is not on the curve")
		}
		return &ecdsa.PublicKey{Curve: curve, X: x, Y: y}, nil
	default:
		return bad("unsupported key type")
	}
}

func b64urlField(s string) ([]byte, error) {
	if s == "" {
		return nil, nerr.New(nerr.InvalidFormat, "oidc.ParseJWKS", "empty field")
	}
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// Fetcher retrieves a document (a JWKS or an OIDC discovery document) by URL.
// Tests inject a fake; production uses HTTPFetcher.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// HTTPFetcher fetches over HTTPS with a bounded body and timeout.
type HTTPFetcher struct {
	Client  *http.Client
	MaxBody int64
}

// NewHTTPFetcher builds a fetcher with a sane timeout and body cap.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{
		Client:  &http.Client{Timeout: 10 * time.Second},
		MaxBody: maxJWKSBytes,
	}
}

// Fetch performs a GET and returns the response body. Non-2xx is an error.
func (f *HTTPFetcher) Fetch(ctx context.Context, url string) ([]byte, error) {
	if !strings.HasPrefix(url, "https://") {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.HTTPFetcher", "only https URLs are allowed")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, "oidc.HTTPFetcher", "build request", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, nerr.Wrap(nerr.Unavailable, "oidc.HTTPFetcher", "get", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, nerr.New(nerr.Unavailable, "oidc.HTTPFetcher", "non-2xx response from identity provider")
	}
	max := f.MaxBody
	if max <= 0 {
		max = maxJWKSBytes
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, max+1))
	if err != nil {
		return nil, nerr.Wrap(nerr.Unavailable, "oidc.HTTPFetcher", "read body", err)
	}
	if int64(len(body)) > max {
		return nil, nerr.New(nerr.InvalidFormat, "oidc.HTTPFetcher", "response body exceeds the limit")
	}
	return body, nil
}

// discoveryDoc is the subset of an OIDC discovery document the broker needs.
type discoveryDoc struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

// JWKSCache serves verification keys for one issuer, refreshing from jwksURI
// with a soft and a hard TTL and a rate limit on refresh attempts. It is safe
// for concurrent use. A refresh runs under the cache lock, so concurrent
// callers that miss coalesce onto one fetch.
type JWKSCache struct {
	fetcher   Fetcher
	issuer    string
	jwksURI   string // explicit; when "" the cache discovers it from issuer
	soft      time.Duration
	hard      time.Duration
	nowFn     func() time.Time
	refreshEv time.Duration

	mu          sync.Mutex
	keys        map[string]PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
	haveFetched bool
}

// JWKSCacheConfig configures a JWKSCache.
type JWKSCacheConfig struct {
	Fetcher Fetcher
	Issuer  string
	JWKSURI string        // optional; discovered from Issuer when empty
	SoftTTL time.Duration // 0 -> DefaultJWKSSoftTTL
	HardTTL time.Duration // 0 -> DefaultJWKSHardTTL
	Now     func() time.Time
}

// NewJWKSCache builds a cache. It does not fetch until the first Key call.
func NewJWKSCache(cfg JWKSCacheConfig) (*JWKSCache, error) {
	if cfg.Fetcher == nil {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.NewJWKSCache", "a fetcher is required")
	}
	if strings.TrimSpace(cfg.Issuer) == "" {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.NewJWKSCache", "an issuer is required")
	}
	soft, hard := cfg.SoftTTL, cfg.HardTTL
	if soft <= 0 {
		soft = DefaultJWKSSoftTTL
	}
	if hard <= 0 {
		hard = DefaultJWKSHardTTL
	}
	if hard < soft {
		return nil, nerr.New(nerr.InvalidArgument, "oidc.NewJWKSCache", "hard TTL must be at least the soft TTL")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	return &JWKSCache{
		fetcher:   cfg.Fetcher,
		issuer:    cfg.Issuer,
		jwksURI:   strings.TrimSpace(cfg.JWKSURI),
		soft:      soft,
		hard:      hard,
		nowFn:     now,
		refreshEv: jwksRefreshInterval,
		keys:      map[string]PublicKey{},
	}, nil
}

// Key returns the verification key for kid. It serves a cached key while it is
// within the soft TTL. On an unknown kid or a soft-expired cache it attempts
// one rate-limited refresh; if that fails it still serves a key that is within
// the hard TTL (a brief JWKS outage does not break logins). Past the hard TTL,
// or when no key matches, it fails closed.
func (c *JWKSCache) Key(ctx context.Context, kid string) (PublicKey, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := c.nowFn()
	if k, ok := c.lookupLocked(kid); ok && c.haveFetched && now.Sub(c.fetchedAt) < c.soft {
		return k, nil
	}

	_, known := c.lookupLocked(kid)
	softExpired := !c.haveFetched || now.Sub(c.fetchedAt) >= c.soft
	if (!known || softExpired) && (!c.haveFetched || now.Sub(c.lastAttempt) >= c.refreshEv) {
		c.lastAttempt = now
		if fresh, err := c.fetchLocked(ctx); err == nil {
			c.keys = fresh
			c.fetchedAt = now
			c.haveFetched = true
		}
	}

	if k, ok := c.lookupLocked(kid); ok && c.haveFetched && c.nowFn().Sub(c.fetchedAt) < c.hard {
		return k, nil
	}
	if !c.haveFetched {
		return PublicKey{}, nerr.New(nerr.Unavailable, "oidc.JWKSCache", "identity provider key set is unavailable")
	}
	if c.nowFn().Sub(c.fetchedAt) >= c.hard {
		return PublicKey{}, nerr.New(nerr.Unavailable, "oidc.JWKSCache", "identity provider key set is stale beyond the hard limit")
	}
	return PublicKey{}, nerr.New(nerr.Unauthorized, "oidc.JWKSCache", "no key matches the token key id")
}

// lookupLocked resolves kid against the cached keys. An empty kid matches the
// sole key of a single-key JWKS (a common IdP shape).
func (c *JWKSCache) lookupLocked(kid string) (PublicKey, bool) {
	if k, ok := c.keys[kid]; ok {
		return k, true
	}
	if kid == "" && len(c.keys) == 1 {
		for _, k := range c.keys {
			return k, true
		}
	}
	return PublicKey{}, false
}

func (c *JWKSCache) fetchLocked(ctx context.Context) (map[string]PublicKey, error) {
	uri := c.jwksURI
	if uri == "" {
		disco, err := c.fetcher.Fetch(ctx, discoveryURL(c.issuer))
		if err != nil {
			return nil, err
		}
		var d discoveryDoc
		if err := strictJSON(disco, &d); err != nil {
			return nil, nerr.New(nerr.InvalidFormat, "oidc.JWKSCache", "discovery document is not valid JSON")
		}
		if d.Issuer != c.issuer {
			return nil, nerr.New(nerr.Unauthorized, "oidc.JWKSCache", "discovery issuer does not match the configured issuer")
		}
		if !strings.HasPrefix(d.JWKSURI, "https://") {
			return nil, nerr.New(nerr.InvalidFormat, "oidc.JWKSCache", "discovery jwks_uri is not https")
		}
		uri = d.JWKSURI
	}
	raw, err := c.fetcher.Fetch(ctx, uri)
	if err != nil {
		return nil, err
	}
	keys, err := ParseJWKS(raw)
	if err != nil {
		return nil, err
	}
	m := make(map[string]PublicKey, len(keys))
	for _, k := range keys {
		m[k.Kid] = k
	}
	return m, nil
}

func discoveryURL(issuer string) string {
	return strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
}
