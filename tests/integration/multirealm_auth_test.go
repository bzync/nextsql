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
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/protocol"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/storage/format"
)

// realmAuthFixture is two independent single-database deployments (no
// dbmanager routing — that stays out of scope for M2-4b-1, see TODO.md's
// "srv.Realm currently pins one realm" finding) in two different realms of
// one shared hosting.Registry, both servers sharing the same *auth.Store and
// *security.ACL instance — proving M2-4b-1's real capability: one
// composite-keyed auth file safely holds independent, same-named principals
// across realms, with a real *hosting.Registry.LookupRealm resolution on
// the wire (not just at the auth.Store/security.ACL package level, already
// covered exhaustively by internal/auth and internal/security's own unit
// tests).
type realmAuthFixture struct {
	registry   *hosting.Registry
	realmA     hosting.Realm
	realmB     hosting.Realm
	users      *auth.Store
	acl        *security.ACL
	addrA      string
	addrB      string
	tlsA, tlsB *tls.Config
}

func newRealmAuthFixture(t *testing.T) *realmAuthFixture {
	t.Helper()
	dir := t.TempDir()
	instanceRoot := createTestKeyFile(t, filepath.Join(dir, "instance.key"))

	identA, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
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
	keyBPath := filepath.Join(dir, "dbb.key")
	if _, err := crypto.CreateKeyFile(keyBPath, 1); err != nil {
		t.Fatal(err)
	}
	realmB, dbB, _, err := reg.CreateRealm("beta", "dbb", identB, keyBPath)
	if err != nil {
		t.Fatal(err)
	}

	dbAPath := filepath.Join(dir, "nextsql.db")
	rootA := createTestKeyFile(t, filepath.Join(dir, "dba.key"))
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
	dbBPath := hosting.ManagedDatabasePath(dir, realmB.ID, dbB.ID)
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

	users, err := auth.Create(filepath.Join(dir, "nextsql.users"))
	if err != nil {
		t.Fatal(err)
	}
	acl, err := security.CreateACL(filepath.Join(dir, "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	// Deployment-wide bootstrap admin, per every real nextsql init/nextsqld
	// --user bootstrap (decision #2: hosting.ID{} means deployment-wide).
	if err := users.Upsert("root", "rootpw"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("root", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("root", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	fx := &realmAuthFixture{registry: reg, realmA: realmA, realmB: realmB, users: users, acl: acl}

	envAOpen, err := crypto.OpenEnvelope(crypto.KeystorePath(dbAPath), rootA)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = envAOpen.Close() })
	openDBA, err := executor.Open(dbAPath, envAOpen, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = openDBA.Close() })
	openDBA.SetDatabaseName(dbA.Name)
	fx.addrA, fx.tlsA = fx.startServer(t, dir, "srvA", openDBA, realmA.Name, dbA.Name)

	envBOpen, err := crypto.OpenEnvelope(crypto.KeystorePath(dbBPath), dbBRoot)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = envBOpen.Close() })
	openDBB, err := executor.Open(dbBPath, envBOpen, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = openDBB.Close() })
	openDBB.SetDatabaseName(dbB.Name)
	fx.addrB, fx.tlsB = fx.startServer(t, dir, "srvB", openDBB, realmB.Name, dbB.Name)

	return fx
}

// startServer starts one standalone (no dbmanager) protocol.Server for one
// realm's primary database, sharing fx.users/fx.acl/fx.registry with every
// other server this fixture starts.
func (fx *realmAuthFixture) startServer(t *testing.T, dir, name string, db *executor.DB, realmName, databaseName string) (addr string, clientTLS *tls.Config) {
	t.Helper()
	certPath := filepath.Join(dir, name+".crt")
	keyFile := filepath.Join(dir, name+".key")
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
	srv := protocol.NewServer(db, fx.users)
	srv.TLS = srvTLS
	srv.ACL = fx.acl
	srv.Realm = realmName
	srv.Database = databaseName
	srv.HostingRegistry = fx.registry
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() { _ = srv.Close() })
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe(ctx, "127.0.0.1:0") }()
	waitForServer(t, srv, serveErr)
	return srv.Addr().String(), clientTLS
}

func waitForServer(t *testing.T, srv *protocol.Server, serveErr chan error) {
	t.Helper()
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
}

// TestCrossRealmSameUsernameIsolatedOverRealConnections is the live,
// wire-level proof of M2-4b-1's headline capability: the same username,
// with different passwords, exists independently in two realms served by
// two connections sharing one composite-keyed auth.Store/security.ACL, and
// each realm's server only ever accepts its own realm's password for that
// username — plus the deployment-wide bootstrap admin still authenticates
// into both.
func TestCrossRealmSameUsernameIsolatedOverRealConnections(t *testing.T) {
	fx := newRealmAuthFixture(t)

	if err := fx.users.UpsertInRealm(fx.realmA.ID, "dba", "passwordA"); err != nil {
		t.Fatal(err)
	}
	if err := fx.acl.GrantInRealm(fx.realmA.ID, "dba", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := fx.users.UpsertInRealm(fx.realmB.ID, "dba", "passwordB"); err != nil {
		t.Fatal(err)
	}
	if err := fx.acl.GrantInRealm(fx.realmB.ID, "dba", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	// Realm A's own password works against server A.
	connA, err := nextsql.Open(nextsql.Config{
		Address: fx.addrA, Realm: fx.realmA.Name, Database: fx.realmA.Databases[0].Name,
		User: "dba", Password: "passwordA", TLS: fx.tlsA,
	})
	if err != nil {
		t.Fatalf("realm A own password must authenticate: %v", err)
	}
	_ = connA.Close()

	// Realm B's password must not work against server A for the same username.
	if _, err := nextsql.Open(nextsql.Config{
		Address: fx.addrA, Realm: fx.realmA.Name, Database: fx.realmA.Databases[0].Name,
		User: "dba", Password: "passwordB", TLS: fx.tlsA,
	}); err == nil {
		t.Fatal("realm B's password must not authenticate against realm A's server")
	}

	// Realm B's own password works against server B.
	connB, err := nextsql.Open(nextsql.Config{
		Address: fx.addrB, Realm: fx.realmB.Name, Database: fx.realmB.Databases[0].Name,
		User: "dba", Password: "passwordB", TLS: fx.tlsB,
	})
	if err != nil {
		t.Fatalf("realm B own password must authenticate: %v", err)
	}
	_ = connB.Close()

	// Realm A's password must not work against server B for the same username.
	if _, err := nextsql.Open(nextsql.Config{
		Address: fx.addrB, Realm: fx.realmB.Name, Database: fx.realmB.Databases[0].Name,
		User: "dba", Password: "passwordA", TLS: fx.tlsB,
	}); err == nil {
		t.Fatal("realm A's password must not authenticate against realm B's server")
	}

	// The deployment-wide bootstrap admin still authenticates into both.
	rootA, err := nextsql.Open(nextsql.Config{
		Address: fx.addrA, Realm: fx.realmA.Name, Database: fx.realmA.Databases[0].Name,
		User: "root", Password: "rootpw", TLS: fx.tlsA,
	})
	if err != nil {
		t.Fatalf("deployment-wide admin must authenticate into realm A: %v", err)
	}
	_ = rootA.Close()
	rootB, err := nextsql.Open(nextsql.Config{
		Address: fx.addrB, Realm: fx.realmB.Name, Database: fx.realmB.Databases[0].Name,
		User: "root", Password: "rootpw", TLS: fx.tlsB,
	})
	if err != nil {
		t.Fatalf("deployment-wide admin must authenticate into realm B: %v", err)
	}
	_ = rootB.Close()
}
