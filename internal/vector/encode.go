package vector

import (
	"bytes"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	payloadMagic   = "NSVV"
	payloadVersion = 1
	metaMagic      = "NSHM"
	metaVersion    = 1
	nodeVersion    = 1

	kindMeta byte = 0x00
	kindNode byte = 0x01
	kindVec  byte = 0x01

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
func EncodePayload(v []float32) ([]byte, error) {
	if err := Check(v, 0); err != nil {
		return nil, err
	}
	buf := make([]byte, 4+1+2+4*len(v))
	copy(buf[0:4], payloadMagic)
	buf[4] = payloadVersion
	encoding.PutU16(buf, 5, uint16(len(v)))
	types.PutF32s(buf[7:], v)
	return buf, nil
}

// DecodePayload reads a vector payload. Malformed input fails closed.
func DecodePayload(raw []byte) ([]float32, error) {
	if len(raw) < 7 || !bytes.Equal(raw[0:4], []byte(payloadMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload magic")
	}
	if raw[4] != payloadVersion {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "unsupported vector payload version")
	}
	dim := encoding.U16(raw, 5)
	if dim == 0 || int(dim) > MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector dimension")
	}
	need := 7 + 4*int(dim)
	if len(raw) != need {
		return nil, nerr.New(nerr.InvalidFormat, "vector.DecodePayload", "bad vector payload length")
	}
	out := types.F32s(raw[7:])
	if err := Check(out, int(dim)); err != nil {
		return nil, err
	}
	return out, nil
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
}

// EncodeMeta writes an HNSW header.
func EncodeMeta(m Meta) ([]byte, error) {
	if m.Dim == 0 || int(m.Dim) > MaxDim {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeMeta", "bad HNSW dimension")
	}
	if m.Metric != MetricCosine && m.Metric != MetricL2 && m.Metric != MetricIP {
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
	buf := make([]byte, 4+1+2+1+1+2+8+1+2+len(m.Entry))
	copy(buf[0:4], metaMagic)
	buf[4] = metaVersion
	encoding.PutU16(buf, 5, m.Dim)
	buf[7] = byte(m.Metric)
	buf[8] = m.M
	encoding.PutU16(buf, 9, m.EfConstruct)
	encoding.PutU64(buf, 11, m.Count)
	buf[19] = m.MaxLevel
	encoding.PutU16(buf, 20, uint16(len(m.Entry)))
	copy(buf[22:], m.Entry)
	return buf, nil
}

// DecodeMeta reads an HNSW header. Malformed input fails closed.
func DecodeMeta(raw []byte) (Meta, error) {
	if len(raw) < 22 || !bytes.Equal(raw[0:4], []byte(metaMagic)) {
		return Meta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeMeta", "bad HNSW meta magic")
	}
	if raw[4] != metaVersion {
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
	if len(raw) != 22+int(n) {
		return Meta{}, nerr.New(nerr.InvalidFormat, "vector.DecodeMeta", "bad HNSW meta length")
	}
	if n > 0 {
		m.Entry = append([]byte(nil), raw[22:]...)
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

// EncodeNode writes an HNSW vertex.
func EncodeNode(n Node) ([]byte, error) {
	if n.Level > maxLvl {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "bad HNSW node level")
	}
	layers := int(n.Level) + 1
	if len(n.Neighbors) != layers {
		return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "HNSW neighbor layers mismatch")
	}
	size := 4
	for _, layer := range n.Neighbors {
		if len(layer) > 2*maxM {
			return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "too many HNSW neighbors")
		}
		size += 2
		for _, pk := range layer {
			if len(pk) > 4096 {
				return nil, nerr.New(nerr.InvalidFormat, "vector.EncodeNode", "HNSW neighbor key too long")
			}
			size += 2 + len(pk)
		}
	}
	buf := make([]byte, size)
	buf[0] = nodeVersion
	buf[1] = n.Level
	if n.Deleted {
		buf[2] = 1
	}
	buf[3] = uint8(layers)
	off := 4
	for _, layer := range n.Neighbors {
		encoding.PutU16(buf, off, uint16(len(layer)))
		off += 2
		for _, pk := range layer {
			encoding.PutU16(buf, off, uint16(len(pk)))
			off += 2
			copy(buf[off:], pk)
			off += len(pk)
		}
	}
	return buf, nil
}

// DecodeNode reads an HNSW vertex. Malformed input fails closed.
func DecodeNode(raw []byte) (Node, error) {
	if len(raw) < 4 || raw[0] != nodeVersion {
		return Node{}, nerr.New(nerr.InvalidFormat, "vector.DecodeNode", "bad HNSW node")
	}
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

// DefaultMeta returns construction parameters. Metric defaults to COSINE.
func DefaultMeta(dim uint16, metric Metric) Meta {
	if metric == MetricInvalid {
		metric = MetricCosine
	}
	return Meta{Dim: dim, Metric: metric, M: 16, EfConstruct: 64}
}
