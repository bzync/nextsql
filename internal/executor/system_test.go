package executor

import (
	"fmt"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestSystemCapabilities(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()
	res, err := sess.Exec("SELECT * FROM system.capabilities")
	if err != nil {
		t.Fatalf("caps: %v", err)
	}
	if len(res.Columns) != 4 {
		t.Fatalf("cols %v", res.Columns)
	}
	if res.Columns[0] != "name" || res.Columns[1] != "status" {
		t.Fatalf("cols order %v", res.Columns)
	}
	if len(res.Rows) == 0 {
		t.Fatal("no rows")
	}
	// deterministic sorted
	prev := ""
	for _, r := range res.Rows {
		if r[0].Str < prev {
			t.Fatalf("not sorted %q < %q", r[0].Str, prev)
		}
		prev = r[0].Str
	}
	// check versioned columns exist for machine consumers
	found := false
	for _, r := range res.Rows {
		if r[0].Str == "system_catalog" {
			found = true
			if r[1].Str != "supported" {
				t.Fatalf("system_catalog status %q", r[1].Str)
			}
		}
	}
	if !found {
		t.Fatal("system_catalog not found")
	}
	// Capability metadata is machine truth, not a stale roadmap. Pin the
	// cross-phase entries most likely to regress while closing P26.
	caps := make(map[string][]types.Value, len(res.Rows))
	for _, r := range res.Rows {
		caps[r[0].Str] = r
	}
	for _, name := range []string{"partitions_range", "partitions_hash", "partitions_list", "system_schema_v2", "system_show_aliases"} {
		row, ok := caps[name]
		if !ok || row[1].Str != "supported" {
			t.Fatalf("capability %q = %v, want supported", name, row)
		}
	}
	if row := caps["follower_reads"]; len(row) != 4 || !contains(row[2].Str, "BOUNDED") || contains(row[2].Str, "no routing") {
		t.Fatalf("stale follower_reads capability: %v", row)
	}
	if row := caps["field_encryption_client"]; len(row) != 4 || !contains(row[2].Str, "Node.js") || !contains(row[2].Str, "PHP") {
		t.Fatalf("stale field_encryption_client capability: %v", row)
	}
	// P26 exit-gate audit: the capability registry must be authoritative for
	// version-aware clients, so every major P23/P25 surface needs its own
	// discoverable row, not just a passing mention inside another row's
	// description.
	for _, name := range []string{"vector_ivf", "vector_ivfpq", "vector_sparse", "quantized_vector_index", "mtls", "token_credentials", "oidc_broker", "audit_chain", "storage_caps"} {
		row, ok := caps[name]
		if !ok || row[1].Str != "supported" {
			t.Fatalf("capability %q = %v, want supported", name, row)
		}
	}
	// selective query
	res, err = sess.Exec("SELECT name FROM system.capabilities WHERE status='supported' LIMIT 2")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("limit %d", len(res.Rows))
	}
	// param
	res, err = sess.ExecContext(nil, "SELECT * FROM system.capabilities WHERE name=$1", []Param{{Name: "1", Value: types.StringValue("vector")}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("param %d", len(res.Rows))
	}
}

func TestSystemTablesAndColumns(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()
	// empty
	res, err := sess.Exec("SELECT * FROM system.tables")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("expected 0 tables, got %d", len(res.Rows))
	}
	_, err = sess.Exec("CREATE TABLE t (id STRING PRIMARY KEY, v STRING)")
	if err != nil {
		t.Fatal(err)
	}
	res, err = sess.Exec("SELECT * FROM system.tables")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "t" {
		t.Fatalf("t %v", res.Rows)
	}
	if res.Columns[0] != "name" {
		t.Fatalf("col name %v", res.Columns)
	}
	// columns
	res, err = sess.Exec("SELECT * FROM system.columns WHERE table_name='t'")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("cols %d", len(res.Rows))
	}
	// indexes
	_, err = sess.Exec("CREATE INDEX ix ON t(v)")
	if err != nil {
		t.Fatal(err)
	}
	res, err = sess.Exec("SELECT * FROM system.indexes")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("idx %d", len(res.Rows))
	}
	if res.Rows[0][1].Str != "ix" {
		t.Fatalf("ix name %v", res.Rows[0])
	}
	// storage redacted
	res, err = sess.Exec("SELECT * FROM system.storage")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatal("storage")
	}
	if res.Columns[6] != "encryption" {
		t.Fatalf("enc col %v", res.Columns)
	}
	if res.Rows[0][6].Str != "enabled" {
		t.Fatalf("enc val %q", res.Rows[0][6].Str)
	}
	if res.Rows[0][0].Str != "default" || contains(res.Rows[0][0].Str, dir) {
		t.Fatalf("database name leaked storage path: %q", res.Rows[0][0].Str)
	}
	s := fmt.Sprintf("%v", res.Rows[0])
	if contains(s, "dek=") {
		t.Fatalf("leaked %s", s)
	}
	// replication stubs
	res, err = sess.Exec("SELECT * FROM system.replication")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatal("repl")
	}
	res2, err := sess.Exec("SELECT * FROM system.raft")
	if err != nil {
		t.Fatal(err)
	}
	if len(res2.Rows) != 1 {
		t.Fatal("raft")
	}
	if res.Rows[0][3].Str == "" {
		t.Fatalf("leader_addr empty")
	}
	if res.Rows[0][3].Str == "[redacted]" {
	} else {
		t.Fatalf("expected redacted %q", res.Rows[0][3].Str)
	}
	// replica_health: single-node deployment reports a healthy leader.
	res, err = sess.Exec("SELECT * FROM system.replica_health")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatal("replica_health")
	}
	rh := map[string]types.Value{}
	for i, c := range res.Columns {
		rh[c] = res.Rows[0][i]
	}
	if rh["role"].Str != "leader" || !rh["healthy"].Bool || !rh["has_leader"].Bool {
		t.Fatalf("replica_health row: %+v", res.Rows[0])
	}
	if rh["replication_suspect"].Bool {
		t.Fatalf("replica_health row: expected replication_suspect=false, got %+v", res.Rows[0])
	}
}

