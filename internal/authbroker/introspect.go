package authbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidc"
)

const (
	defaultIntrospectionTimeout  = 5 * time.Second
	defaultIntrospectionCacheTTL = 5 * time.Minute
	maxIntrospectionCacheSize    = 1024
	maxClientSecretBytes         = 64 << 10
)

// ReadClientSecretFile reads a confidential client secret from a regular mode-0600 file.
func ReadClientSecretFile(path string) (string, error) {
	const op = "authbroker.ReadClientSecretFile"
	if strings.TrimSpace(path) == "" {
		return "", nerr.New(nerr.InvalidArgument, op, "client secret path is required")
	}
	before, err := os.Lstat(path)
	if err != nil {
		return "", nerr.Wrap(nerr.IO, op, "inspect client secret file", err)
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return "", nerr.New(nerr.Forbidden, op, "client secret path must be a regular file")
	}
	if runtime.GOOS != "windows" && before.Mode().Perm()&0o077 != 0 {
		return "", nerr.New(nerr.Forbidden, op, "client secret file permissions are too broad; require mode 0600")
	}
	f, err := os.Open(path)
	if err != nil {
		return "", nerr.Wrap(nerr.IO, op, "open client secret file", err)
	}
	defer f.Close()
	after, err := f.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return "", nerr.New(nerr.Forbidden, op, "client secret file changed while opening")
	}
	if runtime.GOOS != "windows" && after.Mode().Perm()&0o077 != 0 {
		return "", nerr.New(nerr.Forbidden, op, "client secret file permissions changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxClientSecretBytes+1))
	if err != nil {
		return "", nerr.Wrap(nerr.IO, op, "read client secret file", err)
	}
	if len(raw) > maxClientSecretBytes {
		return "", nerr.New(nerr.Exhausted, op, "client secret file exceeds 64 KiB")
	}
	return strings.TrimSpace(string(raw)), nil
}

// IntrospectorConfig configures RFC 7662 token introspection.
type IntrospectorConfig struct {
	Endpoint     string
	ClientID     string
	ClientSecret string
	Issuer       string
	Audience     string
	CacheTTL     time.Duration
	Skew         time.Duration
	Now          func() time.Time
	HTTPClient   *http.Client
}

type cachedIntrospection struct {
	token     *oidc.VerifiedToken
	expiresAt time.Time
}

// Introspector performs RFC 7662 token introspection with bounded LRU caching.
type Introspector struct {
	endpoint     string
	clientID     string
	clientSecret string
	issuer       string
	audience     string
	cacheTTL     time.Duration
	skew         time.Duration
	now          func() time.Time
	client       *http.Client

	mu    sync.Mutex
	cache map[string]*cachedIntrospection
	order []string
}

// NewIntrospector builds a new RFC 7662 introspector.
func NewIntrospector(cfg IntrospectorConfig) (*Introspector, error) {
	const op = "authbroker.NewIntrospector"
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, nerr.New(nerr.InvalidArgument, op, "introspection endpoint is required")
	}
	if !strings.HasPrefix(cfg.Endpoint, "https://") {
		return nil, nerr.New(nerr.InvalidArgument, op, "introspection endpoint must be https")
	}
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	ttl := cfg.CacheTTL
	if ttl <= 0 {
		ttl = defaultIntrospectionCacheTTL
	}
	skew := cfg.Skew
	if skew <= 0 {
		skew = oidc.DefaultSkew
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{
			Timeout: defaultIntrospectionTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
	}
	return &Introspector{
		endpoint:     cfg.Endpoint,
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		issuer:       cfg.Issuer,
		audience:     cfg.Audience,
		cacheTTL:     ttl,
		skew:         skew,
		now:          now,
		client:       client,
		cache:        make(map[string]*cachedIntrospection),
	}, nil
}

// Introspect sends an RFC 7662 introspection request or returns a cached result.
func (i *Introspector) Introspect(ctx context.Context, token string) (*oidc.VerifiedToken, error) {
	const op = "authbroker.Introspect"
	if strings.TrimSpace(token) == "" {
		return nil, nerr.New(nerr.InvalidArgument, op, "token is required")
	}

	h := sha256.Sum256([]byte(token))
	cacheKey := hex.EncodeToString(h[:])

	now := i.now().UTC()
	i.mu.Lock()
	if c, ok := i.cache[cacheKey]; ok {
		if now.Before(c.expiresAt) {
			i.mu.Unlock()
			return c.token, nil
		}
		delete(i.cache, cacheKey)
	}
	i.mu.Unlock()

	form := url.Values{
		"token":           {token},
		"token_type_hint": {"access_token"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, i.endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, nerr.Wrap(nerr.Internal, op, "build request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if i.clientSecret != "" {
		req.SetBasicAuth(i.clientID, i.clientSecret)
	}

	resp, err := i.client.Do(req)
	if err != nil {
		return nil, nerr.Wrap(nerr.Unavailable, op, "introspection request failed", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nerr.New(nerr.Unauthorized, op, "introspection request denied with HTTP status")
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxExchangeBody+1))
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, op, "read introspection response", err)
	}
	if len(body) > maxExchangeBody {
		return nil, nerr.New(nerr.Exhausted, op, "introspection response exceeded 64 KiB")
	}

	var claims map[string]any
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if err := dec.Decode(&claims); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, op, "malformed introspection json", err)
	}

	active, ok := claims["active"].(bool)
	if !ok || !active {
		return nil, nerr.New(nerr.Unauthorized, op, "token is not active")
	}

	sub, ok := claims["sub"].(string)
	if !ok || strings.TrimSpace(sub) == "" {
		return nil, nerr.New(nerr.Unauthorized, op, "introspection response missing sub claim")
	}

	var expTime time.Time
	if expRaw, ok := claims["exp"]; ok {
		var expSec int64
		switch v := expRaw.(type) {
		case json.Number:
			expSec, _ = v.Int64()
		case float64:
			expSec = int64(v)
		}
		if expSec > 0 {
			expTime = time.Unix(expSec, 0).UTC()
		}
	}
	if expTime.IsZero() {
		return nil, nerr.New(nerr.Unauthorized, op, "introspection response missing or invalid exp claim")
	}
	if now.After(expTime.Add(i.skew)) {
		return nil, nerr.New(nerr.Unauthorized, op, "introspected token is expired")
	}

	if iss, ok := claims["iss"].(string); ok && iss != "" && i.issuer != "" {
		if iss != i.issuer {
			return nil, nerr.New(nerr.Unauthorized, op, "introspected token issuer mismatch")
		}
	}

	if cid, ok := claims["client_id"].(string); ok && cid != "" && i.clientID != "" {
		if cid != i.clientID {
			return nil, nerr.New(nerr.Unauthorized, op, "introspected token client_id mismatch")
		}
	}

	var audiences []string
	switch a := claims["aud"].(type) {
	case string:
		audiences = []string{a}
	case []any:
		for _, item := range a {
			if s, ok := item.(string); ok {
				audiences = append(audiences, s)
			}
		}
	}
	if i.audience != "" && len(audiences) > 0 {
		matched := false
		for _, a := range audiences {
			if a == i.audience || a == i.clientID {
				matched = true
				break
			}
		}
		if !matched {
			return nil, nerr.New(nerr.Unauthorized, op, "introspected token audience mismatch")
		}
	}

	var iatTime time.Time
	if iatRaw, ok := claims["iat"]; ok {
		var iatSec int64
		switch v := iatRaw.(type) {
		case json.Number:
			iatSec, _ = v.Int64()
		case float64:
			iatSec = int64(v)
		}
		if iatSec > 0 {
			iatTime = time.Unix(iatSec, 0).UTC()
		}
	}
	if iatTime.IsZero() {
		iatTime = now
	}

	jti, _ := claims["jti"].(string)

	vtok := &oidc.VerifiedToken{
		Issuer:   i.issuer,
		Subject:  sub,
		Audience: audiences,
		IssuedAt: iatTime,
		Expiry:   expTime,
		JTI:      jti,
		Claims:   claims,
	}

	ttl := i.cacheTTL
	if until := expTime.Sub(now); until < ttl {
		ttl = until
	}
	if ttl > 0 {
		i.mu.Lock()
		if len(i.cache) >= maxIntrospectionCacheSize && len(i.order) > 0 {
			oldest := i.order[0]
			i.order = i.order[1:]
			delete(i.cache, oldest)
		}
		i.cache[cacheKey] = &cachedIntrospection{
			token:     vtok,
			expiresAt: now.Add(ttl),
		}
		i.order = append(i.order, cacheKey)
		i.mu.Unlock()
	}

	return vtok, nil
}
