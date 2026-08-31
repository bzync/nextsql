package vector

import (
	"bytes"
	"encoding/binary"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

// IVF-PQ (inverted file with product quantisation) extends the IVF coarse
// quantiser with a compact code for every vector. A vector assigned to coarse
// centroid c is stored as the M-byte product-quantisation code of its residual
// r = v - c: r is split into M equal sub-vectors and each is replaced by the
// index of its nearest entry in a per-subspace codebook of Ksub (<= 256)
// sub-centroids. A search ranks the coarse centroids, and for each probed list
// scores its entries with asymmetric distance computation (ADC) — a per-subspace
// query-to-sub-centroid distance table summed over the M code bytes — instead of
// touching the full vectors. When the caller's store can still supply the
// full-precision payloads the final candidates are re-ranked exactly, so recall
// tracks an unquantised IVF; without them the ADC ranking stands.
//
// This file is the portable in-memory core (training, add/remove, search) and
// the versioned on-disk encodings. The executor persists an IVF-PQ index to an
// encrypted B+Tree through the IVFPQStore interface; tests use IVFPQMem.
const (
	ivfpqMetaMagic   = "NSPQ"
	ivfpqMetaVersion = 1
	ivfpqCbMagic     = "NSPC"
	ivfpqCbVersion   = 1
	ivfpqListMagic   = "NSPL"
	ivfpqListVersion = 1

	ivfpqMetaLen = 32
	// maxPQM bounds the number of product-quantisation subspaces (abuse limit
	// and a decode guard). Dim must be a multiple of M.
	maxPQM = 128
	// MaxIVFPQSubspaces is the exported abuse limit for the IVF-PQ subspace
	// count M (USING IVFPQ WITH (SUBSPACES = M)); Dim must be a multiple of M.
	MaxIVFPQSubspaces = maxPQM
	// pqKsub is the codebook size per subspace. One code byte per subspace, so
	// it never exceeds 256; a small training sample lowers it.
	pqKsub = 256

	kindPQCodebook byte = 0x03
)

// IVFPQMeta is the durable IVF-PQ header.
type IVFPQMeta struct {
	Dim     uint16
	Metric  Metric
	NList   uint32
	NProbe  uint32
	M       uint16 // product-quantisation subspaces; divides Dim
	Count   uint64
	Trained bool
}

// DefaultIVFPQMeta returns build parameters. Metric defaults to COSINE; NProbe
// defaults to ~10% of NList (at least 1). M must be supplied and divide dim.
func DefaultIVFPQMeta(dim uint16, metric Metric, nlist uint32, m uint16) IVFPQMeta {
	if metric == MetricInvalid {
		metric = MetricCosine
	}
	if nlist == 0 {
		nlist = 1
	}
	nprobe := nlist / 10
	if nprobe == 0 {
		nprobe = 1
	}
	return IVFPQMeta{Dim: dim, Metric: metric, NList: nlist, NProbe: nprobe, M: m}
}

func validIVFPQMetric(m Metric) bool {
	// Product quantisation here quantises residuals in Euclidean space; COSINE
	// is handled by unit-normalising first (L2 on the sphere ranks like cosine).
	// INNER_PRODUCT has no residual formulation here and is rejected.
	return m == MetricCosine || m == MetricL2
}

// EncodeIVFPQMeta writes a 32-byte IVF-PQ header. Malformed metadata fails closed.
func EncodeIVFPQMeta(m IVFPQMeta) ([]byte, error) {
	if m.Dim == 0 || int(m.Dim) > MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFPQMeta", "bad IVF-PQ dimension")
	}
	if !validIVFPQMetric(m.Metric) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFPQMeta", "bad IVF-PQ metric")
	}
	if m.NList == 0 || m.NList > MaxIVFLists {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFPQMeta", "bad IVF-PQ list count")
	}
	if m.NProbe == 0 || m.NProbe > m.NList {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFPQMeta", "bad IVF-PQ probe count")
	}
	if m.M == 0 || int(m.M) > maxPQM || int(m.M) > int(m.Dim) || int(m.Dim)%int(m.M) != 0 {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFPQMeta", "bad IVF-PQ subspace count")
	}
	buf := make([]byte, ivfpqMetaLen)
	copy(buf[0:4], ivfpqMetaMagic)
	buf[4] = ivfpqMetaVersion
	encoding.PutU16(buf, 5, m.Dim)
	buf[7] = byte(m.Metric)
	encoding.PutU32(buf, 8, m.NList)
	encoding.PutU32(buf, 12, m.NProbe)
	encoding.PutU16(buf, 16, m.M)
	encoding.PutU64(buf, 18, m.Count)
	if m.Trained {
		buf[26] = 1
	}
	return buf, nil
}

