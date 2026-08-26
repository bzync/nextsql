package catalog

import (
	"bytes"
	"math"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	statsMagic   = "NSST"
	statsVersion = 2
)

// TableStats is a deterministic snapshot of table / index / segment statistics.
type TableStats struct {
	Table    string
	TableID  uint32
	Rows     uint64
	Columns  []ColumnStats
	Indexes  []IndexStats
	Segments []SegmentStats
	Vectors  []VectorStats
}

// VectorStats is ANALYZE output for one VECTOR column (and its HNSW index, if any).
type VectorStats struct {
	Ord         int
	Count       uint64
	Dim         uint16
	IndexName   string
	M           uint16
	EfConstruct uint16
}

type ColumnStats struct {
	Ord         int
	Nulls       uint64
	NDV         uint64
	Min         types.Value
	Max         types.Value
	HasMinMax   bool
	Histogram   []HistBucket
	MCV         []MCV
	Correlation float64
}

type HistBucket struct {
	Lower types.Value
	Upper types.Value
	Count uint64
	NDV   uint64
}

type MCV struct {
	Value types.Value
	Freq  uint64
}

type IndexStats struct {
	Name        string
	Selectivity float64
	NDV         uint64
	Unique      bool
}

type SegmentStats struct {
	ID        int
	Rows      uint64
	LowPK     []types.Value
	HighPK    []types.Value
	ColMin    []types.Value
	ColMax    []types.Value
	HasBounds bool
}

func (s *TableStats) Clone() *TableStats {
	if s == nil {
		return nil
	}
	c := *s
	c.Columns = append([]ColumnStats(nil), s.Columns...)
	for i := range c.Columns {
		c.Columns[i].Min = s.Columns[i].Min.Clone()
		c.Columns[i].Max = s.Columns[i].Max.Clone()
		c.Columns[i].Histogram = append([]HistBucket(nil), s.Columns[i].Histogram...)
		for j := range c.Columns[i].Histogram {
			c.Columns[i].Histogram[j].Lower = s.Columns[i].Histogram[j].Lower.Clone()
			c.Columns[i].Histogram[j].Upper = s.Columns[i].Histogram[j].Upper.Clone()
		}
		c.Columns[i].MCV = append([]MCV(nil), s.Columns[i].MCV...)
		for j := range c.Columns[i].MCV {
			c.Columns[i].MCV[j].Value = s.Columns[i].MCV[j].Value.Clone()
		}
	}
	c.Indexes = append([]IndexStats(nil), s.Indexes...)
	c.Vectors = append([]VectorStats(nil), s.Vectors...)
	c.Segments = append([]SegmentStats(nil), s.Segments...)
	for i := range c.Segments {
		c.Segments[i].LowPK = cloneVals(s.Segments[i].LowPK)
		c.Segments[i].HighPK = cloneVals(s.Segments[i].HighPK)
		c.Segments[i].ColMin = cloneVals(s.Segments[i].ColMin)
		c.Segments[i].ColMax = cloneVals(s.Segments[i].ColMax)
	}
	return &c
}

func (s *TableStats) Column(ord int) (ColumnStats, bool) {
	if s == nil {
		return ColumnStats{}, false
	}
	for _, c := range s.Columns {
		if c.Ord == ord {
			return c, true
		}
	}
	return ColumnStats{}, false
}

func (s *TableStats) Vector(ord int) (VectorStats, bool) {
	if s == nil {
		return VectorStats{}, false
	}
	for _, v := range s.Vectors {
		if v.Ord == ord {
			return v, true
		}
	}
	return VectorStats{}, false
}

func (s *TableStats) Index(name string) (IndexStats, bool) {
	if s == nil {
		return IndexStats{}, false
	}
	for _, idx := range s.Indexes {
		if idx.Name == name {
			return idx, true
		}
	}
	return IndexStats{}, false
}

func cloneVals(in []types.Value) []types.Value {
	if in == nil {
		return nil
	}
	out := make([]types.Value, len(in))
	for i := range in {
		out[i] = in[i].Clone()
	}
	return out
}

func EncodeStats(s *TableStats) ([]byte, error) {
	if s == nil {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeStats", "nil stats")
	}
	var buf []byte
	buf = append(buf, statsMagic...)
	buf = appendU16(buf, statsVersion)
	buf = appendString(buf, s.Table)
	buf = appendU32(buf, s.TableID)
	buf = appendU64(buf, s.Rows)
	buf = appendU16(buf, uint16(len(s.Columns)))
	for _, c := range s.Columns {
		var err error
		buf, err = appendColStats(buf, c)
		if err != nil {
			return nil, err
		}
	}
	buf = appendU16(buf, uint16(len(s.Indexes)))
	for _, idx := range s.Indexes {
		buf = appendIndexStats(buf, idx)
	}
	buf = appendU16(buf, uint16(len(s.Segments)))
	for _, seg := range s.Segments {
		var err error
		buf, err = appendSegStats(buf, seg)
		if err != nil {
			return nil, err
		}
	}
	buf = appendU16(buf, uint16(len(s.Vectors)))
	for _, v := range s.Vectors {
		buf = appendVecStats(buf, v)
	}
	return buf, nil
}

