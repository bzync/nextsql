package integration

import (
	"context"
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

// A BOOL column over the native protocol: a bound BOOL parameter is stored in
// a declared BOOL column, filters by parameter, and comes back typed BOOL. The
// wire already carried BOOL values (every comparison result is one); what is
// new is a column that holds them, so the round trip is checked end to end
// through TLS and authentication rather than assumed from the codec.
func TestBoolColumnOverNativeProtocol(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `CREATE TABLE wire_flags (id INT64 PRIMARY KEY, active BOOL)`); err != nil {
		t.Fatal(err)
	}
	for i, v := range []types.Value{types.BoolValue(true), types.BoolValue(false), types.Null(types.Bool())} {
		if _, err := conn.Exec(ctx, `INSERT INTO wire_flags (id, active) VALUES ($1, $2)`,
			types.Int64Value(int64(i+1)), v); err != nil {
			t.Fatalf("insert %d: %v", i+1, err)
		}
	}
	res, err := conn.Exec(ctx, `SELECT id, active FROM wire_flags WHERE active = $1`, types.BoolValue(false))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Int != 2 {
		t.Fatalf("WHERE active = FALSE returned %+v, want id 2", res.Rows)
	}
	all, err := conn.Exec(ctx, `SELECT active FROM wire_flags ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(all.Rows) != 3 {
		t.Fatalf("%d rows, want 3", len(all.Rows))
	}
	for i, want := range []struct{ null, val bool }{{false, true}, {false, false}, {true, false}} {
		cell := all.Rows[i][0]
		if cell.Typ.Kind != types.KindBool || cell.Null != want.null || (!cell.Null && cell.Bool != want.val) {
			t.Fatalf("row %d = %+v, want BOOL %v (null=%v)", i+1, cell, want.val, want.null)
		}
	}
}
