package security

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/hosting"
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

func TestACLRolesForIncludesInheritedRoles(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.CreateRole("reader"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateRole("analyst"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRole("reader", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRole("analyst", "reader"); err != nil {
		t.Fatal(err)
	}
	got := a.RolesFor("ALICE")
	if len(got) != 2 || got[0] != "analyst" || got[1] != "reader" {
		t.Fatalf("RolesFor(alice) = %v, want [analyst reader]", got)
	}
	if got := a.RolesFor("unknown"); len(got) != 0 {
		t.Fatalf("RolesFor(unknown) = %v, want empty", got)
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

func TestResourceGroupUsageGrantPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acl")
	a, err := CreateACL(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("app", PrivUsage, ScopeResourceGroup, "reporting"); err != nil {
		t.Fatal(err)
	}
	reopened, err := OpenACL(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reopened.Allowed("app", PrivUsage, ScopeResourceGroup, "reporting") || reopened.Allowed("app", PrivUsage, ScopeResourceGroup, "other") {
		t.Fatal("resource group USAGE grant did not round-trip with least privilege")
	}
}

func TestPrivilegeAndScopeStringRoundTrip(t *testing.T) {
	privs := []Privilege{PrivConnect, PrivSelect, PrivInsert, PrivUpdate, PrivDelete, PrivCreate, PrivDrop, PrivAlter, PrivIndex, PrivExecute, PrivUsage, PrivGrant, PrivBackup, PrivRestore, PrivReplicate, PrivAdmin, PrivCDC}
	for _, p := range privs {
		got, err := ParsePrivilege(p.String())
		if err != nil || got != p {
			t.Fatalf("Privilege(%d).String()=%q round-trip: got %d, err %v", p, p.String(), got, err)
		}
	}
	if got := Privilege(9999).String(); got != "unknown" {
		t.Fatalf("out-of-range Privilege.String() = %q, want unknown", got)
	}
	scopes := []ScopeKind{ScopeCluster, ScopeDatabase, ScopeSchema, ScopeTable, ScopeColumn, ScopeFunction, ScopeBackup, ScopeReplication, ScopeAdmin, ScopeResourceGroup}
	for _, sc := range scopes {
		got, err := ParseScope(sc.String())
		if err != nil || got != sc {
			t.Fatalf("ScopeKind(%d).String()=%q round-trip: got %d, err %v", sc, sc.String(), got, err)
		}
	}
	if got := ScopeKind(250).String(); got != "unknown" {
		t.Fatalf("out-of-range ScopeKind.String() = %q, want unknown", got)
	}
}

func TestACLSnapshot(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	if roles, grants := a.Snapshot(); len(roles) != 0 || len(grants) != 0 {
		t.Fatalf("fresh ACL snapshot not empty: roles=%v grants=%v", roles, grants)
	}
	if err := a.CreateRole("reporting"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRole("reporting", "alice"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRole("reporting", "bob"); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("reporting", PrivSelect, ScopeTable, "orders"); err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("alice", PrivAdmin, ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	roles, grants := a.Snapshot()
	if len(roles) != 1 || roles[0].Role != "reporting" {
		t.Fatalf("roles = %v", roles)
	}
	if len(roles[0].Members) != 2 || roles[0].Members[0] != "alice" || roles[0].Members[1] != "bob" {
		t.Fatalf("role members not sorted alice,bob: %v", roles[0].Members)
	}
	if len(grants) != 2 {
		t.Fatalf("grants = %v", grants)
	}
	// deterministic order: grantee alice before reporting
	if grants[0].Grantee != "alice" || grants[1].Grantee != "reporting" {
		t.Fatalf("grants not sorted by grantee: %v", grants)
	}
	// mutating the returned slices must not corrupt the ACL
	roles[0].Members[0] = "corrupted"
	grants[0].Grantee = "corrupted"
	roles2, grants2 := a.Snapshot()
	if roles2[0].Members[0] != "alice" || grants2[0].Grantee != "alice" {
		t.Fatal("Snapshot leaked internal state: caller mutation affected next snapshot")
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

func TestAllowedInRealmIsolatesGrants(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	realmA := hosting.ID{1}
	realmB := hosting.ID{2}
	if err := a.GrantInRealm(realmA, "app", PrivSelect, ScopeTable, "products"); err != nil {
		t.Fatal(err)
	}
	if !a.AllowedInRealm(realmA, "app", PrivSelect, ScopeTable, "products") {
		t.Fatal("realm A grant must apply within realm A")
	}
	if a.AllowedInRealm(realmB, "app", PrivSelect, ScopeTable, "products") {
		t.Fatal("realm A grant must not leak into realm B for an identically-named principal")
	}
	if a.Allowed("app", PrivSelect, ScopeTable, "products") {
		t.Fatal("realm A grant must not leak to deployment-wide")
	}

	// A role, too, stays realm-local.
	if err := a.CreateRoleInRealm(realmA, "reporting"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRoleInRealm(realmA, "reporting", "carol"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantInRealm(realmA, "reporting", PrivSelect, ScopeTable, "orders"); err != nil {
		t.Fatal(err)
	}
	if !a.AllowedInRealm(realmA, "carol", PrivSelect, ScopeTable, "orders") {
		t.Fatal("realm A role membership must grant its privileges within realm A")
	}
	if a.AllowedInRealm(realmB, "carol", PrivSelect, ScopeTable, "orders") {
		t.Fatal("realm A role must not apply in realm B even for an identically-named principal")
	}
}

func TestAllowedInRealmUnionsDeploymentWideGrant(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := a.Grant("support", PrivSelect, ScopeTable, "tickets"); err != nil {
		t.Fatal(err)
	}
	realm := hosting.ID{3}
	if !a.AllowedInRealm(realm, "support", PrivSelect, ScopeTable, "tickets") {
		t.Fatal("a deployment-wide grant must apply inside every realm")
	}
	// And a realm's own grant adds on top of, not instead of, the deployment-wide one.
	if err := a.GrantInRealm(realm, "support", PrivInsert, ScopeTable, "tickets"); err != nil {
		t.Fatal(err)
	}
	if !a.AllowedInRealm(realm, "support", PrivSelect, ScopeTable, "tickets") {
		t.Fatal("deployment-wide grant must still apply after a realm-local grant is added")
	}
	if !a.AllowedInRealm(realm, "support", PrivInsert, ScopeTable, "tickets") {
		t.Fatal("realm-local grant must also apply")
	}
}

func TestClusterAdminGrantAlwaysDeploymentWide(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	realm := hosting.ID{4}
	// Even requested at a specific realm, PrivAdmin+ScopeCluster normalizes
	// to deployment-wide (decision #3) — cluster administration is not a
	// per-realm concept.
	if err := a.GrantInRealm(realm, "dba", PrivAdmin, ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if !a.Allowed("dba", PrivSelect, ScopeTable, "anything") {
		t.Fatal("PrivAdmin+ScopeCluster must act as a deployment-wide superuser regardless of the realm it was granted at")
	}
	other := hosting.ID{5}
	if !a.AllowedInRealm(other, "dba", PrivSelect, ScopeTable, "anything") {
		t.Fatal("deployment-wide cluster admin must authorize inside every realm, not just the one it was granted at")
	}
	_, grants := a.SnapshotInRealm(realm)
	if len(grants) != 0 {
		t.Fatalf("the grant must not be stored at realm, only at hosting.ID{}: %v", grants)
	}
	_, deploymentGrants := a.Snapshot()
	if len(deploymentGrants) != 1 {
		t.Fatalf("expected exactly one deployment-wide grant: %v", deploymentGrants)
	}
}

func TestExpandRoleStaysWithinRealm(t *testing.T) {
	a, err := CreateACL(filepath.Join(t.TempDir(), "nextsql.acl"))
	if err != nil {
		t.Fatal(err)
	}
	realmA := hosting.ID{6}
	realmB := hosting.ID{7}
	if err := a.CreateRoleInRealm(realmA, "team"); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateRoleInRealm(realmB, "team"); err != nil {
		t.Fatal(err)
	}
	if err := a.GrantRoleInRealm(realmA, "team", "alice"); err != nil {
		t.Fatal(err)
	}
	rolesA := a.RolesForInRealm(realmA, "alice")
	if len(rolesA) != 1 || rolesA[0] != "team" {
		t.Fatalf("alice's realm A roles = %v, want [team]", rolesA)
	}
	rolesB := a.RolesForInRealm(realmB, "alice")
	if len(rolesB) != 0 {
		t.Fatalf("alice must hold no roles in realm B: %v", rolesB)
	}
}

func TestLegacyACLFileDecodesEveryEntryDeploymentWide(t *testing.T) {
	roles, grants, err := decodeACL(buildV1ACLFixture(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || len(grants) != 1 {
		t.Fatalf("roles=%v grants=%v", roles, grants)
	}
	for k := range roles {
		if k.Realm != (hosting.ID{}) {
			t.Fatalf("role must decode deployment-wide, got realm %x", k.Realm)
		}
	}
	if grants[0].Realm != (hosting.ID{}) {
		t.Fatalf("grant must decode deployment-wide, got realm %x", grants[0].Realm)
	}
}

// buildV1ACLFixture hand-encodes one role ("reporting" -> ["alice"]) and one
// grant (reporting, SELECT, TABLE, "orders") in the pre-M2-4b-1 v1 layout
// (no RealmID fields), to prove decodeACL still reads it.
func buildV1ACLFixture(t *testing.T) []byte {
	t.Helper()
	role, member, grantee, object := "reporting", "alice", "reporting", "orders"
	n := 4 + 2 + 4
	n += 2 + len(role) + 2 + 2 + len(member)
	n += 4
	n += 2 + len(grantee) + 2 + 1 + 2 + len(object)
	buf := make([]byte, n)
	copy(buf[0:4], []byte(aclMagic))
	encoding.PutU16(buf, 4, aclVersionV1)
	encoding.PutU32(buf, 6, 1) // role count
	off := 10
	encoding.PutU16(buf, off, uint16(len(role)))
	off += 2
	copy(buf[off:], role)
	off += len(role)
	encoding.PutU16(buf, off, 1) // member count
	off += 2
	encoding.PutU16(buf, off, uint16(len(member)))
	off += 2
	copy(buf[off:], member)
	off += len(member)
	encoding.PutU32(buf, off, 1) // grant count
	off += 4
	encoding.PutU16(buf, off, uint16(len(grantee)))
	off += 2
	copy(buf[off:], grantee)
	off += len(grantee)
	encoding.PutU16(buf, off, uint16(PrivSelect))
	off += 2
	buf[off] = byte(ScopeTable)
	off++
	encoding.PutU16(buf, off, uint16(len(object)))
	off += 2
	copy(buf[off:], object)
	off += len(object)
	if off != len(buf) {
		t.Fatalf("fixture size mismatch: off=%d len=%d", off, len(buf))
	}
	return buf
}
