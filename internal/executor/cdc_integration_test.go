package executor

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/cdc"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/wal"
)

func TestSQLDMLProducesCommittedTenantChanges(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cdc_orders (id STRING PRIMARY KEY, tenant_id STRING NOT NULL, note STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO cdc_orders (id, tenant_id, note) VALUES ('o1', 'tenant-a', 'new')`)
	execOK(t, s, `UPDATE cdc_orders SET note = 'paid' WHERE id = 'o1'`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `INSERT INTO cdc_orders (id, tenant_id, note) VALUES ('rolled-back', 'tenant-a', 'x')`)
	execOK(t, s, `ROLLBACK`)
	execOK(t, s, `DELETE FROM cdc_orders WHERE id = 'o1'`)

	sub, err := cdc.Subscribe(db.Eng.WAL, 0, cdc.Filter{
		Tables: map[string]struct{}{"cdc_orders": {}}, Tenant: "tenant-a",
	}, cdc.Limits{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	want := []wal.ChangeOperation{wal.ChangeInsert, wal.ChangeUpdate, wal.ChangeDelete}
	last := uint64(0)
	for i, op := range want {
		tx, err := sub.Next(ctx)
		if err != nil {
			t.Fatalf("event %d: %v", i, err)
		}
		if len(tx.Events) != 1 || tx.Events[0].Operation != op || tx.Events[0].Tenant != "tenant-a" {
			t.Fatalf("event %d: %+v", i, tx)
		}
		if uint64(tx.Token) <= last {
			t.Fatalf("token did not advance: %d after %d", tx.Token, last)
		}
		last = uint64(tx.Token)
	}
}

func TestNativeSubscribeStreamsAtomicTransactionAndResumes(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE stream_orders (id STRING PRIMARY KEY, note STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO stream_orders (id, note) VALUES ('o1', 'new'), ('o2', 'new')`)

	ctx, cancel := context.WithCancel(context.Background())
	res, err := s.QueryContext(ctx, `SUBSCRIBE TO stream_orders`, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Columns) != len(subscribeColumns) || res.Columns[11] != "resume_token" {
		t.Fatalf("columns=%v", res.Columns)
	}
	batch, err := res.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Count != 2 {
		t.Fatalf("batch=%+v", batch)
	}
	rows := batch.Rows()
	if rows[0][0].Str != "INSERT" || rows[1][0].Str != "INSERT" || rows[0][8].Str != rows[1][8].Str || rows[0][10].Str != rows[1][10].Str {
		t.Fatalf("transaction was not delivered atomically: %+v", rows)
	}
	metrics := db.Metrics().Snapshot()
	if metrics.CDCSubscriptions != 1 || metrics.CDCActive != 1 || metrics.CDCTransactions != 1 || metrics.CDCEvents != 2 {
		t.Fatalf("CDC metrics after delivery=%+v", metrics)
	}
	token, err := strconv.ParseUint(rows[0][11].Str, 10, 64)
	if err != nil || token == 0 {
		t.Fatalf("resume token=%q err=%v", rows[0][11].Str, err)
	}
	cancel()
	if _, err := res.NextBatch(); !nerr.HasCode(err, nerr.Canceled) {
		t.Fatalf("cancel error=%v", err)
	}
	if metrics = db.Metrics().Snapshot(); metrics.CDCActive != 0 || metrics.CDCErrors != 1 {
		t.Fatalf("CDC metrics after cancel=%+v", metrics)
	}

	execOK(t, s, `INSERT INTO stream_orders (id, note) VALUES ('o3', 'paid')`)
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	res, err = s.QueryContext(ctx2, `SUBSCRIBE TO stream_orders AFTER `+strconv.FormatUint(token, 10), nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err = res.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Count != 1 || batch.Rows()[0][11].Str == strconv.FormatUint(token, 10) {
		t.Fatalf("resumed batch=%+v", batch)
	}

	execOK(t, s, `BEGIN`)
	if _, err := s.Query(`SUBSCRIBE TO stream_orders`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("subscription inside transaction=%v", err)
	}
	execOK(t, s, `ROLLBACK`)
}

func TestNativeSubscribeRBACRuntimeRevocationTenantAndAudit(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	execOK(t, admin, `CREATE TABLE tenant_stream (id STRING PRIMARY KEY, tenant_id UUID NOT NULL, note STRING NOT NULL)`)
	execOK(t, admin, `INSERT INTO tenant_stream (id, tenant_id, note) VALUES
		('a', '`+tenantA+`', 'alpha'), ('b', '`+tenantB+`', 'beta')`)

	auditPath := filepath.Join(t.TempDir(), "audit.log")
	audit, err := security.OpenAudit(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	defer audit.Close()
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetAudit(audit)
	if _, err := app.Query(`SUBSCRIBE TO tenant_stream`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("missing CDC grant=%v", err)
	}
	if err := acl.Grant("app", security.PrivCDC, security.ScopeTable, "tenant_stream"); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Query(`SUBSCRIBE TO tenant_stream`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("unbound tenant stream=%v", err)
	}
	execOK(t, app, `SET TENANT = '`+tenantA+`'`)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	res, err := app.QueryContext(ctx, `SUBSCRIBE TO tenant_stream`, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := res.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Count != 1 || batch.Rows()[0][4].Str != tenantA {
		t.Fatalf("tenant stream leaked or omitted rows: %+v", batch)
	}
	if err := acl.Revoke("app", security.PrivCDC, security.ScopeTable, "tenant_stream"); err != nil {
		t.Fatal(err)
	}
	if _, err := res.NextBatch(); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("open stream survived CDC revoke: %v", err)
	}
	body, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), security.ActionCDCSubscribe) || !strings.Contains(string(body), `"object":"tenant_stream"`) {
		t.Fatalf("missing CDC audit event: %s", body)
	}
}

