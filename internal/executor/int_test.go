package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/sql/types"
)

// TestIntInsertSelectRoundTrip covers D2 (Datatype expansion track): all 4
// fixed-width int column types, boundary-value CRUD, PK usage, catalog
// persist/reopen, and index-key (ORDER BY / PK lookup) round-trips.
func TestIntInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE ints (id INT64 PRIMARY KEY, a INT8, b INT16, c INT32, d INT64)`)

	execOK(t, s, `INSERT INTO ints (id, a, b, c, d) VALUES (1, -128, -32768, -2147483648, -9223372036854775808)`)
	execOK(t, s, `INSERT INTO ints (id, a, b, c, d) VALUES (2, 127, 32767, 2147483647, 9223372036854775807)`)
	execOK(t, s, `INSERT INTO ints (id, a, b, c, d) VALUES (3, 0, 0, 0, 0)`)

	got, err := s.Exec(`SELECT a, b, c, d FROM ints WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row[0].Int != -128 || row[1].Int != -32768 || row[2].Int != -2147483648 || row[3].Int != -9223372036854775808 {
		t.Fatalf("boundary round trip mismatch: %+v", row)
	}
	if row[0].Typ.Kind != types.KindInt8 || row[1].Typ.Kind != types.KindInt16 ||
		row[2].Typ.Kind != types.KindInt32 || row[3].Typ.Kind != types.KindInt64 {
		t.Fatalf("unexpected result kinds: %+v %+v %+v %+v", row[0].Typ, row[1].Typ, row[2].Typ, row[3].Typ)
	}

	got2, err := s.Exec(`SELECT a, b, c, d FROM ints WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	row2 := got2.Rows[0]
	if row2[0].Int != 127 || row2[1].Int != 32767 || row2[2].Int != 2147483647 || row2[3].Int != 9223372036854775807 {
		t.Fatalf("max boundary round trip mismatch: %+v", row2)
	}

	// Persist/reopen: catalog type + stored ints must survive a restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rs := reopened.Session()
	after, err := rs.Exec(`SELECT a, b, c, d FROM ints WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].Int != -128 || after.Rows[0][3].Int != -9223372036854775808 {
		t.Fatalf("int did not survive restart: %+v", after.Rows[0])
	}
}

// TestIntOrderBySignCorrectness is the critical index-key correctness test:
// negative/positive/zero values across every width must sort numerically,
// not as raw two's-complement bytes (which would put every negative value
// after every positive one). See docs/design-datatypes.md D2.
func TestIntOrderBySignCorrectness(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t32 (id INT32 PRIMARY KEY)`)
	for _, v := range []int{-5, 3, -100, 0, 100, -2147483648, 2147483647} {
		execOK(t, s, "INSERT INTO t32 (id) VALUES ("+intLit(v)+")")
	}
	got, err := s.Exec(`SELECT id FROM t32 ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []int64{-2147483648, -100, -5, 0, 3, 100, 2147483647}
	if len(got.Rows) != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), len(got.Rows))
	}
	for i, row := range got.Rows {
		if row[0].Int != want[i] {
			t.Fatalf("ORDER BY INT32 not numeric at %d: got %v want %v (full: %+v)", i, row[0].Int, want[i], got.Rows)
		}
	}

	// Same check for INT8 (the narrowest width, most sensitive to a
	// sign-bit-flip mistake) and INT64 (widest).
	execOK(t, s, `CREATE TABLE t8 (id INT8 PRIMARY KEY)`)
	for _, v := range []int{-1, 1, -128, 127, 0} {
		execOK(t, s, "INSERT INTO t8 (id) VALUES ("+intLit(v)+")")
	}
	got8, err := s.Exec(`SELECT id FROM t8 ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want8 := []int64{-128, -1, 0, 1, 127}
	for i, row := range got8.Rows {
		if row[0].Int != want8[i] {
			t.Fatalf("ORDER BY INT8 not numeric at %d: got %v want %v", i, row[0].Int, want8[i])
		}
	}

	execOK(t, s, `CREATE TABLE t64 (id INT64 PRIMARY KEY)`)
	for _, v := range []string{"-9223372036854775808", "-1", "0", "1", "9223372036854775807"} {
		execOK(t, s, "INSERT INTO t64 (id) VALUES ("+v+")")
	}
	got64, err := s.Exec(`SELECT id FROM t64 ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want64 := []int64{-9223372036854775808, -1, 0, 1, 9223372036854775807}
	for i, row := range got64.Rows {
		if row[0].Int != want64[i] {
			t.Fatalf("ORDER BY INT64 not numeric at %d: got %v want %v", i, row[0].Int, want64[i])
		}
	}
}

