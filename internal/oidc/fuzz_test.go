package oidc

import (
	"context"
	"testing"
	"time"
)

// FuzzParseJWKS exercises the JWKS decoder against arbitrary bytes. It must
// never panic and must never return keys with a nil crypto key.
func FuzzParseJWKS(f *testing.F) {
	f.Add([]byte(`{"keys":[]}`))
	f.Add([]byte(`{"keys":[{"kty":"RSA","kid":"a","n":"AQAB","e":"AQAB"}]}`))
	f.Add([]byte(`{"keys":[{"kty":"EC","crv":"P-256","x":"","y":""}]}`))
	f.Add([]byte(`{"keys":[{"kty":"oct","k":"AAAA"}]}`))
	f.Add([]byte("not json"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		keys, err := ParseJWKS(raw)
		if err != nil {
			return
		}
		for _, k := range keys {
			if k.Key == nil {
				t.Fatalf("ParseJWKS returned a key with no crypto material for input %q", raw)
			}
		}
	})
}

// FuzzParseCompact exercises the compact-JWS splitter/decoder. It must never
// panic; a parsed token must have a non-empty alg.
func FuzzParseCompact(f *testing.F) {
	f.Add("a.b.c")
	f.Add("eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiJ4In0.AAAA")
	f.Add("....")
	f.Add("")
	f.Fuzz(func(t *testing.T, tok string) {
		p, err := parseCompact(tok)
		if err != nil {
			return
		}
		if p.header.Alg == "" {
			t.Fatalf("parseCompact accepted a token with no alg: %q", tok)
		}
	})
}

// FuzzVerify runs a full verifier over arbitrary token strings with a fixed
// JWKS. It must never panic and must never accept a token (the fuzz corpus is
// not signed by the cache's key).
func FuzzVerify(f *testing.F) {
	f.Add("x")
	f.Add("eyJhbGciOiJub25lIn0.eyJzdWIiOiJhIn0.")
	f.Fuzz(func(t *testing.T, tok string) {
		cache, err := NewJWKSCache(JWKSCacheConfig{
			Fetcher: staticFetcher(`{"keys":[{"kty":"RSA","kid":"k","use":"sig","alg":"RS256","n":"sXch-0YY1w2N2gU8pO2Zg2A0kctq4Thm3E7g0N0N0k","e":"AQAB"}]}`),
			Issuer:  "https://i.example",
			JWKSURI: "https://i.example/jwks",
		})
		if err != nil {
			return
		}
		v, err := NewIDTokenVerifier(IDTokenConfig{Issuer: "https://i.example", ClientID: "c", Now: func() time.Time { return time.Unix(1700000000, 0) }}, cache)
		if err != nil {
			return
		}
		if _, err := v.Verify(context.Background(), tok, ""); err == nil {
			t.Fatalf("fuzz token was accepted: %q", tok)
		}
	})
}

type staticFetcher string

func (s staticFetcher) Fetch(_ context.Context, _ string) ([]byte, error) {
	return []byte(s), nil
}
