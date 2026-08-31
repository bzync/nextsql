package oidc_test

import (
	"context"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/oidc"
	"github.com/bzync/nextsql/internal/oidc/oidctest"
)

const (
	testIssuer = "https://idp.example/realm"
	testClient = "nextsql-client-abc"
)

func newVerifier(t *testing.T, idp *oidctest.IdP, now func() time.Time) (*oidc.IDTokenVerifier, *oidctest.Fetcher) {
	t.Helper()
	f := idp.Fetcher()
	cache, err := oidc.NewJWKSCache(oidc.JWKSCacheConfig{
		Fetcher: f,
		Issuer:  idp.Issuer,
		JWKSURI: idp.JWKSURI(),
		Now:     now,
	})
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	v, err := oidc.NewIDTokenVerifier(oidc.IDTokenConfig{
		Issuer:   idp.Issuer,
		ClientID: testClient,
		Now:      now,
	}, cache)
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	return v, f
}

func TestIDTokenVerifyHappyPath(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	idp := oidctest.NewRSA(t, testIssuer)
	v, _ := newVerifier(t, idp, clock)

	tok := idp.Sign(t, idp.StandardClaims(testClient, "user-1", "nonce-xyz", now, time.Hour))
	vt, err := v.Verify(context.Background(), tok, "nonce-xyz")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if vt.Subject != "user-1" || vt.Issuer != testIssuer {
		t.Fatalf("unexpected verified token: %+v", vt)
	}
}

func TestIDTokenVerifyES256(t *testing.T) {
	now := time.Now()
	idp := oidctest.NewES256(t, testIssuer)
	v, _ := newVerifier(t, idp, func() time.Time { return now })
	tok := idp.Sign(t, idp.StandardClaims(testClient, "user-ec", "n", now, time.Hour))
	if _, err := v.Verify(context.Background(), tok, "n"); err != nil {
		t.Fatalf("verify ES256: %v", err)
	}
}

func TestIDTokenVerifyRejections(t *testing.T) {
	now := time.Now()
	clock := func() time.Time { return now }
	idp := oidctest.NewRSA(t, testIssuer)
	other := oidctest.NewRSA(t, testIssuer) // different key, same issuer string

	cases := []struct {
		name  string
		token func() string
		nonce string
	}{
		{"wrong issuer", func() string {
			c := idp.StandardClaims(testClient, "u", "n", now, time.Hour)
			c["iss"] = "https://evil.example"
			return idp.Sign(t, c)
		}, "n"},
		{"wrong audience", func() string {
			return idp.Sign(t, idp.StandardClaims("some-other-client", "u", "n", now, time.Hour))
		}, "n"},
		{"alg none", func() string {
			return idp.SignWith(t, "none", idp.Kid, idp.StandardClaims(testClient, "u", "n", now, time.Hour))
		}, "n"},
		{"mac alg", func() string {
			return idp.SignWith(t, "HS256", idp.Kid, idp.StandardClaims(testClient, "u", "n", now, time.Hour))
		}, "n"},
		{"expired", func() string {
			return idp.Sign(t, idp.StandardClaims(testClient, "u", "n", now.Add(-2*time.Hour), time.Hour))
		}, "n"},
		{"bad nonce", func() string {
			return idp.Sign(t, idp.StandardClaims(testClient, "u", "other-nonce", now, time.Hour))
		}, "n"},
		{"foreign signature", func() string {
			// signed by `other`, but presents idp's kid so the JWKS lookup
			// finds idp's public key and the signature check fails.
			return other.SignWith(t, oidc.RS256, idp.Kid, idp.StandardClaims(testClient, "u", "n", now, time.Hour))
		}, "n"},
		{"no subject", func() string {
			c := idp.StandardClaims(testClient, "", "n", now, time.Hour)
			delete(c, "sub")
			return idp.Sign(t, c)
		}, "n"},
		{"not a jwt", func() string { return "garbage.value" }, "n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			v, _ := newVerifier(t, idp, clock)
			if _, err := v.Verify(context.Background(), tc.token(), tc.nonce); err == nil {
				t.Fatalf("expected rejection for %s", tc.name)
			}
		})
	}
}

