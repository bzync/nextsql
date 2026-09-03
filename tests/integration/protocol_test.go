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

func startTLSServer(t *testing.T, configure ...func(*protocol.Server)) (addr string, clientTLS *tls.Config) {
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
	for _, fn := range configure {
		fn(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pool, err := executor.NewTaskPool(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := executor.StartTaskRuntime(ctx, db, pool, executor.TaskRuntimeConfig{Batch: 4, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	srv.SetTaskRuntime(tasks)
	t.Cleanup(func() { _ = pool.Close() })
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

func TestDriverIdempotentMutationOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `CREATE TABLE wire_idempotency (id STRING PRIMARY KEY, note STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	const mutation = `INSERT INTO wire_idempotency (id, note) VALUES ($1, $2) RETURNING id, note`
	first, err := conn.ExecIdempotent(ctx, "wire-create-1", mutation, types.StringValue("1"), types.StringValue("once"))
	if err != nil {
		t.Fatal(err)
	}
	replay, err := conn.ExecIdempotent(ctx, "wire-create-1", mutation, types.StringValue("1"), types.StringValue("once"))
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Rows) != 1 || len(replay.Rows) != 1 || first.Rows[0][1].Str != "once" || replay.Rows[0][1].Str != "once" {
		t.Fatalf("idempotent wire results: first=%+v replay=%+v", first.Rows, replay.Rows)
	}
	count, err := conn.Exec(ctx, `SELECT COUNT(*) FROM wire_idempotency`)
	if err != nil {
		t.Fatal(err)
	}
	if len(count.Rows) != 1 || count.Rows[0][0].Dec.String() != "1" {
		t.Fatalf("idempotent wire duplicate: %+v", count.Rows)
	}
	if _, err := conn.ExecIdempotent(ctx, "wire-create-1", mutation, types.StringValue("2"), types.StringValue("different")); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("idempotent wire conflict: %v", err)
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

func TestDriverCronScheduleOverTLS(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	for _, sql := range []string{
		`CREATE TABLE cron_jobs (id STRING PRIMARY KEY)`,
		`CREATE WORKFLOW put_cron(id STRING) AS BEGIN INSERT INTO cron_jobs (id) VALUES ($id); END`,
	} {
		if _, err := conn.Exec(ctx, sql); err != nil {
			t.Fatal(err)
		}
	}
	// A five-field cron expression round-trips through parse, bind, and the
	// versioned catalog descriptor over the real wire protocol.
	if _, err := conn.Exec(ctx, `CREATE SCHEDULE cron_nightly CRON '30 3 * * 1-5' RUN WORKFLOW put_cron('nightly')`); err != nil {
		t.Fatalf("valid cron schedule rejected: %v", err)
	}
	// Idempotency check proves it is durably stored.
	if _, err := conn.Exec(ctx, `CREATE SCHEDULE cron_nightly CRON '30 3 * * 1-5' RUN WORKFLOW put_cron('nightly')`); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("re-create should conflict, got %v", err)
	}
	// An unsatisfiable expression fails closed at definition time.
	if _, err := conn.Exec(ctx, `CREATE SCHEDULE cron_bad CRON '0 0 30 2 *' RUN WORKFLOW put_cron('bad')`); err == nil {
		t.Fatal("unsatisfiable cron expression was accepted")
	}
	// A malformed expression is rejected too.
	if _, err := conn.Exec(ctx, `CREATE SCHEDULE cron_bad CRON 'every tuesday' RUN WORKFLOW put_cron('bad')`); err == nil {
		t.Fatal("malformed cron expression was accepted")
	}
	// Lifecycle works: DROP without IF EXISTS confirms the schedule existed.
	if _, err := conn.Exec(ctx, `ALTER SCHEDULE cron_nightly RENAME TO cron_weekday`); err != nil {
		t.Fatalf("alter cron schedule: %v", err)
	}
	if _, err := conn.Exec(ctx, `DROP SCHEDULE cron_weekday`); err != nil {
		t.Fatalf("drop cron schedule: %v", err)
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

// TestRealmMismatchRejected proves the M2-2 Hello.Realm field is actually
// enforced: a client selecting a realm other than the server's configured
// one is rejected, not silently connected to whatever the server has open.
// It uses a real, correct password to prove the rejection is the same
// generic Unauthorized "authentication failed" a wrong password would
// produce, not a distinguishing NotFound — the pre-auth realm-disclosure
// hardening: an unauthenticated peer must not be able to tell "wrong realm"
// apart from "wrong password" by response content.
func TestRealmMismatchRejected(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		srv.Realm = "tenant-a"
	})
	_, err := nextsql.Open(nextsql.Config{
		Address:  addr,
		Database: "production",
		User:     "app",
		Password: "s3cret",
		Realm:    "tenant-b",
		TLS:      tlsCfg,
	})
	if !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("got %v", err)
	}
}

// TestRealmMatchSucceeds proves a matching realm selection connects.
func TestRealmMatchSucceeds(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		srv.Realm = "tenant-a"
	})
	conn, err := nextsql.Open(nextsql.Config{
		Address:  addr,
		Database: "production",
		User:     "app",
		Password: "s3cret",
		Realm:    "tenant-a",
		TLS:      tlsCfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.Close()
}

// TestUnconfiguredClientSkipsRealmCheck is the regression test for the
// M2-2 compatibility guarantee: a client that never selects a realm
// connects successfully even against a server that hosts a named realm —
// the same way an old, pre-realm client would.
func TestUnconfiguredClientSkipsRealmCheck(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		srv.Realm = "tenant-a"
	})
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
	_ = conn.Close()
}

// TestDatabaseNameMismatchRejectedGenerically proves the legacy
// single-pinned-database flat precheck (the non-hosted, no-DatabaseManager
// case) no longer discloses a database-name mismatch pre-auth: it uses a
// real, correct password so the rejection can only be attributed to the
// wrong database name, and asserts it comes back as the same generic
// Unauthorized "authentication failed" a wrong password would produce, not
// a distinguishing NotFound — the pre-auth database-disclosure hardening,
// the same discipline TestRealmMismatchRejected/TestUnknownRealmStillRejectedCleanly
// apply to realm names.
func TestDatabaseNameMismatchRejectedGenerically(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		srv.Database = "prod-a"
	})
	_, err := nextsql.Open(nextsql.Config{
		Address:  addr,
		Database: "prod-b",
		User:     "app",
		Password: "s3cret",
		TLS:      tlsCfg,
	})
	if !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("got %v", err)
	}
}

func TestPerUserConnectionLimit(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessionsPerUser = 2
		srv.Limits = lim
	})
	open := func() (*nextsql.Conn, error) {
		return nextsql.Open(nextsql.Config{
			Address:  addr,
			Database: "production",
			User:     "app",
			Password: "s3cret",
			TLS:      tlsCfg,
		})
	}
	c1, err := open()
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer c1.Close()
	c2, err := open()
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer c2.Close()
	if _, err := open(); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("third connection: got %v, want Exhausted", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	// The server observes the closed connection asynchronously (its read loop
	// must unblock on the terminate frame/EOF before the per-user count is
	// decremented), so poll rather than assume same-instant visibility.
	deadline := time.Now().Add(2 * time.Second)
	var c3 *nextsql.Conn
	for {
		c3, err = open()
		if err == nil {
			break
		}
		if !nerr.HasCode(err, nerr.Exhausted) || time.Now().After(deadline) {
			t.Fatalf("connection after close: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer c3.Close()
}

// TestPerDatabaseConnectionLimit (P27's own last open exit-gate item)
// mirrors TestPerUserConnectionLimit exactly, proving MaxSessionsPerDatabase
// works the same way on a legacy single-database (no dbmanager) deployment,
// where it collapses to a finer-grained MaxSessions.
func TestPerDatabaseConnectionLimit(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessionsPerDatabase = 2
		srv.Limits = lim
	})
	open := func() (*nextsql.Conn, error) {
		return nextsql.Open(nextsql.Config{
			Address:  addr,
			Database: "production",
			User:     "app",
			Password: "s3cret",
			TLS:      tlsCfg,
		})
	}
	c1, err := open()
	if err != nil {
		t.Fatalf("first connection: %v", err)
	}
	defer c1.Close()
	c2, err := open()
	if err != nil {
		t.Fatalf("second connection: %v", err)
	}
	defer c2.Close()
	if _, err := open(); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("third connection: got %v, want Exhausted", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatalf("close first: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var c3 *nextsql.Conn
	for {
		c3, err = open()
		if err == nil {
			break
		}
		if !nerr.HasCode(err, nerr.Exhausted) || time.Now().After(deadline) {
			t.Fatalf("connection after close: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer c3.Close()
}

func TestTxnTimeoutAbortsOverLiveConnection(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.TxnTimeout = 50 * time.Millisecond
		srv.Limits = lim
	})
	conn := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := conn.Exec(ctx, `CREATE TABLE txn_timeout_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(ctx, `INSERT INTO txn_timeout_t (id) VALUES ('1')`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := conn.Exec(ctx, `INSERT INTO txn_timeout_t (id) VALUES ('2')`); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("statement after transaction timeout = %v, want Exhausted", err)
	}
	// The connection itself must stay usable (a fresh autocommit statement
	// works) — only the timed-out transaction was aborted, not the session.
	if _, err := conn.Exec(ctx, `INSERT INTO txn_timeout_t (id) VALUES ('3')`); err != nil {
		t.Fatalf("statement after forced abort: %v", err)
	}
}

// TestIdleTransactionTimeoutClosesOpenTransactionConnection proves
// idle_transaction_timeout_ms (protocol.Limits.IdleTxn) is a distinct bound
// from the general idle_timeout_ms: it applies only while a transaction is
// open, actively closing the connection via its own socket read deadline
// even though the client never sends another statement — while an ordinary
// idle connection with no open transaction survives well past IdleTxn,
// governed only by the much longer general Idle bound.
func TestIdleTransactionTimeoutClosesOpenTransactionConnection(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(s *protocol.Server) {
		lim := s.Limits
		lim.IdleTxn = 50 * time.Millisecond
		lim.Idle = 5 * time.Second
		s.Limits = lim
	})
	ctx := context.Background()

	plain := openApp(t, addr, tlsCfg)
	if _, err := plain.Exec(ctx, `CREATE TABLE idle_txn_plain_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := plain.Exec(ctx, `SELECT id FROM idle_txn_plain_t`); err != nil {
		t.Fatalf("idle connection with no open transaction was closed early: %v", err)
	}

	busy := openApp(t, addr, tlsCfg)
	if _, err := busy.Exec(ctx, `CREATE TABLE idle_txn_close_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `INSERT INTO idle_txn_close_t (id) VALUES ('1')`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if _, err := busy.Exec(ctx, `SELECT id FROM idle_txn_close_t`); err == nil {
		t.Fatal("expected the open-transaction connection to be closed once idle past IdleTxn")
	}
}

// TestIdleTransactionTimeoutReleasesLocksOnDisconnect proves the idle-in-
// transaction timeout actually reclaims the abandoned transaction's locks —
// not just the socket. Without Session.Abort wired into the connection
// teardown path, the torn-down connection's uncommitted INSERT would keep
// holding its exclusive key lock forever, and the second connection's insert
// of the same primary key below would block until lock_timeout_ms failed it
// Exhausted instead of succeeding.
func TestIdleTransactionTimeoutReleasesLocksOnDisconnect(t *testing.T) {
	addr, tlsCfg := startTLSServer(t, func(s *protocol.Server) {
		lim := s.Limits
		lim.IdleTxn = 50 * time.Millisecond
		s.Limits = lim
		s.DatabaseHandle().SetLockWaitTimeout(1 * time.Second)
	})
	ctx := context.Background()

	busy := openApp(t, addr, tlsCfg)
	if _, err := busy.Exec(ctx, `CREATE TABLE idle_txn_lock_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `INSERT INTO idle_txn_lock_t (id) VALUES ('1')`); err != nil {
		t.Fatal(err)
	}

	// Let the idle-in-transaction deadline fire and tear the connection down
	// without ever sending COMMIT/ROLLBACK.
	time.Sleep(250 * time.Millisecond)

	other := openApp(t, addr, tlsCfg)
	if _, err := other.Exec(ctx, `INSERT INTO idle_txn_lock_t (id) VALUES ('1')`); err != nil {
		t.Fatalf("insert after idle-in-transaction disconnect should succeed once the abandoned transaction's lock is released: %v", err)
	}
}

// TestClusterDrainOverLiveConnection wires DB.SetDrainFunc exactly as
// cmd/nextsqld/main.go does (via Server.DatabaseHandle()), then proves
// CLUSTER DRAIN issued over a live SQL connection actually drains the
// server: idle connections close promptly and new connections are refused.
func TestClusterDrainOverLiveConnection(t *testing.T) {
	var srv *protocol.Server
	addr, tlsCfg := startTLSServer(t, func(s *protocol.Server) {
		srv = s
		s.DrainTimeout = 2 * time.Second
		db := s.DatabaseHandle()
		db.SetDrainFunc(func(timeout time.Duration) {
			if timeout <= 0 {
				timeout = s.DrainTimeout
			}
			s.Drain(timeout)
		})
	})
	idle := openApp(t, addr, tlsCfg)
	admin := openApp(t, addr, tlsCfg)
	ctx := context.Background()

	if _, err := admin.Exec(ctx, `CLUSTER DRAIN WITH (TIMEOUT_MS = 500)`); err != nil {
		t.Fatalf("CLUSTER DRAIN: %v", err)
	}
	_ = srv

	deadline := time.Now().Add(1 * time.Second)
	for {
		if _, err := idle.Exec(ctx, `SELECT 1`); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle connection was not closed by CLUSTER DRAIN")
		}
		time.Sleep(5 * time.Millisecond)
	}

	if _, err := nextsql.Open(nextsql.Config{
		Address:  addr,
		Database: "production",
		User:     "app",
		Password: "s3cret",
		TLS:      tlsCfg,
	}); err == nil {
		t.Fatal("new connection succeeded after CLUSTER DRAIN began")
	}
}

// TestClusterMaintenanceOverLiveConnection proves CLUSTER MAINTENANCE
// ENABLE/DISABLE issued over a live SQL connection actually gates write
// traffic server-side (not just inside a single in-process Session, unlike
// the executor-package unit test), while leaving reads and the connection
// itself untouched — the key behavioral difference from CLUSTER DRAIN.
func TestClusterMaintenanceOverLiveConnection(t *testing.T) {
	addr, tlsCfg := startTLSServer(t)
	admin := openApp(t, addr, tlsCfg)
	ctx := context.Background()

	if _, err := admin.Exec(ctx, `CREATE TABLE maint_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(ctx, `CLUSTER MAINTENANCE ENABLE`); err != nil {
		t.Fatalf("CLUSTER MAINTENANCE ENABLE: %v", err)
	}

	if _, err := admin.Exec(ctx, `INSERT INTO maint_t (id) VALUES ('1')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("insert during maintenance mode = %v, want Unavailable", err)
	}
	if _, err := admin.Exec(ctx, `SELECT id FROM maint_t`); err != nil {
		t.Fatalf("reads must keep working during maintenance mode: %v", err)
	}

	// A second, independent connection observes the same server-local state.
	other := openApp(t, addr, tlsCfg)
	if _, err := other.Exec(ctx, `INSERT INTO maint_t (id) VALUES ('2')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("insert from a different connection during maintenance mode = %v, want Unavailable", err)
	}

	if _, err := admin.Exec(ctx, `CLUSTER MAINTENANCE DISABLE`); err != nil {
		t.Fatalf("CLUSTER MAINTENANCE DISABLE: %v", err)
	}
	if _, err := other.Exec(ctx, `INSERT INTO maint_t (id) VALUES ('2')`); err != nil {
		t.Fatalf("insert after disabling maintenance mode must succeed: %v", err)
	}
}

func TestClusterDrainRejectsUnattachedDrainFunc(t *testing.T) {
	addr, tlsCfg := startTLSServer(t) // no SetDrainFunc wired
	conn := openApp(t, addr, tlsCfg)
	if _, err := conn.Exec(context.Background(), `CLUSTER DRAIN`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("CLUSTER DRAIN with no drain function attached = %v, want Unavailable", err)
	}
}

func TestDrainClosesIdleImmediatelyAndWaitsForOpenTransaction(t *testing.T) {
	var srv *protocol.Server
	addr, tlsCfg := startTLSServer(t, func(s *protocol.Server) { srv = s })
	idle := openApp(t, addr, tlsCfg)
	busy := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := busy.Exec(ctx, `CREATE TABLE drain_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `INSERT INTO drain_t (id) VALUES ('1')`); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		srv.Drain(2 * time.Second)
		close(done)
	}()

	// The idle connection has no in-flight statement and no open transaction,
	// so Drain must close it promptly rather than waiting for the deadline.
	deadline := time.Now().Add(1 * time.Second)
	for {
		if _, err := idle.Exec(ctx, `SELECT 1`); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("idle connection was not closed by Drain")
		}
		time.Sleep(5 * time.Millisecond)
	}

	// The busy connection has an open transaction: Drain must leave it usable
	// rather than force-closing it while other work is still to be done.
	if _, err := busy.Exec(ctx, `SELECT id FROM drain_t WHERE id = '1'`); err != nil {
		t.Fatalf("open transaction was closed early: %v", err)
	}

	select {
	case <-done:
		t.Fatal("Drain returned before the open transaction finished")
	default:
	}

	if _, err := busy.Exec(ctx, `COMMIT`); err != nil {
		t.Fatal(err)
	}

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("Drain did not return promptly after the transaction committed")
	}
}

func TestDrainForceClosesAtDeadline(t *testing.T) {
	var srv *protocol.Server
	addr, tlsCfg := startTLSServer(t, func(s *protocol.Server) { srv = s })
	busy := openApp(t, addr, tlsCfg)
	ctx := context.Background()
	if _, err := busy.Exec(ctx, `CREATE TABLE drain_deadline_t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := busy.Exec(ctx, `BEGIN`); err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	done := make(chan struct{})
	go func() {
		srv.Drain(150 * time.Millisecond)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Drain never returned")
	}
	if elapsed := time.Since(start); elapsed > 1*time.Second {
		t.Fatalf("Drain took too long to force-close: %v", elapsed)
	}
	if _, err := busy.Exec(ctx, `SELECT 1`); err == nil {
		t.Fatal("expected the still-open transaction's connection to be force-closed at the deadline")
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
