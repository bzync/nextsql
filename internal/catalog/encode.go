package catalog

import (
	"bytes"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/upgrade/compat"
)

const (
	tableMagic      = "NSCT"
	tableVersion    = 12
	tableVersionV1  = 1
	tableVersionV2  = 2
	tableVersionV3  = 3
	tableVersionV4  = 4
	tableVersionV5  = 5
	tableVersionV6  = 6
	tableVersionV7  = 7
	tableVersionV8  = 8
	tableVersionV9  = 9
	tableVersionV10 = 10
	tableVersionV11 = 11
	tableVersionV12 = 12
	// KeyTable prefixes durable table descriptors in the catalog tree.
	KeyTable byte = 'T'
	// KeyStats prefixes durable table statistics in the catalog tree.
	KeyStats byte = 'S'
	// KeyAI prefixes per-column AUTOINCREMENT high-water values in the catalog tree.
	KeyAI byte = 'A'
	// KeyPartitionStats prefixes per-physical-partition ANALYZE sketches. The
	// table/partition identities in the key are immutable, so table renames do
	// not require rewriting these records.
	KeyPartitionStats byte = 'J'
)

func TableKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeyTable
	copy(k[1:], name)
	return k
}

func StatsKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeyStats
	copy(k[1:], name)
	return k
}

// PartitionStatsKey addresses one versioned partition statistics record.
func PartitionStatsKey(tableID, partitionID uint32) []byte {
	k := make([]byte, 1+4+4)
	k[0] = KeyPartitionStats
	encoding.PutU32(k, 1, tableID)
	encoding.PutU32(k, 5, partitionID)
	return k
}

// PartitionStatsRange returns the catalog-key interval for one stable table
// identity. It remains valid for the maximum uint32 table identity.
func PartitionStatsRange(tableID uint32) ([]byte, []byte) {
	start := make([]byte, 1+4)
	start[0] = KeyPartitionStats
	encoding.PutU32(start, 1, tableID)
	if tableID == ^uint32(0) {
		return start, []byte{KeyPartitionStats + 1}
	}
	end := make([]byte, 1+4)
	end[0] = KeyPartitionStats
	encoding.PutU32(end, 1, tableID+1)
	return start, end
}

// AIKey is the catalog-tree key for the next AI() value of one column.
func AIKey(tableID uint32, col string) []byte {
	k := make([]byte, 1+4+2+len(col))
	k[0] = KeyAI
	encoding.PutU32(k, 1, tableID)
	encoding.PutU16(k, 5, uint16(len(col)))
	copy(k[7:], col)
	return k
}

func EncodeTable(t *Table) ([]byte, error) {
	if t.CDCImages != CDCImagesKeys && t.CDCImages != CDCImagesFull {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeTable", "invalid CDC image mode")
	}
	if err := ValidatePartitioning(t); err != nil {
		return nil, err
	}
	if err := validateClientTable(t); err != nil {
		return nil, err
	}
	var buf []byte
	buf = append(buf, tableMagic...)
	buf = appendU16(buf, tableVersion)
	buf = appendU32(buf, t.ID)
	buf = appendString(buf, t.Name)
	buf = appendU64(buf, uint64(t.HeapMeta))
	buf = appendU16(buf, uint16(len(t.PK)))
	for _, i := range t.PK {
		buf = appendU16(buf, uint16(i))
	}
	buf = appendU16(buf, uint16(len(t.Columns)))
	for _, c := range t.Columns {
		if !validClientColumn(c) {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeTable", "invalid ENCRYPTED CLIENT column metadata")
		}
		var err error
		buf, err = appendColumn(buf, c)
		if err != nil {
			return nil, err
		}
	}
	buf = appendU16(buf, uint16(len(t.Indexes)))
	for _, idx := range t.Indexes {
		var err error
		buf, err = appendIndex(buf, idx)
		if err != nil {
			return nil, err
		}
	}
	buf = appendU64(buf, uint64(t.VecMeta))
	buf = appendU16(buf, uint16(len(t.ForeignKeys)))
	for _, fk := range t.ForeignKeys {
		buf = appendFK(buf, fk)
	}
	buf = append(buf, byte(t.CDCImages))
	buf, err := appendPartitioning(buf, t)
	if err != nil {
		return nil, err
	}
	// v6: one HNSW traversal-quantisation byte per index, in Indexes order.
	for _, idx := range t.Indexes {
		buf = append(buf, idx.VecQuant)
	}
	// v7: per index, the vector ANN method plus IVF list/probe counts.
	for _, idx := range t.Indexes {
		buf = append(buf, idx.VecMethod)
		buf = appendU32(buf, idx.IVFLists)
		buf = appendU32(buf, idx.IVFProbes)
	}
	// v8: per index, the IVF-PQ product-quantisation subspace count.
	for _, idx := range t.Indexes {
		buf = appendU32(buf, idx.IVFSubspaces)
	}
	// v9: per index, the full-text analyzer id + revision.
	for _, idx := range t.Indexes {
		if !validFTMeta(idx) {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeTable", "invalid full-text analyzer")
		}
		buf = append(buf, idx.FTAnalyzer)
		buf = appendU16(buf, idx.FTVersion)
	}
	// v10: one bounded client-encryption flag and logical type per column.
	// Physical row values remain STRING ciphertext and never contain plaintext.
	for _, col := range t.Columns {
		if !col.ClientEncrypted() {
			buf = append(buf, 0)
			continue
		}
		buf = append(buf, 1)
		buf = appendType(buf, col.ClientType)
	}
	// v11: per column, an ENUM label list (empty for every non-ENUM column).
	for _, col := range t.Columns {
		if col.Type.Kind != types.KindEnum {
			buf = appendU16(buf, 0)
			continue
		}
		buf = appendU16(buf, uint16(len(col.Type.EnumLabels)))
		for _, l := range col.Type.EnumLabels {
			buf = appendString(buf, l)
		}
	}
	// v12: per column, a recursive collection descriptor (STRUCT / ARRAY /
	// MAP). A leading 0 byte marks a non-collection column and carries no
	// further bytes; a leading 1 byte is followed by the full recursive Type
	// (docs/design-collections.md).
	for _, col := range t.Columns {
		if !types.IsCollection(col.Type.Kind) {
			buf = append(buf, 0)
			continue
		}
		buf = append(buf, 1)
		buf = appendTypeRec(buf, col.Type)
	}
	return buf, nil
}

