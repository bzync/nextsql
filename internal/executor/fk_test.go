package executor

import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestFKInsertRequiresParent(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	if _, err := s.Exec(`INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("missing parent: %v", err)
	}
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	got := execOK(t, s, `SELECT id FROM children`)
	if len(got.Rows) != 1 {
		t.Fatalf("rows %d", len(got.Rows))
	}
}

func TestFKMatchSimpleNullSkips(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING REFERENCES parents (id))`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', NULL)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c2', 'p1')`)
	execOK(t, s, `UPDATE children SET parent_id = NULL WHERE id = 'c2'`)
}

func TestFKDeleteRestrict(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY, note STRING)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE RESTRICT)`)
	execOK(t, s, `INSERT INTO parents (id, note) VALUES ('p1', 'x')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("RESTRICT delete: %v", err)
	}
	execOK(t, s, `UPDATE parents SET note = 'y' WHERE id = 'p1'`)
	if _, err := s.Exec(`UPDATE parents SET id = 'p2' WHERE id = 'p1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("parent key update: %v", err)
	}
	execOK(t, s, `DELETE FROM children WHERE id = 'c1'`)
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
}

func TestFKChildUpdateAndUniqueRef(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY, email STRING NOT NULL)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_email ON parents (email)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, email STRING NOT NULL REFERENCES parents (email))`)
	execOK(t, s, `INSERT INTO parents (id, email) VALUES ('p1', 'a@x'), ('p2', 'b@x')`)
	execOK(t, s, `INSERT INTO children (id, email) VALUES ('c1', 'a@x')`)
	if _, err := s.Exec(`UPDATE children SET email = 'no@x' WHERE id = 'c1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("missing unique parent: %v", err)
	}
	execOK(t, s, `UPDATE children SET email = 'b@x' WHERE id = 'c1'`)
	if _, err := s.Exec(`UPDATE parents SET email = 'z@x' WHERE id = 'p2'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("unique parent update: %v", err)
	}
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p2'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("unique parent delete: %v", err)
	}
}

func TestFKSameTxnOverlayChild(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `BEGIN`)
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("same-txn inbound: %v", err)
	}
	execOK(t, s, `ROLLBACK`)
	if _, err := s.Exec(`SELECT * FROM parents`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("rolled back: %v", err)
	}
}

func TestFKSnapshotDeleteSeesCommittedChild(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t2, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	seen := execOK(t, t1, `SELECT id FROM children`)
	if len(seen.Rows) != 0 {
		t.Fatalf("SNAPSHOT must not see T2 child: %d", len(seen.Rows))
	}
	if _, err := t1.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("T1 DELETE must see committed child: %v", err)
	}
	execOK(t, t1, `ROLLBACK`)
}

func TestFKSnapshotInsertSeesDeletedParent(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t2, `DELETE FROM parents WHERE id = 'p1'`)
	seen := execOK(t, t1, `SELECT id FROM parents`)
	if len(seen.Rows) != 1 {
		t.Fatalf("SNAPSHOT must still see parent: %d", len(seen.Rows))
	}
	if _, err := t1.Exec(`INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("T1 INSERT must not attach to deleted parent: %v", err)
	}
	execOK(t, t1, `ROLLBACK`)
}

