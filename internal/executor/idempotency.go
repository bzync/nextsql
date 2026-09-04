package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/txn"
)

const (
	idempotencyRetention  = 24 * time.Hour
	idempotentResultMagic = "NSIR"
	idempotentResultV1    = 1
	idempotentMaxRows     = 4096
	idempotentMaxColumns  = 4096
)

// ExecIdempotent executes one retryable mutation and durably fences it by key.
// The mutation and replay result are committed atomically. Keys are scoped to
// database user, retained for 24 hours, and never persisted in plaintext.
func (s *Session) ExecIdempotent(ctx context.Context, key, sql string, params []Param) (*Result, error) {
	if s == nil || s.db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ExecIdempotent", "session is required")
	}
	if len(key) < 1 || len(key) > catalog.MaxTaskIdempotencyBytes {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ExecIdempotent", "idempotency key length is invalid")
	}
	if s.InTxn() {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ExecIdempotent", "cannot run inside an explicit transaction")
	}
	stmt, err := parser.Parse(sql)
	if err != nil {
		return nil, err
	}
	if !idempotentMutation(stmt) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.ExecIdempotent", "idempotency requires INSERT, UPSERT, UPDATE, DELETE, RUN WORKFLOW, or CANCEL TASK")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	scope := idempotencyScope(s.user, key)
	request, err := resultCacheKey(catalog.NormalizeSQL(sql), s.user, params)
	if err != nil {
		return nil, err
	}
	recordKey := catalog.IdempotencyKey(scope)

	// Serialize retention accounting and same-key execution in this process.
	// The catalog key remains the durable cross-restart/cross-replica fence.
	s.db.idempotencyMu.Lock()
	defer s.db.idempotencyMu.Unlock()

	if err := s.start(txn.SnapshotIsolation); err != nil {
		return nil, err
	}
	abort := func(err error) (*Result, error) {
		if s.InTxn() {
			_ = s.abort()
		}
		return nil, err
	}
	tx := s.x.use(s.db.CatTree)
	now := time.Now().UTC()
	existingRaw, lookupErr := s.treeLookup(tx, recordKey)
	existing := false
	if lookupErr == nil {
		record, err := catalog.DecodeIdempotency(existingRaw)
		if err != nil {
			return abort(err)
		}
		if record.ExpiresNS > now.UnixNano() {
			if record.RequestHash != request {
				return abort(nerr.New(nerr.Conflict, "executor.ExecIdempotent", "idempotency key was used for a different request"))
			}
			result, err := decodeIdempotentResult(record.Response)
			if err != nil {
				return abort(err)
			}
			if _, err := s.rollback(); err != nil {
				return nil, err
			}
			result.IdempotentReplay = true
			return result, nil
		}
		existing = true
	} else if !nerr.HasCode(lookupErr, nerr.NotFound) {
		return abort(lookupErr)
	}
	if err := s.pruneIdempotency(tx, recordKey, now.UnixNano()); err != nil {
		return abort(err)
	}

	result, err := s.ExecContext(ctx, sql, params)
	if err != nil {
		return abort(err)
	}
	if !s.InTxn() {
		return nil, nerr.New(nerr.Internal, "executor.ExecIdempotent", "mutation escaped idempotency transaction")
	}
	response, err := encodeIdempotentResult(result)
	if err != nil {
		return abort(err)
	}
	expires := now.Add(idempotencyRetention)
	raw, err := catalog.EncodeIdempotency(catalog.IdempotencyRecord{
		RequestHash: request,
		CreatedNS:   now.UnixNano(),
		ExpiresNS:   expires.UnixNano(),
		Response:    response,
	})
	if err != nil {
		return abort(err)
	}
	if existing {
		err = s.treeUpdate(tx, recordKey, raw)
	} else {
		err = s.treeInsert(tx, recordKey, raw)
	}
	if err != nil {
		return abort(err)
	}
	if _, err := s.commit(); err != nil {
		return nil, err
	}
	return result, nil
}

func idempotentMutation(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case ast.Insert, ast.Upsert, ast.Update, ast.Delete, ast.RunWorkflow, ast.CancelTask:
		return true
	default:
		return false
	}
}

func idempotencyScope(user, key string) [32]byte {
	h := sha256.New()
	// Keep the historical empty row-tenant component in the durable hash so
	// replay records written after row-tenancy removal remain compatible with
	// records written by the immediately preceding release while unbound.
	for _, value := range []string{user, "", key} {
		var size [4]byte
		binary.LittleEndian.PutUint32(size[:], uint32(len(value)))
		_, _ = h.Write(size[:])
		_, _ = h.Write([]byte(value))
	}
	var out [32]byte
	copy(out[:], h.Sum(nil))
	return out
}