func DecodeStats(raw []byte) (*TableStats, error) {
	if len(raw) < 4 || !bytes.Equal(raw[:4], []byte(statsMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeStats", "bad stats magic")
	}
	off := 4
	ver, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if ver != 1 && ver != statsVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeStats", "unsupported stats version")
	}
	s := &TableStats{}
	s.Table, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	s.TableID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	s.Rows, off, err = takeU64(raw, off)
	if err != nil {
		return nil, err
	}
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var c ColumnStats
		c, off, err = takeColStats(raw, off)
		if err != nil {
			return nil, err
		}
		s.Columns = append(s.Columns, c)
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var idx IndexStats
		idx, off, err = takeIndexStats(raw, off)
		if err != nil {
			return nil, err
		}
		s.Indexes = append(s.Indexes, idx)
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var seg SegmentStats
		seg, off, err = takeSegStats(raw, off)
		if err != nil {
			return nil, err
		}
		s.Segments = append(s.Segments, seg)
	}
	if ver >= 2 {
		n, off, err = takeU16(raw, off)
		if err != nil {
			return nil, err
		}
		for i := 0; i < int(n); i++ {
			var v VectorStats
			v, off, err = takeVecStats(raw, off)
			if err != nil {
				return nil, err
			}
			s.Vectors = append(s.Vectors, v)
		}
	}
	return s, nil
}

func appendColStats(buf []byte, c ColumnStats) ([]byte, error) {
	buf = appendU16(buf, uint16(c.Ord))
	buf = appendU64(buf, c.Nulls)
	buf = appendU64(buf, c.NDV)
	if c.HasMinMax {
		buf = append(buf, 1)
		var err error
		buf, err = appendValue(buf, c.Min)
		if err != nil {
			return nil, err
		}
		buf, err = appendValue(buf, c.Max)
		if err != nil {
			return nil, err
		}
	} else {
		buf = append(buf, 0)
	}
	buf = appendU16(buf, uint16(len(c.Histogram)))
	for _, b := range c.Histogram {
		var err error
		buf, err = appendValue(buf, b.Lower)
		if err != nil {
			return nil, err
		}
		buf, err = appendValue(buf, b.Upper)
		if err != nil {
			return nil, err
		}
		buf = appendU64(buf, b.Count)
		buf = appendU64(buf, b.NDV)
	}
	buf = appendU16(buf, uint16(len(c.MCV)))
	for _, m := range c.MCV {
		var err error
		buf, err = appendValue(buf, m.Value)
		if err != nil {
			return nil, err
		}
		buf = appendU64(buf, m.Freq)
	}
	var bits [8]byte
	encoding.PutU64(bits[:], 0, math.Float64bits(c.Correlation))
	buf = append(buf, bits[:]...)
	return buf, nil
}

