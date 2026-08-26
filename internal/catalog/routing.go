package catalog

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// HashPartitionRemainder returns the NSCT v4 HASH routing bucket. The
// algorithm is part of the durable v4 catalog semantics: SHA-256 over the
// canonical typed tuple key, with the first 64 digest bits interpreted as
// big-endian and reduced modulo the declared partition count.
func HashPartitionRemainder(tuple []types.Value, modulus uint32) (uint32, error) {
	if modulus == 0 || modulus > MaxPartitions {
		return 0, nerr.New(nerr.InvalidArgument, "catalog.HashPartitionRemainder", "invalid HASH modulus")
	}
	raw, err := types.EncodeKey(tuple)
	if err != nil {
		return 0, err
	}
	digest := sha256.Sum256(raw)
	return uint32(binary.BigEndian.Uint64(digest[:8]) % uint64(modulus)), nil
}

// PartitionForRow returns the partition that owns row, or NotFound if no partition matches.
// For RANGE it uses ordered non-overlapping intervals; gaps return error.
func (t *Table) PartitionForRow(row []types.Value) (*Partition, error) {
	if t == nil || t.Partitioning == nil {
		return nil, nil
	}
	p := t.Partitioning
	tuple := make([]types.Value, len(p.Columns))
	for i, ord := range p.Columns {
		if ord < 0 || ord >= len(row) {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.PartitionForRow", "partition column missing")
		}
		tuple[i] = row[ord]
		// NULL partition values are not allowed per validation, but row could be NULL.
		if tuple[i].Null {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.PartitionForRow", "NULL partition value")
		}
	}
	switch p.Kind {
	case PartitionRange:
		for i, part := range p.Partitions {
			lower, upper := part.Values[0], part.Values[1]
			if lower != nil {
				cmp, err := comparePartitionTuple(tuple, lower)
				if err != nil {
					return nil, err
				}
				if cmp < 0 || (cmp == 0 && !part.LowerInclusive) {
					continue
				}
			}
			if upper != nil {
				cmp, err := comparePartitionTuple(tuple, upper)
				if err != nil {
					return nil, err
				}
				if cmp > 0 || (cmp == 0 && !part.UpperInclusive) {
					continue
				}
				// Upper is exclusive by default (LowerInclusive false). So exactly equal to upper should not match.
				// Our check above already handles UpperInclusive flag.
			}
			// Since ranges are ordered and non-overlapping, first match is correct.
			// Verify gap handling: if upper is exclusive, tuple == upper moves to next partition's lower.
			// So we return this partition only if tuple is within.
			return &p.Partitions[i], nil
		}
		return nil, nerr.New(nerr.NotFound, "catalog.PartitionForRow", "no partition for row")
	case PartitionTenant, PartitionList:
		// LIST-like: exact match
		key, err := types.EncodeKey(tuple)
		if err != nil {
			return nil, err
		}
		for i, part := range p.Partitions {
			for _, v := range part.Values {
				ek, err := types.EncodeKey(v)
				if err != nil {
					return nil, err
				}
				if bytes.Equal(key, ek) {
					return &p.Partitions[i], nil
				}
			}
		}
		return nil, nerr.New(nerr.NotFound, "catalog.PartitionForRow", "no partition for row")
	case PartitionHash:
		modulus := p.Partitions[0].Modulus
		remainder, err := HashPartitionRemainder(tuple, modulus)
		if err != nil {
			return nil, err
		}
		for i, part := range p.Partitions {
			if part.Modulus == modulus && part.Remainder == remainder {
				return &p.Partitions[i], nil
			}
		}
		return nil, nerr.New(nerr.Corruption, "catalog.PartitionForRow", "HASH remainder missing from validated descriptor")
	default:
		return nil, nerr.New(nerr.InvalidArgument, "catalog.PartitionForRow", "unknown partition kind")
	}
}

// PrunePartitions returns candidate partitions for a predicate.
// where is a conjunction analysis placeholder; for this slice we support simple equality and range on single-column RANGE or equality on TENANT.
// If where is nil, all partitions are candidates.
// The implementation is conservative: if predicate cannot be analyzed, it returns all partitions.
func (t *Table) PrunePartitions(where interface{}) []Partition {
	if t == nil || t.Partitioning == nil {
		return nil
	}
	// This generic prune is not used directly; executor uses more specific helpers with ast.Expr.
	return t.Partitioning.Partitions
}
