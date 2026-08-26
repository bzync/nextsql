package integration

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/migrate"
	"github.com/bzync/nextsql/internal/sql/types"
)

type connExecer struct{ c *nextsql.Conn }

func (e connExecer) Exec(ctx context.Context, sql string, params ...types.Value) (migrate.Result, error) {
	res, err := e.c.Exec(ctx, sql, params...)
	if err != nil {
		return migrate.Result{}, err
	}
	return migrate.Result{Columns: res.Columns, Rows: res.Rows, Affected: res.Affected}, nil
}

func TestMigrateUpOverNSQL(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	db := connExecer{conn}

	root := repoRoot(t)
	src := filepath.Join(root, "internal", "migrate", "testdata", "ok")
	dir := t.TempDir()
	ents, err := os.ReadDir(src)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range ents {
		b, err := os.ReadFile(filepath.Join(src, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	applied, err := migrate.Up(ctx, db, dir, migrate.UpOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied) != 3 {
		t.Fatalf("applied %v", applied)
	}
	res, err := conn.Exec(ctx, `SELECT name FROM customers`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str != "acme" {
		t.Fatalf("seed %+v %v", res, err)
	}
	ver, err := migrate.CurrentVersion(ctx, db)
	if err != nil || ver != "20260818120200" {
		t.Fatalf("version %q %v", ver, err)
	}
	again, err := migrate.Up(ctx, db, dir, migrate.UpOptions{})
	if err != nil || len(again) != 0 {
		t.Fatalf("second up %v %v", again, err)
	}
	rep, err := migrate.Status(ctx, db, dir)
	if err != nil || rep.Applied != 3 || rep.Pending != 0 || rep.Dirty {
		t.Fatalf("%+v %v", rep, err)
	}
}
