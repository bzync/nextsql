package vector

import (
	"bytes"
	"encoding/binary"
	"math"
	"sort"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

// A sparse vector stores only its non-zero coordinates: a sorted list of
// dimension indices and a parallel list of values. Learned sparse retrieval
// (SPLADE-style term weights, BM25-style bags of words) produces vectors with a
// handful of non-zeros over a very large vocabulary, so a dense []float32 would
// be almost entirely zero. Retrieval is an inverted index: one posting list per
// dimension holding (primary key, value) pairs. A search walks the posting lists
// of the query's non-zero dimensions and accumulates the inner product for every
// document that shares a term, which is exact — no vector is missed and no
// approximation is introduced. COSINE additionally needs the document norms, so
// the top candidates by inner product are re-ranked against the full-precision
// sparse payloads when the store can supply them.
//
// This file is the portable in-memory core (validation, dot product, inverted
// index build/add/remove/search) and the versioned on-disk encodings. The SQL
// surface is SPARSEVECTOR<N> plus CREATE VECTOR INDEX … USING SPARSE; the
// executor persists the inverted index to an encrypted B+Tree through the
// SparseStore interface, and tests use SparseMem.
const (
	sparseVecMagic    = "NSSV"
	sparseVecVersion  = 1
	sparseMetaMagic   = "NSSM"
	sparseMetaVersion = 1
	sparseListMagic   = "NSSP"
	sparseListVersion = 1
	sparseMetaLen     = 21

	// MaxSparseDim bounds the coordinate space of a sparse vector (an abuse
	// limit and a decode guard). Learned-sparse vocabularies are large but not
	// unbounded.
	MaxSparseDim = 1 << 24
	// MaxSparseNNZ bounds the number of non-zero coordinates in one sparse
	// vector. A hostile payload cannot force an unbounded allocation.
	MaxSparseNNZ = 1 << 16
	// maxSparseListLen bounds one posting list on decode.
	maxSparseListLen = 1 << 24

	kindSparseMeta    byte = 0x00
	kindSparsePosting byte = 0x01
)

// SparseVec is a sparse vector: Indices is strictly ascending, every entry is
// below Dim, and Values is the parallel list of non-zero, finite weights.
type SparseVec struct {
	Dim     uint32
	Indices []uint32
	Values  []float32
}

// NewSparseVec builds a validated sparse vector from parallel index/value lists
// in any order. Zero or non-finite values and out-of-range or duplicate indices
// are rejected rather than silently dropped.
func NewSparseVec(dim uint32, indices []uint32, values []float32) (SparseVec, error) {
	if len(indices) != len(values) {
		return SparseVec{}, nerr.New(nerr.InvalidArgument, "vector.NewSparseVec", "sparse index/value length mismatch")
	}
	if dim == 0 || dim > MaxSparseDim {
		return SparseVec{}, nerr.New(nerr.InvalidArgument, "vector.NewSparseVec", "bad sparse dimension")
	}
	if len(indices) > MaxSparseNNZ {
		return SparseVec{}, nerr.New(nerr.InvalidArgument, "vector.NewSparseVec", "too many non-zero coordinates")
	}
	order := make([]int, len(indices))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return indices[order[a]] < indices[order[b]] })
	sv := SparseVec{
		Dim:     dim,
		Indices: make([]uint32, len(indices)),
		Values:  make([]float32, len(indices)),
	}
	for out, in := range order {
		sv.Indices[out] = indices[in]
		sv.Values[out] = values[in]
	}
	if err := CheckSparse(sv); err != nil {
		return SparseVec{}, err
	}
	return sv, nil
}