func TestFKSnapshotOverlappingLocks(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id))`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t2, `BEGIN SNAPSHOT`)

	inserted := make(chan struct{})
	delErr := make(chan error, 1)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-inserted
		_, err := t1.Exec(`DELETE FROM parents WHERE id = 'p1'`)
		delErr <- err
	}()
	if _, err := t2.Exec(`INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`); err != nil {
		t.Fatal(err)
	}
	close(inserted)
	deadline := time.After(2 * time.Second)
	select {
	case <-time.After(30 * time.Millisecond):
	case err := <-delErr:
		t.Fatalf("DELETE returned before child commit: %v", err)
	case <-deadline:
		t.Fatal("timeout before child commit")
	}
	execOK(t, t2, `COMMIT`)
	select {
	case err := <-delErr:
		if !nerr.HasCode(err, nerr.ForeignKey) {
			t.Fatalf("overlapping DELETE: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("DELETE did not finish")
	}
	wg.Wait()
	_ = t1.abort()
}

func TestFKTenantCases(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE customers (
		tenant_id UUID NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (tenant_id, id)
	)`)
	execOK(t, s, `CREATE TABLE orders (
		tenant_id UUID NOT NULL,
		id STRING NOT NULL,
		customer_id STRING NOT NULL,
		PRIMARY KEY (tenant_id, id),
		FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id)
	)`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `INSERT INTO customers (id) VALUES ('cust')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o1', 'cust')`)
	if _, err := s.Exec(`INSERT INTO orders (id, customer_id) VALUES ('o2', 'nope')`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("missing parent: %v", err)
	}
	if _, err := s.Exec(`DELETE FROM customers WHERE id = 'cust'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("same-tenant child: %v", err)
	}

	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	if _, err := s.Exec(`INSERT INTO orders (id, customer_id) VALUES ('o3', 'cust')`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("other-tenant parent: %v", err)
	}
	execOK(t, s, `INSERT INTO customers (id) VALUES ('cust')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o3', 'cust')`)

	execOK(t, s, `RESET TENANT`)
	if _, err := s.Exec(`DELETE FROM customers WHERE id = 'cust'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("unbound delete with children: %v", err)
	}
	res := execOK(t, s, `SELECT tenant_id, id FROM customers`)
	if len(res.Rows) != 2 {
		t.Fatalf("customers %d", len(res.Rows))
	}
}

func TestFKBoundDeleteGlobalParentOtherTenantChild(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (
		id STRING PRIMARY KEY,
		tenant_id UUID NOT NULL,
		parent_id STRING NOT NULL REFERENCES parents (id)
	)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("bound delete of global parent with other-tenant child: %v", err)
	}
	execOK(t, s, `RESET TENANT`)
	got := execOK(t, s, `SELECT id FROM parents`)
	if len(got.Rows) != 1 {
		t.Fatalf("parent must survive: %d", len(got.Rows))
	}
	kids := execOK(t, s, `SELECT id FROM children`)
	if len(kids.Rows) != 1 {
		t.Fatalf("child must survive: %d", len(kids.Rows))
	}
}

func TestFKDeleteCascade(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1'), ('p2')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1'), ('c3', 'p2')`)
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
	got := execOK(t, s, `SELECT id FROM children`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "c3" {
		t.Fatalf("cascade delete: %+v", valuesOf(got))
	}
	left := execOK(t, s, `SELECT id FROM parents`)
	if len(left.Rows) != 1 || left.Rows[0][0].Str != "p2" {
		t.Fatalf("parents: %+v", valuesOf(left))
	}
}

func TestFKUpdateCascade(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON UPDATE CASCADE)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1')`)
	execOK(t, s, `UPDATE parents SET id = 'p2' WHERE id = 'p1'`)
	got := execOK(t, s, `SELECT id, parent_id FROM children`)
	if len(got.Rows) != 2 {
		t.Fatalf("rows %d", len(got.Rows))
	}
	for _, row := range got.Rows {
		if row[1].Str != "p2" {
			t.Fatalf("child FK not rewritten: %+v", row)
		}
	}
}

