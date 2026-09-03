package integration

import (
	"context"
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/dbmanager"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/storage/format"
)

// multiDBFixture is a slimmed, self-contained equivalent of nextsqld's own
// hosted-registry + dbmanager wiring (cmd/nextsqld/main.go), built directly
// against the library primitives rather than the nextsqld binary, for
// testing M2-3a's real routing behavior end to end.
type multiDBFixture struct {
	registry *hosting.Registry
	realm    hosting.Realm
	db1      hosting.Database // primary, preloaded
	db2      hosting.Database // secondary, opened on demand via the manager
	dataDir  string
}

func newMultiDBFixture(t *testing.T) *multiDBFixture {
	t.Helper()
	dataDir := t.TempDir()
	instanceRoot := createTestKeyFile(t, filepath.Join(dataDir, "instance.key"))

	ident1, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dataDir), instanceRoot, hosting.Bootstrap{
		RealmName: "acme", DatabaseName: "db1", DatabaseIdentity: ident1, DatabaseState: hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })

	realm, db1, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}

	ident2, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	key2Path := filepath.Join(dataDir, "db2.key")
	if _, err := crypto.CreateKeyFile(key2Path, 1); err != nil {
		t.Fatal(err)
	}
	db2, _, err := reg.CreateDatabase(realm.ID, "db2", ident2, key2Path)
	if err != nil {
		t.Fatal(err)
	}

	// Physically create db1's file (the primary, legacy-default layout).
	db1Path := filepath.Join(dataDir, "nextsql.db")
	db1Root := createTestKeyFile(t, filepath.Join(dataDir, "db1.key"))
	env1, err := crypto.CreateEnvelope(crypto.KeystorePath(db1Path), ident1, db1Root)
	if err != nil {
		t.Fatal(err)
	}
	created1, err := executor.CreateWithIdentity(db1Path, ident1, env1, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := created1.Close(); err != nil {
		t.Fatal(err)
	}
	_ = env1.Close()

	// Physically create db2's file (managed layout) and activate it. Mirrors
	// nextsql database create's real activateManagedDatabase: KeyRef is the
	// database's own standalone ROOT key, which unlocks an envelope
	// keystore next to the database file (crypto.KeystorePath) — the root
	// key never encrypts the database file directly, matching exactly how
	// the primary database above (and every real deployment) works.
	db2Root, err := crypto.ReadKeyFile(key2Path)
	if err != nil {
		t.Fatal(err)
	}
	db2Path := hosting.ManagedDatabasePath(dataDir, realm.ID, db2.ID)
	if err := os.MkdirAll(filepath.Dir(db2Path), 0o700); err != nil {
		t.Fatal(err)
	}
	env2, err := crypto.CreateEnvelope(crypto.KeystorePath(db2Path), ident2, db2Root)
	if err != nil {
		t.Fatal(err)
	}
	created2, err := executor.CreateWithIdentity(db2Path, ident2, env2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := created2.Close(); err != nil {
		t.Fatal(err)
	}
	_ = env2.Close()
	if err := reg.SetDatabaseState(realm.ID, db2.ID, hosting.StateActive); err != nil {
		t.Fatal(err)
	}
	_, db2, err = reg.Lookup("acme", "db2")
	if err != nil {
		t.Fatal(err)
	}

	return &multiDBFixture{registry: reg, realm: realm, db1: db1, db2: db2, dataDir: dataDir}
}

func createTestKeyFile(t *testing.T, path string) *crypto.DEK {
	t.Helper()
	dek, err := crypto.CreateKeyFile(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	return dek
}

// startMultiDBServer wires a real protocol.Server backed by a real
// dbmanager.Manager over fx's two registered databases, mirroring
// nextsqld's own opener shape (crypto.LoadProvider + executor.Open, no
// cluster/archiver — matching M2-3a's single-node-only scope for
// secondary databases).
func startMultiDBServer(t *testing.T, fx *multiDBFixture, limit int, configure ...func(*protocol.Server)) (addr string, clientTLS *tls.Config, mgr *dbmanager.Manager) {
	t.Helper()
	db1Path := filepath.Join(fx.dataDir, "nextsql.db")
	db1Root, err := crypto.ReadKeyFile(filepath.Join(fx.dataDir, "db1.key"))
	if err != nil {
		t.Fatal(err)
	}
	env1, err := crypto.OpenEnvelope(crypto.KeystorePath(db1Path), db1Root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = env1.Close() })
	db1, err := executor.Open(db1Path, env1, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db1.Close() })
	db1.SetDatabaseName(fx.db1.Name)

	opener := func(realm hosting.Realm, database hosting.Database) (*executor.DB, func() error, error) {
		// Mirrors nextsqld's real Opener (cmd/nextsqld/main.go): KeyRef is
		// the database's standalone root key, which unlocks an envelope
		// keystore next to the database file — not usable directly as the
		// database's own KeyProvider.
		root, err := crypto.ReadKeyFile(database.KeyRef)
		if err != nil {
			return nil, nil, err
		}
		path := hosting.ManagedDatabasePath(fx.dataDir, realm.ID, database.ID)
		env, err := crypto.OpenEnvelope(crypto.KeystorePath(path), root)
		if err != nil {
			return nil, nil, err
		}
		db, err := executor.Open(path, env, 16)
		if err != nil {
			_ = env.Close()
			return nil, nil, err
		}
		db.SetDatabaseName(database.Name)
		cleanup := func() error {
			dbErr := db.Close()
			_ = env.Close()
			return dbErr
		}
		return db, cleanup, nil
	}
	mgr = dbmanager.New(limit, fx.registry.Lookup, opener)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.Preload(fx.realm, fx.db1, db1); err != nil {
		t.Fatal(err)
	}

	users, err := auth.Create(filepath.Join(fx.dataDir, "nextsql.users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}

	certPath := filepath.Join(fx.dataDir, "tls.crt")
	keyFile := filepath.Join(fx.dataDir, "tls.key")
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
	clientTLS, err = security.ClientTLSFromPEM("localhost", pem)
	if err != nil {
		t.Fatal(err)
	}

	srv := protocol.NewServer(db1, users)
	srv.TLS = srvTLS
	srv.Realm = fx.realm.Name
	srv.Database = fx.db1.Name
	srv.SetDatabaseManager(mgr)
	for _, fn := range configure {
		fn(srv)
	}
	ctx, cancel := context.WithCancel(context.Background())
	pool, err := executor.NewTaskPool(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := executor.StartTaskRuntime(ctx, db1, pool, executor.TaskRuntimeConfig{Batch: 4, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour})
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
	return srv.Addr().String(), clientTLS, mgr
}

func openInRealm(t *testing.T, addr string, tlsCfg *tls.Config, database string) *nextsql.Conn {
	t.Helper()
	conn, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "acme", Database: database, User: "app", Password: "s3cret", TLS: tlsCfg,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// TestRealmRoutingReachesDistinctDatabases is the concrete proof that
// Hello.Realm/Hello.Database (M2-2) now actually routes to different
// storage via dbmanager (M2-3a), not just an identity check: a table
// created through a connection routed to db1 is invisible through a
// connection routed to db2, and vice versa.
func TestRealmRoutingReachesDistinctDatabases(t *testing.T) {
	fx := newMultiDBFixture(t)
	addr, tlsCfg, _ := startMultiDBServer(t, fx, 4)

	c1 := openInRealm(t, addr, tlsCfg, "db1")
	if _, err := c1.Exec(context.Background(), `CREATE TABLE only_in_db1 (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	c2 := openInRealm(t, addr, tlsCfg, "db2")
	if _, err := c2.Exec(context.Background(), `CREATE TABLE only_in_db2 (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if _, err := c2.Exec(context.Background(), `SELECT * FROM only_in_db1`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("db2 connection saw db1's table: %v", err)
	}
	if _, err := c1.Exec(context.Background(), `SELECT * FROM only_in_db2`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("db1 connection saw db2's table: %v", err)
	}
}

// TestOpenDatabaseLimitRejectsCleanly proves the (limit+1)th distinct
// database is rejected cleanly over a real connection, not a hang or drop.
func TestOpenDatabaseLimitRejectsCleanly(t *testing.T) {
	fx := newMultiDBFixture(t)
	addr, tlsCfg, _ := startMultiDBServer(t, fx, 1) // primary only

	if _, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "acme", Database: "db2", User: "app", Password: "s3cret", TLS: tlsCfg,
	}); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("got %v", err)
	}

	// The connection is cleanly usable afterward — not left in a bad state.
	c1 := openInRealm(t, addr, tlsCfg, "db1")
	if _, err := c1.Exec(context.Background(), `CREATE TABLE still_usable (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
}

// TestNodeStatusReportsConnectionsOwnDatabase regression-proofs the
// nodeStatus(db) parameter fix: a connection routed to the secondary
// database reports that database's own health, not the primary's.
func TestNodeStatusReportsConnectionsOwnDatabase(t *testing.T) {
	fx := newMultiDBFixture(t)
	addr, tlsCfg, _ := startMultiDBServer(t, fx, 4)

	c2 := openInRealm(t, addr, tlsCfg, "db2")
	status, err := c2.NodeStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if status.Role != "standalone" || !status.Healthy {
		t.Fatalf("secondary database node status: %+v", status)
	}
}

// TestSecondaryDatabaseEvictedWhenIdle (M2-3b-1) proves a secondary
// database actually closes once its last connection disconnects, and
// reopens cleanly (with data intact) on the next connection.
func TestSecondaryDatabaseEvictedWhenIdle(t *testing.T) {
	fx := newMultiDBFixture(t)
	addr, tlsCfg, mgr := startMultiDBServer(t, fx, 4)

	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount before any secondary connection = %d, want 1 (primary only)", got)
	}

	c2 := openInRealm(t, addr, tlsCfg, "db2")
	if _, err := c2.Exec(context.Background(), `CREATE TABLE survives (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := c2.Exec(context.Background(), `INSERT INTO survives (id) VALUES ('row1')`); err != nil {
		t.Fatal(err)
	}
	if got := mgr.OpenCount(); got != 2 {
		t.Fatalf("OpenCount with db2 open = %d, want 2", got)
	}

	if err := c2.Close(); err != nil {
		t.Fatal(err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for mgr.OpenCount() != 1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if got := mgr.OpenCount(); got != 1 {
		t.Fatalf("OpenCount after closing the only db2 connection = %d, want 1 (db2 evicted)", got)
	}

	// Reconnecting reopens db2 fresh, and the earlier write survived the
	// close — proving Close()'s durability held across the evict/reopen
	// cycle, and Acquire's single-flight/limit logic still works correctly
	// on a database that has already cycled through eviction once.
	c2b := openInRealm(t, addr, tlsCfg, "db2")
	res, err := c2b.Exec(context.Background(), `SELECT id FROM survives`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "row1" {
		t.Fatalf("data after evict/reopen: %+v", res.Rows)
	}
	if got := mgr.OpenCount(); got != 2 {
		t.Fatalf("OpenCount after reconnecting to db2 = %d, want 2", got)
	}
}

// TestPerDatabaseConnectionLimitIsolatesDatabases (P27's own last open
// exit-gate item) proves MaxSessionsPerDatabase counts each (realm,
// database) pair independently: exhausting db1's own limit never blocks a
// connection to db2 in the same realm, and vice versa.
func TestPerDatabaseConnectionLimitIsolatesDatabases(t *testing.T) {
	fx := newMultiDBFixture(t)
	addr, tlsCfg, _ := startMultiDBServer(t, fx, 4, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessionsPerDatabase = 1
		srv.Limits = lim
	})

	c1 := openInRealm(t, addr, tlsCfg, "db1")
	if _, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "acme", Database: "db1", User: "app", Password: "s3cret", TLS: tlsCfg,
	}); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("second db1 connection: got %v, want Exhausted", err)
	}

	// db2 is a distinct database — its own limit is untouched by db1's.
	c2 := openInRealm(t, addr, tlsCfg, "db2")
	if _, err := c2.Exec(context.Background(), `CREATE TABLE still_usable (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if err := c1.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var c1b *nextsql.Conn
	var err error
	for {
		c1b, err = nextsql.Open(nextsql.Config{
			Address: addr, Realm: "acme", Database: "db1", User: "app", Password: "s3cret", TLS: tlsCfg,
		})
		if err == nil {
			break
		}
		if !nerr.HasCode(err, nerr.Exhausted) || time.Now().After(deadline) {
			t.Fatalf("db1 connection after close: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer c1b.Close()
}

// TestPerRealmConnectionLimitCountsAcrossDatabases proves
// MaxSessionsPerRealm counts every connection to any database within one
// realm together, unlike MaxSessionsPerDatabase — a connection to db2
// counts against the same realm-wide budget db1's connection already used.
func TestPerRealmConnectionLimitCountsAcrossDatabases(t *testing.T) {
	fx := newMultiDBFixture(t)
	addr, tlsCfg, _ := startMultiDBServer(t, fx, 4, func(srv *protocol.Server) {
		lim := srv.Limits
		lim.MaxSessionsPerRealm = 1
		srv.Limits = lim
	})

	c1 := openInRealm(t, addr, tlsCfg, "db1")
	if _, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "acme", Database: "db2", User: "app", Password: "s3cret", TLS: tlsCfg,
	}); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("db2 connection while db1's realm-wide slot is held: got %v, want Exhausted", err)
	}
	if err := c1.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	var c2 *nextsql.Conn
	var err error
	for {
		c2, err = nextsql.Open(nextsql.Config{
			Address: addr, Realm: "acme", Database: "db2", User: "app", Password: "s3cret", TLS: tlsCfg,
		})
		if err == nil {
			break
		}
		if !nerr.HasCode(err, nerr.Exhausted) || time.Now().After(deadline) {
			t.Fatalf("db2 connection after db1 closed: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer c2.Close()
}
