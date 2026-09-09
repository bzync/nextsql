package nerr

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicCodesRoundTripAndRemainERRPrefixed(t *testing.T) {
	for _, code := range allCodes {
		public := PublicCode(code)
		if !strings.HasPrefix(public, "ERR_") {
			t.Fatalf("PublicCode(%q) = %q", code, public)
		}
		back, ok := ParsePublicCode(public)
		if !ok || back != code {
			t.Fatalf("ParsePublicCode(%q) = %q, %v; want %q, true", public, back, ok, code)
		}
	}
	if _, ok := ParsePublicCode("ERR_NOT_A_NEXTSQL_CODE"); ok {
		t.Fatal("unknown public code accepted")
	}
}

func TestErrorIsByCode(t *testing.T) {
	err := New(Corruption, "page.Parse", "bad magic")
	if !HasCode(err, Corruption) {
		t.Fatal("expected corruption code")
	}
	if HasCode(err, Crypto) {
		t.Fatal("did not expect crypto code")
	}
	if !errors.Is(err, New(Corruption, "", "")) {
		t.Fatal("errors.Is should match by code")
	}
}

func TestForeignKeyCode(t *testing.T) {
	err := New(ForeignKey, "executor.fk", "foreign key violation")
	if !HasCode(err, ForeignKey) {
		t.Fatal("expected foreign_key")
	}
	if err.Code != "foreign_key" {
		t.Fatalf("code %q", err.Code)
	}
}

func TestWrapUnwrap(t *testing.T) {
	inner := errors.New("disk full")
	err := Wrap(IO, "file.Write", "write", inner)
	if !errors.Is(err, inner) {
		t.Fatal("unwrap failed")
	}
	if err.Error() == "" {
		t.Fatal("empty error text")
	}
}
