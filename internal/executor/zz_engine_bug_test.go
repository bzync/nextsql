package executor

import (
	"fmt"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

// Reproduces a storage-engine transaction-rollback bug independent of any
// online rebuild: a transaction that rolls back after touching many B-tree
// pages restores those pages to a stale pre-image, discarding row versions
// other transactions committed to the same pages in the meantime.
func TestEngineRollbackClobbersCommittedNeighbors(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE t (id DECIMAL(10,0) PRIMARY KEY, v DECIMAL(6,0) NOT NULL)`)
	const rows = 1500
	const distinct = 40
	for i := 1; i <= rows; i++ {
		execOK(t, s, fmt.Sprintf(`INSERT INTO t (id, v) VALUES (%d, %d)`, i, i%distinct))
	}
	execOK(t, s, `CREATE INDEX ix_v ON t (v)`)

	var stop atomic.Bool
	var wg sync.WaitGroup
	wg.Add(7)
	for w := 0; w < 6; w++ {
		go func(seed int) {
			defer wg.Done()
			ws := db.Session()
			r := uint64(seed*2654435761 + 1)
			for !stop.Load() {
				r = r*6364136223846793005 + 1442695040888963407
				val := int(r>>17) % distinct
				id := int(r>>33)%rows + 1
				if _, err := ws.Exec(fmt.Sprintf(`UPDATE t SET v = %d WHERE id = %d`, val, id)); err != nil {
					if nerr.HasCode(err, nerr.Serialization) || nerr.HasCode(err, nerr.Deadlock) {
						continue
					}
					t.Errorf("update: %v", err)
					return
				}
			}
		}(w + 1)
	}
	go func() {
		defer wg.Done()
		ls := db.Session()
		for !stop.Load() {
			if _, err := ls.Exec(`BEGIN`); err != nil {
				continue
			}
			for id := 1; id <= 300; id++ {
				if _, err := ls.Exec(fmt.Sprintf(`UPDATE t SET v = v WHERE id = %d`, id)); err != nil {
					break
				}
			}
			ls.Exec(`ROLLBACK`)
		}
	}()

	for k := 0; k < 60; k++ {
		execOK(t, s, `SELECT COUNT(*) FROM t WHERE v = 3`)
	}
	stop.Store(true)
	wg.Wait()

	ref := map[int][]string{}
	all := execOK(t, s, `SELECT v, id FROM t`)
	for _, rr := range all.Rows {
		ref[int(rr[0].Dec.Coef.Int64())] = append(ref[int(rr[0].Dec.Coef.Int64())], rr[1].Dec.String())
	}
	bad := 0
	for v := 0; v < distinct; v++ {
		res := execOK(t, s, fmt.Sprintf(`SELECT id FROM t WHERE v = %d ORDER BY id`, v))
		got := []string{}
		for _, rr := range res.Rows {
			got = append(got, rr[0].Dec.String())
		}
		want := ref[v]
		onlineSortNumeric(want)
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Logf("v=%d: index=%v heap=%v", v, got, want)
			bad++
		}
	}
	if bad > 0 {
		t.Fatalf("%d values inconsistent between ix_v and heap", bad)
	}
}