// DecodeIVFPQMeta reads an IVF-PQ header. Malformed input fails closed.
func DecodeIVFPQMeta(raw []byte) (IVFPQMeta, error) {
	if len(raw) != ivfpqMetaLen || !bytes.Equal(raw[0:4], []byte(ivfpqMetaMagic)) {
		return IVFPQMeta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFPQMeta", "bad IVF-PQ meta")
	}
	if raw[4] != ivfpqMetaVersion {
		return IVFPQMeta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFPQMeta", "unsupported IVF-PQ meta version")
	}
	m := IVFPQMeta{
		Dim:     encoding.U16(raw, 5),
		Metric:  Metric(raw[7]),
		NList:   encoding.U32(raw, 8),
		NProbe:  encoding.U32(raw, 12),
		M:       encoding.U16(raw, 16),
		Count:   encoding.U64(raw, 18),
		Trained: raw[26] != 0,
	}
	if _, err := EncodeIVFPQMeta(m); err != nil {
		return IVFPQMeta{}, err
	}
	return m, nil
}

// PQCodebook holds the M per-subspace sub-centroid sets used to encode and score
// residuals. Sub[m] is Ksub sub-centroids of SubDim floats each.
type PQCodebook struct {
	M      int
	SubDim int
	Ksub   int
	Sub    [][][]float32
}

func (cb *PQCodebook) validate() error {
	if cb == nil {
		return nerr.New(nerr.InvalidFormat, "vector.PQCodebook", "nil codebook")
	}
	if cb.M < 1 || cb.M > maxPQM || cb.SubDim < 1 || cb.SubDim > MaxDim {
		return nerr.New(nerr.InvalidFormat, "vector.PQCodebook", "bad codebook shape")
	}
	if cb.Ksub < 1 || cb.Ksub > pqKsub {
		return nerr.New(nerr.InvalidFormat, "vector.PQCodebook", "bad codebook size")
	}
	if len(cb.Sub) != cb.M {
		return nerr.New(nerr.InvalidFormat, "vector.PQCodebook", "codebook subspace count mismatch")
	}
	for _, sub := range cb.Sub {
		if len(sub) != cb.Ksub {
			return nerr.New(nerr.InvalidFormat, "vector.PQCodebook", "codebook entry count mismatch")
		}
		for _, c := range sub {
			if err := Check(c, cb.SubDim); err != nil {
				return err
			}
		}
	}
	return nil
}

// code returns the index of the nearest sub-centroid in subspace m.
func (cb *PQCodebook) code(sub []float32, m int) byte {
	best, bd := 0, math.Inf(1)
	for j, c := range cb.Sub[m] {
		if d := sqL2(sub, c); d < bd {
			bd, best = d, j
		}
	}
	return byte(best)
}

// distTable returns the squared-L2 distance from qsub to every sub-centroid in
// subspace m.
func (cb *PQCodebook) distTable(qsub []float32, m int) []float64 {
	t := make([]float64, len(cb.Sub[m]))
	for j, c := range cb.Sub[m] {
		t[j] = sqL2(qsub, c)
	}
	return t
}

// EncodePQCodebook writes the product-quantisation codebook as contiguous f32.
func EncodePQCodebook(cb *PQCodebook) ([]byte, error) {
	if err := cb.validate(); err != nil {
		return nil, err
	}
	buf := make([]byte, 11+4*cb.M*cb.Ksub*cb.SubDim)
	copy(buf[0:4], ivfpqCbMagic)
	buf[4] = ivfpqCbVersion
	encoding.PutU16(buf, 5, uint16(cb.M))
	encoding.PutU16(buf, 7, uint16(cb.SubDim))
	encoding.PutU16(buf, 9, uint16(cb.Ksub))
	off := 11
	for m := 0; m < cb.M; m++ {
		for j := 0; j < cb.Ksub; j++ {
			for _, x := range cb.Sub[m][j] {
				encoding.PutU32(buf, off, math.Float32bits(x))
				off += 4
			}
		}
	}
	return buf, nil
}

