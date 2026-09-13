package executor

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/sql/types"
)

// BOOL is the value kind the engine has always had and no CREATE TABLE could
// declare: every comparison produces one, the row codec and the sortable key
// codec have always encoded one, and the catalog could persist one -- but the
// grammar had no spelling for it, so no column could hold one. Log #283 found
// that when CREATE TABLE AS became able to derive a column type from a query:
// `qty > 15` produced a table whose own canonical DDL would not re-parse, so it
// could not be logically exported and restored, and the statement had to refuse
// it. These tests cover the declared type end to end.

// A BOOL column stores TRUE, FALSE and NULL, keeps its declared type through a
// restart, and comes back as BOOL rather than as text or a number.
func TestBoolInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL)`)
	execOK(t, s, `INSERT INTO flags (id, active) VALUES (1, TRUE), (2, FALSE), (3, NULL)`)

	assertFlags := func(t *testing.T, s *Session, where string) {
		t.Helper()
		got, err := s.Exec(`SELECT id, active FROM flags ORDER BY id`)
		if err != nil {
			t.Fatalf("%s: %v", where, err)
		}
		if len(got.Rows) != 3 {
			t.Fatalf("%s: %d rows, want 3", where, len(got.Rows))
		}
		for i, want := range []struct{ null, val bool }{{false, true}, {false, false}, {true, false}} {
			cell := got.Rows[i][1]
			if cell.Typ.Kind != types.KindBool {
				t.Fatalf("%s: row %d is %s, want BOOL", where, i+1, cell.Typ.String())
			}
			if cell.Null != want.null || (!cell.Null && cell.Bool != want.val) {
				t.Fatalf("%s: row %d = %v (null=%v), want %v (null=%v)", where, i+1, cell.Bool, cell.Null, want.val, want.null)
			}
		}
	}
	assertFlags(t, s, "before restart")

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	assertFlags(t, reopened.Session(), "after restart")
}

// FALSE sorts before TRUE, in the heap order and through an index key. A
// declared type that ordered its own values wrongly would corrupt every
// ORDER BY, PRIMARY KEY and index over it.
func TestBoolOrdering(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL)`)
	execOK(t, s, `INSERT INTO flags (id, active) VALUES (1, TRUE), (2, FALSE), (3, TRUE)`)

	got := execOK(t, s, `SELECT id FROM flags ORDER BY active, id`)
	if len(got.Rows) != 3 || got.Rows[0][0].Int != 2 {
		t.Fatalf("ORDER BY active put %v first, want the FALSE row", got.Rows)
	}

	// A BOOL primary key is a clustered key like any other.
	execOK(t, s, `CREATE TABLE keyed (active BOOL PRIMARY KEY, note STRING)`)
	execOK(t, s, `INSERT INTO keyed (active, note) VALUES (TRUE, 'yes'), (FALSE, 'no')`)
	keyed := execOK(t, s, `SELECT note FROM keyed ORDER BY active`)
	if len(keyed.Rows) != 2 || keyed.Rows[0][0].Str != "no" || keyed.Rows[1][0].Str != "yes" {
		t.Fatalf("BOOL primary key scanned as %v, want no then yes", keyed.Rows)
	}
	if _, err := s.Exec(`INSERT INTO keyed (active, note) VALUES (TRUE, 'again')`); err == nil {
		t.Fatal("a BOOL primary key accepted a duplicate")
	}

	// A secondary index over the column is used and answers correctly.
	execOK(t, s, `CREATE INDEX ix_active ON flags (active)`)
	plan := execOK(t, s, `EXPLAIN SELECT id FROM flags WHERE active = TRUE`)
	if !strings.Contains(strings.ToLower(planText(plan)), "indexscan") {
		t.Fatalf("EXPLAIN did not use the index over a BOOL column: %s", planText(plan))
	}
	viaIndex := execOK(t, s, `SELECT id FROM flags WHERE active = TRUE ORDER BY id`)
	if len(viaIndex.Rows) != 2 {
		t.Fatalf("index lookup returned %v, want the two TRUE rows", viaIndex.Rows)
	}
}