func TestFKSetNull(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING REFERENCES parents (id) ON DELETE SET NULL ON UPDATE SET NULL)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1'), ('p2')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p2')`)
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
	got := execOK(t, s, `SELECT id, parent_id FROM children WHERE id = 'c1'`)
	if len(got.Rows) != 1 || !got.Rows[0][1].Null {
		t.Fatalf("SET NULL delete: %+v", got.Rows)
	}
	execOK(t, s, `UPDATE parents SET id = 'p3' WHERE id = 'p2'`)
	got = execOK(t, s, `SELECT parent_id FROM children WHERE id = 'c2'`)
	if len(got.Rows) != 1 || !got.Rows[0][0].Null {
		t.Fatalf("SET NULL update: %+v", got.Rows)
	}
}

func TestFKSetDefaultUsesNullNotLiveValue(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (
		id STRING PRIMARY KEY,
		parent_id STRING NOT NULL DEFAULT 'p0' REFERENCES parents (id) ON DELETE SET DEFAULT ON UPDATE SET DEFAULT
	)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p0'), ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
	got := execOK(t, s, `SELECT parent_id FROM children WHERE id = 'c1'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "p0" {
		t.Fatalf("SET DEFAULT must apply Null default, not keep live key: %+v", got.Rows)
	}
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p2')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c2', 'p2')`)
	execOK(t, s, `UPDATE parents SET id = 'p3' WHERE id = 'p2'`)
	got = execOK(t, s, `SELECT parent_id FROM children WHERE id = 'c2'`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "p0" {
		t.Fatalf("SET DEFAULT update: %+v", got.Rows)
	}
}

func TestFKSetDefaultStillMatchingParentFails(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (
		id STRING PRIMARY KEY,
		parent_id STRING NOT NULL DEFAULT 'p1' REFERENCES parents (id) ON DELETE SET DEFAULT
	)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.ForeignKey) {
		t.Fatalf("default still names deleted parent: %v", err)
	}
	got := execOK(t, s, `SELECT id FROM children`)
	if len(got.Rows) != 1 {
		t.Fatalf("child must survive failed SET DEFAULT: %d", len(got.Rows))
	}
}

func TestFKCascadeRecursive(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE g (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE p (id STRING PRIMARY KEY, g_id STRING NOT NULL REFERENCES g (id) ON DELETE CASCADE)`)
	execOK(t, s, `CREATE TABLE c (id STRING PRIMARY KEY, p_id STRING NOT NULL REFERENCES p (id) ON DELETE CASCADE)`)
	execOK(t, s, `INSERT INTO g (id) VALUES ('g1')`)
	execOK(t, s, `INSERT INTO p (id, g_id) VALUES ('p1', 'g1')`)
	execOK(t, s, `INSERT INTO c (id, p_id) VALUES ('c1', 'p1')`)
	execOK(t, s, `DELETE FROM g WHERE id = 'g1'`)
	if got := execOK(t, s, `SELECT id FROM p`); len(got.Rows) != 0 {
		t.Fatalf("parent leftover %+v", valuesOf(got))
	}
	if got := execOK(t, s, `SELECT id FROM c`); len(got.Rows) != 0 {
		t.Fatalf("child leftover %+v", valuesOf(got))
	}
}

func TestFKSelfRefUpdateCascade(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE emp (
		id STRING PRIMARY KEY,
		mgr STRING REFERENCES emp (id) ON UPDATE CASCADE
	)`)
	execOK(t, s, `INSERT INTO emp (id, mgr) VALUES ('a', NULL)`)
	execOK(t, s, `UPDATE emp SET mgr = 'a' WHERE id = 'a'`)
	execOK(t, s, `UPDATE emp SET id = 'b' WHERE id = 'a'`)
	got := execOK(t, s, `SELECT id, mgr FROM emp`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "b" || got.Rows[0][1].Str != "b" {
		t.Fatalf("self-ref ON UPDATE CASCADE: %+v", got.Rows)
	}
}

func TestFKSelfRefCascadeVisited(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE emp (
		id STRING PRIMARY KEY,
		mgr STRING REFERENCES emp (id) ON DELETE CASCADE
	)`)
	execOK(t, s, `INSERT INTO emp (id, mgr) VALUES ('a', NULL)`)
	execOK(t, s, `INSERT INTO emp (id, mgr) VALUES ('b', 'a')`)
	execOK(t, s, `UPDATE emp SET mgr = 'b' WHERE id = 'a'`)
	execOK(t, s, `DELETE FROM emp WHERE id = 'a'`)
	if got := execOK(t, s, `SELECT id FROM emp`); len(got.Rows) != 0 {
		t.Fatalf("self-ref cycle leftover %+v", valuesOf(got))
	}
}

