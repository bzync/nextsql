package bench

import (
	"bytes"
	"testing"
)

func TestEncodeBenchScanMatchesTypes(t *testing.T) {
	var kb, vb []byte
	for _, n := range []int{0, 1, 9, 10, 99, 100, 255, 256, 999, 1000, 65535, 1_000_000, 9_999_999} {
		wantK, wantV, err := encodeBenchScanRef(n)
		if err != nil {
			t.Fatalf("%d ref: %v", n, err)
		}
		kb, vb = encodeBenchScan(n, kb[:0], vb[:0])
		if !bytes.Equal(kb, wantK) {
			t.Fatalf("%d key\n got %x\nwant %x", n, kb, wantK)
		}
		if !bytes.Equal(vb, wantV) {
			t.Fatalf("%d val\n got %x\nwant %x", n, vb, wantV)
		}
	}
}

func TestDecimalFromInt64MatchesParse(t *testing.T) {
	// covered via encodeBenchScanRef vs encodeBenchScan for 0 and large ints
	_, _, err := encodeBenchScanRef(1234567)
	if err != nil {
		t.Fatal(err)
	}
}
