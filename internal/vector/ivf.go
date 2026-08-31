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
	"github.com/bzync/nextsql/internal/sql/types"
)

// IVF (inverted file) is a coarse-quantiser ANN index: a set of NList centroids
// trained by k-means over a sample of the column, plus one posting list of
// primary keys per centroid. A search ranks the centroids against the query,
// probes the NProbe nearest lists, and scores every vector in them exactly.
//
// This file is the portable in-memory core (training, add/remove, search) and
// the versioned on-disk encodings. The executor persists an IVF index to an
// encrypted B+Tree through the IVFStore interface; tests use IVFMem.
const (
	ivfMetaMagic   = "NSIV"
	ivfMetaVersion = 1
	ivfCentMagic   = "NSIC"
	ivfCentVersion = 1
	ivfListMagic   = "NSIL"
	ivfListVersion = 1

	// MaxIVFLists bounds the coarse quantiser (abuse limit).
	MaxIVFLists = 1 << 16
	// maxIVFListLen bounds one posting list on decode so a hostile record
	// cannot force an unbounded allocation.
	maxIVFListLen       = 1 << 24
	maxKMeansIter       = 25
	ivfMetaLen          = 25
	kindIVFCents   byte = 0x01
	kindIVFPosting byte = 0x02
)

// IVFMeta is the durable IVF header.
type IVFMeta struct {
	Dim     uint16
	Metric  Metric
	NList   uint32
	NProbe  uint32
	Count   uint64
	Trained bool
}

// DefaultIVFMeta returns build parameters. Metric defaults to COSINE; NProbe
// defaults to ~10% of NList (at least 1).
func DefaultIVFMeta(dim uint16, metric Metric, nlist uint32) IVFMeta {
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
	return IVFMeta{Dim: dim, Metric: metric, NList: nlist, NProbe: nprobe}
}

// EncodeIVFMeta writes a 25-byte IVF header. Malformed metadata fails closed.
func EncodeIVFMeta(m IVFMeta) ([]byte, error) {
	if m.Dim == 0 || int(m.Dim) > MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFMeta", "bad IVF dimension")
	}
	if m.Metric != MetricCosine && m.Metric != MetricL2 && m.Metric != MetricIP {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFMeta", "bad IVF metric")
	}
	if m.NList == 0 || m.NList > MaxIVFLists {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFMeta", "bad IVF list count")
	}
	if m.NProbe == 0 || m.NProbe > m.NList {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFMeta", "bad IVF probe count")
	}
	buf := make([]byte, ivfMetaLen)
	copy(buf[0:4], ivfMetaMagic)
	buf[4] = ivfMetaVersion
	encoding.PutU16(buf, 5, m.Dim)
	buf[7] = byte(m.Metric)
	encoding.PutU32(buf, 8, m.NList)
	encoding.PutU32(buf, 12, m.NProbe)
	encoding.PutU64(buf, 16, m.Count)
	if m.Trained {
		buf[24] = 1
	}
	return buf, nil
}

// DecodeIVFMeta reads an IVF header. Malformed input fails closed.
func DecodeIVFMeta(raw []byte) (IVFMeta, error) {
	if len(raw) != ivfMetaLen || !bytes.Equal(raw[0:4], []byte(ivfMetaMagic)) {
		return IVFMeta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFMeta", "bad IVF meta")
	}
	if raw[4] != ivfMetaVersion {
		return IVFMeta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFMeta", "unsupported IVF meta version")
	}
	m := IVFMeta{
		Dim:     encoding.U16(raw, 5),
		Metric:  Metric(raw[7]),
		NList:   encoding.U32(raw, 8),
		NProbe:  encoding.U32(raw, 12),
		Count:   encoding.U64(raw, 16),
		Trained: raw[24] != 0,
	}
	if _, err := EncodeIVFMeta(m); err != nil {
		return IVFMeta{}, err
	}
	return m, nil
}

