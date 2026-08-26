package executor

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func (s *Session) execTenant(st ast.SetTenant) (*Result, error) {
	if st.Value == nil {
		s.tenant = nil
		s.auditRecord(security.ActionTenant, "", nil)
		return &Result{}, nil
	}
	v, err := s.evalTenantExpr(st.Value)
	if err != nil {
		s.auditRecord(security.ActionTenant, "", err)
		return nil, err
	}
	if v.Null {
		s.tenant = nil
		s.auditRecord(security.ActionTenant, "", nil)
		return &Result{}, nil
	}
	bound, err := normalizeTenant(v)
	if err != nil {
		s.auditRecord(security.ActionTenant, "", err)
		return nil, err
	}
	cp := bound.Clone()
	s.tenant = &cp
	s.auditRecord(security.ActionTenant, "", nil)
	return &Result{}, nil
}

func (s *Session) evalTenantExpr(e ast.Expr) (types.Value, error) {
	switch x := e.(type) {
	case ast.Literal:
		return x.Value, nil
	case ast.Param:
		return s.lookupParam(x.Name)
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.SET TENANT", "TENANT must be a literal or parameter")
	}
}

func normalizeTenant(v types.Value) (types.Value, error) {
	switch v.Typ.Kind {
	case types.KindUUID:
		if v.Null {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.SET TENANT", "tenant must not be NULL")
		}
		return v, nil
	case types.KindString, types.KindText:
		if v.Null || v.Str == "" {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.SET TENANT", "tenant must not be empty")
		}
		if u, err := types.ParseUUID(v.Str); err == nil {
			return u, nil
		}
		return types.StringValue(v.Str), nil
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.SET TENANT", "tenant must be UUID or STRING")
	}
}

func (s *Session) boundTenant() (types.Value, bool) {
	if s == nil || s.tenant == nil || s.tenant.Null {
		return types.Value{}, false
	}
	return *s.tenant, true
}

func (s *Session) planCacheKey(sql string) string {
	tv, ok := s.boundTenant()
	if !ok {
		return sql
	}
	return sql + "\x00tenant=" + tv.String()
}

func (s *Session) isAdmin() bool {
	if s == nil || s.acl == nil {
		return false
	}
	return s.acl.Allowed(s.user, security.PrivAdmin, security.ScopeCluster, "")
}

// applyTenant rewrites DML/SELECT so a bound session cannot touch another
// tenant. Production sessions (ACL attached, not ADMIN) must bind a tenant
// before they use a tenant-keyed table. Embedded sessions with no ACL stay
// unrestricted when unbound so engine tests remain hermetic.
func (s *Session) applyTenant(stmt ast.Stmt) (ast.Stmt, error) {
	if s == nil {
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
			q, err := s.applyTenant(cte.Query)
			if err != nil {
				return nil, err
			}
			st.CTEs[i].Query = q
			inner[cte.Name] = struct{}{}
		}
		q, err := s.applyTenant(st.Query)
		if err != nil {
			return nil, err
		}
		st.Query = q
		return st, nil
	case ast.SetOperation:
		left, err := s.applyTenant(st.Left)
		if err != nil {
			return nil, err
		}
		right, err := s.applyTenant(st.Right)
		if err != nil {
			return nil, err
		}
		st.Left, st.Right = left, right
		return st, nil
	case ast.Explain:
		if _, ok := st.Stmt.(ast.SetTenant); ok {
			return nil, nerr.New(nerr.InvalidArgument, "executor.applyTenant", "cannot EXPLAIN SET TENANT")
		}
		inner, err := s.applyTenant(st.Stmt)
		if err != nil {
			return nil, err
		}
		st.Stmt = inner
		return st, nil
	case ast.Select:
		if st.FromQuery != nil {
			inner, err := s.applyTenant(st.FromQuery)
			if err != nil {
				return nil, err
			}
			st.FromQuery = inner
		}
		if err := s.applyTenantSelect(&st); err != nil {
			return nil, err
		}
		return st, nil
	case ast.Insert:
		if err := s.applyTenantInsert(&st); err != nil {
			return nil, err
		}
		if err := s.applyTenantReturning(st.Returning); err != nil {
			return nil, err
		}
		return st, nil
	case ast.Upsert:
		ins := ast.Insert{Table: st.Table, Columns: st.Columns, Rows: st.Rows}
		if err := s.applyTenantInsert(&ins); err != nil {
			return nil, err
		}
		st.Columns, st.Rows = ins.Columns, ins.Rows
		if err := s.applyTenantUpsert(&st); err != nil {
			return nil, err
		}
		if err := s.applyTenantReturning(st.Returning); err != nil {
			return nil, err
		}
		return st, nil
	case ast.Update:
		if err := s.applyTenantUpdate(&st); err != nil {
			return nil, err
		}
		if err := s.applyTenantReturning(st.Returning); err != nil {
			return nil, err
		}
		return st, nil
	case ast.Delete:
		if err := s.applyTenantDelete(&st); err != nil {
			return nil, err
		}
		if err := s.applyTenantReturning(st.Returning); err != nil {
			return nil, err
		}
		return st, nil
	case ast.Subscribe:
		if err := s.guardTenantTable(st.Table); err != nil {
			return nil, err
		}
		return st, nil
	default:
		return stmt, nil
	}
}