// appendTypeRec serialises a full recursive Type, including a collection's
// nested element/key/field descriptors and an ENUM's label list. Mirrors
// takeTypeRec.
func appendTypeRec(buf []byte, t types.Type) []byte {
	buf = append(buf, byte(t.Kind), t.VecElem)
	buf = appendU16(buf, t.Precision)
	buf = appendU16(buf, t.Scale)
	switch t.Kind {
	case types.KindEnum:
		buf = appendU16(buf, uint16(len(t.EnumLabels)))
		for _, l := range t.EnumLabels {
			buf = appendString(buf, l)
		}
	case types.KindArray:
		if len(t.Elem) == 1 {
			buf = appendTypeRec(buf, t.Elem[0])
		}
	case types.KindMap:
		if len(t.Key) == 1 {
			buf = appendTypeRec(buf, t.Key[0])
		}
		if len(t.Elem) == 1 {
			buf = appendTypeRec(buf, t.Elem[0])
		}
	case types.KindStruct:
		buf = appendU16(buf, uint16(len(t.Fields)))
		for _, f := range t.Fields {
			buf = appendString(buf, f.Name)
			buf = appendTypeRec(buf, f.Type)
		}
	}
	return buf
}

// takeTypeRec is the untrusted-decoder inverse of appendTypeRec. depth bounds
// recursion at types.MaxNestDepth; every reconstructed collection/ENUM type is
// re-validated through its own constructor rather than trusted as read.
func takeTypeRec(raw []byte, off, depth int) (types.Type, int, error) {
	if depth > types.MaxNestDepth+1 {
		return types.Type{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeTypeRec", "collection type nesting too deep")
	}
	if off+6 > len(raw) {
		return types.Type{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeTypeRec", "truncated type")
	}
	kind := types.Kind(raw[off])
	elem := raw[off+1]
	off += 2
	prec, off, err := takeU16(raw, off)
	if err != nil {
		return types.Type{}, 0, err
	}
	scale, off, err := takeU16(raw, off)
	if err != nil {
		return types.Type{}, 0, err
	}
	base := types.Type{Kind: kind, VecElem: elem, Precision: prec, Scale: scale}
	switch kind {
	case types.KindEnum:
		var n uint16
		n, off, err = takeU16(raw, off)
		if err != nil {
			return types.Type{}, 0, err
		}
		if n > types.MaxEnumLabels {
			return types.Type{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeTypeRec", "ENUM label count exceeds limit")
		}
		labels := make([]string, n)
		for i := range labels {
			labels[i], off, err = takeString(raw, off)
			if err != nil {
				return types.Type{}, 0, err
			}
		}
		et, eerr := types.EnumType(labels)
		if eerr != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.InvalidFormat, "catalog.takeTypeRec", "invalid ENUM labels", eerr)
		}
		return et, off, nil
	case types.KindArray:
		var e types.Type
		e, off, err = takeTypeRec(raw, off, depth+1)
		if err != nil {
			return types.Type{}, 0, err
		}
		at, aerr := types.ArrayType(e)
		if aerr != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.InvalidFormat, "catalog.takeTypeRec", "invalid ARRAY type", aerr)
		}
		return at, off, nil
	case types.KindMap:
		var k, val types.Type
		k, off, err = takeTypeRec(raw, off, depth+1)
		if err != nil {
			return types.Type{}, 0, err
		}
		val, off, err = takeTypeRec(raw, off, depth+1)
		if err != nil {
			return types.Type{}, 0, err
		}
		mt, merr := types.MapType(k, val)
		if merr != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.InvalidFormat, "catalog.takeTypeRec", "invalid MAP type", merr)
		}
		return mt, off, nil
	case types.KindStruct:
		var n uint16
		n, off, err = takeU16(raw, off)
		if err != nil {
			return types.Type{}, 0, err
		}
		if n == 0 || n > types.MaxStructFields {
			return types.Type{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeTypeRec", "STRUCT field count out of range")
		}
		fields := make([]types.Field, n)
		for i := range fields {
			fields[i].Name, off, err = takeString(raw, off)
			if err != nil {
				return types.Type{}, 0, err
			}
			fields[i].Type, off, err = takeTypeRec(raw, off, depth+1)
			if err != nil {
				return types.Type{}, 0, err
			}
		}
		st, serr := types.StructType(fields)
		if serr != nil {
			return types.Type{}, 0, nerr.Wrap(nerr.InvalidFormat, "catalog.takeTypeRec", "invalid STRUCT type", serr)
		}
		return st, off, nil
	default:
		return base, off, nil
	}
}

