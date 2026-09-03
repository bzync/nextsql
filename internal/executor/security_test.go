package executor

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestRBACDeniesAndGrants(t *testing.T) {
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
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	if _, err := admin.Exec(`CREATE TABLE t (id UUID PRIMARY KEY, n STRING)`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CREATE USER app IDENTIFIED BY 'pw'`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`GRANT SELECT ON TABLE t TO app`); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	if _, err := app.Exec(`SELECT * FROM t`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO t (id, n) VALUES (UUID(), 'x')`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("insert must be forbidden: %v", err)
	}
	if _, err := admin.Exec(`GRANT INSERT ON TABLE t TO app`); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Exec(`INSERT INTO t (id, n) VALUES (UUID(), 'x')`); err != nil {
		t.Fatal(err)
	}

	if _, err := admin.Exec(`CREATE USER nobody IDENTIFIED BY 'pw'`); err != nil {
		t.Fatal(err)
	}
	nobody := db.Session()
	nobody.SetIdentity("nobody")
	nobody.SetACL(acl)
	if _, err := nobody.Exec(`SELECT * FROM t`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted select must fail closed: %v", err)
	}
	if _, err := nobody.Exec(`BEGIN`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted begin must fail closed: %v", err)
	}
}

func TestClusterTransferLeaderRBACAndSingleNode(t *testing.T) {
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
	if _, err := app.Exec(`CLUSTER TRANSFER LEADER`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted transfer must be forbidden: %v", err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	if _, err := admin.Exec(`CLUSTER TRANSFER LEADER`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("single-node deployment must report unavailable: %v", err)
	}

	if _, err := admin.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CLUSTER TRANSFER LEADER`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("transfer inside a transaction must be rejected: %v", err)
	}
	if _, err := admin.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}
}

func TestClusterDrainRBACAndWiring(t *testing.T) {
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
	if _, err := app.Exec(`CLUSTER DRAIN`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted drain must be forbidden: %v", err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)

	// No DrainFunc attached (embedded/CLI use, no listening server).
	if _, err := admin.Exec(`CLUSTER DRAIN`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("CLUSTER DRAIN with no server attached = %v, want Unavailable", err)
	}

	if _, err := admin.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CLUSTER DRAIN`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("drain inside a transaction must be rejected: %v", err)
	}
	if _, err := admin.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}

	// A working DrainFunc is invoked with the WITH (TIMEOUT_MS = ...) value,
	// and unlike TransferLeader this is not gated on a Raft leader — it must
	// succeed on this single-node (no cluster attached) deployment too.
	var gotTimeout time.Duration
	invoked := make(chan struct{}, 1)
	db.SetDrainFunc(func(timeout time.Duration) {
		gotTimeout = timeout
		invoked <- struct{}{}
	})
	res, err := admin.Exec(`CLUSTER DRAIN WITH (TIMEOUT_MS = 1500)`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "drain_initiated" {
		t.Fatalf("result=%+v", res.Rows)
	}
	select {
	case <-invoked:
	case <-time.After(time.Second):
		t.Fatal("DrainFunc was never invoked")
	}
	if gotTimeout != 1500*time.Millisecond {
		t.Fatalf("timeout = %v, want 1.5s", gotTimeout)
	}
}

