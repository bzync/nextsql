package installgui

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"

	"github.com/bzync/nextsql/internal/nerr"
)

// generateToken returns a random 256-bit token, hex-encoded. It is the
// installer's only credential: a stray local process without it gets 403.
// See docs/design-installer-gui.md "Single-operator token auth".
func generateToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", nerr.Wrap(nerr.Internal, "installgui.generateToken", "read random", err)
	}
	return hex.EncodeToString(b), nil
}

// tokenEqual is a constant-time comparison against the installer's token.
func tokenEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
