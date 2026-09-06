package executor

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
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
	for _, name := range []string{"partitions_range", "partitions_hash", "partitions_list", "system_schema_v3", "system_show_aliases"} {
		row, ok := caps[name]
		if !ok || row[1].Str != "supported" {
			t.Fatalf("capability %q = %v, want supported", name, row)
		}
	}
	if row := caps["follower_reads"]; len(row) != 4 || row[1].Str != "supported" || !contains(row[2].Str, "BOUNDED") || contains(row[2].Str, "no routing") {
		t.Fatalf("stale follower_reads capability: %v", row)
	}
	if row := caps["resource_groups"]; len(row) != 4 || row[1].Str != "supported" || contains(row[2].Str, "not yet wired") {
		t.Fatalf("stale resource_groups capability: %v", row)
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
		{`SHOW REALMS`, `SELECT * FROM system.realms`, []string{"realm_id", "name", "state", "database_count", "storage_cap_bytes", "realm_root_delegated"}},
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

func TestSystemForeignKeys(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (
		region STRING NOT NULL,
		id STRING NOT NULL,
		PRIMARY KEY (region, id)
	)`)
	execOK(t, s, `CREATE TABLE kids (
		kid STRING PRIMARY KEY,
		p_region STRING NOT NULL,
		p_id STRING NOT NULL,
		CONSTRAINT fk_kids FOREIGN KEY (p_region, p_id)
			REFERENCES parents (region, id)
			ON DELETE CASCADE ON UPDATE RESTRICT
	)`)
	execOK(t, s, `CREATE TABLE notes (
		id STRING PRIMARY KEY,
		kid_ref STRING REFERENCES kids (kid) ON DELETE SET NULL
	)`)

	res := execOK(t, s, "SELECT * FROM system.foreign_keys")
	wantCols := []string{"table_name", "constraint_name", "ordinal", "column_name", "ref_table", "ref_column", "on_delete", "on_update"}
	if fmt.Sprint(res.Columns) != fmt.Sprint(wantCols) {
		t.Fatalf("columns = %v, want %v", res.Columns, wantCols)
	}
	got := make([][]string, 0, len(res.Rows))
	for _, r := range res.Rows {
		got = append(got, []string{
			r[0].Str, r[1].Str, r[2].Dec.String(), r[3].Str,
			r[4].Str, r[5].Str, r[6].Str, r[7].Str,
		})
	}
	want := [][]string{
		{"kids", "fk_kids", "1", "p_region", "parents", "region", "CASCADE", "RESTRICT"},
		{"kids", "fk_kids", "2", "p_id", "parents", "id", "CASCADE", "RESTRICT"},
		{"notes", "fk_notes_kid_ref", "1", "kid_ref", "kids", "kid", "SET NULL", "RESTRICT"},
	}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("rows =\n%v\nwant\n%v", got, want)
	}

	// SHOW alias parity is not defined for this view; the SELECT is the source
	// of truth. Verify the WHERE filter narrows to one child table.
	res = execOK(t, s, "SELECT * FROM system.foreign_keys WHERE table_name='notes'")
	if len(res.Rows) != 1 || res.Rows[0][1].Str != "fk_notes_kid_ref" {
		t.Fatalf("filtered rows = %v", res.Rows)
	}
}

func TestSystemForeignKeysRBAC(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE kids (id STRING PRIMARY KEY, p STRING NOT NULL REFERENCES parents (id))`)

	acl, err := security.CreateACL(dir + "/acl.db")
	if err != nil {
		t.Fatal(err)
	}
	acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("bob", security.PrivSelect, security.ScopeTable, "kids")
	acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, "")

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	if rows := execOK(t, bob, "SELECT * FROM system.foreign_keys").Rows; len(rows) != 1 || rows[0][0].Str != "kids" {
		t.Fatalf("bob should see the kids FK, got %v", rows)
	}

	alice := db.Session()
	alice.SetACL(acl)
	alice.SetIdentity("alice")
	if rows := execOK(t, alice, "SELECT * FROM system.foreign_keys").Rows; len(rows) != 0 {
		t.Fatalf("alice should see no FK rows, got %v", rows)
	}
}

