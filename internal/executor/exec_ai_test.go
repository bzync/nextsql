package executor

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestAIDefaultInsertAndRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b')`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES (AI(), 'c')`)
	res := execOK(t, s, `SELECT id, n FROM t ORDER BY id`)
	if len(res.Rows) != 3 {
		t.Fatalf("rows %d", len(res.Rows))
	}
	for i, want := range []string{"1", "2", "3"} {
		if res.Rows[i][0].Dec.String() != want || res.Rows[i][1].Str != string(rune('a'+i)) {
			t.Fatalf("row %d %+v", i, res.Rows[i])
		}
	}
	execOK(t, s, `INSERT INTO t (id, n) VALUES (10, 'explicit')`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('after')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	res = execOK(t, s, `SELECT n FROM t WHERE id = 11`)
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "after" {
		t.Fatalf("after restart %+v", res.Rows)
	}
	execOK(t, s, `INSERT INTO t (n) VALUES ('next')`)
	res = execOK(t, s, `SELECT id FROM t WHERE n = 'next'`)
	if len(res.Rows) != 1 || res.Rows[0][0].Dec.String() != "12" {
		t.Fatalf("next after restart %+v", res.Rows)
	}
}

func TestAIRollbackReuses(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('x')`)
	execOK(t, s, `ROLLBACK`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('y')`)
	res := execOK(t, s, `SELECT id, n FROM t`)
	if len(res.Rows) != 1 || res.Rows[0][0].Dec.String() != "1" || res.Rows[0][1].Str != "y" {
		t.Fatalf("%+v", res.Rows)
	}
}

func TestAIRejectsBadTypesAndSelect(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (id UUID PRIMARY KEY DEFAULT AI())`); err == nil {
		t.Fatal("expected UUID DEFAULT AI() to fail")
	}
	if _, err := s.Exec(`CREATE TABLE t (id DECIMAL(10,2) PRIMARY KEY DEFAULT AI())`); err == nil {
		t.Fatal("expected scaled DECIMAL DEFAULT AI() to fail")
	}
	execOK(t, s, `CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a')`)
	if _, err := s.Exec(`SELECT AI() FROM t`); err == nil {
		t.Fatal("expected SELECT AI() to fail")
	}
	execOK(t, s, `CREATE TABLE u (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	if _, err := s.Exec(`INSERT INTO u (id, n) VALUES (AI(), 'x')`); err == nil {
		t.Fatal("expected AI() on a UUID column to fail")
	}
}

func TestAIPrecisionOverflow(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(1,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	for i := 1; i <= 9; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (n) VALUES ('%d')`, i))
	}
	if _, err := s.Exec(`INSERT INTO t (n) VALUES ('overflow')`); err == nil {
		t.Fatal("expected AI() overflow")
	}
}

func TestAIConcurrentInserts(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(18,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	const n = 8
	var wg sync.WaitGroup
	errCh := make(chan error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			sess := db.Session()
			_, err := sess.Exec(fmt.Sprintf(`INSERT INTO t (n) VALUES ('%d')`, i))
			errCh <- err
		}(i)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatal(err)
		}
	}
	res := execOK(t, s, `SELECT COUNT(*) FROM t`)
	if len(res.Rows) != 1 || res.Rows[0][0].Dec.String() != "8" {
		t.Fatalf("count %+v", res.Rows)
	}
	ids := execOK(t, s, `SELECT id FROM t ORDER BY id`)
	if len(ids.Rows) != n {
		t.Fatalf("ids %d", len(ids.Rows))
	}
	seen := map[string]struct{}{}
	for _, row := range ids.Rows {
		s := row[0].Dec.String()
		if _, ok := seen[s]; ok {
			t.Fatalf("duplicate id %s", s)
		}
		seen[s] = struct{}{}
	}
}

func TestAIAddColumn(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('a'), ('b')`)
	execOK(t, s, `ALTER TABLE t ADD seq DECIMAL(18,0) NOT NULL DEFAULT AI()`)
	res := execOK(t, s, `SELECT seq FROM t ORDER BY seq`)
	if len(res.Rows) != 2 || res.Rows[0][0].Dec.String() != "1" || res.Rows[1][0].Dec.String() != "2" {
		t.Fatalf("%+v", res.Rows)
	}
	execOK(t, s, `INSERT INTO t (n) VALUES ('c')`)
	got := execOK(t, s, `SELECT seq FROM t WHERE n = 'c'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "3" {
		t.Fatalf("%+v", got.Rows)
	}
}

func TestAIHasCodeOnOverflow(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(1,0) PRIMARY KEY DEFAULT AI(), n STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO t (n) VALUES ('1')`)
	execOK(t, s, `INSERT INTO t (id, n) VALUES (9, 'nine')`)
	_, err := s.Exec(`INSERT INTO t (n) VALUES ('x')`)
	if err == nil || !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("overflow: %v", err)
	}
}
