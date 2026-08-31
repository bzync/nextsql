// Package oidc holds the pure, offline primitives an authentication broker uses
// to turn an external OpenID Connect token into a verified claim set: compact
// JWS signature verification over an asymmetric key, JWKS document parsing and
// caching, and ID-token / access-token validation.
//
// Nothing here talks to a database or to the NextSQL wire protocol. The broker
// (`internal/authbroker`) composes these primitives; `nextsqld` never imports
// this package. Every decoder rejects malformed input with a typed error and
// never allocates from an unchecked length.
package oidc

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"hash"
	"math/big"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// maxCompactToken bounds the size of a compact JWS/JWT the broker will parse.
// Real ID tokens are well under 8 KiB; this leaves generous headroom while
// keeping a hostile client from forcing large allocations.
const maxCompactToken = 1 << 16 // 64 KiB

// Signature algorithms the broker accepts. Every one is an asymmetric signature
// over SHA-2. MAC algorithms ("HS*") and "none" are never accepted: a broker
// that trusted a MAC alg would verify a token with a key an attacker could also
// hold, and "none" is unauthenticated.
const (
	RS256 = "RS256"
	RS384 = "RS384"
	RS512 = "RS512"
	PS256 = "PS256"
	PS384 = "PS384"
	PS512 = "PS512"
	ES256 = "ES256"
	ES384 = "ES384"
	ES512 = "ES512"
)

// DefaultAllowedAlgs is the conservative allow-list applied when a profile does
// not name its own.
var DefaultAllowedAlgs = []string{RS256, ES256, PS256}

// AlgIsAsymmetric reports whether alg is one of the asymmetric signature
// algorithms this package can verify. It is false for "none", every "HS*" MAC
// algorithm, and anything unrecognized.
func AlgIsAsymmetric(alg string) bool {
	switch alg {
	case RS256, RS384, RS512, PS256, PS384, PS512, ES256, ES384, ES512:
		return true
	default:
		return false
	}
}

// jwsHeader is the protected header of a compact JWS.
type jwsHeader struct {
	Alg string `json:"alg"`
	Kid string `json:"kid"`
	Typ string `json:"typ"`
}

// jwsParts is a decoded but not-yet-verified compact JWS.
type jwsParts struct {
	header       jwsHeader
	signingInput []byte // header_b64 + "." + payload_b64, ASCII
	payload      []byte // decoded payload bytes (JSON)
	sig          []byte // decoded signature bytes
}

// parseCompact splits and base64url-decodes a compact JWS without verifying it.
func parseCompact(token string) (*jwsParts, error) {
	bad := func(msg string) (*jwsParts, error) {
		return nil, nerr.New(nerr.InvalidFormat, "oidc.parseCompact", msg)
	}
	if len(token) == 0 || len(token) > maxCompactToken {
		return bad("token length out of range")
	}
	// A compact JWS has exactly two dots. The header and payload segments must
	// be non-empty; the signature segment may be empty (an "alg":"none"
	// unsecured JWT), which the algorithm allow-list then rejects.
	first := strings.IndexByte(token, '.')
	last := strings.LastIndexByte(token, '.')
	if first <= 0 || last <= first+1 {
		return bad("not a compact JWS")
	}
	if strings.IndexByte(token[first+1:last], '.') >= 0 {
		return bad("compact JWS must have exactly three segments")
	}
	headerB64 := token[:first]
	payloadB64 := token[first+1 : last]
	sigB64 := token[last+1:]

	headerRaw, err := decodeSegment(headerB64)
	if err != nil {
		return bad("header is not base64url")
	}
	payloadRaw, err := decodeSegment(payloadB64)
	if err != nil {
		return bad("payload is not base64url")
	}
	sigRaw, err := decodeSegment(sigB64)
	if err != nil {
		return bad("signature is not base64url")
	}
	var hdr jwsHeader
	if err := strictJSON(headerRaw, &hdr); err != nil {
		return bad("header is not valid JSON")
	}
	if hdr.Alg == "" {
		return bad("header has no alg")
	}
	return &jwsParts{
		header:       hdr,
		signingInput: []byte(token[:last]),
		payload:      payloadRaw,
		sig:          sigRaw,
	}, nil
}

