package executor

import (
  "fmt"
  "testing"

  "github.com/bzync/nextsql/internal/crypto"
  "github.com/bzync/nextsql/internal/security"
  "github.com/bzync/nextsql/internal/sql/types"
)

func TestSystemCapabilities(t *testing.T) {
  dir := t.TempDir()
  dek, _ := crypto.GenerateDEK(1)
  keys, _ := crypto.NewMemoryKeyProvider(dek)
  db, err := Create(dir+"/db", keys, 16)
  if err != nil { t.Fatalf("create: %v", err) }
  defer db.Close()
  sess := db.Session()
  res, err := sess.Exec("SELECT * FROM system.capabilities")
  if err != nil { t.Fatalf("caps: %v", err) }
  if len(res.Columns)!=4 { t.Fatalf("cols %v", res.Columns) }
  if res.Columns[0]!="name" || res.Columns[1]!="status" { t.Fatalf("cols order %v", res.Columns) }
  if len(res.Rows)==0 { t.Fatal("no rows") }
  // deterministic sorted
  prev := ""
  for _, r := range res.Rows {
    if r[0].Str < prev { t.Fatalf("not sorted %q < %q", r[0].Str, prev) }
    prev = r[0].Str
  }
  // check versioned columns exist for machine consumers
  found := false
  for _, r := range res.Rows {
    if r[0].Str=="system_catalog" { found=true; if r[1].Str!="supported" { t.Fatalf("system_catalog status %q", r[1].Str) } }
  }
  if !found { t.Fatal("system_catalog not found") }
  // selective query
  res, err = sess.Exec("SELECT name FROM system.capabilities WHERE status='supported' LIMIT 2")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=2 { t.Fatalf("limit %d", len(res.Rows)) }
  // param
  res, err = sess.ExecContext(nil, "SELECT * FROM system.capabilities WHERE name=$1", []Param{{Name:"1", Value: types.StringValue("vector")}})
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 { t.Fatalf("param %d", len(res.Rows)) }
}

func TestSystemTablesAndColumns(t *testing.T) {
  dir := t.TempDir()
  dek, _ := crypto.GenerateDEK(1)
  keys, _ := crypto.NewMemoryKeyProvider(dek)
  db, err := Create(dir+"/db", keys, 16)
  if err != nil { t.Fatalf("create: %v", err) }
  defer db.Close()
  sess := db.Session()
  // empty
  res, err := sess.Exec("SELECT * FROM system.tables")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=0 { t.Fatalf("expected 0 tables, got %d", len(res.Rows)) }
  _, err = sess.Exec("CREATE TABLE t (id STRING PRIMARY KEY, v STRING)")
  if err != nil { t.Fatal(err) }
  res, err = sess.Exec("SELECT * FROM system.tables")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 || res.Rows[0][0].Str!="t" { t.Fatalf("t %v", res.Rows) }
  if res.Columns[0]!="name" { t.Fatalf("col name %v", res.Columns) }
  // columns
  res, err = sess.Exec("SELECT * FROM system.columns WHERE table_name='t'")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=2 { t.Fatalf("cols %d", len(res.Rows)) }
  // indexes
  _, err = sess.Exec("CREATE INDEX ix ON t(v)")
  if err != nil { t.Fatal(err) }
  res, err = sess.Exec("SELECT * FROM system.indexes")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 { t.Fatalf("idx %d", len(res.Rows)) }
  if res.Rows[0][1].Str!="ix" { t.Fatalf("ix name %v", res.Rows[0]) }
  // storage redacted
  res, err = sess.Exec("SELECT * FROM system.storage")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 { t.Fatal("storage") }
  if res.Columns[6]!="encryption" { t.Fatalf("enc col %v", res.Columns) }
  if res.Rows[0][6].Str!="enabled" { t.Fatalf("enc val %q", res.Rows[0][6].Str) }
  s := fmt.Sprintf("%v", res.Rows[0])
  if contains(s, "dek=") { t.Fatalf("leaked %s", s) }
  // replication stubs
  res, err = sess.Exec("SELECT * FROM system.replication")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 { t.Fatal("repl") }
  res2, err := sess.Exec("SELECT * FROM system.raft")
  if err != nil { t.Fatal(err) }
  if len(res2.Rows)!=1 { t.Fatal("raft") }
  if res.Rows[0][3].Str=="" { t.Fatalf("leader_addr empty") }
  if res.Rows[0][3].Str=="[redacted]" {} else { t.Fatalf("expected redacted %q", res.Rows[0][3].Str) }
}

