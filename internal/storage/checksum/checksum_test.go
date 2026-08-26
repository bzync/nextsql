package checksum

import "testing"

func TestWriteVerify(t *testing.T) {
	buf := []byte("hello checksum world!!!!")
	off := 8
	Write(buf, off)
	if err := Verify(buf, off); err != nil {
		t.Fatal(err)
	}
	buf[0] ^= 0x01
	if err := Verify(buf, off); err == nil {
		t.Fatal("expected mismatch")
	}
}
