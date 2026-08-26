package catalog

import (
	"bytes"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	workflowMagic        = "NSWK"
	workflowVersion      = 2
	KeyWorkflow     byte = 'W'

	MaxWorkflowParams       = 64
	MaxWorkflowStatements   = 256
	MaxWorkflowNameBytes    = 128
	MaxWorkflowDescriptor   = security.MaxSQLBytes
	MaxWorkflowDependencies = MaxWorkflowStatements + MaxWorkflowParams
)

type WorkflowDependencyKind byte

const (
	WorkflowDependencyTable WorkflowDependencyKind = iota + 1
	WorkflowDependencyWorkflow
)

type WorkflowDependency struct {
	Kind WorkflowDependencyKind
	ID   uint32
	Name string
}

const (
	workflowInsert byte = iota + 1
	workflowUpsert
	workflowUpdate
	workflowDelete
	workflowRun
)

// Workflow is the durable, versioned manual-workflow descriptor. Body contains
// only the statement kinds accepted by EncodeWorkflow.
type Workflow struct {
	ID           uint32
	Name         string
	Owner        string
	Params       []ast.WorkflowParam
	Body         []ast.Stmt
	Dependencies []WorkflowDependency
}

func (w *Workflow) Clone() *Workflow {
	if w == nil {
		return nil
	}
	raw, err := EncodeWorkflow(w)
	if err != nil {
		return nil
	}
	out, err := DecodeWorkflow(raw)
	if err != nil {
		return nil
	}
	return out
}

func WorkflowKey(name string) []byte {
	k := make([]byte, 1+len(name))
	k[0] = KeyWorkflow
	copy(k[1:], name)
	return k
}

func EncodeWorkflow(w *Workflow) ([]byte, error) {
	if err := validateWorkflow(w); err != nil {
		return nil, err
	}
	buf := append([]byte(nil), workflowMagic...)
	buf = appendU16(buf, workflowVersion)
	buf = appendU32(buf, w.ID)
	buf = appendString(buf, w.Name)
	buf = appendString(buf, w.Owner)
	buf = appendU16(buf, uint16(len(w.Params)))
	for _, p := range w.Params {
		buf = appendString(buf, p.Name)
		buf = appendType(buf, p.Type)
	}
	buf = appendU16(buf, uint16(len(w.Body)))
	for _, stmt := range w.Body {
		var err error
		buf, err = appendWorkflowStmt(buf, stmt)
		if err != nil {
			return nil, err
		}
		if len(buf) > MaxWorkflowDescriptor {
			return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "workflow descriptor exceeds size limit")
		}
	}
	buf = appendU16(buf, uint16(len(w.Dependencies)))
	for _, dep := range w.Dependencies {
		buf = append(buf, byte(dep.Kind))
		buf = appendU32(buf, dep.ID)
		buf = appendString(buf, dep.Name)
	}
	return buf, nil
}

