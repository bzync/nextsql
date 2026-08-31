package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func (s *Session) planCacheKey(sql string) string { return sql }

func (s *Session) isAdmin() bool {
	if s == nil || s.acl == nil {
		return false
	}
	return s.authAllowed(s.user, security.PrivAdmin, security.ScopeCluster, "")
}

// guardLegacyTenancy is retained as the statement admission pass so older databases
// fail closed after row tenancy is removed. A tenant_id column was the legacy
// marker for a shared-tenant table. Such tables are available only to an ADMIN
// for export/migration into isolated hosted databases; ordinary sessions may
// not read or mutate them.
func (s *Session) guardLegacyTenancy(stmt ast.Stmt) (ast.Stmt, error) {
	if s == nil || stmt == nil {
		return stmt, nil
	}
	switch st := stmt.(type) {
	case ast.With:
		st.CTEs = append([]ast.CTEDef(nil), st.CTEs...)
		saved := s.cteNames
		inner := copyCTENames(saved)
		s.cteNames = inner
		defer func() { s.cteNames = saved }()
		for i, cte := range st.CTEs {
			q, err := s.guardLegacyTenancy(cte.Query)
			if err != nil {
				return nil, err
			}
			st.CTEs[i].Query = q
			inner[cte.Name] = struct{}{}
		}
		q, err := s.guardLegacyTenancy(st.Query)
		if err != nil {
			return nil, err
		}
		st.Query = q
		return st, nil
	case ast.SetOperation:
		left, err := s.guardLegacyTenancy(st.Left)
		if err != nil {
			return nil, err
		}
		right, err := s.guardLegacyTenancy(st.Right)
		if err != nil {
			return nil, err
		}
		st.Left, st.Right = left, right
		return st, nil
	case ast.Explain:
		inner, err := s.guardLegacyTenancy(st.Stmt)
		if err != nil {
			return nil, err
		}
		st.Stmt = inner
		return st, nil
	case ast.Select:
		if st.FromQuery != nil {
			inner, err := s.guardLegacyTenancy(st.FromQuery)
			if err != nil {
				return nil, err
			}
			st.FromQuery = inner
		} else if !s.cteNamed(st.Table) {
			if err := s.guardLegacyTenantTable(st.Table); err != nil {
				return nil, err
			}
		}
		for _, join := range st.Joins {
			if !s.cteNamed(join.Table) {
				if err := s.guardLegacyTenantTable(join.Table); err != nil {
					return nil, err
				}
			}
		}
		if err := s.guardLegacyTenantExprs(selectExprs(st)); err != nil {
			return nil, err
		}
		return st, nil
	case ast.Insert:
		if err := s.guardLegacyTenantTable(st.Table); err != nil {
			return nil, err
		}
		return st, s.guardLegacyTenantExprs(insertExprs(st))
	case ast.Upsert:
		if err := s.guardLegacyTenantTable(st.Table); err != nil {
			return nil, err
		}
		return st, s.guardLegacyTenantExprs(upsertExprs(st))
	case ast.Update:
		if err := s.guardLegacyTenantTable(st.Table); err != nil {
			return nil, err
		}
		exprs := []ast.Expr{st.Where}
		for _, set := range st.Sets {
			exprs = append(exprs, set.Expr)
		}
		for _, item := range st.Returning {
			exprs = append(exprs, item.Expr)
		}
		return st, s.guardLegacyTenantExprs(exprs)
	case ast.Delete:
		if err := s.guardLegacyTenantTable(st.Table); err != nil {
			return nil, err
		}
		exprs := []ast.Expr{st.Where}
		for _, item := range st.Returning {
			exprs = append(exprs, item.Expr)
		}
		return st, s.guardLegacyTenantExprs(exprs)
	case ast.Subscribe:
		return st, s.guardLegacyTenantTable(st.Table)
	default:
		return stmt, nil
	}
}

