package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

func TestOrderByLimitAndHidden(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL, q DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO t (n, q) VALUES ('c', 2), ('a', 1), ('b', 1), ('d', 3)`)
	res := execOK(t, s, `SELECT n FROM t ORDER BY q ASC, n DESC`)
	if len(res.Rows) != 4 {
		t.Fatalf("rows %d", len(res.Rows))
	}
	want := []string{"b", "a", "c", "d"}
	for i, w := range want {
		if res.Rows[i][0].Str != w {
			t.Fatalf("row %d got %q want %q", i, res.Rows[i][0].Str, w)
		}
	}
	if len(res.Columns) != 1 || res.Columns[0] != "n" {
		t.Fatalf("cols %+v", res.Columns)
	}
	lim := execOK(t, s, `SELECT n FROM t ORDER BY n DESC LIMIT 2`)
	if len(lim.Rows) != 2 || lim.Rows[0][0].Str != "d" || lim.Rows[1][0].Str != "c" {
		t.Fatalf("%+v", lim.Rows)
	}
	off := execOK(t, s, `SELECT n FROM t ORDER BY n DESC LIMIT 2 OFFSET 1`)
	if len(off.Rows) != 2 || off.Rows[0][0].Str != "c" || off.Rows[1][0].Str != "b" {
		t.Fatalf("offset %+v", off.Rows)
	}
	off2 := execOK(t, s, `SELECT n FROM t ORDER BY n DESC OFFSET 2`)
	if len(off2.Rows) != 2 || off2.Rows[0][0].Str != "b" || off2.Rows[1][0].Str != "a" {
		t.Fatalf("offset only %+v", off2.Rows)
	}
	none := execOK(t, s, `SELECT n FROM t ORDER BY n DESC LIMIT 2 OFFSET 10`)
	if len(none.Rows) != 0 {
		t.Fatalf("past end %+v", none.Rows)
	}
	ord := execOK(t, s, `SELECT n, q FROM t ORDER BY 2 DESC, 1`)
	if ord.Rows[0][0].Str != "d" {
		t.Fatalf("%+v", ord.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT n FROM t ORDER BY n LIMIT 2 OFFSET 1`)
	found := false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "TopNSort") && strings.Contains(row[0].Str, "3")
	}
	if !found {
		t.Fatalf("EXPLAIN lacks TopNSort fetch=3: %+v", plan.Rows)
	}
}

func TestOrderByNulls(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('b'), (NULL), ('a')`)
	res := execOK(t, s, `SELECT n FROM t ORDER BY n`)
	if len(res.Rows) != 3 || res.Rows[0][0].Str != "a" || !res.Rows[2][0].Null {
		t.Fatalf("asc %+v", res.Rows)
	}
	res = execOK(t, s, `SELECT n FROM t ORDER BY n DESC`)
	if !res.Rows[0][0].Null || res.Rows[1][0].Str != "b" {
		t.Fatalf("desc %+v", res.Rows)
	}
}

func TestSelectDistinct(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE distinct_rows (id STRING PRIMARY KEY, n STRING, q DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO distinct_rows (id, n, q) VALUES ('1', 'b', 2), ('2', 'a', 1), ('3', 'a', 1), ('4', NULL, 1), ('5', NULL, 1)`)

	got := execOK(t, s, `SELECT DISTINCT n FROM distinct_rows ORDER BY n`)
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" || !got.Rows[2][0].Null {
		t.Fatalf("distinct rows: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT DISTINCT n, q FROM distinct_rows ORDER BY n, q`)
	if len(got.Rows) != 3 {
		t.Fatalf("distinct tuples: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT DISTINCT n FROM distinct_rows ORDER BY n LIMIT 2`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("DISTINCT must run before LIMIT: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT DISTINCT * FROM distinct_rows ORDER BY id`)
	if len(got.Rows) != 5 {
		t.Fatalf("distinct star rows: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT DISTINCT COUNT(*) AS total FROM distinct_rows`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "5" {
		t.Fatalf("distinct aggregate: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT DISTINCT n FROM distinct_rows ORDER BY q`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("DISTINCT ORDER BY hidden expression must fail, got %v", err)
	}
	plan := execOK(t, s, `EXPLAIN SELECT DISTINCT n FROM distinct_rows`)
	found := false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "HashDistinct")
	}
	if !found {
		t.Fatalf("EXPLAIN lacks HashDistinct: %+v", plan.Rows)
	}
	plan = execOK(t, s, `EXPLAIN SELECT DISTINCT n, q FROM distinct_rows ORDER BY n, q`)
	found = false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "OrderedDistinct")
	}
	if !found {
		t.Fatalf("EXPLAIN lacks OrderedDistinct: %+v", plan.Rows)
	}
}

func TestIndexAssistedDistinct(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE distinct_keys (id STRING PRIMARY KEY, email STRING NOT NULL, nickname STRING)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_distinct_email ON distinct_keys (email)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_distinct_nickname ON distinct_keys (nickname)`)
	execOK(t, s, `INSERT INTO distinct_keys (id, email, nickname) VALUES ('1', 'a@x', NULL), ('2', 'b@x', 'bee')`)

	for _, tc := range []struct {
		sql, key string
	}{
		{`EXPLAIN SELECT DISTINCT id FROM distinct_keys`, "PRIMARY"},
		{`EXPLAIN SELECT DISTINCT email FROM distinct_keys`, "ux_distinct_email"},
	} {
		plan := execOK(t, s, tc.sql)
		found := false
		for _, row := range plan.Rows {
			found = found || strings.Contains(row[0].Str, "IndexDistinct "+tc.key)
		}
		if !found {
			t.Fatalf("%s lacks index distinct: %+v", tc.sql, plan.Rows)
		}
	}
	plan := execOK(t, s, `EXPLAIN SELECT DISTINCT nickname FROM distinct_keys`)
	for _, row := range plan.Rows {
		if strings.Contains(row[0].Str, "IndexDistinct") {
			t.Fatalf("nullable unique key must not elide DISTINCT: %+v", plan.Rows)
		}
	}
	got := execOK(t, s, `SELECT DISTINCT nickname FROM distinct_keys`)
	if len(got.Rows) != 2 {
		t.Fatalf("nullable DISTINCT result: %+v", got.Rows)
	}
}

func TestHaving(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE having_rows (id STRING PRIMARY KEY, category STRING NOT NULL, amount DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO having_rows (id, category, amount) VALUES ('1', 'a', 2), ('2', 'a', 3), ('3', 'b', 4)`)

	got := execOK(t, s, `SELECT category, COUNT(*) AS total FROM having_rows GROUP BY category HAVING total > 1 ORDER BY category`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" || got.Rows[0][1].Dec.String() != "2" {
		t.Fatalf("HAVING alias result: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT category, SUM(amount) AS total FROM having_rows GROUP BY category HAVING SUM(amount) >= 5`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("HAVING selected aggregate result: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT category, COUNT(*) AS total FROM having_rows GROUP BY category HAVING category = 'b'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b" {
		t.Fatalf("HAVING grouped output result: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT category FROM having_rows HAVING category = 'a'`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("HAVING without aggregation must fail, got %v", err)
	}
	if _, err := s.Exec(`SELECT category, COUNT(*) FROM having_rows GROUP BY category HAVING SUM(amount) > 1`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("unselected HAVING aggregate must fail, got %v", err)
	}
	plan := execOK(t, s, `EXPLAIN SELECT category, COUNT(*) AS total FROM having_rows GROUP BY category HAVING total > 1`)
	found := false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "Having")
	}
	if !found {
		t.Fatalf("EXPLAIN lacks Having: %+v", plan.Rows)
	}
}

