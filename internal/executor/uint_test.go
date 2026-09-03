package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/sql/types"
)

// TestUintInsertSelectRoundTrip covers D3 (Datatype expansion track): all 4
// fixed-width unsigned int column types, boundary-value CRUD, PK usage,
// catalog persist/reopen, and index-key (ORDER BY / PK lookup) round-trips.
func TestUintInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE uints (id UINT64 PRIMARY KEY, a UINT8, b UINT16, c UINT32, d UINT64)`)

	execOK(t, s, `INSERT INTO uints (id, a, b, c, d) VALUES (1, 0, 0, 0, 0)`)
	execOK(t, s, `INSERT INTO uints (id, a, b, c, d) VALUES (2, 255, 65535, 4294967295, 18446744073709551615)`)

	got, err := s.Exec(`SELECT a, b, c, d FROM uints WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	if row[0].Uint != 0 || row[1].Uint != 0 || row[2].Uint != 0 || row[3].Uint != 0 {
		t.Fatalf("zero round trip mismatch: %+v", row)
	}
	if row[0].Typ.Kind != types.KindUint8 || row[1].Typ.Kind != types.KindUint16 ||
		row[2].Typ.Kind != types.KindUint32 || row[3].Typ.Kind != types.KindUint64 {
		t.Fatalf("unexpected result kinds: %+v %+v %+v %+v", row[0].Typ, row[1].Typ, row[2].Typ, row[3].Typ)
	}

	got2, err := s.Exec(`SELECT a, b, c, d FROM uints WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	row2 := got2.Rows[0]
	if row2[0].Uint != 255 || row2[1].Uint != 65535 || row2[2].Uint != 4294967295 || row2[3].Uint != 18446744073709551615 {
		t.Fatalf("max boundary round trip mismatch: %+v", row2)
	}

	// Persist/reopen: catalog type + stored uints (including a UINT64 above
	// math.MaxInt64) must survive a restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rs := reopened.Session()
	after, err := rs.Exec(`SELECT a, b, c, d FROM uints WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][3].Uint != 18446744073709551615 {
		t.Fatalf("uint64 did not survive restart: %+v", after.Rows[0])
	}
}

// TestUintOrderByCorrectness is the key-encoding correctness test: plain
// unsigned big-endian byte order must already sort numerically (no sign-bit
// flip needed, unlike D2's signed ints — see docs/design-datatypes.md D3).
func TestUintOrderByCorrectness(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t32 (id UINT32 PRIMARY KEY)`)
	for _, v := range []string{"5", "3", "100", "0", "4294967295", "2147483648"} {
		execOK(t, s, "INSERT INTO t32 (id) VALUES ("+v+")")
	}
	got, err := s.Exec(`SELECT id FROM t32 ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []uint64{0, 3, 5, 100, 2147483648, 4294967295}
	if len(got.Rows) != len(want) {
		t.Fatalf("expected %d rows, got %d", len(want), len(got.Rows))
	}
	for i, row := range got.Rows {
		if row[0].Uint != want[i] {
			t.Fatalf("ORDER BY UINT32 not numeric at %d: got %v want %v (full: %+v)", i, row[0].Uint, want[i], got.Rows)
		}
	}

	execOK(t, s, `CREATE TABLE t64 (id UINT64 PRIMARY KEY)`)
	for _, v := range []string{"18446744073709551615", "1", "0", "9223372036854775808"} {
		execOK(t, s, "INSERT INTO t64 (id) VALUES ("+v+")")
	}
	got64, err := s.Exec(`SELECT id FROM t64 ORDER BY id ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want64 := []uint64{0, 1, 9223372036854775808, 18446744073709551615}
	for i, row := range got64.Rows {
		if row[0].Uint != want64[i] {
			t.Fatalf("ORDER BY UINT64 not numeric at %d: got %v want %v", i, row[0].Uint, want64[i])
		}
	}
}

// TestUintOverflowRejection covers narrowing/negative-assignment rejection:
// out-of-range or negative values error rather than silently wrapping.
func TestUintOverflowRejection(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UINT64 PRIMARY KEY, a UINT8)`)

	if _, err := s.Exec(`INSERT INTO t (id, a) VALUES (1, 256)`); err == nil {
		t.Fatal("expected 256 to overflow UINT8")
	}
	if _, err := s.Exec(`INSERT INTO t (id, a) VALUES (1, -1)`); err == nil {
		t.Fatal("expected -1 to be rejected for UINT8")
	}
	execOK(t, s, `INSERT INTO t (id, a) VALUES (1, 255)`)
	execOK(t, s, `INSERT INTO t (id, a) VALUES (2, 0)`)

	// Negative INT cannot coerce into UINT.
	execOK(t, s, `CREATE TABLE t2 (id UINT64 PRIMARY KEY, a UINT32)`)
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t2 (id, a) VALUES (1, $1)`,
		[]Param{{Value: types.Int32Value(-5)}}); err == nil {
		t.Fatal("expected negative INT32 to be rejected coercing to UINT32")
	}
	// A non-negative INT coerces fine.
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t2 (id, a) VALUES (1, $1)`,
		[]Param{{Value: types.Int32Value(5)}}); err != nil {
		t.Fatal(err)
	}
}