func copyCTENames(src map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(src)+4)
	for key := range src {
		out[key] = struct{}{}
	}
	return out
}

func (s *Session) cteNamed(name string) bool {
	if s == nil || name == "" {
		return false
	}
	_, ok := s.cteNames[name]
	return ok
}

func selectExprs(st ast.Select) []ast.Expr {
	exprs := []ast.Expr{st.Where, st.Having, st.SearchQuery, st.NearestQuery}
	for _, item := range st.List {
		exprs = append(exprs, item.Expr)
	}
	exprs = append(exprs, st.Group...)
	for _, item := range st.Order {
		exprs = append(exprs, item.Expr)
	}
	for _, join := range st.Joins {
		exprs = append(exprs, join.On)
	}
	return exprs
}

func insertExprs(st ast.Insert) []ast.Expr {
	var exprs []ast.Expr
	for _, row := range st.Rows {
		exprs = append(exprs, row...)
	}
	for _, item := range st.Returning {
		exprs = append(exprs, item.Expr)
	}
	return exprs
}

func upsertExprs(st ast.Upsert) []ast.Expr {
	exprs := insertExprs(ast.Insert{Rows: st.Rows, Returning: st.Returning})
	for _, set := range st.Sets {
		exprs = append(exprs, set.Expr)
	}
	return exprs
}

func (s *Session) guardLegacyTenantExprs(exprs []ast.Expr) error {
	for _, expr := range exprs {
		if err := s.guardLegacyTenantExpr(expr); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) guardLegacyTenantExpr(expr ast.Expr) error {
	if expr == nil {
		return nil
	}
	switch x := expr.(type) {
	case ast.ScalarSubquery:
		_, err := s.guardLegacyTenancy(x.Query)
		return err
	case ast.InSubquery:
		if err := s.guardLegacyTenantExpr(x.Expr); err != nil {
			return err
		}
		_, err := s.guardLegacyTenancy(x.Query)
		return err
	case ast.ExistsSubquery:
		_, err := s.guardLegacyTenancy(x.Query)
		return err
	case ast.Unary:
		return s.guardLegacyTenantExpr(x.Right)
	case ast.Binary:
		if err := s.guardLegacyTenantExpr(x.Left); err != nil {
			return err
		}
		return s.guardLegacyTenantExpr(x.Right)
	case ast.Between:
		return s.guardLegacyTenantExprs([]ast.Expr{x.Expr, x.Low, x.High})
	case ast.IsNull:
		return s.guardLegacyTenantExpr(x.Expr)
	case ast.Call:
		return s.guardLegacyTenantExprs(x.Args)
	case ast.Window:
		exprs := []ast.Expr{x.Fn}
		exprs = append(exprs, x.Partition...)
		for _, item := range x.Order {
			exprs = append(exprs, item.Expr)
		}
		return s.guardLegacyTenantExprs(exprs)
	case ast.Case:
		exprs := []ast.Expr{x.Operand, x.Else}
		for _, when := range x.Whens {
			exprs = append(exprs, when.When, when.Then)
		}
		return s.guardLegacyTenantExprs(exprs)
	default:
		return nil
	}
}

func (s *Session) guardLegacyTenantTable(name string) error {
	tab, ok := s.lookup(name)
	if !ok {
		return nil
	}
	if _, legacy := tab.LegacyTenantCol(); !legacy {
		return nil
	}
	if s.acl == nil || s.isAdmin() {
		return nil
	}
	return nerr.New(nerr.Forbidden, "executor.legacyTenant", "legacy shared-tenant tables require ADMIN migration into an isolated hosted database")
}

func (s *Session) checkLegacyTenantRow(tab *catalog.Table, _ []types.Value) error {
	if tab == nil {
		return nil
	}
	return s.guardLegacyTenantTable(tab.Name)
}

func (s *Session) legacyTenantVisible(tab *catalog.Table, row []types.Value) bool {
	return s.checkLegacyTenantRow(tab, row) == nil
}
