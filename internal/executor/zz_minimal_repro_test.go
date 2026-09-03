package executor

import "testing"

func TestScratchRangeLiveIncludesTombstones(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE mt (id DECIMAL(10,0) PRIMARY KEY)`)
	execOK(t, s, `INSERT INTO mt (id) VALUES (1)`)
	execOK(t, s, `INSERT INTO mt (id) VALUES (2)`)
	execOK(t, s, `DELETE FROM mt WHERE id = 1`)
	// No concurrency at all here: single session, single connection, no
	// other live snapshot -- soleSnapshot() should be true.
	res := execOK(t, s, `SELECT COUNT(*) FROM mt`)
	t.Logf("COUNT(*) after delete (no other sessions ever existed) = %v", res.Rows)
	if res.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("COUNT(*) = %v, want 1 (row 1 was deleted)", res.Rows)
	}
	all := execOK(t, s, `SELECT id FROM mt`)
	t.Logf("SELECT id FROM mt = %v", all.Rows)
	if len(all.Rows) != 1 {
		t.Fatalf("SELECT id FROM mt = %v, want 1 row", all.Rows)
	}
}
