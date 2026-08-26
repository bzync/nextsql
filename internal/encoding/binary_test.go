package encoding

import "testing"

func TestRoundTrip(t *testing.T) {
	b := make([]byte, 16)
	PutU16(b, 0, 0xBEEF)
	PutU32(b, 2, 0x01020304)
	PutU64(b, 6, 0x1122334455667788)
	if U16(b, 0) != 0xBEEF || U32(b, 2) != 0x01020304 || U64(b, 6) != 0x1122334455667788 {
		t.Fatalf("round trip mismatch: %x", b)
	}
}

func TestReadBounds(t *testing.T) {
	b := []byte{1, 2, 3}
	if _, err := ReadU16(b, 2); err == nil {
		t.Fatal("expected truncated field")
	}
	if _, err := ReadU64(b, 0); err == nil {
		t.Fatal("expected truncated field")
	}
	got, err := ReadU16(b, 0)
	if err != nil || got != 0x0201 {
		t.Fatalf("got %x err %v", got, err)
	}
}
