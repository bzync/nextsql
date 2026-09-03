package executor

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestSetResetResourceGroupRBACAndAssignment(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("app", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("dba", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetAuth(users)
	execOK(t, admin, `CREATE RESOURCE GROUP reporting WITH (MAX_CONCURRENCY = 1, MEMORY = 2097152, WORKERS = 3)`)

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetAuth(users)

	// The existence check applies to everyone, including the superuser.
	if _, err := admin.Exec(`SET RESOURCE GROUP nosuch`); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("unknown group: %v", err)
	}

	// A non-privileged user with no USAGE grant is denied.
	if _, err := app.Exec(`SET RESOURCE GROUP reporting`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("ungranted SET RESOURCE GROUP: %v", err)
	}
	// RESET is always allowed (no privilege needed to give up an assignment).
	execOK(t, app, `RESET RESOURCE GROUP`)

	// Cluster ADMIN bypasses the USAGE grant like every other privilege check.
	execOK(t, admin, `SET RESOURCE GROUP reporting`)
	if got := admin.ResourceGroup(); got != "reporting" {
		t.Fatalf("admin.ResourceGroup() = %q", got)
	}
	if lim := admin.limitsOrDefault(); lim.Workers != 3 || lim.Memory != 2097152 {
		t.Fatalf("admin limits after assignment = %+v", lim)
	}
	execOK(t, admin, `RESET RESOURCE GROUP`)
	if got := admin.ResourceGroup(); got != "" {
		t.Fatalf("admin.ResourceGroup() after reset = %q", got)
	}
	if lim := admin.limitsOrDefault(); lim.Workers == 3 && lim.Memory == 2097152 {
		t.Fatalf("admin limits still reflect the group after reset: %+v", lim)
	}

	// Granting USAGE lets the ordinary user switch too.
	execOK(t, admin, `GRANT USAGE ON RESOURCE GROUP reporting TO app`)
	execOK(t, app, `SET RESOURCE GROUP reporting`)
	if got := app.ResourceGroup(); got != "reporting" {
		t.Fatalf("app.ResourceGroup() = %q", got)
	}

	// Revoking USAGE blocks re-assignment (existing assignment is untouched).
	execOK(t, admin, `REVOKE USAGE ON RESOURCE GROUP reporting FROM app`)
	if _, err := app.Exec(`SET RESOURCE GROUP reporting`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("revoked SET RESOURCE GROUP: %v", err)
	}
	execOK(t, app, `RESET RESOURCE GROUP`)
}

func TestResourceGroupGateCacheTracksMaxConcurrency(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE RESOURCE GROUP tight WITH (MAX_CONCURRENCY = 1)`)
	execOK(t, s, `CREATE RESOURCE GROUP loose`)

	gate := db.resourceGroupGate("tight")
	if gate == nil || gate.Stats().Capacity != 1 {
		t.Fatalf("tight gate = %+v", gate)
	}
	// MaxConcurrency == 0 means unbounded: no gate, so the only limit
	// remaining is the process-wide db.admit — never bypassed, never
	// duplicated with a meaningless zero-capacity gate.
	if gate := db.resourceGroupGate("loose"); gate != nil {
		t.Fatalf("unbounded group must have no gate, got %+v", gate)
	}
	if gate := db.resourceGroupGate("missing"); gate != nil {
		t.Fatalf("unknown group must have no gate, got %+v", gate)
	}

	execOK(t, s, `ALTER RESOURCE GROUP tight WITH (MAX_CONCURRENCY = 4)`)
	gate = db.resourceGroupGate("tight")
	if gate == nil || gate.Stats().Capacity != 4 {
		t.Fatalf("altered tight gate = %+v", gate)
	}
}

func TestResourceGroupMaxConcurrencyBlocksExecContext(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE RESOURCE GROUP tight WITH (MAX_CONCURRENCY = 1)`)

	gate := db.resourceGroupGate("tight")
	if gate == nil {
		t.Fatal("expected a gate for a MAX_CONCURRENCY=1 group")
	}
	release, err := gate.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	s2 := db.Session()
	execOK(t, s2, `SET RESOURCE GROUP tight`)

	done := make(chan error, 1)
	go func() {
		_, err := s2.ExecContext(context.Background(), `RESET RESOURCE GROUP`, nil)
		done <- err
	}()

	select {
	case <-done:
		t.Fatal("ExecContext returned while the group's sole slot was held externally")
	case <-time.After(150 * time.Millisecond):
	}

	release()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ExecContext after release: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("ExecContext did not unblock after the gate was released")
	}
	if got := s2.ResourceGroup(); got != "" {
		t.Fatalf("s2.ResourceGroup() after RESET = %q", got)
	}
}
