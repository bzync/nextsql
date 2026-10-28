package executor

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/bzync/nextsql/internal/security"
)

func TestKillSessionDisconnectsLiveSession(t *testing.T) {
	db := newCancelDB(t)
	setup := db.Session()
	mustExec(t, setup, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, setup, "INSERT INTO t VALUES (1, 10)")

	victim := db.Session()
	vid := db.RegisterSession(victim)
	t.Cleanup(func() { db.UnregisterSession(vid) })

	var killed atomic.Bool
	victim.SetOnKill(func() {
		killed.Store(true)
		if victim.InTxn() {
			_ = victim.Abort()
		}
		db.UnregisterSession(vid)
	})

	mustExec(t, victim, "BEGIN")
	mustExec(t, victim, "UPDATE t SET n = 20 WHERE id = 1")

	observer := db.Session()
	res, err := observer.Exec("KILL SESSION '" + stringFromUint64(vid) + "'")
	if err != nil {
		t.Fatalf("KILL SESSION: %v", err)
	}
	if res.Affected != 1 {
		t.Fatalf("expected affected 1, got %d", res.Affected)
	}
	if !killed.Load() {
		t.Fatal("onKill was not invoked")
	}

	// Victim's uncommitted update must have aborted, row remains 10.
	check := db.Session()
	rows := rowsOf(t, check, "SELECT n FROM t WHERE id = 1")
	if len(rows) != 1 || rows[0][0] != "10" {
		t.Fatalf("expected uncommitted transaction to roll back, got %v", rows)
	}
}

func TestCancelSessionAlternativeSyntax(t *testing.T) {
	db := newCancelDB(t)
	s1 := db.Session()
	id1 := db.RegisterSession(s1)
	t.Cleanup(func() { db.UnregisterSession(id1) })

	var killed1 atomic.Bool
	s1.SetOnKill(func() {
		killed1.Store(true)
		db.UnregisterSession(id1)
	})

	observer := db.Session()
	res, err := observer.Exec("CANCEL SESSION '" + stringFromUint64(id1) + "'")
	if err != nil {
		t.Fatalf("CANCEL SESSION: %v", err)
	}
	if res.Affected != 1 || !killed1.Load() {
		t.Fatalf("CANCEL SESSION failed: affected=%d, killed=%v", res.Affected, killed1.Load())
	}

	s2 := db.Session()
	id2 := db.RegisterSession(s2)
	t.Cleanup(func() { db.UnregisterSession(id2) })

	var killed2 atomic.Bool
	s2.SetOnKill(func() {
		killed2.Store(true)
		db.UnregisterSession(id2)
	})

	res, err = observer.Exec("KILL CONNECTION '" + stringFromUint64(id2) + "'")
	if err != nil {
		t.Fatalf("KILL CONNECTION: %v", err)
	}
	if res.Affected != 1 || !killed2.Load() {
		t.Fatalf("KILL CONNECTION failed: affected=%d, killed=%v", res.Affected, killed2.Load())
	}
}

func TestKillSessionUnknownIDIsNotAnError(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	res, err := sess.Exec("KILL SESSION '999999'")
	if err != nil {
		t.Fatalf("killing a non-existent session reported an error: %v", err)
	}
	if res.Affected != 0 {
		t.Fatalf("expected affected 0, got %d", res.Affected)
	}
}

func TestKillSessionRejectsInvalidID(t *testing.T) {
	db := newCancelDB(t)
	sess := db.Session()
	for _, sql := range []string{
		"KILL SESSION 'abc'",
		"KILL SESSION ''",
		"KILL SESSION '-1'",
		"KILL SESSION",
		"CANCEL SESSION 'abc'",
		"CANCEL SESSION ''",
	} {
		if _, err := sess.Exec(sql); err == nil {
			t.Fatalf("accepted invalid kill statement: %s", sql)
		}
	}
}

func TestKillSessionRBAC(t *testing.T) {
	db := newCancelDB(t)

	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	acl.CreateRole("operator")
	acl.AddUser("alice")
	acl.AddUser("bob")
	acl.AddUser("admin")
	acl.Grant("admin", security.PrivAdmin, security.ScopeCluster, "")
	acl.Grant("operator", security.PrivConnect, security.ScopeDatabase, "")
	acl.GrantRole("operator", "alice")
	acl.GrantRole("operator", "bob")

	aliceSess := db.Session()
	aliceSess.SetIdentity("alice")
	aliceSess.SetACL(acl)
	aliceID := db.RegisterSession(aliceSess)
	t.Cleanup(func() { db.UnregisterSession(aliceID) })

	bobSess := db.Session()
	bobSess.SetIdentity("bob")
	bobSess.SetACL(acl)
	bobID := db.RegisterSession(bobSess)
	t.Cleanup(func() { db.UnregisterSession(bobID) })

	// Alice tries to kill Bob's session: must be denied
	if _, err := aliceSess.Exec("KILL SESSION '" + stringFromUint64(bobID) + "'"); err == nil {
		t.Fatal("expected permission denied when alice kills bob's session")
	}

	// Alice can kill her own session
	var aliceKilled atomic.Bool
	aliceSess.SetOnKill(func() {
		aliceKilled.Store(true)
		db.UnregisterSession(aliceID)
	})
	res, err := aliceSess.Exec("KILL SESSION '" + stringFromUint64(aliceID) + "'")
	if err != nil {
		t.Fatalf("alice killing her own session failed: %v", err)
	}
	if res.Affected != 1 || !aliceKilled.Load() {
		t.Fatalf("expected alice session killed, got affected=%d, killed=%v", res.Affected, aliceKilled.Load())
	}

	// Admin can kill Bob's session
	adminSess := db.Session()
	adminSess.SetIdentity("admin")
	adminSess.SetACL(acl)
	var bobKilled atomic.Bool
	bobSess.SetOnKill(func() {
		bobKilled.Store(true)
		db.UnregisterSession(bobID)
	})
	res, err = adminSess.Exec("KILL SESSION '" + stringFromUint64(bobID) + "'")
	if err != nil {
		t.Fatalf("admin killing bob's session failed: %v", err)
	}
	if res.Affected != 1 || !bobKilled.Load() {
		t.Fatalf("expected bob session killed by admin, got affected=%d, killed=%v", res.Affected, bobKilled.Load())
	}
}

func stringFromUint64(v uint64) string {
	b := make([]byte, 0, 20)
	return string(appendUint(b, v))
}

func appendUint(b []byte, v uint64) []byte {
	if v == 0 {
		return append(b, '0')
	}
	var a [20]byte
	i := len(a)
	for v > 0 {
		i--
		a[i] = byte('0' + v%10)
		v /= 10
	}
	return append(b, a[i:]...)
}
