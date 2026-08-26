package crypto

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func TestFileAuthTagWrongKey(t *testing.T) {
	a := testDEK(t, 1)
	b := testDEK(t, 1)
	ident, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	tag := FileAuthTag(a, ident)
	if err := VerifyFileAuthTag(a, ident, tag[:]); err != nil {
		t.Fatal(err)
	}
	if err := VerifyFileAuthTag(b, ident, tag[:]); err == nil {
		t.Fatal("wrong key must fail the file auth tag")
	}
	tag[0] ^= 0x01
	if err := VerifyFileAuthTag(a, ident, tag[:]); err == nil {
		t.Fatal("tampered tag must fail")
	}
}