func TestCaseExpressions(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE case_rows (id STRING PRIMARY KEY, state STRING, amount DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO case_rows (id, state, amount) VALUES ('1', 'paid', 12), ('2', 'new', 5), ('3', NULL, 1)`)

	got := execOK(t, s, `SELECT id, CASE WHEN amount >= 10 THEN 'large' WHEN amount >= 5 THEN 'medium' ELSE 'small' END AS band FROM case_rows ORDER BY id`)
	want := []string{"large", "medium", "small"}
	for i := range want {
		if got.Rows[i][1].Str != want[i] {
			t.Fatalf("searched CASE row %d: %+v", i, got.Rows)
		}
	}
	got = execOK(t, s, `SELECT id, CASE state WHEN 'paid' THEN 'done' WHEN 'new' THEN 'open' ELSE 'unknown' END AS label FROM case_rows ORDER BY id`)
	want = []string{"done", "open", "unknown"}
	for i := range want {
		if got.Rows[i][1].Str != want[i] {
			t.Fatalf("simple CASE row %d: %+v", i, got.Rows)
		}
	}
	got = execOK(t, s, `SELECT CASE WHEN state = 'missing' THEN 'x' END AS absent FROM case_rows WHERE id = '1'`)
	if len(got.Rows) != 1 || !got.Rows[0][0].Null {
		t.Fatalf("CASE implicit ELSE NULL: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT CASE WHEN state = 'paid' THEN CASE WHEN amount > 10 THEN 'nested' ELSE 'other' END ELSE 'no' END AS result FROM case_rows WHERE id = '1'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "nested" {
		t.Fatalf("nested CASE: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT CASE WHEN amount THEN 'bad' ELSE 'ok' END FROM case_rows`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("non-boolean searched CASE must fail, got %v", err)
	}
}

func TestStringBuiltinsLowerUpperLength(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE string_funcs (id STRING PRIMARY KEY, s STRING, body TEXT)`)
	execOK(t, s, `INSERT INTO string_funcs (id, s, body) VALUES ('1', 'MiXeD', 'héllo界'), ('2', NULL, NULL)`)

	got := execOK(t, s, `SELECT LOWER(s), UPPER(s), LENGTH(s), LENGTH(body) FROM string_funcs WHERE id = '1'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "mixed" || got.Rows[0][1].Str != "MIXED" || got.Rows[0][2].Dec.String() != "5" || got.Rows[0][3].Dec.String() != "6" {
		t.Fatalf("string functions: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT LOWER(s), UPPER(body), LENGTH(s) FROM string_funcs WHERE id = '2'`)
	if len(got.Rows) != 1 || !got.Rows[0][0].Null || !got.Rows[0][1].Null || !got.Rows[0][2].Null {
		t.Fatalf("string function NULL propagation: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT LOWER(1) FROM string_funcs`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("LOWER numeric argument must fail, got %v", err)
	}
	if _, err := s.Exec(`SELECT LENGTH(s, s) FROM string_funcs`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("LENGTH arity must fail, got %v", err)
	}
}

func TestStringBuiltinsRemaining(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE more_string_funcs (id STRING PRIMARY KEY, s STRING, body TEXT)`)
	execOK(t, s, `INSERT INTO more_string_funcs (id, s, body) VALUES ('1', '  héllo界  ', 'abcabc'), ('2', NULL, NULL)`)

	got := execOK(t, s, `SELECT SUBSTRING(s, 3, 6), TRIM(s), LTRIM(s), RTRIM(s), REPLACE(body, 'ab', 'X'), CONCAT('pre-', body) FROM more_string_funcs WHERE id = '1'`)
	row := got.Rows[0]
	if row[0].Str != "héllo界" || row[1].Str != "héllo界" || row[2].Str != "héllo界  " || row[3].Str != "  héllo界" || row[4].Str != "XcXc" || row[5].Str != "pre-abcabc" {
		t.Fatalf("remaining string functions: %+v", row)
	}
	got = execOK(t, s, `SELECT STARTS_WITH(body, 'abc'), ENDS_WITH(body, 'abc'), CONTAINS(body, 'bca'), SUBSTRING(body, 4) FROM more_string_funcs WHERE id = '1'`)
	if !got.Rows[0][0].Bool || !got.Rows[0][1].Bool || !got.Rows[0][2].Bool || got.Rows[0][3].Str != "abc" {
		t.Fatalf("string predicates/substring: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT SUBSTRING(s, 1), TRIM(s), REPLACE(s, 'x', 'y'), CONCAT(s, 'x'), CONTAINS(s, 'x') FROM more_string_funcs WHERE id = '2'`)
	for i, v := range got.Rows[0] {
		if !v.Null {
			t.Fatalf("NULL propagation column %d: %+v", i, got.Rows)
		}
	}
	for _, sql := range []string{
		`SELECT SUBSTRING(s, 0) FROM more_string_funcs`,
		`SELECT SUBSTRING(s, 1, -1) FROM more_string_funcs`,
		`SELECT REPLACE(s, 'x') FROM more_string_funcs`,
		`SELECT CONTAINS(s, 1) FROM more_string_funcs`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("invalid string call %q must fail, got %v", sql, err)
		}
	}
}

func TestNullAndValueBuiltins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE value_funcs (id STRING PRIMARY KEY, a STRING, b STRING, n DECIMAL(10,0))`)
	execOK(t, s, `INSERT INTO value_funcs (id, a, b, n) VALUES ('1', NULL, 'fallback', 3), ('2', 'same', 'same', 7), ('3', 'left', NULL, NULL)`)

	got := execOK(t, s, `SELECT id, COALESCE(a, b, 'none'), NULLIF(a, b), GREATEST(n, 5), LEAST(n, 5) FROM value_funcs ORDER BY id`)
	if got.Rows[0][1].Str != "fallback" || !got.Rows[0][2].Null || got.Rows[0][3].Dec.String() != "5" || got.Rows[0][4].Dec.String() != "3" {
		t.Fatalf("value functions row 1: %+v", got.Rows[0])
	}
	if got.Rows[1][1].Str != "same" || !got.Rows[1][2].Null || got.Rows[1][3].Dec.String() != "7" || got.Rows[1][4].Dec.String() != "5" {
		t.Fatalf("value functions row 2: %+v", got.Rows[1])
	}
	if got.Rows[2][1].Str != "left" || got.Rows[2][2].Str != "left" || !got.Rows[2][3].Null || !got.Rows[2][4].Null {
		t.Fatalf("value functions row 3: %+v", got.Rows[2])
	}
	got = execOK(t, s, `SELECT COALESCE('safe', 1 / 0) FROM value_funcs LIMIT 1`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "safe" {
		t.Fatalf("COALESCE lazy evaluation: %+v", got.Rows)
	}
	for _, sql := range []string{
		`SELECT COALESCE() FROM value_funcs`,
		`SELECT NULLIF(a) FROM value_funcs`,
		`SELECT GREATEST() FROM value_funcs`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("invalid value call %q must fail, got %v", sql, err)
		}
	}
}

func TestExactNumericBuiltins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE numeric_funcs (id STRING PRIMARY KEY, n DECIMAL(20,4))`)
	execOK(t, s, `INSERT INTO numeric_funcs (id, n) VALUES ('1', -12.3456), ('2', 12.5000), ('3', NULL)`)

	got := execOK(t, s, `SELECT ABS(n), ROUND(n), ROUND(n, 2), CEIL(n), FLOOR(n), MOD(n, 5) FROM numeric_funcs WHERE id = '1'`)
	row := got.Rows[0]
	want := []string{"12.3456", "-12", "-12.35", "-12", "-13", "-2.3456"}
	for i, expected := range want {
		if row[i].Dec.String() != expected {
			t.Fatalf("numeric function %d got %s want %s: %+v", i, row[i].Dec.String(), expected, row)
		}
	}
	got = execOK(t, s, `SELECT ROUND(n), CEIL(n), FLOOR(n), MOD(n, 5) FROM numeric_funcs WHERE id = '2'`)
	want = []string{"13", "13", "12", "2.5000"}
	for i, expected := range want {
		if got.Rows[0][i].Dec.String() != expected {
			t.Fatalf("positive numeric function %d got %s want %s", i, got.Rows[0][i].Dec.String(), expected)
		}
	}
	got = execOK(t, s, `SELECT ABS(n), ROUND(n), CEIL(n), FLOOR(n), MOD(n, 2) FROM numeric_funcs WHERE id = '3'`)
	for i, v := range got.Rows[0] {
		if !v.Null {
			t.Fatalf("numeric NULL propagation column %d: %+v", i, got.Rows)
		}
	}
	for _, sql := range []string{
		`SELECT ABS('x') FROM numeric_funcs`,
		`SELECT ROUND(n, -1) FROM numeric_funcs`,
		`SELECT MOD(n, 0) FROM numeric_funcs`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("invalid numeric call %q must fail, got %v", sql, err)
		}
	}
}