// EncodeCentroids writes the coarse-quantiser centroids as a contiguous f32
// block. Every centroid must be dim-wide and finite.
func EncodeCentroids(cent [][]float32, dim int) ([]byte, error) {
	if dim <= 0 || dim > MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeCentroids", "bad IVF dimension")
	}
	if len(cent) == 0 || len(cent) > MaxIVFLists {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeCentroids", "bad IVF centroid count")
	}
	buf := make([]byte, 11+4*dim*len(cent))
	copy(buf[0:4], ivfCentMagic)
	buf[4] = ivfCentVersion
	encoding.PutU16(buf, 5, uint16(dim))
	encoding.PutU32(buf, 7, uint32(len(cent)))
	off := 11
	for _, c := range cent {
		if len(c) != dim {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeCentroids", "IVF centroid dimension mismatch")
		}
		if err := types.ValidateVector(c); err != nil {
			return nil, err
		}
		types.PutF32s(buf[off:], c)
		off += 4 * dim
	}
	return buf, nil
}

// DecodeCentroids reads a centroid block. Malformed input fails closed.
func DecodeCentroids(raw []byte) ([][]float32, error) {
	if len(raw) < 11 || !bytes.Equal(raw[0:4], []byte(ivfCentMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeCentroids", "bad IVF centroid magic")
	}
	if raw[4] != ivfCentVersion {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeCentroids", "unsupported IVF centroid version")
	}
	dim := int(encoding.U16(raw, 5))
	n := int(encoding.U32(raw, 7))
	if dim <= 0 || dim > MaxDim || n <= 0 || n > MaxIVFLists {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeCentroids", "bad IVF centroid header")
	}
	if len(raw) != 11+4*dim*n {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeCentroids", "bad IVF centroid length")
	}
	out := make([][]float32, n)
	off := 11
	for i := 0; i < n; i++ {
		c := types.F32s(raw[off : off+4*dim])
		if err := types.ValidateVector(c); err != nil {
			return nil, err
		}
		out[i] = c
		off += 4 * dim
	}
	return out, nil
}

// EncodeIVFList writes one posting list: the primary keys, deduplicated, sorted
// ascending, and front-coded (varint count, then per key varint shared-prefix
// length + varint suffix length + suffix). Order carries no meaning in a list,
// so sorting is free and the keys — sharing a table/column prefix — compress.
func EncodeIVFList(pks [][]byte) ([]byte, error) {
	tmp := make([][]byte, len(pks))
	copy(tmp, pks)
	sort.Slice(tmp, func(i, j int) bool { return bytes.Compare(tmp[i], tmp[j]) < 0 })
	sorted := make([][]byte, 0, len(tmp))
	for i, pk := range tmp {
		if len(pk) == 0 || len(pk) > 4096 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFList", "bad IVF posting key length")
		}
		if i > 0 && bytes.Equal(tmp[i-1], pk) {
			continue
		}
		sorted = append(sorted, pk)
	}
	if len(sorted) > maxIVFListLen {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeIVFList", "IVF posting list too long")
	}
	buf := make([]byte, 5, 16+8*len(sorted))
	copy(buf[0:4], ivfListMagic)
	buf[4] = ivfListVersion
	buf = binary.AppendUvarint(buf, uint64(len(sorted)))
	var prev []byte
	for _, pk := range sorted {
		shared := commonPrefixLen(prev, pk)
		buf = binary.AppendUvarint(buf, uint64(shared))
		buf = binary.AppendUvarint(buf, uint64(len(pk)-shared))
		buf = append(buf, pk[shared:]...)
		prev = pk
	}
	return buf, nil
}

