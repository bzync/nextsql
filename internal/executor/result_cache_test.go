package executor

import (
	"context"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func execParamsOK(t *testing.T, s *Session, sql string, params ...Param) *Result {
	t.Helper()
	result, err := s.ExecContext(context.Background(), sql, params)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func TestResultCacheHitIsolationAndInvalidation(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cache_items (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO cache_items (id, value) VALUES ('1', 'first')`)

	first := execOK(t, s, `SELECT value FROM cache_items WHERE id = '1'`)
	if first.Cached || len(first.Rows) != 1 || first.Rows[0][0].Str != "first" {
		t.Fatalf("first result: %+v", first)
	}
	second := execOK(t, s, `SELECT value FROM cache_items WHERE id = '1'`)
	if !second.Cached {
		t.Fatal("expected result-cache hit")
	}
	second.Rows[0][0].Str = "caller mutation"
	third := execOK(t, s, `SELECT value FROM cache_items WHERE id = '1'`)
	if !third.Cached || third.Rows[0][0].Str != "first" {
		t.Fatalf("cached value was not isolated: %+v", third.Rows)
	}

	execOK(t, s, `UPDATE cache_items SET value = 'second' WHERE id = '1'`)
	afterWrite := execOK(t, s, `SELECT value FROM cache_items WHERE id = '1'`)
	if afterWrite.Cached || afterWrite.Rows[0][0].Str != "second" {
		t.Fatalf("stale result after write: cached=%v rows=%+v", afterWrite.Cached, afterWrite.Rows)
	}
	again := execOK(t, s, `SELECT value FROM cache_items WHERE id = '1'`)
	if !again.Cached || again.Rows[0][0].Str != "second" {
		t.Fatalf("cache did not refill: %+v", again)
	}

	stats := db.ResultCacheStats()
	if stats.Hits < 3 || stats.Misses < 2 || stats.Entries < 1 || stats.Bytes < 1 {
		t.Fatalf("cache stats: %+v", stats)
	}
}

func TestResultCacheParametersIdentityVolatilityAndTransactions(t *testing.T) {
	db := testDB(t)
	setup := db.Session()
	execOK(t, setup, `CREATE TABLE cache_scope (id STRING PRIMARY KEY, value STRING NOT NULL)`)
	execOK(t, setup, `INSERT INTO cache_scope (id, value) VALUES ('1', 'one'), ('2', 'two')`)

	a := db.Session()
	a.SetIdentity("alice")
	one := execParamsOK(t, a, `SELECT value FROM cache_scope WHERE id = $1`, Param{Value: types.StringValue("1")})
	if one.Cached || one.Rows[0][0].Str != "one" {
		t.Fatalf("first parameter result: %+v", one)
	}
	oneHit := execParamsOK(t, a, `SELECT value FROM cache_scope WHERE id = $1`, Param{Value: types.StringValue("1")})
	if !oneHit.Cached {
		t.Fatal("expected typed-parameter cache hit")
	}
	two := execParamsOK(t, a, `SELECT value FROM cache_scope WHERE id = $1`, Param{Value: types.StringValue("2")})
	if two.Cached || two.Rows[0][0].Str != "two" {
		t.Fatalf("parameter cache collision: %+v", two)
	}

	b := db.Session()
	b.SetIdentity("bob")
	bob := execParamsOK(t, b, `SELECT value FROM cache_scope WHERE id = $1`, Param{Value: types.StringValue("1")})
	if bob.Cached {
		t.Fatal("result cache crossed user identity")
	}

	volatile1 := execOK(t, a, `SELECT UUID() FROM cache_scope WHERE id = '1'`)
	volatile2 := execOK(t, a, `SELECT UUID() FROM cache_scope WHERE id = '1'`)
	if volatile1.Cached || volatile2.Cached || volatile1.Rows[0][0].UUID == volatile2.Rows[0][0].UUID {
		t.Fatalf("volatile result cached: %+v %+v", volatile1, volatile2)
	}

	execOK(t, a, `BEGIN`)
	inTxn1 := execOK(t, a, `SELECT value FROM cache_scope WHERE id = '1'`)
	inTxn2 := execOK(t, a, `SELECT value FROM cache_scope WHERE id = '1'`)
	if inTxn1.Cached || inTxn2.Cached {
		t.Fatal("explicit transaction used global result cache")
	}
	execOK(t, a, `ROLLBACK`)
}
