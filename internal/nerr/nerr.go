package nerr

import (
	"errors"
	"fmt"
)

// Code classifies a NextSQL error for callers without string matching.
type Code string

const (
	InvalidArgument Code = "invalid_argument"
	InvalidFormat   Code = "invalid_format"
	Corruption      Code = "corruption"
	NotFound        Code = "not_found"
	AlreadyExists   Code = "already_exists"
	PageFull        Code = "page_full"
	Exhausted       Code = "exhausted"
	Crypto          Code = "crypto"
	IO              Code = "io"
	Internal        Code = "internal"
	Unavailable     Code = "unavailable"
	Conflict        Code = "conflict"
	Deadlock        Code = "deadlock"
	Serialization   Code = "serialization"
	Syntax          Code = "syntax"
	Unauthorized    Code = "unauthorized"
	Forbidden       Code = "forbidden"
	Protocol        Code = "protocol"
	Canceled        Code = "canceled"
	ForeignKey      Code = "foreign_key"
)

// Error is a typed NextSQL error. Message must never contain keys, passwords, or tokens.
type Error struct {
	Code    Code
	Op      string
	Message string
	Err     error
}

func New(code Code, op, message string) *Error {
	return &Error{Code: code, Op: op, Message: message}
}

func Wrap(code Code, op, message string, err error) *Error {
	return &Error{Code: code, Op: op, Message: message, Err: err}
}

func (e *Error) Error() string {
	if e == nil {
		return "nextsql: <nil>"
	}
	if e.Op != "" && e.Err != nil {
		return fmt.Sprintf("nextsql %s: %s: %s: %v", e.Code, e.Op, e.Message, e.Err)
	}
	if e.Op != "" {
		return fmt.Sprintf("nextsql %s: %s: %s", e.Code, e.Op, e.Message)
	}
	if e.Err != nil {
		return fmt.Sprintf("nextsql %s: %s: %v", e.Code, e.Message, e.Err)
	}
	return fmt.Sprintf("nextsql %s: %s", e.Code, e.Message)
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	if !ok || e == nil || t == nil {
		return false
	}
	return e.Code == t.Code
}

func HasCode(err error, code Code) bool {
	var e *Error
	if !errors.As(err, &e) {
		return false
	}
	return e.Code == code
}
