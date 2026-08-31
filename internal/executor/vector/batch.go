package vector

import (
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Batch is a columnar chunk of rows.
type Batch struct {
	Types   []types.Type
	Columns []Vector
	Count   int
	cap     int
}

// Vector is one typed column inside a Batch.
type Vector struct {
	Typ    types.Type
	Null   []bool
	UUID   [][16]byte
	Str    []string
	Dec    []types.Decimal
	Time   []int64
	JSON      [][]byte
	Vec       [][]float32
	VecRef    []bool
	SparseIdx [][]uint32
	SparseVal [][]float32
	Bool      []bool
	Lon    []float64
	Lat    []float64
	Box    [][4]float64
	Coords [][]float64
	Rings  [][]int
}

// New allocates a batch with capacity snapped to a supported size.
func New(cols []types.Type, capacity int) *Batch {
	capacity = scheduler.NormalizeBatch(capacity)
	b := &Batch{
		Types:   append([]types.Type(nil), cols...),
		Columns: make([]Vector, len(cols)),
		cap:     capacity,
	}
	for i, t := range cols {
		b.Columns[i] = newVec(t, capacity)
	}
	return b
}

func newVec(t types.Type, n int) Vector {
	v := Vector{Typ: t, Null: make([]bool, n)}
	switch t.Kind {
	case types.KindUUID:
		v.UUID = make([][16]byte, n)
	case types.KindString, types.KindText:
		v.Str = make([]string, n)
	case types.KindDecimal:
		v.Dec = make([]types.Decimal, n)
	case types.KindTimestampTZ:
		v.Time = make([]int64, n)
	case types.KindJSON:
		v.JSON = make([][]byte, n)
	case types.KindVector:
		v.Vec = make([][]float32, n)
		v.VecRef = make([]bool, n)
		if t.VecElem == types.VecSparse {
			v.SparseIdx = make([][]uint32, n)
			v.SparseVal = make([][]float32, n)
		}
	case types.KindBool:
		v.Bool = make([]bool, n)
	case types.KindPoint:
		v.Lon = make([]float64, n)
		v.Lat = make([]float64, n)
	case types.KindBox:
		v.Box = make([][4]float64, n)
	case types.KindLine, types.KindPolygon:
		v.Coords = make([][]float64, n)
		v.Rings = make([][]int, n)
	}
	return v
}

func (b *Batch) Cap() int {
	if b == nil {
		return 0
	}
	return b.cap
}

func (b *Batch) Full() bool {
	return b == nil || b.Count >= b.cap
}

func (b *Batch) Reset() {
	if b == nil {
		return
	}
	b.Count = 0
}

func (b *Batch) ApproxBytes() int64 {
	if b == nil {
		return 0
	}
	n := int64(b.Count) * 24 * int64(len(b.Columns)+1)
	for i := range b.Columns {
		col := &b.Columns[i]
		for j := 0; j < b.Count; j++ {
			if col.Null[j] {
				continue
			}
			switch col.Typ.Kind {
			case types.KindString, types.KindText:
				n += int64(len(col.Str[j]))
			case types.KindJSON:
				n += int64(len(col.JSON[j]))
			case types.KindVector:
				if col.Typ.VecElem == types.VecSparse && j < len(col.SparseIdx) {
					n += int64(len(col.SparseIdx[j]) * 8)
				} else {
					n += int64(len(col.Vec[j]) * 4)
				}
			case types.KindLine, types.KindPolygon:
				n += int64(len(col.Coords[j]) * 8)
			}
		}
	}
	return n
}

// AppendRow copies one row. Returns false if the batch is full.
func (b *Batch) AppendRow(row []types.Value) bool {
	if b.Full() {
		return false
	}
	i := b.Count
	for c := range b.Columns {
		var v types.Value
		if c < len(row) {
			v = row[c]
		} else {
			v = types.Null(b.Types[c])
		}
		setAt(&b.Columns[c], i, v)
	}
	b.Count++
	return true
}

func setAt(col *Vector, i int, v types.Value) {
	if v.Null {
		col.Null[i] = true
		return
	}
	col.Null[i] = false
	switch col.Typ.Kind {
	case types.KindUUID:
		col.UUID[i] = v.UUID
	case types.KindString, types.KindText:
		col.Str[i] = v.Str
	case types.KindDecimal:
		col.Dec[i] = v.Dec
	case types.KindTimestampTZ:
		col.Time[i] = v.Time
	case types.KindJSON:
		col.JSON[i] = v.JSON
	case types.KindVector:
		if col.VecRef == nil {
			col.VecRef = make([]bool, len(col.Null))
		}
		col.VecRef[i] = v.VecRef
		if v.Typ.VecElem == types.VecSparse || len(v.SparseIdx) > 0 {
			if col.SparseIdx == nil {
				col.SparseIdx = make([][]uint32, len(col.Null))
				col.SparseVal = make([][]float32, len(col.Null))
			}
			col.SparseIdx[i] = v.SparseIdx
			col.SparseVal[i] = v.SparseVal
		} else {
			col.Vec[i] = v.Vec
		}
	case types.KindBool:
		col.Bool[i] = v.Bool
	case types.KindPoint:
		col.Lon[i] = v.Lon
		col.Lat[i] = v.Lat
	case types.KindBox:
		col.Box[i] = v.Box
	case types.KindLine, types.KindPolygon:
		col.Coords[i] = v.Coords
		col.Rings[i] = v.Rings
	}
}

// Row reconstructs row i. The returned Values must not be retained across Reset.
func (b *Batch) Row(i int) []types.Value {
	out := make([]types.Value, len(b.Columns))
	b.FillRow(i, out)
	return out
}

func (b *Batch) FillRow(i int, dst []types.Value) {
	for c := range b.Columns {
		if c >= len(dst) {
			return
		}
		dst[c] = getAt(&b.Columns[c], i)
	}
}

func getAt(col *Vector, i int) types.Value {
	if col.Null[i] {
		return types.Null(col.Typ)
	}
	switch col.Typ.Kind {
	case types.KindUUID:
		return types.UUIDValue(col.UUID[i])
	case types.KindString:
		return types.StringValue(col.Str[i])
	case types.KindText:
		return types.TextValue(col.Str[i])
	case types.KindDecimal:
		return types.DecimalValue(col.Dec[i], col.Typ)
	case types.KindTimestampTZ:
		return types.TimeValue(col.Time[i])
	case types.KindJSON:
		return types.JSONValue(col.JSON[i])
	case types.KindVector:
		if col.VecRef != nil && col.VecRef[i] {
			return types.VectorRef(col.Typ)
		}
		if col.Typ.VecElem == types.VecSparse || (i < len(col.SparseIdx) && col.SparseIdx[i] != nil) {
			return types.SparseValue(col.SparseIdx[i], col.SparseVal[i], col.Typ)
		}
		return types.VectorValue(col.Vec[i], col.Typ)
	case types.KindBool:
		return types.BoolValue(col.Bool[i])
	case types.KindPoint:
		v, _ := types.PointValue(col.Lon[i], col.Lat[i])
		return v
	case types.KindBox:
		bx := col.Box[i]
		v, _ := types.BoxValue(bx[0], bx[1], bx[2], bx[3])
		return v
	case types.KindLine:
		v, _ := types.LineValue(col.Coords[i])
		return v
	case types.KindPolygon:
		v, _ := types.PolygonValue(col.Coords[i], col.Rings[i])
		return v
	default:
		return types.Null(col.Typ)
	}
}

// Compact keeps only indices in sel, in order.
func (b *Batch) Compact(sel []int) {
	if b == nil {
		return
	}
	if len(sel) == 0 {
		b.Count = 0
		return
	}
	if len(sel) == b.Count {
		return
	}
	for c := range b.Columns {
		col := &b.Columns[c]
		for d, s := range sel {
			if s == d {
				continue
			}
			col.Null[d] = col.Null[s]
			switch col.Typ.Kind {
			case types.KindUUID:
				col.UUID[d] = col.UUID[s]
			case types.KindString, types.KindText:
				col.Str[d] = col.Str[s]
			case types.KindDecimal:
				col.Dec[d] = col.Dec[s]
			case types.KindTimestampTZ:
				col.Time[d] = col.Time[s]
			case types.KindJSON:
				col.JSON[d] = col.JSON[s]
			case types.KindVector:
				col.Vec[d] = col.Vec[s]
				if col.VecRef != nil {
					col.VecRef[d] = col.VecRef[s]
				}
				if col.SparseIdx != nil {
					col.SparseIdx[d] = col.SparseIdx[s]
					col.SparseVal[d] = col.SparseVal[s]
				}
			case types.KindBool:
				col.Bool[d] = col.Bool[s]
			case types.KindPoint:
				col.Lon[d] = col.Lon[s]
				col.Lat[d] = col.Lat[s]
			case types.KindBox:
				col.Box[d] = col.Box[s]
			case types.KindLine, types.KindPolygon:
				col.Coords[d] = col.Coords[s]
				col.Rings[d] = col.Rings[s]
			}
		}
	}
	b.Count = len(sel)
}

// Project returns a batch with only the listed column ordinals.
func (b *Batch) Project(ords []int) *Batch {
	cols := make([]types.Type, len(ords))
	for i, o := range ords {
		cols[i] = b.Types[o]
	}
	out := New(cols, b.cap)
	out.Count = b.Count
	for i, o := range ords {
		out.Columns[i] = clonePrefix(b.Columns[o], b.Count, b.cap)
	}
	return out
}

func clonePrefix(src Vector, n, cap int) Vector {
	dst := newVec(src.Typ, cap)
	copy(dst.Null, src.Null[:n])
	switch src.Typ.Kind {
	case types.KindUUID:
		copy(dst.UUID, src.UUID[:n])
	case types.KindString, types.KindText:
		copy(dst.Str, src.Str[:n])
	case types.KindDecimal:
		copy(dst.Dec, src.Dec[:n])
	case types.KindTimestampTZ:
		copy(dst.Time, src.Time[:n])
	case types.KindJSON:
		copy(dst.JSON, src.JSON[:n])
	case types.KindVector:
		copy(dst.Vec, src.Vec[:n])
		if src.VecRef != nil {
			if dst.VecRef == nil {
				dst.VecRef = make([]bool, cap)
			}
			copy(dst.VecRef, src.VecRef[:n])
		}
		if src.SparseIdx != nil {
			if dst.SparseIdx == nil {
				dst.SparseIdx = make([][]uint32, cap)
				dst.SparseVal = make([][]float32, cap)
			}
			copy(dst.SparseIdx, src.SparseIdx[:n])
			copy(dst.SparseVal, src.SparseVal[:n])
		}
	case types.KindBool:
		copy(dst.Bool, src.Bool[:n])
	case types.KindPoint:
		copy(dst.Lon, src.Lon[:n])
		copy(dst.Lat, src.Lat[:n])
	case types.KindBox:
		copy(dst.Box, src.Box[:n])
	case types.KindLine, types.KindPolygon:
		copy(dst.Coords, src.Coords[:n])
		copy(dst.Rings, src.Rings[:n])
	}
	return dst
}

// Concat horizontal-appends right onto left. Counts must match.
func Concat(left, right *Batch) *Batch {
	cols := append(append([]types.Type(nil), left.Types...), right.Types...)
	out := New(cols, left.cap)
	n := left.Count
	if right.Count < n {
		n = right.Count
	}
	out.Count = n
	for i := range left.Columns {
		out.Columns[i] = clonePrefix(left.Columns[i], n, left.cap)
	}
	for i := range right.Columns {
		out.Columns[len(left.Columns)+i] = clonePrefix(right.Columns[i], n, left.cap)
	}
	return out
}

func (b *Batch) Rows() [][]types.Value {
	out := make([][]types.Value, b.Count)
	for i := 0; i < b.Count; i++ {
		out[i] = b.Row(i)
	}
	return out
}