// CheckSparse rejects a malformed sparse vector: length mismatch, an index at or
// above Dim, an out-of-order or duplicate index, or a zero / non-finite value.
func CheckSparse(sv SparseVec) error {
	if sv.Dim == 0 || sv.Dim > MaxSparseDim {
		return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "bad sparse dimension")
	}
	if len(sv.Indices) != len(sv.Values) {
		return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "sparse index/value length mismatch")
	}
	if len(sv.Indices) > MaxSparseNNZ {
		return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "too many non-zero coordinates")
	}
	for i, idx := range sv.Indices {
		if idx >= sv.Dim {
			return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "sparse index out of range")
		}
		if i > 0 && sv.Indices[i-1] >= idx {
			return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "sparse indices not strictly ascending")
		}
		v := sv.Values[i]
		if v == 0 {
			return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "sparse value is zero")
		}
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return nerr.New(nerr.InvalidArgument, "vector.CheckSparse", "sparse value is not finite")
		}
	}
	return nil
}

// SparseNorm is the Euclidean norm of a sparse vector.
func SparseNorm(sv SparseVec) float64 {
	var s float64
	for _, v := range sv.Values {
		f := float64(v)
		s += f * f
	}
	return math.Sqrt(s)
}

// SparseDot is the inner product of two sparse vectors: a merge join over the
// two ascending index lists.
func SparseDot(a, b SparseVec) float64 {
	var dot float64
	i, j := 0, 0
	for i < len(a.Indices) && j < len(b.Indices) {
		switch {
		case a.Indices[i] < b.Indices[j]:
			i++
		case a.Indices[i] > b.Indices[j]:
			j++
		default:
			dot += float64(a.Values[i]) * float64(b.Values[j])
			i++
			j++
		}
	}
	return dot
}

// SparseSimilarity is the natural function value for m (INNER_PRODUCT: dot;
// COSINE: cosine similarity, 0 when either vector is all-zero).
func SparseSimilarity(m Metric, a, b SparseVec) float64 {
	dot := SparseDot(a, b)
	if m == MetricCosine {
		na, nb := SparseNorm(a), SparseNorm(b)
		if na == 0 || nb == 0 {
			return 0
		}
		return dot / (na * nb)
	}
	return dot
}

// SparseDistance is lower-is-closer: COSINE is 1 − similarity, INNER_PRODUCT is
// −dot. Only those two metrics are meaningful for sparse retrieval.
func SparseDistance(m Metric, a, b SparseVec) float64 {
	if m == MetricCosine {
		return 1 - SparseSimilarity(MetricCosine, a, b)
	}
	return -SparseDot(a, b)
}

func validSparseMetric(m Metric) bool { return m == MetricCosine || m == MetricIP }

// EncodeSparse writes a sparse vector: magic, version, dim, non-zero count, the
// indices as ascending deltas (varint), then the parallel values as little-endian
// f32. Malformed input fails closed.
func EncodeSparse(sv SparseVec) ([]byte, error) {
	if err := CheckSparse(sv); err != nil {
		return nil, err
	}
	buf := make([]byte, 4, 13+2*len(sv.Indices)+4*len(sv.Values))
	copy(buf[0:4], sparseVecMagic)
	buf = append(buf, sparseVecVersion)
	var u [4]byte
	encoding.PutU32(u[:], 0, sv.Dim)
	buf = append(buf, u[:]...)
	encoding.PutU32(u[:], 0, uint32(len(sv.Indices)))
	buf = append(buf, u[:]...)
	var prev uint32
	for _, idx := range sv.Indices {
		buf = binary.AppendUvarint(buf, uint64(idx-prev))
		prev = idx
	}
	for _, v := range sv.Values {
		encoding.PutU32(u[:], 0, math.Float32bits(v))
		buf = append(buf, u[:]...)
	}
	return buf, nil
}

