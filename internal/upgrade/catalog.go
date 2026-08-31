// Package upgrade is the format-compatibility catalog for persisted
// NextSQL encodings. A binary opens only versions in [Min, Max]. There
// is no silent rewrite of an unknown version.
package upgrade

import "github.com/bzync/nextsql/internal/nerr"

// Family is one versioned on-disk or on-wire encoding.
type Family string

const (
	FamilyPage     Family = "page"
	FamilyEnvelope Family = "envelope"
	FamilyWAL      Family = "wal"
	FamilyWALCtrl  Family = "wal_control"
	FamilyUNDO     Family = "undo"
	FamilyUNDOCtrl Family = "undo_control"
	FamilyCatalog  Family = "catalog"
	FamilyBackup   Family = "backup"
	FamilyExport   Family = "export"
	FamilyProtocol Family = "protocol"
	FamilyRepl     Family = "replication"
	FamilyIsolated Family = "isolated"
)

// Spec is one family's compatibility window for this binary.
type Spec struct {
	Family      Family
	Magic       string
	Current     uint16
	MinReadable uint16
	MaxReadable uint16
	Notes       string
}

// Catalog is the compatibility matrix this binary understands.
// A future increment that bumps a version must widen MaxReadable
// or add an explicit rewrite path. Unknown versions fail closed.
func Catalog() []Spec {
	return []Spec{
		{Family: FamilyPage, Magic: "NSQL", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "superblock + logical pages"},
		{Family: FamilyEnvelope, Magic: "env", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "AES-256-GCM page envelope"},
		{Family: FamilyWAL, Magic: "NSWL", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "encrypted WAL records"},
		{Family: FamilyWALCtrl, Magic: "NSWC", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "WAL control file"},
		{Family: FamilyUNDO, Magic: "NSUD", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "encrypted UNDO records"},
		{Family: FamilyUNDOCtrl, Magic: "NSUC", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "UNDO control file"},
		{Family: FamilyCatalog, Magic: "NSCT", Current: 9, MinReadable: 1, MaxReadable: 9, Notes: "table descriptors; v1 empty FKs, v2 foreign keys, v3 CDC image policy, v4 partition metadata, v5 stable partition identity allocator, v6 per-index HNSW traversal quantisation, v7 per-index vector ANN method + IVF list/probe counts, v8 per-index IVF-PQ subspace count, v9 per-index full-text analyzer id+revision"},
		{Family: FamilyBackup, Magic: "NSBK", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "physical backup header"},
		{Family: FamilyExport, Magic: "NSXP", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "logical export header"},
		{Family: FamilyProtocol, Magic: "NSQL", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "native wire protocol"},
		{Family: FamilyRepl, Magic: "NSRL", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "encrypted Raft command batch"},
		{Family: FamilyIsolated, Magic: "NSQI", Current: 1, MinReadable: 1, MaxReadable: 1, Notes: "isolated-page quarantine sidecar"},
	}
}

// Lookup returns the spec for family, or false if unknown.
func Lookup(family Family) (Spec, bool) {
	for _, s := range Catalog() {
		if s.Family == family {
			return s, true
		}
	}
	return Spec{}, false
}

// Check reports whether this binary can open version of family.
func Check(family Family, version uint16) error {
	s, ok := Lookup(family)
	if !ok {
		return nerr.New(nerr.InvalidFormat, "upgrade.Check", "unknown format family")
	}
	if version < s.MinReadable {
		return nerr.New(nerr.InvalidFormat, "upgrade.Check", "format version is older than this binary can read")
	}
	if version > s.MaxReadable {
		return nerr.New(nerr.InvalidFormat, "upgrade.Check", "format version is newer than this binary can read")
	}
	return nil
}

// Compatible is Check without the error value.
func Compatible(family Family, version uint16) bool {
	return Check(family, version) == nil
}