func intLit(v int) string {
	neg := v < 0
	if neg {
		v = -v
	}
	if v == 0 {
		return "0"
	}
	var b []byte
	for v > 0 {
		b = append([]byte{byte('0' + v%10)}, b...)
		v /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// TestIntOverflowRejection covers narrowing-assignment and literal-fit
// rejection: an out-of-range value errors rather than silently wrapping
// (docs/design-datatypes.md D2).
func TestIntOverflowRejection(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, a INT8)`)

	if _, err := s.Exec(`INSERT INTO t (id, a) VALUES (1, 128)`); err == nil {
		t.Fatal("expected 128 to overflow INT8")
	}
	if _, err := s.Exec(`INSERT INTO t (id, a) VALUES (1, -129)`); err == nil {
		t.Fatal("expected -129 to overflow INT8")
	}
	execOK(t, s, `INSERT INTO t (id, a) VALUES (1, 127)`)
	execOK(t, s, `INSERT INTO t (id, a) VALUES (2, -128)`)

	// Non-whole DECIMAL cannot coerce into an int column.
	execOK(t, s, `CREATE TABLE t2 (id INT64 PRIMARY KEY, a INT32)`)
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t2 (id, a) VALUES (1, $1)`,
		[]Param{{Value: types.DecimalValue(mustDecimal(t, "3.5"), types.Type{Kind: types.KindDecimal})}}); err == nil {
		t.Fatal("expected fractional DECIMAL to be rejected coercing to INT32")
	}
	// A whole-valued DECIMAL with a fractional scale (3.0) must coerce fine.
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t2 (id, a) VALUES (1, $1)`,
		[]Param{{Value: types.DecimalValue(mustDecimal(t, "3.0"), types.Type{Kind: types.KindDecimal})}}); err != nil {
		t.Fatal(err)
	}

	// Out-of-range DECIMAL -> INT32.
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t2 (id, a) VALUES (2, $1)`,
		[]Param{{Value: types.DecimalValue(mustDecimal(t, "99999999999"), types.Type{Kind: types.KindDecimal})}}); err == nil {
		t.Fatal("expected out-of-range DECIMAL to be rejected coercing to INT32")
	}
}

