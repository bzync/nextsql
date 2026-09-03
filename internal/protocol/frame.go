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

	DefaultMaxPacket              = 1 << 20
	DefaultMaxSQL                 = 1 << 20
	DefaultMaxParams              = 256
	DefaultMaxPrepared            = 64
	DefaultMaxSessions            = 128
	DefaultMaxSessionsPerUser     = 0 // 0 = unlimited
	DefaultMaxSessionsPerDatabase = 0 // 0 = unlimited
	DefaultMaxSessionsPerRealm    = 0 // 0 = unlimited
	DefaultMaxResultBytes         = 64 << 20
	DefaultMaxName                = 256
	DefaultIdleTimeout            = 60 * time.Second
	DefaultBatchRows              = 256
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
	// TypeIdempotentQuery is an additive v1 mutation request carrying a bounded
	// idempotency key followed by the ordinary SQL+parameter payload.
	TypeIdempotentQuery
	// TypeSetReadConsistency is an additive v1 session-control message: it sets
	// the connection's read-consistency mode and bounded-staleness window. The
	// server replies TypeReady (or TypeError then TypeReady).
	TypeSetReadConsistency
	// TypeNodeStatus requests this server node's key-free replication health so
	// a client can route follower reads. The reply is TypeNodeStatusResp then
	// TypeReady.
	TypeNodeStatus
	// TypeNodeStatusResp carries the NodeStatus payload.
	TypeNodeStatusResp
)

// Limits bound every untrusted length on the wire.
type Limits struct {
	MaxPacket   int
	MaxSQL      int
	MaxParams   int
	MaxPrepared int
	MaxSessions int
	// MaxSessionsPerUser caps concurrent authenticated connections held by a
	// single user name. 0 means unlimited, matching other zero-means-uncapped
	// fields in this codebase (e.g. hosting storage caps).
	MaxSessionsPerUser int
	// MaxSessionsPerDatabase and MaxSessionsPerRealm (P27's own last open
	// exit-gate item, closed once selectable multi-database hosting — the
	// M2 track — shipped live routing to more than one database per
	// process) cap concurrent connections resolved to one specific
	// (realm, database) pair, and to one realm across all its databases,
	// respectively. Both 0 (unlimited) by default; a single-database
	// legacy deployment can still set either — the pair collapses to the
	// one pinned database/realm, making it equivalent to (a finer-grained
	// alternative to) MaxSessions there.
	MaxSessionsPerDatabase int
	MaxSessionsPerRealm    int
	MaxResultBytes         int64
	MaxName                int
	Idle                   time.Duration
	Query                  scheduler.Limits
	// TxnTimeout bounds the total wall-clock lifetime of one open transaction
	// (from BEGIN, or the first statement of an implicit autocommit
	// transaction, to COMMIT/ROLLBACK), checked lazily at the start of each
	// statement dispatched inside it. 0 means unbounded (pre-P27 behavior) —
	// unlike Idle/MaxSessions this has no historical non-zero default, so it
	// stays opt-in rather than silently aborting existing long-running
	// transactions (e.g. bulk loads) once an operator upgrades.
	TxnTimeout time.Duration
	// IdleTxn bounds how long a connection may sit with an open transaction
	// and no traffic before its next frame read is force-timed-out and the
	// transaction released. Unlike TxnTimeout (which bounds a transaction's
	// total lifetime and is only checked lazily when the next statement
	// arrives) IdleTxn is enforced by the connection's own socket read
	// deadline, so it actively reclaims a transaction's locks even if the
	// client never sends another statement — the general per-frame Idle
	// deadline does the same thing today, but only at the (typically much
	// longer) Idle bound, which is sized for ordinary connection keep-alive,
	// not for capping how long a transaction may hold locks while idle. 0
	// (the default) applies no distinct bound: an idle transaction is then
	// governed only by Idle, matching pre-P27 behavior.
	IdleTxn time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxPacket:              DefaultMaxPacket,
		MaxSQL:                 DefaultMaxSQL,
		MaxParams:              DefaultMaxParams,
		MaxPrepared:            DefaultMaxPrepared,
		MaxSessions:            DefaultMaxSessions,
		MaxSessionsPerUser:     DefaultMaxSessionsPerUser,
		MaxSessionsPerDatabase: DefaultMaxSessionsPerDatabase,
		MaxSessionsPerRealm:    DefaultMaxSessionsPerRealm,
		MaxResultBytes:         DefaultMaxResultBytes,
		MaxName:                DefaultMaxName,
		Idle:                   DefaultIdleTimeout,
		Query:                  scheduler.DefaultLimits(),
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
	if l.MaxSessionsPerUser < 0 {
		l.MaxSessionsPerUser = 0
	}
	if l.MaxSessionsPerDatabase < 0 {
		l.MaxSessionsPerDatabase = 0
	}
	if l.MaxSessionsPerRealm < 0 {
		l.MaxSessionsPerRealm = 0
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
	if l.TxnTimeout < 0 {
		l.TxnTimeout = 0
	}
	if l.IdleTxn < 0 {
		l.IdleTxn = 0
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
		// A read failure (EOF, connection reset, deadline) means the
		// transport broke, not that the peer sent something invalid — the
		// same distinction WriteFrame's own io.Writer failures already draw
		// with nerr.IO below. Nothing about the frame's contents has been
		// examined yet, so this can never be a protocol violation.
		return 0, nil, nerr.Wrap(nerr.IO, "protocol.ReadFrame", "header", err)
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
		// Same reasoning as the header read above: a broken transport, not a
		// malformed payload (the header was already validated by this point).
		return 0, nil, nerr.Wrap(nerr.IO, "protocol.ReadFrame", "payload", err)
	}
	return typ, payload, nil
}

func protoErr(msg string) error {
	return nerr.New(nerr.Protocol, "protocol", msg)
}