// DecodeIVFList reads one posting list. Malformed input fails closed; the
// shared/suffix varints are bounded before any allocation.
func DecodeIVFList(raw []byte) ([][]byte, error) {
	if len(raw) < 5 || !bytes.Equal(raw[0:4], []byte(ivfListMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "bad IVF posting magic")
	}
	if raw[4] != ivfListVersion {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "unsupported IVF posting version")
	}
	off := 5
	cnt64, k := binary.Uvarint(raw[off:])
	if k <= 0 {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "truncated IVF posting count")
	}
	off += k
	if cnt64 > maxIVFListLen {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "IVF posting list too long")
	}
	out := make([][]byte, 0, cnt64)
	var prev []byte
	for j := 0; j < int(cnt64); j++ {
		shared64, k1 := binary.Uvarint(raw[off:])
		if k1 <= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "truncated IVF posting key")
		}
		off += k1
		suf64, k2 := binary.Uvarint(raw[off:])
		if k2 <= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "truncated IVF posting key")
		}
		off += k2
		if shared64 > 4096 || suf64 > 4096 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "bad IVF posting key")
		}
		shared, suf := int(shared64), int(suf64)
		if shared > len(prev) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "bad IVF posting prefix")
		}
		total := shared + suf
		if total == 0 || total > 4096 || off+suf > len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "bad IVF posting key")
		}
		pk := make([]byte, total)
		copy(pk, prev[:shared])
		copy(pk[shared:], raw[off:off+suf])
		off += suf
		if j > 0 && bytes.Compare(prev, pk) >= 0 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "IVF posting keys not ordered")
		}
		out = append(out, pk)
		prev = pk
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodeIVFList", "trailing IVF posting bytes")
	}
	return out, nil
}

// IVFMetaKey, IVFCentroidsKey, and IVFPostingKey address the records of an IVF
// index tree. The index gets its own detached tree, so key kinds may overlap
// HNSW's.
func IVFMetaKey() []byte      { return []byte{kindMeta} }
func IVFCentroidsKey() []byte { return []byte{kindIVFCents} }

// IVFCentroidChunkKey addresses centroid group `chunk` (0-based). A large
// centroid set does not fit in one B+Tree record, so the persistent store may
// split it across several groups; the bare IVFCentroidsKey record then holds the
// group count instead of the centroid block itself.
func IVFCentroidChunkKey(chunk uint32) []byte {
	out := make([]byte, 5)
	out[0] = kindIVFCents
	encoding.PutU32(out, 1, chunk)
	return out
}

// IVFPostingKey is the record holding posting list `list`.
func IVFPostingKey(list uint32) []byte {
	out := make([]byte, 5)
	out[0] = kindIVFPosting
	encoding.PutU32(out, 1, list)
	return out
}

// IVFPostingBounds is the exclusive key range of every posting list.
func IVFPostingBounds() (start, end []byte) {
	return []byte{kindIVFPosting}, []byte{kindIVFPosting + 1}
}

// SplitIVFPostingKey returns the list ordinal.
func SplitIVFPostingKey(k []byte) (uint32, error) {
	if len(k) != 5 || k[0] != kindIVFPosting {
		return 0, nerr.New(nerr.InvalidFormat, "vector.SplitIVFPostingKey", "not an IVF posting key")
	}
	return encoding.U32(k, 1), nil
}

// IVFStore is a durable or in-memory IVF index.
type IVFStore interface {
	LoadIVFMeta() (IVFMeta, error)
	SaveIVFMeta(IVFMeta) error
	LoadCentroids() ([][]float32, error)
	SaveCentroids([][]float32) error
	ListPKs(list int) ([][]byte, error)
	AddToList(list int, pk []byte) error
	RemoveFromList(list int, pk []byte) error
	LoadVec(pk []byte) ([]float32, error)
}

// IVFMem is an in-memory IVFStore used by tests and benches.
type IVFMem struct {
	Meta      IVFMeta
	Centroids [][]float32
	Lists     [][][]byte
	Vecs      map[string][]float32
}

func (m *IVFMem) LoadIVFMeta() (IVFMeta, error) { return m.Meta, nil }

func (m *IVFMem) SaveIVFMeta(meta IVFMeta) error {
	m.Meta = meta
	return nil
}

func (m *IVFMem) LoadCentroids() ([][]float32, error) {
	out := make([][]float32, len(m.Centroids))
	for i, c := range m.Centroids {
		out[i] = append([]float32(nil), c...)
	}
	return out, nil
}

func (m *IVFMem) SaveCentroids(c [][]float32) error {
	m.Centroids = make([][]float32, len(c))
	for i, v := range c {
		m.Centroids[i] = append([]float32(nil), v...)
	}
	return nil
}