func TestFKCascadeDepthCap(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t0 (id STRING PRIMARY KEY)`)
	for i := 1; i <= 9; i++ {
		execOK(t, s, `CREATE TABLE t`+strconv.Itoa(i)+` (id STRING PRIMARY KEY, p STRING NOT NULL REFERENCES t`+strconv.Itoa(i-1)+` (id) ON DELETE CASCADE)`)
	}
	execOK(t, s, `INSERT INTO t0 (id) VALUES ('x')`)
	for i := 1; i <= 9; i++ {
		execOK(t, s, `INSERT INTO t`+strconv.Itoa(i)+` (id, p) VALUES ('x', 'x')`)
	}
	if _, err := s.Exec(`DELETE FROM t0 WHERE id = 'x'`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("depth cap: %v", err)
	}
	if got := execOK(t, s, `SELECT id FROM t0`); len(got.Rows) != 1 {
		t.Fatalf("depth reject must roll back parent: %d", len(got.Rows))
	}
	if got := execOK(t, s, `SELECT id FROM t9`); len(got.Rows) != 1 {
		t.Fatalf("depth reject must roll back leaf: %d", len(got.Rows))
	}
}

func TestFKCascadeCapAbortsExplicitTxn(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	s.fkMaxTouched = 2
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1'), ('c3', 'p1')`)
	execOK(t, s, `BEGIN`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("row cap: %v", err)
	}
	if _, err := s.Exec(`COMMIT`); err == nil {
		t.Fatal("COMMIT must not persist a partial cascade")
	}
	if got := execOK(t, s, `SELECT id FROM parents`); len(got.Rows) != 1 {
		t.Fatalf("parent after aborted txn: %d", len(got.Rows))
	}
	if got := execOK(t, s, `SELECT id FROM children`); len(got.Rows) != 3 {
		t.Fatalf("children after aborted txn: %d", len(got.Rows))
	}
}

func TestFKCascadeRowCap(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	s.fkMaxTouched = 2
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1'), ('c3', 'p1')`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("row cap: %v", err)
	}
	if got := execOK(t, s, `SELECT id FROM parents`); len(got.Rows) != 1 {
		t.Fatalf("row reject must roll back parent: %d", len(got.Rows))
	}
	if got := execOK(t, s, `SELECT id FROM children`); len(got.Rows) != 3 {
		t.Fatalf("row reject must roll back children: %d", len(got.Rows))
	}
}

func TestFKCascadeTenantCheck(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE customers (
		tenant_id UUID NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (tenant_id, id)
	)`)
	execOK(t, s, `CREATE TABLE orders (
		tenant_id UUID NOT NULL,
		id STRING NOT NULL,
		customer_id STRING NOT NULL,
		PRIMARY KEY (tenant_id, id),
		FOREIGN KEY (tenant_id, customer_id) REFERENCES customers (tenant_id, id) ON DELETE CASCADE
	)`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `INSERT INTO customers (id) VALUES ('cust')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o1', 'cust')`)
	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	execOK(t, s, `INSERT INTO customers (id) VALUES ('cust')`)
	execOK(t, s, `INSERT INTO orders (id, customer_id) VALUES ('o2', 'cust')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `DELETE FROM customers WHERE id = 'cust'`)
	if got := execOK(t, s, `SELECT id FROM orders`); len(got.Rows) != 0 {
		t.Fatalf("tenant A orders leftover %d", len(got.Rows))
	}
	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	if got := execOK(t, s, `SELECT id FROM orders`); len(got.Rows) != 1 {
		t.Fatalf("tenant B order must survive %d", len(got.Rows))
	}
}

func TestFKCascadeBoundCannotTouchOtherTenant(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (
		id STRING PRIMARY KEY,
		tenant_id UUID NOT NULL,
		parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE
	)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `SET TENANT = '`+tenantA+`'`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	execOK(t, s, `SET TENANT = '`+tenantB+`'`)
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p1'`); err == nil {
		t.Fatal("bound session must not cascade into another tenant")
	}
	execOK(t, s, `RESET TENANT`)
	if got := execOK(t, s, `SELECT id FROM parents`); len(got.Rows) != 1 {
		t.Fatalf("parent must survive: %d", len(got.Rows))
	}
	if got := execOK(t, s, `SELECT id FROM children`); len(got.Rows) != 1 {
		t.Fatalf("other-tenant child must survive: %d", len(got.Rows))
	}
}