func validClientColumn(c Column) bool {
	if !c.ClientEncrypted() {
		return c.ClientType.Equals(types.Type{})
	}
	if c.Type.Kind != types.KindString || c.Type.Precision != 0 || c.Type.Scale != 0 || c.Type.VecElem != 0 {
		return false
	}
	if c.Primary || c.Default.Kind != DefNone {
		return false
	}
	return clientenc.SupportedType(c.ClientType)
}

func validateClientTable(t *Table) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "nil table")
	}
	for ord, col := range t.Columns {
		if !validClientColumn(col) {
			return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "invalid ENCRYPTED CLIENT column metadata")
		}
		if !col.ClientEncrypted() {
			continue
		}
		for _, pk := range t.PK {
			if pk == ord {
				return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "ENCRYPTED CLIENT column cannot be a primary key")
			}
		}
		for _, fk := range t.ForeignKeys {
			for _, c := range fk.Columns {
				if c == ord {
					return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "ENCRYPTED CLIENT column cannot be a foreign key")
				}
			}
		}
		for _, idx := range t.Indexes {
			for _, c := range append(append([]int(nil), idx.Columns...), idx.Include...) {
				if c == ord {
					return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "ENCRYPTED CLIENT column cannot be indexed")
				}
			}
			if ExprUsesIdent(idx.Predicate, col.Name) {
				return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "ENCRYPTED CLIENT column cannot be used in an index predicate")
			}
			for _, expr := range idx.Exprs {
				if ExprUsesIdent(expr, col.Name) {
					return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "ENCRYPTED CLIENT column cannot be used in an index expression")
				}
			}
		}
		if t.Partitioning != nil {
			for _, c := range t.Partitioning.Columns {
				if c == ord {
					return nerr.New(nerr.InvalidArgument, "catalog.clientenc", "ENCRYPTED CLIENT column cannot be a partition key")
				}
			}
		}
	}
	return nil
}

func validFTMeta(idx Index) bool {
	if !idx.Fulltext {
		return idx.FTAnalyzer == 0 && idx.FTVersion == 0
	}
	switch idx.FTAnalyzer {
	case FTAnalyzerSimple:
		return idx.FTVersion <= 1
	case FTAnalyzerEnglish:
		return idx.FTVersion == FTAnalyzerEnglishV1 || idx.FTVersion == FTAnalyzerEnglishV2 || idx.FTVersion == FTAnalyzerEnglishV3
	case FTAnalyzerFrench:
		return idx.FTVersion == FTAnalyzerFrenchV1
	case FTAnalyzerGerman:
		return idx.FTVersion == FTAnalyzerGermanV1
	case FTAnalyzerSpanish:
		return idx.FTVersion == FTAnalyzerSpanishV1
	default:
		return false
	}
}