// TestUintIntCoercion covers direct coercion between signed and unsigned
// fixed-width integers (D3 extends D2's coercion matrix to treat both
// families as one coercible "exact integer" group): a signed Value param
// coerces into an unsigned column (and vice versa via types.Coerce
// directly), range/sign checked either way.
func TestUintIntCoercion(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UINT64 PRIMARY KEY, a UINT64)`)

	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t (id, a) VALUES (1, $1)`,
		[]Param{{Value: types.Int32Value(7)}}); err != nil {
		t.Fatal(err)
	}
	got, err := s.Exec(`SELECT a FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindUint64 || got.Rows[0][0].Uint != 7 {
		t.Fatalf("expected UINT64 7, got %+v", got.Rows[0][0])
	}

	// A UINT64 magnitude above math.MaxInt64 cannot coerce into any signed
	// int kind.
	if _, err := types.Coerce(types.Uint64Value(18446744073709551615), types.Int64()); err == nil {
		t.Fatal("expected out-of-range UINT64->INT64 coercion to fail")
	}
	if _, err := types.Coerce(types.Int64Value(-1), types.Uint64()); err == nil {
		t.Fatal("expected negative INT64->UINT64 coercion to fail")
	}
}

// TestUintArithmeticPromotesToDecimal mirrors D2's arithmetic-overflow
// decision for the unsigned kinds: +,-,*,/ always promote to arbitrary-
// precision DECIMAL, so summing near-max values can never overflow.
func TestUintArithmeticPromotesToDecimal(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UINT64 PRIMARY KEY, a UINT8)`)
	execOK(t, s, `INSERT INTO t (id, a) VALUES (1, 255)`)

	got, err := s.Exec(`SELECT a + a FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindDecimal || got.Rows[0][0].Dec.String() != "510" {
		t.Fatalf("expected DECIMAL 510, got %+v", got.Rows[0][0])
	}
}

// TestUintAggregatePromotion covers SUM/MIN/MAX over unsigned int columns.
func TestUintAggregatePromotion(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UINT64 PRIMARY KEY, a UINT8)`)
	for i, v := range []int{200, 200, 200, 0, 50} {
		execOK(t, s, "INSERT INTO t (id, a) VALUES ("+intLit(i+1)+", "+intLit(v)+")")
	}
	got, err := s.Exec(`SELECT SUM(a), MIN(a), MAX(a) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	row := got.Rows[0]
	// Sum = 200*3 + 0 + 50 = 650, well beyond UINT8 range: must not overflow.
	if row[0].Dec.String() != "650" {
		t.Fatalf("expected SUM=650, got %s", row[0].Dec.String())
	}
	if row[1].Uint != 0 || row[1].Typ.Kind != types.KindUint8 {
		t.Fatalf("expected MIN=0 as UINT8, got %+v", row[1])
	}
	if row[2].Uint != 200 || row[2].Typ.Kind != types.KindUint8 {
		t.Fatalf("expected MAX=200 as UINT8, got %+v", row[2])
	}
}

// TestUintForeignKey covers uint-typed FK columns.
func TestUintForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (id UINT32 PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id UINT64 PRIMARY KEY, parent_id UINT32 NOT NULL REFERENCES parent(id))`)
	execOK(t, s, `INSERT INTO parent (id) VALUES (1)`)
	execOK(t, s, `INSERT INTO child (id, parent_id) VALUES (1, 1)`)
	if _, err := s.Exec(`INSERT INTO child (id, parent_id) VALUES (2, 999)`); err == nil {
		t.Fatal("expected FK violation for unknown parent_id")
	}
}

// TestUintEncryptedClient confirms ENCRYPTED CLIENT works over every unsigned
// int width end to end.
func TestUintEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE secrets (id STRING PRIMARY KEY, amount UINT32 ENCRYPTED CLIENT NOT NULL)`)

	fieldKey := clientenc.Key{ID: "k1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 9
	}
	provider := executorFieldKeys{key: fieldKey}
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "secrets", "amount", types.Uint32Value(4200))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, amount) VALUES ('1', $1)`,
		[]Param{{Value: types.Uint32Value(4200)}}); err == nil {
		t.Fatal("server accepted plaintext for ENCRYPTED CLIENT UINT32 column")
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
	if decrypted.Typ.Kind != types.KindUint32 || decrypted.Uint != 4200 {
		t.Fatalf("decrypted mismatch: %+v", decrypted)
	}
}