func takeColStats(raw []byte, off int) (ColumnStats, int, error) {
	var c ColumnStats
	ord, off, err := takeU16(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	c.Ord = int(ord)
	c.Nulls, off, err = takeU64(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	c.NDV, off, err = takeU64(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	if off >= len(raw) {
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeColStats", "truncated minmax")
	}
	if raw[off] != 0 {
		c.HasMinMax = true
		off++
		c.Min, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		c.Max, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
	} else {
		off++
	}
	n, off, err := takeU16(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	for i := 0; i < int(n); i++ {
		var b HistBucket
		b.Lower, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		b.Upper, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		b.Count, off, err = takeU64(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		b.NDV, off, err = takeU64(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		c.Histogram = append(c.Histogram, b)
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	for i := 0; i < int(n); i++ {
		var m MCV
		m.Value, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		m.Freq, off, err = takeU64(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		c.MCV = append(c.MCV, m)
	}
	if off+8 > len(raw) {
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeColStats", "truncated correlation")
	}
	c.Correlation = math.Float64frombits(encoding.U64(raw, off))
	off += 8
	return c, off, nil
}

func appendIndexStats(buf []byte, idx IndexStats) []byte {
	buf = appendString(buf, idx.Name)
	var bits [8]byte
	encoding.PutU64(bits[:], 0, math.Float64bits(idx.Selectivity))
	buf = append(buf, bits[:]...)
	buf = appendU64(buf, idx.NDV)
	u := byte(0)
	if idx.Unique {
		u = 1
	}
	return append(buf, u)
}

func takeIndexStats(raw []byte, off int) (IndexStats, int, error) {
	var idx IndexStats
	var err error
	idx.Name, off, err = takeString(raw, off)
	if err != nil {
		return IndexStats{}, 0, err
	}
	if off+8 > len(raw) {
		return IndexStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeIndexStats", "truncated selectivity")
	}
	idx.Selectivity = math.Float64frombits(encoding.U64(raw, off))
	off += 8
	idx.NDV, off, err = takeU64(raw, off)
	if err != nil {
		return IndexStats{}, 0, err
	}
	if off >= len(raw) {
		return IndexStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeIndexStats", "truncated unique")
	}
	idx.Unique = raw[off] != 0
	off++
	return idx, off, nil
}

func appendVecStats(buf []byte, v VectorStats) []byte {
	buf = appendU16(buf, uint16(v.Ord))
	buf = appendU64(buf, v.Count)
	buf = appendU16(buf, v.Dim)
	buf = appendString(buf, v.IndexName)
	buf = appendU16(buf, v.M)
	return appendU16(buf, v.EfConstruct)
}

func takeVecStats(raw []byte, off int) (VectorStats, int, error) {
	var v VectorStats
	ord, off, err := takeU16(raw, off)
	if err != nil {
		return VectorStats{}, 0, err
	}
	v.Ord = int(ord)
	v.Count, off, err = takeU64(raw, off)
	if err != nil {
		return VectorStats{}, 0, err
	}
	v.Dim, off, err = takeU16(raw, off)
	if err != nil {
		return VectorStats{}, 0, err
	}
	v.IndexName, off, err = takeString(raw, off)
	if err != nil {
		return VectorStats{}, 0, err
	}
	v.M, off, err = takeU16(raw, off)
	if err != nil {
		return VectorStats{}, 0, err
	}
	v.EfConstruct, off, err = takeU16(raw, off)
	if err != nil {
		return VectorStats{}, 0, err
	}
	return v, off, nil
}

func appendSegStats(buf []byte, seg SegmentStats) ([]byte, error) {
	buf = appendU16(buf, uint16(seg.ID))
	buf = appendU64(buf, seg.Rows)
	if seg.HasBounds {
		buf = append(buf, 1)
	} else {
		buf = append(buf, 0)
	}
	var err error
	buf, err = appendValList(buf, seg.LowPK)
	if err != nil {
		return nil, err
	}
	buf, err = appendValList(buf, seg.HighPK)
	if err != nil {
		return nil, err
	}
	buf, err = appendValList(buf, seg.ColMin)
	if err != nil {
		return nil, err
	}
	buf, err = appendValList(buf, seg.ColMax)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

func takeSegStats(raw []byte, off int) (SegmentStats, int, error) {
	var seg SegmentStats
	id, off, err := takeU16(raw, off)
	if err != nil {
		return SegmentStats{}, 0, err
	}
	seg.ID = int(id)
	seg.Rows, off, err = takeU64(raw, off)
	if err != nil {
		return SegmentStats{}, 0, err
	}
	if off >= len(raw) {
		return SegmentStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeSegStats", "truncated bounds")
	}
	seg.HasBounds = raw[off] != 0
	off++
	seg.LowPK, off, err = takeValList(raw, off)
	if err != nil {
		return SegmentStats{}, 0, err
	}
	seg.HighPK, off, err = takeValList(raw, off)
	if err != nil {
		return SegmentStats{}, 0, err
	}
	seg.ColMin, off, err = takeValList(raw, off)
	if err != nil {
		return SegmentStats{}, 0, err
	}
	seg.ColMax, off, err = takeValList(raw, off)
	if err != nil {
		return SegmentStats{}, 0, err
	}
	return seg, off, nil
}

func appendValList(buf []byte, vs []types.Value) ([]byte, error) {
	buf = appendU16(buf, uint16(len(vs)))
	for _, v := range vs {
		var err error
		buf, err = appendValue(buf, v)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func takeValList(raw []byte, off int) ([]types.Value, int, error) {
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, 0, err
	}
	out := make([]types.Value, 0, n)
	for i := 0; i < int(n); i++ {
		var v types.Value
		v, off, err = takeValue(raw, off)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, v)
	}
	return out, off, nil
}

func appendValue(buf []byte, v types.Value) ([]byte, error) {
	buf = append(buf, byte(v.Typ.Kind), v.Typ.VecElem)
	buf = appendU16(buf, v.Typ.Precision)
	buf = appendU16(buf, v.Typ.Scale)
	raw, err := types.EncodeRow([]types.Value{v})
	if err != nil {
		return nil, err
	}
	return appendBytes(buf, raw), nil
}

func takeValue(raw []byte, off int) (types.Value, int, error) {
	if off+2 > len(raw) {
		return types.Value{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeValue", "truncated type")
	}
	var t types.Type
	t.Kind = types.Kind(raw[off])
	t.VecElem = raw[off+1]
	off += 2
	var err error
	t.Precision, off, err = takeU16(raw, off)
	if err != nil {
		return types.Value{}, 0, err
	}
	t.Scale, off, err = takeU16(raw, off)
	if err != nil {
		return types.Value{}, 0, err
	}
	body, off, err := takeBytes(raw, off)
	if err != nil {
		return types.Value{}, 0, err
	}
	vals, err := types.DecodeRow(body, []types.Type{t})
	if err != nil {
		return types.Value{}, 0, err
	}
	return vals[0], off, nil
}
