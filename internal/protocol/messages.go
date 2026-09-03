package protocol

import (
	"errors"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	FlagCancel uint16 = 1 << 0
)

type Hello struct {
	Version  uint16
	Flags    uint16
	Secret   uint64
	Database string
	User     string
	// Realm is an optional trailing field (M2-2): emitted on the wire only
	// when non-empty, so a Hello with no realm selected is byte-identical to
	// the pre-realm wire shape. See EncodeHello/DecodeHello.
	Realm string
}

type HelloOK struct {
	Version    uint16
	AuthMethod uint8
	Secret     uint64
}

type Auth struct {
	Password string
}

type Query struct {
	SQL    string
	Params []executor.Param
}

type IdempotentQuery struct {
	Key    string
	SQL    string
	Params []executor.Param
}

// ReadConsistency mode bytes on the wire. They match
// replication.ReadConsistency / executor.ReadConsistency ordering.
const (
	ReadModeStrong  uint8 = 0
	ReadModeBounded uint8 = 1
	ReadModeStale   uint8 = 2
)

// SetReadConsistency sets a connection's read-consistency mode. MaxStalenessMS
// applies only to the bounded mode; 0 selects the server's default window.
type SetReadConsistency struct {
	Mode           uint8
	MaxStalenessMS uint64
}

// NodeStatus is a key-free replication health snapshot for follower-read
// routing. Role is "standalone" when the server has no attached cluster.
type NodeStatus struct {
	Role          string
	HasLeader     bool
	Healthy       bool
	AppliedLSN    uint64
	LastContactMS int64
	ApplyBacklog  uint64
}

type PrepareOK struct {
	ID uint32
}

type Execute struct {
	ID     uint32
	Params []executor.Param
}

type CloseStmt struct {
	ID uint32
}

type Column struct {
	Name string
	Type types.Type
}

type RowDesc struct {
	Columns []Column
}

type DataBatch struct {
	Rows [][]types.Value
}

type CommandComplete struct {
	Affected int64
}

type ErrorMsg struct {
	Code    string
	Message string
}

