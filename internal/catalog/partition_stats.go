package catalog

import (
	"bytes"
	"math"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	partitionStatsMagic   = "NSPS"
	partitionStatsVersion = 1

	// Each NSPS record is one catalog value. Keep every repeated block well
	// below the catalog page/record limit even for adversarial schemas.
	MaxPartitionSketchColumns = 64
	MaxPartitionSketchIndexes = 64
	MaxPartitionSketchVectors = 64
	MaxPartitionStatsBytes    = 15 * 1024
)

// EncodePartitionStats writes the compact per-partition ANALYZE record. Its
// column sketches intentionally omit histograms/MCVs; those remain in the
// global NSST record while NSPS supplies local NULL/NDV/min/max/correlation,
// index selectivity, and vector population for pruning-aware costing.
func EncodePartitionStats(tableID uint32, snapshot [32]byte, s PartitionStats) ([]byte, error) {
	if tableID == 0 || s.ID == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "zero statistics identity")
	}
	if len(s.Columns) > MaxPartitionSketchColumns || len(s.Indexes) > MaxPartitionSketchIndexes || len(s.Vectors) > MaxPartitionSketchVectors {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "partition sketch count")
	}
	var buf []byte
	buf = append(buf, partitionStatsMagic...)
	buf = appendU16(buf, partitionStatsVersion)
	buf = appendU32(buf, tableID)
	buf = appendU32(buf, s.ID)
	buf = appendU64(buf, s.Rows)
	buf = append(buf, snapshot[:]...)

	seenColumns := make(map[int]struct{}, len(s.Columns))
	buf = appendU16(buf, uint16(len(s.Columns)))
	for _, col := range s.Columns {
		if col.Ord < 0 || col.Ord > int(^uint16(0)) || len(col.Histogram) != 0 || len(col.MCV) != 0 {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "invalid partition column sketch")
		}
		if _, exists := seenColumns[col.Ord]; exists {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "duplicate partition column sketch")
		}
		seenColumns[col.Ord] = struct{}{}
		var err error
		buf, err = appendColStats(buf, col)
		if err != nil {
			return nil, err
		}
	}

	seenIndexes := make(map[string]struct{}, len(s.Indexes))
	buf = appendU16(buf, uint16(len(s.Indexes)))
	for _, idx := range s.Indexes {
		if idx.Name == "" {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "empty partition index sketch name")
		}
		if _, exists := seenIndexes[idx.Name]; exists {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "duplicate partition index sketch")
		}
		seenIndexes[idx.Name] = struct{}{}
		buf = appendIndexStats(buf, idx)
	}

	seenVectors := make(map[int]struct{}, len(s.Vectors))
	buf = appendU16(buf, uint16(len(s.Vectors)))
	for _, vector := range s.Vectors {
		if vector.Ord < 0 || vector.Ord > int(^uint16(0)) {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "invalid partition vector sketch")
		}
		if _, exists := seenVectors[vector.Ord]; exists {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodePartitionStats", "duplicate partition vector sketch")
		}
		seenVectors[vector.Ord] = struct{}{}
		buf = appendVecStats(buf, vector)
	}
	if len(buf) > MaxPartitionStatsBytes {
		return nil, nerr.New(nerr.Exhausted, "catalog.EncodePartitionStats", "partition statistics record exceeds byte limit")
	}
	return buf, nil
}

