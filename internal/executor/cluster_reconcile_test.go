package executor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/replication"
	"github.com/bzync/nextsql/internal/security"
)

// TestClusterReconcileConfirmRBAC mirrors TestClusterMaintenanceRBAC's
// shape: ungranted callers are forbidden, the statement is rejected inside
// a transaction, and (with no cluster attached, the single-node case)
// clearing state that was never set fails Unavailable rather than
// silently succeeding.
func TestClusterReconcileConfirmRBAC(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetAuth(users)
	if _, err := app.Exec(`CLUSTER RECONCILE CONFIRM`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted reconcile confirm must be forbidden: %v", err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	if _, err := admin.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CLUSTER RECONCILE CONFIRM`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("reconcile confirm inside a transaction must be rejected: %v", err)
	}
	if _, err := admin.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}

	// No cluster attached (single-node test DB): Unavailable, not a silent
	// success — there is nothing to reconcile, and ConfirmReplicationReconciled
	// says so rather than pretending it worked.
	if _, err := admin.Exec(`CLUSTER RECONCILE CONFIRM`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("reconcile confirm with no cluster attached: want unavailable, got %v", err)
	}
}

// TestClusterReconcileConfirmClearsSuspectFlag proves the full path end to
// end through real SQL: a replication orphan (simulated the same way
// storage.Engine's commitAndReplicate reports one — see
// replication.Cluster.ReportReplicationOrphan) blocks STRONG reads, and
// CLUSTER RECONCILE CONFIRM — run by a privileged session — clears it.
func TestClusterReconcileConfirmClearsSuspectFlag(t *testing.T) {
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	db := testDB(t)

	addr, transport := raft.NewInmemTransport("")
	peers := []replication.Peer{{ID: "n1", Address: string(addr)}}
	cluster, err := replication.Open(replication.Config{
		NodeID:        "n1",
		Peers:         peers,
		Bootstrap:     true,
		AllowMinority: true,
		Keys:          keys,
		Inmem:         true,
		Transport:     transport,
		ApplyTimeout:  3 * time.Second,
	}, db)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cluster.Shutdown() })
	db.AttachCluster(cluster)
	if _, err := cluster.WaitForLeader(5 * time.Second); err != nil {
		t.Fatal(err)
	}

	if err := cluster.StrongReadBarrier(); err != nil {
		t.Fatalf("barrier before orphan: %v", err)
	}

	cluster.ReportReplicationOrphan()
	if err := cluster.StrongReadBarrier(); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("barrier after orphan: want unavailable, got %v", err)
	}

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)

	res, err := admin.Exec(`CLUSTER RECONCILE CONFIRM`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "replication_reconciled" {
		t.Fatalf("result=%+v", res.Rows)
	}

	if err := cluster.StrongReadBarrier(); err != nil {
		t.Fatalf("barrier after reconcile: %v", err)
	}
}