func copyCTENames(src map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(src)+4)
	for k, v := range src {
		out[k] = v
	}
	return out
}

func (s *Session) cteNamed(name string) bool {
	if s == nil || s.cteNames == nil || name == "" {
		return false
	}
	_, ok := s.cteNames[name]
	return ok
}

func (s *Session) applyTenantSelect(st *ast.Select) error {
	if st.FromQuery != nil {
		return s.applyTenantSelectExprs(st)
	}
	if !s.cteNamed(st.Table) {
		if err := s.guardTenantTable(st.Table); err != nil {
			return err
		}
	}
	for i := range st.Joins {
		if s.cteNamed(st.Joins[i].Table) {
			continue
		}
		if err := s.guardTenantTable(st.Joins[i].Table); err != nil {
			return err
		}
	}
	for _, j := range st.Joins {
		if j.Kind == ast.JoinFull {
			return s.recordFullTenantFilters(st)
		}
	}
	type named struct{ table, alias string }
	var pending []named
	if !s.cteNamed(st.Table) {
		pending = []named{{st.Table, st.Alias}}
	}
	for i := range st.Joins {
		j := &st.Joins[i]
		if s.cteNamed(j.Table) {
			continue
		}
		switch j.Kind {
		case ast.JoinLeft:
			on, err := s.andTenantPred(j.Table, j.Alias, true, j.On)
			if err != nil {
				return err
			}
			j.On = on
		case ast.JoinRight:
			on := j.On
			for _, t := range pending {
				var err error
				on, err = s.andTenantPred(t.table, t.alias, true, on)
				if err != nil {
					return err
				}
			}
			j.On = on
			pending = []named{{j.Table, j.Alias}}
		default:
			pending = append(pending, named{j.Table, j.Alias})
		}
	}
	joined := len(st.Joins) > 0
	for _, t := range pending {
		where, err := s.andTenantPred(t.table, t.alias, joined, st.Where)
		if err != nil {
			return err
		}
		st.Where = where
	}
	return s.applyTenantSelectExprs(st)
}