func TestSystemShowAliases(t *testing.T) {
	db := testDB(t)
	db.SetDatabaseName("production")
	sess := db.Session()
	execOK(t, sess, `CREATE TABLE show_t (id STRING PRIMARY KEY, v STRING)`)
	execOK(t, sess, `CREATE INDEX show_ix ON show_t(v)`)

	tests := []struct {
		show      string
		selectSQL string
		columns   []string
	}{
		{`SHOW DATABASES`, `SELECT * FROM system.storage`, []string{"database"}},
		{`SHOW TABLES`, `SELECT * FROM system.tables`, []string{"name", "id", "column_count", "pk", "legacy_tenant_column"}},
		{`SHOW INDEXES`, `SELECT * FROM system.indexes`, []string{"table_name", "index_name", "kind", "is_unique", "columns", "include_columns", "predicate", "status"}},
		{`SHOW CONNECTIONS`, `SELECT * FROM system.sessions`, []string{"session_id", "user", "remote", "state"}},
		{`SHOW QUERIES`, `SELECT * FROM system.active_queries`, []string{"query_id", "user", "sql", "state"}},
		{`SHOW TRANSACTIONS`, `SELECT * FROM system.transactions`, []string{"txn_id", "user", "isolation", "state"}},
		{`SHOW LOCKS`, `SELECT * FROM system.locks`, []string{"lock_id", "table_name", "mode", "granted"}},
		{`SHOW CLUSTER`, `SELECT * FROM system.replication`, []string{"node_id", "state", "leader_id", "leader_addr", "voters", "applied_lsn", "has_leader", "maintenance_mode"}},
		{`SHOW STORAGE`, `SELECT * FROM system.storage`, []string{"database", "engine", "page_size", "page_count", "file_size", "wal_lsn", "encryption"}},
	}
	for _, tt := range tests {
		t.Run(tt.show, func(t *testing.T) {
			got := execOK(t, sess, tt.show)
			want := execOK(t, sess, tt.selectSQL)
			if fmt.Sprint(got.Columns) != fmt.Sprint(tt.columns) {
				t.Fatalf("columns = %v, want %v", got.Columns, tt.columns)
			}
			if tt.show == `SHOW QUERIES` {
				// The alias and direct SELECT necessarily report different SQL
				// text for the querying session; their stable schema is the
				// source-of-truth assertion for this live view.
				return
			}
			if tt.show == `SHOW DATABASES` {
				if len(got.Rows) != len(want.Rows) {
					t.Fatalf("row count = %d, want %d", len(got.Rows), len(want.Rows))
				}
				for i := range got.Rows {
					if len(got.Rows[i]) != 1 || len(want.Rows[i]) == 0 || fmt.Sprint(got.Rows[i][0]) != fmt.Sprint(want.Rows[i][0]) {
						t.Fatalf("database row[%d] = %v, storage row = %v", i, got.Rows[i], want.Rows[i])
					}
				}
				if len(got.Rows) != 1 || got.Rows[0][0].Str != "production" {
					t.Fatalf("logical database name = %v, want production", got.Rows)
				}
				return
			}
			if fmt.Sprint(got.Columns) != fmt.Sprint(want.Columns) || fmt.Sprint(got.Rows) != fmt.Sprint(want.Rows) {
				t.Fatalf("alias mismatch:\nshow=%v %v\nselect=%v %v", got.Columns, got.Rows, want.Columns, want.Rows)
			}
		})
	}
}

