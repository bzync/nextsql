package protocol

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestEnumRowDescRoundTrip is a regression test for a real bug found during
// live verification of D11 (docs/design-datatypes.md): appendType/readType
// only ever carried Kind/Precision/Scale/VecElem, never Type.EnumLabels.
// ENUM is the first Datatype-expansion type whose Type needs variable-length
// wire metadata, so a column description round trip silently produced an
// ENUM Type with a nil label list — DecodeScalar then failed every ENUM
// value with "ENUM ordinal out of range" the moment it left the in-process
// executor.Session path (which never serializes Type) for the real wire
// protocol. Caught by `nextsql exec` against a live nextsqld, not by any
// in-process go test, since every other executor-level ENUM test calls
// s.Exec directly and never round-trips through this package.
func TestEnumRowDescRoundTrip(t *testing.T) {
	lim := DefaultLimits()
	et, err := types.EnumType([]string{"small", "medium", "large"})
	if err != nil {
		t.Fatal(err)
	}
	desc := RowDesc{Columns: []Column{{Name: "sz", Type: et}}}
	raw, err := EncodeRowDesc(desc, lim)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRowDesc(raw, lim)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Columns) != 1 {
		t.Fatalf("column count: %d", len(got.Columns))
	}
	gt := got.Columns[0].Type
	if gt.Kind != types.KindEnum || len(gt.EnumLabels) != 3 {
		t.Fatalf("enum labels did not survive RowDesc round trip: %+v", gt)
	}
	for i, want := range []string{"small", "medium", "large"} {
		if gt.EnumLabels[i] != want {
			t.Fatalf("label %d: got %q want %q", i, gt.EnumLabels[i], want)
		}
	}
}

// TestEnumValueRoundTrip covers appendValue/readValue directly (the DataBatch
// row-cell path and the bound-parameter path both use these): the exact
// reproduction of the bug in TestEnumRowDescRoundTrip, one level down.
func TestEnumValueRoundTrip(t *testing.T) {
	et, err := types.EnumType([]string{"small", "medium", "large"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := types.EnumValue("large", et)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := appendValue(nil, v, DefaultLimits().MaxPacket)
	if err != nil {
		t.Fatal(err)
	}
	got, next, err := readValue(raw, 0, DefaultLimits().MaxPacket)
	if err != nil {
		t.Fatalf("readValue: %v (this is exactly the live-verification failure: %q)", err, "types.EnumValueByOrdinal: ENUM ordinal out of range")
	}
	if next != len(raw) {
		t.Fatalf("trailing bytes: consumed %d of %d", next, len(raw))
	}
	if got.Str != "large" || len(got.Typ.EnumLabels) != 3 {
		t.Fatalf("value round trip: %+v", got)
	}

	// A NULL ENUM value must also carry its label list (the column's declared
	// type still needs to be reconstructable from a NULL cell).
	nv := types.Null(et)
	rawNull, err := appendValue(nil, nv, DefaultLimits().MaxPacket)
	if err != nil {
		t.Fatal(err)
	}
	gotNull, _, err := readValue(rawNull, 0, DefaultLimits().MaxPacket)
	if err != nil {
		t.Fatal(err)
	}
	if !gotNull.Null || gotNull.Typ.Kind != types.KindEnum || len(gotNull.Typ.EnumLabels) != 3 {
		t.Fatalf("null enum value round trip: %+v", gotNull)
	}
}

// TestEnumRowDescRejectsCorruptLabelCount covers readType/readEnumLabels
// bounding untrusted input: a label count above types.MaxEnumLabels must be
// rejected before any allocation, since a Type also travels the
// client->server bound-parameter path.
func TestEnumRowDescRejectsCorruptLabelCount(t *testing.T) {
	// Kind byte (KindEnum), 5 bytes of Precision/Scale/VecElem meta, then a
	// label-count u16 set far above types.MaxEnumLabels.
	raw := []byte{byte(types.KindEnum), 0, 0, 0, 0, 0, 0xFF, 0xFF}
	if _, _, err := readType(raw, 0); err == nil {
		t.Fatal("expected an oversized ENUM label count to be rejected")
	}
}
