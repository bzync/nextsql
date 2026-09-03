package executor

import (
	"fmt"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/wal"
)

func onlineScalar(t *testing.T, s *Session, sql string) string {
	t.Helper()
	res := execOK(t, s, sql)
	if len(res.Rows) != 1 || len(res.Rows[0]) != 1 {
		t.Fatalf("%s: want 1x1 result, got %d rows", sql, len(res.Rows))
	}
	v := res.Rows[0][0]
	if v.Dec.Coef != nil {
		return v.Dec.String()
	}
	return v.Str
}

func onlineSortNumeric(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && onlineLessNumeric(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

func onlineLessNumeric(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}

// assertIndexMatchesHeap walks the whole table via the heap and checks that an
// index-backed equality lookup for every distinct value returns exactly the
// matching rows.
func assertIndexMatchesHeap(t *testing.T, s *Session, distinct int) {
	t.Helper()
	ref := make(map[int][]string)
	all := execOK(t, s, `SELECT v, id FROM t`)
	for _, r := range all.Rows {
		v := int(r[0].Dec.Coef.Int64())
		ref[v] = append(ref[v], r[1].Dec.String())
	}
	total := 0
	bad := false
	for v := 0; v < distinct; v++ {
		res := execOK(t, s, fmt.Sprintf(`SELECT id FROM t WHERE v = %d ORDER BY id`, v))
		got := make([]string, 0, len(res.Rows))
		for _, r := range res.Rows {
			got = append(got, r[0].Dec.String())
		}
		total += len(got)
		want := ref[v]
		onlineSortNumeric(want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("v=%d: index ids %v != heap ids %v", v, got, want)
			bad = true
		}
	}
	if total != len(all.Rows) {
		t.Errorf("index has %d entries, heap has %d rows", total, len(all.Rows))
		bad = true
	}
	if bad {
		t.FailNow()
	}
}

func TestRebuildIndexOnlineConcurrentWrites(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL)`)
	const rows = 1200
	const distinct = 32
	for i := 1; i <= rows; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, i, i%distinct))
	}
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)

	var stop atomic.Bool
	var writes atomic.Int64
	var wg sync.WaitGroup
	wg.Add(3)
	for w := 0; w < 3; w++ {
		go func(seed int) {
			defer wg.Done()
			ws := db.Session()
			r := uint64(seed*2654435761 + 1)
			tolerant := func(err error) bool {
				return nerr.HasCode(err, nerr.Serialization) || nerr.HasCode(err, nerr.Deadlock) ||
					nerr.HasCode(err, nerr.AlreadyExists) || nerr.HasCode(err, nerr.NotFound)
			}
			for !stop.Load() {
				r = r*6364136223846793005 + 1442695040888963407
				val := int(r>>17) % distinct
				var q string
				switch r >> 61 & 3 {
				case 0: // insert into a churn range above the seeded rows
					q = fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, rows+1+int(r>>33)%400, val)
				case 1: // delete from the churn range
					q = fmt.Sprintf(`DELETE FROM t WHERE id = %d`, rows+1+int(r>>33)%400)
				default: // update a stable row
					q = fmt.Sprintf(`UPDATE t SET v = %d WHERE id = %d`, val, int(r>>33)%rows+1)
				}
				if _, err := ws.Exec(q); err != nil {
					if tolerant(err) {
						continue
					}
					t.Errorf("concurrent write %q: %v", q, err)
					return
				}
				writes.Add(1)
			}
		}(w + 1)
	}

	for writes.Load() < 40 {
		time.Sleep(time.Millisecond)
	}
	execOK(t, s, `REBUILD INDEX ix_v ONLINE`)
	// Keep writing against the freshly swapped index for a bit.
	target := writes.Load() + 150
	for writes.Load() < target {
		time.Sleep(time.Millisecond)
	}
	stop.Store(true)
	wg.Wait()

	if err := db.LastReclaimError(); err != nil {
		t.Fatalf("reclaim error: %v", err)
	}

	plan := execOK(t, s, `EXPLAIN SELECT id FROM t WHERE v = 7`)
	if !explainHas(plan, "IndexScan") || !explainHas(plan, "ix_v") {
		t.Fatalf("expected IndexScan on ix_v:\n%v", plan.Rows)
	}
	assertIndexMatchesHeap(t, s, distinct)

	snap := db.Metrics().Snapshot()
	if snap.IndexRebuilds < 1 || snap.IndexRebuildRows < 1 {
		t.Fatalf("rebuild metrics: %+v", snap)
	}
}

func TestRebuildIndexOnlineRestartAfterSwap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL)`)
	for i := 1; i <= 200; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, i, i%7))
	}
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)
	old := indexesByName(t, db, "t")["ix_v"].Meta
	execOK(t, s, `REBUILD INDEX ix_v ONLINE`)
	neu := indexesByName(t, db, "t")["ix_v"].Meta
	if neu == old || neu == 0 {
		t.Fatalf("meta did not change: old=%d new=%d", old, neu)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := onlineScalar(t, s, `SELECT COUNT(*) FROM t WHERE v = 3`); got != "29" {
		t.Fatalf("post-restart index count for v=3 = %s, want 29", got)
	}
	if got := indexesByName(t, db, "t")["ix_v"].Meta; got != neu {
		t.Fatalf("restart meta=%d want=%d", got, neu)
	}
}