func (m *IVFMem) ListPKs(list int) ([][]byte, error) {
	if list < 0 || list >= len(m.Lists) {
		return nil, nerr.New(nerr.InvalidArgument, "vector.IVFMem.ListPKs", "IVF list out of range")
	}
	out := make([][]byte, len(m.Lists[list]))
	for i, pk := range m.Lists[list] {
		out[i] = append([]byte(nil), pk...)
	}
	return out, nil
}

func (m *IVFMem) AddToList(list int, pk []byte) error {
	if list < 0 || list >= len(m.Lists) {
		return nerr.New(nerr.InvalidArgument, "vector.IVFMem.AddToList", "IVF list out of range")
	}
	for _, ex := range m.Lists[list] {
		if bytes.Equal(ex, pk) {
			return nil
		}
	}
	m.Lists[list] = append(m.Lists[list], append([]byte(nil), pk...))
	return nil
}

func (m *IVFMem) RemoveFromList(list int, pk []byte) error {
	if list < 0 || list >= len(m.Lists) {
		return nerr.New(nerr.InvalidArgument, "vector.IVFMem.RemoveFromList", "IVF list out of range")
	}
	for i, ex := range m.Lists[list] {
		if bytes.Equal(ex, pk) {
			m.Lists[list] = append(m.Lists[list][:i:i], m.Lists[list][i+1:]...)
			return nil
		}
	}
	return nil
}

func (m *IVFMem) LoadVec(pk []byte) ([]float32, error) {
	v, ok := m.Vecs[string(pk)]
	if !ok {
		return nil, nerr.New(nerr.NotFound, "vector.IVFMem.LoadVec", "vector not found")
	}
	return append([]float32(nil), v...), nil
}

// PutVec records the full-precision vector for pk. Call it before AddIVF, the
// same contract as Mem.PutVec / vector.Insert for HNSW.
func (m *IVFMem) PutVec(pk []byte, v []float32) {
	if m.Vecs == nil {
		m.Vecs = make(map[string][]float32)
	}
	m.Vecs[string(pk)] = append([]float32(nil), v...)
}