func DecodeWorkflow(raw []byte) (*Workflow, error) {
	if len(raw) > MaxWorkflowDescriptor {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "workflow descriptor exceeds size limit")
	}
	if len(raw) < len(workflowMagic) || !bytes.Equal(raw[:len(workflowMagic)], []byte(workflowMagic)) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "bad workflow magic")
	}
	off := len(workflowMagic)
	ver, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if ver != 1 && ver != workflowVersion {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "unsupported workflow version")
	}
	w := &Workflow{}
	w.ID, off, err = takeU32(raw, off)
	if err != nil {
		return nil, err
	}
	w.Name, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	w.Owner, off, err = takeString(raw, off)
	if err != nil {
		return nil, err
	}
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if n > MaxWorkflowParams {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "workflow parameter count exceeds limit")
	}
	seen := make(map[string]struct{}, n)
	for i := 0; i < int(n); i++ {
		var p ast.WorkflowParam
		p.Name, off, err = takeString(raw, off)
		if err != nil {
			return nil, err
		}
		p.Type, off, err = takeType(raw, off)
		if err != nil {
			return nil, err
		}
		if !validWorkflowType(p.Type) {
			return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "invalid workflow parameter type")
		}
		if p.Name == "" || len(p.Name) > MaxWorkflowNameBytes {
			return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "invalid workflow parameter name")
		}
		if _, exists := seen[p.Name]; exists {
			return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "duplicate workflow parameter")
		}
		seen[p.Name] = struct{}{}
		w.Params = append(w.Params, p)
	}
	n, off, err = takeU16(raw, off)
	if err != nil {
		return nil, err
	}
	if n == 0 || n > MaxWorkflowStatements {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "invalid workflow statement count")
	}
	for i := 0; i < int(n); i++ {
		var stmt ast.Stmt
		stmt, off, err = takeWorkflowStmt(raw, off)
		if err != nil {
			return nil, err
		}
		w.Body = append(w.Body, stmt)
	}
	if ver >= 2 {
		n, off, err = takeU16(raw, off)
		if err != nil {
			return nil, err
		}
		if n > MaxWorkflowDependencies {
			return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "workflow dependency count exceeds limit")
		}
		for i := 0; i < int(n); i++ {
			if off >= len(raw) {
				return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "truncated workflow dependency")
			}
			dep := WorkflowDependency{Kind: WorkflowDependencyKind(raw[off])}
			off++
			dep.ID, off, err = takeU32(raw, off)
			if err != nil {
				return nil, err
			}
			dep.Name, off, err = takeString(raw, off)
			if err != nil {
				return nil, err
			}
			w.Dependencies = append(w.Dependencies, dep)
		}
	}
	if off != len(raw) {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", "trailing workflow bytes")
	}
	if err := validateWorkflow(w); err != nil {
		return nil, nerr.New(nerr.InvalidFormat, "catalog.DecodeWorkflow", err.Error())
	}
	return w, nil
}

func validateWorkflow(w *Workflow) error {
	if w == nil {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "nil workflow")
	}
	if w.ID == 0 || w.Name == "" || w.Owner == "" || len(w.Name) > MaxWorkflowNameBytes || len(w.Owner) > MaxWorkflowNameBytes {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "invalid workflow identity")
	}
	if len(w.Params) > MaxWorkflowParams || len(w.Body) == 0 || len(w.Body) > MaxWorkflowStatements || len(w.Dependencies) > MaxWorkflowDependencies {
		return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "workflow count exceeds limit")
	}
	seen := make(map[string]struct{}, len(w.Params))
	for _, p := range w.Params {
		if p.Name == "" || len(p.Name) > MaxWorkflowNameBytes || !validWorkflowType(p.Type) {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "invalid workflow parameter")
		}
		if _, exists := seen[p.Name]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "duplicate workflow parameter")
		}
		seen[p.Name] = struct{}{}
	}
	for _, stmt := range w.Body {
		if err := validateWorkflowStmt(stmt); err != nil {
			return err
		}
	}
	deps := make(map[string]struct{}, len(w.Dependencies))
	for _, dep := range w.Dependencies {
		if (dep.Kind != WorkflowDependencyTable && dep.Kind != WorkflowDependencyWorkflow) || dep.ID == 0 || dep.Name == "" || len(dep.Name) > MaxWorkflowNameBytes {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "invalid workflow dependency")
		}
		key := string([]byte{byte(dep.Kind)}) + dep.Name
		if _, exists := deps[key]; exists {
			return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "duplicate workflow dependency")
		}
		deps[key] = struct{}{}
	}
	return nil
}