func TestSystemRBAC(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()
	_, err = sess.Exec("CREATE TABLE t (id STRING PRIMARY KEY, v STRING)")
	if err != nil {
		t.Fatal(err)
	}
	aclPath := dir + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("bob", security.PrivSelect, security.ScopeTable, "t")
	acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, "")
	// bob sees table
	sess2 := db.Session()
	sess2.SetACL(acl)
	sess2.SetIdentity("bob")
	res, err := sess2.Exec("SELECT * FROM system.tables")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("bob %d", len(res.Rows))
	}
	showBob := execOK(t, sess2, "SHOW TABLES")
	if fmt.Sprint(showBob.Rows) != fmt.Sprint(res.Rows) {
		t.Fatalf("SHOW TABLES bypassed/changed bob visibility: show=%v select=%v", showBob.Rows, res.Rows)
	}
	// alice sees 0
	sess3 := db.Session()
	sess3.SetACL(acl)
	sess3.SetIdentity("alice")
	res, err = sess3.Exec("SELECT * FROM system.tables")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("alice %d", len(res.Rows))
	}
	if rows := execOK(t, sess3, "SHOW TABLES").Rows; len(rows) != 0 {
		t.Fatalf("SHOW TABLES bypassed alice visibility: %v", rows)
	}
	// alice can see capabilities (requires CONNECT)
	res, err = sess3.Exec("SELECT * FROM system.capabilities")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) == 0 {
		t.Fatal("alice caps")
	}
	// charlie no grant -> denied
	sess4 := db.Session()
	sess4.SetACL(acl)
	sess4.SetIdentity("charlie")
	_, err = sess4.Exec("SELECT * FROM system.capabilities")
	if err == nil {
		t.Fatal("charlie should deny")
	}
	// columns also filtered
	res, err = sess3.Exec("SELECT * FROM system.columns WHERE table_name='t'")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("alice cols %d", len(res.Rows))
	}
	res, err = sess2.Exec("SELECT * FROM system.columns WHERE table_name='t'")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("bob cols %d", len(res.Rows))
	}
	// indexes filtered
	_, err = sess.Exec("CREATE INDEX ix2 ON t(v)")
	if err != nil {
		t.Fatal(err)
	}
	res, err = sess3.Exec("SELECT * FROM system.indexes")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 0 {
		t.Fatalf("alice idx %d", len(res.Rows))
	}
	res, err = sess2.Exec("SELECT * FROM system.indexes")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("bob idx %d %v", len(res.Rows), res.Rows)
	}
}