func DecodeTable(raw []byte) (*Table, error) {
	if len(raw) < 4 || !bytes.Equal(raw[:4], []byte(tableMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "bad catalog magic")
	}
	off := 4
	ver, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if err := compat.Check(compat.FamilyCatalog, ver); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, "catalog.DecodeTable", "unsupported catalog version", err)
	}
	t := &Table{}
	t.ID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	t.Name, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	var heap uint64
	heap, off, err = takeU64(raw, off)
	if err != nil {
		return nil, err
	}
	t.HeapMeta = format.PageID(heap)
	var n uint16
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var ord uint16
		ord, off, err = takeU16(raw, off)
		if err != nil {
			return nil, err
		}
		t.PK = append(t.PK, int(ord))
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var c Column
		c, off, err = takeColumn(raw, off)
		if err != nil {
			return nil, err
		}
		t.Columns = append(t.Columns, c)
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var idx Index
		idx, off, err = takeIndex(raw, off)
		if err != nil {
			return nil, err
		}
		t.Indexes = append(t.Indexes, idx)
	}
	if ver == tableVersionV1 {
		if off < len(raw) {
			var vm uint64
			vm, off, err = takeU64(raw, off)
			if err != nil {
				return nil, err
			}
			t.VecMeta = format.PageID(vm)
		}
		return t, nil
	}
	var vm uint64
	vm, off, err = takeU64(raw, off)
	if err != nil {
		return nil, err
	}
	t.VecMeta = format.PageID(vm)
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	for i := 0; i < int(n); i++ {
		var fk ForeignKey
		fk, off, err = takeFK(raw, off)
		if err != nil {
			return nil, err
		}
		t.ForeignKeys = append(t.ForeignKeys, fk)
	}
	if ver == tableVersionV2 {
		if off != len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "trailing catalog bytes")
		}
		return t, nil
	}
	if off >= len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "truncated CDC image mode")
	}
	t.CDCImages = CDCImageMode(raw[off])
	off++
	if t.CDCImages != CDCImagesKeys && t.CDCImages != CDCImagesFull {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "unknown CDC image mode")
	}
	if ver == tableVersionV3 {
		if off != len(raw) {
			return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "trailing catalog bytes")
		}
		return t, nil
	}
	t.Partitioning, off, err = takePartitioning(raw, off, t, ver)
	if err != nil {
		return nil, err
	}
	if err := ValidatePartitioning(t); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, "catalog.DecodeTable", "invalid partition descriptor", err)
	}
	if ver >= tableVersionV6 {
		for i := range t.Indexes {
			if off >= len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "truncated index quantisation")
			}
			q := raw[off]
			off++
			if q != 0 && q != types.VecF16 && q != types.VecI8 {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "bad index quantisation")
			}
			t.Indexes[i].VecQuant = q
		}
	}
	if ver >= tableVersionV7 {
		for i := range t.Indexes {
			if off >= len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "truncated index method")
			}
			m := raw[off]
			off++
			if m != VecMethodHNSW && m != VecMethodIVF && m != VecMethodIVFPQ && m != VecMethodSPARSE {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "bad vector index method")
			}
			if m == VecMethodIVFPQ && ver < tableVersionV8 {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "IVF-PQ method requires catalog format v8")
			}
			if m == VecMethodSPARSE && ver < tableVersionV7 {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "SPARSE method requires catalog format v7")
			}
			var lists, probes uint32
			lists, off, err = takeU32(raw, off)
			if err != nil {
				return nil, err
			}
			probes, off, err = takeU32(raw, off)
			if err != nil {
				return nil, err
			}
			if m == VecMethodIVF || m == VecMethodIVFPQ {
				if !t.Indexes[i].Vector {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "IVF method on a non-vector index")
				}
				if lists == 0 || lists > MaxVectorIndexLists || probes > lists {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "bad IVF list/probe count")
				}
			} else if lists != 0 || probes != 0 {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "non-IVF index carries IVF parameters")
			}
			if m == VecMethodSPARSE {
				if !t.Indexes[i].Vector {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "SPARSE method on a non-vector index")
				}
				col := t.Indexes[i].Columns
				if len(col) != 1 || col[0] < 0 || col[0] >= len(t.Columns) || t.Columns[col[0]].Type.VecElem != types.VecSparse {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "SPARSE index requires a SPARSEVECTOR column")
				}
			}
			t.Indexes[i].VecMethod = m
			t.Indexes[i].IVFLists = lists
			t.Indexes[i].IVFProbes = probes
		}
	}
	if ver >= tableVersionV8 {
		for i := range t.Indexes {
			var subs uint32
			subs, off, err = takeU32(raw, off)
			if err != nil {
				return nil, err
			}
			if t.Indexes[i].VecMethod == VecMethodIVFPQ {
				dim := indexVectorDim(t, i)
				if subs == 0 || subs > MaxVectorIndexSubspaces || dim == 0 || dim%int(subs) != 0 {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "bad IVF-PQ subspace count")
				}
			} else if subs != 0 {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "non-IVFPQ index carries a subspace count")
			}
			t.Indexes[i].IVFSubspaces = subs
		}
	}
	if ver >= tableVersionV9 {
		for i := range t.Indexes {
			if off >= len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "truncated full-text analyzer")
			}
			id := raw[off]
			off++
			var veru uint16
			veru, off, err = takeU16(raw, off)
			if err != nil {
				return nil, err
			}
			t.Indexes[i].FTAnalyzer = id
			t.Indexes[i].FTVersion = veru
			if !validFTMeta(t.Indexes[i]) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "bad full-text analyzer")
			}
		}
	}
	if ver >= tableVersionV10 {
		for i := range t.Columns {
			if off >= len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "truncated ENCRYPTED CLIENT metadata")
			}
			flag := raw[off]
			off++
			switch flag {
			case 0:
			case 1:
				var typ types.Type
				typ, off, err = takeType(raw, off)
				if err != nil {
					return nil, err
				}
				t.Columns[i].ClientType = typ
			default:
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "unknown ENCRYPTED CLIENT flag")
			}
			if !validClientColumn(t.Columns[i]) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "invalid ENCRYPTED CLIENT column metadata")
			}
		}
		if err := validateClientTable(t); err != nil {
			return nil, nerr.Wrap(nerr.InvalidFormat, "catalog.DecodeTable", "invalid ENCRYPTED CLIENT table", err)
		}
	}
	if ver >= tableVersionV11 {
		for i := range t.Columns {
			var n uint16
			n, off, err = takeU16(raw, off)
			if err != nil {
				return nil, err
			}
			if n == 0 {
				continue
			}
			if n > types.MaxEnumLabels {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "ENUM label count exceeds limit")
			}
			labels := make([]string, n)
			for j := range labels {
				labels[j], off, err = takeString(raw, off)
				if err != nil {
					return nil, err
				}
			}
			et, eerr := types.EnumType(labels)
			if eerr != nil {
				return nil, nerr.Wrap(nerr.InvalidFormat, "catalog.DecodeTable", "invalid ENUM labels", eerr)
			}
			if t.Columns[i].Type.Kind != types.KindEnum {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "ENUM labels on a non-ENUM column")
			}
			t.Columns[i].Type = et
		}
	}
	if ver >= tableVersionV12 {
		for i := range t.Columns {
			if off >= len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "truncated collection descriptor")
			}
			flag := raw[off]
			off++
			switch flag {
			case 0:
				if types.IsCollection(t.Columns[i].Type.Kind) {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "collection column missing its descriptor")
				}
			case 1:
				var ct types.Type
				ct, off, err = takeTypeRec(raw, off, 0)
				if err != nil {
					return nil, err
				}
				if !types.IsCollection(ct.Kind) || ct.Kind != t.Columns[i].Type.Kind {
					return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "collection descriptor Kind mismatch")
				}
				t.Columns[i].Type = ct
			default:
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "unknown collection descriptor flag")
			}
		}
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeTable", "trailing catalog bytes")
	}
	return t, nil
}