func (s *Session) pruneIdempotency(tx interface {
	Range(start, end []byte, fn func(key, value []byte) error) error
}, current []byte, nowNS int64) error {
	start, end := catalog.IdempotencyBounds()
	var expired [][]byte
	active := 0
	err := tx.Range(start, end, func(key, value []byte) error {
		if bytes.Equal(key, current) {
			return nil
		}
		record, err := catalog.DecodeIdempotency(value)
		if err != nil {
			return err
		}
		if record.ExpiresNS <= nowNS {
			expired = append(expired, append([]byte(nil), key...))
			return nil
		}
		active++
		if active >= catalog.MaxIdempotencyRecords {
			return nerr.New(nerr.Exhausted, "executor.ExecIdempotent", "idempotency record capacity reached")
		}
		return nil
	})
	if err != nil {
		return err
	}
	for _, key := range expired {
		if err := s.treeDelete(s.x.use(s.db.CatTree), key); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
	}
	return nil
}

func encodeIdempotentResult(result *Result) ([]byte, error) {
	if result == nil || result.next != nil || result.close != nil || len(result.Rows) > idempotentMaxRows || len(result.Columns) > idempotentMaxColumns {
		return nil, nerr.New(nerr.Exhausted, "executor.encodeIdempotentResult", "idempotent result exceeds limits")
	}
	out := append([]byte(nil), idempotentResultMagic...)
	out = append(out, idempotentResultV1)
	out = binary.LittleEndian.AppendUint64(out, uint64(result.Affected))
	out = binary.LittleEndian.AppendUint16(out, uint16(len(result.Columns)))
	for _, column := range result.Columns {
		if len(column) > 65535 {
			return nil, nerr.New(nerr.Exhausted, "executor.encodeIdempotentResult", "result column name exceeds limit")
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(len(column)))
		out = append(out, column...)
	}
	out = binary.LittleEndian.AppendUint32(out, uint32(len(result.Rows)))
	for _, row := range result.Rows {
		if len(row) > idempotentMaxColumns {
			return nil, nerr.New(nerr.Exhausted, "executor.encodeIdempotentResult", "result row exceeds column limit")
		}
		out = binary.LittleEndian.AppendUint16(out, uint16(len(row)))
		for _, value := range row {
			out = append(out, byte(value.Typ.Kind))
			out = binary.LittleEndian.AppendUint16(out, value.Typ.Precision)
			out = binary.LittleEndian.AppendUint16(out, value.Typ.Scale)
			out = append(out, value.Typ.VecElem)
			if value.Null {
				out = append(out, 1)
				out = binary.LittleEndian.AppendUint32(out, 0)
				continue
			}
			out = append(out, 0)
			raw, err := types.EncodeScalar(value)
			if err != nil {
				return nil, err
			}
			out = binary.LittleEndian.AppendUint32(out, uint32(len(raw)))
			out = append(out, raw...)
			if len(out) > catalog.MaxIdempotencyResponse {
				return nil, nerr.New(nerr.Exhausted, "executor.encodeIdempotentResult", "idempotent result exceeds byte limit")
			}
		}
	}
	if len(out) > catalog.MaxIdempotencyResponse {
		return nil, nerr.New(nerr.Exhausted, "executor.encodeIdempotentResult", "idempotent result exceeds byte limit")
	}
	return out, nil
}

