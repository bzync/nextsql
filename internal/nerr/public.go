package nerr

import "strings"

// PublicCode is the stable, uppercase vocabulary reserved for externally
// documented NextSQL errors. It is intentionally distinct from Code: Code is
// the long-standing internal/wire-v1 spelling, while a wire-versioned rollout
// can expose PublicCode without making old retry checks silently stop working.
func PublicCode(c Code) string {
	return "ERR_" + strings.ToUpper(string(c))
}

// ParsePublicCode maps the stable ERR_* spelling back to its internal class.
// Unknown values fail closed: callers must not mistake a future class for one
// they know how to retry.
func ParsePublicCode(s string) (Code, bool) {
	if !strings.HasPrefix(s, "ERR_") {
		return "", false
	}
	c := Code(strings.ToLower(strings.TrimPrefix(s, "ERR_")))
	for _, known := range allCodes {
		if c == known {
			return c, true
		}
	}
	return "", false
}

var allCodes = []Code{
	InvalidArgument, InvalidFormat, Corruption, NotFound, AlreadyExists,
	PageFull, Exhausted, Crypto, IO, Internal, Unavailable, Conflict,
	Deadlock, Serialization, Syntax, Unauthorized, Forbidden, Protocol,
	Canceled, ForeignKey,
}

// PublicCodeFor is the emit-side counterpart of ParsePublicCode: it reports
// the stable ERR_* spelling only for a documented class. It fails closed
// because docs/error-codes.md promises that every ERR_* name a client sees is
// one of the documented, stable names — a class that is not in the table has
// no reserved public spelling yet, and inventing one on the wire would make an
// undocumented name look stable. Callers omit the public code entirely in that
// case and leave the legacy class as the only signal.
func PublicCodeFor(c Code) (string, bool) {
	for _, known := range allCodes {
		if c == known {
			return PublicCode(c), true
		}
	}
	return "", false
}