// DecodeSparse reads a sparse vector. Malformed input fails closed; the non-zero
// count is bounded before any allocation.
func DecodeSparse(raw []byte) (SparseVec, error) {
	if len(raw) < 13 || !bytes.Equal(raw[0:4], []byte(sparseVecMagic)) {
		return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "bad sparse vector magic")
	}
	if raw[4] != sparseVecVersion {
		return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "unsupported sparse vector version")
	}
	dim := encoding.U32(raw, 5)
	nnz := encoding.U32(raw, 9)
	if dim == 0 || dim > MaxSparseDim {
		return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "bad sparse dimension")
	}
	if nnz > MaxSparseNNZ || uint64(nnz) > uint64(dim) {
		return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "bad sparse non-zero count")
	}
	sv := SparseVec{Dim: dim, Indices: make([]uint32, nnz), Values: make([]float32, nnz)}
	off := 13
	var prev uint64
	for i := 0; i < int(nnz); i++ {
		d, k := binary.Uvarint(raw[off:])
		if k <= 0 {
			return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "truncated sparse index")
		}
		off += k
		// A delta at or above Dim cannot produce a legal index even from 0.
		// Bound it before adding so a hostile wrap cannot sneak a smaller index.
		if d >= uint64(dim) {
			return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "sparse index out of range")
		}
		if i > 0 && d == 0 {
			return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "sparse indices not strictly ascending")
		}
		idx := prev + d
		if idx < prev || idx >= uint64(dim) {
			return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "sparse index out of range")
		}
		sv.Indices[i] = uint32(idx)
		prev = idx
	}
	if off+4*int(nnz) != len(raw) {
		return SparseVec{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparse", "bad sparse vector length")
	}
	for i := 0; i < int(nnz); i++ {
		sv.Values[i] = math.Float32frombits(encoding.U32(raw, off))
		off += 4
	}
	if err := CheckSparse(sv); err != nil {
		return SparseVec{}, err
	}
	return sv, nil
}

// SparseMeta is the durable inverted-index header.
type SparseMeta struct {
	MaxDim uint32
	Metric Metric
	Count  uint64
}

// DefaultSparseMeta returns build parameters. Metric defaults to COSINE.
func DefaultSparseMeta(maxDim uint32, metric Metric) SparseMeta {
	if metric == MetricInvalid {
		metric = MetricCosine
	}
	return SparseMeta{MaxDim: maxDim, Metric: metric}
}

// EncodeSparseMeta writes a 21-byte header. Malformed metadata fails closed.
func EncodeSparseMeta(m SparseMeta) ([]byte, error) {
	if m.MaxDim == 0 || m.MaxDim > MaxSparseDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeSparseMeta", "bad sparse dimension")
	}
	if !validSparseMetric(m.Metric) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeSparseMeta", "bad sparse metric")
	}
	buf := make([]byte, sparseMetaLen)
	copy(buf[0:4], sparseMetaMagic)
	buf[4] = sparseMetaVersion
	encoding.PutU32(buf, 5, m.MaxDim)
	buf[9] = byte(m.Metric)
	encoding.PutU64(buf, 10, m.Count)
	return buf, nil
}

// DecodeSparseMeta reads the header. Malformed input fails closed.
func DecodeSparseMeta(raw []byte) (SparseMeta, error) {
	if len(raw) != sparseMetaLen || !bytes.Equal(raw[0:4], []byte(sparseMetaMagic)) {
		return SparseMeta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseMeta", "bad sparse meta")
	}
	if raw[4] != sparseMetaVersion {
		return SparseMeta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseMeta", "unsupported sparse meta version")
	}
	m := SparseMeta{
		MaxDim: encoding.U32(raw, 5),
		Metric: Metric(raw[9]),
		Count:  encoding.U64(raw, 10),
	}
	if _, err := EncodeSparseMeta(m); err != nil {
		return SparseMeta{}, err
	}
	return m, nil
}

// SparsePosting is one inverted-list member: a primary key and the vector's
// weight for that dimension.
type SparsePosting struct {
	PK    []byte
	Value float32
}

