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

// multiRealmRoutingFixture is two realms, each with its own database, both
// reachable through ONE protocol.Server + dbmanager.Manager — the genuine
// activation this increment landed (relaxing server.go's flat s.Realm
// equality precheck, which previously made any realm but the pinned default
// unreachable regardless of dbmanager/LookupRealm already supporting it).
type multiRealmRoutingFixture struct {
	registry *hosting.Registry
	realmA   hosting.Realm
	dbA      hosting.Database
	realmB   hosting.Realm
	dbB      hosting.Database
	dataDir  string
}

func newMultiRealmRoutingFixture(t *testing.T) *multiRealmRoutingFixture {
	t.Helper()
	dataDir := t.TempDir()
	instanceRoot := createTestKeyFile(t, filepath.Join(dataDir, "instance.key"))

	identA, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dataDir), instanceRoot, hosting.Bootstrap{
		RealmName: "acme", DatabaseName: "dba", DatabaseIdentity: identA, DatabaseState: hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reg.Close() })
	realmA, dbA, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}

	identB, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	keyBPath := filepath.Join(dataDir, "dbb.key")
	if _, err := crypto.CreateKeyFile(keyBPath, 1); err != nil {
		t.Fatal(err)
	}
	realmB, dbB, _, err := reg.CreateRealm("beta", "dbb", identB, keyBPath)
	if err != nil {
		t.Fatal(err)
	}

	dbAPath := filepath.Join(dataDir, "nextsql.db")
	rootA := createTestKeyFile(t, filepath.Join(dataDir, "dba.key"))
	envA, err := crypto.CreateEnvelope(crypto.KeystorePath(dbAPath), identA, rootA)
	if err != nil {
		t.Fatal(err)
	}
	createdA, err := executor.CreateWithIdentity(dbAPath, identA, envA, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := createdA.Close(); err != nil {
		t.Fatal(err)
	}
	_ = envA.Close()

	dbBRoot, err := crypto.ReadKeyFile(keyBPath)
	if err != nil {
		t.Fatal(err)
	}
	dbBPath := hosting.ManagedDatabasePath(dataDir, realmB.ID, dbB.ID)
	if err := os.MkdirAll(filepath.Dir(dbBPath), 0o700); err != nil {
		t.Fatal(err)
	}
	envB, err := crypto.CreateEnvelope(crypto.KeystorePath(dbBPath), identB, dbBRoot)
	if err != nil {
		t.Fatal(err)
	}
	createdB, err := executor.CreateWithIdentity(dbBPath, identB, envB, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := createdB.Close(); err != nil {
		t.Fatal(err)
	}
	_ = envB.Close()
	if err := reg.SetDatabaseState(realmB.ID, dbB.ID, hosting.StateActive); err != nil {
		t.Fatal(err)
	}
	realmB, dbB, err = reg.Lookup("beta", "dbb")
	if err != nil {
		t.Fatal(err)
	}

	return &multiRealmRoutingFixture{registry: reg, realmA: realmA, dbA: dbA, realmB: realmB, dbB: dbB, dataDir: dataDir}
}

// startMultiRealmServer wires one real protocol.Server, backed by a real
// dbmanager.Manager, serving realm A's database as the preloaded primary
// and realm B's database on demand — mirroring nextsqld's own opener shape
// exactly like startMultiDBServer, generalized across realms instead of
// just across databases within one realm.
func (fx *multiRealmRoutingFixture) startServer(t *testing.T, limit int) (addr string, clientTLS *tls.Config) {
	t.Helper()
	dbAPath := filepath.Join(fx.dataDir, "nextsql.db")
	dbARoot, err := crypto.ReadKeyFile(filepath.Join(fx.dataDir, "dba.key"))
	if err != nil {
		t.Fatal(err)
	}
	envA, err := crypto.OpenEnvelope(crypto.KeystorePath(dbAPath), dbARoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = envA.Close() })
	dbA, err := executor.Open(dbAPath, envA, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = dbA.Close() })
	dbA.SetDatabaseName(fx.dbA.Name)

	opener := func(realm hosting.Realm, database hosting.Database) (*executor.DB, func() error, error) {
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
	mgr := dbmanager.New(limit, fx.registry.Lookup, opener)
	t.Cleanup(func() { _ = mgr.Close() })
	if err := mgr.Preload(fx.realmA, fx.dbA, dbA); err != nil {
		t.Fatal(err)
	}

	users, err := auth.Create(filepath.Join(fx.dataDir, "nextsql.users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	// No ACL configured: like startMultiDBServer's own fixture, this test is
	// about realm/database routing and isolation, not RBAC granularity
	// (already covered by multirealm_auth_test.go) — a nil ACL means every
	// privilege check is unrestricted (security.ACL.Allowed's own documented
	// nil-receiver behavior).

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

	srv := protocol.NewServer(dbA, users)
	srv.TLS = srvTLS
	// srv.Realm still names the deployment's default realm (used only as
	// the fallback when a Hello omits Realm) — it is no longer an
	// equality gate now that srv.HostingRegistry is set, per the fix this
	// test proves.
	srv.Realm = fx.realmA.Name
	srv.Database = fx.dbA.Name
	srv.HostingRegistry = fx.registry
	srv.SetDatabaseManager(mgr)
	ctx, cancel := context.WithCancel(context.Background())
	pool, err := executor.NewTaskPool(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := executor.StartTaskRuntime(ctx, dbA, pool, executor.TaskRuntimeConfig{Batch: 4, PollInterval: 10 * time.Millisecond, PurgeEvery: time.Hour})
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

// TestMultiRealmRoutingThroughOneServer is the live proof that a Hello may
// now legitimately name a realm other than the deployment's pinned default
// and still be routed and isolated correctly, through one real
// protocol.Server + dbmanager.Manager: a table created via a connection
// routed to realm A's database is invisible via a connection routed to
// realm B's database, and vice versa.
func TestMultiRealmRoutingThroughOneServer(t *testing.T) {
	fx := newMultiRealmRoutingFixture(t)
	addr, tlsCfg := fx.startServer(t, 4)

	connA, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "acme", Database: "dba", User: "app", Password: "s3cret", TLS: tlsCfg,
	})
	if err != nil {
		t.Fatalf("connect to realm A: %v", err)
	}
	defer connA.Close()
	if _, err := connA.Exec(context.Background(), `CREATE TABLE only_in_realm_a (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	connB, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "beta", Database: "dbb", User: "app", Password: "s3cret", TLS: tlsCfg,
	})
	if err != nil {
		t.Fatalf("connect to realm B (previously unreachable, blocked by the flat s.Realm precheck): %v", err)
	}
	defer connB.Close()
	if _, err := connB.Exec(context.Background(), `CREATE TABLE only_in_realm_b (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	if _, err := connB.Exec(context.Background(), `SELECT * FROM only_in_realm_a`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("realm B connection saw realm A's table: %v", err)
	}
	if _, err := connA.Exec(context.Background(), `SELECT * FROM only_in_realm_b`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("realm A connection saw realm B's table: %v", err)
	}
}

// TestUnknownRealmStillRejectedCleanly proves the flat precheck's removal
// (for a hosted deployment) did not turn "any string" into a valid realm —
// LookupRealm (M2-4b-1) remains the authoritative, registry-backed check.
// It deliberately uses "app"/"s3cret" — a real deployment-wide credential
// that would authenticate successfully in a *valid* realm — to prove an
// unknown realm is rejected even with correct credentials, and rejected as
// the same generic Unauthorized "authentication failed" a wrong password
// would produce (the pre-auth realm-disclosure hardening below), not a
// distinguishing NotFound.
func TestUnknownRealmStillRejectedCleanly(t *testing.T) {
	fx := newMultiRealmRoutingFixture(t)
	addr, tlsCfg := fx.startServer(t, 4)

	if _, err := nextsql.Open(nextsql.Config{
		Address: addr, Realm: "nonexistent", Database: "dba", User: "app", Password: "s3cret", TLS: tlsCfg,
	}); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("unknown realm with correct credentials must still be rejected Unauthorized, got: %v", err)
	}
}