func TestPowerAndSqrtBuiltins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE approximate_numeric_funcs (id STRING PRIMARY KEY, n DECIMAL(20,4))`)
	execOK(t, s, `INSERT INTO approximate_numeric_funcs (id, n) VALUES ('1', 9), ('2', NULL)`)

	got := execOK(t, s, `SELECT POWER(n, 0.5), POWER(2, 3), SQRT(n), SQRT(2) FROM approximate_numeric_funcs WHERE id = '1'`)
	want := []string{"3.00000000", "8.00000000", "3.00000000", "1.41421356"}
	for i, expected := range want {
		if got.Rows[0][i].Dec.String() != expected {
			t.Fatalf("approximate numeric function %d got %s want %s", i, got.Rows[0][i].Dec.String(), expected)
		}
	}
	got = execOK(t, s, `SELECT POWER(n, 2), SQRT(n) FROM approximate_numeric_funcs WHERE id = '2'`)
	if !got.Rows[0][0].Null || !got.Rows[0][1].Null {
		t.Fatalf("POWER/SQRT NULL propagation: %+v", got.Rows)
	}
	for _, sql := range []string{
		`SELECT SQRT(-1) FROM approximate_numeric_funcs`,
		`SELECT POWER(-1, 0.5) FROM approximate_numeric_funcs`,
		`SELECT POWER(0, -1) FROM approximate_numeric_funcs`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("invalid approximate numeric call %q must fail, got %v", sql, err)
		}
	}
}

func TestJSONReadBuiltins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE json_funcs (id STRING PRIMARY KEY, doc JSON)`)
	execOK(t, s, `INSERT INTO json_funcs (id, doc) VALUES ('1', '{"name":"Ada","active":true,"items":[1,2,3],"meta":{"score":4.5},"none":null}'), ('2', NULL)`)

	got := execOK(t, s, `SELECT JSON_GET(doc, '$.name'), JSON_GET(doc, 'meta.score'), JSON_ARRAY_LENGTH(doc, 'items'), JSON_TYPE(doc), JSON_TYPE(doc, 'items'), JSON_TYPE(doc, 'none') FROM json_funcs WHERE id = '1'`)
	row := got.Rows[0]
	if row[0].Str != "Ada" || row[1].Dec.String() != "4.5" || row[2].Dec.String() != "3" || row[3].Str != "object" || row[4].Str != "array" || row[5].Str != "null" {
		t.Fatalf("JSON read functions: %+v", row)
	}
	got = execOK(t, s, `SELECT JSON_GET(doc, 'missing'), JSON_ARRAY_LENGTH(doc, 'missing'), JSON_TYPE(doc, 'missing') FROM json_funcs WHERE id = '1'`)
	for i, v := range got.Rows[0] {
		if !v.Null {
			t.Fatalf("missing JSON path column %d: %+v", i, got.Rows)
		}
	}
	got = execOK(t, s, `SELECT JSON_GET(doc, 'name'), JSON_ARRAY_LENGTH(doc), JSON_TYPE(doc) FROM json_funcs WHERE id = '2'`)
	for i, v := range got.Rows[0] {
		if !v.Null {
			t.Fatalf("SQL NULL JSON column %d: %+v", i, got.Rows)
		}
	}
	for _, sql := range []string{
		`SELECT JSON_GET(doc) FROM json_funcs`,
		`SELECT JSON_ARRAY_LENGTH(doc, 'name') FROM json_funcs WHERE id = '1'`,
		`SELECT JSON_TYPE('not-json') FROM json_funcs`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("invalid JSON call %q must fail, got %v", sql, err)
		}
	}
}

func TestJSONMutationAndContainsBuiltins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE json_mutation_funcs (id STRING PRIMARY KEY, doc JSON)`)
	execOK(t, s, `INSERT INTO json_mutation_funcs (id, doc) VALUES ('1', '{"name":"Ada","items":[1,2,3],"meta":{"score":4}}')`)

	got := execOK(t, s, `SELECT JSON_GET(JSON_SET(doc, 'meta.score', 10), 'meta.score'), JSON_GET(JSON_SET(doc, 'new_key', 'value'), 'new_key'), JSON_ARRAY_LENGTH(JSON_REMOVE(doc, 'items.1'), 'items') FROM json_mutation_funcs`)
	if got.Rows[0][0].Dec.String() != "10" || got.Rows[0][1].Str != "value" || got.Rows[0][2].Dec.String() != "2" {
		t.Fatalf("JSON mutations: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT JSON_CONTAINS(doc, '{"meta":{"score":4}}'), JSON_CONTAINS(doc, '{"items":[2]}'), JSON_CONTAINS(doc, '{"missing":1}') FROM json_mutation_funcs`)
	if !got.Rows[0][0].Bool || !got.Rows[0][1].Bool || got.Rows[0][2].Bool {
		t.Fatalf("JSON containment: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT JSON_SET(doc, 'items.9', 1) FROM json_mutation_funcs`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("out-of-range JSON_SET must fail, got %v", err)
	}
	if _, err := s.Exec(`SELECT JSON_CONTAINS(doc, 'not-json') FROM json_mutation_funcs`); err == nil {
		t.Fatal("invalid JSON_CONTAINS target must fail")
	}
}

func TestJSONGetConstantPathPreservesIndexSargability(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE json_sargable (id STRING PRIMARY KEY, doc JSON)`)
	execOK(t, s, `CREATE INDEX ix_json_sargable_kind ON json_sargable (doc.kind)`)
	execOK(t, s, `INSERT INTO json_sargable (id, doc) VALUES ('1', '{"kind":"book"}'), ('2', '{"kind":"music"}')`)

	got := execOK(t, s, `SELECT id FROM json_sargable WHERE JSON_GET(doc, '$.kind') = 'book'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" {
		t.Fatalf("JSON_GET indexed result: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT id FROM json_sargable WHERE JSON_GET(doc, '$.kind') = 'book'`)
	found := false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "IndexScan") && strings.Contains(row[0].Str, "ix_json_sargable_kind")
	}
	if !found {
		t.Fatalf("constant JSON_GET path did not use JSON-path index: %+v", plan.Rows)
	}
}

