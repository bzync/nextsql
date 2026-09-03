package executor

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/sql/types"
)

// TestBlobInsertSelectRoundTrip covers D1 (Datatype expansion track): BLOB
// column CRUD, the X'...' literal, embedded 0x00 / non-UTF-8 bytes, PK
// lookup, ORDER BY (byte-lexicographic order), and catalog persist/reopen.
func TestBlobInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE files (id UUID PRIMARY KEY DEFAULT UUID(), payload BLOB NOT NULL)`)

	// Non-UTF-8, embedded-NUL payload via the X'...' literal.
	raw := []byte{0x00, 0xFF, 0xFE, 'h', 'i', 0x00, 0xC3, 0x28} // 0xC3 0x28 is invalid UTF-8
	res, err := s.ExecContext(context.Background(),
		`INSERT INTO files (payload) VALUES ($1) RETURNING id`,
		[]Param{{Value: types.BlobValue(raw)}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 returned row, got %d", len(res.Rows))
	}
	id := res.Rows[0][0]

	got, err := s.ExecContext(context.Background(),
		`SELECT payload FROM files WHERE id = $1`, []Param{{Value: id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(got.Rows))
	}
	gotBytes := []byte(got.Rows[0][0].Str)
	if string(gotBytes) != string(raw) {
		t.Fatalf("round trip mismatch: got %x want %x", gotBytes, raw)
	}

	execOK(t, s, `INSERT INTO files (payload) VALUES (X'DEADBEEF')`)
	execOK(t, s, `INSERT INTO files (payload) VALUES (X'0000')`)
	execOK(t, s, `INSERT INTO files (payload) VALUES (X'')`)

	ordered, err := s.Exec(`SELECT payload FROM files ORDER BY payload ASC`)
	if err != nil {
		t.Fatal(err)
	}
	if len(ordered.Rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(ordered.Rows))
	}
	prev := ordered.Rows[0][0].Str
	for _, row := range ordered.Rows[1:] {
		cur := row[0].Str
		if cur < prev {
			t.Fatalf("ORDER BY on BLOB not byte-lexicographic: %x before %x", []byte(prev), []byte(cur))
		}
		prev = cur
	}
	// The empty blob must sort first.
	if ordered.Rows[0][0].Str != "" {
		t.Fatalf("expected empty BLOB first, got %x", []byte(ordered.Rows[0][0].Str))
	}

	cnt, err := s.Exec(`SELECT COUNT(*) FROM files WHERE payload = X'DEADBEEF'`)
	if err != nil {
		t.Fatal(err)
	}
	if cnt.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("expected exactly one DEADBEEF row, got %s", cnt.Rows[0][0].Dec.String())
	}

	// Persist/reopen: BLOB catalog type and stored bytes must survive a restart.
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	rs := reopened.Session()
	after, err := rs.ExecContext(context.Background(),
		`SELECT payload FROM files WHERE id = $1`, []Param{{Value: id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Rows) != 1 || after.Rows[0][0].Str != string(raw) {
		t.Fatalf("blob did not survive restart: %+v", after.Rows)
	}
}

// TestBlobHexLiteralAndCoercion covers the X'...' literal grammar and the
// deliberately-isolated STRING/TEXT<->BLOB coercion rule (hex text only, no
// byte-for-byte passthrough).
func TestBlobHexLiteralAndCoercion(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), b BLOB NOT NULL)`)

	if _, err := s.Exec(`INSERT INTO t (b) VALUES (X'ZZ')`); err == nil {
		t.Fatal("expected invalid hex literal to be rejected")
	}
	if _, err := s.Exec(`INSERT INTO t (b) VALUES (X'ABC')`); err == nil {
		t.Fatal("expected odd-length hex literal to be rejected")
	}

	// A plain string parameter targeting a BLOB column must be valid hex text.
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t (b) VALUES ($1)`, []Param{{Value: types.StringValue("not hex, raw text")}}); err == nil {
		t.Fatal("expected non-hex string to be rejected when coerced to BLOB")
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO t (b) VALUES ($1)`, []Param{{Value: types.StringValue("cafebabe")}}); err != nil {
		t.Fatal(err)
	}

	// BLOB -> STRING/TEXT formats as hex (CAST-shaped: BLOB never silently
	// becomes raw text).
	execOK(t, s, `INSERT INTO t (b) VALUES (X'CAFEBABE')`)
	got, err := s.Exec(`SELECT b FROM t WHERE b = X'CAFEBABE'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("expected 2 matching rows (literal + hex-text param), got %d", len(got.Rows))
	}
}

// TestBlobEncryptedClient confirms ENCRYPTED CLIENT BLOB columns work end to
// end: the server never accepts plaintext, and a correctly-encrypted
// envelope round-trips.
func TestBlobEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE secrets (id STRING PRIMARY KEY, payload BLOB ENCRYPTED CLIENT NOT NULL)`)

	fieldKey := clientenc.Key{ID: "k1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 7
	}
	provider := executorFieldKeys{key: fieldKey}
	plainBytes := []byte{0x01, 0x00, 0x02, 0xFF}
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "secrets", "payload", types.BlobValue(plainBytes))
	if err != nil {
		t.Fatal(err)
	}

	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, payload) VALUES ('1', $1)`,
		[]Param{{Value: types.BlobValue(plainBytes)}}); err == nil {
		t.Fatal("server accepted plaintext for ENCRYPTED CLIENT BLOB column")
	}
	if _, err := s.ExecContext(context.Background(),
		`INSERT INTO secrets (id, payload) VALUES ('1', $1)`,
		[]Param{{Value: types.StringValue(ciphertext)}}); err != nil {
		t.Fatal(err)
	}

	row, err := s.Exec(`SELECT payload FROM secrets WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	sealed := row.Rows[0][0].Str
	decrypted, err := clientenc.Decrypt(context.Background(), provider, "app", "secrets", "payload", sealed)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted.Typ.Kind != types.KindBlob || decrypted.Str != string(plainBytes) {
		t.Fatalf("decrypted mismatch: %+v", decrypted)
	}
}