// DecodePQCodebook reads a codebook. Malformed input fails closed; the element
// count is bounded before any allocation.
func DecodePQCodebook(raw []byte) (*PQCodebook, error) {
	if len(raw) < 11 || !bytes.Equal(raw[0:4], []byte(ivfpqCbMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQCodebook", "bad codebook magic")
	}
	if raw[4] != ivfpqCbVersion {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQCodebook", "unsupported codebook version")
	}
	m := int(encoding.U16(raw, 5))
	subDim := int(encoding.U16(raw, 7))
	ksub := int(encoding.U16(raw, 9))
	if m < 1 || m > maxPQM || subDim < 1 || subDim > MaxDim || ksub < 1 || ksub > pqKsub {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQCodebook", "bad codebook header")
	}
	// m*subDim is one full residual and cannot exceed MaxDim; guard before make.
	if m*subDim > MaxDim || m*ksub*subDim > pqKsub*MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQCodebook", "codebook too large")
	}
	if len(raw) != 11+4*m*ksub*subDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQCodebook", "bad codebook length")
	}
	cb := &PQCodebook{M: m, SubDim: subDim, Ksub: ksub, Sub: make([][][]float32, m)}
	off := 11
	for mi := 0; mi < m; mi++ {
		cb.Sub[mi] = make([][]float32, ksub)
		for j := 0; j < ksub; j++ {
			c := make([]float32, subDim)
			for d := 0; d < subDim; d++ {
				c[d] = math.Float32frombits(encoding.U32(raw, off))
				off += 4
			}
			cb.Sub[mi][j] = c
		}
	}
	if err := cb.validate(); err != nil {
		return nil, err
	}
	return cb, nil
}

// PQEntry is one posting-list member: a primary key and its M-byte code.
type PQEntry struct {
	PK   []byte
	Code []byte
}

// EncodePQList writes one posting list: entries deduplicated by primary key,
// sorted ascending, and front-coded (varint count, then per entry a varint
// shared-prefix length + varint suffix length + suffix + M code bytes).
func EncodePQList(entries []PQEntry, m int) ([]byte, error) {
	if m < 1 || m > maxPQM {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodePQList", "bad IVF-PQ subspace count")
	}
	tmp := make([]PQEntry, len(entries))
	copy(tmp, entries)
	sort.Slice(tmp, func(i, j int) bool { return bytes.Compare(tmp[i].PK, tmp[j].PK) < 0 })
	sorted := make([]PQEntry, 0, len(tmp))
	for i, e := range tmp {
		if len(e.PK) == 0 || len(e.PK) > 4096 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodePQList", "bad IVF-PQ posting key length")
		}
		if len(e.Code) != m {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodePQList", "bad IVF-PQ code length")
		}
		if i > 0 && bytes.Equal(tmp[i-1].PK, e.PK) {
			continue
		}
		sorted = append(sorted, e)
	}
	if len(sorted) > maxIVFListLen {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodePQList", "IVF-PQ posting list too long")
	}
	buf := make([]byte, 5, 16+(8+m)*len(sorted))
	copy(buf[0:4], ivfpqListMagic)
	buf[4] = ivfpqListVersion
	buf = binary.AppendUvarint(buf, uint64(m))
	buf = binary.AppendUvarint(buf, uint64(len(sorted)))
	var prev []byte
	for _, e := range sorted {
		shared := commonPrefixLen(prev, e.PK)
		buf = binary.AppendUvarint(buf, uint64(shared))
		buf = binary.AppendUvarint(buf, uint64(len(e.PK)-shared))
		buf = append(buf, e.PK[shared:]...)
		buf = append(buf, e.Code...)
		prev = e.PK
	}
	return buf, nil
}