func validateWorkflowStmt(stmt ast.Stmt) error {
	validName := func(name string) bool { return name != "" && len(name) <= MaxWorkflowNameBytes }
	validNames := func(names []string) bool {
		if len(names) > int(^uint16(0)) {
			return false
		}
		for _, name := range names {
			if !validName(name) {
				return false
			}
		}
		return true
	}
	validRows := func(rows [][]ast.Expr) bool {
		if len(rows) > int(^uint16(0)) {
			return false
		}
		for _, row := range rows {
			if len(row) > int(^uint16(0)) {
				return false
			}
			for _, expr := range row {
				if expr == nil || !validWorkflowExpr(expr) {
					return false
				}
			}
		}
		return true
	}
	validSets := func(sets []ast.Assignment) bool {
		if len(sets) > int(^uint16(0)) {
			return false
		}
		for _, set := range sets {
			if !validName(set.Name) || set.Expr == nil || !validWorkflowExpr(set.Expr) {
				return false
			}
		}
		return true
	}

	switch s := stmt.(type) {
	case ast.Insert:
		if !validName(s.Table) || !validNames(s.Columns) || !validRows(s.Rows) || s.ReturningStar || len(s.Returning) != 0 {
			return unsupportedWorkflowStmt()
		}
	case ast.Upsert:
		if !validName(s.Table) || !validNames(s.Columns) || !validRows(s.Rows) || !validNames(s.OnUnique) || !validSets(s.Sets) || s.ReturningStar || len(s.Returning) != 0 {
			return unsupportedWorkflowStmt()
		}
	case ast.Update:
		if !validName(s.Table) || !validSets(s.Sets) || (s.Where != nil && !validWorkflowExpr(s.Where)) || s.Limit < 0 || s.ReturningStar || len(s.Returning) != 0 {
			return unsupportedWorkflowStmt()
		}
	case ast.Delete:
		if !validName(s.Table) || (s.Where != nil && !validWorkflowExpr(s.Where)) || s.Limit < 0 || s.ReturningStar || len(s.Returning) != 0 {
			return unsupportedWorkflowStmt()
		}
	case ast.RunWorkflow:
		if !validName(s.Name) || len(s.Args) > int(^uint16(0)) {
			return unsupportedWorkflowStmt()
		}
		for _, arg := range s.Args {
			if arg == nil || !validWorkflowExpr(arg) {
				return unsupportedWorkflowStmt()
			}
		}
	default:
		return unsupportedWorkflowStmt()
	}
	return nil
}