func decodeIdempotentResult(raw []byte) (*Result, error) {
	if len(raw) < 19 || len(raw) > catalog.MaxIdempotencyResponse || !bytes.Equal(raw[:4], []byte(idempotentResultMagic)) || raw[4] != idempotentResultV1 {
		return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid idempotent result header")
	}
	off := 5
	affected := int64(binary.LittleEndian.Uint64(raw[off : off+8]))
	off += 8
	columnCount := int(binary.LittleEndian.Uint16(raw[off : off+2]))
	off += 2
	if columnCount > idempotentMaxColumns {
		return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "idempotent result has too many columns")
	}
	columns := make([]string, columnCount)
	for i := range columns {
		if off+2 > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "truncated result column")
		}
		n := int(binary.LittleEndian.Uint16(raw[off : off+2]))
		off += 2
		if off+n > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "truncated result column name")
		}
		columns[i] = string(raw[off : off+n])
		off += n
	}
	if off+4 > len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "truncated result row count")
	}
	rowCount := int(binary.LittleEndian.Uint32(raw[off : off+4]))
	off += 4
	if rowCount > idempotentMaxRows {
		return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "idempotent result has too many rows")
	}
	rows := make([][]types.Value, rowCount)
	for i := range rows {
		if off+2 > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "truncated result row")
		}
		width := int(binary.LittleEndian.Uint16(raw[off : off+2]))
		off += 2
		if width > idempotentMaxColumns {
			return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "idempotent result row is too wide")
		}
		rows[i] = make([]types.Value, width)
		for j := range rows[i] {
			if off+11 > len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "truncated result value")
			}
			typ := types.Type{
				Kind:      types.Kind(raw[off]),
				Precision: binary.LittleEndian.Uint16(raw[off+1 : off+3]),
				Scale:     binary.LittleEndian.Uint16(raw[off+3 : off+5]),
				VecElem:   raw[off+5],
			}
			null := raw[off+6]
			n := int(binary.LittleEndian.Uint32(raw[off+7 : off+11]))
			off += 11
			if null > 1 || n < 0 || off+n > len(raw) || null == 1 && n != 0 {
				return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid result value")
			}
			// validateIdempotentResultType is the authoritative allow-list for
			// typ.Kind (its default case rejects KindInvalid and anything not
			// listed), so no separate numeric bound check is needed here — the
			// old `typ.Kind > KindPolygon` bound silently rejected every
			// Datatype-expansion Kind (BLOB/INT*/UINT*/DATE/TIME) appended after
			// KindPolygon.
			if err := validateIdempotentResultType(typ); err != nil {
				return nil, err
			}
			if null == 1 {
				rows[i][j] = types.Null(typ)
				continue
			}
			value, consumed, err := types.DecodeScalar(raw[off:off+n], 0, typ)
			if err != nil || consumed != n {
				if err != nil {
					return nil, err
				}
				return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "result value length mismatch")
			}
			rows[i][j] = value
			if value.Typ.Kind == types.KindVector {
				if len(value.Vec) != int(typ.Precision) {
					return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "vector dimension mismatch")
				}
				if err := types.ValidateVector(value.Vec); err != nil {
					return nil, nerr.Wrap(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid vector result", err)
				}
			}
			off += n
		}
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "trailing idempotent result bytes")
	}
	return &Result{Columns: columns, Rows: rows, Affected: affected}, nil
}

func validateIdempotentResultType(typ types.Type) error {
	switch typ.Kind {
	case types.KindDecimal:
		if typ.Precision > 38 || typ.Scale > typ.Precision && typ.Precision != 0 {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid decimal result type")
		}
	case types.KindVector:
		if typ.VecElem != types.VecF32 || typ.Precision < 1 || typ.Precision > types.MaxVectorDim {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid vector result type")
		}
	case types.KindChar, types.KindVarchar:
		if typ.Precision < 1 || typ.Precision > types.MaxCharLen || typ.Scale != 0 || typ.VecElem != 0 {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid char result type")
		}
	case types.KindEnum:
		if len(typ.EnumLabels) < 1 || len(typ.EnumLabels) > types.MaxEnumLabels {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid enum result type")
		}
	case types.KindArray:
		if len(typ.Elem) != 1 {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid array result type")
		}
		if err := validateIdempotentResultType(typ.Elem[0]); err != nil {
			return err
		}
	case types.KindMap:
		if len(typ.Key) != 1 || len(typ.Elem) != 1 {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid map result type")
		}
		if err := validateIdempotentResultType(typ.Key[0]); err != nil {
			return err
		}
		if err := validateIdempotentResultType(typ.Elem[0]); err != nil {
			return err
		}
	case types.KindStruct:
		if len(typ.Fields) == 0 {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid struct result type")
		}
		for _, f := range typ.Fields {
			if err := validateIdempotentResultType(f.Type); err != nil {
				return err
			}
		}
	case types.KindGeometry, types.KindGeography:
		if typ.Scale > uint16(types.GeomSubGeometryCollection) {
			return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid geometry result type")
		}
	case types.KindUUID, types.KindString, types.KindText, types.KindBlob, types.KindTimestampTZ, types.KindJSON, types.KindBool, types.KindNull, types.KindPoint, types.KindBox, types.KindLine, types.KindPolygon,
		types.KindInt8, types.KindInt16, types.KindInt32, types.KindInt64,
		types.KindUint8, types.KindUint16, types.KindUint32, types.KindUint64,
		types.KindDate, types.KindTime, types.KindTimestamp, types.KindFloat32, types.KindFloat64, types.KindInterval:
	default:
		return nerr.New(nerr.InvalidFormat, "executor.decodeIdempotentResult", "invalid result type")
	}
	return nil
}
