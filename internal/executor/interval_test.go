package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestIntervalInsertSelectRoundTrip covers D6 (Datatype expansion track):
// INTERVAL literal text coercion, exact round trip, and catalog persist/reopen.
func TestIntervalInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, dur INTERVAL)`)
	execOK(t, s, `INSERT INTO t (id, dur) VALUES (1, '1 year 2 months 3 days 4 hours')`)

	got, err := s.Exec(`SELECT dur FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row[0].Typ.Kind != types.KindInterval || row[0].IntervalMonths != 14 || row[0].IntervalDays != 3 || row[0].Time != 4*int64(3_600_000_000_000) {
		t.Fatalf("interval insert/select: %+v", row[0])
	}
	if row[0].Str != "" && row[0].String() != "1 year 2 months 3 days 4 hours" {
		t.Fatalf("interval format: %q", row[0].String())
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rs := reopened.Session()
	after, err := rs.Exec(`SELECT dur FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].IntervalMonths != 14 || after.Rows[0][0].IntervalDays != 3 {
		t.Fatalf("interval did not survive restart: %+v", after.Rows[0][0])
	}
}

// TestIntervalDateArithmeticPromotesToTimestamp covers DATE +/- INTERVAL
// always promoting to TIMESTAMP, and calendar-month day-of-month clamping
// (Jan 31 + 1 month = Feb 28) — the entire point of D6's design.
func TestIntervalDateArithmeticPromotesToTimestamp(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, d DATE)`)
	execOK(t, s, `INSERT INTO t (id, d) VALUES (1, '2024-01-31')`)

	got, err := s.Exec(`SELECT d + INTERVAL '1 month' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	v := got.Rows[0][0]
	if v.Typ.Kind != types.KindTimestamp {
		t.Fatalf("DATE + INTERVAL should promote to TIMESTAMP, got %s", v.Typ.Kind)
	}
	if v.String() != "2024-02-29T00:00:00" && v.String() != "2024-02-29 00:00:00" {
		// 2024 is a leap year: Jan 31 + 1 month clamps to Feb 29.
		if got2, err := s.Exec(`SELECT (d + INTERVAL '1 month')::TEXT FROM t WHERE id = 1`); err == nil {
			t.Logf("cast form: %v", got2.Rows)
		}
		t.Fatalf("Jan 31 + 1 month should clamp to Feb 29 (2024 is a leap year): %q", v.String())
	}

	// A non-leap year: Jan 31 + 1 month clamps to Feb 28.
	execOK(t, s, `INSERT INTO t (id, d) VALUES (2, '2023-01-31')`)
	got2, err := s.Exec(`SELECT d + INTERVAL '1 month' FROM t WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Rows[0][0].String() != "2023-02-28T00:00:00" && got2.Rows[0][0].String() != "2023-02-28 00:00:00" {
		t.Fatalf("Jan 31 + 1 month (2023, non-leap) should clamp to Feb 28: %q", got2.Rows[0][0].String())
	}

	// DATE - INTERVAL also promotes.
	got3, err := s.Exec(`SELECT d - INTERVAL '1 day' FROM t WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if got3.Rows[0][0].Typ.Kind != types.KindTimestamp {
		t.Fatalf("DATE - INTERVAL should promote to TIMESTAMP: %+v", got3.Rows[0][0])
	}
}

// TestIntervalTimestampArithmetic covers TIMESTAMP/TIMESTAMPTZ +/- INTERVAL
// staying in their own Kind (no promotion, unlike DATE).
func TestIntervalTimestampArithmetic(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, ts TIMESTAMP, tz TIMESTAMPTZ)`)
	execOK(t, s, `INSERT INTO t (id, ts, tz) VALUES (1, '2024-06-15 10:00:00', '2024-06-15T10:00:00Z')`)

	got, err := s.Exec(`SELECT ts + INTERVAL '1 day 2 hours', tz + INTERVAL '1 day 2 hours' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row[0].Typ.Kind != types.KindTimestamp {
		t.Fatalf("TIMESTAMP + INTERVAL should stay TIMESTAMP: %s", row[0].Typ.Kind)
	}
	if row[1].Typ.Kind != types.KindTimestampTZ {
		t.Fatalf("TIMESTAMPTZ + INTERVAL should stay TIMESTAMPTZ: %s", row[1].Typ.Kind)
	}
	wantNaive := "2024-06-16T12:00:00"
	if row[0].String() != wantNaive && row[0].String() != "2024-06-16 12:00:00" {
		t.Fatalf("ts + interval: %q", row[0].String())
	}

	// INTERVAL + TIMESTAMP (commutative) gives the same result.
	got2, err := s.Exec(`SELECT INTERVAL '1 day 2 hours' + ts FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Rows[0][0].Time != row[0].Time {
		t.Fatalf("INTERVAL + TIMESTAMP should equal TIMESTAMP + INTERVAL: %+v vs %+v", got2.Rows[0][0], row[0])
	}

	// INTERVAL - TIMESTAMP is rejected.
	if _, err := s.Exec(`SELECT INTERVAL '1 day' - ts FROM t WHERE id = 1`); err == nil {
		t.Fatal("expected INTERVAL - TIMESTAMP to be rejected")
	}
}

// TestIntervalTimeArithmeticWraps covers TIME +/- INTERVAL discarding the
// interval's months/days components and wrapping modulo 24h (matching
// Postgres's own time+interval rule).
func TestIntervalTimeArithmeticWraps(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, tod TIME)`)
	execOK(t, s, `INSERT INTO t (id, tod) VALUES (1, '23:00:00')`)

	got, err := s.Exec(`SELECT tod + INTERVAL '2 hours' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindTime {
		t.Fatalf("TIME + INTERVAL should stay TIME: %s", got.Rows[0][0].Typ.Kind)
	}
	if got.Rows[0][0].String() != "01:00:00" {
		t.Fatalf("23:00:00 + 2 hours should wrap to 01:00:00: %q", got.Rows[0][0].String())
	}

	// The interval's day component is discarded for TIME arithmetic.
	got2, err := s.Exec(`SELECT tod + INTERVAL '5 days 1 hour' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Rows[0][0].String() != "00:00:00" {
		t.Fatalf("TIME arithmetic must discard the day component: %q", got2.Rows[0][0].String())
	}
}

// TestIntervalArithmetic covers INTERVAL +/- INTERVAL and unary negation.
// This dialect requires FROM on every SELECT (no bare "SELECT <expr>"), so
// these compute against a real one-row table like every other test here.
func TestIntervalArithmetic(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO t (id) VALUES (1)`)

	got, err := s.Exec(`SELECT INTERVAL '1 month 5 days' + INTERVAL '1 month 30 days' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	v := got.Rows[0][0]
	if v.Typ.Kind != types.KindInterval || v.IntervalMonths != 2 || v.IntervalDays != 35 {
		t.Fatalf("interval + interval: %+v", v)
	}

	got2, err := s.Exec(`SELECT INTERVAL '2 months' - INTERVAL '1 month 10 days' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	v2 := got2.Rows[0][0]
	if v2.IntervalMonths != 1 || v2.IntervalDays != -10 {
		t.Fatalf("interval - interval: %+v", v2)
	}

	got3, err := s.Exec(`SELECT -INTERVAL '1 month 5 days' FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	v3 := got3.Rows[0][0]
	if v3.IntervalMonths != -1 || v3.IntervalDays != -5 {
		t.Fatalf("negated interval: %+v", v3)
	}
}

// TestIntervalSubtractionYieldsInterval covers same-Kind temporal
// subtraction (TIMESTAMP - TIMESTAMP, DATE - DATE) producing INTERVAL as the
// exact elapsed duration. Uses two columns in one row rather than a
// self-join — simpler, and sidesteps this dialect's comma-join support
// (or lack of it) entirely, which is unrelated to what this test covers.
func TestIntervalSubtractionYieldsInterval(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, ts1 TIMESTAMP, ts2 TIMESTAMP)`)
	execOK(t, s, `INSERT INTO t (id, ts1, ts2) VALUES (1, '2024-06-16 12:00:00', '2024-06-15 10:00:00')`)

	got, err := s.Exec(`SELECT ts1 - ts2 FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	v := got.Rows[0][0]
	if v.Typ.Kind != types.KindInterval || v.IntervalMonths != 0 || v.IntervalDays != 0 {
		t.Fatalf("timestamp - timestamp should be a pure-duration interval: %+v", v)
	}
	wantNanos := int64(26) * int64(3_600_000_000_000) // 1 day 2 hours
	if v.Time != wantNanos {
		t.Fatalf("elapsed duration: got %d want %d", v.Time, wantNanos)
	}

	execOK(t, s, `CREATE TABLE d2 (id INT64 PRIMARY KEY, d1 DATE, d2 DATE)`)
	execOK(t, s, `INSERT INTO d2 (id, d1, d2) VALUES (1, '2024-01-10', '2024-01-01')`)
	got2, err := s.Exec(`SELECT d1 - d2 FROM d2 WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Rows[0][0].Time != 9*dayNanosConst {
		t.Fatalf("date - date: %+v", got2.Rows[0][0])
	}
}

// TestIntervalOrderByAndAggregates covers ORDER BY / MIN / MAX using the
// justified comparison, and SUM/AVG correctly rejecting INTERVAL.
func TestIntervalOrderByAndAggregates(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, dur INTERVAL)`)
	execOK(t, s, `INSERT INTO t (id, dur) VALUES (1, '1 month')`)
	execOK(t, s, `INSERT INTO t (id, dur) VALUES (2, '5 days')`)
	execOK(t, s, `INSERT INTO t (id, dur) VALUES (3, '2 months')`)

	got, err := s.Exec(`SELECT id FROM t ORDER BY dur ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{2, 1, 3}
	for i, row := range got.Rows {
		if row[0].Int != want[i] {
			t.Fatalf("ORDER BY INTERVAL wrong at %d: got %d want %d", i, row[0].Int, want[i])
		}
	}

	agg, err := s.Exec(`SELECT MIN(dur), MAX(dur) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Rows[0][0].IntervalDays != 5 || agg.Rows[0][1].IntervalMonths != 2 {
		t.Fatalf("MIN/MAX: %+v", agg.Rows[0])
	}

	if _, err := s.Exec(`SELECT SUM(dur) FROM t`); err == nil {
		t.Fatal("expected SUM(INTERVAL) to be rejected")
	}
}

// TestIntervalForeignKey covers INTERVAL as an ordinary FK-eligible scalar.
func TestIntervalForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (dur INTERVAL PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id INT64 PRIMARY KEY, pdur INTERVAL NOT NULL REFERENCES parent(dur))`)
	execOK(t, s, `INSERT INTO parent (dur) VALUES ('1 day')`)
	execOK(t, s, `INSERT INTO child (id, pdur) VALUES (1, '1 day')`)
	if _, err := s.Exec(`INSERT INTO child (id, pdur) VALUES (2, '2 days')`); err == nil {
		t.Fatal("expected FK violation for an interval value that was never inserted")
	}
}

// TestIntervalEncryptedClient covers INTERVAL's ENCRYPTED CLIENT eligibility
// (generic opaque-scalar reasoning, same as D1-D3/D5/D7/D8's precedent).
func TestIntervalEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id STRING PRIMARY KEY, dur INTERVAL ENCRYPTED CLIENT)`)
	if _, err := s.Exec(`INSERT INTO t (id, dur) VALUES ('a', '1 day')`); err == nil {
		t.Fatal("expected a plaintext INSERT into an ENCRYPTED CLIENT column to be rejected server-side")
	}
}