func TestSystemRBAC(t *testing.T) {
  dir := t.TempDir()
  dek, _ := crypto.GenerateDEK(1)
  keys, _ := crypto.NewMemoryKeyProvider(dek)
  db, err := Create(dir+"/db", keys, 16)
  if err != nil { t.Fatalf("create: %v", err) }
  defer db.Close()
  sess := db.Session()
  _, err = sess.Exec("CREATE TABLE t (id STRING PRIMARY KEY, v STRING)")
  if err != nil { t.Fatal(err) }
  aclPath := dir + "/acl.db"
  acl, err := security.CreateACL(aclPath)
  if err != nil { t.Fatal(err) }
  acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
  acl.Grant("bob", security.PrivSelect, security.ScopeTable, "t")
  acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, "")
  // bob sees table
  sess2 := db.Session()
  sess2.SetACL(acl)
  sess2.SetIdentity("bob")
  res, err := sess2.Exec("SELECT * FROM system.tables")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 { t.Fatalf("bob %d", len(res.Rows)) }
  // alice sees 0
  sess3 := db.Session()
  sess3.SetACL(acl)
  sess3.SetIdentity("alice")
  res, err = sess3.Exec("SELECT * FROM system.tables")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=0 { t.Fatalf("alice %d", len(res.Rows)) }
  // alice can see capabilities (requires CONNECT)
  res, err = sess3.Exec("SELECT * FROM system.capabilities")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)==0 { t.Fatal("alice caps") }
  // charlie no grant -> denied
  sess4 := db.Session()
  sess4.SetACL(acl)
  sess4.SetIdentity("charlie")
  _, err = sess4.Exec("SELECT * FROM system.capabilities")
  if err == nil { t.Fatal("charlie should deny") }
  // columns also filtered
  res, err = sess3.Exec("SELECT * FROM system.columns WHERE table_name='t'")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=0 { t.Fatalf("alice cols %d", len(res.Rows)) }
  res, err = sess2.Exec("SELECT * FROM system.columns WHERE table_name='t'")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=2 { t.Fatalf("bob cols %d", len(res.Rows)) }
  // indexes filtered
  _, err = sess.Exec("CREATE INDEX ix2 ON t(v)")
  if err != nil { t.Fatal(err) }
  res, err = sess3.Exec("SELECT * FROM system.indexes")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=0 { t.Fatalf("alice idx %d", len(res.Rows)) }
  res, err = sess2.Exec("SELECT * FROM system.indexes")
  if err != nil { t.Fatal(err) }
  if len(res.Rows)!=1 { t.Fatalf("bob idx %d %v", len(res.Rows), res.Rows) }
}

func TestSystemTenantIsolation(t *testing.T) {
  dir := t.TempDir()
  dek, _ := crypto.GenerateDEK(1)
  keys, _ := crypto.NewMemoryKeyProvider(dek)
  db, err := Create(dir+"/db", keys, 16)
  if err != nil { t.Fatalf("create: %v", err) }
  defer db.Close()
  sess := db.Session()
  _, err = sess.Exec("CREATE TABLE tt (id STRING PRIMARY KEY, tenant_id STRING)")
  if err != nil { t.Fatal(err) }
  aclPath := dir + "/acl2"
  acl, err := security.CreateACL(aclPath)
  if err != nil { t.Fatal(err) }
  acl.Grant("bob", security.PrivConnect, security.ScopeDatabase, "")
  acl.Grant("bob", security.PrivSelect, security.ScopeTable, "tt")
  acl.Grant("alice", security.PrivConnect, security.ScopeDatabase, "")
  acl.Grant("alice", security.PrivSelect, security.ScopeTable, "tt")
  // bob with tenant bound
  sessBob := db.Session()
  sessBob.SetACL(acl)
  sessBob.SetIdentity("bob")
  _, err = sessBob.Exec("SET TENANT = '00000000-0000-0000-0000-000000000001'")
  if err != nil { t.Fatal(err) }
  res, err := sessBob.Exec("SELECT * FROM system.tables")
  if err != nil { t.Fatal(err) }
  // both see tables (catalog not tenant filtered), but verify still works
  if len(res.Rows)==0 { t.Fatalf("bob tenant tables %d", len(res.Rows)) }
  sessAlice := db.Session()
  sessAlice.SetACL(acl)
  sessAlice.SetIdentity("alice")
  _, err = sessAlice.Exec("SET TENANT = '00000000-0000-0000-0000-000000000002'")
  if err != nil { t.Fatal(err) }
  res, err = sessAlice.Exec("SELECT * FROM system.tables")
  if err != nil { t.Fatal(err) }
  // ensure storage/replication still accessible with tenant
  res, err = sessBob.Exec("SELECT * FROM system.storage")
  if err != nil { t.Fatal(err) }
  res, err = sessAlice.Exec("SELECT * FROM system.replication")
  if err != nil { t.Fatal(err) }
  _ = res
}

func TestSystemRedacted(t *testing.T) {
  dir := t.TempDir()
  dek, _ := crypto.GenerateDEK(1)
  keys, _ := crypto.NewMemoryKeyProvider(dek)
  db, err := Create(dir+"/db", keys, 16)
  if err != nil { t.Fatal(err) }
  defer db.Close()
  sess := db.Session()
  res, err := sess.Exec("SELECT * FROM system.storage")
  if err != nil { t.Fatal(err) }
  for _, col := range res.Columns {
    if col=="key" || col=="dek" || col=="secret" { t.Fatalf("sensitive col %q", col) }
  }
  s := fmt.Sprintf("%v", res.Rows[0])
  for _, bad := range []string{"dek=", "key=", "secret", "password"} {
    if contains(s, bad) { t.Fatalf("leaked %q in %s", bad, s) }
  }
  res, err = sess.Exec("SELECT * FROM system.replication")
  if err != nil { t.Fatal(err) }
  s = fmt.Sprintf("%v", res.Rows[0])
  if contains(s, "dek=") { t.Fatalf("leaked repl %s", s) }
}

func contains(s, sub string) bool {
  for i:=0; i+len(sub) <= len(s); i++ { if s[i:i+len(sub)]==sub { return true } }
  return false
}
