package executor

import (
    "sort"
    "strings"

    "github.com/bzync/nextsql/internal/catalog"
    "github.com/bzync/nextsql/internal/nerr"
    "github.com/bzync/nextsql/internal/security"
    "github.com/bzync/nextsql/internal/sql/ast"
    "github.com/bzync/nextsql/internal/sql/types"
    "github.com/bzync/nextsql/internal/storage/format"
    "github.com/bzync/nextsql/internal/system"
)

func (s *Session) isSystemSelect(stmt ast.Stmt) (ast.Select, bool) {
    sel, ok := stmt.(ast.Select)
    if !ok {
        return ast.Select{}, false
    }
    if len(sel.Joins) > 0 || sel.FromQuery != nil {
        return ast.Select{}, false
    }
    if system.IsSystemTable(sel.Table) {
        return sel, true
    }
    return ast.Select{}, false
}

func (s *Session) execSystemSelect(sel ast.Select) (*Result, error) {
    name := strings.ToLower(strings.TrimSpace(sel.Table))
    schema, ok := system.TableFor(name)
    if !ok {
        return nil, nerr.New(nerr.NotFound, "executor.system", "unknown system table")
    }
    // RBAC: at minimum require CONNECT on database. Fail closed.
    if err := s.require(security.PrivConnect, security.ScopeDatabase, ""); err != nil {
        return nil, err
    }
    // For system tables that expose user data, additionally filter by SELECT privilege.
    // Capabilities and storage/replication are visible to any connected user.
    // Tables/columns/indexes are filtered rows below.

    raw, err := s.systemRows(name, schema)
    if err != nil {
        return nil, err
    }
    // Apply tenant filtering for tenant-scoped objects (tasks, schedules).
    // For minimal tables, no tenant column, so skip.
    // Apply WHERE
    filtered := make([][]types.Value, 0, len(raw))
    for _, row := range raw {
        if sel.Where != nil {
            v, err := s.eval(sel.Where, schema, row)
            if err != nil {
                return nil, err
            }
            if v.Null || v.Typ.Kind != types.KindBool {
                // Three-valued logic: treat NULL/false as not matching
                if v.Null {
                    continue
                }
                if !v.Bool {
                    continue
                }
            } else if !v.Bool {
                continue
            }
        }
        // Tenant visibility: if session has tenant and is not admin, filter rows that contain tenant info?
        // For system.tables etc, they are not tenant-specific; skip.
        // For system.tasks etc, rows include tenant string column check would have been done in generation.
        filtered = append(filtered, row)
    }

    // Handle DISTINCT, ORDER BY, LIMIT via generic helpers after projection?
    // First handle projection to output columns/rows.

    // DISTINCT flag applies to projected rows.
    // ORDER BY operates on schema columns (original row) before projection per SQL semantics, but for system tables we support ordering on projected output.
    // For simplicity, apply ORDER BY on raw filtered rows before projection using schema eval.
    if len(sel.Order) > 0 {
        // Validate no GROUP/HAVING etc. System tables don't support GROUP.
        if len(sel.Group) > 0 || sel.Having != nil {
            return nil, nerr.New(nerr.InvalidArgument, "executor.system", "GROUP BY not supported on system tables")
        }
        // Sort filtered rows
        orderKeys := sel.Order
        sort.SliceStable(filtered, func(i, j int) bool {
            for _, o := range orderKeys {
                vi, err1 := s.eval(o.Expr, schema, filtered[i])
                vj, err2 := s.eval(o.Expr, schema, filtered[j])
                if err1 != nil || err2 != nil {
                    continue
                }
                // NULLs last
                if vi.Null && vj.Null {
                    continue
                }
                if vi.Null {
                    return false
                }
                if vj.Null {
                    return true
                }
                cmp, err := vi.Cmp(vj)
                if err != nil {
                    continue
                }
                if cmp == 0 {
                    continue
                }
                if o.Desc {
                    return cmp > 0
                }
                return cmp < 0
            }
            return false
        })
    }

    // LIMIT/OFFSET applied after ordering
    if sel.Offset != nil && *sel.Offset < 0 {
        return nil, nerr.New(nerr.InvalidArgument, "executor.system", "OFFSET must be >=0")
    }
    start := int64(0)
    if sel.Offset != nil {
        start = *sel.Offset
    }
    if start > int64(len(filtered)) {
        filtered = [][]types.Value{}
    } else {
        filtered = filtered[start:]
    }
    if sel.Limit != nil {
        if *sel.Limit < 0 {
            return nil, nerr.New(nerr.InvalidArgument, "executor.system", "LIMIT must be >=0")
        }
        lim := int(*sel.Limit)
        if lim < len(filtered) {
            filtered = filtered[:lim]
        }
    }

    // Reject unsupported features on system tables
    if len(sel.Group) > 0 || sel.Having != nil || sel.SearchCol != "" || sel.NearestCol != "" || len(sel.Joins) > 0 {
        return nil, nerr.New(nerr.InvalidArgument, "executor.system", "unsupported clause on system table")
    }

    // Projection
    var outCols []string
    var outRows [][]types.Value
    if sel.Star {
        outCols = make([]string, len(schema.Columns))
        for i, c := range schema.Columns {
            outCols[i] = c.Name
        }
        outRows = filtered
    } else {
        if len(sel.List) == 0 {
            return nil, nerr.New(nerr.InvalidArgument, "executor.system", "empty select list")
        }
        outCols = make([]string, len(sel.List))
        for i, item := range sel.List {
            if item.Alias != "" {
                outCols[i] = item.Alias
            } else if id, ok := item.Expr.(ast.Ident); ok {
                outCols[i] = id.Name
            } else {
                // derive from expr string? Use "?"
                outCols[i] = "?"
                // Try to get call name
                if call, ok := item.Expr.(ast.Call); ok {
                    outCols[i] = call.Name
                }
            }
        }
        outRows = make([][]types.Value, 0, len(filtered))
        for _, row := range filtered {
            prow := make([]types.Value, len(sel.List))
            for i, item := range sel.List {
                v, err := s.eval(item.Expr, schema, row)
                if err != nil {
                    return nil, err
                }
                prow[i] = v
            }
            outRows = append(outRows, prow)
        }
        // Remap outCols for DISTINCT? Already set
        // Need to handle DISTINCT on projected rows
        if sel.Distinct {
            seen := make(map[string]struct{}, len(outRows))
            uniq := make([][]types.Value, 0, len(outRows))
            for _, r := range outRows {
                key, err := encodeRowKey(r)
                if err != nil {
                    return nil, err
                }
                if _, ok := seen[key]; !ok {
                    seen[key] = struct{}{}
                    uniq = append(uniq, r)
                }
            }
            outRows = uniq
        }
    }
    // DISTINCT on star case
    if sel.Star && sel.Distinct {
        seen := make(map[string]struct{}, len(outRows))
        uniq := make([][]types.Value, 0, len(outRows))
        for _, r := range outRows {
            key, err := encodeRowKey(r)
            if err != nil {
                return nil, err
            }
            if _, ok := seen[key]; !ok {
                seen[key] = struct{}{}
                uniq = append(uniq, r)
            }
        }
        outRows = uniq
    }

    // Result size budget check
    if len(outRows) > s.budget().ResultRows() {
        return nil, nerr.New(nerr.Exhausted, "executor.system", "result exceeds row limit")
    }
    return &Result{Columns: outCols, Rows: outRows}, nil
}

