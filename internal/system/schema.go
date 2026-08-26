package system

import (
    "sort"
    "strings"

    "github.com/bzync/nextsql/internal/catalog"
    "github.com/bzync/nextsql/internal/sql/types"
    "github.com/bzync/nextsql/internal/version"
)

// Version is the system schema version for machine consumers.
// Deterministic and monotonic; bump only with column changes.
const SchemaVersion = 1

// SchemaName is the virtual schema name.
const SchemaName = "system"

var tables = map[string]*catalog.Table{}

func init() {
    register("capabilities", []catalog.Column{
        {Name: "name", Type: types.String()},
        {Name: "status", Type: types.String()},
        {Name: "description", Type: types.String()},
        {Name: "since_version", Type: types.String()},
    })
    register("tables", []catalog.Column{
        {Name: "name", Type: types.String()},
        {Name: "id", Type: dec(10,0)},
        {Name: "column_count", Type: dec(10,0)},
        {Name: "pk", Type: types.String()},
        {Name: "tenant_column", Type: types.String()},
    })
    register("columns", []catalog.Column{
        {Name: "table_name", Type: types.String()},
        {Name: "column_name", Type: types.String()},
        {Name: "ordinal", Type: dec(10,0)},
        {Name: "type", Type: types.String()},
        {Name: "not_null", Type: types.Bool()},
        {Name: "is_primary", Type: types.Bool()},
        {Name: "default_value", Type: types.String()},
    })
    register("indexes", []catalog.Column{
        {Name: "table_name", Type: types.String()},
        {Name: "index_name", Type: types.String()},
        {Name: "kind", Type: types.String()},
        {Name: "is_unique", Type: types.Bool()},
        {Name: "columns", Type: types.String()},
        {Name: "include_columns", Type: types.String()},
        {Name: "predicate", Type: types.String()},
        {Name: "status", Type: types.String()},
    })
    register("storage", []catalog.Column{
        {Name: "database", Type: types.String()},
        {Name: "engine", Type: types.String()},
        {Name: "page_size", Type: dec(10,0)},
        {Name: "page_count", Type: dec(20,0)},
        {Name: "file_size", Type: dec(20,0)},
        {Name: "wal_lsn", Type: dec(20,0)},
        {Name: "encryption", Type: types.String()},
    })
    register("replication", []catalog.Column{
        {Name: "node_id", Type: types.String()},
        {Name: "state", Type: types.String()},
        {Name: "leader_id", Type: types.String()},
        {Name: "leader_addr", Type: types.String()},
        {Name: "voters", Type: dec(10,0)},
        {Name: "applied_lsn", Type: dec(20,0)},
        {Name: "has_leader", Type: types.Bool()},
    })
    // raft is an alias for replication
    if t, ok := tables["system.replication"]; ok {
        cp := t.Clone()
        cp.Name = "system.raft"
        tables["system.raft"] = cp
    }
    // stubs for remaining P26 objects: empty but deterministic schemas
    register("active_queries", []catalog.Column{
        {Name: "query_id", Type: types.String()},
        {Name: "user", Type: types.String()},
        {Name: "sql", Type: types.String()},
        {Name: "state", Type: types.String()},
    })
    register("sessions", []catalog.Column{
        {Name: "session_id", Type: types.String()},
        {Name: "user", Type: types.String()},
        {Name: "remote", Type: types.String()},
        {Name: "state", Type: types.String()},
    })
    register("transactions", []catalog.Column{
        {Name: "txn_id", Type: types.String()},
        {Name: "user", Type: types.String()},
        {Name: "isolation", Type: types.String()},
        {Name: "state", Type: types.String()},
    })
    register("locks", []catalog.Column{
        {Name: "lock_id", Type: types.String()},
        {Name: "table_name", Type: types.String()},
        {Name: "mode", Type: types.String()},
        {Name: "granted", Type: types.Bool()},
    })
    register("table_stats", []catalog.Column{
        {Name: "table_name", Type: types.String()},
        {Name: "row_count", Type: dec(20,0)},
        {Name: "updated_at", Type: types.String()},
    })
    register("index_stats", []catalog.Column{
        {Name: "table_name", Type: types.String()},
        {Name: "index_name", Type: types.String()},
        {Name: "row_count", Type: dec(20,0)},
    })
    register("workflows", []catalog.Column{
        {Name: "name", Type: types.String()},
        {Name: "owner", Type: types.String()},
        {Name: "param_count", Type: dec(10,0)},
        {Name: "statement_count", Type: dec(10,0)},
    })
    register("tasks", []catalog.Column{
        {Name: "id", Type: types.String()},
        {Name: "schedule", Type: types.String()},
        {Name: "workflow", Type: types.String()},
        {Name: "state", Type: types.String()},
        {Name: "attempts", Type: dec(10,0)},
    })
    register("change_streams", []catalog.Column{
        {Name: "table_name", Type: types.String()},
        {Name: "lsn", Type: dec(20,0)},
        {Name: "state", Type: types.String()},
    })
    register("partitions", []catalog.Column{
        {Name: "table_name", Type: types.String()},
        {Name: "partition_name", Type: types.String()},
        {Name: "kind", Type: types.String()},
        {Name: "ordinal", Type: dec(10,0)},
    })
}

