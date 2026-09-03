package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/sql/types"
)

// TestDateTimeInsertSelectRoundTrip covers D5 (Datatype expansion track):
// DATE and TIME column CRUD via string-literal coercion, PK usage, catalog
// persist/reopen, and index-key round-trips.
func TestDateTimeInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE events (id INT64 PRIMARY KEY, d DATE, tm TIME)`)

	execOK(t, s, `INSERT INTO events (id, d, tm) VALUES (1, '2024-01-15', '13:45:00')`)
	execOK(t, s, `INSERT INTO events (id, d, tm) VALUES (2, '1969-12-31', '00:00:00')`)
	execOK(t, s, `INSERT INTO events (id, d, tm) VALUES (3, '1970-01-01', '23:59:59.999999999')`)
	execOK(t, s, `INSERT INTO events (id, d, tm) VALUES (4, '2000-02-29', '12:00:00.5')`) // leap day

	got, err := s.Exec(`SELECT d, tm FROM events WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row[0].Typ.Kind != types.KindDate || row[0].String() != "2024-01-15" {
		t.Fatalf("unexpected date: %+v", row[0])
	}
	if row[1].Typ.Kind != types.KindTime || row[1].String() != "13:45:00" {
		t.Fatalf("unexpected time: %+v", row[1])
	}

	got2, err := s.Exec(`SELECT d, tm FROM events WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-epoch date: day count is negative.
	if got2.Rows[0][0].Int != -1 {
		t.Fatalf("expected day count -1 for 1969-12-31, got %d", got2.Rows[0][0].Int)
	}

	got3, err := s.Exec(`SELECT d, tm FROM events WHERE id = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if got3.Rows[0][0].Int != 0 {
		t.Fatalf("expected day count 0 for 1970-01-01, got %d", got3.Rows[0][0].Int)
	}
	if got3.Rows[0][1].Time != 86399999999999 {
		t.Fatalf("expected 86399999999999 ns for 23:59:59.999999999, got %d", got3.Rows[0][1].Time)
	}
	if got3.Rows[0][1].String() != "23:59:59.999999999" {
		t.Fatalf("unexpected time format: %s", got3.Rows[0][1].String())
	}

	got4, err := s.Exec(`SELECT d, tm FROM events WHERE id = 4`)
	if err != nil {
		t.Fatal(err)
	}
	if got4.Rows[0][0].String() != "2000-02-29" {
		t.Fatalf("expected leap day to round-trip, got %s", got4.Rows[0][0].String())
	}
	if got4.Rows[0][1].String() != "12:00:00.5" {
		t.Fatalf("expected fractional time to round-trip, got %s", got4.Rows[0][1].String())
	}

	// Persist/reopen: catalog type + stored values must survive a restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rs := reopened.Session()
	after, err := rs.Exec(`SELECT d, tm FROM events WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].String() != "2024-01-15" || after.Rows[0][1].String() != "13:45:00" {
		t.Fatalf("date/time did not survive restart: %+v", after.Rows[0])
	}
}

// TestDateOrderByStraddlesEpoch is the critical index-key correctness test
// for DATE: negative (pre-1970) and positive day counts must sort
// numerically, not as raw two's-complement bytes (docs/design-datatypes.md D5).
func TestDateOrderByStraddlesEpoch(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE d (id INT64 PRIMARY KEY, v DATE)`)
	dates := []string{"2024-01-15", "1900-01-01", "1969-12-31", "1970-01-01", "2100-12-31", "0001-01-01"}
	for i, v := range dates {
		execOK(t, s, "INSERT INTO d (id, v) VALUES ("+intLit(i+1)+", '"+v+"')")
	}
	got, err := s.Exec(`SELECT v FROM d ORDER BY v ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"0001-01-01", "1900-01-01", "1969-12-31", "1970-01-01", "2024-01-15", "2100-12-31"}
	if len(got.Rows) != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), len(got.Rows))
	}
	for i, row := range got.Rows {
		if row[0].String() != want[i] {
			t.Fatalf("ORDER BY DATE not numeric at %d: got %v want %v (full: %+v)", i, row[0].String(), want[i], got.Rows)
		}
	}
}

// TestTimeOrderBy covers TIME's plain-unsigned index-key ordering.
func TestTimeOrderBy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, v TIME)`)
	times := []string{"13:00:00", "00:00:00", "23:59:59.999999999", "00:00:00.000000001", "12:00:00"}
	for i, v := range times {
		execOK(t, s, "INSERT INTO t (id, v) VALUES ("+intLit(i+1)+", '"+v+"')")
	}
	got, err := s.Exec(`SELECT v FROM t ORDER BY v ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"00:00:00", "00:00:00.000000001", "12:00:00", "13:00:00", "23:59:59.999999999"}
	for i, row := range got.Rows {
		if row[0].String() != want[i] {
			t.Fatalf("ORDER BY TIME wrong at %d: got %v want %v", i, row[0].String(), want[i])
		}
	}
}

// TestDateTimeInvalidTextRejected covers CAST/coercion failure paths: DATE
// and TIME are isolated from every family but text (docs/design-datatypes.md
// D5, mirroring D1-D3's isolation precedent) — malformed text and
// out-of-range TIME must error, and neither type implicitly accepts an
// integer or DECIMAL value.
func TestDateTimeInvalidTextRejected(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, d DATE, tm TIME)`)

	if _, err := s.Exec(`INSERT INTO t (id, d) VALUES (1, 'not-a-date')`); err == nil {
		t.Fatal("expected malformed DATE text to be rejected")
	}
	if _, err := s.Exec(`INSERT INTO t (id, d) VALUES (1, '2024-13-01')`); err == nil {
		t.Fatal("expected out-of-range month to be rejected")
	}
	if _, err := s.Exec(`INSERT INTO t (id, tm) VALUES (1, '24:00:00')`); err == nil {
		t.Fatal("expected hour 24 to be rejected")
	}
	if _, err := s.Exec(`INSERT INTO t (id, tm) VALUES (1, 'not-a-time')`); err == nil {
		t.Fatal("expected malformed TIME text to be rejected")
	}

	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t (id, d) VALUES (1, $1)`,
		[]Param{{Value: types.Int32Value(19737)}}); err == nil {
		t.Fatal("expected DATE to reject an implicit integer day count")
	}

	execOK(t, s, `INSERT INTO t (id, d, tm) VALUES (2, '2024-01-15', '13:45:00')`)
}

// TestDateTimeArithmeticRejected: calendar/time arithmetic is out of scope
// for D5 (deferred to D6 INTERVAL, docs/design-datatypes.md), so + and - over
// DATE/TIME operands must error rather than silently doing something wrong.
func TestDateTimeArithmeticRejected(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, d DATE)`)
	execOK(t, s, `INSERT INTO t (id, d) VALUES (1, '2024-01-15')`)
	if _, err := s.Exec(`SELECT d + d FROM t WHERE id = 1`); err == nil {
		t.Fatal("expected DATE arithmetic to be rejected")
	}
}

