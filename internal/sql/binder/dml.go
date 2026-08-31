package binder

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func bindInsertRows(table string, columns []string, rows [][]ast.Expr, lookup Lookup, kind string) (*catalog.Table, []int, error) {
	tab, err := mustTable(lookup, table)
	if err != nil {
		return nil, nil, err
	}
	cols := make([]int, 0, len(tab.Columns))
	if len(columns) == 0 {
		for i := range tab.Columns {
			cols = append(cols, i)
		}
	} else {
		seen := make(map[int]struct{}, len(columns))
		for _, name := range columns {
			i, ok := tab.ColIndex(name)
			if !ok {
				return nil, nil, nerr.New(nerr.NotFound, "sql.binder", "unknown insert column")
			}
			if _, dup := seen[i]; dup {
				return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate insert column")
			}
			seen[i] = struct{}{}
			cols = append(cols, i)
		}
	}
	for _, row := range rows {
		if len(row) != len(cols) {
			return nil, nil, nerr.New(nerr.InvalidArgument, "sql.binder", kind+" value count mismatch")
		}
		for i, ex := range row {
			if err := checkExpr(ex, tab, tab.Columns[cols[i]].Type, false); err != nil {
				return nil, nil, err
			}
			if err := rejectSearchHL(ex); err != nil {
				return nil, nil, err
			}
		}
	}
	return tab, cols, nil
}

func bindUpsert(s ast.Upsert, lookup Lookup) (Bound, error) {
	tab, cols, err := bindInsertRows(s.Table, s.Columns, s.Rows, lookup, "UPSERT")
	if err != nil {
		return nil, err
	}
	if tab.Partitioning != nil && tab.Partitioning.Kind == catalog.PartitionLegacyTenant {
		// Legacy shared-tenant tables are compatibility/migration surface only.
		return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "UPSERT is not supported on legacy TENANT-partitioned tables")
	}
	uniqueCols, uniquePK, uniqueIdx, err := resolveUpsertTarget(tab, s.OnUnique, cols)
	if err != nil {
		return nil, err
	}
	evalTab := excludedSchema(tab)
	var sets []Set
	defaultSet := len(s.Sets) == 0
	if !defaultSet {
		seen := make(map[int]struct{}, len(s.Sets))
		for _, a := range s.Sets {
			i, ok := tab.ColIndex(a.Name)
			if !ok {
				return nil, nerr.New(nerr.NotFound, "sql.binder", "unknown update column")
			}
			if _, dup := seen[i]; dup {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate update column")
			}
			seen[i] = struct{}{}
			ex := rewriteQual(a.Expr, evalTab)
			if containsWindow(ex) || containsGroupingAgg(ex) {
				return nil, nerr.New(nerr.InvalidArgument, "sql.binder", "UPSERT SET does not support window functions or aggregates")
			}
			if err := checkExpr(ex, evalTab, tab.Columns[i].Type, false); err != nil {
				return nil, err
			}
			if err := rejectSearchHL(ex); err != nil {
				return nil, err
			}
			sets = append(sets, Set{Col: i, Expr: ex})
		}
	}
	ret, err := bindReturning(s.ReturningStar, s.Returning, tab, evalTab)
	if err != nil {
		return nil, err
	}
	return Upsert{
		Table:      tab,
		Columns:    cols,
		Rows:       s.Rows,
		UniqueCols: uniqueCols,
		UniquePK:   uniquePK,
		UniqueIdx:  uniqueIdx,
		Sets:       sets,
		DefaultSet: defaultSet,
		Returning:  ret,
	}, nil
}

func bindReturning(star bool, list []ast.SelectItem, tab, eval *catalog.Table) (Returning, error) {
	if !star && len(list) == 0 {
		return Returning{}, nil
	}
	if eval == nil {
		eval = tab
	}
	if star {
		if len(list) > 0 {
			return Returning{}, nerr.New(nerr.Syntax, "sql.binder", "RETURNING * cannot mix with other items")
		}
		r := Returning{Eval: tab}
		for i, c := range tab.Columns {
			r.Cols = append(r.Cols, i)
			r.Exprs = append(r.Exprs, ast.Ident{Name: c.Name})
			r.Names = append(r.Names, c.Name)
		}
		return r, nil
	}
	r := Returning{Eval: eval}
	for _, item := range list {
		ex := rewriteQual(item.Expr, eval)
		if containsWindow(ex) || containsGroupingAgg(ex) {
			return Returning{}, nerr.New(nerr.InvalidArgument, "sql.binder", "RETURNING does not support window functions or aggregates")
		}
		if err := rejectSearchHL(ex); err != nil {
			return Returning{}, err
		}
		if err := checkExpr(ex, eval, types.Type{}, false); err != nil {
			return Returning{}, err
		}
		name := item.Alias
		ord := -1
		if id, ok := ex.(ast.Ident); ok {
			if i, found := tab.ColIndex(id.Name); found {
				ord = i
				if name == "" {
					name = tab.Columns[i].Name
				}
			} else if name == "" {
				name = id.Name
			}
		}
		if name == "" {
			if call, ok := ex.(ast.Call); ok && call.Name != "" {
				name = call.Name
			} else {
				name = "?column?"
			}
		}
		r.Cols = append(r.Cols, ord)
		r.Exprs = append(r.Exprs, ex)
		r.Names = append(r.Names, name)
	}
	return r, nil
}

