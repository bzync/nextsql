// Package xcrypto holds local copies of the golang.org/x/crypto packages
// NextSQL actually uses.
//
// Docker Scout and similar scanners match GO-2026-5932 against every
// version of the golang.org/x/crypto module (the unmaintained openpgp
// package; no fix version exists). This repository never imported
// openpgp. The copies here are the argon2, blake2b, and ocsp packages
// from golang.org/x/crypto v0.56.0, with the argon2 → blake2b import
// rewritten so published nextsql/nextsqld binaries do not record that
// module. HKDF used by ENCRYPTED CLIENT lives in the standard library
// crypto/hkdf package instead.
package xcrypto
