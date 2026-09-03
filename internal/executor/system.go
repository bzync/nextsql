package executor

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/system"
	"github.com/bzync/nextsql/internal/txn"
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
	if len(sel.Group) > 0 || sel.Having != nil || len(sel.SearchCols) > 0 || sel.NearestCol != "" || sel.Nearest2Col != "" || len(sel.Joins) > 0 {
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
	case "system.replica_health":
		return s.systemReplicaHealthRows()
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
	case "system.sessions":
		return s.systemSessionsRows()
	case "system.active_queries":
		return s.systemActiveQueriesRows()
	case "system.transactions":
		return s.systemTransactionsRows()
	case "system.change_streams":
		return s.systemChangeStreamsRows()
	case "system.locks":
		return s.systemLocksRows()
	case "system.users":
		return s.systemUsersRows()
	case "system.roles":
		return s.systemRolesRows()
	case "system.grants":
		return s.systemGrantsRows()
	case "system.resource_groups":
		return s.systemResourceGroupsRows()
	case "system.realms":
		return s.systemRealmsRows()
	case "system.databases":
		return s.systemDatabasesRows()
	default:
		return nil, nerr.New(nerr.NotFound, "executor.system", "unknown system table")
	}
}

func (s *Session) systemTablesRows() ([][]types.Value, error) {
	if s.db == nil || s.db.Cat == nil {
		return [][]types.Value{}, nil
	}
	list := s.db.Cat.List()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	out := make([][]types.Value, 0, len(list))
	for _, t := range list {
		if !s.canSeeTable(t.Name) {
			continue
		}
		idVal := types.DecimalValue(types.DecimalFromInt64(int64(t.ID)), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0})
		colCount := types.DecimalValue(types.DecimalFromInt64(int64(len(t.Columns))), types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0})
		pkStr := strings.Join(pkNames(t), ",")
		tenantCol := ""
		if idx, ok := t.LegacyTenantCol(); ok {
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
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	var out [][]types.Value
	for _, t := range list {
		if !s.canSeeTable(t.Name) {
			continue
		}
		for ord, col := range t.Columns {
			// Tenant filtering not needed
			logical := col.LogicalType()
			typStr := logical.String()
			if logical.Kind == types.KindDecimal {
				typStr = "DECIMAL"
			}
			if col.ClientEncrypted() {
				typStr += " ENCRYPTED CLIENT"
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
	sort.Slice(out, func(i, j int) bool {
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
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
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
	sort.Slice(out, func(i, j int) bool {
		if out[i][0].Str == out[j][0].Str {
			return out[i][1].Str < out[j][1].Str
		}
		return out[i][0].Str < out[j][0].Str
	})
	return out, nil
}

func (s *Session) systemStorageRows() ([][]types.Value, error) {
	dbName := "default"
	if s.db != nil {
		dbName = s.db.DatabaseName()
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

func sysDec(v int64, prec uint16) types.Value {
	return types.DecimalValue(types.DecimalFromInt64(v), types.Type{Kind: types.KindDecimal, Precision: prec, Scale: 0})
}

func (s *Session) systemReplicationRows() ([][]types.Value, error) {
	nodeID := "standalone"
	state := "single"
	leaderID := "standalone"
	// leader_addr is always redacted: never expose network addresses over SQL.
	leaderAddr := "[redacted]"
	voters := int64(1)
	applied := int64(0)
	hasLeader := true
	if s.db != nil {
		if st, ok := s.db.ClusterStatus(); ok {
			nodeID = st.NodeID
			state = st.State
			leaderID = st.LeaderID
			voters = int64(st.Voters)
			applied = int64(st.Applied)
			hasLeader = st.HasLeader
		}
	}
	row := []types.Value{
		types.StringValue(nodeID),
		types.StringValue(state),
		types.StringValue(leaderID),
		types.StringValue(leaderAddr),
		sysDec(voters, 10),
		sysDec(applied, 20),
		types.BoolValue(hasLeader),
		types.BoolValue(s.db.InMaintenanceMode()),
	}
	return [][]types.Value{row}, nil
}

func (s *Session) systemReplicaHealthRows() ([][]types.Value, error) {
	nodeID := "standalone"
	role := "leader"
	hasLeader := true
	var appliedLSN, commitIdx, appliedIdx, backlog int64
	lastContactMS := int64(0)
	healthy := true
	replicationSuspect := false
	if s.db != nil {
		if h, ok := s.db.ClusterHealth(); ok {
			nodeID = h.NodeID
			role = h.Role
			hasLeader = h.HasLeader
			appliedLSN = int64(h.AppliedLSN)
			commitIdx = int64(h.CommitIndex)
			appliedIdx = int64(h.AppliedIndex)
			backlog = int64(h.ApplyBacklog)
			healthy = h.Healthy
			replicationSuspect = h.ReplicationSuspect
			if h.LastContact < 0 {
				lastContactMS = -1
			} else {
				lastContactMS = h.LastContact.Milliseconds()
			}
		}
	}
	row := []types.Value{
		types.StringValue(nodeID),
		types.StringValue(role),
		types.BoolValue(hasLeader),
		sysDec(appliedLSN, 20),
		sysDec(commitIdx, 20),
		sysDec(appliedIdx, 20),
		sysDec(backlog, 20),
		sysDec(lastContactMS, 20),
		types.BoolValue(healthy),
		types.BoolValue(replicationSuspect),
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
	sort.Slice(out, func(i, j int) bool { return out[i][0].Str < out[j][0].Str })
	return out, nil
}

// systemResourceGroupsRows is admin-only, like system.users/roles/grants:
// RESOURCE GROUP is a cluster-wide workload-governance object gated on
// PrivAdmin/ScopeCluster, so listing it follows the same "row-filter, never
// fail, on RBAC" convention rather than system.workflows' broader visibility.
func (s *Session) systemResourceGroupsRows() ([][]types.Value, error) {
	if s.db == nil {
		return [][]types.Value{}, nil
	}
	if !(s.acl == nil || s.isAdmin()) {
		return [][]types.Value{}, nil
	}
	dec10 := types.Type{Kind: types.KindDecimal, Precision: 10, Scale: 0}
	dec20 := types.Type{Kind: types.KindDecimal, Precision: 20, Scale: 0}
	s.db.mu.RLock()
	defer s.db.mu.RUnlock()
	var out [][]types.Value
	for _, g := range s.db.resGroups {
		if g == nil {
			continue
		}
		out = append(out, []types.Value{
			types.StringValue(g.Name),
			types.StringValue(g.Owner),
			types.DecimalValue(types.DecimalFromInt64(int64(g.MaxConcurrency)), dec10),
			types.DecimalValue(types.DecimalFromInt64(g.MemoryBytes), dec20),
			types.DecimalValue(types.DecimalFromInt64(int64(g.Workers)), dec10),
			types.DecimalValue(types.DecimalFromInt64(int64(g.Priority)), dec10),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].Str < out[j][0].Str })
	return out, nil
}

// hostingStateName/hostingLayoutName give system.realms/system.databases
// (M2-4a) human-readable strings for hosting.State/hosting.Layout, mirroring
// how every other system.* view (e.g. replica_health's role column) avoids
// exposing raw numeric enum values.
func hostingStateName(st hosting.State) string {
	switch st {
	case hosting.StateProvisioning:
		return "provisioning"
	case hosting.StateActive:
		return "active"
	case hosting.StateSuspended:
		return "suspended"
	case hosting.StateDeleting:
		return "deleting"
	case hosting.StateTombstoned:
		return "tombstoned"
	case hosting.StateFailed:
		return "failed"
	default:
		return "unknown"
	}
}

func hostingLayoutName(l hosting.Layout) string {
	switch l {
	case hosting.LayoutLegacyDefault:
		return "legacy_default"
	case hosting.LayoutManaged:
		return "managed"
	default:
		return "unknown"
	}
}

// systemRealmsRows and systemDatabasesRows (M2-4a) expose the hosted
// deployment registry (internal/hosting.Registry.Manifest) read-only.
// Admin-only, like system.resource_groups: deployment structure across
// realms is not tenant-visible data. Empty (not an error) on a
// legacy/non-hosted deployment (s.hostingRegistry is nil, e.g. Databases
// was never configured) or for a non-admin caller.
func (s *Session) systemRealmsRows() ([][]types.Value, error) {
	if s.hostingRegistry == nil {
		return [][]types.Value{}, nil
	}
	if !(s.acl == nil || s.isAdmin()) {
		return [][]types.Value{}, nil
	}
	m := s.hostingRegistry.Manifest()
	out := make([][]types.Value, 0, len(m.Realms))
	for _, r := range m.Realms {
		var zero [32]byte
		out = append(out, []types.Value{
			types.StringValue(r.ID.String()),
			types.StringValue(r.Name),
			types.StringValue(hostingStateName(r.State)),
			sysDec(int64(len(r.Databases)), 10),
			sysDec(int64(r.StorageCapBytes), 20),
			types.BoolValue(r.RealmRootAuthHash != zero),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i][1].Str < out[j][1].Str })
	return out, nil
}

func (s *Session) systemDatabasesRows() ([][]types.Value, error) {
	if s.hostingRegistry == nil {
		return [][]types.Value{}, nil
	}
	if !(s.acl == nil || s.isAdmin()) {
		return [][]types.Value{}, nil
	}
	m := s.hostingRegistry.Manifest()
	var out [][]types.Value
	for _, r := range m.Realms {
		for _, d := range r.Databases {
			out = append(out, []types.Value{
				types.StringValue(r.ID.String()),
				types.StringValue(r.Name),
				types.StringValue(d.ID.String()),
				types.StringValue(d.Name),
				types.StringValue(hostingStateName(d.State)),
				types.StringValue(hostingLayoutName(d.Layout)),
				sysDec(int64(d.StorageCapBytes), 20),
			})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i][1].Str != out[j][1].Str {
			return out[i][1].Str < out[j][1].Str
		}
		return out[i][3].Str < out[j][3].Str
	})
	return out, nil
}

func (s *Session) systemTasksRows() ([][]types.Value, error) {
	if s.db == nil || s.db.CatTree == nil {
		return [][]types.Value{}, nil
	}
	// Tasks live only in the catalog tree (not the in-memory catalog.Store),
	// so unlike the other system.* views this needs a scan txn; reuse one
	// already open on the session, or open a short-lived autocommit read.
	auto := false
	if s.x == nil {
		if err := s.startRead(txn.SnapshotIsolation); err != nil {
			return nil, err
		}
		auto = true
	}
	out, err := s.scanTaskRows()
	if auto {
		if err != nil {
			_ = s.abort()
			return nil, err
		}
		if _, cerr := s.commit(); cerr != nil {
			return nil, cerr
		}
	}
	return out, err
}

func (s *Session) scanTaskRows() ([][]types.Value, error) {
	tx := s.x.use(s.db.CatTree)
	admin := s.acl == nil || s.isAdmin()
	var start, end []byte
	if admin {
		start, end = catalog.TaskKey(""), []byte{catalog.KeyTask + 1}
	} else {
		start, end = catalog.TaskOwnerRange(s.user)
	}
	var out [][]types.Value
	err := tx.Range(start, end, func(key, value []byte) error {
		raw := value
		if !admin {
			id := string(value)
			var lerr error
			raw, lerr = tx.Lookup(catalog.TaskKey(id))
			if lerr != nil {
				return nerr.New(nerr.InvalidFormat, "executor.system", "dangling task owner index")
			}
		} else if len(key) < 1 || key[0] != catalog.KeyTask {
			return nerr.New(nerr.InvalidFormat, "executor.system", "invalid task key")
		}
		task, derr := catalog.DecodeTask(raw)
		if derr != nil {
			return derr
		}
		out = append(out, []types.Value{
			types.StringValue(task.ID),
			types.StringValue(task.Schedule),
			types.StringValue(task.Workflow),
			types.StringValue(taskStateName(task.State)),
			sysDec(int64(task.Attempt), 10),
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(out, func(i, j int) bool { return out[i][0].Str < out[j][0].Str })
	return out, nil
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
	sort.Slice(out, func(i, j int) bool {
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
	sort.Slice(out, func(i, j int) bool { return out[i][0].Str < out[j][0].Str })
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
	sort.Slice(out, func(i, j int) bool {
		if out[i][0].Str == out[j][0].Str {
			return out[i][1].Str < out[j][1].Str
		}
		return out[i][0].Str < out[j][0].Str
	})
	return out, nil
}

// systemSessionsRows lists sessions registered by DB.RegisterSession — i.e.
// live network connections on this node. A non-admin sees only their own
// sessions; an admin sees every session.
func (s *Session) systemSessionsRows() ([][]types.Value, error) {
	if s.db == nil {
		return [][]types.Value{}, nil
	}
	admin := s.acl == nil || s.isAdmin()
	live := s.db.LiveSessions()
	type row struct {
		id     uint64
		user   string
		remote string
		state  string
	}
	rows := make([]row, 0, len(live))
	for _, sess := range live {
		if sess == nil || sess.SessionID() == 0 {
			continue
		}
		user := sess.User()
		if !admin && !strings.EqualFold(user, s.user) {
			continue
		}
		_, _, _, running := sess.CurrentQuery()
		state := "idle"
		if running {
			state = "active"
		}
		rows = append(rows, row{sess.SessionID(), user, sess.Remote(), state})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	out := make([][]types.Value, 0, len(rows))
	for _, r := range rows {
		out = append(out, []types.Value{
			types.StringValue(strconv.FormatUint(r.id, 10)),
			types.StringValue(r.user),
			types.StringValue(r.remote),
			types.StringValue(r.state),
		})
	}
	return out, nil
}

// systemActiveQueriesRows lists the statement (if any) each registered
// session is presently executing. A non-admin sees only their own queries;
// an admin sees every session's.
func (s *Session) systemActiveQueriesRows() ([][]types.Value, error) {
	if s.db == nil {
		return [][]types.Value{}, nil
	}
	admin := s.acl == nil || s.isAdmin()
	live := s.db.LiveSessions()
	type row struct {
		id   uint64
		user string
		sql  string
	}
	rows := make([]row, 0, len(live))
	for _, sess := range live {
		if sess == nil {
			continue
		}
		qid, text, _, running := sess.CurrentQuery()
		if !running {
			continue
		}
		user := sess.User()
		if !admin && !strings.EqualFold(user, s.user) {
			continue
		}
		rows = append(rows, row{qid, user, text})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	out := make([][]types.Value, 0, len(rows))
	for _, r := range rows {
		out = append(out, []types.Value{
			types.StringValue(strconv.FormatUint(r.id, 10)),
			types.StringValue(r.user),
			types.StringValue(r.sql),
			types.StringValue("running"),
		})
	}
	return out, nil
}

// systemTransactionsRows lists sessions with a currently open transaction.
// A non-admin sees only their own transactions; an admin sees every
// session's.
func (s *Session) systemTransactionsRows() ([][]types.Value, error) {
	if s.db == nil {
		return [][]types.Value{}, nil
	}
	admin := s.acl == nil || s.isAdmin()
	live := s.db.LiveSessions()
	type row struct {
		id   format.TxnID
		user string
		iso  string
	}
	rows := make([]row, 0, len(live))
	for _, sess := range live {
		if sess == nil {
			continue
		}
		txnID, iso, _, _, active := sess.TxnSnapshot()
		if !active {
			continue
		}
		user := sess.User()
		if !admin && !strings.EqualFold(user, s.user) {
			continue
		}
		rows = append(rows, row{txnID, user, iso.String()})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].id < rows[j].id })
	out := make([][]types.Value, 0, len(rows))
	for _, r := range rows {
		out = append(out, []types.Value{
			types.StringValue(strconv.FormatUint(uint64(r.id), 10)),
			types.StringValue(r.user),
			types.StringValue(r.iso),
			types.StringValue("active"),
		})
	}
	return out, nil
}

// systemChangeStreamsRows lists currently open CDC subscriptions on this
// node, filtered to tables the caller may see.
func (s *Session) systemChangeStreamsRows() ([][]types.Value, error) {
	if s.db == nil {
		return [][]types.Value{}, nil
	}
	subs := s.db.CDCSubscriptions()
	type row struct {
		table string
		lsn   uint64
	}
	rows := make([]row, 0, len(subs))
	for _, sub := range subs {
		if !s.canSeeTable(sub.Table) {
			continue
		}
		rows = append(rows, row{sub.Table, sub.LSN})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].table == rows[j].table {
			return rows[i].lsn < rows[j].lsn
		}
		return rows[i].table < rows[j].table
	})
	out := make([][]types.Value, 0, len(rows))
	for _, r := range rows {
		out = append(out, []types.Value{
			types.StringValue(r.table),
			sysDec(int64(r.lsn), 20),
			types.StringValue("active"),
		})
	}
	return out, nil
}

// systemLocksRows lists every key/range lock currently held in this
// database's storage engine (internal/txn.LockManager.Snapshot; waiting,
// not-yet-granted requests are not included, so granted is always true
// today). table_name reflects the tags threaded through btree.Tree.SetName
// at the executor's tree-resolver call sites; a lock acquired through an
// untagged path reports an empty table_name rather than a wrong one.
// Visibility matches system.transactions: a lock is attributed to the user
// of whichever live registered session currently holds that transaction; an
// admin sees every lock, a non-admin sees only their own, and a lock whose
// transaction cannot be attributed to a live session (e.g. embedded/CLI/test
// use never registered with DB.RegisterSession) is visible only to an admin.
func (s *Session) systemLocksRows() ([][]types.Value, error) {
	if s.db == nil {
		return [][]types.Value{}, nil
	}
	admin := s.acl == nil || s.isAdmin()
	userByTxn := make(map[format.TxnID]string)
	for _, sess := range s.db.LiveSessions() {
		if sess == nil {
			continue
		}
		if txnID, _, _, _, active := sess.TxnSnapshot(); active {
			userByTxn[txnID] = sess.User()
		}
	}
	infos := s.db.LockSnapshot()
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].Txn != infos[j].Txn {
			return infos[i].Txn < infos[j].Txn
		}
		if infos[i].Tag != infos[j].Tag {
			return infos[i].Tag < infos[j].Tag
		}
		if infos[i].Mode != infos[j].Mode {
			return infos[i].Mode < infos[j].Mode
		}
		return !infos[i].Range && infos[j].Range
	})
	counts := make(map[format.TxnID]int)
	out := make([][]types.Value, 0, len(infos))
	for _, li := range infos {
		user, known := userByTxn[li.Txn]
		if !admin && (!known || !strings.EqualFold(user, s.user)) {
			continue
		}
		n := counts[li.Txn]
		counts[li.Txn] = n + 1
		out = append(out, []types.Value{
			types.StringValue(fmt.Sprintf("%d:%d", uint64(li.Txn), n)),
			types.StringValue(li.Tag),
			types.StringValue(lockModeName(li.Mode)),
			types.BoolValue(true),
		})
	}
	return out, nil
}

func lockModeName(m txn.Mode) string {
	switch m {
	case txn.Shared:
		return "shared"
	case txn.Exclusive:
		return "exclusive"
	default:
		return "unknown"
	}
}

// systemUsersRows, systemRolesRows, and systemGrantsRows back a Manager-style
// security dashboard entirely through the official system.* surface: before
// these landed, listing users/roles/grants had no SQL-level answer at all,
// so a Studio/Manager implementation would have had to read the auth.Store
// or security.ACL files directly. Like system.tasks/system.locks, they are
// admin-only: a non-admin gets zero rows rather than an error, matching the
// rest of this package's "row-filter, never fail, on RBAC" convention for
// SELECT against system.*. Never expose password hashes or salts.

func (s *Session) systemUsersRows() ([][]types.Value, error) {
	if s.users == nil {
		return [][]types.Value{}, nil
	}
	if !(s.acl == nil || s.isAdmin()) {
		return [][]types.Value{}, nil
	}
	infos := s.users.SnapshotInRealm(s.realmID)
	out := make([][]types.Value, 0, len(infos))
	for _, u := range infos {
		out = append(out, []types.Value{
			types.StringValue(u.Name),
			types.StringValue(u.Algo),
		})
	}
	return out, nil
}

func (s *Session) systemRolesRows() ([][]types.Value, error) {
	if s.acl == nil || !s.isAdmin() {
		return [][]types.Value{}, nil
	}
	roles, _ := s.acl.SnapshotInRealm(s.realmID)
	out := make([][]types.Value, 0, len(roles))
	for _, r := range roles {
		out = append(out, []types.Value{
			types.StringValue(r.Role),
			types.StringValue(strings.Join(r.Members, ",")),
		})
	}
	return out, nil
}

func (s *Session) systemGrantsRows() ([][]types.Value, error) {
	if s.acl == nil || !s.isAdmin() {
		return [][]types.Value{}, nil
	}
	_, grants := s.acl.SnapshotInRealm(s.realmID)
	out := make([][]types.Value, 0, len(grants))
	for _, g := range grants {
		out = append(out, []types.Value{
			types.StringValue(g.Grantee),
			types.StringValue(g.Priv.String()),
			types.StringValue(g.Scope.String()),
			types.StringValue(g.Object),
		})
	}
	return out, nil
}

func (s *Session) canSeeTable(name string) bool {
	if s.acl == nil {
		return true
	}
	if s.isAdmin() {
		return true
	}
	if s.authAllowed(s.user, security.PrivSelect, security.ScopeTable, name) {
		return true
	}
	if s.authAllowed(s.user, security.PrivSelect, security.ScopeDatabase, "") {
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
	if s.authAllowed(s.user, security.PrivExecute, security.ScopeFunction, name) {
		return true
	}
	if s.authAllowed(s.user, security.PrivSelect, security.ScopeDatabase, "") {
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
		if ord >= 0 && ord < len(t.Columns) {
			name := t.Columns[ord].Name
			if i == 0 && len(idx.Path) > 0 {
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
		if ord >= 0 && ord < len(t.Columns) {
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
	case catalog.PartitionLegacyTenant:
		return "legacy_tenant"
	default:
		return "none"
	}
}
