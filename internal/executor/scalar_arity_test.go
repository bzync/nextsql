package executor

import (
	"fmt"
	"testing"

	nsjson "github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestNumericFunctionArity(t *testing.T) {
	d1 := decimalIntValue(4)
	d2 := decimalIntValue(2)
	for _, tc := range []struct {
		name string
		args []types.Value
	}{
		{"abs", []types.Value{d1}},
		{"ceil", []types.Value{d1}},
		{"floor", []types.Value{d1}},
		{"sqrt", []types.Value{d1}},
		{"mod", []types.Value{d1, d2}},
		{"power", []types.Value{d1, d2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for count := 0; count <= len(tc.args)+1; count++ {
				for _, nulls := range []bool{false, true} {
					t.Run(fmt.Sprintf("args=%d/nulls=%t", count, nulls), func(t *testing.T) {
						args := append(append([]types.Value(nil), tc.args...), d1)[:count]
						if nulls {
							for i := range args {
								args[i] = types.Null(args[i].Typ)
							}
						}
						v, handled, err := evalNumericFn(tc.name, args)
						if !handled {
							t.Fatal("numeric function was not handled")
						}
						if count != len(tc.args) {
							if !nerr.HasCode(err, nerr.InvalidArgument) {
								t.Fatalf("expected InvalidArgument for %s with %d args, got %v", tc.name, count, err)
							}
							return
						}
						if err != nil || v.Null != nulls {
							t.Fatalf("valid arity: value=%+v err=%v", v, err)
						}
					})
				}
			}
		})
	}

	// Test round with 1 and 2 arguments
	for count := 0; count <= 3; count++ {
		for _, nulls := range []bool{false, true} {
			t.Run(fmt.Sprintf("round/args=%d/nulls=%t", count, nulls), func(t *testing.T) {
				args := []types.Value{d1, d2, d1}[:count]
				if nulls {
					for i := range args {
						args[i] = types.Null(args[i].Typ)
					}
				}
				v, handled, err := evalNumericFn("round", args)
				if !handled {
					t.Fatal("round was not handled")
				}
				if count != 1 && count != 2 {
					if !nerr.HasCode(err, nerr.InvalidArgument) {
						t.Fatalf("expected InvalidArgument for round with %d args, got %v", count, err)
					}
					return
				}
				if err != nil || v.Null != nulls {
					t.Fatalf("valid arity: value=%+v err=%v", v, err)
				}
			})
		}
	}
}

func TestStringFunctionArity(t *testing.T) {
	s1 := types.StringValue("hello")
	s2 := types.StringValue("l")
	s3 := types.StringValue("x")
	d1 := decimalIntValue(1)
	d2 := decimalIntValue(2)

	for _, tc := range []struct {
		name string
		args []types.Value
	}{
		{"lower", []types.Value{s1}},
		{"upper", []types.Value{s1}},
		{"length", []types.Value{s1}},
		{"trim", []types.Value{s1}},
		{"ltrim", []types.Value{s1}},
		{"rtrim", []types.Value{s1}},
		{"starts_with", []types.Value{s1, s2}},
		{"ends_with", []types.Value{s1, s2}},
		{"contains", []types.Value{s1, s2}},
		{"replace", []types.Value{s1, s2, s3}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for count := 0; count <= len(tc.args)+1; count++ {
				for _, nulls := range []bool{false, true} {
					t.Run(fmt.Sprintf("args=%d/nulls=%t", count, nulls), func(t *testing.T) {
						args := append(append([]types.Value(nil), tc.args...), s1)[:count]
						if nulls {
							for i := range args {
								args[i] = types.Null(args[i].Typ)
							}
						}
						v, handled, err := evalStringFn(tc.name, args)
						if !handled {
							t.Fatal("string function was not handled")
						}
						if count != len(tc.args) {
							if !nerr.HasCode(err, nerr.InvalidArgument) {
								t.Fatalf("expected InvalidArgument for %s with %d args, got %v", tc.name, count, err)
							}
							return
						}
						if err != nil || v.Null != nulls {
							t.Fatalf("valid arity: value=%+v err=%v", v, err)
						}
					})
				}
			}
		})
	}

	// Test substring with 2 and 3 arguments
	for count := 0; count <= 4; count++ {
		for _, nulls := range []bool{false, true} {
			t.Run(fmt.Sprintf("substring/args=%d/nulls=%t", count, nulls), func(t *testing.T) {
				args := []types.Value{s1, d1, d2, s1}[:count]
				if nulls {
					for i := range args {
						args[i] = types.Null(args[i].Typ)
					}
				}
				v, handled, err := evalStringFn("substring", args)
				if !handled {
					t.Fatal("substring was not handled")
				}
				if count != 2 && count != 3 {
					if !nerr.HasCode(err, nerr.InvalidArgument) {
						t.Fatalf("expected InvalidArgument for substring with %d args, got %v", count, err)
					}
					return
				}
				if err != nil || v.Null != nulls {
					t.Fatalf("valid arity: value=%+v err=%v", v, err)
				}
			})
		}
	}

	// Test concat with 0 arguments
	_, handled, err := evalStringFn("concat", nil)
	if !handled || !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("expected InvalidArgument for concat with 0 args, got %v", err)
	}
}

