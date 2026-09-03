package executor

import (
	"context"
	"math"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestFloatInsertSelectArith covers D8 (Datatype expansion track): FLOAT32/
// FLOAT64 column CRUD, float arithmetic (evaluates in float64, yields
// FLOAT64), and catalog persist/reopen.
func TestFloatInsertSelectArith(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE m (id INT64 PRIMARY KEY, x FLOAT64, y FLOAT32)`)
	execOK(t, s, `INSERT INTO m (id, x, y) VALUES (1, 1.5, 0.5)`)
	execOK(t, s, `INSERT INTO m (id, x, y) VALUES (2, -3.25, 2.5)`)
	execOK(t, s, `INSERT INTO m (id, x, y) VALUES (3, '1e10', '3.14159265358979')`)

	got, err := s.Exec(`SELECT x, y, x * 2, x + y FROM m WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rows[0]
	if r[0].Typ.Kind != types.KindFloat64 || r[0].Flt != 1.5 {
		t.Fatalf("x: %+v", r[0])
	}
	if r[1].Typ.Kind != types.KindFloat32 || r[1].Flt != 0.5 {
		t.Fatalf("y: %+v", r[1])
	}
	if r[2].Typ.Kind != types.KindFloat64 || r[2].Flt != 3.0 {
		t.Fatalf("x*2: %+v", r[2])
	}
	if r[3].Flt != 2.0 {
		t.Fatalf("x+y: %+v", r[3])
	}

	// FLOAT32 stores at 32-bit precision.
	got3, err := s.Exec(`SELECT y FROM m WHERE id = 3`)
	if err != nil {
		t.Fatal(err)
	}
	if got3.Rows[0][0].Flt != float64(float32(3.14159265358979)) {
		t.Fatalf("FLOAT32 precision: %v", got3.Rows[0][0].Flt)
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, err := reopened.Session().Exec(`SELECT x FROM m WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].Flt != -3.25 {
		t.Fatalf("did not survive restart: %v", after.Rows[0][0].Flt)
	}
}

// TestFloatOrderByTotalOrder covers the canonical index-key order end to end:
// -Inf < negatives < 0 < positives < +Inf.
func TestFloatOrderByTotalOrder(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE f (id INT64 PRIMARY KEY, v FLOAT64)`)
	rows := map[int]string{1: "3.5", 2: "-1.0", 3: "0.0", 4: "'-1e308'", 5: "100.0"}
	for id, v := range rows {
		execOK(t, s, "INSERT INTO f (id, v) VALUES ("+intLit(id)+", "+v+")")
	}
	// -0.0 as a bound param, to exercise canonicalization.
	if _, err := s.ExecContext(context.Background(), `INSERT INTO f (id, v) VALUES (6, $1)`,
		[]Param{{Value: types.Float64Value(math.Copysign(0, -1))}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Exec(`SELECT id FROM f ORDER BY v ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{4, 2, 3, 6, 1, 5}
	for i, row := range got.Rows {
		if row[0].Int != want[i] {
			t.Fatalf("ORDER BY FLOAT64 wrong at %d: %d want %d (all: %+v)", i, row[0].Int, want[i], got.Rows)
		}
	}
}

// TestFloatAggregatesAndPK covers SUM/AVG/MIN/MAX over a float column and
// FLOAT64 as a primary key (needs the canonical total order).
func TestFloatAggregatesAndPK(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE f (k FLOAT64 PRIMARY KEY)`)
	for _, v := range []string{"1.5", "2.5", "4.0"} {
		execOK(t, s, "INSERT INTO f (k) VALUES ("+v+")")
	}
	got, err := s.Exec(`SELECT MIN(k), MAX(k), SUM(k), AVG(k), COUNT(*) FROM f`)
	if err != nil {
		t.Fatal(err)
	}
	r := got.Rows[0]
	if r[0].Flt != 1.5 || r[1].Flt != 4.0 {
		t.Fatalf("MIN/MAX: %+v", r)
	}
	// SUM/AVG promote to DECIMAL (reusing the existing accumulator).
	if r[2].Dec.String() != "8.0" {
		t.Fatalf("SUM: %+v", r[2])
	}
	if r[3].Dec.String() != "2.6666666" {
		t.Fatalf("AVG: %+v", r[3])
	}
}
