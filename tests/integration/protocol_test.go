package integration

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

// lastClientCAPEM is the PEM from the most recent startTLSServer call.
var lastClientCAPEM []byte

func startTLSServer(t *testing.T) (addr string, clientTLS *tls.Config) {
	t.Helper()
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if _, err := crypto.CreateKeyFile(keyPath, 1); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.LoadProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(filepath.Join(dir, "nextsql.db"), keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })

	users, err := auth.Create(filepath.Join(dir, "nextsql.users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}

	certPath := filepath.Join(dir, "tls.crt")
	keyFile := filepath.Join(dir, "tls.key")
	if err := security.WriteSelfSigned(certPath, keyFile, "localhost"); err != nil {
		t.Fatal(err)
	}
	srvTLS, err := security.ServerTLS(certPath, keyFile)
	if err != nil {
		t.Fatal(err)
	}
	pem, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	lastClientCAPEM = pem
	clientTLS, err = security.ClientTLSFromPEM("localhost", pem)
	if err != nil {
		t.Fatal(err)
	}

	srv := protocol.NewServer(db, users)
	srv.TLS = srvTLS
	ctx, cancel := context.WithCancel(context.Background())
	tasks, err := executor.StartTaskRuntime(ctx, db, executor.TaskRuntimeConfig{Workers: 1, Batch: 4, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetTaskRuntime(tasks)
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = srv.Close() })
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe(ctx, "127.0.0.1:0") }()
	deadline := time.Now().Add(2 * time.Second)
	for srv.Addr() == nil && time.Now().Before(deadline) {
		select {
		case err := <-serveErr:
			t.Fatalf("server start: %v", err)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	if srv.Addr() == nil {
		t.Fatal("server did not start")
	}
	return srv.Addr().String(), clientTLS
}

func openApp(t *testing.T, addr string, tlsCfg *tls.Config) *nextsql.Conn {
	t.Helper()
	conn, err := nextsql.Open(nextsql.Config{
		Address:  addr,
		Database: "production",
		User:     "app",
		Password: "s3cret",
		TLS:      tlsCfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestNativeSubscribeOverTLSAndPreparedCancellation(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	if _, err := conn.Exec(context.Background(), `CREATE TABLE wire_changes (id STRING PRIMARY KEY, note STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), `INSERT INTO wire_changes (id, note) VALUES ('one', 'first')`); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	rows, err := conn.Query(ctx, `SUBSCRIBE TO wire_changes`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatalf("first change: %v", rows.Err())
	}
	vals := rows.Values()
	if len(vals) != 15 || vals[0].Str != "INSERT" || vals[3].Str != "wire_changes" || vals[11].Str == "" {
		t.Fatalf("change row=%+v", vals)
	}
	token := vals[11].Str
	cancel()
	if rows.Next() || !nerr.HasCode(rows.Err(), nerr.Canceled) {
		t.Fatalf("stream cancellation err=%v", rows.Err())
	}
	if err := rows.Close(); !nerr.HasCode(err, nerr.Canceled) {
		t.Fatalf("close after cancellation=%v", err)
	}

	if _, err := conn.Exec(context.Background(), `INSERT INTO wire_changes (id, note) VALUES ('two', 'second')`); err != nil {
		t.Fatal(err)
	}
	stmt, err := conn.Prepare(context.Background(), `SUBSCRIBE TO wire_changes AFTER `+token)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithCancel(context.Background())
	rows, err = stmt.Query(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatalf("prepared resumed change: %v", rows.Err())
	}
	vals = rows.Values()
	if len(vals) != 15 || vals[11].Str == token || vals[3].Str != "wire_changes" {
		t.Fatalf("prepared change row=%+v", vals)
	}
	cancel()
	if rows.Next() || !nerr.HasCode(rows.Err(), nerr.Canceled) {
		t.Fatalf("prepared stream cancellation err=%v", rows.Err())
	}
	_ = rows.Close()
	if err := stmt.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDriverPhase5OverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)

	if _, err := conn.Exec(context.Background(), `CREATE TABLE items (
		id UUID PRIMARY KEY DEFAULT UUID(),
		sku STRING NOT NULL,
		qty DECIMAL(10,0)
	)`); err != nil {
		t.Fatal(err)
	}
	ins, err := conn.Exec(context.Background(), `INSERT INTO items (sku, qty) VALUES ('A-1', 3), ('B-2', 9)`)
	if err != nil {
		t.Fatal(err)
	}
	if ins.Affected != 2 {
		t.Fatalf("inserted %d", ins.Affected)
	}
	res, err := conn.Exec(context.Background(), `SELECT sku, qty FROM items WHERE sku = $1`, types.StringValue("B-2"))
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "B-2" {
		t.Fatalf("%+v", res.Rows)
	}
	if _, err := conn.Exec(context.Background(), `CREATE INDEX ix_sku ON items (sku)`); err != nil {
		t.Fatal(err)
	}
	upd, err := conn.Exec(context.Background(), `UPDATE items SET qty = $1 WHERE sku = $2`, types.DecimalValue(mustDec(t, "12"), types.Type{Kind: types.KindDecimal, Precision: 10}), types.StringValue("A-1"))
	if err != nil {
		t.Fatal(err)
	}
	if upd.Affected != 1 {
		t.Fatalf("updated %d", upd.Affected)
	}
	if _, err := conn.Exec(context.Background(), `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), `DELETE FROM items WHERE sku = 'B-2'`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(context.Background(), `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	got, err := conn.Exec(context.Background(), `SELECT sku FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("after rollback %d", len(got.Rows))
	}

	st, err := conn.Prepare(context.Background(), `SELECT sku FROM items WHERE sku = $1`)
	if err != nil {
		t.Fatal(err)
	}
	pres, err := st.Exec(context.Background(), types.StringValue("A-1"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pres.Rows) != 1 || pres.Rows[0][0].Str != "A-1" {
		t.Fatalf("prepared %+v", pres.Rows)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDriverP18SQLOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `CREATE TABLE p18 (
		id STRING PRIMARY KEY,
		k STRING,
		n DECIMAL(10,0)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO p18 (id, k, n) VALUES ('1', 'a', 1), ('2', 'b', 2), ('3', NULL, 3)`); err != nil {
		t.Fatal(err)
	}

	got, err := conn.Exec(ctx, `SELECT DISTINCT k FROM p18 ORDER BY k`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 || got.Rows[0][0].Str != "a" || got.Rows[1][0].Str != "b" || !got.Rows[2][0].Null {
		t.Fatalf("DISTINCT %+v", got.Rows)
	}
	got, err = conn.Exec(ctx, `SELECT k, COUNT(*) AS total FROM p18 GROUP BY k HAVING total >= 1 ORDER BY k`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 3 {
		t.Fatalf("HAVING %+v", got.Rows)
	}
	got, err = conn.Exec(ctx, `SELECT CASE WHEN k IS NULL THEN 'missing' ELSE k END FROM p18 WHERE id = '3'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "missing" {
		t.Fatalf("CASE %+v", got.Rows)
	}
	got, err = conn.Exec(ctx, `SELECT k FROM p18 WHERE id = '1' UNION SELECT k FROM p18 WHERE id = '2'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 {
		t.Fatalf("UNION %+v", got.Rows)
	}
	got, err = conn.Exec(ctx, `WITH c AS (SELECT k FROM p18 WHERE k IS NOT NULL) SELECT k FROM c WHERE k = 'a'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "a" {
		t.Fatalf("CTE %+v", got.Rows)
	}
	got, err = conn.Exec(ctx, `SELECT id FROM p18 WHERE EXISTS (SELECT id FROM p18 i WHERE i.k = p18.k AND i.id <> p18.id)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 0 {
		t.Fatalf("EXISTS %+v", got.Rows)
	}
	got, err = conn.Exec(ctx, `SELECT id, ROW_NUMBER() OVER (ORDER BY id) FROM p18 WHERE k IS NOT NULL ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 2 || got.Rows[0][1].Dec.String() != "1" {
		t.Fatalf("window %+v", got.Rows)
	}

	if _, err := conn.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	up, err := conn.Exec(ctx, `UPSERT INTO p18 (id, k, n) VALUES ('1', 'a', 9) RETURNING n`)
	if err != nil {
		t.Fatal(err)
	}
	if len(up.Rows) != 1 {
		t.Fatalf("UPSERT RETURNING %+v", up.Rows)
	}
	if _, err := conn.Exec(ctx, `ROLLBACK`); err != nil {
		t.Fatal(err)
	}
	got, err = conn.Exec(ctx, `SELECT n FROM p18 WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("rollback UPSERT %+v", got.Rows)
	}

	st, err := conn.Prepare(ctx, `SELECT k FROM p18 WHERE id = $1 UNION SELECT k FROM p18 WHERE k = $2`)
	if err != nil {
		t.Fatal(err)
	}
	pres, err := st.Exec(ctx, types.StringValue("1"), types.StringValue("b"))
	if err != nil {
		t.Fatal(err)
	}
	if len(pres.Rows) != 2 {
		t.Fatalf("prepared UNION %+v", pres.Rows)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestDriverWorkflowOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `CREATE TABLE jobs (id STRING PRIMARY KEY, state STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `CREATE WORKFLOW put_job(id STRING, state STRING) AS BEGIN INSERT INTO jobs (id, state) VALUES ($id, $state); END`); err != nil {
		t.Fatal(err)
	}
	stmt, err := conn.Prepare(ctx, `RUN WORKFLOW put_job($1, $2)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	res, err := stmt.Exec(ctx, types.StringValue("j1"), types.StringValue("queued"))
	if err != nil {
		t.Fatal(err)
	}
	if res.Affected != 1 {
		t.Fatalf("affected=%d", res.Affected)
	}
	got, err := conn.Exec(ctx, `SELECT state FROM jobs WHERE id = 'j1'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 || got.Rows[0][0].Str != "queued" {
		t.Fatalf("rows=%v", got.Rows)
	}
}

func TestDriverScheduledTaskOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	for _, sql := range []string{
		`CREATE TABLE scheduled_jobs (id STRING PRIMARY KEY)`,
		`CREATE WORKFLOW put_scheduled(id STRING) AS BEGIN INSERT INTO scheduled_jobs (id) VALUES ($id); END`,
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	fireAt := time.Now().UTC().Add(50 * time.Millisecond)
	if _, err := conn.Exec(ctx, `CREATE SCHEDULE tls_once AT '`+fireAt.Format(time.RFC3339Nano)+`' RUN WORKFLOW put_scheduled('tls-task')`); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	var taskID string
	for time.Now().Before(deadline) {
		shown, err := conn.Exec(ctx, `SHOW TASKS LIMIT 1`)
		if err != nil {
			t.Fatal(err)
		}
		if len(shown.Rows) == 1 {
			taskID = shown.Rows[0][0].Str
			if shown.Rows[0][1].Str == "SUCCEEDED" {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if taskID == "" {
		t.Fatal("scheduled task was not observable")
	}
	got, err := conn.Exec(ctx, `SELECT id FROM scheduled_jobs WHERE id = 'tls-task'`)
	if err != nil || len(got.Rows) != 1 {
		t.Fatalf("scheduled rows=%+v err=%v", got, err)
	}
	if cancelled, err := conn.Exec(ctx, `CANCEL TASK '`+taskID+`'`); err != nil || cancelled.Affected != 0 {
		t.Fatalf("terminal cancellation affected=%d err=%v", cancelled.Affected, err)
	}
}

func TestDriverTriggerOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	for _, sql := range []string{
		`CREATE TABLE source (id STRING PRIMARY KEY)`,
		`CREATE TABLE audit (id STRING PRIMARY KEY)`,
		`CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO audit (id) VALUES ($id); END`,
		`CREATE TRIGGER record_insert AFTER INSERT ON source FOR EACH ROW RUN WORKFLOW record(NEW.id)`,
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	stmt, err := conn.Prepare(ctx, `INSERT INTO source (id) VALUES ($1)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	if _, err := stmt.Exec(ctx, types.StringValue("tls-trigger")); err != nil {
		t.Fatal(err)
	}
	got, err := conn.Exec(ctx, `SELECT id FROM audit WHERE id = 'tls-trigger'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Rows) != 1 {
		t.Fatalf("rows=%v", got.Rows)
	}
}

func TestDriverStreamAndCancel(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	if _, err := conn.Exec(context.Background(), `CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	var ins strings.Builder
	ins.WriteString("INSERT INTO t (n) VALUES ")
	for i := 0; i < 400; i++ {
		if i > 0 {
			ins.WriteString(",")
		}
		ins.WriteString("('x')")
	}
	if _, err := conn.Exec(context.Background(), ins.String()); err != nil {
		t.Fatal(err)
	}
	rows, err := conn.Query(context.Background(), `SELECT n FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	var n int
	for rows.Next() {
		n++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if n != 400 {
		t.Fatalf("streamed %d", n)
	}

	rows, err = conn.Query(context.Background(), `SELECT n FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if !rows.Next() {
		t.Fatal("expected at least one row before cancel")
	}
	if err := conn.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
	}
	cerr := rows.Err()
	_ = rows.Close()
	if cerr == nil {
		t.Fatal("expected stream error after cancel")
	}
}

func TestDriverRejectsKeyURL(t *testing.T) {
	_, err := nextsql.Open(nextsql.Config{
		Address:  "nextsql://app:pw@localhost/db?key=secret",
		User:     "app",
		Password: "x",
		TLS:      &tls.Config{MinVersion: tls.VersionTLS13},
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("got %v", err)
	}
}

func TestAuthFailure(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	_, err := nextsql.Open(nextsql.Config{
		Address:  addr,
		User:     "app",
		Password: "wrong",
		TLS:      tlsCfg,
	})
	if !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("got %v", err)
	}
}

func mustDec(t *testing.T, s string) types.Decimal {
	t.Helper()
	d, err := types.ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}