// EncodeSparseList writes one posting list: entries deduplicated by primary key,
// sorted ascending, and front-coded (varint count, then per entry a varint
// shared-prefix length + varint suffix length + suffix + the f32 value).
func EncodeSparseList(entries []SparsePosting) ([]byte, error) {
	tmp := make([]SparsePosting, len(entries))
	copy(tmp, entries)
	sort.Slice(tmp, func(i, j int) bool { return bytes.Compare(tmp[i].PK, tmp[j].PK) < 0 })
	sorted := make([]SparsePosting, 0, len(tmp))
	for i, e := range tmp {
		if len(e.PK) == 0 || len(e.PK) > 4096 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeSparseList", "bad sparse posting key length")
		}
		if e.Value == 0 || math.IsNaN(float64(e.Value)) || math.IsInf(float64(e.Value), 0) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeSparseList", "bad sparse posting value")
		}
		if i > 0 && bytes.Equal(tmp[i-1].PK, e.PK) {
			continue
		}
		sorted = append(sorted, e)
	}
	if len(sorted) > maxSparseListLen {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeSparseList", "sparse posting list too long")
	}
	buf := make([]byte, 5, 16+12*len(sorted))
	copy(buf[0:4], sparseListMagic)
	buf[4] = sparseListVersion
	buf = binary.AppendUvarint(buf, uint64(len(sorted)))
	var prev []byte
	var u [4]byte
	for _, e := range sorted {
		shared := commonPrefixLen(prev, e.PK)
		buf = binary.AppendUvarint(buf, uint64(shared))
		buf = binary.AppendUvarint(buf, uint64(len(e.PK)-shared))
		buf = append(buf, e.PK[shared:]...)
		encoding.PutU32(u[:], 0, math.Float32bits(e.Value))
		buf = append(buf, u[:]...)
		prev = e.PK
	}
	return buf, nil
}

// DecodeSparseList reads one posting list. Malformed input fails closed; every
// varint is bounded before an allocation.
func DecodeSparseList(raw []byte) ([]SparsePosting, error) {
	if len(raw) < 5 || !bytes.Equal(raw[0:4], []byte(sparseListMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "bad sparse posting magic")
	}
	if raw[4] != sparseListVersion {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "unsupported sparse posting version")
	}
	off := 5
	cnt64, k := binary.Uvarint(raw[off:])
	if k <= 0 {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "truncated sparse posting count")
	}
	off += k
	if cnt64 > maxSparseListLen {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "sparse posting list too long")
	}
	out := make([]SparsePosting, 0, cnt64)
	var prev []byte
	for j := 0; j < int(cnt64); j++ {
		shared64, k1 := binary.Uvarint(raw[off:])
		if k1 <= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "truncated sparse posting key")
		}
		off += k1
		suf64, k2 := binary.Uvarint(raw[off:])
		if k2 <= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "truncated sparse posting key")
		}
		off += k2
		if shared64 > 4096 || suf64 > 4096 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "bad sparse posting key")
		}
		shared, suf := int(shared64), int(suf64)
		if shared > len(prev) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "bad sparse posting prefix")
		}
		total := shared + suf
		if total == 0 || total > 4096 || off+suf+4 > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "bad sparse posting key")
		}
		pk := make([]byte, total)
		copy(pk, prev[:shared])
		copy(pk[shared:], raw[off:off+suf])
		off += suf
		val := math.Float32frombits(encoding.U32(raw, off))
		off += 4
		if val == 0 || math.IsNaN(float64(val)) || math.IsInf(float64(val), 0) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "bad sparse posting value")
		}
		if j > 0 && bytes.Compare(prev, pk) >= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "sparse posting keys not ordered")
		}
		out = append(out, SparsePosting{PK: pk, Value: val})
		prev = pk
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeSparseList", "trailing sparse posting bytes")
	}
	return out, nil
}

// SparseMetaKey and SparsePostingKey address the records of a sparse index tree.
// The index gets its own detached tree, so the key space is small.
func SparseMetaKey() []byte { return []byte{kindSparseMeta} }

// SparsePostingKey addresses the posting list of dimension dim.
func SparsePostingKey(dim uint32) []byte {
	out := make([]byte, 5)
	out[0] = kindSparsePosting
	encoding.PutU32(out, 1, dim)
	return out
}