func (s *Session) applyTenantSelectExprs(st *ast.Select) error {
	var err error
	st.Where, err = s.applyTenantExpr(st.Where)
	if err != nil {
		return err
	}
	st.Having, err = s.applyTenantExpr(st.Having)
	if err != nil {
		return err
	}
	st.SearchQuery, err = s.applyTenantExpr(st.SearchQuery)
	if err != nil {
		return err
	}
	st.NearestQuery, err = s.applyTenantExpr(st.NearestQuery)
	if err != nil {
		return err
	}
	for i := range st.List {
		st.List[i].Expr, err = s.applyTenantExpr(st.List[i].Expr)
		if err != nil {
			return err
		}
	}
	for i := range st.Group {
		st.Group[i], err = s.applyTenantExpr(st.Group[i])
		if err != nil {
			return err
		}
	}
	for i := range st.Order {
		st.Order[i].Expr, err = s.applyTenantExpr(st.Order[i].Expr)
		if err != nil {
			return err
		}
	}
	for i := range st.Joins {
		st.Joins[i].On, err = s.applyTenantExpr(st.Joins[i].On)
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) applyTenantExpr(e ast.Expr) (ast.Expr, error) {
	if e == nil {
		return nil, nil
	}
	switch x := e.(type) {
	case ast.ScalarSubquery:
		q, err := s.applyTenant(x.Query)
		if err != nil {
			return nil, err
		}
		x.Query = q
		return x, nil
	case ast.InSubquery:
		left, err := s.applyTenantExpr(x.Expr)
		if err != nil {
			return nil, err
		}
		q, err := s.applyTenant(x.Query)
		if err != nil {
			return nil, err
		}
		x.Expr, x.Query = left, q
		return x, nil
	case ast.ExistsSubquery:
		q, err := s.applyTenant(x.Query)
		if err != nil {
			return nil, err
		}
		x.Query = q
		return x, nil
	case ast.Unary:
		r, err := s.applyTenantExpr(x.Right)
		if err != nil {
			return nil, err
		}
		x.Right = r
		return x, nil
	case ast.Binary:
		l, err := s.applyTenantExpr(x.Left)
		if err != nil {
			return nil, err
		}
		r, err := s.applyTenantExpr(x.Right)
		if err != nil {
			return nil, err
		}
		x.Left, x.Right = l, r
		return x, nil
	case ast.Between:
		v, err := s.applyTenantExpr(x.Expr)
		if err != nil {
			return nil, err
		}
		lo, err := s.applyTenantExpr(x.Low)
		if err != nil {
			return nil, err
		}
		hi, err := s.applyTenantExpr(x.High)
		if err != nil {
			return nil, err
		}
		x.Expr, x.Low, x.High = v, lo, hi
		return x, nil
	case ast.IsNull:
		inner, err := s.applyTenantExpr(x.Expr)
		if err != nil {
			return nil, err
		}
		x.Expr = inner
		return x, nil
	case ast.Call:
		for i := range x.Args {
			a, err := s.applyTenantExpr(x.Args[i])
			if err != nil {
				return nil, err
			}
			x.Args[i] = a
		}
		return x, nil
	case ast.Window:
		fn, err := s.applyTenantExpr(x.Fn)
		if err != nil {
			return nil, err
		}
		if call, ok := fn.(ast.Call); ok {
			x.Fn = call
		}
		for i := range x.Partition {
			p, err := s.applyTenantExpr(x.Partition[i])
			if err != nil {
				return nil, err
			}
			x.Partition[i] = p
		}
		for i := range x.Order {
			o, err := s.applyTenantExpr(x.Order[i].Expr)
			if err != nil {
				return nil, err
			}
			x.Order[i].Expr = o
		}
		return x, nil
	case ast.Case:
		op, err := s.applyTenantExpr(x.Operand)
		if err != nil {
			return nil, err
		}
		x.Operand = op
		for i := range x.Whens {
			w, err := s.applyTenantExpr(x.Whens[i].When)
			if err != nil {
				return nil, err
			}
			t, err := s.applyTenantExpr(x.Whens[i].Then)
			if err != nil {
				return nil, err
			}
			x.Whens[i].When, x.Whens[i].Then = w, t
		}
		el, err := s.applyTenantExpr(x.Else)
		if err != nil {
			return nil, err
		}
		x.Else = el
		return x, nil
	default:
		return e, nil
	}
}

func (s *Session) recordFullTenantFilters(st *ast.Select) error {
	add := func(table, alias string) error {
		pred, err := s.andTenantPred(table, alias, true, nil)
		if err != nil {
			return err
		}
		if pred == nil {
			return nil
		}
		st.TenantScanFilters = append(st.TenantScanFilters, ast.TenantScanFilter{
			Table: table,
			Alias: alias,
			Pred:  pred,
		})
		return nil
	}
	if err := add(st.Table, st.Alias); err != nil {
		return err
	}
	for _, j := range st.Joins {
		if err := add(j.Table, j.Alias); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) applyTenantUpdate(st *ast.Update) error {
	if err := s.guardTenantTable(st.Table); err != nil {
		return err
	}
	var err error
	st.Where, err = s.applyTenantExpr(st.Where)
	if err != nil {
		return err
	}
	where, err := s.andTenantPred(st.Table, "", false, st.Where)
	if err != nil {
		return err
	}
	st.Where = where
	if _, bound := s.boundTenant(); bound {
		for _, a := range st.Sets {
			if a.Name == catalog.TenantColumn {
				return nerr.New(nerr.Forbidden, "executor.applyTenant", "cannot change tenant_id while TENANT is set")
			}
		}
	}
	return nil
}

func (s *Session) applyTenantDelete(st *ast.Delete) error {
	if err := s.guardTenantTable(st.Table); err != nil {
		return err
	}
	var err error
	st.Where, err = s.applyTenantExpr(st.Where)
	if err != nil {
		return err
	}
	where, err := s.andTenantPred(st.Table, "", false, st.Where)
	if err != nil {
		return err
	}
	st.Where = where
	return nil
}

func (s *Session) applyTenantReturning(list []ast.SelectItem) error {
	for i := range list {
		ex, err := s.applyTenantExpr(list[i].Expr)
		if err != nil {
			return err
		}
		list[i].Expr = ex
	}
	return nil
}

func (s *Session) applyTenantUpsert(st *ast.Upsert) error {
	if _, bound := s.boundTenant(); bound {
		for _, a := range st.Sets {
			if a.Name == catalog.TenantColumn {
				return nerr.New(nerr.Forbidden, "executor.applyTenant", "cannot change tenant_id while TENANT is set")
			}
		}
	}
	for i := range st.Sets {
		ex, err := s.applyTenantExpr(st.Sets[i].Expr)
		if err != nil {
			return err
		}
		st.Sets[i].Expr = ex
	}
	return nil
}

func (s *Session) applyTenantInsert(st *ast.Insert) error {
	tab, ok := s.lookup(st.Table)
	if !ok {
		return nil
	}
	idx, keyed := tab.TenantCol()
	if !keyed {
		return nil
	}
	if err := s.requireTenantBound(tab.Name); err != nil {
		return err
	}
	tv, bound := s.boundTenant()
	if !bound {
		return nil
	}
	want, err := types.Coerce(tv, tab.Columns[idx].Type)
	if err != nil {
		return err
	}
	lit := ast.Literal{Value: want}
	if len(st.Columns) == 0 {
		return nil
	}
	pos := -1
	for i, name := range st.Columns {
		if name == catalog.TenantColumn {
			pos = i
			break
		}
	}
	if pos < 0 {
		st.Columns = append(st.Columns, catalog.TenantColumn)
		for i := range st.Rows {
			st.Rows[i] = append(st.Rows[i], lit)
		}
		return nil
	}
	for i := range st.Rows {
		st.Rows[i][pos] = lit
	}
	return nil
}

func (s *Session) guardTenantTable(name string) error {
	tab, ok := s.lookup(name)
	if !ok {
		return nil
	}
	if _, keyed := tab.TenantCol(); !keyed {
		return nil
	}
	return s.requireTenantBound(name)
}

func (s *Session) requireTenantBound(table string) error {
	if _, ok := s.boundTenant(); ok {
		return nil
	}
	if s.acl == nil || s.isAdmin() {
		return nil
	}
	_ = table
	return nerr.New(nerr.Forbidden, "executor.applyTenant", "SET TENANT is required for tenant-keyed tables")
}

func (s *Session) andTenantPred(table, alias string, qualified bool, where ast.Expr) (ast.Expr, error) {
	tab, ok := s.lookup(table)
	if !ok {
		return where, nil
	}
	idx, keyed := tab.TenantCol()
	if !keyed {
		return where, nil
	}
	tv, bound := s.boundTenant()
	if !bound {
		return where, nil
	}
	want, err := types.Coerce(tv, tab.Columns[idx].Type)
	if err != nil {
		return nil, err
	}
	col := catalog.TenantColumn
	if qualified {
		a := alias
		if a == "" {
			a = table
		}
		col = a + "." + catalog.TenantColumn
	}
	pred := ast.Binary{Op: "=", Left: ast.Ident{Name: col}, Right: ast.Literal{Value: want}}
	if where == nil {
		return pred, nil
	}
	return ast.Binary{Op: "AND", Left: where, Right: pred}, nil
}

func (s *Session) checkTenantRow(tab *catalog.Table, row []types.Value) error {
	idx, keyed := tab.TenantCol()
	if !keyed {
		return nil
	}
	tv, bound := s.boundTenant()
	if !bound {
		if s.acl != nil && !s.isAdmin() {
			return nerr.New(nerr.Forbidden, "executor.tenant", "SET TENANT is required for tenant-keyed tables")
		}
		return nil
	}
	if idx >= len(row) {
		return nerr.New(nerr.Forbidden, "executor.tenant", "tenant_id is missing")
	}
	want, err := types.Coerce(tv, tab.Columns[idx].Type)
	if err != nil {
		return err
	}
	if row[idx].Null {
		return nerr.New(nerr.Forbidden, "executor.tenant", "tenant_id must match the session tenant")
	}
	cmp, err := row[idx].Cmp(want)
	if err != nil || cmp != 0 {
		return nerr.New(nerr.Forbidden, "executor.tenant", "tenant_id must match the session tenant")
	}
	return nil
}

func (s *Session) tenantVisible(tab *catalog.Table, row []types.Value) bool {
	return s.checkTenantRow(tab, row) == nil
}