// DecodePQList reads one posting list. Malformed input fails closed; every
// varint is bounded before an allocation.
func DecodePQList(raw []byte) ([]PQEntry, error) {
	if len(raw) < 5 || !bytes.Equal(raw[0:4], []byte(ivfpqListMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "bad IVF-PQ posting magic")
	}
	if raw[4] != ivfpqListVersion {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "unsupported IVF-PQ posting version")
	}
	off := 5
	m64, k := binary.Uvarint(raw[off:])
	if k <= 0 || m64 < 1 || m64 > maxPQM {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "bad IVF-PQ subspace count")
	}
	off += k
	m := int(m64)
	cnt64, k := binary.Uvarint(raw[off:])
	if k <= 0 {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "truncated IVF-PQ posting count")
	}
	off += k
	if cnt64 > maxIVFListLen {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "IVF-PQ posting list too long")
	}
	out := make([]PQEntry, 0, cnt64)
	var prev []byte
	for j := 0; j < int(cnt64); j++ {
		shared64, k1 := binary.Uvarint(raw[off:])
		if k1 <= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "truncated IVF-PQ posting key")
		}
		off += k1
		suf64, k2 := binary.Uvarint(raw[off:])
		if k2 <= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "truncated IVF-PQ posting key")
		}
		off += k2
		if shared64 > 4096 || suf64 > 4096 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "bad IVF-PQ posting key")
		}
		shared, suf := int(shared64), int(suf64)
		if shared > len(prev) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "bad IVF-PQ posting prefix")
		}
		total := shared + suf
		if total == 0 || total > 4096 || off+suf+m > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "bad IVF-PQ posting key")
		}
		pk := make([]byte, total)
		copy(pk, prev[:shared])
		copy(pk[shared:], raw[off:off+suf])
		off += suf
		code := make([]byte, m)
		copy(code, raw[off:off+m])
		off += m
		if j > 0 && bytes.Compare(prev, pk) >= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "IVF-PQ posting keys not ordered")
		}
		out = append(out, PQEntry{PK: pk, Code: code})
		prev = pk
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePQList", "trailing IVF-PQ posting bytes")
	}
	return out, nil
}

// IVFPQCodebookKey addresses the codebook record of an IVF-PQ index tree. The
// index gets its own detached tree, so meta/centroid/posting keys are shared
// with IVF (IVFMetaKey, IVFCentroidsKey, IVFPostingKey). A product-quantisation
// codebook does not fit in one B+Tree record, so a persistent store splits the
// encoded block across IVFPQCodebookChunkKey(i) records and keeps the chunk
// count in the bare IVFPQCodebookKey record.
func IVFPQCodebookKey() []byte { return []byte{kindPQCodebook} }

// IVFPQCodebookChunkKey addresses codebook chunk `chunk` (0-based).
func IVFPQCodebookChunkKey(chunk uint32) []byte {
	out := make([]byte, 5)
	out[0] = kindPQCodebook
	encoding.PutU32(out, 1, chunk)
	return out
}

// IVFPQStore is a durable or in-memory IVF-PQ index.
type IVFPQStore interface {
	LoadIVFPQMeta() (IVFPQMeta, error)
	SaveIVFPQMeta(IVFPQMeta) error
	LoadCentroids() ([][]float32, error)
	SaveCentroids([][]float32) error
	LoadCodebook() (*PQCodebook, error)
	SaveCodebook(*PQCodebook) error
	ListEntries(list int) ([]PQEntry, error)
	AddEntry(list int, e PQEntry) error
	RemoveEntry(list int, pk []byte) (bool, error)
	// LoadVec returns the full-precision vector for pk, or a NotFound error when
	// the caller keeps no payload store (ADC-only search).
	LoadVec(pk []byte) ([]float32, error)
}

// IVFPQMem is an in-memory IVFPQStore used by tests and benches.
type IVFPQMem struct {
	Meta      IVFPQMeta
	Centroids [][]float32
	Codebook  *PQCodebook
	Lists     [][]PQEntry
	Vecs      map[string][]float32
}

func (m *IVFPQMem) LoadIVFPQMeta() (IVFPQMeta, error) { return m.Meta, nil }

func (m *IVFPQMem) SaveIVFPQMeta(meta IVFPQMeta) error {
	m.Meta = meta
	return nil
}

