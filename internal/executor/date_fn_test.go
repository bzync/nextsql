package executor

import (
	"fmt"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestDateFunctionArity(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []types.Value
	}{
		{"extract", []types.Value{types.StringValue("minute"), types.TimeValue(0)}},
		{"date_trunc", []types.Value{types.StringValue("minute"), types.TimeValue(0)}},
		{"date_add", []types.Value{types.TimeValue(0), decimalIntValue(-5), types.StringValue("minute")}},
		{"date_diff", []types.Value{types.TimeValue(0), types.TimeValue(0), types.StringValue("minute")}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for count := 0; count <= len(tc.args)+1; count++ {
				for _, nulls := range []bool{false, true} {
					t.Run(fmt.Sprintf("args=%d/nulls=%t", count, nulls), func(t *testing.T) {
						args := append(append([]types.Value(nil), tc.args...), types.StringValue("extra"))[:count]
						if nulls {
							for i := range args {
								args[i] = types.Null(args[i].Typ)
							}
						}
						v, handled, err := evalDateFn(tc.name, args)
						if !handled {
							t.Fatal("date function was not handled")
						}
						if count != len(tc.args) {
							if !nerr.HasCode(err, nerr.InvalidArgument) {
								t.Fatalf("expected InvalidArgument, got %v", err)
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
}

func TestDateFunctionAritySQL(t *testing.T) {
	s := testDB(t).Session()
	execOK(t, s, `CREATE TABLE date_arity (id INT64 PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO date_arity (id) VALUES (1)`)
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"EXTRACT", []string{"'minute'", "NOW()"}},
		{"DATE_TRUNC", []string{"'minute'", "NOW()"}},
		{"DATE_ADD", []string{"NOW()", "-5", "'minute'"}},
		{"DATE_DIFF", []string{"NOW()", "NOW()", "'minute'"}},
	} {
		for count := 0; count <= len(tc.args)+1; count++ {
			args := append(append([]string(nil), tc.args...), "NULL")[:count]
			for _, from := range []string{"", " FROM date_arity"} {
				sql := "SELECT " + tc.name + "(" + strings.Join(args, ", ") + ")" + from
				t.Run(sql, func(t *testing.T) {
					_, err := s.Exec(sql)
					if count == len(tc.args) {
						if err != nil {
							t.Fatal(err)
						}
					} else if !nerr.HasCode(err, nerr.InvalidArgument) {
						t.Fatalf("expected InvalidArgument, got %v", err)
					}
				})
			}
		}
	}
	// The exact Launcher report, including its INTERVAL argument.
	if _, err := s.Exec(`SELECT DATE_ADD(NOW(), INTERVAL '-5 minutes')`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("expected InvalidArgument, got %v", err)
	}
	got := execOK(t, s, `SELECT DATE_ADD(CAST('2026-10-26T12:00:00Z' AS TIMESTAMPTZ), -5, 'minute')`)
	if got.Rows[0][0].String() != "2026-10-26T11:55:00Z" {
		t.Fatalf("unexpected DATE_ADD result: %v", got.Rows)
	}
}