func TestBulkDeleteHeapSwapProducesDeleteIdentities(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cdc_bulk (id STRING PRIMARY KEY, note STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO cdc_bulk (id, note) VALUES ('a', 'one'), ('b', 'two')`)
	after := db.Eng.WAL.DurableLSN()
	if n, err := s.BulkDeleteAll("cdc_bulk"); err != nil || n != 2 {
		t.Fatalf("bulk delete n=%d err=%v", n, err)
	}
	sub, err := cdc.Subscribe(db.Eng.WAL, after, cdc.Filter{Tables: map[string]struct{}{"cdc_bulk": {}}}, cdc.Limits{PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	tx, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(tx.Events) != 2 || tx.Events[0].Operation != wal.ChangeDelete || tx.Events[1].Operation != wal.ChangeDelete || len(tx.Events[0].Key) == 0 || len(tx.Events[1].Key) == 0 {
		t.Fatalf("bulk CDC=%+v", tx)
	}
}

func TestNativeSubscribeOperationPredicate(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cdc_predicate (id STRING PRIMARY KEY, note STRING NOT NULL)`)
	execOK(t, s, `INSERT INTO cdc_predicate (id, note) VALUES ('a', 'one')`)
	execOK(t, s, `UPDATE cdc_predicate SET note = 'two' WHERE id = 'a'`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := s.QueryContext(ctx, `SUBSCRIBE TO cdc_predicate WHERE operation = 'UPDATE'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := res.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	if batch == nil || batch.Count != 1 || batch.Rows()[0][0].Str != "UPDATE" {
		t.Fatalf("operation-filtered batch=%+v", batch)
	}
}

func TestCDCFullImagesAreExplicitAndDecodable(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cdc_images (id STRING PRIMARY KEY, note STRING NOT NULL)`)
	execOK(t, s, `ALTER TABLE cdc_images SET CDC IMAGES FULL`)
	execOK(t, s, `INSERT INTO cdc_images (id, note) VALUES ('a', 'one')`)
	execOK(t, s, `UPDATE cdc_images SET note = 'two' WHERE id = 'a'`)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	res, err := s.QueryContext(ctx, `SUBSCRIBE TO cdc_images WHERE operation = 'UPDATE'`, nil)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := res.NextBatch()
	if err != nil {
		t.Fatal(err)
	}
	rows := batch.Rows()
	if len(rows) != 1 || rows[0][13].Str == "" || rows[0][14].Str == "" {
		t.Fatalf("full images=%+v", rows)
	}
	tab, ok := s.lookup("cdc_images")
	if !ok {
		t.Fatal("table missing")
	}
	beforeRaw, err := hex.DecodeString(rows[0][13].Str)
	if err != nil {
		t.Fatal(err)
	}
	afterRaw, err := hex.DecodeString(rows[0][14].Str)
	if err != nil {
		t.Fatal(err)
	}
	before, err := types.DecodeRow(beforeRaw, tab.Types())
	if err != nil {
		t.Fatal(err)
	}
	after, err := types.DecodeRow(afterRaw, tab.Types())
	if err != nil {
		t.Fatal(err)
	}
	if before[1].Str != "one" || after[1].Str != "two" {
		t.Fatalf("decoded images before=%v after=%v", before, after)
	}
	execOK(t, s, `ALTER TABLE cdc_images SET CDC IMAGES KEYS`)
	if tab, _ := s.lookup("cdc_images"); tab.CDCImages != catalog.CDCImagesKeys {
		t.Fatalf("image mode=%v", tab.CDCImages)
	}
}
