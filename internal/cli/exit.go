package cli

import (
	"errors"

	"github.com/bzync/nextsql/internal/nerr"
)

// Process exit codes for nextsql (design B.11).
const (
	ExitOK         = 0
	ExitUsage      = 1
	ExitConnect    = 2
	ExitDirty      = 3
	ExitChecksum   = 4
	ExitSQL        = 5
	ExitValidation = 6
	ExitLocal      = 7
)

// Migrate fault sentinels. Wrap these so Code maps to 3/4/6 before nerr codes.
var (
	ErrDirty        = errors.New("database is dirty")
	ErrChecksum     = errors.New("migration checksum mismatch")
	ErrValidation   = errors.New("migration validation failed")
	ErrApply        = errors.New("migration apply failed")
	ErrLocalMissing = errors.New("data-dir or key-file is required")
)

type codedError struct {
	code int
	err  error
}

func (e *codedError) Error() string { return e.err.Error() }
func (e *codedError) Unwrap() error { return e.err }

// LocalMissing is an invalid_argument that exits 7 (missing data-dir / key-file).
func LocalMissing(op, message string) error {
	return &codedError{
		code: ExitLocal,
		err:  nerr.New(nerr.InvalidArgument, op, message),
	}
}

// Validation is an invalid_argument that exits 6 (a supplied plan or
// configuration failed validation — distinct from a malformed flag).
func Validation(op, message string) error {
	return &codedError{
		code: ExitValidation,
		err:  nerr.New(nerr.InvalidArgument, op, message),
	}
}

// Code maps err to a process exit code. nil is 0.
func Code(err error) int {
	if err == nil {
		return ExitOK
	}
	var ce *codedError
	if errors.As(err, &ce) {
		return ce.code
	}
	switch {
	case errors.Is(err, ErrDirty):
		return ExitDirty
	case errors.Is(err, ErrChecksum):
		return ExitChecksum
	case errors.Is(err, ErrValidation):
		return ExitValidation
	case errors.Is(err, ErrApply):
		return ExitSQL
	case errors.Is(err, ErrLocalMissing):
		return ExitLocal
	}
	var e *nerr.Error
	if !errors.As(err, &e) {
		return ExitUsage
	}
	switch e.Code {
	case nerr.InvalidArgument:
		return ExitUsage
	case nerr.IO, nerr.Unauthorized, nerr.Protocol, nerr.Unavailable:
		return ExitConnect
	default:
		return ExitSQL
	}
}