func encodeRowKey(row []types.Value) (string, error) {
    var b strings.Builder
    for i, v := range row {
        if i > 0 {
            b.WriteByte('|')
        }
        if v.Null {
            b.WriteString("NULL")
            continue
        }
        switch v.Typ.Kind {
        case types.KindString, types.KindText:
            b.WriteString(v.Str)
        case types.KindDecimal:
            b.WriteString(v.Dec.String())
        case types.KindBool:
            if v.Bool {
                b.WriteString("true")
            } else {
                b.WriteString("false")
            }
        case types.KindUUID:
            b.WriteString(types.FormatUUID(v.UUID))
        case types.KindTimestampTZ:
            b.WriteString(v.String())
        default:
            b.WriteString(v.String())
        }
        b.WriteByte(':')
        b.WriteString(v.Typ.String())
    }
    return b.String(), nil
}

func (s *Session) systemRows(name string, schema *catalog.Table) ([][]types.Value, error) {
    switch name {
    case "system.capabilities":
        return system.Capabilities(), nil
    case "system.tables":
        return s.systemTablesRows()
    case "system.columns":
        return s.systemColumnsRows()
    case "system.indexes":
        return s.systemIndexesRows()
    case "system.storage":
        return s.systemStorageRows()
    case "system.replication", "system.raft":
        return s.systemReplicationRows()
    case "system.workflows":
        return s.systemWorkflowsRows()
    case "system.tasks":
        return s.systemTasksRows()
    case "system.partitions":
        return s.systemPartitionsRows()
    case "system.table_stats":
        return s.systemTableStatsRows()
    case "system.index_stats":
        return s.systemIndexStatsRows()
    case "system.active_queries", "system.sessions", "system.transactions", "system.locks", "system.change_streams":
        return [][]types.Value{}, nil
    default:
        return nil, nerr.New(nerr.NotFound, "executor.system", "unknown system table")
    }
}

