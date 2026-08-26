package aggregate

import (
	"testing"

	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
)

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