func dec(p,s uint16) types.Type { t, _ := types.DecimalType(p,s); return t }

func register(name string, cols []catalog.Column) {
    full := SchemaName + "." + name
    t := &catalog.Table{
        Name:    full,
        Columns: cols,
    }
    tables[full] = t
}

// IsSystemTable reports whether name is a virtual system table (case-folded).
func IsSystemTable(name string) bool {
    _, ok := tables[strings.ToLower(strings.TrimSpace(name))]
    return ok
}

// TableFor returns the schema for a system table.
func TableFor(name string) (*catalog.Table, bool) {
    t, ok := tables[strings.ToLower(strings.TrimSpace(name))]
    if !ok {
        return nil, false
    }
    return t.Clone(), true
}

// List returns all system table names sorted.
func List() []string {
    out := make([]string, 0, len(tables))
    for k := range tables {
        out = append(out, k)
    }
    sort.Strings(out)
    return out
}

// Capabilities returns deterministic capability rows sorted by name.
// Columns: name, status, description, since_version
func Capabilities() [][]types.Value {
    // status values: supported, experimental, unsupported, deprecated
    rows := [][]types.Value{
        rowCap("backup", "supported", "encrypted backup and restore", "0.1.0"),
        rowCap("btree", "supported", "clustered B+Tree with MVCC", "0.1.0"),
        rowCap("cdc", "supported", "change data capture streams", "0.1.0"),
        rowCap("covering_indexes", "supported", "INCLUDE covering indexes", "0.1.0"),
        rowCap("distinct", "supported", "SELECT DISTINCT", "0.1.0"),
        rowCap("encryption", "supported", "AES-256-GCM envelope", "0.1.0"),
        rowCap("expression_indexes", "supported", "expression indexes", "0.1.0"),
        rowCap("foreign_keys", "supported", "FOREIGN KEY constraints", "0.1.0"),
        rowCap("fulltext", "supported", "full-text SEARCH", "0.1.0"),
        rowCap("geo", "supported", "POINT/BOX/LINESTRING/POLYGON and spatial index", "0.1.0"),
        rowCap("hybrid_search", "supported", "hybrid SEARCH+NEAREST", "0.1.0"),
        rowCap("hnsw", "supported", "HNSW vector index", "0.1.0"),
        rowCap("json", "supported", "JSON binary and path indexes", "0.1.0"),
        rowCap("locks", "supported", "row-level locking and deadlock detection", "0.1.0"),
        rowCap("maintenance", "supported", "MAINTAIN DATABASE/TABLE/INDEX", "0.1.0"),
        rowCap("mvcc", "supported", "snapshot isolation", "0.1.0"),
        rowCap("partial_indexes", "supported", "partial indexes with predicate", "0.1.0"),
        rowCap("pitr", "supported", "point-in-time recovery from WAL", "0.1.0"),
        rowCap("raft", "supported", "HA replication via Raft", "0.1.0"),
        rowCap("returning", "supported", "INSERT/UPDATE/DELETE RETURNING", "0.1.0"),
        rowCap("schedules", "supported", "SCHEDULE every/at", "0.1.0"),
        rowCap("set_operations", "supported", "UNION/INTERSECT/EXCEPT", "0.1.0"),
        rowCap("subqueries", "supported", "scalar, IN, EXISTS subqueries", "0.1.0"),
        rowCap("system_catalog", "supported", "virtual system schema", version.String),
        rowCap("tasks", "supported", "durable TASK execution", "0.1.0"),
        rowCap("tenants", "supported", "tenant isolation via SET TENANT", "0.1.0"),
        rowCap("transactions", "supported", "BEGIN/COMMIT/ROLLBACK", "0.1.0"),
        rowCap("triggers", "supported", "TRIGGER RUN WORKFLOW", "0.1.0"),
        rowCap("upsert", "supported", "UPSERT with RETURNING", "0.1.0"),
        rowCap("vector", "supported", "VECTOR<F32,N> and NEAREST", "0.1.0"),
        rowCap("window_functions", "supported", "ROW_NUMBER/RANK/LAG etc", "0.1.0"),
        rowCap("workflows", "supported", "CREATE/RUN WORKFLOW", "0.1.0"),
        rowCap("cte", "supported", "WITH and WITH RECURSIVE", "0.1.0"),
        rowCap("partitions_range", "experimental", "RANGE partitioning", "0.1.0"),
        rowCap("partitions_hash", "experimental", "HASH partitioning", "0.1.0"),
        rowCap("follower_reads", "unsupported", "follower reads", "0.1.0"),
        rowCap("rebuild_index_online", "unsupported", "REBUILD INDEX ONLINE", "0.1.0"),
    }
    // Ensure deterministic order already sorted by name; sort to guarantee.
    sort.Slice(rows, func(i, j int) bool {
        return rows[i][0].Str < rows[j][0].Str
    })
    return rows
}

func rowCap(name, status, desc, since string) []types.Value {
    return []types.Value{
        types.StringValue(name),
        types.StringValue(status),
        types.StringValue(desc),
        types.StringValue(since),
    }
}