// A BOOL column is a predicate, so it can be tested directly and combined --
// including the three-valued case where NULL is neither TRUE nor FALSE.
func TestBoolPredicates(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL)`)
	execOK(t, s, `INSERT INTO flags (id, active) VALUES (1, TRUE), (2, FALSE), (3, NULL)`)

	for _, tc := range []struct {
		where string
		want  []int64
	}{
		{"active", []int64{1}},
		{"NOT active", []int64{2}},
		{"active = TRUE", []int64{1}},
		{"active <> TRUE", []int64{2}},
		{"active IS NULL", []int64{3}},
		{"active IS NOT NULL", []int64{1, 2}},
		{"active OR id = 3", []int64{1, 3}},
		{"active AND id = 1", []int64{1}},
	} {
		got := execOK(t, s, `SELECT id FROM flags WHERE `+tc.where+` ORDER BY id`)
		if len(got.Rows) != len(tc.want) {
			t.Fatalf("WHERE %s returned %v, want ids %v", tc.where, got.Rows, tc.want)
		}
		for i, want := range tc.want {
			if got.Rows[i][0].Int != want {
				t.Fatalf("WHERE %s returned %v, want ids %v", tc.where, got.Rows, tc.want)
			}
		}
	}
}

// BOOL is isolated from every other family in both directions except rendering
// as text: there is no truthiness, so neither a number nor a quoted string
// becomes a BOOL, and a BOOL becomes neither a number nor JSON. Rendering one
// as text is allowed, which is what makes CAST the documented remedy when a
// derived BOOL is not the column type wanted.
func TestBoolCoercionIsolation(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL)`)
	execOK(t, s, `INSERT INTO flags (id, active) VALUES (1, TRUE)`)

	for _, sql := range []string{
		`INSERT INTO flags (id, active) VALUES (2, 1)`,
		`INSERT INTO flags (id, active) VALUES (2, 'TRUE')`,
		`SELECT CAST('TRUE' AS BOOL)`,
		`SELECT CAST(1 AS BOOL)`,
		`SELECT CAST(TRUE AS INT64)`,
		`SELECT CAST(TRUE AS DECIMAL(1,0))`,
		`SELECT active + 1 FROM flags`,
		`SELECT SUM(active) FROM flags`,
		`SELECT AVG(active) FROM flags`,
		`SELECT active = 1 FROM flags`,
	} {
		if _, err := s.Exec(sql); err == nil {
			t.Fatalf("%s was accepted; BOOL has no truthiness or arithmetic", sql)
		}
	}

	text := execOK(t, s, `SELECT CAST(active AS STRING) FROM flags`)
	if text.Rows[0][0].Str != "TRUE" {
		t.Fatalf("CAST(active AS STRING) = %q, want TRUE", text.Rows[0][0].Str)
	}
	// MIN/MAX/COUNT work through the generic comparison path.
	agg := execOK(t, s, `SELECT MIN(active), MAX(active), COUNT(active) FROM flags`)
	if agg.Rows[0][0].Bool != true || agg.Rows[0][2].Dec.String() != "1" {
		t.Fatalf("aggregates over BOOL: %v", agg.Rows)
	}
}

// A BOOL column takes a DEFAULT, a NOT NULL and a CHECK like any other scalar.
func TestBoolConstraints(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL NOT NULL DEFAULT FALSE, agreed BOOL CHECK (agreed IS NULL OR agreed))`)
	execOK(t, s, `INSERT INTO flags (id) VALUES (1)`)
	got := execOK(t, s, `SELECT active FROM flags WHERE id = 1`)
	if got.Rows[0][0].Null || got.Rows[0][0].Bool {
		t.Fatalf("DEFAULT FALSE stored %v", got.Rows[0][0])
	}
	if _, err := s.Exec(`INSERT INTO flags (id, agreed) VALUES (3, FALSE)`); err == nil {
		t.Fatal("CHECK over a BOOL column did not fire")
	}
	execOK(t, s, `INSERT INTO flags (id, agreed) VALUES (4, TRUE)`)

	// NOT NULL without a default rejects NULL, from a literal and from a
	// parameter alike.
	execOK(t, s, `CREATE TABLE required (id INT64 PRIMARY KEY, active BOOL NOT NULL)`)
	if _, err := s.Exec(`INSERT INTO required (id, active) VALUES (1, NULL)`); err == nil {
		t.Fatal("NOT NULL BOOL accepted NULL")
	}
	if _, err := s.ExecContext(context.Background(), `INSERT INTO required (id, active) VALUES (1, $1)`,
		[]Param{{Value: types.Null(types.Bool())}}); err == nil {
		t.Fatal("NOT NULL BOOL accepted a NULL parameter")
	}
}

// A BOOL column is an ordinary foreign-key-eligible scalar.
func TestBoolForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE allowed (active BOOL PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE uses (id INT64 PRIMARY KEY, active BOOL NOT NULL REFERENCES allowed(active))`)
	execOK(t, s, `INSERT INTO allowed (active) VALUES (TRUE)`)
	execOK(t, s, `INSERT INTO uses (id, active) VALUES (1, TRUE)`)
	if _, err := s.Exec(`INSERT INTO uses (id, active) VALUES (2, FALSE)`); err == nil {
		t.Fatal("expected an FK violation for a value the parent does not hold")
	}
}

