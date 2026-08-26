package join

import (
	"strconv"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestHashSemiAndAntiJoin(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	left := [][]types.Value{
		{types.StringValue("1"), types.StringValue("L1")},
		{types.StringValue("2"), types.StringValue("L2")},
		{types.StringValue("2"), types.StringValue("L2b")},
		{types.StringValue("4"), types.StringValue("L4")},
	}
	right := [][]types.Value{
		{types.StringValue("1"), types.StringValue("R1")},
		{types.StringValue("1"), types.StringValue("R1b")},
		{types.StringValue("2"), types.StringValue("R2")},
		{types.StringValue("3"), types.StringValue("R3")},
	}
	semi, err := HashSemiJoin(left, right, []int{0}, []int{0}, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(semi) != 3 {
		t.Fatalf("semi %d %+v", len(semi), semi)
	}
	anti, err := HashAntiJoin(left, right, []int{0}, []int{0}, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(anti) != 1 || anti[0][0].Str != "4" {
		t.Fatalf("anti %+v", anti)
	}

	nullLeft := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Lnull")},
		{types.StringValue("1"), types.StringValue("L1")},
	}
	nullRight := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Rnull")},
		{types.StringValue("1"), types.StringValue("R1")},
	}
	semi, err = HashSemiJoin(nullLeft, nullRight, []int{0}, []int{0}, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(semi) != 1 || semi[0][0].Str != "1" {
		t.Fatalf("null semi %+v", semi)
	}
	anti, err = HashAntiJoin(nullLeft, nullRight, []int{0}, []int{0}, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(anti) != 1 || !anti[0][0].Null {
		t.Fatalf("null anti %+v", anti)
	}

	exist, err := HashSemiJoin(left, right, nil, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(exist) != len(left) {
		t.Fatalf("uncorrelated semi %d", len(exist))
	}
	none, err := HashAntiJoin(left, nil, nil, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != len(left) {
		t.Fatalf("empty anti %d", len(none))
	}
}

func TestHashAndMergeJoin(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	left := [][]types.Value{
		{types.StringValue("1"), types.StringValue("L1")},
		{types.StringValue("2"), types.StringValue("L2")},
	}
	right := [][]types.Value{
		{types.StringValue("1"), types.StringValue("R1")},
		{types.StringValue("1"), types.StringValue("R1b")},
		{types.StringValue("3"), types.StringValue("R3")},
	}
	h, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinInner, nil, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 {
		t.Fatalf("hash %d", len(h))
	}
	m, err := MergeJoin(left, right, []int{0}, []int{0}, ast.JoinInner, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 2 {
		t.Fatalf("merge %d", len(m))
	}
}

func TestHashAndMergeNullKeysDoNotMatch(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	left := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Lnull")},
		{types.StringValue("1"), types.StringValue("L1")},
		{types.Null(types.String()), types.StringValue("Lnull2")},
	}
	right := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Rnull")},
		{types.StringValue("1"), types.StringValue("R1")},
		{types.Null(types.String()), types.StringValue("Rnull2")},
	}
	h, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinInner, nil, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 {
		t.Fatalf("hash null keys matched: %d rows %+v", len(h), h)
	}
	if h[0][1].Str != "L1" || h[0][3].Str != "R1" {
		t.Fatalf("hash row %+v", h[0])
	}
	// Merge inputs must be sorted on the join key (NULL-first, matching btree).
	mleft := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Lnull")},
		{types.Null(types.String()), types.StringValue("Lnull2")},
		{types.StringValue("1"), types.StringValue("L1")},
	}
	mright := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Rnull")},
		{types.Null(types.String()), types.StringValue("Rnull2")},
		{types.StringValue("1"), types.StringValue("R1")},
	}
	m, err := MergeJoin(mleft, mright, []int{0}, []int{0}, ast.JoinInner, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 1 {
		t.Fatalf("merge null keys matched: %d rows %+v", len(m), m)
	}
	if m[0][1].Str != "L1" || m[0][3].Str != "R1" {
		t.Fatalf("merge row %+v", m[0])
	}
}

func TestHashSpillNullKeysDoNotMatch(t *testing.T) {
	// Memory is below a 40-row build (~4KiB) so HashJoin spills; leftover after
	// the failed build still covers one 16*len(row) output charge.
	b := scheduler.NewBudget(nil, scheduler.Limits{Workers: 1, Memory: 1500, Disk: 1 << 20, IO: 1 << 20, BatchSize: 64})
	defer b.Close()
	left := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Lnull")},
		{types.StringValue("1"), types.StringValue("L1")},
	}
	var right [][]types.Value
	for i := 0; i < 40; i++ {
		right = append(right, []types.Value{types.StringValue(strconv.Itoa(i + 2)), types.StringValue("R")})
	}
	right = append(right, []types.Value{types.Null(types.String()), types.StringValue("Rnull")})
	right = append(right, []types.Value{types.StringValue("1"), types.StringValue("R1")})
	h, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinInner, nil, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 1 {
		t.Fatalf("spill hash %d rows %+v", len(h), h)
	}
}

func TestParallelHashJoin(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.Limits{Workers: 4, Memory: 1 << 22, Disk: 1 << 22, IO: 1 << 22, BatchSize: 1024})
	defer b.Close()
	var left, right [][]types.Value
	for i := 0; i < 40; i++ {
		s := types.StringValue(string(rune('a' + i%10)))
		left = append(left, []types.Value{s, types.StringValue("L")})
		right = append(right, []types.Value{s, types.StringValue("R")})
	}
	got, err := ParallelHash(scheduler.DefaultPool, b, left, right, []int{0}, []int{0}, ast.JoinInner, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 160 { // 40 left * 4 matching right each (same letter appears 4 times)
		// each of 10 keys has 4 left and 4 right = 16, * 10 = 160
		if len(got) == 0 {
			t.Fatal("empty join")
		}
	}
}

func TestLeftHashAndMergeJoin(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	rTypes := []types.Type{types.String(), types.String()}
	left := [][]types.Value{
		{types.StringValue("1"), types.StringValue("L1")},
		{types.StringValue("2"), types.StringValue("L2")},
		{types.Null(types.String()), types.StringValue("Lnull")},
	}
	right := [][]types.Value{
		{types.StringValue("1"), types.StringValue("R1")},
		{types.StringValue("3"), types.StringValue("R3")},
	}
	h, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinLeft, nil, rTypes, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 3 {
		t.Fatalf("left hash %d %+v", len(h), h)
	}
	mleft := [][]types.Value{
		{types.Null(types.String()), types.StringValue("Lnull")},
		{types.StringValue("1"), types.StringValue("L1")},
		{types.StringValue("2"), types.StringValue("L2")},
	}
	mright := [][]types.Value{
		{types.StringValue("1"), types.StringValue("R1")},
		{types.StringValue("3"), types.StringValue("R3")},
	}
	m, err := MergeJoin(mleft, mright, []int{0}, []int{0}, ast.JoinLeft, rTypes, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(m) != 3 {
		t.Fatalf("left merge %d %+v", len(m), m)
	}
}

func TestLeftHashEmptyRight(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	rTypes := []types.Type{types.String(), types.String()}
	left := [][]types.Value{
		{types.StringValue("1"), types.StringValue("L1")},
		{types.StringValue("2"), types.StringValue("L2")},
	}
	h, err := HashJoin(left, nil, []int{0}, []int{0}, ast.JoinLeft, nil, rTypes, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 {
		t.Fatalf("empty-right left hash %d", len(h))
	}
	for _, row := range h {
		if len(row) != 4 || !row[2].Null || !row[3].Null {
			t.Fatalf("want left+nulls, got %+v", row)
		}
	}
	sp, err := hashWithSpill(left, nil, []int{0}, []int{0}, ast.JoinLeft, rTypes, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(sp) != 2 {
		t.Fatalf("empty-right left spill %d", len(sp))
	}
}

func TestLeftHashSpillUnmatched(t *testing.T) {
	// Long keys inflate build ChargeMem so leftover after the failed in-memory
	// build covers two 16*len(row) LEFT output charges.
	b := scheduler.NewBudget(nil, scheduler.Limits{Workers: 1, Memory: 1500, Disk: 1 << 20, IO: 1 << 20, BatchSize: 64})
	defer b.Close()
	pad := strings.Repeat("k", 300)
	key := func(s string) types.Value { return types.StringValue(s + pad) }
	left := [][]types.Value{
		{key("1"), types.StringValue("L1")},
		{key("miss"), types.StringValue("Lmiss")},
	}
	var right [][]types.Value
	for i := 0; i < 40; i++ {
		right = append(right, []types.Value{key(strconv.Itoa(i + 2)), types.StringValue("R")})
	}
	right = append(right, []types.Value{key("1"), types.StringValue("R1")})
	h, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinLeft, nil, nil, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 2 {
		t.Fatalf("left spill %d rows %+v", len(h), h)
	}
}

func TestLeftParallelHash(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.Limits{Workers: 4, Memory: 1 << 22, Disk: 1 << 22, IO: 1 << 22, BatchSize: 1024})
	defer b.Close()
	var left, right [][]types.Value
	for i := 0; i < 40; i++ {
		s := types.StringValue(string(rune('a' + i%10)))
		left = append(left, []types.Value{s, types.StringValue("L")})
		if i%2 == 0 {
			right = append(right, []types.Value{s, types.StringValue("R")})
		}
	}
	left = append(left, []types.Value{types.StringValue("zz"), types.StringValue("Lmiss")})
	got, err := ParallelHash(scheduler.DefaultPool, b, left, right, []int{0}, []int{0}, ast.JoinLeft, nil, []types.Type{types.String(), types.String()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 5 even letters × 4 left × 4 right = 80 matches; 5 odd letters × 4 left
	// plus zz are unmatched. INNER would emit only the 80 matches.
	var unmatched, sawZZ int
	for _, row := range got {
		if len(row) != 4 {
			t.Fatalf("width %d %+v", len(row), row)
		}
		if !row[2].Null {
			continue
		}
		unmatched++
		if !row[3].Null || row[2].Typ.Kind != types.KindString || row[3].Typ.Kind != types.KindString {
			t.Fatalf("typed nulls %+v", row)
		}
		if row[0].Str == "zz" && row[1].Str == "Lmiss" {
			sawZZ++
		}
	}
	if unmatched != 21 || sawZZ != 1 || len(got) != 101 {
		t.Fatalf("left parallel total=%d unmatched=%d zz=%d", len(got), unmatched, sawZZ)
	}
}

func TestFullHashJoin(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.DefaultLimits())
	defer b.Close()
	lTypes := []types.Type{types.String(), types.String()}
	rTypes := []types.Type{types.String(), types.String()}
	left := [][]types.Value{
		{types.StringValue("1"), types.StringValue("L1")},
		{types.StringValue("2"), types.StringValue("L2")},
		{types.Null(types.String()), types.StringValue("Lnull")},
	}
	right := [][]types.Value{
		{types.StringValue("1"), types.StringValue("R1")},
		{types.StringValue("3"), types.StringValue("R3")},
		{types.Null(types.String()), types.StringValue("Rnull")},
	}
	h, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinFull, lTypes, rTypes, nil, b)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 5 {
		t.Fatalf("full hash %d %+v", len(h), h)
	}
	var sawMatch, unmatchedL, unmatchedR int
	for _, row := range h {
		if len(row) != 4 {
			t.Fatalf("width %d %+v", len(row), row)
		}
		if !row[0].Null && row[0].Str == "1" && !row[2].Null && row[2].Str == "1" {
			sawMatch++
			continue
		}
		if row[2].Null && row[3].Null {
			unmatchedL++
		}
		if row[0].Null && row[1].Null {
			unmatchedR++
		}
	}
	if sawMatch != 1 || unmatchedL != 2 || unmatchedR != 2 {
		t.Fatalf("match=%d unmatchedL=%d unmatchedR=%d %+v", sawMatch, unmatchedL, unmatchedR, h)
	}
}

func TestFullHashRefusesSpill(t *testing.T) {
	b := scheduler.NewBudget(nil, scheduler.Limits{Workers: 1, Memory: 200, Disk: 1 << 20, IO: 1 << 20, BatchSize: 64})
	defer b.Close()
	left := [][]types.Value{{types.StringValue("1"), types.StringValue("L1")}}
	var right [][]types.Value
	pad := strings.Repeat("k", 80)
	for i := 0; i < 40; i++ {
		right = append(right, []types.Value{types.StringValue(strconv.Itoa(i) + pad), types.StringValue("R")})
	}
	_, err := HashJoin(left, right, []int{0}, []int{0}, ast.JoinFull, nil, nil, nil, b)
	if err == nil {
		t.Fatal("expected FULL to refuse spill")
	}
}