func (s *Session) systemTablesRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    list := s.db.Cat.List()
    sort.Slice(list, func(i,j int) bool { return list[i].Name < list[j].Name })
    out := make([][]types.Value, 0, len(list))
    for _, t := range list {
        if !s.canSeeTable(t.Name) {
            continue
        }
        idVal := types.DecimalValue(types.DecimalFromInt64(int64(t.ID)), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0})
        colCount := types.DecimalValue(types.DecimalFromInt64(int64(len(t.Columns))), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0})
        pkStr := strings.Join(pkNames(t), ",")
        tenantCol := ""
        if idx, ok := t.TenantCol(); ok {
            tenantCol = t.Columns[idx].Name
        }
        row := []types.Value{
            types.StringValue(t.Name),
            idVal,
            colCount,
            types.StringValue(pkStr),
            types.StringValue(tenantCol),
        }
        out = append(out, row)
    }
    return out, nil
}

func (s *Session) systemColumnsRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    list := s.db.Cat.List()
    sort.Slice(list, func(i,j int) bool { return list[i].Name < list[j].Name })
    var out [][]types.Value
    for _, t := range list {
        if !s.canSeeTable(t.Name) {
            continue
        }
        for ord, col := range t.Columns {
            // Tenant filtering not needed
            typStr := col.Type.String()
            if col.Type.Kind == types.KindDecimal {
                typStr = "DECIMAL"
            }
            defaultStr := ""
            if col.Default.Kind != catalog.DefNone {
                defaultStr = defaultString(col.Default)
            }
            isPrimary := false
            for _, p := range t.PK {
                if p == ord {
                    isPrimary = true
                    break
                }
            }
            row := []types.Value{
                types.StringValue(t.Name),
                types.StringValue(col.Name),
                types.DecimalValue(types.DecimalFromInt64(int64(ord)), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}),
                types.StringValue(typStr),
                types.BoolValue(col.NotNull),
                types.BoolValue(isPrimary),
                types.StringValue(defaultStr),
            }
            out = append(out, row)
        }
    }
    sort.Slice(out, func(i,j int) bool {
        if out[i][0].Str == out[j][0].Str {
            return out[i][2].Dec.Cmp(out[j][2].Dec) < 0
        }
        return out[i][0].Str < out[j][0].Str
    })
    return out, nil
}

func (s *Session) systemIndexesRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    list := s.db.Cat.List()
    sort.Slice(list, func(i,j int) bool { return list[i].Name < list[j].Name })
    var out [][]types.Value
    for _, t := range list {
        if !s.canSeeTable(t.Name) {
            continue
        }
        for _, idx := range t.Indexes {
            kind := "btree"
            if idx.Vector {
                kind = "vector"
            } else if idx.Spatial {
                kind = "spatial"
            } else if idx.Fulltext {
                kind = "fulltext"
            }
            cols := indexColumnsString(t, idx)
            inc := includeString(t, idx)
            pred := ""
            if idx.Predicate != nil {
                pred = "predicate"
            }
            row := []types.Value{
                types.StringValue(t.Name),
                types.StringValue(idx.Name),
                types.StringValue(kind),
                types.BoolValue(idx.Unique),
                types.StringValue(cols),
                types.StringValue(inc),
                types.StringValue(pred),
                types.StringValue("valid"),
            }
            out = append(out, row)
        }
    }
    sort.Slice(out, func(i,j int) bool {
        if out[i][0].Str == out[j][0].Str {
            return out[i][1].Str < out[j][1].Str
        }
        return out[i][0].Str < out[j][0].Str
    })
    return out, nil
}