func DecodePartitionStats(raw []byte) (uint32, [32]byte, PartitionStats, error) {
	var snapshot [32]byte
	if len(raw) > MaxPartitionStatsBytes {
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "partition statistics record exceeds byte limit")
	}
	if len(raw) < 4 || !bytes.Equal(raw[:4], []byte(partitionStatsMagic)) {
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "bad partition stats magic")
	}
	off := 4
	version, next, err := takeU16(raw, off)
	if err != nil {
		return 0, snapshot, PartitionStats{}, err
	}
	off = next
	if version != partitionStatsVersion {
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "unsupported partition stats version")
	}
	tableID, off, err := takeU32(raw, off)
	if err != nil {
		return 0, snapshot, PartitionStats{}, err
	}
	var out PartitionStats
	out.ID, off, err = takeU32(raw, off)
	if err != nil {
		return 0, snapshot, PartitionStats{}, err
	}
	out.Rows, off, err = takeU64(raw, off)
	if err != nil {
		return 0, snapshot, PartitionStats{}, err
	}
	if off+len(snapshot) > len(raw) {
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "truncated statistics snapshot")
	}
	copy(snapshot[:], raw[off:off+len(snapshot)])
	off += len(snapshot)
	if tableID == 0 || out.ID == 0 {
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "zero statistics identity")
	}

	var count uint16
	count, off, err = takeU16(raw, off)
	if err != nil || int(count) > MaxPartitionSketchColumns {
		if err != nil {
			return 0, snapshot, PartitionStats{}, err
		}
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "partition column sketch count")
	}
	seenColumns := make(map[int]struct{}, int(count))
	for i := 0; i < int(count); i++ {
		var col ColumnStats
		col, off, err = takeCompactColumnStats(raw, off)
		if err != nil {
			return 0, snapshot, PartitionStats{}, err
		}
		if len(col.Histogram) != 0 || len(col.MCV) != 0 {
			return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "non-compact partition column sketch")
		}
		if _, exists := seenColumns[col.Ord]; exists {
			return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "duplicate partition column sketch")
		}
		seenColumns[col.Ord] = struct{}{}
		out.Columns = append(out.Columns, col)
	}

	count, off, err = takeU16(raw, off)
	if err != nil || int(count) > MaxPartitionSketchIndexes {
		if err != nil {
			return 0, snapshot, PartitionStats{}, err
		}
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "partition index sketch count")
	}
	seenIndexes := make(map[string]struct{}, int(count))
	for i := 0; i < int(count); i++ {
		var idx IndexStats
		idx, off, err = takeIndexStats(raw, off)
		if err != nil {
			return 0, snapshot, PartitionStats{}, err
		}
		if idx.Name == "" {
			return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "empty partition index sketch name")
		}
		if _, exists := seenIndexes[idx.Name]; exists {
			return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "duplicate partition index sketch")
		}
		seenIndexes[idx.Name] = struct{}{}
		out.Indexes = append(out.Indexes, idx)
	}

	count, off, err = takeU16(raw, off)
	if err != nil || int(count) > MaxPartitionSketchVectors {
		if err != nil {
			return 0, snapshot, PartitionStats{}, err
		}
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "partition vector sketch count")
	}
	seenVectors := make(map[int]struct{}, int(count))
	for i := 0; i < int(count); i++ {
		var vector VectorStats
		vector, off, err = takeVecStats(raw, off)
		if err != nil {
			return 0, snapshot, PartitionStats{}, err
		}
		if _, exists := seenVectors[vector.Ord]; exists {
			return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "duplicate partition vector sketch")
		}
		seenVectors[vector.Ord] = struct{}{}
		out.Vectors = append(out.Vectors, vector)
	}
	if off != len(raw) {
		return 0, snapshot, PartitionStats{}, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "trailing partition stats bytes")
	}
	return tableID, snapshot, out, nil
}

// takeCompactColumnStats rejects non-zero histogram/MCV counts before any
// repeated decode or allocation. NSPS deliberately keeps those distributions
// in the global NSST record.
func takeCompactColumnStats(raw []byte, off int) (ColumnStats, int, error) {
	var out ColumnStats
	ord, off, err := takeU16(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	out.Ord = int(ord)
	out.Nulls, off, err = takeU64(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	out.NDV, off, err = takeU64(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	if off >= len(raw) {
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "truncated partition minmax")
	}
	switch raw[off] {
	case 0:
		off++
	case 1:
		out.HasMinMax = true
		off++
		out.Min, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
		out.Max, off, err = takeValue(raw, off)
		if err != nil {
			return ColumnStats{}, 0, err
		}
	default:
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "invalid partition minmax flag")
	}
	histograms, off, err := takeU16(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	if histograms != 0 {
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "partition histogram not compact")
	}
	mcvs, off, err := takeU16(raw, off)
	if err != nil {
		return ColumnStats{}, 0, err
	}
	if mcvs != 0 {
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "partition MCV not compact")
	}
	if off+8 > len(raw) {
		return ColumnStats{}, 0, nerr.New(nerr.InvalidFormat, "catalog.DecodePartitionStats", "truncated partition correlation")
	}
	out.Correlation = math.Float64frombits(encoding.U64(raw, off))
	off += 8
	return out, off, nil
}
