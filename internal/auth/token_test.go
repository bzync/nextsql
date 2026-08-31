package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

func newIssuer(t *testing.T) *TokenKeyset {
	t.Helper()
	ks, err := CreateTokenKeyset(filepath.Join(t.TempDir(), "keyset"))
	if err != nil {
		t.Fatal(err)
	}
	return ks
}

func TestTokenMintVerifyRoundTrip(t *testing.T) {
	ks := newIssuer(t)
	now := time.Now()
	tok, id, exp, err := ks.Mint(TokenMintRequest{
		Principal: "App",
		Audience:  "prod-eu",
		Database:  "orders",
		Realm:     "acme",
		Roles:     []string{"Readonly", "readonly"},
		TTL:       10 * time.Minute,
	}, now)
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeToken(tok) {
		t.Fatalf("minted token not recognized: %q", tok)
	}
	v := NewTokenVerifier(ks, nil, "prod-eu")
	claims, err := v.Verify(tok)
	if err != nil {
		t.Fatal(err)
	}
	if claims.Principal != "app" || claims.Database != "orders" || claims.Realm != "acme" || claims.Audience != "prod-eu" {
		t.Fatalf("claims: %+v", claims)
	}
	if len(claims.Roles) != 1 || claims.Roles[0] != "readonly" {
		t.Fatalf("roles not normalized/deduped: %+v", claims.Roles)
	}
	if claims.TokenID != id {
		t.Fatalf("token id mismatch")
	}
	if !claims.ExpiresAt.Equal(exp.UTC()) {
		t.Fatalf("expiry mismatch: %v vs %v", claims.ExpiresAt, exp)
	}
}

func TestTokenExpiry(t *testing.T) {
	ks := newIssuer(t)
	tok, _, _, err := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v := NewTokenVerifier(ks, nil, "")
	v.SetClock(func() time.Time { return time.Now().Add(2 * time.Minute) })
	if _, err := v.Verify(tok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("expired credential accepted: %v", err)
	}
}

func TestTokenNotYetValid(t *testing.T) {
	ks := newIssuer(t)
	tok, _, _, err := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour, NotBefore: time.Now().Add(30 * time.Minute)}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v := NewTokenVerifier(ks, nil, "")
	if _, err := v.Verify(tok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("not-yet-valid credential accepted: %v", err)
	}
}

func TestTokenAudienceMismatch(t *testing.T) {
	ks := newIssuer(t)
	tok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", Audience: "prod", TTL: time.Hour}, time.Now())
	if _, err := NewTokenVerifier(ks, nil, "staging").Verify(tok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("wrong audience accepted: %v", err)
	}
	// A verifier that requires an audience rejects an unscoped credential.
	unscoped, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	if _, err := NewTokenVerifier(ks, nil, "prod").Verify(unscoped); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("unscoped credential accepted by audience-bound verifier: %v", err)
	}
}

func TestTokenMaxLifetime(t *testing.T) {
	ks := newIssuer(t)
	tok, _, _, err := ks.Mint(TokenMintRequest{Principal: "app", TTL: 48 * time.Hour}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	v := NewTokenVerifier(ks, nil, "") // default max lifetime 24h
	if _, err := v.Verify(tok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("over-long credential accepted: %v", err)
	}
}

func TestTokenTamperRejected(t *testing.T) {
	ks := newIssuer(t)
	tok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	// Flip a byte in the base64url body.
	b := []byte(tok)
	b[len(b)-3] ^= 0x40
	if _, err := NewTokenVerifier(ks, nil, "").Verify(string(b)); err == nil {
		t.Fatal("tampered credential verified")
	}
}

func TestTokenWrongKeyset(t *testing.T) {
	ks := newIssuer(t)
	other := newIssuer(t)
	tok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	if _, err := NewTokenVerifier(other, nil, "").Verify(tok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("credential verified against foreign keyset: %v", err)
	}
}

func TestTokenKeyRotationOverlap(t *testing.T) {
	ks := newIssuer(t)
	oldTok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	id2, err := ks.AddKey()
	if err != nil {
		t.Fatal(err)
	}
	newTok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	v := NewTokenVerifier(ks, nil, "")
	if _, err := v.Verify(oldTok); err != nil {
		t.Fatalf("old credential rejected during overlap: %v", err)
	}
	if _, err := v.Verify(newTok); err != nil {
		t.Fatalf("new credential rejected: %v", err)
	}
	// Retire key 1: its credentials stop verifying, key 2's keep working.
	list := ks.List()
	var oldID uint32
	for _, k := range list {
		if k.ID != id2 {
			oldID = k.ID
		}
	}
	if err := ks.Retire(oldID); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(oldTok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("retired-key credential still verifies: %v", err)
	}
	if _, err := v.Verify(newTok); err != nil {
		t.Fatalf("current-key credential rejected after retiring old key: %v", err)
	}
}

func TestTokenKeysetReloadLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "keyset")
	ks, err := CreateTokenKeyset(path)
	if err != nil {
		t.Fatal(err)
	}
	tok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	// Corrupt the file, then reload: the in-memory keyset must be retained.
	if err := os.WriteFile(path, []byte("garbage"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := ks.Reload(); err == nil {
		t.Fatal("reload of corrupt keyset returned nil error")
	}
	if _, err := NewTokenVerifier(ks, nil, "").Verify(tok); err != nil {
		t.Fatalf("keyset not retained after failed reload: %v", err)
	}
}

func TestVerifyOnlyKeysetCannotMint(t *testing.T) {
	ks := newIssuer(t)
	pub := ks.PublicOnly()
	if _, _, _, err := pub.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now()); err == nil {
		t.Fatal("verify-only keyset minted a credential")
	}
	tok, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	if _, err := NewTokenVerifier(pub, nil, "").Verify(tok); err != nil {
		t.Fatalf("verify-only keyset failed to verify: %v", err)
	}
}

func TestDecodeTokenClaimsRejectsGarbage(t *testing.T) {
	cases := []string{
		"",
		"NSSC1.",
		"NSSC1.!!!!",
		"NSSC1." + "AAAA",
		"password123",
	}
	v := NewTokenVerifier(newIssuer(t), nil, "")
	for _, c := range cases {
		if _, err := v.Verify(c); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}