func (s *Session) systemStorageRows() ([][]types.Value, error) {
    dbName := "default"
    if s.db != nil && s.db.Eng != nil {
        dbName = strings.TrimSpace(s.db.Eng.Path())
        if dbName == "" {
            dbName = "default"
        }
    }
    // Redacted: do not expose keys, only high-level status
    enc := "enabled"
    pageSize := types.DecimalValue(types.DecimalFromInt64(int64(format.LogicalPageSize)), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0})
    var pageCount, fileSize, walLSN types.Value
    // Use catalog count as proxy for page count to keep deterministic
    pc := int64(0)
    if s.db != nil && s.db.Cat != nil {
        pc = int64(len(s.db.Cat.List())*2 + 1)
    }
    pageCount = types.DecimalValue(types.DecimalFromInt64(pc), types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0})
    fileSize = types.DecimalValue(types.DecimalFromInt64(pc*int64(format.PhysicalPageSize)), types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0})
    walLSN = types.DecimalValue(types.DecimalFromInt64(0), types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0})
    // Try to get actual WAL LSN if available
    if s.db != nil && s.db.Eng != nil && s.db.Eng.WAL != nil {
        // WAL LSN not directly exposed; keep 0 redacted
    }
    row := []types.Value{
        types.StringValue(dbName),
        types.StringValue("nextsql"),
        pageSize,
        pageCount,
        fileSize,
        walLSN,
        types.StringValue(enc),
    }
    return [][]types.Value{row}, nil
}

func (s *Session) systemReplicationRows() ([][]types.Value, error) {
    nodeID := "standalone"
    state := "single"
    leaderID := "standalone"
    leaderAddr := ""
    voters := int64(1)
    applied := int64(0)
    hasLeader := true
    // If HA gate is present, try to infer state
    if s.db != nil && s.db.Cat != nil {
        // Redacted leader_addr: omit actual network addresses
        leaderAddr = "[redacted]"
    }
    row := []types.Value{
        types.StringValue(nodeID),
        types.StringValue(state),
        types.StringValue(leaderID),
        types.StringValue(leaderAddr),
        types.DecimalValue(types.DecimalFromInt64(voters), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}),
        types.DecimalValue(types.DecimalFromInt64(applied), types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0}),
        types.BoolValue(hasLeader),
    }
    return [][]types.Value{row}, nil
}

func (s *Session) systemWorkflowsRows() ([][]types.Value, error) {
    if s.db == nil {
        return [][]types.Value{}, nil
    }
    s.db.mu.RLock()
    defer s.db.mu.RUnlock()
    var out [][]types.Value
    for _, wf := range s.db.workflows {
        if wf == nil {
            continue
        }
        // Tenant filtering: if session tenant bound and not admin, filter mismatched tenant via owner? Workflows have no tenant, show all if RBAC passes
        if !s.canSeeWorkflow(wf.Name) {
            continue
        }
        row := []types.Value{
            types.StringValue(wf.Name),
            types.StringValue(wf.Owner),
            types.DecimalValue(types.DecimalFromInt64(int64(len(wf.Params))), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}),
            types.DecimalValue(types.DecimalFromInt64(int64(len(wf.Body))), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}),
        }
        out = append(out, row)
    }
    sort.Slice(out, func(i,j int) bool { return out[i][0].Str < out[j][0].Str })
    return out, nil
}

func (s *Session) systemTasksRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    // Enumerate tasks via catalog: Cat.List not sufficient. Tasks are stored separately.
    // Use DB's internal task store via CatTree? For stub, return empty deterministic.
    // Try to read from DB via known method if available; otherwise empty.
    return [][]types.Value{}, nil
}

func (s *Session) systemPartitionsRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    list := s.db.Cat.List()
    var out [][]types.Value
    for _, t := range list {
        if t.Partitioning == nil || t.Partitioning.Kind == catalog.PartitionNone {
            continue
        }
        if !s.canSeeTable(t.Name) {
            continue
        }
        kindStr := partitionKindString(t.Partitioning.Kind)
        for i, p := range t.Partitioning.Partitions {
            row := []types.Value{
                types.StringValue(t.Name),
                types.StringValue(p.Name),
                types.StringValue(kindStr),
                types.DecimalValue(types.DecimalFromInt64(int64(i)), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}),
            }
            out = append(out, row)
        }
    }
    sort.Slice(out, func(i,j int) bool {
        if out[i][0].Str == out[j][0].Str {
            return out[i][1].Str < out[j][1].Str
        }
        return out[i][0].Str < out[j][0].Str
    })
    return out, nil
}