func mustDecimal(t *testing.T, s string) types.Decimal {
	t.Helper()
	d, err := types.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

// TestIntArithmeticPromotesToDecimal covers the arithmetic-overflow design
// decision: +,-,*,/ over int operands promote to arbitrary-precision DECIMAL
// (never overflows mid-operation), and unary minus does the same.
func TestIntArithmeticPromotesToDecimal(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, a INT8)`)
	execOK(t, s, `INSERT INTO t (id, a) VALUES (1, 127)`)

	// 127 + 127 = 254, far beyond INT8's range, but must NOT wrap/error:
	// the result is DECIMAL.
	got, err := s.Exec(`SELECT a + a FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindDecimal || got.Rows[0][0].Dec.String() != "254" {
		t.Fatalf("expected DECIMAL 254, got %+v", got.Rows[0][0])
	}

	got2, err := s.Exec(`SELECT -a FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Rows[0][0].Dec.String() != "-127" {
		t.Fatalf("expected -127, got %+v", got2.Rows[0][0])
	}

	// Comparison across int width and DECIMAL literal both directions.
	cnt, err := s.Exec(`SELECT COUNT(*) FROM t WHERE a = 127`)
	if err != nil {
		t.Fatal(err)
	}
	if cnt.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("expected 1 matching row, got %s", cnt.Rows[0][0].Dec.String())
	}
}

// TestIntAggregatePromotion covers SUM/AVG/MIN/MAX over int columns: SUM/AVG
// promote to DECIMAL (so accumulation cannot overflow even summing many
// near-max INT8 values), MIN/MAX stay in the column's own int kind.
func TestIntAggregatePromotion(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, a INT8)`)
	for i, v := range []int{120, 120, 120, -100, 0} {
		execOK(t, s, "INSERT INTO t (id, a) VALUES ("+intLit(i+1)+", "+intLit(v)+")")
	}
	// SUM and AVG are queried separately: this repo's aggregate accumulator
	// (internal/executor/aggregate/hash.go) shares one running sum/count
	// between a SUM spec and an AVG spec on the same column, double-counting
	// when both appear in one query — a pre-existing bug reproducible with
	// plain DECIMAL columns too (confirmed unrelated to D2), out of scope
	// here. MIN/MAX are unaffected (each keeps independent state) and are
	// checked together with SUM below.
	got, err := s.Exec(`SELECT SUM(a), MIN(a), MAX(a) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	// Sum = 120*3 - 100 + 0 = 260, well beyond INT8 range: must not overflow.
	if row[0].Dec.String() != "260" {
		t.Fatalf("expected SUM=260, got %s", row[0].Dec.String())
	}
	if row[1].Int != -100 || row[1].Typ.Kind != types.KindInt8 {
		t.Fatalf("expected MIN=-100 as INT8, got %+v", row[1])
	}
	if row[2].Int != 120 || row[2].Typ.Kind != types.KindInt8 {
		t.Fatalf("expected MAX=120 as INT8, got %+v", row[2])
	}

	gotAvg, err := s.Exec(`SELECT AVG(a) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if gotAvg.Rows[0][0].Dec.String() != "52.000000" {
		t.Fatalf("expected AVG=52.000000, got %s", gotAvg.Rows[0][0].Dec.String())
	}
}

// TestIntGroupBy covers GROUP BY on an int column (index-key hashing path).
func TestIntGroupBy(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, grp INT16)`)
	for i, g := range []int{1, 1, 2, -1, -1, -1} {
		execOK(t, s, "INSERT INTO t (id, grp) VALUES ("+intLit(i+1)+", "+intLit(g)+")")
	}
	got, err := s.Exec(`SELECT grp, COUNT(*) FROM t GROUP BY grp ORDER BY grp ASC`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("expected 3 groups, got %d: %+v", len(got.Rows), got.Rows)
	}
	if got.Rows[0][0].Int != -1 || got.Rows[0][1].Dec.String() != "3" {
		t.Fatalf("unexpected first group: %+v", got.Rows[0])
	}
	if got.Rows[1][0].Int != 1 || got.Rows[1][1].Dec.String() != "2" {
		t.Fatalf("unexpected second group: %+v", got.Rows[1])
	}
	if got.Rows[2][0].Int != 2 || got.Rows[2][1].Dec.String() != "1" {
		t.Fatalf("unexpected third group: %+v", got.Rows[2])
	}
}

// TestIntForeignKey covers int-typed FK columns (unlike BLOB/VECTOR/JSON,
// ints are ordinary FK-eligible scalars).
func TestIntForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (id INT32 PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id INT64 PRIMARY KEY, parent_id INT32 NOT NULL REFERENCES parent(id))`)
	execOK(t, s, `INSERT INTO parent (id) VALUES (1)`)
	execOK(t, s, `INSERT INTO child (id, parent_id) VALUES (1, 1)`)
	if _, err := s.Exec(`INSERT INTO child (id, parent_id) VALUES (2, 999)`); err == nil {
		t.Fatal("expected FK violation for unknown parent_id")
	}
}

// TestIntEncryptedClient confirms ENCRYPTED CLIENT works over every int
// width end to end (server-side opaque-ciphertext path, plaintext rejected).
func TestIntEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE secrets (id STRING PRIMARY KEY, amount INT32 ENCRYPTED CLIENT NOT NULL)`)

	fieldKey := clientenc.Key{ID: "k1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 9
	}
	provider := executorFieldKeys{key: fieldKey}
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "secrets", "amount", types.Int32Value(-4200))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, amount) VALUES ('1', $1)`,
		[]Param{{Value: types.Int32Value(-4200)}}); err == nil {
		t.Fatal("server accepted plaintext for ENCRYPTED CLIENT INT32 column")
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, amount) VALUES ('1', $1)`,
		[]Param{{Value: types.StringValue(ciphertext)}}); err != nil {
		t.Fatal(err)
	}

	row, err := s.Exec(`SELECT amount FROM secrets WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	sealed := row.Rows[0][0].Str
	decrypted, err := clientenc.Decrypt(context.Background(), provider, "app", "secrets", "amount", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted.Typ.Kind != types.KindInt32 || decrypted.Int != -4200 {
		t.Fatalf("decrypted mismatch: %+v", decrypted)
	}
}
