// Package dockerentry is the Linux container PID 1: it initializes or
// restores the data directory, waits for Raft peers when bootstrapping a
// Compose HA cluster, then execs nextsqld.
package dockerentry

import "fmt"

// Exit codes match the historical docker/entrypoint.sh so Compose/automation
// that branched on them keeps working.
const (
	exitFail  = 1
	exitTLS   = 2  // mTLS flags used without the TLS pair they require
	exitUsage = 64 // missing bootstrap user/password or split TLS pair
)

// Error is an entrypoint failure with a process exit code. Message must never
// contain passwords, keys, or tokens.
type Error struct {
	Code    int
	Message string
}

func (e *Error) Error() string { return e.Message }

func usage(format string, args ...any) error {
	return &Error{Code: exitUsage, Message: "nextsql: " + fmt.Sprintf(format, args...)}
}

func tlsFail(format string, args ...any) error {
	return &Error{Code: exitTLS, Message: "nextsql: " + fmt.Sprintf(format, args...)}
}

func fail(format string, args ...any) error {
	return &Error{Code: exitFail, Message: "nextsql: " + fmt.Sprintf(format, args...)}
}