func TestRebuildIndexOnlineCrashKeepsOldIndex(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL)`)
	for i := 1; i <= 200; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, i, i%7))
	}
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)
	old := indexesByName(t, db, "t")["ix_v"].Meta

	db.Eng.SetCrash(wal.PointDuringIndexBuild)
	if _, err := s.Exec(`REBUILD INDEX ix_v ONLINE`); !wal.IsCrash(err) {
		t.Fatalf("expected crash, got %v", err)
	}
	db.Eng.Kill()

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	if got := indexesByName(t, db, "t")["ix_v"].Meta; got != old {
		t.Fatalf("crash meta=%d want old=%d", got, old)
	}
	if got := onlineScalar(t, s, `SELECT COUNT(*) FROM t WHERE v = 3`); got != "29" {
		t.Fatalf("post-crash index count for v=3 = %s, want 29", got)
	}
	// A fresh online rebuild now succeeds and produces a consistent index.
	execOK(t, s, `REBUILD INDEX ix_v ONLINE`)
	if got := onlineScalar(t, s, `SELECT COUNT(*) FROM t WHERE v = 3`); got != "29" {
		t.Fatalf("after retry index count for v=3 = %s, want 29", got)
	}
}

func TestRebuildIndexOnlineRejections(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL, emb VECTOR<F32,3>, body TEXT)`)
	execOK(t, s, `INSERT INTO t (id, v, emb, body) VALUES (1, 1, (1,0,0), 'hello world')`)
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)
	execOK(t, s, `CREATE VECTOR INDEX ix_emb ON t (emb) USING HNSW`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_body ON t (body)`)

	if _, err := s.Exec(`REBUILD INDEX ix_emb ONLINE`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("vector: %v", err)
	}
	if _, err := s.Exec(`REBUILD INDEX ix_body ONLINE`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("fulltext: %v", err)
	}

	execOK(t, s, `BEGIN`)
	if _, err := s.Exec(`REBUILD INDEX ix_v ONLINE`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("in-txn: %v", err)
	}
	execOK(t, s, `ROLLBACK`)

	execOK(t, s, `CREATE TABLE p (id DECIMAL(10,0), region STRING, v DECIMAL(6,0) NOT NULL, PRIMARY KEY (region, id)) PARTITION BY LIST (region) (PARTITION pa VALUES IN ('a'), PARTITION pb VALUES IN ('b'))`)
	execOK(t, s, `CREATE INDEX ix_pv ON p (v)`)
	if _, err := s.Exec(`REBUILD INDEX ix_pv ONLINE`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("partitioned: %v", err)
	}
}

func TestRebuildIndexOnlineBlocksConflictingDDL(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL)`)
	for i := 1; i <= 400; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, i, i%9))
	}
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)

	key := idxKey("t", "ix_v")
	if !db.armOnlineBuild(key, "t", "ix_v") {
		t.Fatal("arm failed")
	}
	for _, q := range []string{
		`DROP INDEX ix_v`,
		`REBUILD INDEX ix_v`,
		`ALTER TABLE t ADD COLUMN w DECIMAL(6,0)`,
		`DROP TABLE t`,
	} {
		if _, err := db.Session().Exec(q); !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("%s: expected unavailable, got %v", q, err)
		}
	}
	db.abortOnlineBuild(key)
	execOK(t, db.Session(), `DROP INDEX ix_v`)
}
