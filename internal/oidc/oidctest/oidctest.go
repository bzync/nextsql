// Package oidctest is a self-contained fake OpenID Connect provider for tests:
// an RSA (or ECDSA) signing key, a JWKS document, an OIDC discovery document,
// a compact-JWT signer, and an oidc.Fetcher that serves the two documents
// without any network.
package oidctest

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/oidc"
)

// IdP is a fake identity provider.
type IdP struct {
	Issuer string
	Kid    string
	Alg    string

	rsaKey *rsa.PrivateKey
	ecKey  *ecdsa.PrivateKey
}

// NewRSA builds a fake IdP that signs with RS256.
func NewRSA(t testing.TB, issuer string) *IdP {
	t.Helper()
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa keygen: %v", err)
	}
	return &IdP{Issuer: issuer, Kid: "test-rsa-1", Alg: oidc.RS256, rsaKey: k}
}

// NewES256 builds a fake IdP that signs with ES256.
func NewES256(t testing.TB, issuer string) *IdP {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ec keygen: %v", err)
	}
	return &IdP{Issuer: issuer, Kid: "test-ec-1", Alg: oidc.ES256, ecKey: k}
}

// StandardClaims returns a claim set with iss/aud/sub/exp/iat/nonce populated.
func (p *IdP) StandardClaims(clientID, subject, nonce string, now time.Time, ttl time.Duration) map[string]any {
	return map[string]any{
		"iss":   p.Issuer,
		"aud":   clientID,
		"sub":   subject,
		"iat":   now.Unix(),
		"exp":   now.Add(ttl).Unix(),
		"nonce": nonce,
	}
}

// Sign returns a compact JWT for claims signed with the IdP key.
func (p *IdP) Sign(t testing.TB, claims map[string]any) string {
	return p.SignWith(t, p.Alg, p.Kid, claims)
}

// SignWith signs with an explicit alg and kid so tests can forge mismatches.
// alg "none" produces an unsecured JWT with an empty signature.
func (p *IdP) SignWith(t testing.TB, alg, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": alg, "typ": "JWT"}
	if kid != "" {
		header["kid"] = kid
	}
	signingInput := seg(header) + "." + seg(claims)

	switch alg {
	case "none":
		return signingInput + "."
	case oidc.RS256:
		sum := sha256.Sum256([]byte(signingInput))
		sig, err := rsa.SignPKCS1v15(rand.Reader, p.rsaKey, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	case oidc.ES256:
		sum := sha256.Sum256([]byte(signingInput))
		r, s, err := ecdsa.Sign(rand.Reader, p.ecKey, sum[:])
		if err != nil {
			t.Fatalf("sign: %v", err)
		}
		out := make([]byte, 64)
		r.FillBytes(out[:32])
		s.FillBytes(out[32:])
		return signingInput + "." + base64.RawURLEncoding.EncodeToString(out)
	case "HS256":
		// A deliberately bogus MAC signature so tests can assert it is rejected
		// before any verification is attempted.
		return signingInput + "." + base64.RawURLEncoding.EncodeToString([]byte("not-a-real-mac"))
	default:
		t.Fatalf("oidctest: unsupported alg %q", alg)
		return ""
	}
}

// JWKS returns the JWKS document as JSON.
func (p *IdP) JWKS() []byte {
	var key map[string]any
	switch {
	case p.rsaKey != nil:
		key = map[string]any{
			"kty": "RSA",
			"kid": p.Kid,
			"use": "sig",
			"alg": oidc.RS256,
			"n":   base64.RawURLEncoding.EncodeToString(p.rsaKey.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(p.rsaKey.E)).Bytes()),
		}
	case p.ecKey != nil:
		key = map[string]any{
			"kty": "EC",
			"kid": p.Kid,
			"use": "sig",
			"alg": oidc.ES256,
			"crv": "P-256",
			"x":   base64.RawURLEncoding.EncodeToString(p.ecKey.X.Bytes()),
			"y":   base64.RawURLEncoding.EncodeToString(p.ecKey.Y.Bytes()),
		}
	}
	doc, _ := json.Marshal(map[string]any{"keys": []any{key}})
	return doc
}

// Discovery returns the OIDC discovery document as JSON.
func (p *IdP) Discovery() []byte {
	doc, _ := json.Marshal(map[string]any{
		"issuer":   p.Issuer,
		"jwks_uri": p.JWKSURI(),
	})
	return doc
}

// JWKSURI is the fake jwks_uri for this provider.
func (p *IdP) JWKSURI() string { return strings.TrimRight(p.Issuer, "/") + "/jwks" }

// Fetcher returns an oidc.Fetcher that serves this provider's discovery and
// JWKS documents and errors on any other URL.
func (p *IdP) Fetcher() *Fetcher {
	return &Fetcher{docs: map[string][]byte{
		strings.TrimRight(p.Issuer, "/") + "/.well-known/openid-configuration": p.Discovery(),
		p.JWKSURI(): p.JWKS(),
	}}
}

// Fetcher is an in-memory oidc.Fetcher. Its behaviour can be perturbed for
// outage / rotation tests.
type Fetcher struct {
	docs  map[string][]byte
	Fail  bool // when true every fetch errors
	Calls int  // total fetch attempts
}

// Fetch implements oidc.Fetcher.
func (f *Fetcher) Fetch(_ context.Context, url string) ([]byte, error) {
	f.Calls++
	if f.Fail {
		return nil, errFake("identity provider unreachable")
	}
	if b, ok := f.docs[url]; ok {
		return append([]byte(nil), b...), nil
	}
	return nil, errFake("no such document: " + url)
}

// Set replaces a served document (used for key-rotation tests).
func (f *Fetcher) Set(url string, doc []byte) { f.docs[url] = doc }

type errFake string

func (e errFake) Error() string { return string(e) }

func seg(v any) string {
	b, _ := json.Marshal(v)
	return base64.RawURLEncoding.EncodeToString(b)
}