func TestDateTimeBuiltins(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE date_funcs (id STRING PRIMARY KEY, ts TIMESTAMPTZ)`)
	ts, err := types.ParseTimestamp("2024-02-29T13:14:15.987654321Z")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(context.Background(), `INSERT INTO date_funcs (id, ts) VALUES ('1', $1)`, []Param{{Value: ts}}); err != nil {
		t.Fatal(err)
	}
	execOK(t, s, `INSERT INTO date_funcs (id, ts) VALUES ('2', NULL)`)

	got := execOK(t, s, `SELECT EXTRACT('year', ts), EXTRACT('month', ts), EXTRACT('day', ts), DATE_TRUNC('day', ts), DATE_ADD(ts, 1, 'month'), DATE_DIFF(ts, DATE_ADD(ts, 90, 'second'), 'second') FROM date_funcs WHERE id = '1'`)
	row := got.Rows[0]
	if row[0].Dec.String() != "2024" || row[1].Dec.String() != "2" || row[2].Dec.String() != "29" || row[3].String() != "2024-02-29T00:00:00Z" || row[4].String() != "2024-03-29T13:14:15.987654321Z" || row[5].Dec.String() != "90" {
		t.Fatalf("date/time functions: %+v", row)
	}
	got = execOK(t, s, `SELECT EXTRACT('year', ts), DATE_TRUNC('hour', ts), DATE_ADD(ts, 1, 'day'), DATE_DIFF(ts, ts, 'day') FROM date_funcs WHERE id = '2'`)
	for i, v := range got.Rows[0] {
		if !v.Null {
			t.Fatalf("date/time NULL propagation column %d: %+v", i, got.Rows)
		}
	}
	for _, sql := range []string{
		`SELECT EXTRACT('week', ts) FROM date_funcs`,
		`SELECT DATE_TRUNC('day', 'not-a-time') FROM date_funcs`,
		`SELECT DATE_ADD(ts, 1.5, 'day') FROM date_funcs`,
		`SELECT DATE_DIFF(ts, ts, 'fortnight') FROM date_funcs`,
	} {
		if _, err := s.Exec(sql); !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("invalid date call %q must fail, got %v", sql, err)
		}
	}
}

func TestUnionAll(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE union_left (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE TABLE union_right (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO union_left (id, n) VALUES ('1', 'a'), ('2', 'dup')`)
	execOK(t, s, `INSERT INTO union_right (id, n) VALUES ('3', 'b'), ('4', 'dup')`)

	got := execOK(t, s, `SELECT n AS value FROM union_left ORDER BY id UNION ALL SELECT n FROM union_right ORDER BY id`)
	if len(got.Rows) != 4 || len(got.Columns) != 1 || got.Columns[0] != "value" {
		t.Fatalf("UNION ALL shape: %+v", got)
	}
	want := []string{"a", "dup", "b", "dup"}
	for i, expected := range want {
		if got.Rows[i][0].Str != expected {
			t.Fatalf("UNION ALL row %d: %+v", i, got.Rows)
		}
	}
	if _, err := s.Exec(`SELECT id, n FROM union_left UNION ALL SELECT id FROM union_right`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("UNION ALL column mismatch must fail, got %v", err)
	}
	plan := execOK(t, s, `EXPLAIN SELECT n FROM union_left UNION ALL SELECT n FROM union_right`)
	found := false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "UnionAll")
	}
	if !found {
		t.Fatalf("EXPLAIN lacks UnionAll: %+v", plan.Rows)
	}
}