func TestClusterMaintenanceRBAC(t *testing.T) {
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
	if _, err := app.Exec(`CLUSTER MAINTENANCE ENABLE`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted maintenance toggle must be forbidden: %v", err)
	}
	if db.InMaintenanceMode() {
		t.Fatal("a forbidden toggle must not change state")
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	if _, err := admin.Exec(`BEGIN`); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(`CLUSTER MAINTENANCE ENABLE`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("toggle inside a transaction must be rejected: %v", err)
	}
	if _, err := admin.Exec(`ROLLBACK`); err != nil {
		t.Fatal(err)
	}

	res, err := admin.Exec(`CLUSTER MAINTENANCE ENABLE`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "maintenance_enabled" {
		t.Fatalf("result=%+v", res.Rows)
	}
	if !db.InMaintenanceMode() {
		t.Fatal("expected maintenance mode enabled")
	}

	res, err = admin.Exec(`CLUSTER MAINTENANCE DISABLE`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "maintenance_disabled" {
		t.Fatalf("result=%+v", res.Rows)
	}
	if db.InMaintenanceMode() {
		t.Fatal("expected maintenance mode disabled")
	}
}

func TestClusterMaintenanceBlocksWritesNotReads(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY, v STRING)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO t (id, v) VALUES ('1', 'a')`); err != nil {
		t.Fatal(err)
	}

	if _, err := s.Exec(`CLUSTER MAINTENANCE ENABLE`); err != nil {
		t.Fatal(err)
	}
	defer s.Exec(`CLUSTER MAINTENANCE DISABLE`)

	if _, err := s.Exec(`INSERT INTO t (id, v) VALUES ('2', 'b')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("insert during maintenance mode = %v, want Unavailable", err)
	}
	if _, err := s.Exec(`CREATE TABLE u (id STRING PRIMARY KEY)`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("DDL during maintenance mode = %v, want Unavailable", err)
	}
	if _, err := s.Exec(`BEGIN`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("BEGIN during maintenance mode = %v, want Unavailable", err)
	}
	res, err := s.Exec(`SELECT id, v FROM t`)
	if err != nil {
		t.Fatalf("reads must keep working during maintenance mode: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%+v", res.Rows)
	}
	res, err = s.Exec(`SELECT maintenance_mode FROM system.replication`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || !res.Rows[0][0].Bool {
		t.Fatalf("system.replication.maintenance_mode=%+v, want true", res.Rows)
	}

	if _, err := s.Exec(`CLUSTER MAINTENANCE DISABLE`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO t (id, v) VALUES ('2', 'b')`); err != nil {
		t.Fatalf("insert after disabling maintenance mode must succeed: %v", err)
	}
}

// TestDiskWatermarkTrippedBlocksWritesNotReads mirrors
// TestClusterMaintenanceBlocksWritesNotReads for the automatic
// disk-watermark reject state (cmd/nextsqld's monitor, driven here directly
// via DB.SetDiskWatermarkTripped since there is no admin SQL surface for
// it — see internal/executor/session.go requireNotMaintenance).
func TestDiskWatermarkTrippedBlocksWritesNotReads(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY, v STRING)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO t (id, v) VALUES ('1', 'a')`); err != nil {
		t.Fatal(err)
	}

	db.SetDiskWatermarkTripped(true)
	defer db.SetDiskWatermarkTripped(false)

	if _, err := s.Exec(`INSERT INTO t (id, v) VALUES ('2', 'b')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("insert while disk watermark tripped = %v, want Unavailable", err)
	}
	if _, err := s.Exec(`CREATE TABLE u (id STRING PRIMARY KEY)`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("DDL while disk watermark tripped = %v, want Unavailable", err)
	}
	res, err := s.Exec(`SELECT id, v FROM t`)
	if err != nil {
		t.Fatalf("reads must keep working while disk watermark is tripped: %v", err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("rows=%+v", res.Rows)
	}

	db.SetDiskWatermarkTripped(false)
	if _, err := s.Exec(`INSERT INTO t (id, v) VALUES ('2', 'b')`); err != nil {
		t.Fatalf("insert after clearing disk watermark must succeed: %v", err)
	}
}

// TestDiskWatermarkAndMaintenanceModeAreIndependent guards the design
// decision (TODO.md Phase 27, "Disk watermark policies") that the two
// node-local write-reject flags must not be conflated: clearing one must
// never clear the other.
func TestDiskWatermarkAndMaintenanceModeAreIndependent(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}

	db.SetDiskWatermarkTripped(true)
	if _, err := s.Exec(`CLUSTER MAINTENANCE DISABLE`); err != nil {
		t.Fatal(err)
	}
	if !db.DiskWatermarkTripped() {
		t.Fatal("CLUSTER MAINTENANCE DISABLE must not clear the disk watermark trip")
	}
	if _, err := s.Exec(`INSERT INTO t (id) VALUES ('1')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("insert while disk watermark tripped = %v, want Unavailable", err)
	}
	db.SetDiskWatermarkTripped(false)

	if _, err := s.Exec(`CLUSTER MAINTENANCE ENABLE`); err != nil {
		t.Fatal(err)
	}
	db.SetDiskWatermarkTripped(false)
	if !db.InMaintenanceMode() {
		t.Fatal("clearing the disk watermark trip must not clear maintenance mode")
	}
	if _, err := s.Exec(`INSERT INTO t (id) VALUES ('2')`); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("insert during maintenance mode = %v, want Unavailable", err)
	}
	_, _ = s.Exec(`CLUSTER MAINTENANCE DISABLE`)
}