// TestSystemCatalogRBACRemainingViews closes a P26 exit-gate coverage gap:
// system.table_stats, system.index_stats, and system.partitions share
// canSeeTable with system.tables/columns/indexes (already pinned by
// TestSystemRBAC), and system.workflows uses the separate canSeeWorkflow
// gate — none of the four had its own dedicated RBAC assertion.
func TestSystemCatalogRBACRemainingViews(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()
	execOK(t, sess, `CREATE TABLE t (
		region STRING NOT NULL,
		id STRING NOT NULL,
		v STRING,
		PRIMARY KEY (region, id)
	) PARTITION BY LIST (region) (
		PARTITION p1 VALUES IN ('a')
	)`)
	execOK(t, sess, "CREATE INDEX ix_t_v ON t(v)")
	execOK(t, sess, "CREATE WORKFLOW wf(id STRING) AS BEGIN INSERT INTO t (region, id, v) VALUES ('a', $id, 'x'); END")

	aclPath := dir + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("bob", security.PrivSelect, security.ScopeTable, "t"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("bob", security.PrivExecute, security.ScopeFunction, "wf"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	alice := db.Session()
	alice.SetACL(acl)
	alice.SetIdentity("alice")

	for _, view := range []string{"system.table_stats", "system.index_stats", "system.partitions", "system.workflows"} {
		bobRes := execOK(t, bob, "SELECT * FROM "+view)
		if len(bobRes.Rows) == 0 {
			t.Fatalf("bob should see rows in %s, got none", view)
		}
		aliceRes := execOK(t, alice, "SELECT * FROM "+view)
		if len(aliceRes.Rows) != 0 {
			t.Fatalf("alice should see no rows in %s, got %v", view, aliceRes.Rows)
		}
	}
}

func TestSystemRedacted(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sess := db.Session()
	res, err := sess.Exec("SELECT * FROM system.storage")
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range res.Columns {
		if col == "key" || col == "dek" || col == "secret" {
			t.Fatalf("sensitive col %q", col)
		}
	}
	s := fmt.Sprintf("%v", res.Rows[0])
	for _, bad := range []string{"dek=", "key=", "secret", "password"} {
		if contains(s, bad) {
			t.Fatalf("leaked %q in %s", bad, s)
		}
	}
	if got := res.Rows[0][0].Str; got != "default" || contains(got, dir) {
		t.Fatalf("database name leaked storage path: %q", got)
	}
	res, err = sess.Exec("SELECT * FROM system.replication")
	if err != nil {
		t.Fatal(err)
	}
	s = fmt.Sprintf("%v", res.Rows[0])
	if contains(s, "dek=") {
		t.Fatalf("leaked repl %s", s)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestSystemSessionsAndActiveQueriesLive covers system.sessions and
// system.active_queries: an idle registered session shows up idle with its
// user/remote, and the querying session's own SELECT shows up in both
// tables as its running statement.
func TestSystemSessionsAndActiveQueriesLive(t *testing.T) {
	db := testDB(t)

	idle := db.Session()
	idle.SetIdentity("idlebob")
	idle.SetRemote("10.0.0.1:5555")
	idleID := db.RegisterSession(idle)
	if idleID == 0 {
		t.Fatal("expected nonzero session id")
	}
	defer db.UnregisterSession(idleID)
	if idle.SessionID() != idleID {
		t.Fatalf("SessionID() = %d, want %d", idle.SessionID(), idleID)
	}
	if idle.ConnectedAt().IsZero() {
		t.Fatal("ConnectedAt should be set after registration")
	}

	querier := db.Session()
	querier.SetIdentity("queryalice")
	querierID := db.RegisterSession(querier)
	defer db.UnregisterSession(querierID)

	live := db.LiveSessions()
	if len(live) != 2 {
		t.Fatalf("LiveSessions() = %d, want 2", len(live))
	}

	res := execOK(t, querier, "SELECT * FROM system.sessions")
	if len(res.Columns) != 4 {
		t.Fatalf("cols %v", res.Columns)
	}
	var sawIdle, sawSelf bool
	for _, r := range res.Rows {
		switch r[1].Str {
		case "idlebob":
			sawIdle = true
			if r[2].Str != "10.0.0.1:5555" {
				t.Fatalf("idle remote = %q", r[2].Str)
			}
			if r[3].Str != "idle" {
				t.Fatalf("idle state = %q, want idle", r[3].Str)
			}
		case "queryalice":
			sawSelf = true
			if r[3].Str != "active" {
				t.Fatalf("querier state = %q, want active", r[3].Str)
			}
		}
	}
	if !sawIdle || !sawSelf {
		t.Fatalf("missing rows: idle=%v self=%v rows=%v", sawIdle, sawSelf, res.Rows)
	}

	res2 := execOK(t, querier, "SELECT * FROM system.active_queries")
	if len(res2.Columns) != 4 {
		t.Fatalf("cols %v", res2.Columns)
	}
	var foundSelf bool
	for _, r := range res2.Rows {
		if r[1].Str == "idlebob" {
			t.Fatalf("idle session must not appear in active_queries: %v", r)
		}
		if r[1].Str == "queryalice" {
			foundSelf = true
			if !contains(r[2].Str, "system.active_queries") {
				t.Fatalf("active query sql = %q", r[2].Str)
			}
			if r[3].Str != "running" {
				t.Fatalf("active query state = %q, want running", r[3].Str)
			}
		}
	}
	if !foundSelf {
		t.Fatalf("querier missing from active_queries: %v", res2.Rows)
	}

	// After unregistering, the session disappears from both tables.
	db.UnregisterSession(idleID)
	res3 := execOK(t, querier, "SELECT * FROM system.sessions")
	for _, r := range res3.Rows {
		if r[1].Str == "idlebob" {
			t.Fatalf("unregistered session still visible: %v", res3.Rows)
		}
	}
}

// TestSystemTransactionsLive covers system.transactions: an explicit BEGIN
// on one session is visible to another session querying system.transactions
// (with its isolation level), and disappears after ROLLBACK.
func TestSystemTransactionsLive(t *testing.T) {
	db := testDB(t)

	txnSess := db.Session()
	txnSess.SetIdentity("txnbob")
	txnID := db.RegisterSession(txnSess)
	defer db.UnregisterSession(txnID)

	querier := db.Session()
	querier.SetIdentity("queryalice")
	qID := db.RegisterSession(querier)
	defer db.UnregisterSession(qID)

	execOK(t, txnSess, "BEGIN")

	res := execOK(t, querier, "SELECT * FROM system.transactions")
	if len(res.Columns) != 4 {
		t.Fatalf("cols %v", res.Columns)
	}
	var found bool
	for _, r := range res.Rows {
		if r[1].Str == "txnbob" {
			found = true
			if r[0].Str == "" {
				t.Fatal("txn_id empty")
			}
			if r[2].Str == "" {
				t.Fatal("isolation empty")
			}
			if r[3].Str != "active" {
				t.Fatalf("state = %q, want active", r[3].Str)
			}
		}
	}
	if !found {
		t.Fatalf("txnbob missing from system.transactions: %v", res.Rows)
	}

	execOK(t, txnSess, "ROLLBACK")

	res2 := execOK(t, querier, "SELECT * FROM system.transactions")
	for _, r := range res2.Rows {
		if r[1].Str == "txnbob" {
			t.Fatalf("txn still visible after ROLLBACK: %v", res2.Rows)
		}
	}
}

// TestSystemSessionsRBAC verifies a non-admin sees only their own sessions,
// active queries, and transactions, while an admin sees every session's.
func TestSystemSessionsRBAC(t *testing.T) {
	db := testDB(t)

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("root", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("root", security.PrivAdmin, security.ScopeCluster, "")

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	bobID := db.RegisterSession(bob)
	defer db.UnregisterSession(bobID)

	alice := db.Session()
	alice.SetACL(acl)
	alice.SetIdentity("alice")
	aliceID := db.RegisterSession(alice)
	defer db.UnregisterSession(aliceID)

	execOK(t, alice, "BEGIN")
	defer func() { _, _ = alice.Exec("ROLLBACK") }()

	// bob (non-admin) sees only his own session, not alice's.
	res := execOK(t, bob, "SELECT * FROM system.sessions")
	for _, r := range res.Rows {
		if r[1].Str == "alice" {
			t.Fatalf("bob should not see alice's session: %v", res.Rows)
		}
	}
	var sawBob bool
	for _, r := range res.Rows {
		if r[1].Str == "bob" {
			sawBob = true
		}
	}
	if !sawBob {
		t.Fatalf("bob should see his own session: %v", res.Rows)
	}

	// bob should not see alice's open transaction.
	resT := execOK(t, bob, "SELECT * FROM system.transactions")
	for _, r := range resT.Rows {
		if r[1].Str == "alice" {
			t.Fatalf("bob should not see alice's transaction: %v", resT.Rows)
		}
	}

	// root (admin) sees both.
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")
	rootID := db.RegisterSession(root)
	defer db.UnregisterSession(rootID)

	resAdmin := execOK(t, root, "SELECT * FROM system.transactions")
	var sawAlice bool
	for _, r := range resAdmin.Rows {
		if r[1].Str == "alice" {
			sawAlice = true
		}
	}
	if !sawAlice {
		t.Fatalf("admin should see alice's transaction: %v", resAdmin.Rows)
	}
}

// TestSystemChangeStreamsLive covers system.change_streams: an open
// SUBSCRIBE shows up as an active row for its table, and disappears once
// the subscription is closed.
func TestSystemChangeStreamsLive(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE cdc_watch (id STRING PRIMARY KEY, note STRING NOT NULL)`)

	res, err := s.Query(`SUBSCRIBE TO cdc_watch`)
	if err != nil {
		t.Fatal(err)
	}

	querier := db.Session()
	out := execOK(t, querier, "SELECT * FROM system.change_streams")
	if len(out.Columns) != 3 {
		t.Fatalf("cols %v", out.Columns)
	}
	var found bool
	for _, r := range out.Rows {
		if r[0].Str == "cdc_watch" {
			found = true
			if r[2].Str != "active" {
				t.Fatalf("state = %q, want active", r[2].Str)
			}
		}
	}
	if !found {
		t.Fatalf("cdc_watch missing from system.change_streams: %v", out.Rows)
	}

	res.Close()

	out2 := execOK(t, querier, "SELECT * FROM system.change_streams")
	for _, r := range out2.Rows {
		if r[0].Str == "cdc_watch" {
			t.Fatalf("cdc_watch should be gone after Close: %v", out2.Rows)
		}
	}
}

func TestSystemTasks(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(t.TempDir() + "/acl")
	if err != nil {
		t.Fatal(err)
	}
	for _, grant := range []struct {
		user  string
		priv  security.Privilege
		scope security.ScopeKind
		obj   string
	}{
		{"app", security.PrivConnect, security.ScopeDatabase, ""},
		{"app", security.PrivCreate, security.ScopeDatabase, ""},
		{"app", security.PrivExecute, security.ScopeFunction, "record_sys"},
		{"app", security.PrivInsert, security.ScopeTable, "sink_sys"},
		{"other", security.PrivConnect, security.ScopeDatabase, ""},
		{"dba", security.PrivAdmin, security.ScopeCluster, ""},
	} {
		if err := acl.Grant(grant.user, grant.priv, grant.scope, grant.obj); err != nil {
			t.Fatal(err)
		}
	}
	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	execOK(t, app, `CREATE TABLE sink_sys (id STRING PRIMARY KEY)`)
	execOK(t, app, `CREATE WORKFLOW record_sys(id STRING) AS BEGIN INSERT INTO sink_sys (id) VALUES ($id); END`)
	execOK(t, app, `CREATE SCHEDULE task_sys EVERY '1h' RUN WORKFLOW record_sys('x')`)
	_, id := createDueScheduledTask(t, db, "task_sys")

	// Owner sees the task, in autocommit mode.
	res := execOK(t, app, `SELECT * FROM system.tasks`)
	if len(res.Rows) != 1 {
		t.Fatalf("app rows=%+v", res.Rows)
	}
	row := res.Rows[0]
	if row[0].Str != id || row[1].Str != "task_sys" || row[2].Str != "record_sys" || row[3].Str != "PENDING" {
		t.Fatalf("unexpected row %+v", row)
	}
	if row[4].Dec.String() != "0" {
		t.Fatalf("unexpected attempts %+v", row[4])
	}

	// Also visible from inside an explicit read transaction.
	execOK(t, app, `BEGIN`)
	res = execOK(t, app, `SELECT id FROM system.tasks WHERE id='`+id+`'`)
	if len(res.Rows) != 1 {
		t.Fatalf("in-txn rows=%+v", res.Rows)
	}
	execOK(t, app, `COMMIT`)

	// Non-owner, non-admin sees nothing (owner isolation, same as SHOW TASKS).
	other := db.Session()
	other.SetIdentity("other")
	other.SetACL(acl)
	if rows := execOK(t, other, `SELECT * FROM system.tasks`).Rows; len(rows) != 0 {
		t.Fatalf("other rows=%+v", rows)
	}

	// Admin sees every task regardless of owner.
	dba := db.Session()
	dba.SetIdentity("dba")
	dba.SetACL(acl)
	if rows := execOK(t, dba, `SELECT * FROM system.tasks`).Rows; len(rows) != 1 {
		t.Fatalf("dba rows=%+v", rows)
	}
}

// TestSystemLocksLive covers system.locks: an FK check inside an open,
// uncommitted transaction always takes a real lock (unlike ordinary
// single-writer INSERT/UPDATE, which skips locking when it is the only
// active writer), so a second session can observe it mid-transaction and
// see it disappear after COMMIT.
func TestSystemLocksLive(t *testing.T) {
	db := testDB(t)

	holder := db.Session()
	holder.SetIdentity("holderuser")
	holderID := db.RegisterSession(holder)
	defer db.UnregisterSession(holderID)

	querier := db.Session()
	querier.SetIdentity("querieruser")
	qID := db.RegisterSession(querier)
	defer db.UnregisterSession(qID)

	execOK(t, holder, `CREATE TABLE parent (id STRING PRIMARY KEY)`)
	execOK(t, holder, `CREATE TABLE child (id STRING PRIMARY KEY, parent_id STRING REFERENCES parent (id))`)
	execOK(t, holder, `INSERT INTO parent (id) VALUES ('p1')`)

	execOK(t, holder, `BEGIN`)
	execOK(t, holder, `INSERT INTO child (id, parent_id) VALUES ('c1', 'p1')`)

	res := execOK(t, querier, `SELECT * FROM system.locks`)
	if len(res.Columns) != 4 {
		t.Fatalf("cols %v", res.Columns)
	}
	var found bool
	for _, r := range res.Rows {
		if r[1].Str != "parent" {
			continue
		}
		found = true
		if r[2].Str != "shared" {
			t.Fatalf("mode = %q, want shared", r[2].Str)
		}
		if !r[3].Bool {
			t.Fatal("granted should be true")
		}
		if r[0].Str == "" {
			t.Fatal("lock_id empty")
		}
	}
	if !found {
		t.Fatalf("parent lock missing from system.locks: %v", res.Rows)
	}

	execOK(t, holder, `COMMIT`)

	res2 := execOK(t, querier, `SELECT * FROM system.locks`)
	for _, r := range res2.Rows {
		if r[1].Str == "parent" {
			t.Fatalf("lock still visible after COMMIT: %v", res2.Rows)
		}
	}
}

// TestSystemLocksRBAC verifies a non-admin only sees locks held by their own
// user's transactions, while an admin sees every lock, matching
// system.transactions.
func TestSystemLocksRBAC(t *testing.T) {
	db := testDB(t)

	setup := db.Session()
	execOK(t, setup, `CREATE TABLE parent (id STRING PRIMARY KEY)`)
	execOK(t, setup, `CREATE TABLE child (id STRING PRIMARY KEY, parent_id STRING REFERENCES parent (id))`)
	execOK(t, setup, `INSERT INTO parent (id) VALUES ('p1')`)

	holder := db.Session()
	holder.SetIdentity("alice")
	holderID := db.RegisterSession(holder)
	defer db.UnregisterSession(holderID)

	execOK(t, holder, `BEGIN`)
	execOK(t, holder, `INSERT INTO child (id, parent_id) VALUES ('c1', 'p1')`)
	defer func() { _, _ = holder.Exec("ROLLBACK") }()

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("root", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("root", security.PrivAdmin, security.ScopeCluster, "")

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	bobID := db.RegisterSession(bob)
	defer db.UnregisterSession(bobID)

	resBob := execOK(t, bob, `SELECT * FROM system.locks`)
	for _, r := range resBob.Rows {
		if r[1].Str == "parent" {
			t.Fatalf("bob should not see alice's lock: %v", resBob.Rows)
		}
	}

	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")
	rootID := db.RegisterSession(root)
	defer db.UnregisterSession(rootID)

	resRoot := execOK(t, root, `SELECT * FROM system.locks`)
	var sawParent bool
	for _, r := range resRoot.Rows {
		if r[1].Str == "parent" {
			sawParent = true
		}
	}
	if !sawParent {
		t.Fatalf("admin should see alice's lock: %v", resRoot.Rows)
	}
}

// TestSystemUsersRolesGrants covers the P26 exit-gate security-dashboard
// gap: before system.users/system.roles/system.grants landed, a
// Studio/Manager implementation had no official SQL-level way to list
// users/roles/grants at all and would have had to read the auth.Store /
// security.ACL files directly. All three are admin-only.
func TestSystemUsersRolesGrants(t *testing.T) {
	db := testDB(t)

	usersPath := t.TempDir() + "/users.db"
	users, err := auth.Create(usersPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("alice", "s3cretalice"); err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("root", "s3cretroot123"); err != nil {
		t.Fatal(err)
	}

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("root", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("root", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.CreateRole("reporting"); err != nil {
		t.Fatal(err)
	}
	if err := acl.GrantRole("reporting", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("reporting", security.PrivSelect, security.ScopeTable, "orders"); err != nil {
		t.Fatal(err)
	}

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetAuth(users)
	bob.SetIdentity("bob")

	for _, sql := range []string{"SELECT * FROM system.users", "SELECT * FROM system.roles", "SELECT * FROM system.grants"} {
		res := execOK(t, bob, sql)
		if len(res.Rows) != 0 {
			t.Fatalf("non-admin bob must see zero rows from %q, got %v", sql, res.Rows)
		}
	}

	root := db.Session()
	root.SetACL(acl)
	root.SetAuth(users)
	root.SetIdentity("root")

	resUsers := execOK(t, root, "SELECT * FROM system.users")
	if len(resUsers.Rows) != 2 || resUsers.Rows[0][0].Str != "alice" || resUsers.Rows[1][0].Str != "root" {
		t.Fatalf("admin system.users = %v", resUsers.Rows)
	}
	if resUsers.Rows[0][1].Str != "argon2id" {
		t.Fatalf("admin system.users algo = %v", resUsers.Rows[0])
	}

	resRoles := execOK(t, root, "SELECT * FROM system.roles")
	if len(resRoles.Rows) != 1 || resRoles.Rows[0][0].Str != "reporting" || resRoles.Rows[0][1].Str != "alice" {
		t.Fatalf("admin system.roles = %v", resRoles.Rows)
	}

	resGrants := execOK(t, root, "SELECT * FROM system.grants")
	found := false
	for _, r := range resGrants.Rows {
		if r[0].Str == "reporting" && r[1].Str == "select" && r[2].Str == "table" && r[3].Str == "orders" {
			found = true
		}
		// password material must never leak through any grants column.
		for _, v := range r {
			if v.Str == "s3cretalice" || v.Str == "s3cretroot123" {
				t.Fatalf("password leaked into system.grants: %v", r)
			}
		}
	}
	if !found {
		t.Fatalf("admin system.grants missing reporting SELECT orders row: %v", resGrants.Rows)
	}
}
