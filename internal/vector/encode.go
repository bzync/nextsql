package vector

import (
	"bytes"
	"encoding/binary"
	"sort"

	"github.com/bzync/nextsql/internal/bitvec"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/float16"
	"github.com/bzync/nextsql/internal/int8vec"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	payloadMagic = "NSVV"
	// payloadVersion 1: NSVV | 1 | dim(u16) | f32[dim]
	// payloadVersion 2: NSVV | 2 | elem(1) | dim(u16) | <elem-specific>  (Phase 23)
	//   elem F16: half[dim]
	//   elem I8 : scale(f32 LE) | int8[dim]
	//   elem BIT: packed bits, ceil(dim/8) bytes, LSB-first
	payloadVersion  = 1
	payloadVersionQ = 2
	metaMagic       = "NSHM"
	metaVersion     = 1
	// metaVersionQ carries the traversal-quantisation element tag for a
	// quantised HNSW index (Phase 23). v1 headers decode with Quant == 0.
	metaVersionQ = 2
	nodeVersion  = 1
	// nodeVersionC front-codes each layer's neighbour list: a varint count, then
	// the keys sorted ascending with a shared-prefix length + suffix per key
	// (Phase 23). Neighbour order carries no meaning in the graph, so sorting is
	// free and the keys — which share a table/column prefix and, for a dense id
	// space, several leading bytes — compress well. v1 nodes still decode.
	nodeVersionC = 2

	kindMeta byte = 0x00
	kindNode byte = 0x01
	kindVec  byte = 0x01
	kindQVec byte = 0x02

	// MaxDim is the abuse limit for VECTOR<F32,N>.
	MaxDim = 8192
	maxM   = 64
	maxEf  = 1024
	maxLvl = 16
)

// PayloadKey is the table vector-store key for one column and primary key.
func PayloadKey(col uint16, pk []byte) []byte {
	out := make([]byte, 3+len(pk))
	out[0] = kindVec
	encoding.PutU16(out, 1, col)
	copy(out[3:], pk)
	return out
}

// PayloadBounds is the exclusive range of every payload for col.
func PayloadBounds(col uint16) (start, end []byte) {
	start = PayloadKey(col, nil)
	end = types.PrefixEnd(start)
	return start, end
}

// SplitPayloadKey extracts column ordinal and primary key.
func SplitPayloadKey(k []byte) (col uint16, pk []byte, err error) {
	if len(k) < 3 || k[0] != kindVec {
		return 0, nil, nerr.New(nerr.InvalidFormat, "vector.SplitPayloadKey", "not a vector payload key")
	}
	return encoding.U16(k, 1), append([]byte(nil), k[3:]...), nil
}

// EncodePayload writes a versioned contiguous f32 block.
func EncodePayload(v []float32) ([]byte, error) { return EncodePayloadElem(v, types.VecF32) }

// EncodePayloadElem writes a vector payload in the on-disk element encoding.
// VecF16 stores each element as an IEEE 754 half (2 bytes); VecI8 stores each
// element as a signed byte plus a per-vector float32 scale. Quantisation is
// applied here; the value read back by DecodePayload is the widened element.
func EncodePayloadElem(v []float32, elem uint8) ([]byte, error) {
	if err := Check(v, 0); err != nil {
		return nil, err
	}
	switch elem {
	case types.VecF16:
		buf := make([]byte, 4+1+1+2+2*len(v))
		copy(buf[0:4], payloadMagic)
		buf[4] = payloadVersionQ
		buf[5] = types.VecF16
		encoding.PutU16(buf, 6, uint16(len(v)))
		float16.Put(buf[8:], v)
		return buf, nil
	case types.VecI8:
		buf := make([]byte, 4+1+1+2+int8vec.Bytes(len(v)))
		copy(buf[0:4], payloadMagic)
		buf[4] = payloadVersionQ
		buf[5] = types.VecI8
		encoding.PutU16(buf, 6, uint16(len(v)))
		int8vec.Encode(buf[8:], v)
		return buf, nil
	case types.VecBit:
		if err := bitvec.Validate(v); err != nil {
			return nil, err
		}
		buf := make([]byte, 4+1+1+2+bitvec.Bytes(len(v)))
		copy(buf[0:4], payloadMagic)
		buf[4] = payloadVersionQ
		buf[5] = types.VecBit
		encoding.PutU16(buf, 6, uint16(len(v)))
		bitvec.Encode(buf[8:], v)
		return buf, nil
	}
	buf := make([]byte, 4+1+2+4*len(v))
	copy(buf[0:4], payloadMagic)
	buf[4] = payloadVersion
	encoding.PutU16(buf, 5, uint16(len(v)))
	types.PutF32s(buf[7:], v)
	return buf, nil
}