func excludedSchema(tab *catalog.Table) *catalog.Table {
	out := tab.Clone()
	for _, c := range tab.Columns {
		out.Columns = append(out.Columns, catalog.Column{
			Name:    "excluded." + c.Name,
			Type:    c.Type,
			NotNull: c.NotNull,
		})
	}
	return out
}

func resolveUpsertTarget(tab *catalog.Table, onUnique []string, insertCols []int) ([]int, bool, string, error) {
	if len(onUnique) > 0 {
		seen := make(map[int]struct{}, len(onUnique))
		cols := make([]int, 0, len(onUnique))
		for _, name := range onUnique {
			i, ok := tab.ColIndex(name)
			if !ok {
				return nil, false, "", nerr.New(nerr.NotFound, "sql.binder", "unknown ON UNIQUE column")
			}
			if _, dup := seen[i]; dup {
				return nil, false, "", nerr.New(nerr.InvalidArgument, "sql.binder", "duplicate ON UNIQUE column")
			}
			seen[i] = struct{}{}
			cols = append(cols, i)
		}
		if sameIntSet(cols, tab.PK) {
			return append([]int(nil), tab.PK...), true, "", nil
		}
		if idx, ok := uniqueBtreeOn(tab, cols); ok {
			return append([]int(nil), idx.Columns...), false, idx.Name, nil
		}
		if jsonPathUniqueOn(tab, cols) {
			return nil, false, "", nerr.New(nerr.InvalidArgument, "sql.binder", "UPSERT ON UNIQUE does not support JSON-path indexes")
		}
		return nil, false, "", nerr.New(nerr.InvalidArgument, "sql.binder", "ON UNIQUE must name the PRIMARY KEY or a UNIQUE btree index")
	}
	insertSet := make(map[int]struct{}, len(insertCols))
	for _, c := range insertCols {
		insertSet[c] = struct{}{}
	}
	if covered(tab.PK, insertSet) {
		return append([]int(nil), tab.PK...), true, "", nil
	}
	var matches []catalog.Index
	for _, idx := range tab.Indexes {
		if !usableUnique(idx) {
			continue
		}
		if covered(idx.Columns, insertSet) {
			matches = append(matches, idx)
		}
	}
	if len(matches) == 1 {
		return append([]int(nil), matches[0].Columns...), false, matches[0].Name, nil
	}
	return nil, false, "", nerr.New(nerr.InvalidArgument, "sql.binder", "UPSERT requires ON UNIQUE")
}

func uniqueBtreeOn(tab *catalog.Table, cols []int) (catalog.Index, bool) {
	if tab == nil {
		return catalog.Index{}, false
	}
	for _, idx := range tab.Indexes {
		if !usableUnique(idx) {
			continue
		}
		if sameIntSet(idx.Columns, cols) {
			return idx, true
		}
	}
	return catalog.Index{}, false
}

func jsonPathUniqueOn(tab *catalog.Table, cols []int) bool {
	if tab == nil {
		return false
	}
	for _, idx := range tab.Indexes {
		if !idx.Unique || len(idx.Path) == 0 {
			continue
		}
		if sameIntSet(idx.Columns, cols) {
			return true
		}
	}
	return false
}

func usableUnique(idx catalog.Index) bool {
	return idx.Unique && !idx.Fulltext && !idx.Vector && !idx.Spatial && len(idx.Path) == 0 && !idx.HasExpr()
}

func covered(cols []int, have map[int]struct{}) bool {
	if len(cols) == 0 {
		return false
	}
	for _, c := range cols {
		if _, ok := have[c]; !ok {
			return false
		}
	}
	return true
}

func sameIntSet(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	seen := make(map[int]int, len(a))
	for _, o := range a {
		seen[o]++
	}
	for _, o := range b {
		n := seen[o]
		if n == 0 {
			return false
		}
		if n == 1 {
			delete(seen, o)
		} else {
			seen[o] = n - 1
		}
	}
	return len(seen) == 0
}
