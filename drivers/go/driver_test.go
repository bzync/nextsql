package nextsql

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/protocol"
)

func TestParseDSN(t *testing.T) {
	// URL format with extended options
	uDSN := "nextsql://alice:s3cret@127.0.0.1:7210/maindb?insecure=true&realm=primary&read_consistency=bounded&max_staleness=5s&nodes=127.0.0.1:7210,127.0.0.1:7211"
	cfg, err := ParseDSN(uDSN)
	if err != nil {
		t.Fatalf("ParseDSN URL: %v", err)
	}
	if cfg.Address != "127.0.0.1:7210" || cfg.User != "alice" || cfg.Password != "s3cret" || cfg.Database != "maindb" || !cfg.InsecureNoTLS {
		t.Fatalf("unexpected parsed URL cfg: %+v", cfg)
	}
	if cfg.Realm != "primary" || cfg.ReadConsistency != Bounded || cfg.MaxStaleness != 5*time.Second || len(cfg.Nodes) != 2 {
		t.Fatalf("unexpected extended URL cfg: %+v", cfg)
	}

	// Key-value format with extended options
	kvDSN := "address=127.0.0.1:7210 user=bob password=pass database=testdb insecure=true read_consistency=stale nodes=node1:7210,node2:7210 max_staleness_ms=2500"
	cfg2, err := ParseDSN(kvDSN)
	if err != nil {
		t.Fatalf("ParseDSN KV: %v", err)
	}
	if cfg2.Address != "127.0.0.1:7210" || cfg2.User != "bob" || cfg2.Password != "pass" || cfg2.Database != "testdb" || !cfg2.InsecureNoTLS {
		t.Fatalf("unexpected parsed KV cfg: %+v", cfg2)
	}
	if cfg2.ReadConsistency != Stale || cfg2.MaxStaleness != 2500*time.Millisecond || len(cfg2.Nodes) != 2 {
		t.Fatalf("unexpected extended KV cfg: %+v", cfg2)
	}

	// Invalid
	if _, err := ParseDSN("address=127.0.0.1:7210"); err == nil {
		t.Fatal("expected error for missing user")
	}
	if _, err := ParseDSN("user=alice"); err == nil {
		t.Fatal("expected error for missing address")
	}
	if _, err := ParseDSN("nextsql://alice:s3cret@127.0.0.1:7210/db?read_consistency=invalid"); err == nil {
		t.Fatal("expected error for invalid read_consistency")
	}
}

func startTestServer(t *testing.T) string {
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

	srv := protocol.NewServer(db, users)
	ctx, cancel := context.WithCancel(context.Background())
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
	return srv.Addr().String()
}

func TestDatabaseSQLDriver(t *testing.T) {
	addr := startTestServer(t)
	dsn := "nextsql://app:s3cret@" + addr + "/default?insecure=true"

	db, err := sql.Open("nextsql", dsn)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	defer db.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Ping
	if err := db.PingContext(ctx); err != nil {
		t.Fatalf("Ping: %v", err)
	}

	// Exec DDL
	_, err = db.ExecContext(ctx, "CREATE TABLE users (id INT64 PRIMARY KEY, name STRING, active BOOL)")
	if err != nil {
		t.Fatalf("CREATE TABLE: %v", err)
	}

	// Exec INSERT
	res, err := db.ExecContext(ctx, "INSERT INTO users VALUES ($1, $2, $3)", 1, "alice", true)
	if err != nil {
		t.Fatalf("INSERT: %v", err)
	}
	aff, err := res.RowsAffected()
	if err != nil || aff != 1 {
		t.Fatalf("RowsAffected: %d, err: %v", aff, err)
	}

	// Query row
	var id int64
	var name string
	var active bool
	err = db.QueryRowContext(ctx, "SELECT id, name, active FROM users WHERE id = $1", 1).Scan(&id, &name, &active)
	if err != nil {
		t.Fatalf("QueryRow: %v", err)
	}
	if id != 1 || name != "alice" || !active {
		t.Fatalf("unexpected scanned values: id=%d, name=%s, active=%v", id, name, active)
	}

	// Query multiple rows
	_, err = db.ExecContext(ctx, "INSERT INTO users VALUES ($1, $2, $3)", 2, "bob", false)
	if err != nil {
		t.Fatal(err)
	}

	rows, err := db.QueryContext(ctx, "SELECT id, name FROM users ORDER BY id")
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		count++
		var uid int64
		var uname string
		if err := rows.Scan(&uid, &uname); err != nil {
			t.Fatalf("Scan: %v", err)
		}
	}
	if count != 2 {
		t.Fatalf("expected 2 rows, got %d", count)
	}

	// Transaction commit
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatalf("BeginTx: %v", err)
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO users VALUES ($1, $2, $3)", 3, "charlie", true)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("Commit: %v", err)
	}

	// Transaction rollback
	tx2, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = tx2.ExecContext(ctx, "INSERT INTO users VALUES ($1, $2, $3)", 4, "dave", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := tx2.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}

	// Verify dave was not committed
	var daveCount int
	err = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM users WHERE id = $1", 4).Scan(&daveCount)
	if err != nil || daveCount != 0 {
		t.Fatalf("expected dave to be rolled back, count=%d, err=%v", daveCount, err)
	}
}