// SplitSparsePostingKey extracts the dimension from a posting-list key.
func SplitSparsePostingKey(k []byte) (uint32, error) {
	if len(k) != 5 || k[0] != kindSparsePosting {
		return 0, nerr.New(nerr.InvalidFormat, "vector.SplitSparsePostingKey", "not a sparse posting key")
	}
	return encoding.U32(k, 1), nil
}

// SparseStore is a durable or in-memory sparse inverted index.
type SparseStore interface {
	LoadSparseMeta() (SparseMeta, error)
	SaveSparseMeta(SparseMeta) error
	ListPostings(dim uint32) ([]SparsePosting, error)
	AddPosting(dim uint32, p SparsePosting) error
	RemovePosting(dim uint32, pk []byte) (bool, error)
	// LoadSparse returns the full-precision sparse vector for pk, or a NotFound
	// error when the caller keeps no payload store (inner-product-only search and
	// no removal by primary key alone).
	LoadSparse(pk []byte) (SparseVec, error)
}

// SparseMem is an in-memory SparseStore used by tests and benches.
type SparseMem struct {
	Meta  SparseMeta
	Lists map[uint32][]SparsePosting
	Vecs  map[string]SparseVec
}

// NewSparseMem returns an empty in-memory sparse index for meta.
func NewSparseMem(meta SparseMeta) *SparseMem {
	return &SparseMem{
		Meta:  meta,
		Lists: make(map[uint32][]SparsePosting),
		Vecs:  make(map[string]SparseVec),
	}
}

func (m *SparseMem) LoadSparseMeta() (SparseMeta, error) { return m.Meta, nil }

func (m *SparseMem) SaveSparseMeta(meta SparseMeta) error {
	m.Meta = meta
	return nil
}

func (m *SparseMem) ListPostings(dim uint32) ([]SparsePosting, error) {
	src := m.Lists[dim]
	out := make([]SparsePosting, len(src))
	for i, e := range src {
		out[i] = SparsePosting{PK: append([]byte(nil), e.PK...), Value: e.Value}
	}
	return out, nil
}

func (m *SparseMem) AddPosting(dim uint32, p SparsePosting) error {
	if m.Lists == nil {
		m.Lists = make(map[uint32][]SparsePosting)
	}
	for i, e := range m.Lists[dim] {
		if bytes.Equal(e.PK, p.PK) {
			m.Lists[dim][i].Value = p.Value
			return nil
		}
	}
	m.Lists[dim] = append(m.Lists[dim], SparsePosting{PK: append([]byte(nil), p.PK...), Value: p.Value})
	return nil
}

func (m *SparseMem) RemovePosting(dim uint32, pk []byte) (bool, error) {
	for i, e := range m.Lists[dim] {
		if bytes.Equal(e.PK, pk) {
			m.Lists[dim] = append(m.Lists[dim][:i:i], m.Lists[dim][i+1:]...)
			if len(m.Lists[dim]) == 0 {
				delete(m.Lists, dim)
			}
			return true, nil
		}
	}
	return false, nil
}

func (m *SparseMem) LoadSparse(pk []byte) (SparseVec, error) {
	sv, ok := m.Vecs[string(pk)]
	if !ok {
		return SparseVec{}, nerr.New(nerr.NotFound, "vector.SparseMem.LoadSparse", "sparse vector not found")
	}
	return sv.clone(), nil
}

// PutVec records the full-precision sparse vector for pk so search can re-rank
// cosine exactly and RemoveSparse can find its dimensions.
func (m *SparseMem) PutVec(pk []byte, sv SparseVec) {
	if m.Vecs == nil {
		m.Vecs = make(map[string]SparseVec)
	}
	m.Vecs[string(pk)] = sv.clone()
}

func (sv SparseVec) clone() SparseVec {
	return SparseVec{
		Dim:     sv.Dim,
		Indices: append([]uint32(nil), sv.Indices...),
		Values:  append([]float32(nil), sv.Values...),
	}
}