// DecodePayload reads a vector payload, widening quantised elements to float32.
// Malformed input fails closed.
func DecodePayload(raw []byte) ([]float32, error) {
	if len(raw) < 7 || !bytes.Equal(raw[0:4], []byte(payloadMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload magic")
	}
	switch raw[4] {
	case payloadVersion:
		dim := encoding.U16(raw, 5)
		if dim == 0 || int(dim) > MaxDim {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector dimension")
		}
		if len(raw) != 7+4*int(dim) {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload length")
		}
		out := types.F32s(raw[7:])
		if err := Check(out, int(dim)); err != nil {
			return nil, err
		}
		return out, nil
	case payloadVersionQ:
		if len(raw) < 8 {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "truncated vector payload")
		}
		dim := encoding.U16(raw, 6)
		if dim == 0 || int(dim) > MaxDim {
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector dimension")
		}
		var out []float32
		switch raw[5] {
		case types.VecF16:
			if len(raw) != 8+2*int(dim) {
				return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload length")
			}
			out = float16.Read(raw[8:])
		case types.VecI8:
			if len(raw) != 8+int8vec.Bytes(int(dim)) {
				return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload length")
			}
			out = int8vec.Decode(raw[8:])
		case types.VecBit:
			if len(raw) != 8+bitvec.Bytes(int(dim)) {
				return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload length")
			}
			out = bitvec.Decode(raw[8:], int(dim))
		default:
			return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "unknown vector element encoding")
		}
		if err := Check(out, int(dim)); err != nil {
			return nil, err
		}
		return out, nil
	default:
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "unsupported vector payload version")
	}
}

// Check rejects empty, oversize, dimension-mismatch, and non-finite vectors.
// want == 0 means any legal dimension.
func Check(v []float32, want int) error {
	if len(v) == 0 {
		return nerr.New(nerr.InvalidArgument, "vector.Check", "VECTOR is empty")
	}
	if len(v) > MaxDim {
		return nerr.New(nerr.InvalidArgument, "vector.Check", "VECTOR dimension exceeds limit")
	}
	if want > 0 && len(v) != want {
		return nerr.New(nerr.InvalidArgument, "vector.Check", "VECTOR dimension mismatch")
	}
	return types.ValidateVector(v)
}

// MetaKey is the single HNSW header record.
func MetaKey() []byte { return []byte{kindMeta} }

// NodeKey stores one HNSW graph node.
func NodeKey(pk []byte) []byte {
	out := make([]byte, 1+len(pk))
	out[0] = kindNode
	copy(out[1:], pk)
	return out
}

// SplitNodeKey returns the primary-key suffix.
func SplitNodeKey(k []byte) ([]byte, error) {
	if len(k) < 2 || k[0] != kindNode {
		return nil, nerr.New(nerr.InvalidFormat, "vector.SplitNodeKey", "not an HNSW node key")
	}
	return append([]byte(nil), k[1:]...), nil
}

// NodeBounds is the exclusive key range of every HNSW vertex.
func NodeBounds() (start, end []byte) {
	return []byte{kindNode}, []byte{kindNode + 1}
}

// Meta is the durable HNSW header.
type Meta struct {
	Dim         uint16
	Metric      Metric
	M           uint8
	EfConstruct uint16
	Count       uint64
	MaxLevel    uint8
	Entry       []byte
	// Quant is the traversal-quantisation encoding (0 / VecF16 / VecI8). When
	// non-zero the graph keeps a quantised copy of every vector for distance
	// computation during search, and Search re-ranks the result against the
	// full-precision column payloads.
	Quant uint8
}

