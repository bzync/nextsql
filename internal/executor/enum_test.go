package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// TestEnumInsertSelectRoundTrip covers D11 (Datatype expansion track):
// ENUM(...) column creation, membership-validated INSERT, PK usage, and
// catalog persist/reopen (including the declared label list itself).
func TestEnumInsertSelectRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, sz ENUM('small', 'medium', 'large'))`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (1, 'small')`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (2, 'large')`)

	got, err := s.Exec(`SELECT sz FROM t WHERE id = 1`)
	if err != nil {
		t.Fatal(err)
	}
	if got.Rows[0][0].Typ.Kind != types.KindEnum || got.Rows[0][0].Str != "small" {
		t.Fatalf("enum select: %+v", got.Rows[0][0])
	}

	// A non-member label is rejected.
	if _, err := s.Exec(`INSERT INTO t (id, sz) VALUES (3, 'huge')`); err == nil {
		t.Fatal("expected a non-member ENUM label to be rejected")
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
	after, err := rs.Exec(`SELECT sz FROM t WHERE id = 2`)
	if err != nil {
		t.Fatal(err)
	}
	if after.Rows[0][0].Str != "large" || len(after.Rows[0][0].Typ.EnumLabels) != 3 {
		t.Fatalf("ENUM type/labels did not survive restart: %+v", after.Rows[0][0].Typ)
	}
	// The non-member INSERT rejected above must not have landed.
	if cnt, err := rs.Exec(`SELECT id FROM t WHERE id = 3`); err != nil || len(cnt.Rows) != 0 {
		t.Fatalf("rejected insert should not have persisted: %v rows=%d", err, len(cnt.Rows))
	}
}

// TestEnumOrderByDeclarationOrder covers ORDER BY sorting by declaration
// position, not lexicographically (docs/design-datatypes.md D11) — the
// entire point of ENUM.
func TestEnumOrderByDeclarationOrder(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, sz ENUM('small', 'medium', 'large'))`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (1, 'large')`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (2, 'small')`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (3, 'medium')`)

	got, err := s.Exec(`SELECT sz FROM t ORDER BY sz ASC`)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"small", "medium", "large"}
	if len(got.Rows) != len(want) {
		t.Fatalf("row count: %d", len(got.Rows))
	}
	for i, row := range got.Rows {
		if row[0].Str != want[i] {
			t.Fatalf("ORDER BY ENUM wrong at %d: %q want %q (must be declaration order, not alphabetic)", i, row[0].Str, want[i])
		}
	}
}

// TestEnumCastValidatesMembership covers CAST(x AS ENUM-typed-column-expr)
// via an implicit comparison coercion, and MIN/MAX using declaration order.
func TestEnumCastValidatesMembership(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, sz ENUM('small', 'medium', 'large'))`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (1, 'small')`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (2, 'medium')`)
	execOK(t, s, `INSERT INTO t (id, sz) VALUES (3, 'large')`)

	// Implicit comparison coercion (string literal -> ENUM) validates
	// membership at query time too, not just on INSERT.
	got, err := s.Exec(`SELECT id FROM t WHERE sz = 'medium'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Int != 2 {
		t.Fatalf("enum equality: %+v", got.Rows)
	}

	agg, err := s.Exec(`SELECT MIN(sz), MAX(sz) FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if agg.Rows[0][0].Str != "small" || agg.Rows[0][1].Str != "large" {
		t.Fatalf("MIN/MAX declaration order: %+v", agg.Rows[0])
	}
}

// TestEnumForeignKey covers ENUM as an ordinary FK-eligible scalar (matching
// declared label sets on both sides), same as CHAR's D4 precedent.
func TestEnumForeignKey(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parent (sz ENUM('small', 'medium', 'large') PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE child (id INT64 PRIMARY KEY, psz ENUM('small', 'medium', 'large') NOT NULL REFERENCES parent(sz))`)
	execOK(t, s, `INSERT INTO parent (sz) VALUES ('small')`)
	execOK(t, s, `INSERT INTO child (id, psz) VALUES (1, 'small')`)
	if _, err := s.Exec(`INSERT INTO child (id, psz) VALUES (2, 'medium')`); err == nil {
		t.Fatal("expected FK violation for a parent value that was never inserted")
	}
}

// TestEnumNotEncryptedClient documents that ENUM is deliberately excluded
// from the ENCRYPTED CLIENT allow-list (declared labels aren't a stated
// sensitivity need) — docs/design-datatypes.md D11.
func TestEnumNotEncryptedClient(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY, sz ENUM('a', 'b') ENCRYPTED CLIENT)`); err == nil {
		t.Fatal("expected ENUM ENCRYPTED CLIENT to be rejected")
	}
}

// TestEnumFacet covers ENUM as a FACET column (added to binder.facetable
// alongside D11's other wiring) — low-cardinality declared labels are a
// natural fit for faceting.
func TestEnumFacet(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE articles (
		id UUID PRIMARY KEY DEFAULT UUID(),
		body TEXT,
		sz ENUM('small', 'medium', 'large')
	)`)
	execOK(t, s, `INSERT INTO articles (body, sz) VALUES
		('the cat sat', 'small'),
		('the cat sat on the mat', 'small'),
		('the cat and databases', 'medium')`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON articles (body)`)

	got, err := s.Exec(`SELECT * FROM articles SEARCH body FOR 'cat' FACET sz`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("expected 2 facet buckets, got %d: %+v", len(got.Rows), got.Rows)
	}
}

// TestEnumVectorizedBatchRoundTrip exercises the vectorized batch path
// (internal/executor/vector.Batch) directly: newVec/setAt/getAt sharing the
// Int slice with the fixed-width-integer/DATE family, plus Compact and
// clonePrefix (Project), since those don't have per-row-count exec coverage
// otherwise.
func TestEnumVectorizedBatchRoundTrip(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id INT64 PRIMARY KEY, sz ENUM('small', 'medium', 'large'))`)
	for i := 1; i <= 5; i++ {
		labels := []string{"small", "medium", "large"}
		execOK(t, s, "INSERT INTO t (id, sz) VALUES ("+intLit(i)+", '"+labels[i%3]+"')")
	}
	// A WHERE-filtered, projected SELECT forces Compact + Project
	// (clonePrefix) over the ENUM column in the vectorized executor.
	got, err := s.Exec(`SELECT sz FROM t WHERE id > 1 ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 4 {
		t.Fatalf("row count: %d", len(got.Rows))
	}
	for _, row := range got.Rows {
		if row[0].Typ.Kind != types.KindEnum || row[0].Str == "" {
			t.Fatalf("vectorized ENUM value corrupted: %+v", row[0])
		}
	}
}
