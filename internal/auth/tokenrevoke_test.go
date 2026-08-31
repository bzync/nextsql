package auth

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestRevokeByTokenID(t *testing.T) {
	ks := newIssuer(t)
	tok, id, exp, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now())
	rev, err := CreateTokenRevocations(filepath.Join(t.TempDir(), "rev"))
	if err != nil {
		t.Fatal(err)
	}
	v := NewTokenVerifier(ks, rev, "")
	if _, err := v.Verify(tok); err != nil {
		t.Fatalf("pre-revocation verify failed: %v", err)
	}
	if err := rev.Revoke(id, exp); err != nil {
		t.Fatal(err)
	}
	if _, err := v.Verify(tok); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("revoked credential still verifies: %v", err)
	}
}

func TestRevokePrincipalCutoff(t *testing.T) {
	ks := newIssuer(t)
	old, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now().Add(-time.Minute))
	rev, err := CreateTokenRevocations(filepath.Join(t.TempDir(), "rev"))
	if err != nil {
		t.Fatal(err)
	}
	if err := rev.RevokePrincipal("App", time.Now()); err != nil {
		t.Fatal(err)
	}
	v := NewTokenVerifier(ks, rev, "")
	if _, err := v.Verify(old); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("credential issued before the cutoff still verifies: %v", err)
	}
	// A credential issued after the cutoff is unaffected.
	fresh, _, _, _ := ks.Mint(TokenMintRequest{Principal: "app", TTL: time.Hour}, time.Now().Add(time.Minute))
	if _, err := v.Verify(fresh); err != nil {
		t.Fatalf("credential issued after the cutoff rejected: %v", err)
	}
}

func TestRevocationPrunesExpired(t *testing.T) {
	rev, err := CreateTokenRevocations(filepath.Join(t.TempDir(), "rev"))
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	id[0] = 1
	if err := rev.Revoke(id, time.Now().Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rev.Prune(); err != nil {
		t.Fatal(err)
	}
	if ids, _ := rev.Counts(); ids != 0 {
		t.Fatalf("expired revocation not pruned: %d", ids)
	}
}

func TestRevocationReloadLastKnownGood(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rev")
	rev, err := CreateTokenRevocations(path)
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	id[3] = 9
	if err := rev.Revoke(id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rev.Reload(); err == nil {
		t.Fatal("reload of corrupt revocation file returned nil")
	}
	claims := &TokenClaims{Principal: "app", IssuedAt: time.Now()}
	claims.TokenID = id
	if !rev.IsRevoked(claims) {
		t.Fatal("revocation set not retained after failed reload")
	}
}

func TestRevocationRoundTripsOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rev")
	rev, err := CreateTokenRevocations(path)
	if err != nil {
		t.Fatal(err)
	}
	var id [16]byte
	id[1] = 7
	if err := rev.Revoke(id, time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := rev.RevokePrincipal("svc", time.Now()); err != nil {
		t.Fatal(err)
	}
	re, err := OpenTokenRevocations(path)
	if err != nil {
		t.Fatal(err)
	}
	ids, cutoffs := re.Counts()
	if ids != 1 || cutoffs != 1 {
		t.Fatalf("reopened revocation set: ids=%d cutoffs=%d", ids, cutoffs)
	}
}

func FuzzDecodeTokenClaims(f *testing.F) {
	ks, err := CreateTokenKeyset(filepath.Join(f.TempDir(), "keyset"))
	if err != nil {
		f.Fatal(err)
	}
	good, _, _, err := ks.Mint(TokenMintRequest{Principal: "app", Roles: []string{"r"}, TTL: time.Hour}, time.Now())
	if err != nil {
		f.Fatal(err)
	}
	if signed, _, err := splitToken(good); err == nil {
		f.Add(signed)
	}
	f.Add([]byte("NSSC"))
	f.Add([]byte{0, 1, 2, 3, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		claims, err := decodeTokenClaims(raw)
		if err != nil {
			if claims != nil {
				t.Fatal("error with non-nil claims")
			}
			return
		}
		if claims == nil {
			t.Fatal("nil claims without error")
		}
		// Re-encoding a decoded claim set must not panic.
		_, _ = encodeTokenClaims(claims)
	})
}

func FuzzDecodeTokenKeys(f *testing.F) {
	ks, err := CreateTokenKeyset(filepath.Join(f.TempDir(), "keyset"))
	if err != nil {
		f.Fatal(err)
	}
	if raw, err := os.ReadFile(ks.Path()); err == nil {
		f.Add(raw)
	}
	f.Add([]byte("NSTK"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		keys, err := decodeTokenKeys(raw)
		if err != nil {
			if keys != nil {
				t.Fatal("error with non-nil keys")
			}
			return
		}
		if keys == nil {
			t.Fatal("nil keys without error")
		}
	})
}