// TrainIVF builds a coarse quantiser for meta from a training sample (typically
// a random subset of the column's vectors). Centroids are deterministic for a
// given (meta, samples). The returned index has centroids but no postings — add
// every vector with AddIVF. NList is reduced to len(samples) when the sample is
// smaller.
func TrainIVF(meta IVFMeta, samples [][]float32) (*IVFMem, error) {
	if _, err := EncodeIVFMeta(meta); err != nil {
		return nil, err
	}
	if len(samples) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "vector.TrainIVF", "no training vectors")
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
	rng := rand.New(rand.NewSource(ivfSeed(meta, samples)))
	cent := kmeans(prepped, nlist, dim, rng, meta.Metric)
	m := &IVFMem{
		Meta:      meta,
		Centroids: cent,
		Lists:     make([][][]byte, len(cent)),
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

// AddIVF assigns pk to its nearest centroid's posting list. vec must already be
// loadable from st.LoadVec. A pk already present is moved to its new list.
func AddIVF(st IVFStore, pk []byte, vec []float32) error {
	if len(pk) == 0 {
		return nerr.New(nerr.InvalidArgument, "vector.AddIVF", "empty primary key")
	}
	meta, err := st.LoadIVFMeta()
	if err != nil {
		return err
	}
	if !meta.Trained {
		return nerr.New(nerr.InvalidArgument, "vector.AddIVF", "IVF index is not trained")
	}
	if err := Check(vec, int(meta.Dim)); err != nil {
		return err
	}
	if _, err := RemoveIVF(st, pk); err != nil {
		return err
	}
	meta, err = st.LoadIVFMeta()
	if err != nil {
		return err
	}
	cent, err := st.LoadCentroids()
	if err != nil {
		return err
	}
	if len(cent) == 0 {
		return nerr.New(nerr.Corruption, "vector.AddIVF", "IVF index has no centroids")
	}
	list := nearestCentroid(meta.Metric, cent, vec)
	if err := st.AddToList(list, pk); err != nil {
		return err
	}
	meta.Count++
	return st.SaveIVFMeta(meta)
}

// RemoveIVF removes pk from whichever posting list holds it. It reports whether
// pk was found and is a no-op otherwise.
func RemoveIVF(st IVFStore, pk []byte) (bool, error) {
	meta, err := st.LoadIVFMeta()
	if err != nil {
		return false, err
	}
	for l := 0; l < int(meta.NList); l++ {
		pks, err := st.ListPKs(l)
		if err != nil {
			return false, err
		}
		for _, p := range pks {
			if !bytes.Equal(p, pk) {
				continue
			}
			if err := st.RemoveFromList(l, pk); err != nil {
				return false, err
			}
			if meta.Count > 0 {
				meta.Count--
			}
			return true, st.SaveIVFMeta(meta)
		}
	}
	return false, nil
}

// SearchIVF returns the k closest vectors. It ranks the centroids against the
// query, probes the nprobe nearest posting lists (nprobe <= 0 uses Meta.NProbe),
// and scores every candidate exactly. Recall rises with nprobe.
func SearchIVF(st IVFStore, query []float32, k, nprobe, workers int) ([]Hit, error) {
	meta, err := st.LoadIVFMeta()
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
	if nprobe <= 0 {
		nprobe = int(meta.NProbe)
	}
	if nprobe < 1 {
		nprobe = 1
	}
	if nprobe > len(cent) {
		nprobe = len(cent)
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
	var cands []Candidate
	seen := make(map[string]struct{})
	for i := 0; i < nprobe; i++ {
		pks, err := st.ListPKs(order[i].list)
		if err != nil {
			return nil, err
		}
		for _, pk := range pks {
			if _, dup := seen[string(pk)]; dup {
				continue
			}
			seen[string(pk)] = struct{}{}
			v, err := st.LoadVec(pk)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return nil, err
			}
			cands = append(cands, Candidate{PK: append([]byte(nil), pk...), Vec: v})
		}
	}
	if len(cands) == 0 {
		return nil, nil
	}
	return FlatSearch(query, meta.Metric, cands, k, workers)
}

// PersistIVF writes src's centroids, postings, and meta to dst.
func PersistIVF(dst IVFStore, src *IVFMem) error {
	if dst == nil || src == nil {
		return nerr.New(nerr.InvalidArgument, "vector.PersistIVF", "nil store")
	}
	if err := dst.SaveCentroids(src.Centroids); err != nil {
		return err
	}
	for l, list := range src.Lists {
		for _, pk := range list {
			if err := dst.AddToList(l, pk); err != nil {
				return err
			}
		}
	}
	return dst.SaveIVFMeta(src.Meta)
}

// LoadIVFMem copies a durable IVF index into memory for search and further adds.
func LoadIVFMem(src IVFStore) (*IVFMem, error) {
	if src == nil {
		return nil, nerr.New(nerr.InvalidArgument, "vector.LoadIVFMem", "nil store")
	}
	meta, err := src.LoadIVFMeta()
	if err != nil {
		return nil, err
	}
	cent, err := src.LoadCentroids()
	if err != nil {
		return nil, err
	}
	m := &IVFMem{
		Meta:      meta,
		Centroids: cent,
		Lists:     make([][][]byte, meta.NList),
		Vecs:      make(map[string][]float32),
	}
	for l := 0; l < int(meta.NList); l++ {
		pks, err := src.ListPKs(l)
		if err != nil {
			return nil, err
		}
		m.Lists[l] = pks
		for _, pk := range pks {
			v, err := src.LoadVec(pk)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return nil, err
			}
			m.Vecs[string(pk)] = v
		}
	}
	return m, nil
}

// --- clustering helpers (portable, deterministic) ---

func ivfSeed(m IVFMeta, samples [][]float32) int64 {
	h := fnv.New64a()
	var b [8]byte
	encoding.PutU16(b[:], 0, m.Dim)
	b[2] = byte(m.Metric)
	encoding.PutU32(b[:], 3, m.NList)
	_, _ = h.Write(b[:7])
	encoding.PutU64(b[:], 0, uint64(len(samples)))
	_, _ = h.Write(b[:])
	// Fold a few sample bytes in so distinct data trains distinct centroids.
	for i := 0; i < len(samples); i += 1 + len(samples)/64 {
		for _, x := range samples[i] {
			bits := math.Float32bits(x)
			encoding.PutU32(b[:], 0, bits)
			_, _ = h.Write(b[:4])
		}
	}
	return int64(h.Sum64() & 0x7fffffffffffffff)
}