func (m *IVFPQMem) LoadCentroids() ([][]float32, error) {
	out := make([][]float32, len(m.Centroids))
	for i, c := range m.Centroids {
		out[i] = append([]float32(nil), c...)
	}
	return out, nil
}

func (m *IVFPQMem) SaveCentroids(c [][]float32) error {
	m.Centroids = make([][]float32, len(c))
	for i, v := range c {
		m.Centroids[i] = append([]float32(nil), v...)
	}
	return nil
}

func (m *IVFPQMem) LoadCodebook() (*PQCodebook, error) {
	if m.Codebook == nil {
		return nil, nerr.New(nerr.NotFound, "vector.IVFPQMem.LoadCodebook", "no codebook")
	}
	cp := &PQCodebook{M: m.Codebook.M, SubDim: m.Codebook.SubDim, Ksub: m.Codebook.Ksub, Sub: make([][][]float32, m.Codebook.M)}
	for mi := range m.Codebook.Sub {
		cp.Sub[mi] = make([][]float32, len(m.Codebook.Sub[mi]))
		for j := range m.Codebook.Sub[mi] {
			cp.Sub[mi][j] = append([]float32(nil), m.Codebook.Sub[mi][j]...)
		}
	}
	return cp, nil
}

func (m *IVFPQMem) SaveCodebook(cb *PQCodebook) error {
	if err := cb.validate(); err != nil {
		return err
	}
	m.Codebook = cb
	return nil
}

func (m *IVFPQMem) ListEntries(list int) ([]PQEntry, error) {
	if list < 0 || list >= len(m.Lists) {
		return nil, nerr.New(nerr.InvalidArgument, "vector.IVFPQMem.ListEntries", "IVF-PQ list out of range")
	}
	out := make([]PQEntry, len(m.Lists[list]))
	for i, e := range m.Lists[list] {
		out[i] = PQEntry{PK: append([]byte(nil), e.PK...), Code: append([]byte(nil), e.Code...)}
	}
	return out, nil
}

func (m *IVFPQMem) AddEntry(list int, e PQEntry) error {
	if list < 0 || list >= len(m.Lists) {
		return nerr.New(nerr.InvalidArgument, "vector.IVFPQMem.AddEntry", "IVF-PQ list out of range")
	}
	for i, ex := range m.Lists[list] {
		if bytes.Equal(ex.PK, e.PK) {
			m.Lists[list][i] = PQEntry{PK: append([]byte(nil), e.PK...), Code: append([]byte(nil), e.Code...)}
			return nil
		}
	}
	m.Lists[list] = append(m.Lists[list], PQEntry{PK: append([]byte(nil), e.PK...), Code: append([]byte(nil), e.Code...)})
	return nil
}

