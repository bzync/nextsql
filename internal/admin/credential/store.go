// Package credential isolates NextSQL Admin's use of the operating system
// credential store. Profile metadata is deliberately separate and contains no
// secret; only a password is ever passed to this package.
package credential

import (
	"crypto/sha256"
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
// credential service: Secret Service on Linux, Keychain on macOS, and
// Credential Manager on Windows. It intentionally has no file fallback.
type OSStore struct{}

func (OSStore) Get(key string) (string, error) { return keyring.Get(Service, key) }
func (OSStore) Set(key, secret string) error   { return keyring.Set(Service, key, secret) }
func (OSStore) Delete(key string) error        { return keyring.Delete(Service, key) }

// Key returns an opaque, deterministic keyring account identifier for one
// exact NSQL target. The address, principal, realm, and database never appear
// in the OS-keyring account name or logs; changing any target component cannot
// retrieve another target's credential.
func Key(address, user, realm, database string) string {
	parts := []string{strings.TrimSpace(address), strings.TrimSpace(user), strings.TrimSpace(realm), strings.TrimSpace(database)}
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "target-" + hex.EncodeToString(sum[:])
}
