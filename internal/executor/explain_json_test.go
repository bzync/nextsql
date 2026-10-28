package executor

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestExplainFormatJSON(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	mustExec(t, sess, "CREATE TABLE users (id INT64 PRIMARY KEY, name STRING, score INT64)")
	mustExec(t, sess, "INSERT INTO users VALUES (1, 'alice', 100), (2, 'bob', 200), (3, 'carol', 300)")

	// 1. Basic EXPLAIN (FORMAT JSON) without ANALYZE
	res, err := sess.Exec("EXPLAIN (FORMAT JSON) SELECT * FROM users WHERE score > 50")
	if err != nil {
		t.Fatalf("EXPLAIN (FORMAT JSON): %v", err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "plan" {
		t.Fatalf("expected 1 column 'plan', got %v", res.Columns)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	jsonStr := res.Rows[0][0].Str
	var doc map[string]interface{}
	if err := json.Unmarshal([]byte(jsonStr), &doc); err != nil {
		t.Fatalf("failed to parse JSON plan: %v\nJSON:\n%s", err, jsonStr)
	}
	if op, ok := doc["operator"].(string); !ok || op == "" {
		t.Fatalf("missing or empty operator in JSON: %v", doc)
	}
	if _, ok := doc["estimates"]; !ok {
		t.Fatalf("missing estimates in JSON: %v", doc)
	}
	if _, ok := doc["actuals"]; ok {
		t.Fatalf("did not expect actuals without ANALYZE: %v", doc)
	}

	// 2. EXPLAIN (ANALYZE, FORMAT JSON)
	res, err = sess.Exec("EXPLAIN (ANALYZE, FORMAT JSON) SELECT * FROM users WHERE score > 50")
	if err != nil {
		t.Fatalf("EXPLAIN (ANALYZE, FORMAT JSON): %v", err)
	}
	if len(res.Columns) != 1 || res.Columns[0] != "plan" {
		t.Fatalf("expected 1 column 'plan', got %v", res.Columns)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(res.Rows))
	}
	jsonStr = res.Rows[0][0].Str
	doc = nil
	if err := json.Unmarshal([]byte(jsonStr), &doc); err != nil {
		t.Fatalf("failed to parse ANALYZE JSON plan: %v\nJSON:\n%s", err, jsonStr)
	}
	actuals, ok := doc["actuals"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected actuals in ANALYZE JSON: %v", doc)
	}
	if _, ok := actuals["rows"]; !ok {
		t.Fatalf("expected rows in actuals: %v", actuals)
	}
	if _, ok := actuals["time_ns"]; !ok {
		t.Fatalf("expected time_ns in actuals: %v", actuals)
	}
	resources, ok := doc["resources"].(map[string]interface{})
	if !ok {
		t.Fatalf("expected resources in ANALYZE JSON: %v", doc)
	}
	if workers, ok := resources["workers"].(float64); !ok || workers < 1 {
		t.Fatalf("expected workers >= 1 in resources: %v", resources)
	}

	// 3. Alternate syntaxes
	variants := []string{
		"EXPLAIN (FORMAT JSON, ANALYZE) SELECT name FROM users",
		"EXPLAIN ANALYZE (FORMAT JSON) SELECT name FROM users",
		"EXPLAIN FORMAT JSON SELECT name FROM users",
	}
	for _, sql := range variants {
		res, err := sess.Exec(sql)
		if err != nil {
			t.Fatalf("variant %q failed: %v", sql, err)
		}
		if len(res.Columns) != 1 || res.Columns[0] != "plan" {
			t.Fatalf("variant %q expected column 'plan', got %v", sql, res.Columns)
		}
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(res.Rows[0][0].Str), &parsed); err != nil {
			t.Fatalf("variant %q output invalid JSON: %v", sql, err)
		}
	}

	// 4. Default / EXPLAIN (FORMAT TEXT) returns tabular 11 columns
	resText, err := sess.Exec("EXPLAIN (FORMAT TEXT) SELECT * FROM users")
	if err != nil {
		t.Fatalf("EXPLAIN (FORMAT TEXT): %v", err)
	}
	if len(resText.Columns) != 11 {
		t.Fatalf("expected 11 columns for TEXT explain, got %d", len(resText.Columns))
	}

	// 5. Invalid format fails closed
	_, err = sess.Exec("EXPLAIN (FORMAT XML) SELECT * FROM users")
	if err == nil || !strings.Contains(err.Error(), "expected JSON or TEXT") {
		t.Fatalf("expected error for XML format, got: %v", err)
	}
}
