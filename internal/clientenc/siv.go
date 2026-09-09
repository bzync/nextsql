package clientenc

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/subtle"
	"errors"

	"github.com/bzync/nextsql/internal/nerr"
)

var (
	zero16 = make([]byte, 16)
	rb16   = byte(0x87)
)

// dbl multiplies a 16-byte block by 2 in GF(2^128) per RFC 5297 §2.3.
func dbl(in []byte) []byte {
	out := make([]byte, 16)
	var carry byte
	for i := 15; i >= 0; i-- {
		b := in[i]
		out[i] = (b << 1) | carry
		carry = (b >> 7) & 1
	}
	if carry != 0 {
		out[15] ^= rb16
	}
	return out
}

func xor16(dst, a, b []byte) {
	for i := 0; i < 16; i++ {
		dst[i] = a[i] ^ b[i]
	}
}

// cmac computes AES-CMAC (NIST SP 800-38B) over msg using the provided AES block cipher.
func cmac(block cipher.Block, msg []byte) []byte {
	l := make([]byte, 16)
	block.Encrypt(l, zero16)
	k1 := dbl(l)
	k2 := dbl(k1)

	n := len(msg)
	numBlocks := (n + 15) / 16
	if n == 0 {
		numBlocks = 1
	}

	y := make([]byte, 16)
	lastBlock := make([]byte, 16)

	if n > 0 && n%16 == 0 {
		copy(lastBlock, msg[n-16:])
		for i := 0; i < 16; i++ {
			lastBlock[i] ^= k1[i]
		}
	} else {
		rem := 0
		if n > 0 {
			rem = n % 16
			copy(lastBlock, msg[n-rem:])
		}
		lastBlock[rem] = 0x80
		for i := 0; i < 16; i++ {
			lastBlock[i] ^= k2[i]
		}
	}

	buf := make([]byte, 16)
	for i := 0; i < numBlocks-1; i++ {
		mBlock := msg[i*16 : (i+1)*16]
		for j := 0; j < 16; j++ {
			buf[j] = y[j] ^ mBlock[j]
		}
		block.Encrypt(y, buf)
	}

	for j := 0; j < 16; j++ {
		buf[j] = y[j] ^ lastBlock[j]
	}
	block.Encrypt(y, buf)
	return y
}

// s2v computes S2V over a list of vectors per RFC 5297 §2.4.
func s2v(block cipher.Block, vectors ...[]byte) []byte {
	if len(vectors) == 0 {
		return cmac(block, zero16)
	}

	d := cmac(block, zero16)
	for i := 0; i < len(vectors)-1; i++ {
		c := cmac(block, vectors[i])
		d = dbl(d)
		for j := 0; j < 16; j++ {
			d[j] ^= c[j]
		}
	}

	last := vectors[len(vectors)-1]
	var t []byte
	if len(last) >= 16 {
		t = make([]byte, len(last))
		copy(t, last)
		off := len(t) - 16
		for j := 0; j < 16; j++ {
			t[off+j] ^= d[j]
		}
	} else {
		padded := make([]byte, 16)
		copy(padded, last)
		padded[len(last)] = 0x80
		dDbl := dbl(d)
		t = make([]byte, 16)
		for j := 0; j < 16; j++ {
			t[j] = dDbl[j] ^ padded[j]
		}
	}

	return cmac(block, t)
}

// ctrCrypt performs AES-CTR encryption/decryption starting with counter q per RFC 5297 §2.5.
// The rightmost 64 bits of the counter block are incremented modulo 2^64.
func ctrCrypt(block cipher.Block, q []byte, in []byte) []byte {
	out := make([]byte, len(in))
	ctr := make([]byte, 16)
	copy(ctr, q)
	var pad [16]byte
	for i := 0; i < len(in); i += 16 {
		block.Encrypt(pad[:], ctr)
		n := 16
		if len(in)-i < n {
			n = len(in) - i
		}
		for j := 0; j < n; j++ {
			out[i+j] = in[i+j] ^ pad[j]
		}
		for k := 15; k >= 8; k-- {
			ctr[k]++
			if ctr[k] != 0 {
				break
			}
		}
	}
	return out
}

// AESSIV implements RFC 5297 Synthetic Initialization Vector authenticated encryption.
type AESSIV struct {
	k1Block cipher.Block
	k2Block cipher.Block
}

// NewAESSIV creates a new AES-SIV instance from key.
// Key must be 32, 48, or 64 bytes (split evenly into K1 and K2).
func NewAESSIV(key []byte) (*AESSIV, error) {
	if len(key) != 32 && len(key) != 48 && len(key) != 64 {
		return nil, nerr.New(nerr.InvalidArgument, "clientenc.NewAESSIV", "invalid key length; want 32, 48, or 64 bytes")
	}
	half := len(key) / 2
	k1, k2 := key[:half], key[half:]
	b1, err := aes.NewCipher(k1)
	if err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "clientenc.NewAESSIV", "AES K1", err)
	}
	b2, err := aes.NewCipher(k2)
	if err != nil {
		return nil, nerr.Wrap(nerr.Crypto, "clientenc.NewAESSIV", "AES K2", err)
	}
	return &AESSIV{k1Block: b1, k2Block: b2}, nil
}

// Seal deterministically encrypts plaintext with associated data.
// Returns synthetic IV (16 bytes) followed by ciphertext.
func (siv *AESSIV) Seal(plaintext []byte, ad ...[]byte) []byte {
	all := make([][]byte, 0, len(ad)+1)
	all = append(all, ad...)
	all = append(all, plaintext)

	v := s2v(siv.k1Block, all...)

	q := make([]byte, 16)
	copy(q, v)
	q[8] &= 0x7f
	q[12] &= 0x7f

	c := ctrCrypt(siv.k2Block, q, plaintext)
	out := make([]byte, 16+len(c))
	copy(out[:16], v)
	copy(out[16:], c)
	return out
}

// Open decrypts and verifies the ciphertext.
// Returns plaintext or an authentication error.
func (siv *AESSIV) Open(ciphertext []byte, ad ...[]byte) ([]byte, error) {
	if len(ciphertext) < 16 {
		return nil, nerr.New(nerr.InvalidFormat, "clientenc.Open", "ciphertext shorter than IV")
	}
	v := ciphertext[:16]
	c := ciphertext[16:]

	q := make([]byte, 16)
	copy(q, v)
	q[8] &= 0x7f
	q[12] &= 0x7f

	plaintext := ctrCrypt(siv.k2Block, q, c)

	all := make([][]byte, 0, len(ad)+1)
	all = append(all, ad...)
	all = append(all, plaintext)

	vExpected := s2v(siv.k1Block, all...)
	if subtle.ConstantTimeCompare(v, vExpected) != 1 {
		return nil, errors.New("ciphertext authentication failed")
	}

	return plaintext, nil
}