func validWorkflowExpr(expr ast.Expr) bool {
	if expr == nil {
		return false
	}
	validText := func(s string) bool { return len(s) <= MaxWorkflowNameBytes }
	switch x := expr.(type) {
	case ast.Literal:
		return x.Value.Typ.Kind == types.KindNull ||
			(x.Value.Typ.Kind == types.KindDecimal && x.Value.Typ.Precision == 0) ||
			validWorkflowType(x.Value.Typ)
	case ast.Ident:
		return x.Name != "" && validText(x.Name)
	case ast.Param:
		return x.Name != "" && validText(x.Name)
	case ast.Path:
		if len(x.Parts) == 0 || len(x.Parts) > int(^uint16(0)) {
			return false
		}
		for _, part := range x.Parts {
			if part == "" || !validText(part) {
				return false
			}
		}
		return true
	case ast.Unary:
		return validText(x.Op) && validWorkflowExpr(x.Right)
	case ast.Binary:
		return validText(x.Op) && validWorkflowExpr(x.Left) && validWorkflowExpr(x.Right)
	case ast.Between:
		return validWorkflowExpr(x.Expr) && validWorkflowExpr(x.Low) && validWorkflowExpr(x.High)
	case ast.IsNull:
		return validWorkflowExpr(x.Expr)
	case ast.Call:
		if x.Name == "" || !validText(x.Name) || len(x.Args) > int(^uint16(0)) {
			return false
		}
		for _, arg := range x.Args {
			if !validWorkflowExpr(arg) {
				return false
			}
		}
		return true
	case ast.Case:
		if len(x.Whens) > int(^uint16(0)) || (x.Operand != nil && !validWorkflowExpr(x.Operand)) || (x.Else != nil && !validWorkflowExpr(x.Else)) {
			return false
		}
		for _, arm := range x.Whens {
			if !validWorkflowExpr(arm.When) || !validWorkflowExpr(arm.Then) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func validWorkflowType(t types.Type) bool {
	switch t.Kind {
	case types.KindUUID, types.KindString, types.KindText, types.KindTimestampTZ,
		types.KindJSON, types.KindBool, types.KindPoint, types.KindBox,
		types.KindLine, types.KindPolygon:
		return true
	case types.KindDecimal:
		return t.Precision >= 1 && t.Precision <= 38 && t.Scale <= t.Precision
	case types.KindVector:
		return t.VecElem == types.VecF32 && t.Precision >= 1 && t.Precision <= types.MaxVectorDim
	default:
		return false
	}
}

func appendWorkflowStmt(buf []byte, stmt ast.Stmt) ([]byte, error) {
	switch s := stmt.(type) {
	case ast.Insert:
		if s.ReturningStar || len(s.Returning) != 0 {
			return nil, unsupportedWorkflowStmt()
		}
		buf = append(buf, workflowInsert)
		buf = appendString(buf, s.Table)
		buf = appendStrings(buf, s.Columns)
		return appendExprRows(buf, s.Rows)
	case ast.Upsert:
		if s.ReturningStar || len(s.Returning) != 0 {
			return nil, unsupportedWorkflowStmt()
		}
		buf = append(buf, workflowUpsert)
		buf = appendString(buf, s.Table)
		buf = appendStrings(buf, s.Columns)
		var err error
		buf, err = appendExprRows(buf, s.Rows)
		if err != nil {
			return nil, err
		}
		buf = appendStrings(buf, s.OnUnique)
		return appendAssignments(buf, s.Sets)
	case ast.Update:
		if s.ReturningStar || len(s.Returning) != 0 || s.Limit < 0 {
			return nil, unsupportedWorkflowStmt()
		}
		buf = append(buf, workflowUpdate)
		buf = appendString(buf, s.Table)
		var err error
		buf, err = appendAssignments(buf, s.Sets)
		if err != nil {
			return nil, err
		}
		buf, err = appendExpr(buf, s.Where)
		if err != nil {
			return nil, err
		}
		return appendU64(buf, uint64(s.Limit)), nil
	case ast.Delete:
		if s.ReturningStar || len(s.Returning) != 0 || s.Limit < 0 {
			return nil, unsupportedWorkflowStmt()
		}
		buf = append(buf, workflowDelete)
		buf = appendString(buf, s.Table)
		var err error
		buf, err = appendExpr(buf, s.Where)
		if err != nil {
			return nil, err
		}
		return appendU64(buf, uint64(s.Limit)), nil
	case ast.RunWorkflow:
		buf = append(buf, workflowRun)
		buf = appendString(buf, s.Name)
		return appendExprs(buf, s.Args)
	default:
		return nil, unsupportedWorkflowStmt()
	}
}

func takeWorkflowStmt(raw []byte, off int) (ast.Stmt, int, error) {
	if off >= len(raw) {
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeWorkflowStmt", "truncated workflow statement")
	}
	tag := raw[off]
	off++
	table, next, err := takeString(raw, off)
	if err != nil {
		return nil, 0, err
	}
	off = next
	switch tag {
	case workflowInsert:
		cols, off, err := takeStrings(raw, off)
		if err != nil {
			return nil, 0, err
		}
		rows, off, err := takeExprRows(raw, off)
		return ast.Insert{Table: table, Columns: cols, Rows: rows}, off, err
	case workflowUpsert:
		cols, off, err := takeStrings(raw, off)
		if err != nil {
			return nil, 0, err
		}
		rows, off, err := takeExprRows(raw, off)
		if err != nil {
			return nil, 0, err
		}
		onUnique, off, err := takeStrings(raw, off)
		if err != nil {
			return nil, 0, err
		}
		sets, off, err := takeAssignments(raw, off)
		return ast.Upsert{Table: table, Columns: cols, Rows: rows, OnUnique: onUnique, Sets: sets}, off, err
	case workflowUpdate:
		sets, off, err := takeAssignments(raw, off)
		if err != nil {
			return nil, 0, err
		}
		where, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		limit, off, err := takeU64(raw, off)
		if err != nil || limit > uint64(^uint64(0)>>1) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeWorkflowStmt", "invalid update limit")
		}
		return ast.Update{Table: table, Sets: sets, Where: where, Limit: int64(limit)}, off, nil
	case workflowDelete:
		where, off, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		limit, off, err := takeU64(raw, off)
		if err != nil || limit > uint64(^uint64(0)>>1) {
			return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeWorkflowStmt", "invalid delete limit")
		}
		return ast.Delete{Table: table, Where: where, Limit: int64(limit)}, off, nil
	case workflowRun:
		args, off, err := takeExprs(raw, off)
		return ast.RunWorkflow{Name: table, Args: args}, off, err
	default:
		return nil, 0, nerr.New(nerr.InvalidFormat, "catalog.takeWorkflowStmt", "unknown workflow statement tag")
	}
}

func unsupportedWorkflowStmt() error {
	return nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "unsupported workflow statement")
}

func appendStrings(buf []byte, values []string) []byte {
	buf = appendU16(buf, uint16(len(values)))
	for _, value := range values {
		buf = appendString(buf, value)
	}
	return buf
}

func takeStrings(raw []byte, off int) ([]string, int, error) {
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, 0, err
	}
	values := make([]string, 0, n)
	for i := 0; i < int(n); i++ {
		value, next, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		off = next
		values = append(values, value)
	}
	return values, off, nil
}