// indexVectorDim returns the declared dimension of index i's vector column, or 0
// when the index is not a single-column vector index.
func indexVectorDim(t *Table, i int) int {
	if i < 0 || i >= len(t.Indexes) {
		return 0
	}
	idx := t.Indexes[i]
	if !idx.Vector || len(idx.Columns) != 1 {
		return 0
	}
	c := idx.Columns[0]
	if c < 0 || c >= len(t.Columns) {
		return 0
	}
	return int(t.Columns[c].Type.Precision)
}

func appendPartitioning(buf []byte, t *Table) ([]byte, error) {
	if t.Partitioning == nil {
		return append(buf, byte(PartitionNone)), nil
	}
	p := t.Partitioning
	buf = append(buf, byte(p.Kind))
	buf = appendU16(buf, uint16(len(p.Columns)))
	for _, ord := range p.Columns {
		buf = appendU16(buf, uint16(ord))
	}
	buf = appendU16(buf, uint16(len(p.Partitions)))
	for _, part := range p.Partitions {
		buf = appendU32(buf, part.ID)
		buf = appendString(buf, part.Name)
		buf = appendU64(buf, uint64(part.HeapMeta))
		buf = appendU64(buf, uint64(part.VecMeta))
		flags := byte(0)
		if part.LowerInclusive {
			flags |= 1
		}
		if part.UpperInclusive {
			flags |= 2
		}
		buf = append(buf, flags)
		buf = appendU32(buf, part.Modulus)
		buf = appendU32(buf, part.Remainder)
		buf = appendU16(buf, uint16(len(part.Indexes)))
		for _, idx := range part.Indexes {
			buf = appendString(buf, idx.Name)
			buf = appendU64(buf, uint64(idx.Meta))
		}
		buf = appendU32(buf, uint32(len(part.Values)))
		for _, tuple := range part.Values {
			if tuple == nil {
				buf = append(buf, 0)
				continue
			}
			raw, err := types.EncodeRow(tuple)
			if err != nil {
				return nil, err
			}
			buf = append(buf, 1)
			buf = appendBytes(buf, raw)
		}
	}
	nextID := p.NextID
	if nextID == 0 {
		for _, part := range p.Partitions {
			if part.ID >= nextID {
				nextID = part.ID + 1
			}
		}
	}
	buf = appendU32(buf, nextID)
	return buf, nil
}