func TestJSONFunctionArity(t *testing.T) {
	raw, err := nsjson.FromText([]byte(`{"a": [1, 2, 3]}`))
	if err != nil {
		t.Fatal(err)
	}
	j1 := types.JSONValue(raw)
	s1 := types.StringValue("a")
	s2 := types.StringValue("1")

	for _, tc := range []struct {
		name string
		args []types.Value
	}{
		{"json_get", []types.Value{j1, s1}},
		{"json_remove", []types.Value{j1, s1}},
		{"json_contains", []types.Value{j1, s2}},
		{"json_set", []types.Value{j1, s1, s2}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for count := 0; count <= len(tc.args)+1; count++ {
				for _, nulls := range []bool{false, true} {
					t.Run(fmt.Sprintf("args=%d/nulls=%t", count, nulls), func(t *testing.T) {
						args := append(append([]types.Value(nil), tc.args...), s1)[:count]
						if nulls {
							for i := range args {
								args[i] = types.Null(args[i].Typ)
							}
						}
						v, handled, err := evalJSONFn(tc.name, args)
						if !handled {
							t.Fatal("json function was not handled")
						}
						if count != len(tc.args) {
							if !nerr.HasCode(err, nerr.InvalidArgument) {
								t.Fatalf("expected InvalidArgument for %s with %d args, got %v", tc.name, count, err)
							}
							return
						}
						if err != nil || v.Null != nulls {
							t.Fatalf("valid arity: value=%+v err=%v", v, err)
						}
					})
				}
			}
		})
	}

	// Test json_array_length and json_type (1 or 2 args)
	jArr, _ := nsjson.FromText([]byte(`[1, 2, 3]`))
	for _, fn := range []string{"json_array_length", "json_type"} {
		for count := 0; count <= 3; count++ {
			for _, nulls := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/args=%d/nulls=%t", fn, count, nulls), func(t *testing.T) {
					target := j1
					if fn == "json_array_length" && count == 1 {
						target = types.JSONValue(jArr)
					}
					args := []types.Value{target, s1, s1}[:count]
					if nulls {
						for i := range args {
							args[i] = types.Null(args[i].Typ)
						}
					}
					v, handled, err := evalJSONFn(fn, args)
					if !handled {
						t.Fatal(fn + " was not handled")
					}
					if count != 1 && count != 2 {
						if !nerr.HasCode(err, nerr.InvalidArgument) {
							t.Fatalf("expected InvalidArgument for %s with %d args, got %v", fn, count, err)
						}
						return
					}
					if err != nil || v.Null != nulls {
						t.Fatalf("valid arity: value=%+v err=%v", v, err)
					}
				})
			}
		}
	}
}

func TestScalarFunctionAritySQL(t *testing.T) {
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE scalar_arity (id INT64 PRIMARY KEY, j JSON)`)
	execOK(t, s, `INSERT INTO scalar_arity (id, j) VALUES (1, '{"k": 1}')`)

	// Invalid arity must fail with InvalidArgument via SQL execution
	for _, sql := range []string{
		`SELECT ABS()`,
		`SELECT ABS(1, 2)`,
		`SELECT MOD(1)`,
		`SELECT MOD(1, 2, 3)`,
		`SELECT POWER(2)`,
		`SELECT SQRT()`,
		`SELECT ROUND()`,
		`SELECT ROUND(1, 2, 3)`,
		`SELECT LOWER()`,
		`SELECT LOWER('a', 'b')`,
		`SELECT REPLACE('a', 'b')`,
		`SELECT STARTS_WITH('a')`,
		`SELECT SUBSTRING('abc')`,
		`SELECT JSON_GET('{"a":1}')`,
		`SELECT JSON_SET('{"a":1}', 'a')`,
		`SELECT JSON_REMOVE('{"a":1}')`,
		`SELECT JSON_CONTAINS('{"a":1}')`,
	} {
		t.Run(sql, func(t *testing.T) {
			_, err := s.Exec(sql)
			if !nerr.HasCode(err, nerr.InvalidArgument) {
				t.Fatalf("expected InvalidArgument for %q, got %v", sql, err)
			}
		})
	}

	// Valid calls return expected results
	got := execOK(t, s, `SELECT ABS(-42), MOD(10, 3), REPLACE('hello world', 'world', 'there'), JSON_GET(j, 'k') FROM scalar_arity`)
	if len(got.Rows) != 1 {
		t.Fatalf("unexpected row count: %d", len(got.Rows))
	}
	if got.Rows[0][0].String() != "42" {
		t.Fatalf("unexpected ABS: %v", got.Rows[0][0])
	}
	if got.Rows[0][1].String() != "1" {
		t.Fatalf("unexpected MOD: %v", got.Rows[0][1])
	}
	if got.Rows[0][2].String() != "hello there" {
		t.Fatalf("unexpected REPLACE: %v", got.Rows[0][2])
	}
	if got.Rows[0][3].String() != "1" {
		t.Fatalf("unexpected JSON_GET: %v", got.Rows[0][3])
	}
}