// EncodeMeta writes an HNSW header.
func EncodeMeta(m Meta) ([]byte, error) {
	if m.Dim == 0 || int(m.Dim) > MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW dimension")
	}
	if m.Metric != MetricCosine && m.Metric != MetricL2 && m.Metric != MetricIP && m.Metric != MetricHamming {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW metric")
	}
	if m.M == 0 || m.M > maxM {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW M")
	}
	if m.EfConstruct == 0 || m.EfConstruct > maxEf {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW efConstruction")
	}
	if m.MaxLevel > maxLvl {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW level")
	}
	if len(m.Entry) > 4096 {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "HNSW entry key too long")
	}
	if m.Quant != 0 && m.Quant != types.VecF16 && m.Quant != types.VecI8 {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW traversal quantisation")
	}
	extra := 0
	if m.Quant != 0 {
		extra = 1
	}
	buf := make([]byte, 4+1+2+1+1+2+8+1+2+len(m.Entry)+extra)
	copy(buf[0:4], metaMagic)
	buf[4] = metaVersion
	if m.Quant != 0 {
		buf[4] = metaVersionQ
	}
	encoding.PutU16(buf, 5, m.Dim)
	buf[7] = byte(m.Metric)
	buf[8] = m.M
	encoding.PutU16(buf, 9, m.EfConstruct)
	encoding.PutU64(buf, 11, m.Count)
	buf[19] = m.MaxLevel
	encoding.PutU16(buf, 20, uint16(len(m.Entry)))
	copy(buf[22:], m.Entry)
	if m.Quant != 0 {
		buf[22+len(m.Entry)] = m.Quant
	}
	return buf, nil
}

// DecodeMeta reads an HNSW header. Malformed input fails closed.
func DecodeMeta(raw []byte) (Meta, error) {
	if len(raw) < 22 || !bytes.Equal(raw[0:4], []byte(metaMagic)) {
		return Meta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeMeta", "bad HNSW meta magic")
	}
	quantised := raw[4] == metaVersionQ
	if raw[4] != metaVersion && !quantised {
		return Meta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeMeta", "unsupported HNSW meta version")
	}
	m := Meta{
		Dim:         encoding.U16(raw, 5),
		Metric:      Metric(raw[7]),
		M:           raw[8],
		EfConstruct: encoding.U16(raw, 9),
		Count:       encoding.U64(raw, 11),
		MaxLevel:    raw[19],
	}
	n := encoding.U16(raw, 20)
	want := 22 + int(n)
	if quantised {
		want++
	}
	if len(raw) != want {
		return Meta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeMeta", "bad HNSW meta length")
	}
	if n > 0 {
		m.Entry = append([]byte(nil), raw[22:22+int(n)]...)
	}
	if quantised {
		m.Quant = raw[22+int(n)]
		if m.Quant != types.VecF16 && m.Quant != types.VecI8 {
			return Meta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeMeta", "bad HNSW traversal quantisation")
		}
	}
	if _, err := EncodeMeta(m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

// Node is one HNSW vertex. Neighbors[layer] is a list of primary keys.
type Node struct {
	Level     uint8
	Deleted   bool
	Neighbors [][][]byte
}

// EncodeNode writes an HNSW vertex in node format v2 (front-coded neighbour
// lists). Each layer is a varint neighbour count followed by the neighbour keys
// sorted ascending, every key stored as varint(shared-prefix length with the
// previous key) + varint(suffix length) + suffix bytes.
func EncodeNode(n Node) ([]byte, error) {
	if n.Level > maxLvl {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "bad HNSW node level")
	}
	layers := int(n.Level) + 1
	if len(n.Neighbors) != layers {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "HNSW neighbor layers mismatch")
	}
	buf := make([]byte, 4, 64)
	buf[0] = nodeVersionC
	buf[1] = n.Level
	if n.Deleted {
		buf[2] = 1
	}
	buf[3] = uint8(layers)
	for _, layer := range n.Neighbors {
		if len(layer) > 2*maxM {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "too many HNSW neighbors")
		}
		sorted := make([][]byte, len(layer))
		copy(sorted, layer)
		sort.Slice(sorted, func(i, j int) bool { return bytes.Compare(sorted[i], sorted[j]) < 0 })
		buf = binary.AppendUvarint(buf, uint64(len(sorted)))
		var prev []byte
		for _, pk := range sorted {
			if len(pk) == 0 || len(pk) > 4096 {
				return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "bad HNSW neighbor key length")
			}
			shared := commonPrefixLen(prev, pk)
			buf = binary.AppendUvarint(buf, uint64(shared))
			buf = binary.AppendUvarint(buf, uint64(len(pk)-shared))
			buf = append(buf, pk[shared:]...)
			prev = pk
		}
	}
	return buf, nil
}