func appendExprs(buf []byte, exprs []ast.Expr) ([]byte, error) {
	if len(exprs) > int(^uint16(0)) {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "too many workflow expressions")
	}
	buf = appendU16(buf, uint16(len(exprs)))
	for _, expr := range exprs {
		var err error
		buf, err = appendExpr(buf, expr)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func takeExprs(raw []byte, off int) ([]ast.Expr, int, error) {
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, 0, err
	}
	exprs := make([]ast.Expr, 0, n)
	for i := 0; i < int(n); i++ {
		expr, next, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		off = next
		exprs = append(exprs, expr)
	}
	return exprs, off, nil
}

func appendExprRows(buf []byte, rows [][]ast.Expr) ([]byte, error) {
	if len(rows) > int(^uint16(0)) {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "too many workflow rows")
	}
	buf = appendU16(buf, uint16(len(rows)))
	for _, row := range rows {
		var err error
		buf, err = appendExprs(buf, row)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func takeExprRows(raw []byte, off int) ([][]ast.Expr, int, error) {
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, 0, err
	}
	rows := make([][]ast.Expr, 0, n)
	for i := 0; i < int(n); i++ {
		row, next, err := takeExprs(raw, off)
		if err != nil {
			return nil, 0, err
		}
		off = next
		rows = append(rows, row)
	}
	return rows, off, nil
}

func appendAssignments(buf []byte, sets []ast.Assignment) ([]byte, error) {
	if len(sets) > int(^uint16(0)) {
		return nil, nerr.New(nerr.InvalidArgument, "catalog.EncodeWorkflow", "too many workflow assignments")
	}
	buf = appendU16(buf, uint16(len(sets)))
	for _, set := range sets {
		buf = appendString(buf, set.Name)
		var err error
		buf, err = appendExpr(buf, set.Expr)
		if err != nil {
			return nil, err
		}
	}
	return buf, nil
}

func takeAssignments(raw []byte, off int) ([]ast.Assignment, int, error) {
	n, off, err := takeU16(raw, off)
	if err != nil {
		return nil, 0, err
	}
	sets := make([]ast.Assignment, 0, n)
	for i := 0; i < int(n); i++ {
		name, next, err := takeString(raw, off)
		if err != nil {
			return nil, 0, err
		}
		off = next
		expr, next, err := takeExpr(raw, off)
		if err != nil {
			return nil, 0, err
		}
		off = next
		sets = append(sets, ast.Assignment{Name: name, Expr: expr})
	}
	return sets, off, nil
}
