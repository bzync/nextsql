package security

import (
	"github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Query-abuse limits. Attackers must not grow memory or CPU from unchecked input.
const (
	// MaxJoinTables is FROM + JOIN tables in one SELECT (hard cap).
	MaxJoinTables = 8
	// MaxCTEs is WITH list length in one query.
	MaxCTEs = 32
	// MaxRecursiveDepth is WITH RECURSIVE working-table iterations.
	MaxRecursiveDepth = 100
	MaxJSONDepth      = json.MaxDepth
	MaxJSONBytes      = json.MaxBytes
	MaxVectorDim      = types.MaxVectorDim
	MaxGeoVertices    = types.MaxGeoVertices
	MaxSQLBytes       = 1 << 20
	MaxPacket         = 1 << 20
	MaxResult         = 64 << 20

	// Cascade stack and fan-out. Exceeding either fails the statement with
	// exhausted so a wide DELETE cannot amplify WAL without bound.
	MaxFKDepth       = 8
	MaxFKTouchedRows = 100_000
)