// AddSparse inserts pk with sparse vector sv into the inverted index: one
// posting per non-zero coordinate. A pk already present is replaced.
func AddSparse(st SparseStore, pk []byte, sv SparseVec) error {
	if len(pk) == 0 {
		return nerr.New(nerr.InvalidArgument, "vector.AddSparse", "empty primary key")
	}
	if err := CheckSparse(sv); err != nil {
		return err
	}
	meta, err := st.LoadSparseMeta()
	if err != nil {
		return err
	}
	if sv.Dim > meta.MaxDim {
		return nerr.New(nerr.InvalidArgument, "vector.AddSparse", "sparse vector dimension exceeds index")
	}
	if _, err := RemoveSparse(st, pk); err != nil {
		return err
	}
	meta, err = st.LoadSparseMeta()
	if err != nil {
		return err
	}
	for i, idx := range sv.Indices {
		if err := st.AddPosting(idx, SparsePosting{PK: pk, Value: sv.Values[i]}); err != nil {
			return err
		}
	}
	meta.Count++
	if mem, ok := st.(*SparseMem); ok {
		mem.PutVec(pk, sv)
	}
	return st.SaveSparseMeta(meta)
}

// RemoveSparse removes pk from every posting list that holds it. It uses
// LoadSparse to discover the coordinates; a store with no payloads reports the
// pk as not found. It is a no-op when pk is absent.
func RemoveSparse(st SparseStore, pk []byte) (bool, error) {
	sv, err := st.LoadSparse(pk)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return false, nil
		}
		return false, err
	}
	meta, err := st.LoadSparseMeta()
	if err != nil {
		return false, err
	}
	found := false
	for _, idx := range sv.Indices {
		ok, err := st.RemovePosting(idx, pk)
		if err != nil {
			return false, err
		}
		found = found || ok
	}
	if found {
		if meta.Count > 0 {
			meta.Count--
		}
		if mem, ok := st.(*SparseMem); ok {
			delete(mem.Vecs, string(pk))
		}
		if err := st.SaveSparseMeta(meta); err != nil {
			return false, err
		}
	}
	return found, nil
}

// SparseCand is one candidate for brute-force sparse search.
type SparseCand struct {
	PK  []byte
	Vec SparseVec
}

// SparseFlat returns the k closest candidates by exact sparse distance. k < 1
// means every candidate.
func SparseFlat(query SparseVec, metric Metric, cands []SparseCand, k int) ([]Hit, error) {
	if err := CheckSparse(query); err != nil {
		return nil, err
	}
	if len(cands) == 0 {
		return nil, nil
	}
	hits := make([]Hit, len(cands))
	for i, c := range cands {
		if len(c.PK) == 0 {
			return nil, nerr.New(nerr.InvalidArgument, "vector.SparseFlat", "empty primary key")
		}
		if err := CheckSparse(c.Vec); err != nil {
			return nil, err
		}
		hits[i] = Hit{PK: append([]byte(nil), c.PK...), Dist: SparseDistance(metric, query, c.Vec)}
	}
	sort.Slice(hits, func(i, j int) bool { return LessHit(hits[i], hits[j]) })
	if k < 1 || k > len(hits) {
		k = len(hits)
	}
	return hits[:k], nil
}

type sparseCandidate struct {
	pk  []byte
	dot float64
}

