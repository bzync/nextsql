package executor

import (
	"fmt"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func TestSeedKVStyleInsert(t *testing.T) {
	db, err := Create(t.TempDir()+"/nextsql.db", testKeys(t), 4096)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE kv (id STRING PRIMARY KEY, n DECIMAL(12,2) NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	decTy := types.Type{Kind: types.KindDecimal, Precision: 12, Scale: 2}
	const n = 25000
	if _, err := s.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	inTxn := 0
	for i := 0; i < n; i += 256 {
		end := i + 256
		if end > n {
			end = n
		}
		rows := make([][]types.Value, 0, end-i)
		for j := i; j < end; j++ {
			d, _ := types.ParseDecimal(fmt.Sprintf("%d", j))
			rows = append(rows, []types.Value{
				types.StringValue("k" + fmt.Sprintf("%d", j)),
				types.DecimalValue(d, decTy),
			})
		}
		if _, err := s.InsertRows("kv", rows); err != nil {
			t.Fatalf("at %d: %v", i, err)
		}
		inTxn += end - i
		if inTxn >= 4096 {
			if _, err := s.Exec(`COMMIT`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Exec(`BEGIN`); err != nil {
				t.Fatal(err)
			}
			inTxn = 0
		}
	}
	if _, err := s.Exec(`COMMIT`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE INDEX ix_kv_n ON kv (n)`); err != nil {
		t.Fatal(err)
	}
	got := execOK(t, s, `SELECT COUNT(*) FROM kv`)
	if got.Rows[0][0].Dec.String() != "25000" {
		t.Fatalf("count %v", got.Rows)
	}
}