func EncodeHello(h Hello, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	buf := make([]byte, 0, 16+len(h.Database)+len(h.User)+len(h.Realm))
	var hdr [12]byte
	encoding.PutU16(hdr[:], 0, h.Version)
	encoding.PutU16(hdr[:], 2, h.Flags)
	encoding.PutU64(hdr[:], 4, h.Secret)
	buf = append(buf, hdr[:]...)
	var err error
	if buf, err = appendU16String(buf, h.Database, lim.MaxName); err != nil {
		return nil, err
	}
	if buf, err = appendU16String(buf, h.User, lim.MaxName); err != nil {
		return nil, err
	}
	// Realm is emitted only when selected, so an unconfigured client's Hello
	// is byte-identical to the pre-realm wire shape (docs/design-multidatabase-dbaas.md §19 item 7).
	if h.Realm != "" {
		if buf, err = appendU16String(buf, h.Realm, lim.MaxName); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func DecodeHello(b []byte, lim Limits) (Hello, error) {
	lim = lim.normalized()
	if len(b) < 12 {
		return Hello{}, protoErr("truncated hello")
	}
	h := Hello{
		Version: encoding.U16(b, 0),
		Flags:   encoding.U16(b, 2),
		Secret:  encoding.U64(b, 4),
	}
	off := 12
	var err error
	h.Database, off, err = readU16String(b, off, lim.MaxName)
	if err != nil {
		return Hello{}, err
	}
	h.User, off, err = readU16String(b, off, lim.MaxName)
	if err != nil {
		return Hello{}, err
	}
	// Optional trailing field, present only from a client that selected a
	// realm (see EncodeHello). Absent entirely from an old-shape Hello, in
	// which case off == len(b) already and Realm stays "". Mirrors NSCT's
	// V1 tail-sniff (internal/catalog/encode.go).
	if off < len(b) {
		h.Realm, off, err = readU16String(b, off, lim.MaxName)
		if err != nil {
			return Hello{}, err
		}
	}
	if off != len(b) {
		return Hello{}, protoErr("trailing hello bytes")
	}
	return h, nil
}

func EncodeHelloOK(h HelloOK) []byte {
	buf := make([]byte, 11)
	encoding.PutU16(buf, 0, h.Version)
	buf[2] = h.AuthMethod
	encoding.PutU64(buf, 3, h.Secret)
	return buf
}

func DecodeHelloOK(b []byte) (HelloOK, error) {
	if len(b) != 11 {
		return HelloOK{}, protoErr("bad hello-ok length")
	}
	return HelloOK{
		Version:    encoding.U16(b, 0),
		AuthMethod: b[2],
		Secret:     encoding.U64(b, 3),
	}, nil
}

func EncodeAuth(a Auth, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	return appendU16String(nil, a.Password, lim.MaxName)
}

func DecodeAuth(b []byte, lim Limits) (Auth, error) {
	lim = lim.normalized()
	pw, off, err := readU16String(b, 0, lim.MaxName)
	if err != nil {
		return Auth{}, err
	}
	if off != len(b) {
		return Auth{}, protoErr("trailing auth bytes")
	}
	return Auth{Password: pw}, nil
}

func EncodeQuery(q Query, lim Limits) ([]byte, error) {
	return encodeSQLParams(q.SQL, q.Params, lim)
}

func DecodeQuery(b []byte, lim Limits) (Query, error) {
	sql, params, err := decodeSQLParams(b, lim)
	if err != nil {
		return Query{}, err
	}
	return Query{SQL: sql, Params: params}, nil
}

func EncodeIdempotentQuery(q IdempotentQuery, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	buf, err := appendU16String(nil, q.Key, lim.MaxName)
	if err != nil {
		return nil, err
	}
	body, err := encodeSQLParams(q.SQL, q.Params, lim)
	if err != nil {
		return nil, err
	}
	if len(buf)+len(body) > lim.MaxPacket {
		return nil, protoErr("idempotent query exceeds packet limit")
	}
	return append(buf, body...), nil
}

func DecodeIdempotentQuery(b []byte, lim Limits) (IdempotentQuery, error) {
	lim = lim.normalized()
	key, off, err := readU16String(b, 0, lim.MaxName)
	if err != nil {
		return IdempotentQuery{}, err
	}
	if key == "" {
		return IdempotentQuery{}, protoErr("empty idempotency key")
	}
	sql, params, err := decodeSQLParams(b[off:], lim)
	if err != nil {
		return IdempotentQuery{}, err
	}
	return IdempotentQuery{Key: key, SQL: sql, Params: params}, nil
}

func EncodeSetReadConsistency(s SetReadConsistency) []byte {
	buf := make([]byte, 9)
	buf[0] = s.Mode
	encoding.PutU64(buf, 1, s.MaxStalenessMS)
	return buf
}

func DecodeSetReadConsistency(b []byte) (SetReadConsistency, error) {
	if len(b) != 9 {
		return SetReadConsistency{}, protoErr("bad set-read-consistency length")
	}
	m := SetReadConsistency{Mode: b[0], MaxStalenessMS: encoding.U64(b, 1)}
	if m.Mode > ReadModeStale {
		return SetReadConsistency{}, protoErr("unknown read consistency mode")
	}
	return m, nil
}

func EncodeNodeStatus(n NodeStatus, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	buf, err := appendU16String(nil, n.Role, lim.MaxName)
	if err != nil {
		return nil, err
	}
	var flags byte
	if n.HasLeader {
		flags |= 1
	}
	if n.Healthy {
		flags |= 2
	}
	buf = append(buf, flags)
	var tail [24]byte
	encoding.PutU64(tail[:], 0, n.AppliedLSN)
	encoding.PutU64(tail[:], 8, uint64(n.LastContactMS))
	encoding.PutU64(tail[:], 16, n.ApplyBacklog)
	return append(buf, tail[:]...), nil
}

func DecodeNodeStatus(b []byte, lim Limits) (NodeStatus, error) {
	lim = lim.normalized()
	role, off, err := readU16String(b, 0, lim.MaxName)
	if err != nil {
		return NodeStatus{}, err
	}
	if len(b)-off != 25 {
		return NodeStatus{}, protoErr("bad node-status length")
	}
	flags := b[off]
	off++
	return NodeStatus{
		Role:          role,
		HasLeader:     flags&1 != 0,
		Healthy:       flags&2 != 0,
		AppliedLSN:    encoding.U64(b, off),
		LastContactMS: int64(encoding.U64(b, off+8)),
		ApplyBacklog:  encoding.U64(b, off+16),
	}, nil
}

func EncodePrepare(sql string, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	return appendU32Bytes(nil, []byte(sql), lim.MaxSQL)
}

func DecodePrepare(b []byte, lim Limits) (string, error) {
	lim = lim.normalized()
	raw, off, err := readU32Bytes(b, 0, lim.MaxSQL)
	if err != nil {
		return "", err
	}
	if off != len(b) {
		return "", protoErr("trailing prepare bytes")
	}
	return string(raw), nil
}

func EncodePrepareOK(id uint32) []byte {
	buf := make([]byte, 4)
	encoding.PutU32(buf, 0, id)
	return buf
}

func DecodePrepareOK(b []byte) (uint32, error) {
	if len(b) != 4 {
		return 0, protoErr("bad prepare-ok length")
	}
	return encoding.U32(b, 0), nil
}

func EncodeExecute(x Execute, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	buf := make([]byte, 4)
	encoding.PutU32(buf, 0, x.ID)
	rest, err := encodeParams(x.Params, lim)
	if err != nil {
		return nil, err
	}
	return append(buf, rest...), nil
}

func DecodeExecute(b []byte, lim Limits) (Execute, error) {
	if len(b) < 4 {
		return Execute{}, protoErr("truncated execute")
	}
	id := encoding.U32(b, 0)
	params, off, err := decodeParams(b, 4, lim)
	if err != nil {
		return Execute{}, err
	}
	if off != len(b) {
		return Execute{}, protoErr("trailing execute bytes")
	}
	return Execute{ID: id, Params: params}, nil
}

func EncodeCloseStmt(id uint32) []byte {
	return EncodePrepareOK(id)
}

func DecodeCloseStmt(b []byte) (uint32, error) {
	return DecodePrepareOK(b)
}

func EncodeRowDesc(d RowDesc, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	if len(d.Columns) > 4096 {
		return nil, protoErr("too many columns")
	}
	buf := make([]byte, 2)
	encoding.PutU16(buf, 0, uint16(len(d.Columns)))
	var err error
	for _, c := range d.Columns {
		if buf, err = appendU16String(buf, c.Name, lim.MaxName); err != nil {
			return nil, err
		}
		buf = appendType(buf, c.Type)
	}
	return buf, nil
}

func DecodeRowDesc(b []byte, lim Limits) (RowDesc, error) {
	lim = lim.normalized()
	n, err := encoding.ReadU16(b, 0)
	if err != nil {
		return RowDesc{}, protoErr("truncated row description")
	}
	if int(n) > 4096 {
		return RowDesc{}, protoErr("too many columns")
	}
	off := 2
	cols := make([]Column, 0, n)
	for i := 0; i < int(n); i++ {
		var name string
		name, off, err = readU16String(b, off, lim.MaxName)
		if err != nil {
			return RowDesc{}, err
		}
		var typ types.Type
		typ, off, err = readType(b, off)
		if err != nil {
			return RowDesc{}, err
		}
		cols = append(cols, Column{Name: name, Type: typ})
	}
	if off != len(b) {
		return RowDesc{}, protoErr("trailing row description")
	}
	return RowDesc{Columns: cols}, nil
}

func EncodeDataBatch(batch DataBatch, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	if len(batch.Rows) > 0xFFFF {
		return nil, protoErr("batch too large")
	}
	buf := make([]byte, 4)
	encoding.PutU32(buf, 0, uint32(len(batch.Rows)))
	for _, row := range batch.Rows {
		if len(row) > 4096 {
			return nil, protoErr("too many columns")
		}
		var n [2]byte
		encoding.PutU16(n[:], 0, uint16(len(row)))
		buf = append(buf, n[:]...)
		for _, v := range row {
			var err error
			buf, err = appendValue(buf, v, lim.MaxPacket)
			if err != nil {
				return nil, err
			}
		}
	}
	if len(buf) > lim.MaxPacket {
		return nil, nerr.New(nerr.Protocol, "protocol.EncodeDataBatch", "batch exceeds packet limit")
	}
	return buf, nil
}

func DecodeDataBatch(b []byte, lim Limits) (DataBatch, error) {
	lim = lim.normalized()
	nrows, err := encoding.ReadU32(b, 0)
	if err != nil {
		return DataBatch{}, protoErr("truncated data batch")
	}
	if nrows > uint32(lim.MaxPacket) {
		return DataBatch{}, protoErr("too many rows")
	}
	off := 4
	rows := make([][]types.Value, 0, nrows)
	for i := uint32(0); i < nrows; i++ {
		ncols, err := encoding.ReadU16(b, off)
		if err != nil {
			return DataBatch{}, protoErr("truncated row")
		}
		if int(ncols) > 4096 {
			return DataBatch{}, protoErr("too many columns")
		}
		off += 2
		row := make([]types.Value, 0, ncols)
		for j := 0; j < int(ncols); j++ {
			v, next, err := readValue(b, off, lim.MaxPacket)
			if err != nil {
				return DataBatch{}, err
			}
			off = next
			row = append(row, v)
		}
		rows = append(rows, row)
	}
	if off != len(b) {
		return DataBatch{}, protoErr("trailing data batch")
	}
	return DataBatch{Rows: rows}, nil
}

func EncodeCommandComplete(c CommandComplete) []byte {
	buf := make([]byte, 8)
	encoding.PutU64(buf, 0, uint64(c.Affected))
	return buf
}

func DecodeCommandComplete(b []byte) (CommandComplete, error) {
	if len(b) != 8 {
		return CommandComplete{}, protoErr("bad command-complete length")
	}
	return CommandComplete{Affected: int64(encoding.U64(b, 0))}, nil
}

func EncodeError(e ErrorMsg, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	buf, err := appendU16String(nil, e.Code, lim.MaxName)
	if err != nil {
		return nil, err
	}
	return appendU16String(buf, e.Message, lim.MaxName)
}

func DecodeError(b []byte, lim Limits) (ErrorMsg, error) {
	lim = lim.normalized()
	code, off, err := readU16String(b, 0, lim.MaxName)
	if err != nil {
		return ErrorMsg{}, err
	}
	msg, off, err := readU16String(b, off, lim.MaxName)
	if err != nil {
		return ErrorMsg{}, err
	}
	if off != len(b) {
		return ErrorMsg{}, protoErr("trailing error")
	}
	return ErrorMsg{Code: code, Message: msg}, nil
}

func encodeSQLParams(sql string, params []executor.Param, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	buf, err := appendU32Bytes(nil, []byte(sql), lim.MaxSQL)
	if err != nil {
		return nil, err
	}
	rest, err := encodeParams(params, lim)
	if err != nil {
		return nil, err
	}
	return append(buf, rest...), nil
}

func decodeSQLParams(b []byte, lim Limits) (string, []executor.Param, error) {
	lim = lim.normalized()
	raw, off, err := readU32Bytes(b, 0, lim.MaxSQL)
	if err != nil {
		return "", nil, err
	}
	params, off, err := decodeParams(b, off, lim)
	if err != nil {
		return "", nil, err
	}
	if off != len(b) {
		return "", nil, protoErr("trailing query bytes")
	}
	return string(raw), params, nil
}

func encodeParams(params []executor.Param, lim Limits) ([]byte, error) {
	lim = lim.normalized()
	if len(params) > lim.MaxParams {
		return nil, nerr.New(nerr.Protocol, "protocol", "too many parameters")
	}
	buf := make([]byte, 2)
	encoding.PutU16(buf, 0, uint16(len(params)))
	for _, p := range params {
		var err error
		if buf, err = appendU16String(buf, p.Name, lim.MaxName); err != nil {
			return nil, err
		}
		if buf, err = appendValue(buf, p.Value, lim.MaxPacket); err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func decodeParams(b []byte, off int, lim Limits) ([]executor.Param, int, error) {
	lim = lim.normalized()
	n, err := encoding.ReadU16(b, off)
	if err != nil {
		return nil, 0, protoErr("truncated parameter count")
	}
	if int(n) > lim.MaxParams {
		return nil, 0, protoErr("too many parameters")
	}
	off += 2
	out := make([]executor.Param, 0, n)
	for i := 0; i < int(n); i++ {
		var name string
		name, off, err = readU16String(b, off, lim.MaxName)
		if err != nil {
			return nil, 0, err
		}
		v, next, err := readValue(b, off, lim.MaxPacket)
		if err != nil {
			return nil, 0, err
		}
		off = next
		out = append(out, executor.Param{Name: name, Value: v})
	}
	return out, off, nil
}

func errorFrom(err error) ErrorMsg {
	code := string(nerr.Internal)
	msg := "internal error"
	var e *nerr.Error
	if errors.As(err, &e) && e != nil {
		code = string(e.Code)
		msg = e.Message
		if msg == "" {
			msg = e.Error()
		}
	} else if err != nil {
		msg = err.Error()
	}
	if len(msg) > DefaultMaxName {
		msg = msg[:DefaultMaxName]
	}
	return ErrorMsg{Code: code, Message: msg}
}