func TestIDTokenVerifierRejectsMACConfig(t *testing.T) {
	idp := oidctest.NewRSA(t, testIssuer)
	f := idp.Fetcher()
	cache, _ := oidc.NewJWKSCache(oidc.JWKSCacheConfig{Fetcher: f, Issuer: idp.Issuer, JWKSURI: idp.JWKSURI()})
	if _, err := oidc.NewIDTokenVerifier(oidc.IDTokenConfig{
		Issuer:      testIssuer,
		ClientID:    testClient,
		AllowedAlgs: []string{"HS256"},
	}, cache); err == nil {
		t.Fatal("expected NewIDTokenVerifier to reject a MAC algorithm")
	}
}

func TestReplayGuard(t *testing.T) {
	now := time.Now()
	g := oidc.NewReplayGuard(func() time.Time { return now })
	tok := &oidc.VerifiedToken{Subject: "u", IssuedAt: now, Expiry: now.Add(time.Hour), JTI: "abc"}
	if err := g.Observe(tok); err != nil {
		t.Fatalf("first observe: %v", err)
	}
	if err := g.Observe(tok); err == nil {
		t.Fatal("second observe of the same token must fail")
	}
	// A token whose replay window has passed is pruned and accepted again is
	// not a concern — but a fresh distinct token is fine.
	if err := g.Observe(&oidc.VerifiedToken{Subject: "u2", IssuedAt: now, Expiry: now.Add(time.Hour)}); err != nil {
		t.Fatalf("distinct token: %v", err)
	}
}

func TestJWKSCacheSoftStaleAndHardExpiry(t *testing.T) {
	base := time.Now()
	cur := base
	clock := func() time.Time { return cur }
	idp := oidctest.NewRSA(t, testIssuer)
	f := idp.Fetcher()
	cache, err := oidc.NewJWKSCache(oidc.JWKSCacheConfig{
		Fetcher: f, Issuer: idp.Issuer, JWKSURI: idp.JWKSURI(),
		SoftTTL: time.Hour, HardTTL: 24 * time.Hour, Now: clock,
	})
	if err != nil {
		t.Fatalf("cache: %v", err)
	}
	if _, err := cache.Key(context.Background(), idp.Kid); err != nil {
		t.Fatalf("initial key: %v", err)
	}
	calls := f.Calls

	// Within the soft TTL: served from cache, no fetch.
	cur = base.Add(30 * time.Minute)
	if _, err := cache.Key(context.Background(), idp.Kid); err != nil {
		t.Fatalf("cached key: %v", err)
	}
	if f.Calls != calls {
		t.Fatalf("expected no refresh within soft TTL")
	}

	// Past the soft TTL with the IdP down: stale-but-within-hard key still served.
	f.Fail = true
	cur = base.Add(3 * time.Hour)
	if _, err := cache.Key(context.Background(), idp.Kid); err != nil {
		t.Fatalf("soft-stale key should still be served: %v", err)
	}

	// Past the hard TTL: fail closed.
	cur = base.Add(48 * time.Hour)
	if _, err := cache.Key(context.Background(), idp.Kid); err == nil {
		t.Fatal("expected hard-TTL expiry to fail closed")
	}
}

func TestJWKSCacheUnknownKidRefresh(t *testing.T) {
	base := time.Now()
	cur := base
	idp := oidctest.NewRSA(t, testIssuer)
	f := idp.Fetcher()
	cache, _ := oidc.NewJWKSCache(oidc.JWKSCacheConfig{
		Fetcher: f, Issuer: idp.Issuer, JWKSURI: idp.JWKSURI(), Now: func() time.Time { return cur },
	})
	if _, err := cache.Key(context.Background(), idp.Kid); err != nil {
		t.Fatalf("known kid: %v", err)
	}
	// Move past the refresh rate-limit window but stay within the soft TTL.
	cur = base.Add(10 * time.Minute)
	// Unknown kid triggers exactly one refresh, then fails (rate-limited).
	before := f.Calls
	if _, err := cache.Key(context.Background(), "rotated-kid"); err == nil {
		t.Fatal("unknown kid must fail")
	}
	if f.Calls != before+1 {
		t.Fatalf("expected one refresh attempt for the unknown kid, got %d", f.Calls-before)
	}
	// A second immediate lookup does not refresh again.
	if _, err := cache.Key(context.Background(), "rotated-kid"); err == nil {
		t.Fatal("unknown kid must still fail")
	}
	if f.Calls != before+1 {
		t.Fatal("expected the unknown-kid refresh to be rate-limited")
	}
}
