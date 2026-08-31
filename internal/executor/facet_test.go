package executor

import (
	"strconv"
	"testing"

	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestFacetDistinctValueCap(t *testing.T) {
	s := testDB(t).Session()
	rows := make([][]types.Value, fulltext.MaxFacetValues+1)
	for i := range rows {
		rows[i] = []types.Value{types.StringValue(strconv.Itoa(i))}
	}
	_, err := s.facetRows(&planner.Facet{Columns: []int{0}, Names: []string{"k"}, Limit: -1}, rows)
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("want exhausted, got %v", err)
	}
	rows = rows[:fulltext.MaxFacetValues]
	got, err := s.facetRows(&planner.Facet{Columns: []int{0}, Names: []string{"k"}, Limit: 3}, rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("limit 3 of cap: %d", len(got))
	}
}