func (m *IVFPQMem) RemoveEntry(list int, pk []byte) (bool, error) {
	if list < 0 || list >= len(m.Lists) {
		return false, nerr.New(nerr.InvalidArgument, "vector.IVFPQMem.RemoveEntry", "IVF-PQ list out of range")
	}
	for i, ex := range m.Lists[list] {
		if bytes.Equal(ex.PK, pk) {
			m.Lists[list] = append(m.Lists[list][:i:i], m.Lists[list][i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

func (m *IVFPQMem) LoadVec(pk []byte) ([]float32, error) {
	v, ok := m.Vecs[string(pk)]
	if !ok {
		return nil, nerr.New(nerr.NotFound, "vector.IVFPQMem.LoadVec", "vector not found")
	}
	return append([]float32(nil), v...), nil
}

// PutVec records the full-precision vector for pk so search can re-rank exactly.
// A store with no payloads (never PutVec'd) serves ADC-only results.
func (m *IVFPQMem) PutVec(pk []byte, v []float32) {
	if m.Vecs == nil {
		m.Vecs = make(map[string][]float32)
	}
	m.Vecs[string(pk)] = append([]float32(nil), v...)
}

// TrainIVFPQ builds an IVF-PQ index for meta from a training sample: a coarse
// quantiser of NList centroids, then an M-subspace product-quantisation codebook
// over the residuals. Centroids and codebook are deterministic for a given
// (meta, samples). The returned index has no postings — add every vector with
// AddIVFPQ.
func TrainIVFPQ(meta IVFPQMeta, samples [][]float32) (*IVFPQMem, error) {
	if _, err := EncodeIVFPQMeta(meta); err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "vector.TrainIVFPQ", "no training vectors")
	}
	dim := int(meta.Dim)
	for _, s := range samples {
		if err := Check(s, dim); err != nil {
			return nil, err
		}
	}
	nlist := int(meta.NList)
	if nlist > len(samples) {
		nlist = len(samples)
	}
	prepped := make([][]float32, len(samples))
	for i, s := range samples {
		prepped[i] = prepVec(meta.Metric, s)
	}
	rng := rand.New(rand.NewSource(ivfpqSeed(meta, samples)))
	cent := kmeans(prepped, nlist, dim, rng, meta.Metric)

	// Residuals in the prepped space.
	residuals := make([][]float32, len(prepped))
	for i, p := range prepped {
		c := cent[nearestPrepped(p, cent)]
		r := make([]float32, dim)
		for d := 0; d < dim; d++ {
			r[d] = p[d] - c[d]
		}
		residuals[i] = r
	}

	mm := int(meta.M)
	subDim := dim / mm
	ksub := pqKsub
	if ksub > len(samples) {
		ksub = len(samples)
	}
	cb := &PQCodebook{M: mm, SubDim: subDim, Ksub: ksub, Sub: make([][][]float32, mm)}
	for s := 0; s < mm; s++ {
		subSamples := make([][]float32, len(residuals))
		for i, r := range residuals {
			subSamples[i] = r[s*subDim : (s+1)*subDim]
		}
		sub := kmeans(subSamples, ksub, subDim, rng, MetricL2)
		// kmeans returns exactly min(ksub, len(pts)) centroids; len(pts) is the
		// same for every subspace, so this always equals ksub.
		if len(sub) != ksub {
			return nil, nerr.New(nerr.Internal, "vector.TrainIVFPQ", "codebook training produced the wrong centroid count")
		}
		cb.Sub[s] = sub
	}
	if err := cb.validate(); err != nil {
		return nil, err
	}

	m := &IVFPQMem{
		Meta:      meta,
		Centroids: cent,
		Codebook:  cb,
		Lists:     make([][]PQEntry, len(cent)),
		Vecs:      make(map[string][]float32),
	}
	m.Meta.NList = uint32(len(cent))
	if m.Meta.NProbe > m.Meta.NList {
		m.Meta.NProbe = m.Meta.NList
	}
	m.Meta.Trained = true
	m.Meta.Count = 0
	return m, nil
}

// encodeResidual splits a prepped-space residual into its M product-quantisation
// code bytes.
func encodeResidual(cb *PQCodebook, residual []float32) []byte {
	code := make([]byte, cb.M)
	for s := 0; s < cb.M; s++ {
		code[s] = cb.code(residual[s*cb.SubDim:(s+1)*cb.SubDim], s)
	}
	return code
}

// AddIVFPQ assigns pk to its nearest coarse centroid and stores the residual's
// product-quantisation code. vec is the full-precision vector. A pk already
// present is moved to its new list.
func AddIVFPQ(st IVFPQStore, pk []byte, vec []float32) error {
	if len(pk) == 0 {
		return nerr.New(nerr.InvalidArgument, "vector.AddIVFPQ", "empty primary key")
	}
	meta, err := st.LoadIVFPQMeta()
	if err != nil {
		return err
	}
	if !meta.Trained {
		return nerr.New(nerr.InvalidArgument, "vector.AddIVFPQ", "IVF-PQ index is not trained")
	}
	if err := Check(vec, int(meta.Dim)); err != nil {
		return err
	}
	if _, err := RemoveIVFPQ(st, pk); err != nil {
		return err
	}
	meta, err = st.LoadIVFPQMeta()
	if err != nil {
		return err
	}
	cent, err := st.LoadCentroids()
	if err != nil {
		return err
	}
	if len(cent) == 0 {
		return nerr.New(nerr.Corruption, "vector.AddIVFPQ", "IVF-PQ index has no centroids")
	}
	cb, err := st.LoadCodebook()
	if err != nil {
		return err
	}
	if err := cb.validate(); err != nil {
		return err
	}
	p := prepVec(meta.Metric, vec)
	list := nearestPrepped(p, cent)
	residual := make([]float32, len(p))
	for d := range p {
		residual[d] = p[d] - cent[list][d]
	}
	if err := st.AddEntry(list, PQEntry{PK: pk, Code: encodeResidual(cb, residual)}); err != nil {
		return err
	}
	meta.Count++
	return st.SaveIVFPQMeta(meta)
}

// RemoveIVFPQ removes pk from whichever posting list holds it. It reports
// whether pk was found and is a no-op otherwise.
func RemoveIVFPQ(st IVFPQStore, pk []byte) (bool, error) {
	meta, err := st.LoadIVFPQMeta()
	if err != nil {
		return false, err
	}
	for l := 0; l < int(meta.NList); l++ {
		ok, err := st.RemoveEntry(l, pk)
		if err != nil {
			return false, err
		}
		if ok {
			if meta.Count > 0 {
				meta.Count--
			}
			return true, st.SaveIVFPQMeta(meta)
		}
	}
	return false, nil
}

type pqCand struct {
	pk     []byte
	approx float64
}

// SearchIVFPQ returns the k closest vectors. It ranks the coarse centroids,
// probes the nprobe nearest posting lists (nprobe <= 0 uses Meta.NProbe), scores
// every entry with ADC over its code, and keeps the best rerank candidates
// (rerank <= 0 uses 4*k). If the store can supply the full-precision vectors for
// all of them the result is re-ranked exactly; otherwise the ADC ranking stands.
func SearchIVFPQ(st IVFPQStore, query []float32, k, nprobe, rerank, workers int) ([]Hit, error) {
	meta, err := st.LoadIVFPQMeta()
	if err != nil {
		return nil, err
	}
	if err := Check(query, int(meta.Dim)); err != nil {
		return nil, err
	}
	if k < 1 || !meta.Trained || meta.Count == 0 {
		return nil, nil
	}
	cent, err := st.LoadCentroids()
	if err != nil {
		return nil, err
	}
	if len(cent) == 0 {
		return nil, nil
	}
	cb, err := st.LoadCodebook()
	if err != nil {
		return nil, err
	}
	if err := cb.validate(); err != nil {
		return nil, err
	}
	if cb.M != int(meta.M) {
		return nil, nerr.New(nerr.Corruption, "vector.SearchIVFPQ", "codebook subspace count disagrees with meta")
	}
	if nprobe <= 0 {
		nprobe = int(meta.NProbe)
	}
	if nprobe < 1 {
		nprobe = 1
	}
	if nprobe > len(cent) {
		nprobe = len(cent)
	}
	if rerank <= 0 {
		rerank = 4 * k
	}

	q := prepVec(meta.Metric, query)
	type cd struct {
		list int
		d    float64
	}
	order := make([]cd, len(cent))
	for c := range cent {
		order[c] = cd{list: c, d: sqL2(q, cent[c])}
	}
	sort.Slice(order, func(i, j int) bool {
		if order[i].d != order[j].d {
			return order[i].d < order[j].d
		}
		return order[i].list < order[j].list
	})

	var cands []pqCand
	seen := make(map[string]struct{})
	residual := make([]float32, len(q))
	for i := 0; i < nprobe; i++ {
		list := order[i].list
		for d := range q {
			residual[d] = q[d] - cent[list][d]
		}
		lut := make([][]float64, cb.M)
		for s := 0; s < cb.M; s++ {
			lut[s] = cb.distTable(residual[s*cb.SubDim:(s+1)*cb.SubDim], s)
		}
		entries, err := st.ListEntries(list)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if _, dup := seen[string(e.PK)]; dup {
				continue
			}
			if len(e.Code) != cb.M {
				return nil, nerr.New(nerr.Corruption, "vector.SearchIVFPQ", "IVF-PQ code length disagrees with meta")
			}
			seen[string(e.PK)] = struct{}{}
			var approx float64
			for s := 0; s < cb.M; s++ {
				c := int(e.Code[s])
				if c >= len(lut[s]) {
					return nil, nerr.New(nerr.Corruption, "vector.SearchIVFPQ", "IVF-PQ code out of range")
				}
				approx += lut[s][c]
			}
			cands = append(cands, pqCand{pk: append([]byte(nil), e.PK...), approx: approx})
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	sort.Slice(cands, func(i, j int) bool {
		if cands[i].approx != cands[j].approx {
			return cands[i].approx < cands[j].approx
		}
		return bytes.Compare(cands[i].pk, cands[j].pk) < 0
	})
	if rerank < k {
		rerank = k
	}
	if rerank > len(cands) {
		rerank = len(cands)
	}
	top := cands[:rerank]

	full := make([]Candidate, 0, len(top))
	haveAll := true
	for _, c := range top {
		v, err := st.LoadVec(c.pk)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				haveAll = false
				break
			}
			return nil, err
		}
		full = append(full, Candidate{PK: c.pk, Vec: v})
	}
	if haveAll && len(full) > 0 {
		return FlatSearch(query, meta.Metric, full, k, workers)
	}
	// ADC-only ordering (approx is squared L2 in the prepped space).
	n := k
	if n > len(top) {
		n = len(top)
	}
	out := make([]Hit, n)
	for i := 0; i < n; i++ {
		out[i] = Hit{PK: append([]byte(nil), top[i].pk...), Dist: top[i].approx}
	}
	return out, nil
}

// PersistIVFPQ writes src's centroids, codebook, postings, and meta to dst.
func PersistIVFPQ(dst IVFPQStore, src *IVFPQMem) error {
	if dst == nil || src == nil {
		return nerr.New(nerr.InvalidArgument, "vector.PersistIVFPQ", "nil store")
	}
	if err := dst.SaveCentroids(src.Centroids); err != nil {
		return err
	}
	if err := dst.SaveCodebook(src.Codebook); err != nil {
		return err
	}
	for l, list := range src.Lists {
		for _, e := range list {
			if err := dst.AddEntry(l, e); err != nil {
				return err
			}
		}
	}
	return dst.SaveIVFPQMeta(src.Meta)
}

// LoadIVFPQMem copies a durable IVF-PQ index into memory for search and further
// adds. Full-precision payloads are re-supplied by the caller via PutVec.
func LoadIVFPQMem(src IVFPQStore) (*IVFPQMem, error) {
	if src == nil {
		return nil, nerr.New(nerr.InvalidArgument, "vector.LoadIVFPQMem", "nil store")
	}
	meta, err := src.LoadIVFPQMeta()
	if err != nil {
		return nil, err
	}
	cent, err := src.LoadCentroids()
	if err != nil {
		return nil, err
	}
	cb, err := src.LoadCodebook()
	if err != nil {
		return nil, err
	}
	m := &IVFPQMem{
		Meta:      meta,
		Centroids: cent,
		Codebook:  cb,
		Lists:     make([][]PQEntry, meta.NList),
		Vecs:      make(map[string][]float32),
	}
	for l := 0; l < int(meta.NList); l++ {
		entries, err := src.ListEntries(l)
		if err != nil {
			return nil, err
		}
		m.Lists[l] = entries
		for _, e := range entries {
			v, err := src.LoadVec(e.PK)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return nil, err
			}
			m.Vecs[string(e.PK)] = v
		}
	}
	return m, nil
}

func ivfpqSeed(m IVFPQMeta, samples [][]float32) int64 {
	h := fnv.New64a()
	var b [8]byte
	encoding.PutU16(b[:], 0, m.Dim)
	b[2] = byte(m.Metric)
	encoding.PutU32(b[:], 3, m.NList)
	_, _ = h.Write(b[:7])
	encoding.PutU16(b[:], 0, m.M)
	_, _ = h.Write(b[:2])
	encoding.PutU64(b[:], 0, uint64(len(samples)))
	_, _ = h.Write(b[:])
	for i := 0; i < len(samples); i += 1 + len(samples)/64 {
		for _, x := range samples[i] {
			encoding.PutU32(b[:], 0, math.Float32bits(x))
			_, _ = h.Write(b[:4])
		}
	}
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// nearestPrepped returns the index of the centroid closest (squared L2) to an
// already-prepped vector.
func nearestPrepped(p []float32, cent [][]float32) int {
	best, bd := 0, math.Inf(1)
	for c := range cent {
		if d := sqL2(p, cent[c]); d < bd {
			bd, best = d, c
		}
	}
	return best
}
