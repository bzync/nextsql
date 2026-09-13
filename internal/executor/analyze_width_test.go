package executor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
)

// Table statistics are one catalog record, capped at half a page. Explicit
// ANALYZE of a table with more than three or four columns used to exceed it
// and fail with "record exceeds page capacity"; statistics are now reduced in a
// fixed order until the record fits (see fitStats).

func wideTable(t *testing.T, s *Session, name string, cols int, rows int) {
	t.Helper()
	var def strings.Builder
	fmt.Fprintf(&def, "CREATE TABLE %s (id INT64 PRIMARY KEY", name)
	for c := 0; c < cols; c++ {
		if c%2 == 0 {
			fmt.Fprintf(&def, ", s%d STRING", c)
		} else {
			fmt.Fprintf(&def, ", d%d DECIMAL(12,2)", c)
		}
	}
	def.WriteString(")")
	execOK(t, s, def.String())
	for r := 0; r < rows; r += 100 {
		var ins strings.Builder
		fmt.Fprintf(&ins, "INSERT INTO %s VALUES ", name)
		for i := r; i < r+100 && i < rows; i++ {
			if i > r {
				ins.WriteString(", ")
			}
			fmt.Fprintf(&ins, "(%d", i)
			for c := 0; c < cols; c++ {
				if c%2 == 0 {
					fmt.Fprintf(&ins, ", 'value-%d-%05d-some-padding-text'", c, i)
				} else {
					fmt.Fprintf(&ins, ", %d.%02d", i*(c+1), i%100)
				}
			}
			ins.WriteString(")")
		}
		execOK(t, s, ins.String())
	}
}

func TestAnalyzeSucceedsOnWideTables(t *testing.T) {
	for _, cols := range []int{4, 12, 40, 120} {
		t.Run(fmt.Sprintf("%d-columns", cols), func(t *testing.T) {
			s := testDB(t).Session()
			wideTable(t, s, "w", cols, 500)
			execOK(t, s, "ANALYZE w")
			st, ok := s.lookupStats("w")
			if !ok {
				t.Fatal("ANALYZE stored no statistics")
			}
			if st.Rows != 500 || len(st.Columns) != cols+1 {
				t.Fatalf("stats rows=%d columns=%d, want 500 and %d", st.Rows, len(st.Columns), cols+1)
			}
			for _, c := range st.Columns {
				if c.NDV == 0 {
					t.Fatalf("column %d lost its distinct count", c.Ord)
				}
			}
			// The planner still has statistics to cost with after a restart of
			// the catalog read path.
			execOK(t, s, "SELECT COUNT(*) FROM w WHERE id < 10")
		})
	}
}

// A narrow table keeps full detail: fitting must not degrade what already fit.
func TestAnalyzeKeepsFullDetailWhenItFits(t *testing.T) {
	s := testDB(t).Session()
	execOK(t, s, "CREATE TABLE n (id INT64 PRIMARY KEY, k INT64)")
	var b strings.Builder
	for i := 0; i < 500; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		fmt.Fprintf(&b, "(%d, %d)", i, i%50)
	}
	execOK(t, s, "INSERT INTO n VALUES "+b.String())
	execOK(t, s, "ANALYZE n")
	st, _ := s.lookupStats("n")
	if len(st.Columns[1].Histogram) != histBuckets || len(st.Columns[1].MCV) == 0 || len(st.Segments) == 0 {
		t.Fatalf("a table whose statistics fit lost detail: %d buckets, %d MCVs, %d segments",
			len(st.Columns[1].Histogram), len(st.Columns[1].MCV), len(st.Segments))
	}
}

// Merging buckets keeps the total row count the histogram describes exact and
// the value range it covers unchanged.
func TestMergeHistogramPairsPreservesCountsAndRange(t *testing.T) {
	var h []catalog.HistBucket
	var total uint64
	for i := 0; i < 7; i++ {
		h = append(h, catalog.HistBucket{Lower: types.Int64Value(int64(i * 10)), Upper: types.Int64Value(int64(i*10 + 9)), Count: uint64(i + 1), NDV: 1})
		total += uint64(i + 1)
	}
	m := mergeHistogramPairs(h)
	if len(m) != 4 {
		t.Fatalf("7 buckets merged into %d, want 4", len(m))
	}
	var got uint64
	for _, b := range m {
		got += b.Count
	}
	if got != total || m[0].Lower.Int != 0 || m[len(m)-1].Upper.Int != 69 {
		t.Fatalf("merged histogram count %d (want %d), range %v..%v", got, total, m[0].Lower, m[len(m)-1].Upper)
	}
}

// The fitted record is always one the catalog tree accepts.
func TestFitStatsRespectsTheRecordLimit(t *testing.T) {
	s := testDB(t).Session()
	wideTable(t, s, "wide_fit", 60, 300)
	execOK(t, s, "BEGIN")
	defer execOK(t, s, "ROLLBACK")
	tab, _ := s.lookup("wide_fit")
	st, err := s.collectStats(tab)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := fitStats(st)
	if err != nil {
		t.Fatal(err)
	}
	if limit := btree.MaxTxnValueSize(len(catalog.StatsKey("wide_fit"))); len(raw) > limit {
		t.Fatalf("fitted record is %d bytes, limit %d", len(raw), limit)
	}
}