// TestDateTimeAggregateAndGroupBy covers MIN/MAX (generic Value.Cmp path,
// zero extra code needed) and GROUP BY over DATE/TIME columns.
func TestDateTimeAggregateAndGroupBy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, d DATE, grp TIME)`)
	rows := []struct {
		d, tm string
	}{
		{"2024-01-15", "09:00:00"},
		{"2020-06-01", "09:00:00"},
		{"2024-01-15", "17:00:00"},
	}
	for i, r := range rows {
		execOK(t, s, "INSERT INTO t (id, d, grp) VALUES ("+intLit(i+1)+", '"+r.d+"', '"+r.tm+"')")
	}
	got, err := s.Exec(`SELECT MIN(d), MAX(d) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].String() != "2020-06-01" || got.Rows[0][1].String() != "2024-01-15" {
		t.Fatalf("unexpected MIN/MAX: %+v", got.Rows[0])
	}
	if got.Rows[0][0].Typ.Kind != types.KindDate {
		t.Fatalf("MIN did not preserve DATE kind: %+v", got.Rows[0][0])
	}

	grouped, err := s.Exec(`SELECT grp, COUNT(*) FROM t GROUP BY grp ORDER BY grp ASC`)
	if err != nil {
		t.Fatal(err)
	}
	if len(grouped.Rows) != 2 {
		t.Fatalf("expected 2 groups, got %d: %+v", len(grouped.Rows), grouped.Rows)
	}
	if grouped.Rows[0][0].String() != "09:00:00" || grouped.Rows[0][1].Dec.String() != "2" {
		t.Fatalf("unexpected first group: %+v", grouped.Rows[0])
	}
}

// TestDateTimeForeignKey covers DATE/TIME as ordinary FK-eligible scalars.
func TestDateTimeForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (id DATE PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id INT64 PRIMARY KEY, parent_id DATE NOT NULL REFERENCES parent(id))`)
	execOK(t, s, `INSERT INTO parent (id) VALUES ('2024-01-01')`)
	execOK(t, s, `INSERT INTO child (id, parent_id) VALUES (1, '2024-01-01')`)
	if _, err := s.Exec(`INSERT INTO child (id, parent_id) VALUES (2, '1999-01-01')`); err == nil {
		t.Fatal("expected FK violation for unknown parent_id")
	}
}

// TestDateTimeEncryptedClient confirms ENCRYPTED CLIENT works over DATE and
// TIME end to end (server-side opaque-ciphertext path, plaintext rejected).
func TestDateTimeEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE secrets (id STRING PRIMARY KEY, dob DATE ENCRYPTED CLIENT NOT NULL)`)

	fieldKey := clientenc.Key{ID: "k1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 9
	}
	provider := executorFieldKeys{key: fieldKey}
	dob, err := types.ParseDate("1990-05-20")
	if err != nil {
		t.Fatal(err)
	}
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "secrets", "dob", dob)
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, dob) VALUES ('1', $1)`,
		[]Param{{Value: dob}}); err == nil {
		t.Fatal("server accepted plaintext for ENCRYPTED CLIENT DATE column")
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, dob) VALUES ('1', $1)`,
		[]Param{{Value: types.StringValue(ciphertext)}}); err != nil {
		t.Fatal(err)
	}

	row, err := s.Exec(`SELECT dob FROM secrets WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	sealed := row.Rows[0][0].Str
	decrypted, err := clientenc.Decrypt(context.Background(), provider, "app", "secrets", "dob", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted.Typ.Kind != types.KindDate || decrypted.String() != "1990-05-20" {
		t.Fatalf("decrypted mismatch: %+v", decrypted)
	}
}