func TestSystemTableDDL(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE parents (id STRING PRIMARY KEY, note STRING DEFAULT 'x')`)
	execOK(t, s, `CREATE TABLE kids (
		kid STRING PRIMARY KEY,
		p STRING NOT NULL REFERENCES parents (id) ON DELETE CASCADE,
		meta JSON
	)`)
	execOK(t, s, `CREATE INDEX kids_p ON kids (p)`)

	res := execOK(t, s, "SELECT * FROM system.table_ddl")
	wantCols := []string{"table_name", "object_type", "object_name", "ddl"}
	if fmt.Sprint(res.Columns) != fmt.Sprint(wantCols) {
		t.Fatalf("columns = %v, want %v", res.Columns, wantCols)
	}
	byKey := map[string]string{}
	for _, r := range res.Rows {
		byKey[r[1].Str+" "+r[2].Str] = r[3].Str
	}
	if got := byKey["TABLE kids"]; !strings.Contains(got, `CREATE TABLE "kids"`) ||
		!strings.Contains(got, `FOREIGN KEY ("p") REFERENCES "parents" ("id") ON DELETE CASCADE`) {
		t.Fatalf("kids table DDL = %q", got)
	}
	if got := byKey["TABLE parents"]; !strings.Contains(got, `"note" STRING DEFAULT 'x'`) {
		t.Fatalf("parents table DDL = %q", got)
	}
	if got := byKey["INDEX kids_p"]; got != `CREATE INDEX "kids_p" ON "kids" ("p")` {
		t.Fatalf("kids_p index DDL = %q", got)
	}

	// TABLE rows sort before INDEX rows within a table; ordered by table_name.
	var order []string
	for _, r := range res.Rows {
		order = append(order, r[0].Str+"/"+r[1].Str+"/"+r[2].Str)
	}
	want := []string{"kids/TABLE/kids", "kids/INDEX/kids_p", "parents/TABLE/parents"}
	if fmt.Sprint(order) != fmt.Sprint(want) {
		t.Fatalf("row order = %v, want %v", order, want)
	}

	// The rendered DDL round-trips: dropping and recreating from it works.
	execOK(t, s, "DROP TABLE kids")
	execOK(t, s, byKey["TABLE kids"])
	execOK(t, s, byKey["INDEX kids_p"])
}

func TestSystemTableDDLRBAC(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE visible (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE hidden (id STRING PRIMARY KEY)`)

	acl, err := security.CreateACL(dir + "/acl.db")
	if err != nil {
		t.Fatal(err)
	}
	acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("bob", security.PrivSelect, security.ScopeTable, "visible")

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	rows := execOK(t, bob, "SELECT * FROM system.table_ddl").Rows
	if len(rows) != 1 || rows[0][0].Str != "visible" {
		t.Fatalf("bob should see only the visible table DDL, got %v", rows)
	}
}