// prepVec returns the vector as used for coarse-quantiser distance: a unit-norm
// copy for COSINE (so L2 on the sphere ranks like cosine), a plain copy
// otherwise. A zero vector keeps its (zero) direction rather than producing NaN.
func prepVec(metric Metric, v []float32) []float32 {
	out := make([]float32, len(v))
	if metric != MetricCosine {
		copy(out, v)
		return out
	}
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	if n == 0 {
		return out
	}
	for i, x := range v {
		out[i] = float32(float64(x) / n)
	}
	return out
}

func normalizeInPlace(v []float32) {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	n := math.Sqrt(sum)
	if n == 0 {
		return
	}
	for i := range v {
		v[i] = float32(float64(v[i]) / n)
	}
}

func sqL2(a, b []float32) float64 {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	var s float64
	for i := 0; i < n; i++ {
		d := float64(a[i]) - float64(b[i])
		s += d * d
	}
	return s
}

func nearestCentroid(metric Metric, cent [][]float32, vec []float32) int {
	q := prepVec(metric, vec)
	best, bd := 0, math.Inf(1)
	for c := range cent {
		if d := sqL2(q, cent[c]); d < bd {
			bd, best = d, c
		}
	}
	return best
}

// kmeans runs k-means++ seeding then Lloyd iterations over pts (already prepared
// for the metric). An empty cluster is re-seeded to the worst-served point.
func kmeans(pts [][]float32, k, dim int, rng *rand.Rand, metric Metric) [][]float32 {
	if k < 1 {
		k = 1
	}
	if k > len(pts) {
		k = len(pts)
	}
	cent := make([][]float32, 0, k)
	cent = append(cent, append([]float32(nil), pts[rng.Intn(len(pts))]...))
	d2 := make([]float64, len(pts))
	for i := range d2 {
		d2[i] = sqL2(pts[i], cent[0])
	}
	for len(cent) < k {
		var sum float64
		for _, d := range d2 {
			sum += d
		}
		pick := rng.Intn(len(pts))
		if sum > 0 {
			target := rng.Float64() * sum
			acc := 0.0
			for i, d := range d2 {
				acc += d
				if acc >= target {
					pick = i
					break
				}
			}
		}
		cent = append(cent, append([]float32(nil), pts[pick]...))
		last := cent[len(cent)-1]
		for i := range pts {
			if nd := sqL2(pts[i], last); nd < d2[i] {
				d2[i] = nd
			}
		}
	}
	assign := make([]int, len(pts))
	for i := range assign {
		assign[i] = -1
	}
	for iter := 0; iter < maxKMeansIter; iter++ {
		changed := false
		for i, p := range pts {
			best, bd := 0, math.Inf(1)
			for c := range cent {
				if d := sqL2(p, cent[c]); d < bd {
					bd, best = d, c
				}
			}
			if assign[i] != best {
				assign[i] = best
				changed = true
			}
		}
		if !changed && iter > 0 {
			break
		}
		sums := make([][]float64, len(cent))
		counts := make([]int, len(cent))
		for c := range sums {
			sums[c] = make([]float64, dim)
		}
		for i, p := range pts {
			c := assign[i]
			counts[c]++
			for d := 0; d < dim; d++ {
				sums[c][d] += float64(p[d])
			}
		}
		for c := range cent {
			if counts[c] == 0 {
				worst, wd := 0, -1.0
				for i, p := range pts {
					if d := sqL2(p, cent[assign[i]]); d > wd {
						wd, worst = d, i
					}
				}
				copy(cent[c], pts[worst])
				assign[worst] = c
				continue
			}
			for d := 0; d < dim; d++ {
				cent[c][d] = float32(sums[c][d] / float64(counts[c]))
			}
			if metric == MetricCosine {
				normalizeInPlace(cent[c])
			}
		}
	}
	return cent
}