func commonPrefixLen(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	i := 0
	for i < n && a[i] == b[i] {
		i++
	}
	return i
}

// DecodeNode reads an HNSW vertex (node format v1 or v2). Malformed input fails closed.
func DecodeNode(raw []byte) (Node, error) {
	if len(raw) < 4 {
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW node")
	}
	switch raw[0] {
	case nodeVersion:
		return decodeNodeV1(raw)
	case nodeVersionC:
		return decodeNodeV2(raw)
	default:
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW node")
	}
}

func decodeNodeV1(raw []byte) (Node, error) {
	n := Node{Level: raw[1], Deleted: raw[2] != 0}
	layers := int(raw[3])
	if layers < 1 || layers > maxLvl+1 || int(n.Level)+1 != layers {
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW node layers")
	}
	off := 4
	n.Neighbors = make([][][]byte, layers)
	for i := 0; i < layers; i++ {
		if off+2 > len(raw) {
			return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "truncated HNSW neighbors")
		}
		cnt := int(encoding.U16(raw, off))
		off += 2
		if cnt > 2*maxM {
			return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "too many HNSW neighbors")
		}
		n.Neighbors[i] = make([][]byte, cnt)
		for j := 0; j < cnt; j++ {
			if off+2 > len(raw) {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "truncated HNSW neighbor")
			}
			ln := int(encoding.U16(raw, off))
			off += 2
			if ln == 0 || ln > 4096 || off+ln > len(raw) {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW neighbor key")
			}
			n.Neighbors[i][j] = append([]byte(nil), raw[off:off+ln]...)
			off += ln
		}
	}
	if off != len(raw) {
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "trailing HNSW node bytes")
	}
	return n, nil
}

func decodeNodeV2(raw []byte) (Node, error) {
	n := Node{Level: raw[1], Deleted: raw[2] != 0}
	layers := int(raw[3])
	if layers < 1 || layers > maxLvl+1 || int(n.Level)+1 != layers {
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW node layers")
	}
	off := 4
	n.Neighbors = make([][][]byte, layers)
	for i := 0; i < layers; i++ {
		cnt64, k := binary.Uvarint(raw[off:])
		if k <= 0 {
			return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "truncated HNSW neighbors")
		}
		off += k
		if cnt64 > uint64(2*maxM) {
			return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "too many HNSW neighbors")
		}
		cnt := int(cnt64)
		n.Neighbors[i] = make([][]byte, cnt)
		var prev []byte
		for j := 0; j < cnt; j++ {
			shared64, k1 := binary.Uvarint(raw[off:])
			if k1 <= 0 {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "truncated HNSW neighbor")
			}
			off += k1
			suf64, k2 := binary.Uvarint(raw[off:])
			if k2 <= 0 {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "truncated HNSW neighbor")
			}
			off += k2
			if shared64 > 4096 || suf64 > 4096 {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW neighbor key")
			}
			shared, suf := int(shared64), int(suf64)
			if shared > len(prev) {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW neighbor prefix")
			}
			total := shared + suf
			if total == 0 || total > 4096 || off+suf > len(raw) {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW neighbor key")
			}
			pk := make([]byte, total)
			copy(pk, prev[:shared])
			copy(pk[shared:], raw[off:off+suf])
			off += suf
			if j > 0 && bytes.Compare(prev, pk) > 0 {
				return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "HNSW neighbors not ordered")
			}
			n.Neighbors[i][j] = pk
			prev = pk
		}
	}
	if off != len(raw) {
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "trailing HNSW node bytes")
	}
	return n, nil
}

// DefaultMeta returns construction parameters. Metric defaults to COSINE.
func DefaultMeta(dim uint16, metric Metric) Meta {
	if metric == MetricInvalid {
		metric = MetricCosine
	}
	return Meta{Dim: dim, Metric: metric, M: 16, EfConstruct: 64}
}
