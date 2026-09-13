package executor

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// A comparison between a column and a bound parameter is planned as its
// literal form, so it can use an index (see param_sarg.go). These tests hold
// that change to two properties: it picks the index, and it never changes an
// answer. The answer oracle is the same statement written so the parameter
// cannot be substituted (`$1 + 0` is an expression, not a bare parameter), which
// runs the pre-existing runtime filter.

func paramSargSession(t *testing.T) *Session {
	t.Helper()
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE ps (id INT64 PRIMARY KEY, k INT64, name STRING, amount DECIMAL(10,2), flag BOOL)`)
	var b strings.Builder
	b.WriteString(`INSERT INTO ps (id, k, name, amount, flag) VALUES `)
	for i := 0; i < 400; i++ {
		if i > 0 {
			b.WriteString(", ")
		}
		fmt.Fprintf(&b, "(%d, %d, 'n%03d', %d.25, %v)", i, i%13, i, i, i%2 == 0)
	}
	execOK(t, s, b.String())
	execOK(t, s, `CREATE INDEX ps_k ON ps (k)`)
	execOK(t, s, `CREATE INDEX ps_name ON ps (name)`)
	execOK(t, s, `ANALYZE ps`)
	return s
}

func execParams(t *testing.T, s *Session, sql string, params ...types.Value) (*Result, error) {
	t.Helper()
	ps := make([]Param, len(params))
	for i, v := range params {
		ps[i] = Param{Value: v}
	}
	return s.ExecContext(context.Background(), sql, ps)
}

func canonicalRows(res *Result) []string {
	out := make([]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		parts := make([]string, len(r))
		for i, v := range r {
			parts[i] = v.String()
		}
		out = append(out, strings.Join(parts, "|"))
	}
	sort.Strings(out)
	return out
}

func TestParamComparisonUsesTheIndex(t *testing.T) {
	s := paramSargSession(t)
	for _, tc := range []struct {
		sql   string
		param types.Value
		want  string
	}{
		{`EXPLAIN SELECT name FROM ps WHERE id = $1`, types.Int64Value(7), "IndexScan ps pk"},
		{`EXPLAIN SELECT name FROM ps WHERE $1 = id`, types.Int64Value(7), "IndexScan ps pk"},
		{`EXPLAIN SELECT name FROM ps WHERE k = $1`, types.Int64Value(3), "IndexScan ps ps_k"},
		{`EXPLAIN SELECT id FROM ps WHERE name = $1`, types.StringValue("n010"), "IndexScan ps ps_name"},
		{`EXPLAIN UPDATE ps SET k = 1 WHERE id = $1`, types.Int64Value(7), "IndexScan ps pk"},
		{`EXPLAIN DELETE FROM ps WHERE id = $1`, types.Int64Value(7), "IndexScan ps pk"},
	} {
		res, err := execParams(t, s, tc.sql, tc.param)
		if err != nil {
			t.Fatalf("%s: %v", tc.sql, err)
		}
		if plan := planText(res); !strings.Contains(plan, tc.want) {
			t.Fatalf("%s did not use %q:\n%s", tc.sql, tc.want, plan)
		}
	}
}

// The plan built for one execution's values must not be reused for another's:
// plans are cached by SQL text, and a cached plan carrying the first value
// would answer every later execution with the first row.
func TestParamSubstitutedPlanIsNotReusedAcrossValues(t *testing.T) {
	s := paramSargSession(t)
	for _, id := range []int64{5, 6, 5, 399, 0} {
		res, err := execParams(t, s, `SELECT name FROM ps WHERE id = $1`, types.Int64Value(id))
		if err != nil {
			t.Fatal(err)
		}
		if want := fmt.Sprintf("n%03d", id); len(res.Rows) != 1 || res.Rows[0][0].Str != want {
			t.Fatalf("id=%d returned %v, want %s", id, res.Rows, want)
		}
	}
}

func TestParamSubstitutionNeverChangesAnAnswer(t *testing.T) {
	s := paramSargSession(t)
	cases := []struct {
		where string
		param types.Value
	}{
		{"id = %s", types.Int64Value(42)},
		{"id = %s", types.Int32Value(42)},
		{"id = %s", types.DecimalValue(mustDecimal(t, "42"), types.Type{Kind: types.KindDecimal})},
		{"id = %s", types.DecimalValue(mustDecimal(t, "42.5"), types.Type{Kind: types.KindDecimal})},
		{"id = %s", types.StringValue("42")},
		{"id = %s", types.Int64Value(100000)},
		{"id = %s", types.Int64Value(-1)},
		{"id < %s", types.Int64Value(10)},
		{"id <= %s", types.Int64Value(10)},
		{"id > %s", types.Int64Value(390)},
		{"id >= %s", types.Int64Value(390)},
		{"id <> %s", types.Int64Value(3)},
		{"k = %s", types.Int64Value(4)},
		{"k = %s AND id > 100", types.Int64Value(4)},
		{"k = %s OR id = 1", types.Int64Value(4)},
		{"name = %s", types.StringValue("n123")},
		{"name >= %s", types.StringValue("n390")},
		{"amount = %s", types.DecimalValue(mustDecimal(t, "7.25"), types.Type{Kind: types.KindDecimal})},
		{"flag = %s", types.BoolValue(true)},
		{"id = %s", types.Null(types.Int64())},
	}
	for _, tc := range cases {
		direct := fmt.Sprintf(`SELECT id, k, name, amount, flag FROM ps WHERE `+tc.where, "$1")
		oracle := fmt.Sprintf(`SELECT id, k, name, amount, flag FROM ps WHERE `+tc.where, "($1 + 0)")
		if tc.param.Typ.Kind == types.KindString || tc.param.Typ.Kind == types.KindBool || tc.param.Null {
			// `+ 0` is not defined for these; COALESCE keeps the value while
			// still hiding the bare parameter from substitution.
			oracle = fmt.Sprintf(`SELECT id, k, name, amount, flag FROM ps WHERE `+tc.where, "COALESCE($1, $1)")
		}
		got, gotErr := execParams(t, s, direct, tc.param)
		want, wantErr := execParams(t, s, oracle, tc.param)
		if (gotErr == nil) != (wantErr == nil) {
			t.Fatalf("%s with %v: error %v, oracle error %v", tc.where, tc.param, gotErr, wantErr)
		}
		if gotErr != nil {
			continue
		}
		if g, w := canonicalRows(got), canonicalRows(want); strings.Join(g, "\n") != strings.Join(w, "\n") {
			t.Fatalf("%s with %v: %d rows, oracle %d rows", tc.where, tc.param, len(g), len(w))
		}
	}

	// Writes: the same number of rows is affected either way.
	res, err := execParams(t, s, `UPDATE ps SET k = 99 WHERE id < $1`, types.Int64Value(20))
	if err != nil || res.Affected != 20 {
		t.Fatalf("UPDATE ... WHERE id < $1 affected %v (%v), want 20", resAffected(res), err)
	}
	res, err = execParams(t, s, `DELETE FROM ps WHERE k = $1`, types.Int64Value(99))
	if err != nil || res.Affected != 20 {
		t.Fatalf("DELETE ... WHERE k = $1 affected %v (%v), want 20", resAffected(res), err)
	}
}

func resAffected(r *Result) int64 {
	if r == nil {
		return -1
	}
	return r.Affected
}