func TestUnionDistinctNullAndDuplicates(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE union_distinct_left (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE TABLE union_distinct_right (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO union_distinct_left (id, n) VALUES ('1', 'a'), ('2', 'dup'), ('3', NULL)`)
	execOK(t, s, `INSERT INTO union_distinct_right (id, n) VALUES ('4', 'b'), ('5', 'dup'), ('6', NULL)`)

	got := execOK(t, s, `SELECT n FROM union_distinct_left UNION SELECT n FROM union_distinct_right`)
	if len(got.Rows) != 4 {
		t.Fatalf("UNION did not eliminate duplicates: %+v", got.Rows)
	}
	seen := map[string]int{}
	nulls := 0
	for _, row := range got.Rows {
		if row[0].Null {
			nulls++
		} else {
			seen[row[0].Str]++
		}
	}
	if nulls != 1 || seen["a"] != 1 || seen["b"] != 1 || seen["dup"] != 1 {
		t.Fatalf("UNION duplicate/NULL semantics: rows=%+v seen=%v nulls=%d", got.Rows, seen, nulls)
	}
	plan := execOK(t, s, `EXPLAIN SELECT n FROM union_distinct_left UNION SELECT n FROM union_distinct_right`)
	found := false
	for _, row := range plan.Rows {
		found = found || strings.Contains(row[0].Str, "Union") && !strings.Contains(row[0].Str, "UnionAll")
	}
	if !found {
		t.Fatalf("EXPLAIN lacks distinct Union: %+v", plan.Rows)
	}
}

func TestIntersectExceptNullAndDuplicates(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE set_left (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE TABLE set_right (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO set_left (id, n) VALUES ('1', 'a'), ('2', 'dup'), ('3', 'dup'), ('4', NULL)`)
	execOK(t, s, `INSERT INTO set_right (id, n) VALUES ('5', 'b'), ('6', 'dup'), ('7', NULL)`)

	got := execOK(t, s, `SELECT n FROM set_left INTERSECT SELECT n FROM set_right`)
	if len(got.Rows) != 2 {
		t.Fatalf("INTERSECT rows: %+v", got.Rows)
	}
	seenDup, seenNull := false, false
	for _, row := range got.Rows {
		seenDup = seenDup || row[0].Str == "dup"
		seenNull = seenNull || row[0].Null
	}
	if !seenDup || !seenNull {
		t.Fatalf("INTERSECT duplicate/NULL semantics: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT n FROM set_left EXCEPT SELECT n FROM set_right`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("EXCEPT duplicate/NULL semantics: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT n FROM set_left INTERSECT SELECT n FROM set_right`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[0][0].Str, "Intersect") {
		t.Fatalf("EXPLAIN lacks Intersect: %+v", plan.Rows)
	}
	plan = execOK(t, s, `EXPLAIN SELECT n FROM set_left EXCEPT SELECT n FROM set_right`)
	if len(plan.Rows) == 0 || !strings.Contains(plan.Rows[0][0].Str, "Except") {
		t.Fatalf("EXPLAIN lacks Except: %+v", plan.Rows)
	}
}

func TestSetOperationTypeCoercion(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE set_string (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `CREATE TABLE set_text (id STRING PRIMARY KEY, value TEXT)`)
	execOK(t, s, `INSERT INTO set_string (id, value) VALUES ('1', 'short')`)
	execOK(t, s, `INSERT INTO set_text (id, value) VALUES ('2', 'long')`)
	got := execOK(t, s, `SELECT value FROM set_string UNION ALL SELECT value FROM set_text`)
	if len(got.Rows) != 2 || got.Rows[0][0].Typ.Kind != types.KindText || got.Rows[1][0].Typ.Kind != types.KindText {
		t.Fatalf("STRING/TEXT common type: %+v", got.Rows)
	}

	execOK(t, s, `CREATE TABLE set_uuid (id STRING PRIMARY KEY, value UUID)`)
	execOK(t, s, `INSERT INTO set_uuid (id, value) VALUES ('3', '11111111-1111-1111-1111-111111111111')`)
	if _, err := s.Exec(`SELECT value FROM set_string UNION SELECT value FROM set_uuid`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("incompatible set-operation types must fail, got %v", err)
	}
}

func TestScalarSubquery(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE scalar_outer (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE scalar_inner (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `INSERT INTO scalar_outer (id) VALUES ('outer')`)
	execOK(t, s, `INSERT INTO scalar_inner (id, value) VALUES ('1', 'one'), ('2', 'two')`)

	got := execOK(t, s, `SELECT (SELECT value FROM scalar_inner WHERE id = '1') AS nested FROM scalar_outer`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "one" || got.Columns[0] != "nested" {
		t.Fatalf("scalar subquery result: %+v", got)
	}
	got = execOK(t, s, `SELECT (SELECT value FROM scalar_inner WHERE id = 'missing') FROM scalar_outer`)
	if len(got.Rows) != 1 || !got.Rows[0][0].Null {
		t.Fatalf("empty scalar subquery must yield NULL: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT (SELECT value FROM scalar_inner) FROM scalar_outer`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("multi-row scalar subquery must fail, got %v", err)
	}
	if _, err := s.Exec(`SELECT (SELECT id, value FROM scalar_inner) FROM scalar_outer`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("multi-column scalar subquery must fail, got %v", err)
	}
}

func TestInSubqueryNullSemantics(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE in_outer (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `CREATE TABLE in_inner (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `INSERT INTO in_outer (id, value) VALUES ('1', 'a'), ('2', 'b'), ('3', NULL)`)
	execOK(t, s, `INSERT INTO in_inner (id, value) VALUES ('4', 'a'), ('5', NULL)`)

	got := execOK(t, s, `SELECT id FROM in_outer WHERE value IN (SELECT value FROM in_inner) ORDER BY id`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" {
		t.Fatalf("IN result: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM in_outer WHERE value NOT IN (SELECT value FROM in_inner)`)
	if len(got.Rows) != 0 {
		t.Fatalf("NOT IN with RHS NULL must be unknown for non-matches: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM in_outer WHERE value NOT IN (SELECT value FROM in_inner WHERE id = 'missing') ORDER BY id`)
	if len(got.Rows) != 3 {
		t.Fatalf("NOT IN empty subquery must be true, including NULL LHS: %+v", got.Rows)
	}
	if _, err := s.Exec(`SELECT id FROM in_outer WHERE value IN (SELECT id, value FROM in_inner)`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("multi-column IN subquery must fail, got %v", err)
	}
}

func TestExistsSubquery(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE exists_outer (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE exists_inner (id STRING PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO exists_outer (id) VALUES ('1')`)
	execOK(t, s, `INSERT INTO exists_inner (id) VALUES ('present')`)

	got := execOK(t, s, `SELECT id FROM exists_outer WHERE EXISTS (SELECT id FROM exists_inner)`)
	if len(got.Rows) != 1 {
		t.Fatalf("EXISTS result: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM exists_outer WHERE NOT EXISTS (SELECT id FROM exists_inner WHERE id = 'missing')`)
	if len(got.Rows) != 1 {
		t.Fatalf("NOT EXISTS result: %+v", got.Rows)
	}
}

func TestConstantSubqueryEvaluatedOncePerOccurrence(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cache_outer (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE cache_inner (id STRING PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO cache_outer (id) VALUES ('1'), ('2')`)
	execOK(t, s, `INSERT INTO cache_inner (id) VALUES ('inner')`)

	got := execOK(t, s, `SELECT (SELECT UUID() FROM cache_inner) AS token FROM cache_outer ORDER BY id`)
	if len(got.Rows) != 2 || got.Rows[0][0].String() != got.Rows[1][0].String() {
		t.Fatalf("uncorrelated scalar subquery was not reused: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT (SELECT UUID() FROM cache_inner), (SELECT UUID() FROM cache_inner) FROM cache_outer LIMIT 1`)
	if got.Rows[0][0].String() == got.Rows[0][1].String() {
		t.Fatalf("distinct subquery occurrences shared a cached value: %+v", got.Rows)
	}
}

func TestDerivedTable(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE derived_source (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `INSERT INTO derived_source (id, value) VALUES ('1', 'b'), ('2', 'a'), ('3', 'a')`)

	got := execOK(t, s, `SELECT d.value FROM (SELECT value FROM derived_source WHERE id <> '1') AS d WHERE d.value = 'a' ORDER BY d.value`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "a" {
		t.Fatalf("derived table result: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT DISTINCT d.value FROM (SELECT value FROM derived_source) d ORDER BY d.value`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("derived DISTINCT result: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT d.value FROM (SELECT DISTINCT value FROM derived_source) d ORDER BY d.value`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("inner derived DISTINCT: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT d.value FROM (SELECT value FROM derived_source ORDER BY id) d`)
	if len(got.Rows) != 3 || len(got.Rows[0]) != 1 || got.Rows[0][0].Str != "b" || got.Rows[1][0].Str != "a" || got.Rows[2][0].Str != "a" {
		t.Fatalf("inner derived ORDER BY hidden: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT d.value FROM (SELECT DISTINCT value FROM derived_source ORDER BY value) d`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("inner derived ordered DISTINCT: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT d.value FROM (SELECT value FROM derived_source ORDER BY id LIMIT 1) d`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b" {
		t.Fatalf("inner derived LIMIT: %+v", got.Rows)
	}
}

func TestSubquerySemiAntiJoin(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE sj_outer (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, s, `CREATE TABLE sj_inner (id STRING PRIMARY KEY, owner STRING NOT NULL, value STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO sj_outer (id, value) VALUES ('a', 'one'), ('b', 'two'), ('c', 'three'), ('d', 'dup')`)
	execOK(t, s, `INSERT INTO sj_inner (id, owner, value) VALUES ('1', 'a', 'one'), ('2', 'a', 'one'), ('3', 'b', 'two'), ('4', 'e', 'gone')`)

	plan := execOK(t, s, `EXPLAIN SELECT o.id FROM sj_outer o WHERE EXISTS (SELECT i.id FROM sj_inner i WHERE i.owner = o.id)`)
	if !explainHas(plan, "HashSemiJoin") {
		t.Fatalf("EXISTS plan: %+v", plan.Rows)
	}
	got := execOK(t, s, `SELECT o.id FROM sj_outer o WHERE EXISTS (SELECT i.id FROM sj_inner i WHERE i.owner = o.id) ORDER BY o.id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("decorrelated EXISTS duplicates: %+v", got.Rows)
	}

	plan = execOK(t, s, `EXPLAIN SELECT o.id FROM sj_outer o WHERE NOT EXISTS (SELECT i.id FROM sj_inner i WHERE i.owner = o.id)`)
	if !explainHas(plan, "HashAntiJoin") {
		t.Fatalf("NOT EXISTS plan: %+v", plan.Rows)
	}
	got = execOK(t, s, `SELECT o.id FROM sj_outer o WHERE NOT EXISTS (SELECT i.id FROM sj_inner i WHERE i.owner = o.id) ORDER BY o.id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "c" || got.Rows[1][0].Str != "d" {
		t.Fatalf("decorrelated NOT EXISTS: %+v", got.Rows)
	}

	plan = execOK(t, s, `EXPLAIN SELECT o.id FROM sj_outer o WHERE o.value IN (SELECT i.value FROM sj_inner i)`)
	if !explainHas(plan, "HashSemiJoin") {
		t.Fatalf("IN plan: %+v", plan.Rows)
	}
	got = execOK(t, s, `SELECT o.id FROM sj_outer o WHERE o.value IN (SELECT i.value FROM sj_inner i) ORDER BY o.id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("IN semi-join: %+v", got.Rows)
	}

	plan = execOK(t, s, `EXPLAIN SELECT o.id FROM sj_outer o WHERE o.value NOT IN (SELECT i.value FROM sj_inner i)`)
	if !explainHas(plan, "HashAntiJoin") {
		t.Fatalf("NOT IN plan: %+v", plan.Rows)
	}
	got = execOK(t, s, `SELECT o.id FROM sj_outer o WHERE o.value NOT IN (SELECT i.value FROM sj_inner i) ORDER BY o.id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "c" || got.Rows[1][0].Str != "d" {
		t.Fatalf("NOT IN anti-join: %+v", got.Rows)
	}

	got = execOK(t, s, `SELECT o.id FROM sj_outer o WHERE EXISTS (SELECT i.id FROM sj_inner i WHERE i.owner = o.id AND i.value = 'two') ORDER BY o.id`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b" {
		t.Fatalf("EXISTS residual inner predicate: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM sj_outer WHERE EXISTS (SELECT id FROM sj_inner WHERE id = 'missing')`)
	if len(got.Rows) != 0 {
		t.Fatalf("uncorrelated empty EXISTS: %+v", got.Rows)
	}
}

func TestSubqueryAdversarialNulls(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE n_outer (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `CREATE TABLE n_inner (id STRING PRIMARY KEY, value STRING)`)
	execOK(t, s, `INSERT INTO n_outer (id, value) VALUES ('1', 'a'), ('2', 'b'), ('3', NULL)`)
	execOK(t, s, `INSERT INTO n_inner (id, value) VALUES ('4', 'a'), ('5', NULL), ('6', 'a')`)

	got := execOK(t, s, `SELECT id FROM n_outer WHERE value IN (SELECT value FROM n_inner) ORDER BY id`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" {
		t.Fatalf("IN with RHS NULL still matches: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT id FROM n_outer WHERE value NOT IN (SELECT value FROM n_inner)`)
	if len(got.Rows) != 0 {
		t.Fatalf("nullable NOT IN must stay unknown: %+v", got.Rows)
	}
	plan := execOK(t, s, `EXPLAIN SELECT id FROM n_outer WHERE value NOT IN (SELECT value FROM n_inner)`)
	if explainHas(plan, "HashAntiJoin") {
		t.Fatalf("nullable NOT IN must not become anti-join: %+v", plan.Rows)
	}
	got = execOK(t, s, `SELECT id FROM n_outer WHERE EXISTS (SELECT id FROM n_inner WHERE n_inner.value = n_outer.value) ORDER BY id`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "1" {
		t.Fatalf("NULL-eq EXISTS: %+v", got.Rows)
	}
}

func TestCorrelatedSubqueries(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE corr_outer (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE corr_inner (id STRING PRIMARY KEY, owner STRING, value STRING)`)
	execOK(t, s, `INSERT INTO corr_outer (id) VALUES ('a'), ('b'), ('c')`)
	execOK(t, s, `INSERT INTO corr_inner (id, owner, value) VALUES ('1', 'a', 'one'), ('2', 'b', 'two')`)

	got := execOK(t, s, `SELECT o.id, (SELECT i.value FROM corr_inner i WHERE i.owner = o.id) AS nested FROM corr_outer o ORDER BY o.id`)
	if len(got.Rows) != 3 || got.Rows[0][1].Str != "one" || got.Rows[1][1].Str != "two" || !got.Rows[2][1].Null {
		t.Fatalf("correlated scalar results: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT o.id FROM corr_outer o WHERE EXISTS (SELECT i.id FROM corr_inner i WHERE i.owner = o.id) ORDER BY o.id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("correlated EXISTS results: %+v", got.Rows)
	}
	got = execOK(t, s, `SELECT o.id FROM corr_outer o WHERE o.id IN (SELECT i.owner FROM corr_inner i WHERE i.owner = o.id) ORDER BY o.id`)
	if len(got.Rows) != 2 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" {
		t.Fatalf("correlated IN results: %+v", got.Rows)
	}
}

func TestDropTableAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('keep')`)
	execOK(t, s, `DROP TABLE t`)
	if _, err := s.Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("dropped table still visible: %v", err)
	}
	execOK(t, s, `DROP TABLE IF EXISTS t`)
	if _, err := s.Exec(`DROP TABLE missing`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("missing drop: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Session().Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("after restart: %v", err)
	}
}

func TestDropTableRollback(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('keep')`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `DROP TABLE t`)
	if _, err := s.Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("in txn: %v", err)
	}
	execOK(t, s, `ROLLBACK`)
	got := execOK(t, s, `SELECT n FROM t`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "keep" {
		t.Fatalf("%+v", got.Rows)
	}
}

func TestDropTableReferencedFK(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE p (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE c (id STRING PRIMARY KEY, p_id STRING REFERENCES p (id))`)
	if _, err := s.Exec(`DROP TABLE p`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("parent drop: %v", err)
	}
	execOK(t, s, `DROP TABLE c`)
	execOK(t, s, `DROP TABLE p`)
}

func TestDropIndexKindsRollbackAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (id STRING PRIMARY KEY, name STRING, body TEXT, metadata JSON, loc POINT, emb VECTOR<F32,3>)`)
	execOK(t, s, `CREATE INDEX ix_name ON docs (name)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_name ON docs (name)`)
	execOK(t, s, `CREATE INDEX ix_json ON docs (metadata.kind)`)
	execOK(t, s, `CREATE SPATIAL INDEX ix_loc ON docs (loc)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON docs (body)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON docs (emb) USING HNSW`)

	execOK(t, s, `BEGIN`)
	execOK(t, s, `DROP INDEX ix_name`)
	if _, err := s.Exec(`CREATE INDEX ix_name ON docs (name)`); err != nil {
		t.Fatalf("dropped index not transactionally hidden: %v", err)
	}
	execOK(t, s, `ROLLBACK`)
	if _, err := s.Exec(`CREATE INDEX ix_name ON docs (name)`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("rollback did not restore index: %v", err)
	}

	for _, name := range []string{"ix_name", "ux_name", "ix_json", "ix_loc", "ix_body", "ix_emb"} {
		execOK(t, s, `DROP INDEX `+name)
	}
	execOK(t, s, `DROP INDEX IF EXISTS missing`)
	if _, err := s.Exec(`DROP INDEX missing`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("missing drop: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tab, ok := db.Cat.Get("docs")
	if !ok || len(tab.Indexes) != 0 {
		t.Fatalf("indexes after restart: %+v", tab)
	}
}

func TestDropIndexAmbiguousAndForeignKeyDependency(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE a (id STRING PRIMARY KEY, email STRING)`)
	execOK(t, s, `CREATE TABLE b (id STRING PRIMARY KEY, email STRING)`)
	execOK(t, s, `CREATE INDEX ix_email ON a (email)`)
	execOK(t, s, `CREATE INDEX ix_email ON b (email)`)
	if _, err := s.Exec(`DROP INDEX ix_email`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("ambiguous drop: %v", err)
	}

	execOK(t, s, `CREATE UNIQUE INDEX ux_email ON a (email)`)
	execOK(t, s, `CREATE TABLE child (id STRING PRIMARY KEY, email STRING REFERENCES a (email))`)
	if _, err := s.Exec(`DROP INDEX ux_email`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("foreign-key index drop: %v", err)
	}
	execOK(t, s, `CREATE UNIQUE INDEX ux_email_2 ON a (email)`)
	execOK(t, s, `DROP INDEX ux_email`)
}

func TestDropIndexCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `DROP INDEX ix_n`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	if _, err := s.Exec(`CREATE INDEX ix_n ON t (n)`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("uncommitted drop survived crash: %v", err)
	}
	execOK(t, s, `DROP INDEX ix_n`)
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Session().Exec(`CREATE INDEX ix_n ON t (n)`); err != nil {
		t.Fatalf("committed drop lost after crash: %v", err)
	}
}

type denyWriteGate struct{}

func (denyWriteGate) AllowWrite() error {
	return nerr.New(nerr.Unavailable, "test.gate", "not the leader")
}

func TestDropIndexLeaderGate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	db.SetGate(denyWriteGate{})
	if _, err := s.Exec(`DROP INDEX ix_n`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("follower drop: %v", err)
	}
	db.SetGate(nil)
	if _, err := s.Exec(`CREATE INDEX ix_n ON t (n)`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("rejected drop changed catalog: %v", err)
	}
}

func TestDropIndexRBACAndAudit(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	execOK(t, admin, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, admin, `CREATE INDEX ix_n ON t (n)`)
	execOK(t, admin, `CREATE USER app IDENTIFIED BY 'pw'`)

	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	audit, err := security.OpenAudit(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetAudit(audit)
	if _, err := app.Exec(`DROP INDEX ix_n`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted drop: %v", err)
	}
	execOK(t, admin, `GRANT INDEX ON TABLE t TO app`)
	execOK(t, app, `DROP INDEX ix_n`)
	if err := audit.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("audit lines=%d: %s", len(lines), raw)
	}
	for i, want := range []string{"failure", "success"} {
		var ev security.Event
		if err := json.Unmarshal([]byte(lines[i]), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.Action != security.ActionDDL || ev.Object != "t" || ev.Outcome != want {
			t.Fatalf("event %d: %+v", i, ev)
		}
	}
}

func TestDropIndexCommitCrashDoesNotPartiallyApply(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	db.Eng.SetCrash(wal.PointBeforeCommitRecord)
	if _, err := s.Exec(`DROP INDEX ix_n`); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	db.Eng.Kill()
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Session().Exec(`CREATE INDEX ix_n ON t (n)`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("partial drop after commit crash: %v", err)
	}
}

func TestRebuildIndexKindsRollbackAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE docs (id STRING PRIMARY KEY, name STRING, body TEXT, metadata JSON, loc POINT, emb VECTOR<F32,3>)`)
	execOK(t, s, `INSERT INTO docs (id, name, body, metadata, loc, emb) VALUES ('1', 'one', 'hello world', '{"kind":"a"}', POINT(1,2), (1,0,0))`)
	execOK(t, s, `CREATE INDEX ix_name ON docs (name)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_name ON docs (name)`)
	execOK(t, s, `CREATE INDEX ix_json ON docs (metadata.kind)`)
	execOK(t, s, `CREATE SPATIAL INDEX ix_loc ON docs (loc)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON docs (body)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON docs (emb) USING HNSW`)

	before := indexesByName(t, db, "docs")
	for name, old := range before {
		execOK(t, s, `REBUILD INDEX `+name)
		neu := indexesByName(t, db, "docs")[name]
		if neu.Meta == old.Meta || neu.Meta == 0 {
			t.Fatalf("%s meta did not change: %d", name, neu.Meta)
		}
		old.Meta, neu.Meta = 0, 0
		if !reflect.DeepEqual(old, neu) {
			t.Fatalf("%s options changed: old=%+v new=%+v", name, old, neu)
		}
	}

	committed := indexesByName(t, db, "docs")["ix_name"].Meta
	execOK(t, s, `BEGIN`)
	execOK(t, s, `REBUILD INDEX ix_name`)
	inside, _ := s.lookup("docs")
	if got := indexMeta(inside, "ix_name"); got == committed {
		t.Fatal("rebuild was not visible inside transaction")
	}
	execOK(t, s, `ROLLBACK`)
	if got := indexesByName(t, db, "docs")["ix_name"].Meta; got != committed {
		t.Fatalf("rollback meta=%d want=%d", got, committed)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := indexesByName(t, db, "docs")["ix_name"].Meta; got != committed {
		t.Fatalf("restart meta=%d want=%d", got, committed)
	}
}

func TestRebuildIndexCrashKeepsOldIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	old := indexesByName(t, db, "t")["ix_n"].Meta
	db.Eng.SetCrash(wal.PointDuringIndexBuild)
	if _, err := s.Exec(`REBUILD INDEX ix_n`); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	snap := db.Metrics().Snapshot()
	if snap.IndexRebuilds != 1 || snap.IndexRebuildFailures != 1 {
		t.Fatalf("failure metrics: %+v", snap)
	}
	db.Eng.Kill()
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if got := indexesByName(t, db, "t")["ix_n"].Meta; got != old {
		t.Fatalf("crash meta=%d want old=%d", got, old)
	}
}

func TestRebuildIndexProgressAndMetrics(t *testing.T) {
	db := testDB(t)
	p := db.beginIndexRebuild("docs", "ix_body")
	p.add(7, 9)
	got := db.IndexRebuildProgress()
	if len(got) != 1 || got[0].Table != "docs" || got[0].Index != "ix_body" || got[0].Phase != "building" || got[0].Rows != 7 || got[0].Entries != 9 || got[0].Started.IsZero() {
		t.Fatalf("progress: %+v", got)
	}
	db.finishIndexRebuild(p)
	if got := db.IndexRebuildProgress(); len(got) != 0 {
		t.Fatalf("finished progress: %+v", got)
	}

	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES ('1', 'a'), ('2', 'b'), ('3', NULL)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	before := db.Metrics().Snapshot()
	execOK(t, s, `REBUILD INDEX ix_n`)
	after := db.Metrics().Snapshot()
	if after.IndexRebuilds-before.IndexRebuilds != 1 || after.IndexRebuildFailures != before.IndexRebuildFailures || after.IndexRebuildRows-before.IndexRebuildRows != 3 || after.IndexRebuildEntries-before.IndexRebuildEntries != 3 || after.IndexRebuildDuration <= before.IndexRebuildDuration {
		t.Fatalf("success metrics before=%+v after=%+v", before, after)
	}
}

func TestDropIndexReclaimsAndReusesPages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	var insert strings.Builder
	insert.WriteString(`INSERT INTO t (id, n) VALUES `)
	for i := 0; i < 1200; i++ {
		if i > 0 {
			insert.WriteByte(',')
		}
		fmt.Fprintf(&insert, `('%06d', 'value-%06d')`, i, i)
	}
	execOK(t, s, insert.String())
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	tab, _ := db.Cat.Get("t")
	idxTree, err := s.indexOf(tab, tab.Indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	owned, err := idxTree.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	freeBefore := db.Eng.Alloc.FreeCount()
	execOK(t, s, `DROP INDEX ix_n`)
	if err := db.LastReclaimError(); err != nil {
		t.Fatal(err)
	}
	if got := db.Eng.Alloc.FreeCount() - freeBefore; got != len(owned) {
		t.Fatalf("freed=%d want=%d", got, len(owned))
	}
	// Reopen before allocating the replacement: reuse must come from the
	// durable freelist, not merely the current process's allocator state.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s = db.Session()
	execOK(t, s, `CREATE INDEX ix_n2 ON t (n)`)
	tab, _ = db.Cat.Get("t")
	newTree, err := s.indexOf(tab, tab.Indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	newPages, err := newTree.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	reused := false
	for _, id := range newPages {
		reused = reused || slices.Contains(owned, id)
	}
	if !reused {
		t.Fatalf("new index did not reuse reclaimed pages: old=%v new=%v", owned, newPages)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Session().indexUsable("t", "ix_n2"); err != nil {
		t.Fatal(err)
	}
}

func TestCrashDuringPageReclamationReplaysDurableIntent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE reclaim_crash (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	var insert strings.Builder
	insert.WriteString(`INSERT INTO reclaim_crash (id, n) VALUES `)
	for i := 0; i < 300; i++ {
		if i > 0 {
			insert.WriteByte(',')
		}
		fmt.Fprintf(&insert, `('%06d', 'value-%06d')`, i, i)
	}
	execOK(t, s, insert.String())
	execOK(t, s, `CREATE INDEX ix_reclaim_crash ON reclaim_crash (n)`)
	tab, _ := db.Cat.Get("reclaim_crash")
	oldTree, err := s.indexOf(tab, tab.Indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	owned, err := oldTree.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetCrash(wal.PointDuringPageReclaim)
	execOK(t, s, `DROP INDEX ix_reclaim_crash`)
	if !wal.IsCrash(db.LastReclaimError()) {
		t.Fatalf("reclaim crash not recorded: %v", db.LastReclaimError())
	}
	if _, err := os.Stat(path + ".reclaim"); err != nil {
		t.Fatalf("durable reclaim intent missing: %v", err)
	}
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(path + ".reclaim"); !os.IsNotExist(err) {
		t.Fatalf("replayed reclaim intent remains: %v", err)
	}
	free := db.Eng.Alloc.State().Free
	for _, id := range owned {
		if !slices.Contains(free, id) {
			t.Fatalf("intent page %d was not reclaimed after restart", id)
		}
	}
	s = db.Session()
	execOK(t, s, `CREATE INDEX ix_reclaim_reused ON reclaim_crash (n)`)
	tab, _ = db.Cat.Get("reclaim_crash")
	newTree, err := s.indexOf(tab, tab.Indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	newPages, err := newTree.OwnedPages()
	if err != nil {
		t.Fatal(err)
	}
	reused := false
	for _, id := range newPages {
		reused = reused || slices.Contains(owned, id)
	}
	if !reused {
		t.Fatalf("replayed pages not reused: old=%v new=%v", owned, newPages)
	}
}

func TestCrashAfterPageReclamationReplayIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE reclaim_done (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_reclaim_done ON reclaim_done (n)`)
	tab, _ := db.Cat.Get("reclaim_done")
	oldTree, _ := s.indexOf(tab, tab.Indexes[0])
	owned, _ := oldTree.OwnedPages()
	db.Eng.SetCrash(wal.PointAfterPageReclaimBeforeIntentClear)
	execOK(t, s, `DROP INDEX ix_reclaim_done`)
	if !wal.IsCrash(db.LastReclaimError()) {
		t.Fatalf("post-reclaim crash not recorded: %v", db.LastReclaimError())
	}
	for _, id := range owned {
		if !slices.Contains(db.Eng.Alloc.State().Free, id) {
			t.Fatalf("page %d was not durably freed before crash", id)
		}
	}
	db.Eng.Kill()
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := os.Stat(path + ".reclaim"); !os.IsNotExist(err) {
		t.Fatalf("idempotent replay did not clear intent: %v", err)
	}
	for _, id := range owned {
		if !slices.Contains(db.Eng.Alloc.State().Free, id) {
			t.Fatalf("already-free page %d lost during replay", id)
		}
	}
}

func TestTamperedReclaimIntentFailsClosed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE reclaim_tamper (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_reclaim_tamper ON reclaim_tamper (n)`)
	db.Eng.SetCrash(wal.PointDuringPageReclaim)
	execOK(t, s, `DROP INDEX ix_reclaim_tamper`)
	if !wal.IsCrash(db.LastReclaimError()) {
		t.Fatalf("reclaim crash not recorded: %v", db.LastReclaimError())
	}
	db.Eng.Kill()

	intentPath := path + ".reclaim"
	raw, err := os.ReadFile(intentPath)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)-1] ^= 0xff
	if err := os.WriteFile(intentPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path, keys, 32); err == nil || !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("tampered reclaim intent must fail closed with crypto error, got %v", err)
	}
}

func TestDropTableReclaimsAfterSnapshotsDrain(t *testing.T) {
	db := testDB(t)
	setup := db.Session()
	execOK(t, setup, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, setup, `INSERT INTO t (id, n) VALUES ('1', 'a'), ('2', 'b')`)
	execOK(t, setup, `CREATE INDEX ix_n ON t (n)`)
	tab, _ := db.Cat.Get("t")
	heap, err := setup.heapOf(tab)
	if err != nil {
		t.Fatal(err)
	}
	idx, err := setup.indexOf(tab, tab.Indexes[0])
	if err != nil {
		t.Fatal(err)
	}
	heapPages, _ := heap.OwnedPages()
	idxPages, _ := idx.OwnedPages()
	want := len(heapPages) + len(idxPages)
	freeBefore := db.Eng.Alloc.FreeCount()

	reader := db.Session()
	execOK(t, reader, `BEGIN SNAPSHOT`)
	execOK(t, reader, `SELECT * FROM t`)
	done := make(chan error, 1)
	var returned atomic.Bool
	go func() {
		_, err := db.Session().Exec(`DROP TABLE t`)
		returned.Store(true)
		done <- err
	}()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok := db.Cat.Get("t"); !ok {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if returned.Load() {
		t.Fatal("DROP returned before the old snapshot drained")
	}
	execOK(t, reader, `ROLLBACK`)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if err := db.LastReclaimError(); err != nil {
		t.Fatal(err)
	}
	if got := db.Eng.Alloc.FreeCount() - freeBefore; got != want {
		t.Fatalf("freed=%d want=%d", got, want)
	}
}

func TestDropIndexRollbackDoesNotReclaim(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, n STRING)`)
	execOK(t, s, `CREATE INDEX ix_n ON t (n)`)
	before := db.Eng.Alloc.FreeCount()
	execOK(t, s, `BEGIN`)
	execOK(t, s, `DROP INDEX ix_n`)
	execOK(t, s, `ROLLBACK`)
	if got := db.Eng.Alloc.FreeCount(); got != before {
		t.Fatalf("rollback reclaimed pages: %d -> %d", before, got)
	}
	if _, err := s.Exec(`CREATE INDEX ix_n ON t (n)`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("index missing after rollback: %v", err)
	}
}

func (s *Session) indexUsable(table, index string) error {
	tab, ok := s.db.Cat.Get(table)
	if !ok {
		return nerr.New(nerr.NotFound, "test", "table missing")
	}
	for _, idx := range tab.Indexes {
		if idx.Name == index {
			tr, err := s.indexOf(tab, idx)
			if err != nil {
				return err
			}
			_, err = tr.OwnedPages()
			return err
		}
	}
	return nerr.New(nerr.NotFound, "test", "index missing")
}

func indexesByName(t *testing.T, db *DB, table string) map[string]catalog.Index {
	t.Helper()
	tab, ok := db.Cat.Get(table)
	if !ok {
		t.Fatalf("missing table %s", table)
	}
	out := make(map[string]catalog.Index, len(tab.Indexes))
	for _, idx := range tab.Indexes {
		out[idx.Name] = idx
	}
	return out
}

func indexMeta(tab *catalog.Table, name string) format.PageID {
	for _, idx := range tab.Indexes {
		if idx.Name == name {
			return idx.Meta
		}
	}
	return 0
}

func TestAlterTableAddDropRename(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE items (id STRING PRIMARY KEY, n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO items (id, n) VALUES ('1', 'alpha')`)
	execOK(t, s, `ALTER TABLE items ADD note STRING`)
	got := execOK(t, s, `SELECT id, n, note FROM items`)
	if len(got.Rows) != 1 || !got.Rows[0][2].Null {
		t.Fatalf("add %+v", got.Rows)
	}
	execOK(t, s, `UPDATE items SET note = 'x' WHERE id = '1'`)
	execOK(t, s, `ALTER TABLE items ADD extra STRING NOT NULL DEFAULT 'z'`)
	got = execOK(t, s, `SELECT extra FROM items`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "z" {
		t.Fatalf("default %+v", got.Rows)
	}
	execOK(t, s, `ALTER TABLE items RENAME COLUMN n TO title`)
	got = execOK(t, s, `SELECT title FROM items`)
	if got.Rows[0][0].Str != "alpha" {
		t.Fatalf("rename col %+v", got.Rows)
	}
	execOK(t, s, `ALTER TABLE items DROP COLUMN extra`)
	if _, err := s.Exec(`SELECT extra FROM items`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("dropped col: %v", err)
	}
	execOK(t, s, `ALTER TABLE items RENAME TO products`)
	got = execOK(t, s, `SELECT title FROM products`)
	if len(got.Rows) != 1 {
		t.Fatalf("rename table %+v", got.Rows)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	got = execOK(t, db.Session(), `SELECT title, note FROM products ORDER BY title`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "alpha" || got.Rows[0][1].Str != "x" {
		t.Fatalf("restart %+v", got.Rows)
	}
}

func TestAlterTableAddConstraint(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE customers (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, customer_id STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO customers (id) VALUES ('c1')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o1', 'c1')`)
	execOK(t, s, `ALTER TABLE orders ADD CONSTRAINT fk_orders_customer FOREIGN KEY (customer_id) REFERENCES customers (id)`)
	if _, err := s.Exec(`INSERT INTO orders (id, customer_id) VALUES ('o2', 'missing')`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("missing parent: %v", err)
	}
	execOK(t, s, `ALTER TABLE orders DROP CONSTRAINT fk_orders_customer`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o2', 'missing')`)
}

func TestCreateDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main")
	keys := testKeys(t)
	db, err := Create(path, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE DATABASE app`)
	sib := filepath.Join(dir, "app")
	st, err := os.Stat(sib)
	if err != nil || st.IsDir() {
		t.Fatalf("sibling %v %v", st, err)
	}
	other, err := Open(sib, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	execOK(t, other.Session(), `CREATE TABLE t (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE DATABASE IF NOT EXISTS app`)
	if _, err := s.Exec(`CREATE DATABASE app`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("dup: %v", err)
	}
	execOK(t, s, `BEGIN`)
	if _, err := s.Exec(`CREATE DATABASE other`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("in txn: %v", err)
	}
	execOK(t, s, `ROLLBACK`)
}

func TestExplainOrderBy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	res := execOK(t, s, `EXPLAIN SELECT n FROM t ORDER BY n DESC LIMIT 5`)
	found := false
	for _, row := range res.Rows {
		if len(row) > 0 && strings.Contains(row[0].Str, "Sort") {
			found = true
		}
	}
	if !found {
		t.Fatalf("explain rows %+v", res.Rows)
	}
}
