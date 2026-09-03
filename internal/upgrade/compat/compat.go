// Package compat is the format-compatibility catalog for persisted NextSQL
// encodings — the single source of truth for what version of each on-disk
// or on-wire family this binary can open. A binary opens only versions in
// [MinReadable, MaxReadable]; there is no silent rewrite of an unknown
// version. It is a leaf package (only "fmt" and internal/nerr) so that
// version-check call sites deep in the storage/catalog stack (which
// internal/upgrade itself cannot be imported from — it pulls in
// internal/wal/internal/undo for its diagnose-report Inspect, which would
// cycle back through internal/storage/file) can depend on it directly. See
// docs/storage-format.md "Format and catalog migration strategy".
package compat

import (
	"fmt"

	"github.com/bzync/nextsql/internal/nerr"
)

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
		{Family: FamilyCatalog, Magic: "NSCT", Current: 11, MinReadable: 1, MaxReadable: 11, Notes: "table descriptors; v1 empty FKs, v2 foreign keys, v3 CDC image policy, v4 partition metadata, v5 stable partition identity allocator, v6 per-index HNSW traversal quantisation, v7 per-index vector ANN method + IVF list/probe counts, v8 per-index IVF-PQ subspace count, v9 per-index full-text analyzer id+revision, v10 per-column ENCRYPTED CLIENT logical type, v11 per-column ENUM label list"},
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

// Check reports whether this binary can open version of family. The error,
// when non-nil, names the family and the actual/supported version numbers
// so an operator can tell a too-old file (needs the offline dump/reload
// migration — see docs/storage-format.md "Format and catalog migration
// strategy") apart from a too-new one (needs a newer nextsqld binary)
// without having to cross-reference Catalog() by hand.
func Check(family Family, version uint16) error {
	s, ok := Lookup(family)
	if !ok {
		return nerr.New(nerr.InvalidFormat, "compat.Check", fmt.Sprintf("unknown format family %q", family))
	}
	if version < s.MinReadable {
		return nerr.New(nerr.InvalidFormat, "compat.Check", fmt.Sprintf(
			"%s format version %d is older than this binary supports (minimum %d); migrate via backup/restore into a freshly created database",
			family, version, s.MinReadable))
	}
	if version > s.MaxReadable {
		return nerr.New(nerr.InvalidFormat, "compat.Check", fmt.Sprintf(
			"%s format version %d is newer than this binary supports (maximum %d); upgrade nextsqld",
			family, version, s.MaxReadable))
	}
	return nil
}

// Compatible is Check without the error value.
func Compatible(family Family, version uint16) bool {
	return Check(family, version) == nil
}