func takePartitioning(raw []byte, off int, t *Table, version uint16) (*Partitioning, int, error) {
	if off >= len(raw) {
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "truncated partition kind")
	}
	kind := PartitionKind(raw[off])
	off++
	if kind == PartitionNone {
		return nil, off, nil
	}
	if kind < PartitionRange || kind > PartitionLegacyTenant {
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "unknown partition kind")
	}
	p := &Partitioning{Kind: kind}
	var err error
	var n uint16
	n, off, err = takeU16(raw, off)
	if err != nil || n == 0 || n > MaxPartitionColumns {
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "partition column count")
	}
	for i := 0; i < int(n); i++ {
		var ord uint16
		ord, off, err = takeU16(raw, off)
		if err != nil {
			return nil, 0, err
		}
		p.Columns = append(p.Columns, int(ord))
	}
	t.Partitioning = p
	tupleTypes := partitionTupleTypes(t)
	n, off, err = takeU16(raw, off)
	if err != nil || n == 0 || n > MaxPartitions {
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "partition count")
	}
	totalValues := 0
	for i := 0; i < int(n); i++ {
		var part Partition
		part.ID, off, err = takeU32(raw, off)
		if err != nil {
			return nil, 0, err
		}
		part.Name, off, err = takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		var meta uint64
		meta, off, err = takeU64(raw, off)
		if err != nil {
			return nil, 0, err
		}
		part.HeapMeta = format.PageID(meta)
		meta, off, err = takeU64(raw, off)
		if err != nil {
			return nil, 0, err
		}
		part.VecMeta = format.PageID(meta)
		if off >= len(raw) || raw[off]&^byte(3) != 0 {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "partition flags")
		}
		part.LowerInclusive = raw[off]&1 != 0
		part.UpperInclusive = raw[off]&2 != 0
		off++
		part.Modulus, off, err = takeU32(raw, off)
		if err != nil {
			return nil, 0, err
		}
		part.Remainder, off, err = takeU32(raw, off)
		if err != nil {
			return nil, 0, err
		}
		var ni uint16
		ni, off, err = takeU16(raw, off)
		if err != nil || int(ni) > len(t.Indexes) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "partition index count")
		}
		for j := 0; j < int(ni); j++ {
			var idx PartitionIndex
			idx.Name, off, err = takeString(raw, off)
			if err != nil {
				return nil, 0, err
			}
			meta, off, err = takeU64(raw, off)
			if err != nil {
				return nil, 0, err
			}
			idx.Meta = format.PageID(meta)
			part.Indexes = append(part.Indexes, idx)
		}
		var nv uint32
		nv, off, err = takeU32(raw, off)
		if err != nil || nv > MaxPartitionValues || totalValues+int(nv) > MaxPartitionValues {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "partition value count")
		}
		totalValues += int(nv)
		for j := 0; j < int(nv); j++ {
			if off >= len(raw) {
				return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "truncated partition tuple")
			}
			present := raw[off]
			off++
			if present == 0 {
				part.Values = append(part.Values, nil)
				continue
			}
			if present != 1 {
				return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "partition tuple marker")
			}
			var body []byte
			body, off, err = takeBytes(raw, off)
			if err != nil {
				return nil, 0, err
			}
			tuple, err := types.DecodeRow(body, tupleTypes)
			if err != nil {
				return nil, 0, nerr.Wrap(nerr.InvalidFormat, "catalog.takePartitioning", "partition tuple", err)
			}
			part.Values = append(part.Values, tuple)
		}
		p.Partitions = append(p.Partitions, part)
	}
	if version >= tableVersionV5 {
		p.NextID, off, err = takeU32(raw, off)
		if err != nil {
			return nil, 0, err
		}
		if p.NextID == 0 {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takePartitioning", "zero next partition identity")
		}
	} else {
		for _, part := range p.Partitions {
			if part.ID >= p.NextID {
				p.NextID = part.ID + 1
			}
		}
	}
	return p, off, nil
}

func appendFK(buf []byte, fk ForeignKey) []byte {
	buf = appendString(buf, fk.Name)
	buf = appendU16(buf, uint16(len(fk.Columns)))
	for _, c := range fk.Columns {
		buf = appendU16(buf, uint16(c))
	}
	buf = appendString(buf, fk.RefTable)
	buf = appendU32(buf, fk.RefTableID)
	buf = appendU16(buf, uint16(len(fk.RefColumns)))
	for _, c := range fk.RefColumns {
		buf = appendU16(buf, uint16(c))
	}
	return append(buf, byte(fk.OnDelete), byte(fk.OnUpdate))
}

