package types

import "testing"

func TestCoerceSparseFromDense(t *testing.T) {
	dest, err := VectorSparse(8)
	if err != nil {
		t.Fatal(err)
	}
	src := VectorValue([]float32{0.9, 0.1, 0, 0, 0, 0, 0, 0}, Type{Kind: KindVector, Precision: 8, VecElem: VecF32})
	got, err := Coerce(src, dest)
	if err != nil {
		t.Fatal(err)
	}
	if got.Typ.VecElem != VecSparse || len(got.SparseIdx) != 2 || got.SparseIdx[0] != 0 || got.SparseIdx[1] != 1 {
		t.Fatalf("got %+v idx=%v val=%v", got.Typ, got.SparseIdx, got.SparseVal)
	}
}
