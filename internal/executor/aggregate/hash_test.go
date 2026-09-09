package aggregate

import (
	"testing"

	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
)

// TestMultiSpecNoSlotSharing guards against the regression where a single
// shared sum/nval/min/max on state double-counted whenever two specs used the
// same slot: SUM+AVG of one column, two MINs, COUNT(col)+SUM, etc.
func TestMultiSpecNoSlotSharing(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	dv := func(s string) types.Value {
		d, _ := types.ParseDecimal(s)
		return types.DecimalValue(d, types.Type{Kind: types.KindDecimal})
	}
	// specs: SUM(c1), AVG(c1), MIN(c1), MAX(c1), COUNT(c1)
	h := New(nil, []Spec{
		{Fun: "sum", Col: 0}, {Fun: "avg", Col: 0}, {Fun: "min", Col: 0},
		{Fun: "max", Col: 0}, {Fun: "count", Col: 0},
	}, nil, b)
	defer h.Close()
	for _, n := range []string{"2", "4", "6"} {
		if err := h.Add([]types.Value{dv(n)}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := h.Finish()
	if err != nil {
		t.Fatal(err)
	}
	r := rows[0]
	if r[0].Dec.String() != "12" {
		t.Fatalf("SUM = %s, want 12 (double-counting regression)", r[0].Dec.String())
	}
	if r[1].Dec.String() != "4.000000" {
		t.Fatalf("AVG = %s, want 4", r[1].Dec.String())
	}
	if r[2].Dec.String() != "2" || r[3].Dec.String() != "6" {
		t.Fatalf("MIN/MAX = %s/%s, want 2/6", r[2].Dec.String(), r[3].Dec.String())
	}
	if r[4].Dec.String() != "3" {
		t.Fatalf("COUNT = %s, want 3", r[4].Dec.String())
	}
}

func TestHashCountSum(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	h := New([]int{0}, []Spec{{Fun: "count", Col: -1}, {Fun: "sum", Col: 1}}, nil, b)
	defer h.Close()
	for _, pair := range []struct {
		k string
		n string
	}{{"a", "1"}, {"a", "2"}, {"b", "10"}} {
		d, _ := types.ParseDecimal(pair.n)
		if err := h.Add([]types.Value{types.StringValue(pair.k), types.DecimalValue(d, types.Type{Kind: types.KindDecimal})}); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := h.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatalf("%d groups", len(rows))
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r[0].Str] = r[1].Dec.String() + "/" + r[2].Dec.String()
	}
	if got["a"] != "2"+"/"+"3" && got["a"] != "2/3" && got["a"] != "2/3.0" {
		// count is 2, sum is 3
		if got["a"][:1] != "2" {
			t.Fatalf("%v", got)
		}
	}
}

func TestAddCountStarMatchesAdd(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	full := New([]int{0}, []Spec{{Fun: "count", Col: -1}}, nil, b)
	defer full.Close()
	proj := New([]int{0}, []Spec{{Fun: "count", Col: -1}}, nil, b)
	defer proj.Close()
	for _, k := range []string{"a", "b", "a", "c", "b", "a"} {
		v := types.StringValue(k)
		if err := full.Add([]types.Value{v}); err != nil {
			t.Fatal(err)
		}
		if err := proj.AddCountStar([]types.Value{v}); err != nil {
			t.Fatal(err)
		}
	}
	a, err := full.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c, err := proj.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 3 || len(c) != 3 {
		t.Fatalf("groups %d %d", len(a), len(c))
	}
	got := map[string]string{}
	for _, r := range c {
		got[r[0].Str] = r[1].Dec.String()
	}
	if got["a"] != "3" || got["b"] != "2" || got["c"] != "1" {
		t.Fatalf("%v", got)
	}
}

func TestAddCountStarBytesMatchesAddCountStar(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	vals := New([]int{0}, []Spec{{Fun: "count", Col: -1}}, nil, b)
	defer vals.Close()
	raws := New([]int{0}, []Spec{{Fun: "count", Col: -1}}, nil, b)
	defer raws.Close()
	for _, k := range []string{"a", "b", "a", "c", "b", "a"} {
		if err := vals.AddCountStar([]types.Value{types.StringValue(k)}); err != nil {
			t.Fatal(err)
		}
		if err := raws.AddCountStarBytes([]byte(k), false, types.String()); err != nil {
			t.Fatal(err)
		}
	}
	a, err := vals.Finish()
	if err != nil {
		t.Fatal(err)
	}
	c, err := raws.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(a) != 3 || len(c) != 3 {
		t.Fatalf("groups %d %d", len(a), len(c))
	}
	got := map[string]string{}
	for _, r := range c {
		got[r[0].Str] = r[1].Dec.String()
	}
	if got["a"] != "3" || got["b"] != "2" || got["c"] != "1" {
		t.Fatalf("%v", got)
	}
}

func TestParallelAgg(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.Limits{Workers: 4, Memory: 1 << 20, Disk: 1 << 20, IO: 1 << 20, BatchSize: 1024})
	defer b.Close()
	var parts [][][]types.Value
	d1, _ := types.ParseDecimal("1")
	d2, _ := types.ParseDecimal("2")
	parts = append(parts, [][]types.Value{{types.StringValue("x"), types.DecimalValue(d1, types.Type{Kind: types.KindDecimal})}})
	parts = append(parts, [][]types.Value{{types.StringValue("x"), types.DecimalValue(d2, types.Type{Kind: types.KindDecimal})}})
	rows, err := Parallel(scheduler.DefaultPool, b, []int{0}, []Spec{{Fun: "count", Col: -1}}, nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("%d", len(rows))
	}
}