func takeFK(raw []byte, off int) (ForeignKey, int, error) {
	var fk ForeignKey
	var err error
	fk.Name, off, err = takeString(raw, off)
	if err != nil {
		return ForeignKey{}, 0, err
	}
	var n uint16
	n, off, err = takeU16(raw, off)
	if err != nil {
		return ForeignKey{}, 0, err
	}
	for i := 0; i < int(n); i++ {
		var ord uint16
		ord, off, err = takeU16(raw, off)
		if err != nil {
			return ForeignKey{}, 0, err
		}
		fk.Columns = append(fk.Columns, int(ord))
	}
	fk.RefTable, off, err = takeString(raw, off)
	if err != nil {
		return ForeignKey{}, 0, err
	}
	fk.RefTableID, off, err = takeU32(raw, off)
	if err != nil {
		return ForeignKey{}, 0, err
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return ForeignKey{}, 0, err
	}
	for i := 0; i < int(n); i++ {
		var ord uint16
		ord, off, err = takeU16(raw, off)
		if err != nil {
			return ForeignKey{}, 0, err
		}
		fk.RefColumns = append(fk.RefColumns, int(ord))
	}
	if off+2 > len(raw) {
		return ForeignKey{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeFK", "truncated actions")
	}
	fk.OnDelete = FKAction(raw[off])
	fk.OnUpdate = FKAction(raw[off+1])
	if !validFKAction(fk.OnDelete) || !validFKAction(fk.OnUpdate) {
		return ForeignKey{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeFK", "unknown foreign key action")
	}
	return fk, off + 2, nil
}

func appendColumn(buf []byte, c Column) ([]byte, error) {
	buf = appendString(buf, c.Name)
	buf = append(buf, byte(c.Type.Kind), c.Type.VecElem)
	buf = appendU16(buf, c.Type.Precision)
	buf = appendU16(buf, c.Type.Scale)
	flags := uint16(0)
	if c.NotNull {
		flags |= 1
	}
	if c.Primary {
		flags |= 2
	}
	buf = appendU16(buf, flags)
	buf = append(buf, c.Default.Kind)
	if c.Default.Kind == DefLiteral {
		raw, err := types.EncodeRow([]types.Value{c.Default.Literal})
		if err != nil {
			return nil, err
		}
		buf = appendBytes(buf, raw)
	}
	return buf, nil
}

func takeColumn(raw []byte, off int) (Column, int, error) {
	var c Column
	var err error
	c.Name, off, err = takeString(raw, off)
	if err != nil {
		return Column{}, 0, err
	}
	if off+2 > len(raw) {
		return Column{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeColumn", "truncated type")
	}
	c.Type.Kind = types.Kind(raw[off])
	c.Type.VecElem = raw[off+1]
	off += 2
	c.Type.Precision, off, err = takeU16(raw, off)
	if err != nil {
		return Column{}, 0, err
	}
	c.Type.Scale, off, err = takeU16(raw, off)
	if err != nil {
		return Column{}, 0, err
	}
	var flags uint16
	flags, off, err = takeU16(raw, off)
	if err != nil {
		return Column{}, 0, err
	}
	c.NotNull = flags&1 != 0
	c.Primary = flags&2 != 0
	if off >= len(raw) {
		return Column{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeColumn", "truncated default")
	}
	c.Default.Kind = raw[off]
	off++
	switch c.Default.Kind {
	case DefNone, DefUUID, DefNow, DefAI, DefLiteral:
	default:
		return Column{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeColumn", "unknown default kind")
	}
	if c.Default.Kind == DefLiteral {
		var body []byte
		body, off, err = takeBytes(raw, off)
		if err != nil {
			return Column{}, 0, err
		}
		vals, err := types.DecodeRow(body, []types.Type{c.Type})
		if err != nil {
			return Column{}, 0, err
		}
		c.Default.Literal = vals[0]
	}
	return c, off, nil
}

const (
	idxFlagUnique   = 1
	idxFlagSpatial  = 2
	idxFlagPath     = 4
	idxFlagFulltext = 8
	idxFlagVector   = 16
	idxFlagInclude  = 32
	idxFlagPred     = 64
	idxFlagExpr     = 128
)

func appendIndex(buf []byte, idx Index) ([]byte, error) {
	buf = appendString(buf, idx.Name)
	u := byte(0)
	if idx.Unique {
		u |= idxFlagUnique
	}
	if idx.Spatial {
		u |= idxFlagSpatial
	}
	if len(idx.Path) > 0 {
		u |= idxFlagPath
	}
	if idx.Fulltext {
		u |= idxFlagFulltext
	}
	if idx.Vector {
		u |= idxFlagVector
	}
	if len(idx.Include) > 0 {
		u |= idxFlagInclude
	}
	if idx.Predicate != nil {
		u |= idxFlagPred
	}
	if idx.HasExpr() {
		u |= idxFlagExpr
	}
	buf = append(buf, u)
	buf = appendU64(buf, uint64(idx.Meta))
	buf = appendU16(buf, uint16(len(idx.Columns)))
	for _, c := range idx.Columns {
		buf = appendU16(buf, uint16(c))
	}
	if u&idxFlagPath != 0 {
		buf = appendU16(buf, uint16(len(idx.Path)))
		for _, p := range idx.Path {
			buf = appendString(buf, p)
		}
	}
	if u&idxFlagInclude != 0 {
		buf = appendU16(buf, uint16(len(idx.Include)))
		for _, c := range idx.Include {
			buf = appendU16(buf, uint16(c))
		}
	}
	if u&idxFlagPred != 0 {
		var err error
		buf, err = appendExpr(buf, idx.Predicate)
		if err != nil {
			return nil, err
		}
	}
	if u&idxFlagExpr != 0 {
		buf = appendU16(buf, uint16(len(idx.Columns)))
		for i := range idx.Columns {
			if !idx.KeyIsExpr(i) {
				buf = append(buf, 0)
				continue
			}
			buf = append(buf, 1)
			var err error
			buf, err = appendExpr(buf, idx.Exprs[i])
			if err != nil {
				return nil, err
			}
			typ := types.Type{}
			if i < len(idx.ExprTypes) {
				typ = idx.ExprTypes[i]
			}
			buf = appendType(buf, typ)
		}
	}
	return buf, nil
}

func takeIndex(raw []byte, off int) (Index, int, error) {
	var idx Index
	var err error
	idx.Name, off, err = takeString(raw, off)
	if err != nil {
		return Index{}, 0, err
	}
	if off >= len(raw) {
		return Index{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeIndex", "truncated unique")
	}
	flags := raw[off]
	idx.Unique = flags&idxFlagUnique != 0
	idx.Spatial = flags&idxFlagSpatial != 0
	hasPath := flags&idxFlagPath != 0
	idx.Fulltext = flags&idxFlagFulltext != 0
	idx.Vector = flags&idxFlagVector != 0
	hasInclude := flags&idxFlagInclude != 0
	hasPred := flags&idxFlagPred != 0
	hasExpr := flags&idxFlagExpr != 0
	off++
	var meta uint64
	meta, off, err = takeU64(raw, off)
	if err != nil {
		return Index{}, 0, err
	}
	idx.Meta = format.PageID(meta)
	var n uint16
	n, off, err = takeU16(raw, off)
	if err != nil {
		return Index{}, 0, err
	}
	for i := 0; i < int(n); i++ {
		var c uint16
		c, off, err = takeU16(raw, off)
		if err != nil {
			return Index{}, 0, err
		}
		idx.Columns = append(idx.Columns, int(c))
	}
	if hasPath {
		n, off, err = takeU16(raw, off)
		if err != nil {
			return Index{}, 0, err
		}
		for i := 0; i < int(n); i++ {
			var part string
			part, off, err = takeString(raw, off)
			if err != nil {
				return Index{}, 0, err
			}
			idx.Path = append(idx.Path, part)
		}
	}
	if hasInclude {
		n, off, err = takeU16(raw, off)
		if err != nil {
			return Index{}, 0, err
		}
		for i := 0; i < int(n); i++ {
			var c uint16
			c, off, err = takeU16(raw, off)
			if err != nil {
				return Index{}, 0, err
			}
			idx.Include = append(idx.Include, int(c))
		}
	}
	if hasPred {
		idx.Predicate, off, err = takeExpr(raw, off)
		if err != nil {
			return Index{}, 0, err
		}
	}
	if hasExpr {
		n, off, err = takeU16(raw, off)
		if err != nil {
			return Index{}, 0, err
		}
		if int(n) != len(idx.Columns) {
			return Index{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeIndex", "expression key count")
		}
		idx.Exprs = make([]ast.Expr, n)
		idx.ExprTypes = make([]types.Type, n)
		for i := 0; i < int(n); i++ {
			if off >= len(raw) {
				return Index{}, 0, nerr.New(nerr.InvalidFormat, "catalog.takeIndex", "truncated expression key")
			}
			if raw[off] == 0 {
				off++
				continue
			}
			off++
			idx.Exprs[i], off, err = takeExpr(raw, off)
			if err != nil {
				return Index{}, 0, err
			}
			idx.ExprTypes[i], off, err = takeType(raw, off)
			if err != nil {
				return Index{}, 0, err
			}
		}
	}
	return idx, off, nil
}

func appendString(buf []byte, s string) []byte {
	return appendBytes(buf, []byte(s))
}

func appendBytes(buf []byte, b []byte) []byte {
	buf = appendU16(buf, uint16(len(b)))
	return append(buf, b...)
}

func appendU16(buf []byte, v uint16) []byte {
	var tmp [2]byte
	encoding.PutU16(tmp[:], 0, v)
	return append(buf, tmp[:]...)
}

func appendU32(buf []byte, v uint32) []byte {
	var tmp [4]byte
	encoding.PutU32(tmp[:], 0, v)
	return append(buf, tmp[:]...)
}

func appendU64(buf []byte, v uint64) []byte {
	var tmp [8]byte
	encoding.PutU64(tmp[:], 0, v)
	return append(buf, tmp[:]...)
}

func takeString(b []byte, off int) (string, int, error) {
	raw, off, err := takeBytes(b, off)
	return string(raw), off, err
}

func takeBytes(b []byte, off int) ([]byte, int, error) {
	n, off, err := takeU16(b, off)
	if err != nil {
		return nil, 0, err
	}
	raw, err := encoding.ReadBytes(b, off, int(n))
	if err != nil {
		return nil, 0, err
	}
	return raw, off + int(n), nil
}

func takeU16(b []byte, off int) (uint16, int, error) {
	v, err := encoding.ReadU16(b, off)
	return v, off + 2, err
}

func takeU32(b []byte, off int) (uint32, int, error) {
	v, err := encoding.ReadU32(b, off)
	return v, off + 4, err
}

func takeU64(b []byte, off int) (uint64, int, error) {
	v, err := encoding.ReadU64(b, off)
	return v, off + 8, err
}
