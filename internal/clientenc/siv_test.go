package clientenc

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func fromHex(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatalf("hex.DecodeString(%q): %v", s, err)
	}
	return b
}

func TestRFC5297AppendixA1(t *testing.T) {
	// RFC 5297 Appendix A.1 Test Vector
	keyHex := "fffefdfcfbfaf9f8f7f6f5f4f3f2f1f0f0f1f2f3f4f5f6f7f8f9fafbfcfdfeff"
	adHex := "101112131415161718191a1b1c1d1e1f2021222324252627"
	plainHex := "112233445566778899aabbccddee"
	expectedCipherHex := "85632d07c6e8f37f950acd320a2ecc9340c02b9690c4dc04daef7f6afe5c"

	key := fromHex(t, keyHex)
	ad := fromHex(t, adHex)
	plain := fromHex(t, plainHex)
	expectedCipher := fromHex(t, expectedCipherHex)

	siv, err := NewAESSIV(key)
	if err != nil {
		t.Fatalf("NewAESSIV: %v", err)
	}

	sealed := siv.Seal(plain, ad)
	if !bytes.Equal(sealed, expectedCipher) {
		t.Fatalf("Seal got %x, want %x", sealed, expectedCipher)
	}

	opened, err := siv.Open(sealed, ad)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plain) {
		t.Fatalf("Open got %x, want %x", opened, plain)
	}

	// Tampered AD must fail
	badAD := append([]byte(nil), ad...)
	badAD[0] ^= 0x01
	if _, err := siv.Open(sealed, badAD); err == nil {
		t.Fatalf("Open with tampered AD expected error, got nil")
	}

	// Tampered ciphertext must fail
	badCipher := append([]byte(nil), sealed...)
	badCipher[len(badCipher)-1] ^= 0x01
	if _, err := siv.Open(badCipher, ad); err == nil {
		t.Fatalf("Open with tampered ciphertext expected error, got nil")
	}
}

func TestRFC5297AppendixA2(t *testing.T) {
	// RFC 5297 Appendix A.2 Test Vector
	keyHex := "7f7e7d7c7b7a79787776757473727170404142434445464748494a4b4c4d4e4f"
	ad1Hex := "00112233445566778899aabbccddeeffdeaddadadeaddadaffeeddccbbaa99887766554433221100"
	ad2Hex := "102030405060708090a0"
	nonceHex := "09f911029d74e35bd84156c5635688c0"
	plainHex := "7468697320697320736f6d6520706c61696e7465787420746f20656e6372797074207573696e67205349562d414553"
	expectedCipherHex := "7bdb6e3b432667eb06f4d14bff2fbd0fcb900f2fddbe404326601965c889bf17dba77ceb094fa663b7a3f748ba8af829ea64ad544a272e9c485b62a3fd5c0d"

	key := fromHex(t, keyHex)
	ad1 := fromHex(t, ad1Hex)
	ad2 := fromHex(t, ad2Hex)
	nonce := fromHex(t, nonceHex)
	plain := fromHex(t, plainHex)
	expectedCipher := fromHex(t, expectedCipherHex)

	siv, err := NewAESSIV(key)
	if err != nil {
		t.Fatalf("NewAESSIV: %v", err)
	}

	sealed := siv.Seal(plain, ad1, ad2, nonce)
	if !bytes.Equal(sealed, expectedCipher) {
		t.Fatalf("Seal got %x, want %x", sealed, expectedCipher)
	}

	opened, err := siv.Open(sealed, ad1, ad2, nonce)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if !bytes.Equal(opened, plain) {
		t.Fatalf("Open got %x, want %x", opened, plain)
	}
}
