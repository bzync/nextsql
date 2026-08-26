package vector

import (
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

// EvalFunc evaluates one expression against one row.
type EvalFunc func(e ast.Expr, tab *catalog.Table, row []types.Value) (types.Value, error)

// Filter keeps rows where pred is true. Uses a selection vector, then compact.
func Filter(b *Batch, tab *catalog.Table, pred ast.Expr, eval EvalFunc) error {
	if b == nil || pred == nil {
		return nil
	}
	sel := make([]int, 0, b.Count)
	row := make([]types.Value, len(b.Columns))
	for i := 0; i < b.Count; i++ {
		b.FillRow(i, row)
		v, err := eval(pred, tab, row)
		if err != nil {
			return err
		}
		if !v.Null && v.Typ.Kind == types.KindBool && v.Bool {
			sel = append(sel, i)
		}
	}
	b.Compact(sel)
	return nil
}

// ProjectExprs evaluates exprs into a new batch.
func ProjectExprs(b *Batch, tab *catalog.Table, exprs []ast.Expr, outTypes []types.Type, eval EvalFunc) (*Batch, error) {
	if b.Count == 0 {
		if len(outTypes) != len(exprs) {
			outTypes = make([]types.Type, len(exprs))
			for i := range outTypes {
				outTypes[i] = types.String()
			}
		}
		return New(outTypes, b.cap), nil
	}
	rows := make([][]types.Value, b.Count)
	row := make([]types.Value, len(b.Columns))
	for i := 0; i < b.Count; i++ {
		b.FillRow(i, row)
		dst := make([]types.Value, len(exprs))
		for j, ex := range exprs {
			v, err := eval(ex, tab, row)
			if err != nil {
				return nil, err
			}
			dst[j] = v
		}
		rows[i] = dst
	}
	if len(outTypes) != len(exprs) {
		outTypes = make([]types.Type, len(exprs))
		for j := range exprs {
			outTypes[j] = rows[0][j].Typ
			if outTypes[j].Kind == types.KindInvalid || outTypes[j].Kind == types.KindNull {
				outTypes[j] = types.String()
			}
		}
	}
	out := New(outTypes, b.cap)
	for _, r := range rows {
		if !out.AppendRow(r) {
			return nil, nerr.New(nerr.Internal, "vector.ProjectExprs", "batch overflow")
		}
	}
	return out, nil
}