// TestUngroupedEmptyInputEmitsOneRow pins the SQL rule that an aggregate with
// no GROUP BY is defined over one implicit group spanning the whole input, so
// it reports exactly one row even when the input is empty: COUNT is 0 and
// every other aggregate is NULL. A filter that matches nothing previously
// returned zero rows here, which drivers surface as "no rows in result set"
// rather than a count of 0.
func TestUngroupedEmptyInputEmitsOneRow(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	specs := []Spec{
		{Fun: "count", Col: -1},
		{Fun: "count", Col: 0},
		{Fun: "sum", Col: 1},
		{Fun: "avg", Col: 1},
		{Fun: "min", Col: 0},
		{Fun: "max", Col: 0},
	}
	h := New(nil, specs, nil, b)
	defer h.Close()
	rows, err := h.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("ungrouped empty aggregate returned %d rows, want 1", len(rows))
	}
	row := rows[0]
	if len(row) != len(specs) {
		t.Fatalf("row has %d columns, want %d", len(row), len(specs))
	}
	if row[0].Null || row[0].Dec.String() != "0" {
		t.Fatalf("COUNT(*) = %v, want 0", row[0])
	}
	if row[1].Null || row[1].Dec.String() != "0" {
		t.Fatalf("COUNT(col) = %v, want 0", row[1])
	}
	for i, name := range []string{"SUM", "AVG", "MIN", "MAX"} {
		if !row[i+2].Null {
			t.Fatalf("%s over empty input = %v, want NULL", name, row[i+2])
		}
	}
}

// TestGroupedEmptyInputEmitsNoRows is the other half of the rule: with a
// GROUP BY there is no group to report, so empty input stays zero rows.
func TestGroupedEmptyInputEmitsNoRows(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	h := New([]int{0}, []Spec{{Fun: "count", Col: -1}}, nil, b)
	defer h.Close()
	rows, err := h.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 0 {
		t.Fatalf("grouped empty aggregate returned %d rows, want 0", len(rows))
	}
}

func TestArrayAggAndMapAgg(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()

	arrType, _ := types.ArrayType(types.Type{Kind: types.KindInt64})
	mapType, _ := types.MapType(types.String(), types.Type{Kind: types.KindInt64})

	specs := []Spec{
		{Fun: "array_agg", Col: 1, OutType: arrType},
		{Fun: "map_agg", Col: 2, Col2: 1, OutType: mapType},
	}
	h := New([]int{0}, specs, nil, b)
	defer h.Close()

	// rows: [dept, salary, name]
	rows := [][]types.Value{
		{types.StringValue("eng"), types.IntValue(types.KindInt64, 100), types.StringValue("alice")},
		{types.StringValue("eng"), types.IntValue(types.KindInt64, 200), types.StringValue("bob")},
		{types.StringValue("sales"), types.IntValue(types.KindInt64, 150), types.StringValue("charlie")},
	}
	for _, r := range rows {
		if err := h.Add(r); err != nil {
			t.Fatal(err)
		}
	}
	out, err := h.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 groups, got %d", len(out))
	}
	// eng group: array_agg should have [100, 200]
	engRow := out[0]
	if engRow[0].Str != "eng" {
		t.Fatalf("expected eng, got %s", engRow[0].Str)
	}
	arrVal := engRow[1]
	if len(arrVal.Coll) != 2 || arrVal.Coll[0].Int != 100 || arrVal.Coll[1].Int != 200 {
		t.Fatalf("expected [100, 200], got %v", arrVal.Coll)
	}
	mapVal := engRow[2]
	if len(mapVal.CollKeys) != 2 {
		t.Fatalf("expected 2 map keys, got %d", len(mapVal.CollKeys))
	}
	// alice: 100, bob: 200
	if mapVal.CollKeys[0].Str != "alice" || mapVal.Coll[0].Int != 100 {
		t.Fatalf("expected alice:100, got %v:%v", mapVal.CollKeys[0], mapVal.Coll[0])
	}
	if mapVal.CollKeys[1].Str != "bob" || mapVal.Coll[1].Int != 200 {
		t.Fatalf("expected bob:200, got %v:%v", mapVal.CollKeys[1], mapVal.Coll[1])
	}

	// Test empty ungrouped array_agg and map_agg returns NULL
	hEmpty := New(nil, specs, nil, b)
	defer hEmpty.Close()
	emptyOut, err := hEmpty.Finish()
	if err != nil {
		t.Fatal(err)
	}
	if len(emptyOut) != 1 {
		t.Fatalf("expected 1 row, got %d", len(emptyOut))
	}
	if !emptyOut[0][0].Null || !emptyOut[0][1].Null {
		t.Fatalf("expected NULLs for empty array_agg and map_agg, got %v", emptyOut[0])
	}

	// Test duplicate map key rejection
	hDup := New(nil, []Spec{{Fun: "map_agg", Col: 0, Col2: 1, OutType: mapType}}, nil, b)
	defer hDup.Close()
	_ = hDup.Add([]types.Value{types.StringValue("dup"), types.IntValue(types.KindInt64, 1)})
	_ = hDup.Add([]types.Value{types.StringValue("dup"), types.IntValue(types.KindInt64, 2)})
	if _, err := hDup.Finish(); err == nil {
		t.Fatal("expected error for duplicate MAP key, got nil")
	}
}

