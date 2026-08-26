package vector

import (
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestBatchRoundTrip(t *testing.T) {
	cols := []types.Type{types.String(), types.Type{Kind: types.KindDecimal}}
	b := New(cols, 1024)
	d, _ := types.ParseDecimal("2.5")
	if !b.AppendRow([]types.Value{types.StringValue("a"), types.DecimalValue(d, cols[1])}) {
		t.Fatal("append")
	}
	if !b.AppendRow([]types.Value{types.Null(types.String()), types.DecimalValue(d, cols[1])}) {
		t.Fatal("append2")
	}
	if b.Count != 2 {
		t.Fatal(b.Count)
	}
	r0 := b.Row(0)
	if r0[0].Str != "a" {
		t.Fatalf("%+v", r0)
	}
	if !b.Row(1)[0].Null {
		t.Fatal("expected null")
	}
	enc, err := types.EncodeRow(r0)
	if err != nil {
		t.Fatal(err)
	}
	b2 := New(cols, 1024)
	ok, err := AppendEncoded(b2, enc, cols)
	if err != nil || !ok || b2.Count != 1 {
		t.Fatalf("decode %v %v", ok, err)
	}
}

func TestFilterAndProject(t *testing.T) {
	cols := []types.Type{types.String(), types.Type{Kind: types.KindDecimal}}
	b := New(cols, 1024)
	for _, n := range []string{"1", "5", "9"} {
		d, _ := types.ParseDecimal(n)
		b.AppendRow([]types.Value{types.StringValue("n" + n), types.DecimalValue(d, cols[1])})
	}
	tab := &catalog.Table{Columns: []catalog.Column{{Name: "name", Type: cols[0]}, {Name: "n", Type: cols[1]}}}
	five, _ := types.ParseDecimal("5")
	pred := ast.Binary{Op: ">=", Left: ast.Ident{Name: "n"}, Right: ast.Literal{Value: types.DecimalValue(five, cols[1])}}
	if err := Filter(b, tab, pred, func(e ast.Expr, tab *catalog.Table, row []types.Value) (types.Value, error) {
		if bin, ok := e.(ast.Binary); ok {
			l, _ := row[1].Cmp(bin.Right.(ast.Literal).Value)
			return types.BoolValue(l >= 0), nil
		}
		return types.BoolValue(true), nil
	}); err != nil {
		t.Fatal(err)
	}
	if b.Count != 2 {
		t.Fatalf("filtered %d", b.Count)
	}
	p := b.Project([]int{0})
	if len(p.Columns) != 1 || p.Count != 2 {
		t.Fatalf("%d cols %d rows", len(p.Columns), p.Count)
	}
}