// SearchSparse returns the k closest vectors from the inverted index. It walks
// the posting list of every non-zero query coordinate and accumulates the exact
// inner product for each document sharing a term. For INNER_PRODUCT that ranking
// is final. For COSINE the top rerank candidates (rerank <= 0 uses 4*k) are
// re-ranked against their full-precision payloads when the store can supply them;
// otherwise the inner-product ranking stands.
func SearchSparse(st SparseStore, query SparseVec, k, rerank, workers int) ([]Hit, error) {
	_ = workers
	meta, err := st.LoadSparseMeta()
	if err != nil {
		return nil, err
	}
	if err := CheckSparse(query); err != nil {
		return nil, err
	}
	if query.Dim > meta.MaxDim {
		return nil, nerr.New(nerr.InvalidArgument, "vector.SearchSparse", "query dimension exceeds index")
	}
	if k < 1 || meta.Count == 0 {
		return nil, nil
	}

	acc := make(map[string]float64)
	for i, idx := range query.Indices {
		postings, err := st.ListPostings(idx)
		if err != nil {
			return nil, err
		}
		qv := float64(query.Values[i])
		for _, p := range postings {
			acc[string(p.PK)] += qv * float64(p.Value)
		}
	}
	if len(acc) == 0 {
		return nil, nil
	}
	cands := make([]sparseCandidate, 0, len(acc))
	for pk, dot := range acc {
		cands = append(cands, sparseCandidate{pk: []byte(pk), dot: dot})
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].dot != cands[j].dot {
			return cands[i].dot > cands[j].dot
		}
		return bytes.Compare(cands[i].pk, cands[j].pk) < 0
	})

	if meta.Metric != MetricCosine {
		n := k
		if n > len(cands) {
			n = len(cands)
		}
		out := make([]Hit, n)
		for i := 0; i < n; i++ {
			out[i] = Hit{PK: append([]byte(nil), cands[i].pk...), Dist: -cands[i].dot}
		}
		return out, nil
	}

	if rerank <= 0 {
		rerank = 4 * k
	}
	if rerank < k {
		rerank = k
	}
	if rerank > len(cands) {
		rerank = len(cands)
	}
	top := cands[:rerank]
	qNorm := SparseNorm(query)
	full := make([]Hit, 0, len(top))
	for _, c := range top {
		sv, err := st.LoadSparse(c.pk)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				full = nil
				break
			}
			return nil, err
		}
		dn := SparseNorm(sv)
		var sim float64
		if qNorm != 0 && dn != 0 {
			sim = c.dot / (qNorm * dn)
		}
		full = append(full, Hit{PK: append([]byte(nil), c.pk...), Dist: 1 - sim})
	}
	if len(full) > 0 {
		sort.Slice(full, func(i, j int) bool { return LessHit(full[i], full[j]) })
		if k > len(full) {
			k = len(full)
		}
		return full[:k], nil
	}
	// No payloads: fall back to the inner-product ordering.
	n := k
	if n > len(cands) {
		n = len(cands)
	}
	out := make([]Hit, n)
	for i := 0; i < n; i++ {
		out[i] = Hit{PK: append([]byte(nil), cands[i].pk...), Dist: -cands[i].dot}
	}
	return out, nil
}

// PersistSparse writes src's postings and meta to dst.
func PersistSparse(dst SparseStore, src *SparseMem) error {
	if dst == nil || src == nil {
		return nerr.New(nerr.InvalidArgument, "vector.PersistSparse", "nil store")
	}
	dims := make([]uint32, 0, len(src.Lists))
	for d := range src.Lists {
		dims = append(dims, d)
	}
	sort.Slice(dims, func(i, j int) bool { return dims[i] < dims[j] })
	for _, d := range dims {
		for _, e := range src.Lists[d] {
			if err := dst.AddPosting(d, e); err != nil {
				return err
			}
		}
	}
	return dst.SaveSparseMeta(src.Meta)
}

// LoadSparseMem copies a durable sparse index into memory for search and further
// adds. Full-precision payloads are re-supplied by the caller via PutVec.
func LoadSparseMem(src SparseStore, dims []uint32) (*SparseMem, error) {
	if src == nil {
		return nil, nerr.New(nerr.InvalidArgument, "vector.LoadSparseMem", "nil store")
	}
	meta, err := src.LoadSparseMeta()
	if err != nil {
		return nil, err
	}
	m := NewSparseMem(meta)
	for _, d := range dims {
		postings, err := src.ListPostings(d)
		if err != nil {
			return nil, err
		}
		if len(postings) == 0 {
			continue
		}
		m.Lists[d] = postings
		for _, p := range postings {
			if _, seen := m.Vecs[string(p.PK)]; seen {
				continue
			}
			sv, err := src.LoadSparse(p.PK)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return nil, err
			}
			m.Vecs[string(p.PK)] = sv
		}
	}
	return m, nil
}
