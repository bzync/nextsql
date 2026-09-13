// Package credential isolates NextSQL Admin's use of the operating system
// credential store. Profile metadata is deliberately separate and contains no
// secret; only a password is ever passed to this package.
package credential

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"strings"

	keyring "github.com/zalando/go-keyring"
)

// Service is the stable OS-keyring namespace for Studio connection secrets.
// It is versioned so a future credential envelope can never be mistaken for a
// password written by this format.
const Service = "nextsql-admin/studio-password/v1"

// Store is the narrow credential-store contract used by Operations mode. Its
// small shape permits deterministic tests without ever requiring a real
// desktop keyring in CI.
type Store interface {
	Get(key string) (string, error)
	Set(key, secret string) error
	Delete(key string) error
}

// OSStore stores secrets only in the current operating system user's native
// credential service: Secret Service on Linux (WSL 2 included, where a
// Secret Service provider must be running) and Keychain on macOS. It
// intentionally has no file fallback.
type OSStore struct{}

func (OSStore) Get(key string) (string, error) { return keyring.Get(Service, key) }
func (OSStore) Set(key, secret string) error   { return keyring.Set(Service, key, secret) }
func (OSStore) Delete(key string) error        { return keyring.Delete(Service, key) }

// Principal is one authenticated identity at one exact nextsqld: the
// address Admin dials, the TLS server name it verifies, and the NSQL user.
type Principal struct {
	Address    string
	ServerName string
	User       string
}

// Target is the principal a saved password signs in as, plus the database
// named in its Hello.
type Target struct {
	Principal
	Database string
}

// DelegationKey returns the opaque, deterministic keyring account for a
// password that origin saved for target. Binding the origin matters: Admin
// can be served to several people, and a saved password is a delegation
// ("whoever can authenticate as origin may also sign in as target"), not a
// secret any signed-in operator may spend. A different origin principal, a
// different target address/server name/user/database, or a relabelled
// profile pointing somewhere else all derive a different account, so none of
// them can retrieve the credential. Every field is length-prefixed, so no
// choice of values can collide with another, and none appears in the account
// name or in logs.
func DelegationKey(origin Principal, target Target) string {
	h := sha256.New()
	write := func(s string) {
		s = strings.TrimSpace(s)
		var n [4]byte
		binary.BigEndian.PutUint32(n[:], uint32(len(s)))
		h.Write(n[:])
		h.Write([]byte(s))
	}
	write("nextsql-admin/switch-delegation/v2")
	for _, s := range []string{
		origin.Address, origin.ServerName, origin.User,
		target.Address, target.ServerName, target.User, target.Database,
	} {
		write(s)
	}
	return "delegation-" + hex.EncodeToString(h.Sum(nil))
}