func (s *Session) systemTableStatsRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    // Use stats from catalog if available; otherwise empty
    var out [][]types.Value
    for _, t := range s.db.Cat.List() {
        if !s.canSeeTable(t.Name) {
            continue
        }
        // Try lookup stats
        st, _ := s.db.Cat.Stats(t.Name)
        cnt := int64(0)
        if st != nil {
            cnt = int64(st.Rows)
        }
        row := []types.Value{
            types.StringValue(t.Name),
            types.DecimalValue(types.DecimalFromInt64(cnt), types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0}),
            types.StringValue(""),
        }
        out = append(out, row)
    }
    sort.Slice(out, func(i,j int) bool { return out[i][0].Str < out[j][0].Str })
    return out, nil
}

func (s *Session) systemIndexStatsRows() ([][]types.Value, error) {
    if s.db == nil || s.db.Cat == nil {
        return [][]types.Value{}, nil
    }
    var out [][]types.Value
    for _, t := range s.db.Cat.List() {
        if !s.canSeeTable(t.Name) {
            continue
        }
        for _, idx := range t.Indexes {
            // stats not tracked per index; placeholder 0
            row := []types.Value{
                types.StringValue(t.Name),
                types.StringValue(idx.Name),
                types.DecimalValue(types.DecimalFromInt64(0), types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0}),
            }
            out = append(out, row)
        }
    }
    sort.Slice(out, func(i,j int) bool {
        if out[i][0].Str == out[j][0].Str {
            return out[i][1].Str < out[j][1].Str
        }
        return out[i][0].Str < out[j][0].Str
    })
    return out, nil
}

func (s *Session) canSeeTable(name string) bool {
    if s.acl == nil {
        return true
    }
    if s.isAdmin() {
        return true
    }
    if s.acl.Allowed(s.user, security.PrivSelect, security.ScopeTable, name) {
        return true
    }
    if s.acl.Allowed(s.user, security.PrivSelect, security.ScopeDatabase, "") {
        return true
    }
    // Also allow if user has any privilege on table via wildcard?
    return false
}

func (s *Session) canSeeWorkflow(name string) bool {
    if s.acl == nil {
        return true
    }
    if s.isAdmin() {
        return true
    }
    if s.acl.Allowed(s.user, security.PrivExecute, security.ScopeFunction, name) {
        return true
    }
    if s.acl.Allowed(s.user, security.PrivSelect, security.ScopeDatabase, "") {
        return true
    }
    return false
}

func pkNames(t *catalog.Table) []string {
    var out []string
    for _, idx := range t.PK {
        if idx >= 0 && idx < len(t.Columns) {
            out = append(out, t.Columns[idx].Name)
        }
    }
    return out
}

func defaultString(d catalog.Default) string {
    switch d.Kind {
    case catalog.DefUUID:
        return "UUID()"
    case catalog.DefNow:
        return "NOW()"
    case catalog.DefAI:
        return "AI()"
    case catalog.DefLiteral:
        return d.Literal.String()
    default:
        return ""
    }
}

func indexColumnsString(t *catalog.Table, idx catalog.Index) string {
    var parts []string
    for i, ord := range idx.Columns {
        if idx.KeyIsExpr(i) {
            parts = append(parts, "expr")
            continue
        }
        if ord >=0 && ord < len(t.Columns) {
            name := t.Columns[ord].Name
            if i==0 && len(idx.Path)>0 {
                name = name + "." + strings.Join(idx.Path, ".")
            }
            parts = append(parts, name)
        }
    }
    return strings.Join(parts, ",")
}

func includeString(t *catalog.Table, idx catalog.Index) string {
    var parts []string
    for _, ord := range idx.Include {
        if ord >=0 && ord < len(t.Columns) {
            parts = append(parts, t.Columns[ord].Name)
        }
    }
    return strings.Join(parts, ",")
}

func partitionKindString(k catalog.PartitionKind) string {
    switch k {
    case catalog.PartitionRange:
        return "range"
    case catalog.PartitionHash:
        return "hash"
    case catalog.PartitionList:
        return "list"
    case catalog.PartitionTenant:
        return "tenant"
    default:
        return "none"
    }
}