// ENCRYPTED CLIENT works over a BOOL column: the server stores an opaque
// ciphertext and refuses a plaintext value, exactly as for every other
// eligible scalar.
func TestBoolEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE secrets (id STRING PRIMARY KEY, active BOOL ENCRYPTED CLIENT NOT NULL)`)

	fieldKey := clientenc.Key{ID: "k1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 7
	}
	provider := executorFieldKeys{key: fieldKey}
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "secrets", "active", types.BoolValue(true))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, active) VALUES ('1', $1)`,
		[]Param{{Value: types.BoolValue(true)}}); err == nil {
		t.Fatal("server accepted plaintext for an ENCRYPTED CLIENT BOOL column")
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, active) VALUES ('1', $1)`,
		[]Param{{Value: types.StringValue(ciphertext)}}); err != nil {
		t.Fatal(err)
	}
	row, err := s.Exec(`SELECT active FROM secrets WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	decrypted, err := clientenc.Decrypt(context.Background(), provider, "app", "secrets", "active", row.Rows[0][0].Str)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted.Typ.Kind != types.KindBool || !decrypted.Bool {
		t.Fatalf("decrypted %+v, want BOOL TRUE", decrypted)
	}
}

// The table's own canonical DDL -- what system.table_ddl reports and what a
// logical export replays -- declares the BOOL column and parses back. This is
// the property whose absence log #283 had to refuse.
func TestBoolCanonicalDDLRoundTrips(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE flags (id INT64 PRIMARY KEY, active BOOL)`)
	got := execOK(t, s, `SELECT * FROM system.table_ddl`)
	var text string
	for _, row := range got.Rows {
		if candidate := row[len(row)-1].Str; strings.Contains(candidate, "CREATE TABLE") {
			text = candidate
		}
	}
	if !strings.Contains(text, `"active" BOOL`) {
		t.Fatalf("canonical DDL = %q, want a BOOL column", text)
	}
	execOK(t, s, `DROP TABLE flags`)
	execOK(t, s, text)
	execOK(t, s, `INSERT INTO flags (id, active) VALUES (1, TRUE)`)
}

// CREATE TABLE AS derives a BOOL column from a comparison. Log #283 had to
// refuse exactly this, because the derived table's DDL would not re-parse.
func TestBoolDerivedByCreateTableAs(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE src (id INT64 PRIMARY KEY, qty INT64)`)
	execOK(t, s, `INSERT INTO src (id, qty) VALUES (1, 20), (2, 5)`)
	execOK(t, s, `CREATE TABLE big PRIMARY KEY (id) AS SELECT id, qty > 15 AS big FROM src`)

	got := execOK(t, s, `SELECT id, big FROM big ORDER BY id`)
	if len(got.Rows) != 2 {
		t.Fatalf("derived %d rows, want 2", len(got.Rows))
	}
	if got.Rows[0][1].Typ.Kind != types.KindBool || !got.Rows[0][1].Bool || got.Rows[1][1].Bool {
		t.Fatalf("derived column = %v, want TRUE then FALSE as BOOL", got.Rows)
	}
}

// BOOL joins the type keywords as a reserved word, the same as every other one
// (STRING, TEXT, INT64 ...). A schema that used it as an identifier keeps
// working through the quoted form, which is what the canonical DDL renderer
// emits for every identifier.
func TestBoolIsReservedButQuotesStillWork(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (bool INT64 PRIMARY KEY)`); err == nil {
		t.Fatal("bool was accepted as a bare identifier; it is a type keyword now")
	}
	execOK(t, s, `CREATE TABLE t ("bool" INT64 PRIMARY KEY, "BOOL" STRING)`)
	execOK(t, s, `INSERT INTO t ("bool", "BOOL") VALUES (1, 'x')`)
	got := execOK(t, s, `SELECT "bool" FROM t`)
	if len(got.Rows) != 1 || got.Rows[0][0].Int != 1 {
		t.Fatalf("quoted identifier returned %v", got.Rows)
	}
}