// verifySignature checks p's signature against pub using the header alg, which
// must already have been confirmed to be in the caller's allow-list.
func verifySignature(p *jwsParts, pub crypto.PublicKey) error {
	fail := nerr.New(nerr.Unauthorized, "oidc.verifySignature", "token signature is invalid")
	h, err := hashFor(p.header.Alg)
	if err != nil {
		return err
	}
	digest := sum(h, p.signingInput)

	switch p.header.Alg {
	case RS256, RS384, RS512:
		rk, ok := pub.(*rsa.PublicKey)
		if !ok {
			return keyMismatch()
		}
		if rsa.VerifyPKCS1v15(rk, h, digest, p.sig) != nil {
			return fail
		}
		return nil
	case PS256, PS384, PS512:
		rk, ok := pub.(*rsa.PublicKey)
		if !ok {
			return keyMismatch()
		}
		opts := &rsa.PSSOptions{SaltLength: rsa.PSSSaltLengthEqualsHash, Hash: h}
		if rsa.VerifyPSS(rk, h, digest, p.sig, opts) != nil {
			return fail
		}
		return nil
	case ES256, ES384, ES512:
		ek, ok := pub.(*ecdsa.PublicKey)
		if !ok {
			return keyMismatch()
		}
		// JWS ECDSA signatures are the fixed-width R||S concatenation, not ASN.1.
		size := (ek.Curve.Params().BitSize + 7) / 8
		if len(p.sig) != 2*size {
			return fail
		}
		r := new(big.Int).SetBytes(p.sig[:size])
		s := new(big.Int).SetBytes(p.sig[size:])
		if !ecdsa.Verify(ek, digest, r, s) {
			return fail
		}
		return nil
	default:
		return nerr.New(nerr.Unauthorized, "oidc.verifySignature", "unsupported signature algorithm")
	}
}

func keyMismatch() error {
	return nerr.New(nerr.Unauthorized, "oidc.verifySignature", "JWKS key type does not match the token algorithm")
}

func hashFor(alg string) (crypto.Hash, error) {
	switch alg {
	case RS256, PS256, ES256:
		return crypto.SHA256, nil
	case RS384, PS384, ES384:
		return crypto.SHA384, nil
	case RS512, PS512, ES512:
		return crypto.SHA512, nil
	default:
		return 0, nerr.New(nerr.Unauthorized, "oidc.hashFor", "unsupported signature algorithm")
	}
}

func newHash(h crypto.Hash) hash.Hash {
	switch h {
	case crypto.SHA256:
		return sha256.New()
	case crypto.SHA384:
		return sha512.New384()
	case crypto.SHA512:
		return sha512.New()
	default:
		return sha256.New()
	}
}

func sum(h crypto.Hash, data []byte) []byte {
	hh := newHash(h)
	hh.Write(data)
	return hh.Sum(nil)
}

// decodeSegment decodes one compact-JWS segment: base64url, URL alphabet, no
// padding per RFC 7515. A few IdPs emit padded segments; tolerate that by
// trimming '=' first.
func decodeSegment(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	return base64.RawURLEncoding.DecodeString(s)
}

// strictJSON unmarshals exactly one JSON value from data and rejects trailing
// bytes. Numbers are preserved as json.Number for callers that need exact
// integer timestamps.
func strictJSON(data []byte, v any) error {
	dec := json.NewDecoder(strings.NewReader(string(data)))
	dec.UseNumber()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return nerr.New(nerr.InvalidFormat, "oidc.strictJSON", "trailing bytes after JSON value")
	}
	return nil
}