func TestFKSnapshotIndexedCascadeDelete(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (
		id STRING PRIMARY KEY,
		parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE
	)`)
	execOK(t, s, `CREATE UNIQUE INDEX ux_children_parent ON children (parent_id)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t2, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	execOK(t, t1, `DELETE FROM parents WHERE id = 'p1'`)
	execOK(t, t1, `COMMIT`)
	if got := execOK(t, s, `SELECT id FROM children`); len(got.Rows) != 0 {
		t.Fatalf("indexed CASCADE delete leftover heap %d", len(got.Rows))
	}
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c2', 'p1')`)
}

func TestFKSnapshotIndexedCascadeUpdate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (
		id STRING PRIMARY KEY,
		parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE ON UPDATE CASCADE
	)`)
	execOK(t, s, `CREATE INDEX ix_children_parent ON children (parent_id)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t2, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	execOK(t, t1, `UPDATE parents SET id = 'p2' WHERE id = 'p1'`)
	execOK(t, t1, `COMMIT`)
	got := execOK(t, s, `SELECT id, parent_id FROM children`)
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "c1" || got.Rows[0][1].Str != "p2" {
		t.Fatalf("indexed CASCADE update: %+v", got.Rows)
	}
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
	got = execOK(t, s, `SELECT id, parent_id FROM children`)
	if len(got.Rows) != 1 || got.Rows[0][1].Str != "p2" {
		t.Fatalf("stale FK index must not delete rewritten child: %+v", got.Rows)
	}
}

func TestFKSnapshotDeleteCascadesCommittedChild(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)

	t1 := db.Session()
	t2 := db.Session()
	execOK(t, t1, `BEGIN SNAPSHOT`)
	execOK(t, t2, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1')`)
	execOK(t, t1, `DELETE FROM parents WHERE id = 'p1'`)
	execOK(t, t1, `COMMIT`)
	if got := execOK(t, s, `SELECT id FROM children`); len(got.Rows) != 0 {
		t.Fatalf("probe snapshot must cascade T2 child: %d", len(got.Rows))
	}
}

func TestFKCascadeMetrics(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE children (id STRING PRIMARY KEY, parent_id STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE)`)
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p1')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c1', 'p1'), ('c2', 'p1')`)
	before := db.Metrics().Snapshot()
	execOK(t, s, `DELETE FROM parents WHERE id = 'p1'`)
	after := db.Metrics().Snapshot()
	if after.FKCascadeRows-before.FKCascadeRows < 2 {
		t.Fatalf("fk_cascade_rows %d -> %d", before.FKCascadeRows, after.FKCascadeRows)
	}
	s.fkMaxTouched = 1
	execOK(t, s, `INSERT INTO parents (id) VALUES ('p2')`)
	execOK(t, s, `INSERT INTO children (id, parent_id) VALUES ('c3', 'p2'), ('c4', 'p2')`)
	before = db.Metrics().Snapshot()
	if _, err := s.Exec(`DELETE FROM parents WHERE id = 'p2'`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("row cap: %v", err)
	}
	after = db.Metrics().Snapshot()
	if after.FKCascadeRejects-before.FKCascadeRejects < 1 {
		t.Fatalf("fk_cascade_reject %d -> %d", before.FKCascadeRejects, after.FKCascadeRejects)
	}
}
