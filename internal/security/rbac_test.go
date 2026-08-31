package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestACLLeastPrivilege(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	if a.Allowed("app", PrivSelect, ScopeTable, "products") {
		t.Fatal("new user must have no grants")
	}
	if err := a.Grant("app", PrivSelect, ScopeTable, "products"); err != nil {
		t.Fatal(err)
	}
	if !a.Allowed("app", PrivSelect, ScopeTable, "products") {
		t.Fatal("granted SELECT denied")
	}
	if a.Allowed("app", PrivInsert, ScopeTable, "products") {
		t.Fatal("INSERT must stay denied")
	}
	if a.Allowed("app", PrivSelect, ScopeTable, "orders") {
		t.Fatal("other table must stay denied")
	}
	if err := a.Revoke("app", PrivSelect, ScopeTable, "products"); err != nil {
		t.Fatal(err)
	}
	if a.Allowed("app", PrivSelect, ScopeTable, "products") {
		t.Fatal("revoked SELECT still allowed")
	}
}

func TestACLAllowedScoped(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range []string{"readonly", "writer"} {
		if err := a.CreateRole(r); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Grant("readonly", PrivSelect, ScopeTable, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("writer", PrivInsert, ScopeTable, ""); err != nil {
		t.Fatal(err)
	}
	// alice holds both roles directly, plus a direct DELETE grant.
	for _, r := range []string{"readonly", "writer"} {
		if err := a.GrantRole(r, "alice"); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Grant("alice", PrivDelete, ScopeTable, "orders"); err != nil {
		t.Fatal(err)
	}

	// No scope: alice has everything she was granted.
	if !a.AllowedScoped("alice", nil, PrivDelete, ScopeTable, "orders") {
		t.Fatal("unscoped check lost a direct grant")
	}
	// Scoped to readonly: SELECT yes, INSERT/DELETE no (narrowed below her rights).
	if !a.AllowedScoped("alice", []string{"readonly"}, PrivSelect, ScopeTable, "orders") {
		t.Fatal("scoped credential lost the role's own privilege")
	}
	if a.AllowedScoped("alice", []string{"readonly"}, PrivInsert, ScopeTable, "orders") {
		t.Fatal("scoped credential kept a privilege outside the role")
	}
	if a.AllowedScoped("alice", []string{"readonly"}, PrivDelete, ScopeTable, "orders") {
		t.Fatal("scoped credential kept the principal's direct grant")
	}
	// Scoping to a role alice does not hold denies everything (no escalation).
	if err := a.CreateRole("dba"); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("dba", PrivAdmin, ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if a.AllowedScoped("alice", []string{"dba"}, PrivSelect, ScopeTable, "orders") {
		t.Fatal("credential escalated to a role the principal lacks")
	}
}

func TestACLAdminAndRoles(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateRole("analyst"); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("analyst", PrivSelect, ScopeTable, ""); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRole("analyst", "bob"); err != nil {
		t.Fatal(err)
	}
	if !a.Allowed("bob", PrivSelect, ScopeTable, "anything") {
		t.Fatal("role grant not inherited")
	}
	if err := a.Grant("dba", PrivAdmin, ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if !a.Allowed("dba", PrivDrop, ScopeTable, "t") || !a.Allowed("dba", PrivBackup, ScopeBackup, "") {
		t.Fatal("cluster admin must pass every check")
	}
	if err := a.RevokeRole("analyst", "bob"); err != nil {
		t.Fatal(err)
	}
	if a.Allowed("bob", PrivSelect, ScopeTable, "anything") {
		t.Fatal("revoked role still effective")
	}
}

func TestACLPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acl")
	a, err := CreateACL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateRole("r"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRole("r", "u"); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("r", PrivInsert, ScopeTable, "t"); err != nil {
		t.Fatal(err)
	}
	re, err := OpenACL(path)
	if err != nil {
		t.Fatal(err)
	}
	if !re.Allowed("u", PrivInsert, ScopeTable, "t") {
		t.Fatal("persisted grant missing")
	}
}

func TestACLDecodeRejectsGarbage(t *testing.T) {
	cases := [][]byte{nil, []byte("xxxx"), []byte("NSAC"), append([]byte("NSAC"), 0, 99)}
	for _, c := range cases {
		if _, _, err := decodeACL(c); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}

func TestParsePrivilege(t *testing.T) {
	p, err := ParsePrivilege("SELECT")
	if err != nil || p != PrivSelect {
		t.Fatalf("%v %v", p, err)
	}
	if _, err := ParsePrivilege("explode"); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	for _, name := range []string{"CDC", "subscribe"} {
		p, err := ParsePrivilege(name)
		if err != nil || p != PrivCDC {
			t.Fatalf("%s: %v %v", name, p, err)
		}
	}
}

func TestCDCPermissionPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acl")
	a, err := CreateACL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("streamer", PrivCDC, ScopeTable, "orders"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenACL(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Allowed("streamer", PrivCDC, ScopeTable, "orders") || reopened.Allowed("streamer", PrivCDC, ScopeTable, "other") {
		t.Fatal("CDC table grant did not round-trip with least privilege")
	}
}

func FuzzDecodeACL(f *testing.F) {
	a, err := CreateACL(filepath.Join(f.TempDir(), "acl"))
	if err != nil {
		f.Fatal(err)
	}
	_ = a.CreateRole("r")
	_ = a.Grant("r", PrivSelect, ScopeTable, "t")
	good, err := os.ReadFile(a.Path())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte("NSAC"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _, _ = decodeACL(raw)
	})
}
