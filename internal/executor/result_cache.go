package executor

import (
	"crypto/sha256"
	"encoding/binary"
	"sync"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	resultCacheMaxEntries = 128
	resultCacheMaxBytes   = 8 << 20
	resultCacheMaxRows    = 4096
	resultCacheMaxEntry   = 1 << 20
)

type resultVersion struct {
	lsn format.LSN
	cat uint64
}

type resultCacheEntry struct {
	version resultVersion
	result  *Result
	bytes   int
}

type resultCache struct {
	mu     sync.Mutex
	items  map[[32]byte]resultCacheEntry
	order  [][32]byte
	bytes  int
	hits   uint64
	misses uint64
}

// ResultCacheStats is a bounded-cache snapshot suitable for diagnostics.
type ResultCacheStats struct {
	Entries int
	Bytes   int
	Hits    uint64
	Misses  uint64
}

func newResultCache() *resultCache {
	return &resultCache{items: make(map[[32]byte]resultCacheEntry)}
}

func (c *resultCache) get(key [32]byte, version resultVersion) (*Result, bool) {
	if c == nil {
		return nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.items[key]
	if !ok || entry.version != version {
		c.misses++
		return nil, false
	}
	c.hits++
	result := cloneResult(entry.result)
	result.Cached = true
	return result, true
}

func (c *resultCache) put(key [32]byte, version resultVersion, result *Result) {
	if c == nil || result == nil || result.next != nil || result.close != nil || len(result.Rows) > resultCacheMaxRows {
		return
	}
	bytes := resultSize(result)
	if bytes > resultCacheMaxEntry || bytes > resultCacheMaxBytes {
		return
	}
	entry := resultCacheEntry{version: version, result: cloneResult(result), bytes: bytes}
	c.mu.Lock()
	defer c.mu.Unlock()
	if old, ok := c.items[key]; ok {
		c.bytes -= old.bytes
		for i, queued := range c.order {
			if queued == key {
				copy(c.order[i:], c.order[i+1:])
				c.order = c.order[:len(c.order)-1]
				break
			}
		}
	}
	for len(c.order) >= resultCacheMaxEntries || c.bytes+bytes > resultCacheMaxBytes {
		oldest := c.order[0]
		c.order = c.order[1:]
		c.bytes -= c.items[oldest].bytes
		delete(c.items, oldest)
	}
	c.items[key] = entry
	c.order = append(c.order, key)
	c.bytes += bytes
}

func (c *resultCache) stats() ResultCacheStats {
	if c == nil {
		return ResultCacheStats{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return ResultCacheStats{Entries: len(c.items), Bytes: c.bytes, Hits: c.hits, Misses: c.misses}
}

func cloneResult(in *Result) *Result {
	if in == nil {
		return nil
	}
	out := &Result{
		Columns:          append([]string(nil), in.Columns...),
		Rows:             make([][]types.Value, len(in.Rows)),
		Affected:         in.Affected,
		Cached:           in.Cached,
		IdempotentReplay: in.IdempotentReplay,
	}
	for i, row := range in.Rows {
		out.Rows[i] = make([]types.Value, len(row))
		for j, value := range row {
			out.Rows[i][j] = value.Clone()
		}
	}
	return out
}

func resultSize(result *Result) int {
	size := 32
	for _, column := range result.Columns {
		size += len(column) + 16
	}
	for _, row := range result.Rows {
		size += 24 + len(row)*32
		for _, value := range row {
			size += len(value.Str) + len(value.JSON) + len(value.Vec)*4 + len(value.Coords)*8 + len(value.Rings)*8
			if value.Typ.Kind == types.KindDecimal && value.Dec.Coef != nil {
				size += len(value.Dec.Coef.Bytes())
			}
		}
	}
	return size
}

func resultCacheKey(sql, user string, params []Param) ([32]byte, error) {
	h := sha256.New()
	writeHashPart := func(part []byte) {
		var size [8]byte
		binary.LittleEndian.PutUint64(size[:], uint64(len(part)))
		_, _ = h.Write(size[:])
		_, _ = h.Write(part)
	}
	writeHashPart([]byte(sql))
	writeHashPart([]byte(user))
	for _, param := range params {
		writeHashPart([]byte(param.Name))
		var typ [7]byte
		typ[0] = byte(param.Value.Typ.Kind)
		binary.LittleEndian.PutUint16(typ[1:3], param.Value.Typ.Precision)
		binary.LittleEndian.PutUint16(typ[3:5], param.Value.Typ.Scale)
		typ[5] = param.Value.Typ.VecElem
		if param.Value.Null {
			typ[6] = 1
		}
		writeHashPart(typ[:])
		if !param.Value.Null {
			raw, err := types.EncodeScalar(param.Value)
			if err != nil {
				return [32]byte{}, err
			}
			writeHashPart(raw)
		}
	}
	var key [32]byte
	copy(key[:], h.Sum(nil))
	return key, nil
}

func resultCacheable(stmt ast.Stmt) bool {
	switch q := stmt.(type) {
	case ast.Select:
		return !selectIsVolatile(q)
	case ast.SetOperation:
		return resultCacheable(q.Left) && resultCacheable(q.Right)
	case ast.With:
		for _, cte := range q.CTEs {
			if !resultCacheable(cte.Query) {
				return false
			}
		}
		return resultCacheable(q.Query)
	default:
		return false
	}
}

func selectIsVolatile(q ast.Select) bool {
	for _, item := range q.List {
		if exprIsVolatile(item.Expr) {
			return true
		}
	}
	for _, expr := range q.Group {
		if exprIsVolatile(expr) {
			return true
		}
	}
	for _, item := range q.Order {
		if exprIsVolatile(item.Expr) {
			return true
		}
	}
	for _, join := range q.Joins {
		if exprIsVolatile(join.On) {
			return true
		}
	}
	for _, expr := range []ast.Expr{q.Where, q.Having, q.SearchQuery, q.NearestQuery} {
		if exprIsVolatile(expr) {
			return true
		}
	}
	return q.FromQuery != nil && !resultCacheable(q.FromQuery)
}

func exprIsVolatile(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	switch x := expr.(type) {
	case ast.Call:
		if x.Name == "uuid" || x.Name == "now" || x.Name == "ai" {
			return true
		}
		for _, arg := range x.Args {
			if exprIsVolatile(arg) {
				return true
			}
		}
	case ast.Window:
		if exprIsVolatile(x.Fn) {
			return true
		}
		for _, expr := range x.Partition {
			if exprIsVolatile(expr) {
				return true
			}
		}
		for _, item := range x.Order {
			if exprIsVolatile(item.Expr) {
				return true
			}
		}
		if x.Frame != nil && (exprIsVolatile(x.Frame.Start.Offset) || exprIsVolatile(x.Frame.End.Offset)) {
			return true
		}
	case ast.Unary:
		return exprIsVolatile(x.Right)
	case ast.Binary:
		return exprIsVolatile(x.Left) || exprIsVolatile(x.Right)
	case ast.Between:
		return exprIsVolatile(x.Expr) || exprIsVolatile(x.Low) || exprIsVolatile(x.High)
	case ast.IsNull:
		return exprIsVolatile(x.Expr)
	case ast.Case:
		if exprIsVolatile(x.Operand) || exprIsVolatile(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if exprIsVolatile(arm.When) || exprIsVolatile(arm.Then) {
				return true
			}
		}
	case ast.ScalarSubquery:
		return !resultCacheable(x.Query)
	case ast.InSubquery:
		return exprIsVolatile(x.Expr) || !resultCacheable(x.Query)
	case ast.ExistsSubquery:
		return !resultCacheable(x.Query)
	}
	return false
}