func TestSystemTriggersAndSchedules(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY, state STRING)`)
	execOK(t, s, `CREATE TABLE audit_log (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW record(id STRING) AS BEGIN INSERT INTO audit_log (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER audit_ins AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW record(NEW.id)`)
	execOK(t, s, `CREATE SCHEDULE nightly EVERY '1h' RUN WORKFLOW record('sched')`)

	trg := execOK(t, s, "SELECT * FROM system.triggers")
	wantTrgCols := []string{"name", "owner", "timing", "event", "table_name", "workflow", "arg_count"}
	if fmt.Sprint(trg.Columns) != fmt.Sprint(wantTrgCols) {
		t.Fatalf("triggers columns = %v, want %v", trg.Columns, wantTrgCols)
	}
	if len(trg.Rows) != 1 {
		t.Fatalf("triggers rows = %v", trg.Rows)
	}
	r := trg.Rows[0]
	if r[0].Str != "audit_ins" || r[2].Str != "AFTER" || r[3].Str != "INSERT" || r[4].Str != "orders" || r[5].Str != "record" || r[6].Dec.String() != "1" {
		t.Fatalf("trigger row = %v", r)
	}

	sch := execOK(t, s, "SELECT * FROM system.schedules")
	wantSchCols := []string{"name", "owner", "kind", "spec", "workflow", "enabled", "next_fire", "last_fire"}
	if fmt.Sprint(sch.Columns) != fmt.Sprint(wantSchCols) {
		t.Fatalf("schedules columns = %v, want %v", sch.Columns, wantSchCols)
	}
	if len(sch.Rows) != 1 {
		t.Fatalf("schedules rows = %v", sch.Rows)
	}
	sr := sch.Rows[0]
	if sr[0].Str != "nightly" || sr[2].Str != "EVERY" || sr[3].Str != "1h0m0s" || sr[4].Str != "record" || !sr[5].Bool {
		t.Fatalf("schedule row = %v", sr)
	}
	if sr[7].Typ.Kind != types.KindTimestampTZ || !sr[7].Null {
		t.Fatalf("last_fire should be a typed NULL before first run: %+v", sr[7])
	}

	// A trigger is not visible once its table is gone from view.
	execOK(t, s, "DROP TRIGGER audit_ins")
	if rows := execOK(t, s, "SELECT * FROM system.triggers").Rows; len(rows) != 0 {
		t.Fatalf("dropped trigger still listed: %v", rows)
	}
}

func TestSystemTriggersAndSchedulesRBAC(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	s := db.Session()
	execOK(t, s, `CREATE TABLE orders (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE TABLE sink (id STRING PRIMARY KEY)`)
	execOK(t, s, `CREATE WORKFLOW w(id STRING) AS BEGIN INSERT INTO sink (id) VALUES ($id); END`)
	execOK(t, s, `CREATE TRIGGER t_orders AFTER INSERT ON orders FOR EACH ROW RUN WORKFLOW w(NEW.id)`)
	execOK(t, s, `CREATE SCHEDULE hourly EVERY '1h' RUN WORKFLOW w('scheduled')`)

	acl, err := security.CreateACL(dir + "/acl.db")
	if err != nil {
		t.Fatal(err)
	}
	acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, "")

	alice := db.Session()
	alice.SetACL(acl)
	alice.SetIdentity("alice")
	if rows := execOK(t, alice, "SELECT * FROM system.triggers").Rows; len(rows) != 0 {
		t.Fatalf("connect-only user should see no triggers, got %v", rows)
	}
	if rows := execOK(t, alice, "SELECT * FROM system.schedules").Rows; len(rows) != 0 {
		t.Fatalf("connect-only user should see no schedules, got %v", rows)
	}

	acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("bob", security.PrivSelect, security.ScopeTable, "orders")
	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	if rows := execOK(t, bob, "SELECT * FROM system.triggers").Rows; len(rows) != 1 || rows[0][0].Str != "t_orders" {
		t.Fatalf("table reader should see its trigger, got %v", rows)
	}
	if rows := execOK(t, bob, "SELECT * FROM system.schedules").Rows; len(rows) != 0 {
		t.Fatalf("table reader without workflow EXECUTE should see no schedules, got %v", rows)
	}

	acl.Grant("carol", security.PrivConnect, security.ScopeDatabase, "")
	acl.Grant("carol", security.PrivExecute, security.ScopeFunction, "w")
	carol := db.Session()
	carol.SetACL(acl)
	carol.SetIdentity("carol")
	if rows := execOK(t, carol, "SELECT * FROM system.triggers").Rows; len(rows) != 0 {
		t.Fatalf("workflow executor without table SELECT should see no triggers, got %v", rows)
	}
	if rows := execOK(t, carol, "SELECT * FROM system.schedules").Rows; len(rows) != 1 || rows[0][0].Str != "hourly" {
		t.Fatalf("workflow executor should see its schedule, got %v", rows)
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

func TestSystemTLS(t *testing.T) {
	db := testDB(t)

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

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No TLS listener attached (the default for an embedded/CLI DB, and for
	// SetTLSStatusSource never called): a non-admin sees zero rows, an
	// admin sees exactly one row reporting enabled=false rather than an
	// error, same convention as system.replication's "standalone" row.
	if res := execOK(t, bob, "SELECT * FROM system.tls"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows, got %v", res.Rows)
	}
	res := execOK(t, root, "SELECT * FROM system.tls")
	if len(res.Rows) != 1 {
		t.Fatalf("admin system.tls row count = %d, want 1: %v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	if row[0].Bool {
		t.Fatalf("enabled=true with no TLS status source wired: %v", row)
	}
	if row[1].Str != "" || row[2].Str != "" || row[6].Str != "" || !row[3].Null || !row[4].Null {
		t.Fatalf("non-empty identity fields with no TLS status source wired: %v", row)
	}

	// Wire a fixed status (as nextsqld does via
	// security.ServerTLSReloader.Status) and confirm every column reflects
	// it, with the private-key-adjacent fields (there are none in
	// TLSStatus, by construction) and network address (there is no address
	// field either) never present.
	notBefore := time.Now().Add(-24 * time.Hour).Truncate(time.Second)
	notAfter := time.Now().Add(30 * 24 * time.Hour).Truncate(time.Second)
	db.SetTLSStatusSource(func() (security.TLSStatus, bool) {
		return security.TLSStatus{
			Enabled:             true,
			Subject:             "CN=db.example.internal",
			Issuer:              "CN=Internal CA",
			NotBefore:           notBefore,
			NotAfter:            notAfter,
			DNSNames:            []string{"db.example.internal", "db2.example.internal"},
			MTLSRequired:        true,
			ClientCAConfigured:  true,
			ClientCRLConfigured: true,
		}, true
	})

	res = execOK(t, root, "SELECT * FROM system.tls")
	if len(res.Rows) != 1 {
		t.Fatalf("admin system.tls row count = %d, want 1: %v", len(res.Rows), res.Rows)
	}
	row = res.Rows[0]
	if !row[0].Bool {
		t.Fatalf("enabled=false with a TLS status source wired: %v", row)
	}
	if row[1].Str != "CN=db.example.internal" || row[2].Str != "CN=Internal CA" {
		t.Fatalf("subject/issuer = %q/%q", row[1].Str, row[2].Str)
	}
	if row[3].Null || row[3].Time != notBefore.UnixNano() {
		t.Fatalf("not_before = %v, want %v", row[3], notBefore)
	}
	if row[4].Null || row[4].Time != notAfter.UnixNano() {
		t.Fatalf("not_after = %v, want %v", row[4], notAfter)
	}
	daysStr := row[5].Dec.String()
	days, err := strconv.Atoi(daysStr)
	if row[5].Null || err != nil || days < 28 || days > 30 {
		t.Fatalf("days_until_expiry = %q (parse err %v), want ~29-30", daysStr, err)
	}
	if row[6].Str != "db.example.internal,db2.example.internal" {
		t.Fatalf("dns_names = %q", row[6].Str)
	}
	if !row[7].Bool || !row[8].Bool || !row[9].Bool {
		t.Fatalf("mtls_required/client_ca_configured/client_crl_configured not all true: %v", row)
	}

	// Never an error even for a plaintext deployment; a non-admin still
	// sees zero rows once a status source is wired.
	if res := execOK(t, bob, "SELECT * FROM system.tls"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows even with a status source wired, got %v", res.Rows)
	}
}

func TestSystemKeyVersions(t *testing.T) {
	db := testDB(t)

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

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No envelope attached (the default for the embedded/CLI testDB, and
	// for SetKeyStatusSource never called): a list-shaped table returns
	// zero rows rather than a placeholder row or an error, for admin and
	// non-admin alike — the "not applicable" convention system.databases/
	// realms already use on a non-hosted deployment.
	if res := execOK(t, root, "SELECT * FROM system.key_versions"); len(res.Rows) != 0 {
		t.Fatalf("admin system.key_versions with no source wired must be empty, got %v", res.Rows)
	}
	if res := execOK(t, bob, "SELECT * FROM system.key_versions"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows, got %v", res.Rows)
	}

	// Wire a fixed status (as nextsqld does via crypto.Envelope.KeyStatus)
	// and confirm every column reflects it, admin-only.
	db.SetKeyStatusSource(func() ([]crypto.KeyStatus, bool) {
		return []crypto.KeyStatus{
			{Domain: "kek", CurrentVersion: 1, VersionCount: 1},
			{Domain: "page", CurrentVersion: 2, VersionCount: 1, RevokedCount: 1},
		}, true
	})

	res := execOK(t, root, "SELECT * FROM system.key_versions ORDER BY key_name")
	if len(res.Rows) != 2 {
		t.Fatalf("admin system.key_versions row count = %d, want 2: %v", len(res.Rows), res.Rows)
	}
	kek, page := res.Rows[0], res.Rows[1]
	if kek[0].Str != "kek" || kek[1].Dec.String() != "1" || kek[2].Dec.String() != "1" || kek[3].Dec.String() != "0" || kek[4].Dec.String() != "0" {
		t.Fatalf("kek row = %v", kek)
	}
	if page[0].Str != "page" || page[1].Dec.String() != "2" || page[2].Dec.String() != "1" || page[3].Dec.String() != "1" || page[4].Dec.String() != "0" {
		t.Fatalf("page row = %v", page)
	}

	if res := execOK(t, bob, "SELECT * FROM system.key_versions"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows even with a status source wired, got %v", res.Rows)
	}
}

func TestSystemConfig(t *testing.T) {
	db := testDB(t)

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

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No config source wired (the default for the embedded/CLI testDB):
	// a list-shaped table returns zero rows, admin and non-admin alike.
	if res := execOK(t, root, "SELECT * FROM system.config"); len(res.Rows) != 0 {
		t.Fatalf("admin system.config with no source wired must be empty, got %v", res.Rows)
	}
	if res := execOK(t, bob, "SELECT * FROM system.config"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows, got %v", res.Rows)
	}

	// Wire a fixed set of entry states (as nextsqld does via config.DiffState)
	// and confirm they pass through unchanged — redaction itself is
	// config.TestDiffState's job, not this layer's.
	db.SetConfigSource(func() ([]config.EntryState, bool) {
		return []config.EntryState{
			{Key: "data_dir", Value: "/var/lib/nextsql", FileValue: "/var/lib/nextsql"},
			{Key: "buffer_pages", Value: "2048", FileValue: "1024", RestartRequired: true},
		}, true
	})

	res := execOK(t, root, "SELECT * FROM system.config ORDER BY name")
	if len(res.Rows) != 2 {
		t.Fatalf("admin system.config row count = %d, want 2: %v", len(res.Rows), res.Rows)
	}
	// columns: name, value, file_value, restart_required
	if res.Rows[1][0].Str != "data_dir" || res.Rows[1][1].Str != "/var/lib/nextsql" || res.Rows[1][3].Str != "no" {
		t.Fatalf("data_dir row = %v", res.Rows[1])
	}
	if res.Rows[0][0].Str != "buffer_pages" || res.Rows[0][2].Str != "1024" || res.Rows[0][3].Str != "yes" {
		t.Fatalf("buffer_pages row = %v", res.Rows[0])
	}

	if res := execOK(t, bob, "SELECT * FROM system.config"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows even with a source wired, got %v", res.Rows)
	}
}

func TestSetConfig(t *testing.T) {
	db := testDB(t)

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"bob", "root"} {
		if err := acl.Grant(u, security.PrivConnect, security.ScopeDatabase, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := acl.Grant("root", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No writer wired: SET CONFIG fails Unavailable, not a silent no-op.
	if _, err := root.Exec("SET CONFIG buffer_pages = 4096"); err == nil {
		t.Fatal("SET CONFIG with no writer wired must error")
	} else if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("want Unavailable, got %v", err)
	}

	// Wire a writer that records what it was asked to persist.
	var gotKey, gotVal string
	var gotReset bool
	db.SetConfigWriter(func(key, value string, reset bool) (ConfigWriteResult, error) {
		gotKey, gotVal, gotReset = key, value, reset
		return ConfigWriteResult{Key: key, FileValue: value, RunningValue: "1024", RestartRequired: true}, nil
	})

	// Non-admin is refused before the writer is ever called.
	gotKey = ""
	if _, err := bob.Exec("SET CONFIG buffer_pages = 4096"); err == nil {
		t.Fatal("non-admin SET CONFIG must be refused")
	}
	if gotKey != "" {
		t.Fatalf("writer called for an unauthorized SET CONFIG: %q", gotKey)
	}

	res, err := root.Exec("SET CONFIG buffer_pages = 4096")
	if err != nil {
		t.Fatal(err)
	}
	if gotKey != "buffer_pages" || gotVal != "4096" || gotReset {
		t.Fatalf("writer got key=%q val=%q reset=%v", gotKey, gotVal, gotReset)
	}
	// columns: key, file_value, running_value, restart_required
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "buffer_pages" || res.Rows[0][3].Str != "yes" {
		t.Fatalf("SET CONFIG result = %v", res.Rows)
	}

	// DEFAULT reaches the writer as reset=true.
	if _, err := root.Exec("SET CONFIG buffer_pages = DEFAULT"); err != nil {
		t.Fatal(err)
	}
	if !gotReset {
		t.Fatal("SET CONFIG ... = DEFAULT did not set reset=true")
	}

	// The writer's error propagates.
	db.SetConfigWriter(func(key, value string, reset bool) (ConfigWriteResult, error) {
		return ConfigWriteResult{}, nerr.New(nerr.InvalidArgument, "test", "bad value")
	})
	if _, err := root.Exec("SET CONFIG buffer_pages = 4096"); err == nil {
		t.Fatal("writer error must propagate")
	}
}

func TestSystemMetrics(t *testing.T) {
	db := testDB(t)

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

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No metrics source wired (the default for the embedded/CLI testDB):
	// a list-shaped table returns zero rows, admin and non-admin alike.
	if res := execOK(t, root, "SELECT * FROM system.metrics"); len(res.Rows) != 0 {
		t.Fatalf("admin system.metrics with no source wired must be empty, got %v", res.Rows)
	}
	if res := execOK(t, bob, "SELECT * FROM system.metrics"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows, got %v", res.Rows)
	}

	// Wire a registry (as nextsqld does with metrics.Default()) and record a
	// couple of observations, then confirm they surface.
	reg := metrics.New()
	reg.ObserveQuery(time.Millisecond, nil)
	reg.AddCommit()
	db.SetMetricsSource(func() *metrics.Registry { return reg })

	res := execOK(t, root, "SELECT * FROM system.metrics")
	if len(res.Rows) == 0 {
		t.Fatal("admin system.metrics must have rows once a source is wired")
	}
	seen := map[string]string{}
	for _, r := range res.Rows {
		// columns: category, name, value, unit
		seen[r[1].Str] = r[2].Str
		if r[0].Str == "" || r[3].Str == "" {
			t.Fatalf("every metric row needs a category and unit: %v", r)
		}
	}
	if seen["queries"] != "1" {
		t.Fatalf("queries metric = %q, want 1", seen["queries"])
	}
	if seen["commits"] != "1" {
		t.Fatalf("commits metric = %q, want 1", seen["commits"])
	}
	if _, ok := seen["goroutines"]; !ok {
		t.Fatal("expected a runtime goroutines metric")
	}

	if res := execOK(t, bob, "SELECT * FROM system.metrics"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows even with a source wired, got %v", res.Rows)
	}
}

func TestSystemServerLog(t *testing.T) {
	db := testDB(t)

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"bob", "root"} {
		if err := acl.Grant(u, security.PrivConnect, security.ScopeDatabase, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := acl.Grant("root", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No log source wired: zero rows, admin and non-admin alike.
	if res := execOK(t, root, "SELECT * FROM system.server_log"); len(res.Rows) != 0 {
		t.Fatalf("admin system.server_log with no source wired must be empty, got %v", res.Rows)
	}

	// Wire a ring (as nextsqld does with logging.NewWithRing) and log a line.
	l, ring := logging.NewWithRing("info", io.Discard)
	l.Info("checkpoint complete", "lsn", 4096)
	db.SetServerLogSource(ring.Snapshot)

	res := execOK(t, root, "SELECT * FROM system.server_log ORDER BY seq DESC")
	if len(res.Rows) != 1 {
		t.Fatalf("admin system.server_log row count = %d, want 1: %v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	// columns: seq, event_time, level, message, attributes
	if row[2].Str != "INFO" || row[3].Str != "checkpoint complete" {
		t.Fatalf("server_log row = level=%q msg=%q", row[2].Str, row[3].Str)
	}
	if !strings.Contains(row[4].Str, "lsn=4096") {
		t.Fatalf("server_log attributes = %q, want lsn=4096", row[4].Str)
	}

	if res := execOK(t, bob, "SELECT * FROM system.server_log"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero rows even with a source wired, got %v", res.Rows)
	}
}

func TestBackupStatementsAndSystemBackups(t *testing.T) {
	db := testDB(t)

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []string{"bob", "root", "bk"} {
		if err := acl.Grant(u, security.PrivConnect, security.ScopeDatabase, ""); err != nil {
			t.Fatal(err)
		}
	}
	if err := acl.Grant("root", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("bk", security.PrivBackup, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	mk := func(id string) *Session {
		s := db.Session()
		s.SetACL(acl)
		s.SetIdentity(id)
		return s
	}
	bob, root, bk := mk("bob"), mk("root"), mk("bk")

	// Nothing wired: BACKUP DATABASE / VERIFY BACKUP fail Unavailable,
	// system.backups is empty.
	if _, err := root.Exec("BACKUP DATABASE"); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("BACKUP DATABASE with no ops wired: want Unavailable, got %v", err)
	}
	if res := execOK(t, root, "SELECT * FROM system.backups"); len(res.Rows) != 0 {
		t.Fatalf("system.backups with nothing wired must be empty, got %v", res.Rows)
	}

	created := false
	verifiedName := ""
	db.SetBackupOps(
		func() (BackupCreateResult, error) {
			created = true
			return BackupCreateResult{Name: "backup-x", CheckpointLSN: 7, DurableLSN: 42, Members: 8}, nil
		},
		func() ([]BackupListEntry, bool) {
			return []BackupListEntry{{Name: "backup-x", CreatedUnix: 1000, DatabaseID: "db1", CheckpointLSN: 7, DurableLSN: 42}}, true
		},
		func(name string) (BackupVerifyResult, error) {
			verifiedName = name
			return BackupVerifyResult{Name: name, OK: true}, nil
		},
	)

	// Non-privileged user is refused before the op runs.
	created = false
	if _, err := bob.Exec("BACKUP DATABASE"); err == nil {
		t.Fatal("unprivileged BACKUP DATABASE must be refused")
	}
	if created {
		t.Fatal("backup ran for an unauthorized user")
	}

	// The BACKUP privilege is enough (not just cluster ADMIN).
	res, err := bk.Exec("BACKUP DATABASE")
	if err != nil {
		t.Fatal(err)
	}
	if !created || res.Rows[0][0].Str != "backup-x" || res.Rows[0][3].Dec.String() != "8" {
		t.Fatalf("BACKUP DATABASE result = %v", res.Rows)
	}

	// system.backups lists it (BACKUP-privileged user can see it too).
	rows := execOK(t, bk, "SELECT * FROM system.backups").Rows
	if len(rows) != 1 || rows[0][0].Str != "backup-x" || rows[0][2].Str != "db1" {
		t.Fatalf("system.backups rows = %v", rows)
	}
	if r := execOK(t, bob, "SELECT * FROM system.backups"); len(r.Rows) != 0 {
		t.Fatalf("unprivileged bob must see zero system.backups rows, got %v", r.Rows)
	}

	// VERIFY BACKUP passes the name through and reports the outcome.
	vr, err := root.Exec("VERIFY BACKUP 'backup-x'")
	if err != nil {
		t.Fatal(err)
	}
	if verifiedName != "backup-x" || vr.Rows[0][1].Str != "yes" {
		t.Fatalf("VERIFY BACKUP: name=%q result=%v", verifiedName, vr.Rows)
	}

	// A failed verification surfaces as verified=no + problem, not an error.
	db.SetBackupOps(nil, nil, func(name string) (BackupVerifyResult, error) {
		return BackupVerifyResult{Name: name, OK: false, Problem: "hash mismatch"}, nil
	})
	vr, err = root.Exec("VERIFY BACKUP 'bad'")
	if err != nil {
		t.Fatal(err)
	}
	if vr.Rows[0][1].Str != "no" || vr.Rows[0][2].Str != "hash mismatch" {
		t.Fatalf("failed verify = %v", vr.Rows)
	}

	// BACKUP DATABASE inside a transaction is refused.
	if _, err := root.Exec("BEGIN"); err != nil {
		t.Fatal(err)
	}
	if _, err := root.Exec("BACKUP DATABASE"); err == nil {
		t.Fatal("BACKUP DATABASE inside a transaction must be refused")
	}
	_, _ = root.Exec("ROLLBACK")
}

func TestSystemAuditVerifyAndLog(t *testing.T) {
	db := testDB(t)

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

	bob := db.Session()
	bob.SetACL(acl)
	bob.SetIdentity("bob")
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	// No audit source wired (the default for the embedded/CLI testDB):
	// audit_verify still returns its one status row (verified=false, all
	// counts zero), audit_log returns zero rows (list-shaped) — for admin
	// and non-admin alike.
	res := execOK(t, root, "SELECT * FROM system.audit_verify")
	if len(res.Rows) != 1 {
		t.Fatalf("admin system.audit_verify row count = %d, want 1: %v", len(res.Rows), res.Rows)
	}
	row := res.Rows[0]
	if row[0].Dec.String() != "0" || row[6].Bool {
		t.Fatalf("no-source-wired audit_verify row = %v", row)
	}
	if res := execOK(t, root, "SELECT * FROM system.audit_log"); len(res.Rows) != 0 {
		t.Fatalf("admin system.audit_log with no source wired must be empty, got %v", res.Rows)
	}
	if res := execOK(t, bob, "SELECT * FROM system.audit_verify"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero audit_verify rows, got %v", res.Rows)
	}

	// Wire a fixed TailReport (as nextsqld does via security.TailEvents)
	// and confirm every column reflects it, admin-only.
	when := time.Now().UTC().Truncate(time.Second)
	db.SetAuditSource(func(maxEvents int) (security.TailReport, bool) {
		full := security.TailReport{
			VerifyReport: security.VerifyReport{
				Lines: 5, Legacy: 1, Chained: 4, Signed: 2,
				SigningStarted: true, SignaturesChecked: true, Verified: true,
			},
			Events: []security.Event{
				{Seq: 3, Time: when, Actor: "app", Action: "auth.success", Object: "orders", Outcome: "success", Remote: "10.0.0.5:5000", IdentitySource: "password"},
				{Seq: 4, Time: when, Actor: "app", Action: "grant", Object: "orders", Outcome: "success", Sig: "c2ln", KeyID: 7},
			},
		}
		if maxEvents <= 0 {
			return security.TailReport{VerifyReport: full.VerifyReport}, true
		}
		return full, true
	})

	res = execOK(t, root, "SELECT * FROM system.audit_verify")
	if len(res.Rows) != 1 {
		t.Fatalf("admin system.audit_verify row count = %d, want 1: %v", len(res.Rows), res.Rows)
	}
	row = res.Rows[0]
	if row[0].Dec.String() != "5" || row[1].Dec.String() != "1" || row[2].Dec.String() != "4" || row[3].Dec.String() != "2" {
		t.Fatalf("audit_verify counts = %v", row)
	}
	if !row[4].Bool || !row[5].Bool || !row[6].Bool {
		t.Fatalf("audit_verify signing_started/signatures_checked/verified not all true: %v", row)
	}

	res = execOK(t, root, "SELECT * FROM system.audit_log ORDER BY seq")
	if len(res.Rows) != 2 {
		t.Fatalf("admin system.audit_log row count = %d, want 2: %v", len(res.Rows), res.Rows)
	}
	unsigned, signed := res.Rows[0], res.Rows[1]
	if unsigned[0].Dec.String() != "3" || unsigned[2].Str != "app" || unsigned[3].Str != "auth.success" || unsigned[4].Str != "orders" || unsigned[6].Str != "10.0.0.5:5000" || unsigned[8].Bool {
		t.Fatalf("unsigned audit_log row = %v", unsigned)
	}
	if unsigned[1].Null || unsigned[1].Time != when.UnixNano() {
		t.Fatalf("audit_log time = %v, want %v", unsigned[1], when)
	}
	if signed[0].Dec.String() != "4" || signed[3].Str != "grant" || !signed[8].Bool {
		t.Fatalf("signed audit_log row = %v", signed)
	}

	if res := execOK(t, bob, "SELECT * FROM system.audit_log"); len(res.Rows) != 0 {
		t.Fatalf("non-admin bob must see zero audit_log rows even with a source wired, got %v", res.Rows)
	}
}
