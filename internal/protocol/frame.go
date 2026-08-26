// Package protocol is the versioned NextSQL native wire protocol.
package protocol

import (
	"io"
	"time"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
)

const (
	Magic           = "NSQL"
	Version         = uint16(1)
	HeaderSize      = 12
	AuthPassword    = 1
	AuthPasswordKey = 2

	DefaultMaxPacket      = 1 << 20
	DefaultMaxSQL         = 1 << 20
	DefaultMaxParams      = 256
	DefaultMaxPrepared    = 64
	DefaultMaxSessions    = 128
	DefaultMaxResultBytes = 64 << 20
	DefaultMaxName        = 256
	DefaultIdleTimeout    = 60 * time.Second
	DefaultBatchRows      = 256
)

// Type is a framed message type. Unknown types are protocol errors.
type Type uint8

const (
	TypeHello Type = iota + 1
	TypeHelloOK
	TypeAuth
	TypeAuthOK
	TypeQuery
	TypePrepare
	TypePrepareOK
	TypeExecute
	TypeCloseStmt
	TypeCloseOK
	TypeFlowAck
	TypeCancel
	TypeTerminate
	TypeRowDesc
	TypeDataBatch
	TypeCommandComplete
	TypeError
	TypeReady
	TypeUnlock
	TypeUnlockOK
)

// Limits bound every untrusted length on the wire.
type Limits struct {
	MaxPacket      int
	MaxSQL         int
	MaxParams      int
	MaxPrepared    int
	MaxSessions    int
	MaxResultBytes int64
	MaxName        int
	Idle           time.Duration
	Query          scheduler.Limits
}

func DefaultLimits() Limits {
	return Limits{
		MaxPacket:      DefaultMaxPacket,
		MaxSQL:         DefaultMaxSQL,
		MaxParams:      DefaultMaxParams,
		MaxPrepared:    DefaultMaxPrepared,
		MaxSessions:    DefaultMaxSessions,
		MaxResultBytes: DefaultMaxResultBytes,
		MaxName:        DefaultMaxName,
		Idle:           DefaultIdleTimeout,
		Query:          scheduler.DefaultLimits(),
	}
}

func (l Limits) normalized() Limits {
	d := DefaultLimits()
	if l.MaxPacket < 64 {
		l.MaxPacket = d.MaxPacket
	}
	if l.MaxSQL < 1 {
		l.MaxSQL = d.MaxSQL
	}
	if l.MaxParams < 1 {
		l.MaxParams = d.MaxParams
	}
	if l.MaxPrepared < 1 {
		l.MaxPrepared = d.MaxPrepared
	}
	if l.MaxSessions < 1 {
		l.MaxSessions = d.MaxSessions
	}
	if l.MaxResultBytes < 1 {
		l.MaxResultBytes = d.MaxResultBytes
	}
	if l.MaxName < 1 {
		l.MaxName = d.MaxName
	}
	if l.Idle <= 0 {
		l.Idle = d.Idle
	}
	return l
}

// WriteFrame writes one versioned frame. payload is rejected if it exceeds max.
func WriteFrame(w io.Writer, typ Type, payload []byte, max int) error {
	if max < HeaderSize {
		max = DefaultMaxPacket
	}
	if len(payload) > max {
		return nerr.New(nerr.Protocol, "protocol.WriteFrame", "payload exceeds packet limit")
	}
	var hdr [HeaderSize]byte
	copy(hdr[0:4], Magic)
	encoding.PutU16(hdr[:], 4, Version)
	hdr[6] = byte(typ)
	hdr[7] = 0
	encoding.PutU32(hdr[:], 8, uint32(len(payload)))
	if _, err := w.Write(hdr[:]); err != nil {
		return nerr.Wrap(nerr.IO, "protocol.WriteFrame", "header", err)
	}
	if len(payload) == 0 {
		return nil
	}
	if _, err := w.Write(payload); err != nil {
		return nerr.Wrap(nerr.IO, "protocol.WriteFrame", "payload", err)
	}
	return nil
}

// ReadFrame reads one frame. A length larger than max is rejected without allocating it.
func ReadFrame(r io.Reader, max int) (Type, []byte, error) {
	if max < HeaderSize {
		max = DefaultMaxPacket
	}
	var hdr [HeaderSize]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, nerr.Wrap(nerr.Protocol, "protocol.ReadFrame", "header", err)
	}
	if string(hdr[0:4]) != Magic {
		return 0, nil, nerr.New(nerr.Protocol, "protocol.ReadFrame", "bad magic")
	}
	if encoding.U16(hdr[:], 4) != Version {
		return 0, nil, nerr.New(nerr.Protocol, "protocol.ReadFrame", "unsupported protocol version")
	}
	typ := Type(hdr[6])
	if typ == 0 {
		return 0, nil, nerr.New(nerr.Protocol, "protocol.ReadFrame", "invalid message type")
	}
	n := encoding.U32(hdr[:], 8)
	if n > uint32(max) {
		return 0, nil, nerr.New(nerr.Protocol, "protocol.ReadFrame", "packet exceeds limit")
	}
	if n == 0 {
		return typ, nil, nil
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		return 0, nil, nerr.Wrap(nerr.Protocol, "protocol.ReadFrame", "payload", err)
	}
	return typ, payload, nil
}

func protoErr(msg string) error {
	return nerr.New(nerr.Protocol, "protocol", msg)
}
