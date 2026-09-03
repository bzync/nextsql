package security

import (
	"os"
	"sort"
	"strings"
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	aclMagic     = "NSAC"
	aclVersionV1 = 1 // legacy: no RealmID (every role/grant is deployment-wide)
	aclVersion   = 2 // current: adds a 16-byte RealmID per role and per grant (M2-4b-1)
	maxACLName   = 128
	maxRoles     = 4096
	maxGrants    = 16384
)

// Privilege is a single right. Least privilege: a new principal has none.
type Privilege uint16

const (
	PrivConnect Privilege = iota + 1
	PrivSelect
	PrivInsert
	PrivUpdate
	PrivDelete
	PrivCreate
	PrivDrop
	PrivAlter
	PrivIndex
	PrivExecute
	PrivUsage
	PrivGrant
	PrivBackup
	PrivRestore
	PrivReplicate
	PrivAdmin
	PrivCDC
)

// ScopeKind is a GRANT target.
type ScopeKind uint8

const (
	ScopeCluster ScopeKind = iota + 1
	ScopeDatabase
	ScopeSchema
	ScopeTable
	ScopeColumn
	ScopeFunction
	ScopeBackup
	ScopeReplication
	ScopeAdmin
	ScopeResourceGroup
)

// Grant is one persisted authorization. Realm is hosting.ID{} for a
// deployment-wide grant (see roleKey and the *InRealm methods below), which
// applies inside every realm in addition to that realm's own grants
// (union, not shadowing — authorization is additive) — except for a
// PrivAdmin+ScopeCluster or PrivAdmin+ScopeAdmin grant, which always means
// deployment-wide regardless of the Realm value it was made with: cluster
// administration is not a per-realm concept (see hasLocked).
type Grant struct {
	Realm   hosting.ID
	Grantee string
	Priv    Privilege
	Scope   ScopeKind
	Object  string
}

// RoleInfo is a read-only summary of one role and its members, for
// introspection surfaces such as system.roles.
type RoleInfo struct {
	Role    string
	Members []string // sorted; users or nested role names
}

// roleKey is the composite (Realm, Name) key each role is stored under,
// mirroring auth.Store's userKey. hosting.ID{} means deployment-wide; a
// role's members are always names within its own realm bucket.
type roleKey struct {
	Realm hosting.ID
	Name  string
}

// Snapshot returns every deployment-wide (hosting.ID{}) role and grant. See SnapshotInRealm.
func (a *ACL) Snapshot() ([]RoleInfo, []Grant) { return a.SnapshotInRealm(hosting.ID{}) }

// SnapshotInRealm returns every role (with its members) and every grant
// belonging to realm, both sorted deterministically, for introspection
// surfaces such as system.roles / system.grants. Realm-scoped only: it does
// not also return deployment-wide entries when realm is not hosting.ID{}
// (a realm's own principal namespace, not everything that could authorize
// into it). Callers are responsible for their own authorization gating
// before calling it (system.roles/system.grants are admin-only).
func (a *ACL) SnapshotInRealm(realm hosting.ID) ([]RoleInfo, []Grant) {
	a.mu.Lock()
	defer a.mu.Unlock()
	var roles []RoleInfo
	for key, members := range a.roles {
		if key.Realm != realm {
			continue
		}
		m := append([]string(nil), members...)
		sort.Strings(m)
		roles = append(roles, RoleInfo{Role: key.Name, Members: m})
	}
	sort.Slice(roles, func(i, j int) bool { return roles[i].Role < roles[j].Role })
	var grants []Grant
	for _, g := range a.grants {
		if g.Realm == realm {
			grants = append(grants, g)
		}
	}
	sort.Slice(grants, func(i, j int) bool {
		if grants[i].Grantee != grants[j].Grantee {
			return grants[i].Grantee < grants[j].Grantee
		}
		if grants[i].Scope != grants[j].Scope {
			return grants[i].Scope < grants[j].Scope
		}
		if grants[i].Object != grants[j].Object {
			return grants[i].Object < grants[j].Object
		}
		return grants[i].Priv < grants[j].Priv
	})
	return roles, grants
}

// ACL is a least-privilege role catalog. A missing grant is a deny.
type ACL struct {
	mu     sync.Mutex
	path   string
	roles  map[roleKey][]string // role -> members (users or roles)
	grants []Grant
}

func CreateACL(path string) (*ACL, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "security.CreateACL", "ACL file exists")
	}
	a := &ACL{path: path, roles: make(map[roleKey][]string)}
	if err := a.persist(); err != nil {
		return nil, err
	}
	return a, nil
}

func OpenACL(path string) (*ACL, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "security.OpenACL", "read", err)
	}
	roles, grants, err := decodeACL(raw)
	if err != nil {
		return nil, err
	}
	return &ACL{path: path, roles: roles, grants: grants}, nil
}

func OpenOrCreateACL(path string) (*ACL, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return CreateACL(path)
	}
	return OpenACL(path)
}

func (a *ACL) Path() string { return a.path }

// clusterRealm forces scope/priv combinations that are inherently
// deployment-wide (cluster or admin-gateway superuser scopes) to always
// store and match at hosting.ID{}, regardless of what realm was requested —
// "cluster administration" is not a per-realm concept (see hasLocked, which
// relies on every such grant already being normalized this way).
func clusterRealm(realm hosting.ID, priv Privilege, scope ScopeKind) hosting.ID {
	if priv == PrivAdmin && (scope == ScopeCluster || scope == ScopeAdmin) {
		return hosting.ID{}
	}
	return realm
}

func (a *ACL) AddUser(user string) error { return a.AddUserInRealm(hosting.ID{}, user) }

func (a *ACL) AddUserInRealm(realm hosting.ID, user string) error {
	name, err := normalizeName(user)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.roles[roleKey{Realm: realm, Name: name}]; !ok {
		// users are principals; they need no role entry until granted a role
	}
	_ = name
	return a.persist()
}

func (a *ACL) CreateRole(role string) error { return a.CreateRoleInRealm(hosting.ID{}, role) }

func (a *ACL) CreateRoleInRealm(realm hosting.ID, role string) error {
	name, err := normalizeName(role)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := roleKey{Realm: realm, Name: name}
	if _, ok := a.roles[key]; ok {
		return nerr.New(nerr.AlreadyExists, "security.CreateRole", "role exists")
	}
	if len(a.roles) >= maxRoles {
		return nerr.New(nerr.InvalidArgument, "security.CreateRole", "too many roles")
	}
	a.roles[key] = nil
	return a.persist()
}

func (a *ACL) DropRole(role string) error { return a.DropRoleInRealm(hosting.ID{}, role) }

func (a *ACL) DropRoleInRealm(realm hosting.ID, role string) error {
	name, err := normalizeName(role)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := roleKey{Realm: realm, Name: name}
	if _, ok := a.roles[key]; !ok {
		return nerr.New(nerr.NotFound, "security.DropRole", "unknown role")
	}
	delete(a.roles, key)
	kept := a.grants[:0]
	for _, g := range a.grants {
		if g.Grantee != name || g.Realm != realm {
			kept = append(kept, g)
		}
	}
	a.grants = kept
	return a.persist()
}

func (a *ACL) DropUser(user string) error { return a.DropUserInRealm(hosting.ID{}, user) }

func (a *ACL) DropUserInRealm(realm hosting.ID, user string) error {
	name, err := normalizeName(user)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for role, members := range a.roles {
		if role.Realm != realm {
			continue
		}
		out := members[:0]
		for _, m := range members {
			if m != name {
				out = append(out, m)
			}
		}
		a.roles[role] = out
	}
	kept := a.grants[:0]
	for _, g := range a.grants {
		if g.Grantee != name || g.Realm != realm {
			kept = append(kept, g)
		}
	}
	a.grants = kept
	return a.persist()
}

func (a *ACL) GrantRole(role, member string) error {
	return a.GrantRoleInRealm(hosting.ID{}, role, member)
}

func (a *ACL) GrantRoleInRealm(realm hosting.ID, role, member string) error {
	r, err := normalizeName(role)
	if err != nil {
		return err
	}
	m, err := normalizeName(member)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := roleKey{Realm: realm, Name: r}
	if _, ok := a.roles[key]; !ok {
		return nerr.New(nerr.NotFound, "security.GrantRole", "unknown role")
	}
	for _, existing := range a.roles[key] {
		if existing == m {
			return nil
		}
	}
	a.roles[key] = append(a.roles[key], m)
	return a.persist()
}

func (a *ACL) RevokeRole(role, member string) error {
	return a.RevokeRoleInRealm(hosting.ID{}, role, member)
}

func (a *ACL) RevokeRoleInRealm(realm hosting.ID, role, member string) error {
	r, err := normalizeName(role)
	if err != nil {
		return err
	}
	m, err := normalizeName(member)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	key := roleKey{Realm: realm, Name: r}
	members := a.roles[key]
	out := members[:0]
	found := false
	for _, existing := range members {
		if existing == m {
			found = true
			continue
		}
		out = append(out, existing)
	}
	if !found {
		return nerr.New(nerr.NotFound, "security.RevokeRole", "membership not found")
	}
	a.roles[key] = out
	return a.persist()
}

func (a *ACL) Grant(grantee string, priv Privilege, scope ScopeKind, object string) error {
	return a.GrantInRealm(hosting.ID{}, grantee, priv, scope, object)
}

// GrantInRealm persists one authorization for grantee within realm. A
// PrivAdmin+ScopeCluster/ScopeAdmin grant always normalizes to
// hosting.ID{} regardless of realm (see clusterRealm).
func (a *ACL) GrantInRealm(realm hosting.ID, grantee string, priv Privilege, scope ScopeKind, object string) error {
	name, err := normalizeName(grantee)
	if err != nil {
		return err
	}
	if priv == 0 || scope == 0 {
		return nerr.New(nerr.InvalidArgument, "security.Grant", "privilege and scope are required")
	}
	realm = clusterRealm(realm, priv, scope)
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.grants) >= maxGrants {
		return nerr.New(nerr.InvalidArgument, "security.Grant", "too many grants")
	}
	for _, g := range a.grants {
		if g.Realm == realm && g.Grantee == name && g.Priv == priv && g.Scope == scope && g.Object == object {
			return nil
		}
	}
	a.grants = append(a.grants, Grant{Realm: realm, Grantee: name, Priv: priv, Scope: scope, Object: object})
	return a.persist()
}

func (a *ACL) Revoke(grantee string, priv Privilege, scope ScopeKind, object string) error {
	return a.RevokeInRealm(hosting.ID{}, grantee, priv, scope, object)
}

func (a *ACL) RevokeInRealm(realm hosting.ID, grantee string, priv Privilege, scope ScopeKind, object string) error {
	name, err := normalizeName(grantee)
	if err != nil {
		return err
	}
	realm = clusterRealm(realm, priv, scope)
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.grants[:0]
	found := false
	for _, g := range a.grants {
		if g.Realm == realm && g.Grantee == name && g.Priv == priv && g.Scope == scope && g.Object == object {
			found = true
			continue
		}
		kept = append(kept, g)
	}
	if !found {
		return nerr.New(nerr.NotFound, "security.Revoke", "grant not found")
	}
	a.grants = kept
	return a.persist()
}

// Allowed reports whether user may exercise priv on scope/object, at
// hosting.ID{} (deployment-wide). See AllowedInRealm.
// Cluster ADMIN is a superuser. Empty object on a grant matches any object
// at that scope. Table grants do not imply column grants unless object is empty.
func (a *ACL) Allowed(user string, priv Privilege, scope ScopeKind, object string) bool {
	return a.AllowedInRealm(hosting.ID{}, user, priv, scope, object)
}

// AllowedInRealm is Allowed, evaluated within realm: it sees realm's own
// grants and role definitions plus every deployment-wide (hosting.ID{})
// one, unioned (decision #2) — except that a PrivAdmin+ScopeCluster or
// PrivAdmin+ScopeAdmin grant only ever exists at hosting.ID{} (GrantInRealm
// normalizes it there), so those two superuser checks are always
// deployment-wide-only, never realm-unioned (decision #3).
func (a *ACL) AllowedInRealm(realm hosting.ID, user string, priv Privilege, scope ScopeKind, object string) bool {
	if a == nil {
		return true
	}
	name := strings.ToLower(strings.TrimSpace(user))
	if name == "" {
		return false
	}
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.allowedForLocked(realm, a.expandLocked(realm, name), priv, scope, object)
}

// RolesFor returns every role held by user at hosting.ID{} (deployment-wide). See RolesForInRealm.
func (a *ACL) RolesFor(user string) []string { return a.RolesForInRealm(hosting.ID{}, user) }

// RolesForInRealm returns every direct or transitively inherited native
// role held by user within realm (realm's own roles plus deployment-wide
// ones, unioned). The result is sorted and bounded by the persisted ACL
// role limit. It is intended for trusted authorization components such as
// the embedded OIDC broker; it contains role names only, never grants or
// secrets.
func (a *ACL) RolesForInRealm(realm hosting.ID, user string) []string {
	if a == nil {
		return nil
	}
	name := strings.ToLower(strings.TrimSpace(user))
	if name == "" {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	expanded := a.expandLocked(realm, name)
	roles := make([]string, 0, len(expanded))
	for principal := range expanded {
		if a.isRoleLocked(realm, principal) {
			roles = append(roles, principal)
		}
	}
	sort.Strings(roles)
	return roles
}

// AllowedScoped is AllowedScopedInRealm at hosting.ID{} (deployment-wide).
func (a *ACL) AllowedScoped(user string, roles []string, priv Privilege, scope ScopeKind, object string) bool {
	return a.AllowedScopedInRealm(hosting.ID{}, user, roles, priv, scope, object)
}

// AllowedScopedInRealm is AllowedInRealm further narrowed to the privileges
// reachable through the named roles, as carried by a short-lived
// credential's role scope. The user must be a member of every listed role;
// an empty roles slice is identical to AllowedInRealm. A listed role the
// user does not hold denies every check (the credential cannot escalate).
func (a *ACL) AllowedScopedInRealm(realm hosting.ID, user string, roles []string, priv Privilege, scope ScopeKind, object string) bool {
	if a == nil {
		return true
	}
	if len(roles) == 0 {
		return a.AllowedInRealm(realm, user, priv, scope, object)
	}
	name := strings.ToLower(strings.TrimSpace(user))
	if name == "" {
		return false
	}
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	member := a.expandLocked(realm, name)
	principals := make(map[string]struct{})
	for _, role := range roles {
		r := strings.ToLower(strings.TrimSpace(role))
		if r == "" {
			return false
		}
		if _, ok := member[r]; !ok {
			return false
		}
		for k := range a.expandLocked(realm, r) {
			principals[k] = struct{}{}
		}
	}
	return a.allowedForLocked(realm, principals, priv, scope, object)
}

func (a *ACL) allowedForLocked(realm hosting.ID, principals map[string]struct{}, priv Privilege, scope ScopeKind, object string) bool {
	if a.hasLocked(hosting.ID{}, principals, PrivAdmin, ScopeCluster, "") {
		return true
	}
	if a.hasLocked(hosting.ID{}, principals, PrivAdmin, ScopeAdmin, "") {
		return true
	}
	if a.hasLockedUnion(realm, principals, priv, scope, object) {
		return true
	}
	if object != "" && a.hasLockedUnion(realm, principals, priv, scope, "") {
		return true
	}
	if scope == ScopeColumn {
		table, _, ok := strings.Cut(object, ".")
		if ok && a.hasLockedUnion(realm, principals, priv, ScopeTable, table) {
			return true
		}
		if a.hasLockedUnion(realm, principals, priv, ScopeTable, "") {
			return true
		}
	}
	return false
}

// hasLockedUnion checks realm's own grants plus every deployment-wide
// (hosting.ID{}) grant together — authorization is additive (decision #2).
func (a *ACL) hasLockedUnion(realm hosting.ID, principals map[string]struct{}, priv Privilege, scope ScopeKind, object string) bool {
	if a.hasLocked(realm, principals, priv, scope, object) {
		return true
	}
	return realm != (hosting.ID{}) && a.hasLocked(hosting.ID{}, principals, priv, scope, object)
}

func (a *ACL) hasLocked(realm hosting.ID, principals map[string]struct{}, priv Privilege, scope ScopeKind, object string) bool {
	for _, g := range a.grants {
		if g.Realm != realm {
			continue
		}
		if _, ok := principals[g.Grantee]; !ok {
			continue
		}
		if g.Priv != priv || g.Scope != scope {
			continue
		}
		if g.Object == object {
			return true
		}
	}
	return false
}

func (a *ACL) isRoleLocked(realm hosting.ID, name string) bool {
	if _, ok := a.roles[roleKey{Realm: realm, Name: name}]; ok {
		return true
	}
	if realm == (hosting.ID{}) {
		return false
	}
	_, ok := a.roles[roleKey{Name: name}]
	return ok
}

// expandLocked returns every principal name reachable from user through
// role membership within realm, unioned with deployment-wide (hosting.ID{})
// role definitions — a deployment-wide role membership applies inside
// every realm, mirroring hasLockedUnion's grant semantics.
func (a *ACL) expandLocked(realm hosting.ID, user string) map[string]struct{} {
	out := map[string]struct{}{user: {}}
	changed := true
	for changed {
		changed = false
		for role, members := range a.roles {
			if role.Realm != realm && role.Realm != (hosting.ID{}) {
				continue
			}
			if _, already := out[role.Name]; already {
				continue
			}
			for _, m := range members {
				if _, ok := out[m]; ok {
					out[role.Name] = struct{}{}
					changed = true
					break
				}
			}
		}
	}
	return out
}

func (a *ACL) persist() error {
	raw, err := encodeACL(a.roles, a.grants)
	if err != nil {
		return err
	}
	tmp := a.path + ".tmp"
	if err := os.WriteFile(tmp, raw, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "security.ACL.persist", "write", err)
	}
	if err := os.Rename(tmp, a.path); err != nil {
		_ = os.Remove(tmp)
		return nerr.Wrap(nerr.IO, "security.ACL.persist", "rename", err)
	}
	return nil
}

func normalizeName(s string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(s))
	if name == "" || len(name) > maxACLName {
		return "", nerr.New(nerr.InvalidArgument, "security", "invalid name")
	}
	return name, nil
}

func userKey(name string) string { return name }

// String renders a Privilege in the same spelling ParsePrivilege accepts,
// for introspection surfaces such as system.grants.
func (p Privilege) String() string {
	switch p {
	case PrivConnect:
		return "connect"
	case PrivSelect:
		return "select"
	case PrivInsert:
		return "insert"
	case PrivUpdate:
		return "update"
	case PrivDelete:
		return "delete"
	case PrivCreate:
		return "create"
	case PrivDrop:
		return "drop"
	case PrivAlter:
		return "alter"
	case PrivIndex:
		return "index"
	case PrivExecute:
		return "execute"
	case PrivUsage:
		return "usage"
	case PrivGrant:
		return "grant"
	case PrivBackup:
		return "backup"
	case PrivRestore:
		return "restore"
	case PrivReplicate:
		return "replication"
	case PrivAdmin:
		return "admin"
	case PrivCDC:
		return "cdc"
	default:
		return "unknown"
	}
}

// String renders a ScopeKind in the same spelling ParseScope accepts, for
// introspection surfaces such as system.grants.
func (k ScopeKind) String() string {
	switch k {
	case ScopeCluster:
		return "cluster"
	case ScopeDatabase:
		return "database"
	case ScopeSchema:
		return "schema"
	case ScopeTable:
		return "table"
	case ScopeColumn:
		return "column"
	case ScopeFunction:
		return "function"
	case ScopeBackup:
		return "backup"
	case ScopeReplication:
		return "replication"
	case ScopeAdmin:
		return "administration"
	case ScopeResourceGroup:
		return "resourcegroup"
	default:
		return "unknown"
	}
}

func ParsePrivilege(s string) (Privilege, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "connect":
		return PrivConnect, nil
	case "select":
		return PrivSelect, nil
	case "insert":
		return PrivInsert, nil
	case "update":
		return PrivUpdate, nil
	case "delete":
		return PrivDelete, nil
	case "create":
		return PrivCreate, nil
	case "drop":
		return PrivDrop, nil
	case "alter":
		return PrivAlter, nil
	case "index":
		return PrivIndex, nil
	case "execute":
		return PrivExecute, nil
	case "usage":
		return PrivUsage, nil
	case "grant":
		return PrivGrant, nil
	case "backup":
		return PrivBackup, nil
	case "restore":
		return PrivRestore, nil
	case "replication":
		return PrivReplicate, nil
	case "cdc", "subscribe":
		return PrivCDC, nil
	case "admin", "all":
		return PrivAdmin, nil
	default:
		return 0, nerr.New(nerr.InvalidArgument, "security.ParsePrivilege", "unknown privilege")
	}
}

func ParseScope(s string) (ScopeKind, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cluster":
		return ScopeCluster, nil
	case "database":
		return ScopeDatabase, nil
	case "schema":
		return ScopeSchema, nil
	case "table", "":
		return ScopeTable, nil
	case "column":
		return ScopeColumn, nil
	case "function":
		return ScopeFunction, nil
	case "backup":
		return ScopeBackup, nil
	case "replication":
		return ScopeReplication, nil
	case "administration", "admin":
		return ScopeAdmin, nil
	case "resourcegroup":
		return ScopeResourceGroup, nil
	default:
		return 0, nerr.New(nerr.InvalidArgument, "security.ParseScope", "unknown scope")
	}
}

// encodeACL always writes the current (v2) format: every role and grant
// carries a 16-byte RealmID (hosting.ID{} for a deployment-wide entry).
// decodeACL still reads v1 files (see decodeACLV1).
func encodeACL(roles map[roleKey][]string, grants []Grant) ([]byte, error) {
	if len(roles) > maxRoles || len(grants) > maxGrants {
		return nil, nerr.New(nerr.InvalidArgument, "security.encodeACL", "ACL exceeds limit")
	}
	n := 4 + 2 + 4
	for role, members := range roles {
		n += len(role.Realm) + 2 + len(role.Name) + 2
		for _, m := range members {
			n += 2 + len(m)
		}
	}
	n += 4
	for _, g := range grants {
		n += len(g.Realm) + 2 + len(g.Grantee) + 2 + 1 + 2 + len(g.Object)
	}
	buf := make([]byte, n)
	copy(buf[0:4], aclMagic)
	encoding.PutU16(buf, 4, aclVersion)
	encoding.PutU32(buf, 6, uint32(len(roles)))
	off := 10
	for role, members := range roles {
		copy(buf[off:], role.Realm[:])
		off += len(role.Realm)
		encoding.PutU16(buf, off, uint16(len(role.Name)))
		off += 2
		copy(buf[off:], role.Name)
		off += len(role.Name)
		encoding.PutU16(buf, off, uint16(len(members)))
		off += 2
		for _, m := range members {
			encoding.PutU16(buf, off, uint16(len(m)))
			off += 2
			copy(buf[off:], m)
			off += len(m)
		}
	}
	encoding.PutU32(buf, off, uint32(len(grants)))
	off += 4
	for _, g := range grants {
		copy(buf[off:], g.Realm[:])
		off += len(g.Realm)
		encoding.PutU16(buf, off, uint16(len(g.Grantee)))
		off += 2
		copy(buf[off:], g.Grantee)
		off += len(g.Grantee)
		encoding.PutU16(buf, off, uint16(g.Priv))
		off += 2
		buf[off] = byte(g.Scope)
		off++
		encoding.PutU16(buf, off, uint16(len(g.Object)))
		off += 2
		copy(buf[off:], g.Object)
		off += len(g.Object)
	}
	return buf[:off], nil
}

// decodeACL parses an ACL file. The legacy v1 format decodes with every
// role/grant implicitly deployment-wide (roleKey.Realm/Grant.Realm ==
// hosting.ID{}); the current v2 format carries an explicit RealmID for
// each. encodeACL always writes v2.
func decodeACL(raw []byte) (map[roleKey][]string, []Grant, error) {
	if len(raw) < 10 {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated ACL")
	}
	if string(raw[0:4]) != aclMagic {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "bad ACL magic")
	}
	switch encoding.U16(raw, 4) {
	case aclVersionV1:
		return decodeACLV1(raw)
	case aclVersion:
		return decodeACLV2(raw)
	default:
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "unsupported ACL version")
	}
}

func decodeACLV1(raw []byte) (map[roleKey][]string, []Grant, error) {
	nrole := encoding.U32(raw, 6)
	if nrole > maxRoles {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "role count exceeds limit")
	}
	roles := make(map[roleKey][]string, nrole)
	off := 10
	for i := uint32(0); i < nrole; i++ {
		nl, err := encoding.ReadU16(raw, off)
		if err != nil || int(nl) > maxACLName {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated role")
		}
		nameb, err := encoding.ReadBytes(raw, off+2, int(nl))
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated role")
		}
		off += 2 + int(nl)
		nm, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated members")
		}
		off += 2
		var members []string
		for j := uint16(0); j < nm; j++ {
			ml, err := encoding.ReadU16(raw, off)
			if err != nil || int(ml) > maxACLName {
				return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated member")
			}
			mb, err := encoding.ReadBytes(raw, off+2, int(ml))
			if err != nil {
				return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated member")
			}
			off += 2 + int(ml)
			members = append(members, string(mb))
		}
		roles[roleKey{Name: string(nameb)}] = members
	}
	ng, err := encoding.ReadU32(raw, off)
	if err != nil {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated grant count")
	}
	off += 4
	if ng > maxGrants {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "grant count exceeds limit")
	}
	grants := make([]Grant, 0, ng)
	for i := uint32(0); i < ng; i++ {
		gl, err := encoding.ReadU16(raw, off)
		if err != nil || int(gl) > maxACLName {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated grantee")
		}
		gb, err := encoding.ReadBytes(raw, off+2, int(gl))
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated grantee")
		}
		off += 2 + int(gl)
		priv, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated privilege")
		}
		off += 2
		if Privilege(priv) < PrivConnect || Privilege(priv) > PrivCDC {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "unknown privilege")
		}
		if off >= len(raw) {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated scope")
		}
		scope := ScopeKind(raw[off])
		off++
		if scope < ScopeCluster || scope > ScopeResourceGroup {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "unknown scope")
		}
		ol, err := encoding.ReadU16(raw, off)
		if err != nil || int(ol) > 512 {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated object")
		}
		ob, err := encoding.ReadBytes(raw, off+2, int(ol))
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated object")
		}
		off += 2 + int(ol)
		grants = append(grants, Grant{Grantee: string(gb), Priv: Privilege(priv), Scope: scope, Object: string(ob)})
	}
	if off != len(raw) {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "trailing ACL bytes")
	}
	return roles, grants, nil
}

func decodeACLV2(raw []byte) (map[roleKey][]string, []Grant, error) {
	nrole := encoding.U32(raw, 6)
	if nrole > maxRoles {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "role count exceeds limit")
	}
	roles := make(map[roleKey][]string, nrole)
	off := 10
	for i := uint32(0); i < nrole; i++ {
		realmB, err := encoding.ReadBytes(raw, off, 16)
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated realm")
		}
		off += 16
		nl, err := encoding.ReadU16(raw, off)
		if err != nil || int(nl) > maxACLName {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated role")
		}
		nameb, err := encoding.ReadBytes(raw, off+2, int(nl))
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated role")
		}
		off += 2 + int(nl)
		nm, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated members")
		}
		off += 2
		var members []string
		for j := uint16(0); j < nm; j++ {
			ml, err := encoding.ReadU16(raw, off)
			if err != nil || int(ml) > maxACLName {
				return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated member")
			}
			mb, err := encoding.ReadBytes(raw, off+2, int(ml))
			if err != nil {
				return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated member")
			}
			off += 2 + int(ml)
			members = append(members, string(mb))
		}
		var realm hosting.ID
		copy(realm[:], realmB)
		roles[roleKey{Realm: realm, Name: string(nameb)}] = members
	}
	ng, err := encoding.ReadU32(raw, off)
	if err != nil {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated grant count")
	}
	off += 4
	if ng > maxGrants {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "grant count exceeds limit")
	}
	grants := make([]Grant, 0, ng)
	for i := uint32(0); i < ng; i++ {
		grealmB, err := encoding.ReadBytes(raw, off, 16)
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated realm")
		}
		off += 16
		gl, err := encoding.ReadU16(raw, off)
		if err != nil || int(gl) > maxACLName {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated grantee")
		}
		gb, err := encoding.ReadBytes(raw, off+2, int(gl))
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated grantee")
		}
		off += 2 + int(gl)
		priv, err := encoding.ReadU16(raw, off)
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated privilege")
		}
		off += 2
		if Privilege(priv) < PrivConnect || Privilege(priv) > PrivCDC {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "unknown privilege")
		}
		if off >= len(raw) {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated scope")
		}
		scope := ScopeKind(raw[off])
		off++
		if scope < ScopeCluster || scope > ScopeResourceGroup {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "unknown scope")
		}
		ol, err := encoding.ReadU16(raw, off)
		if err != nil || int(ol) > 512 {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated object")
		}
		ob, err := encoding.ReadBytes(raw, off+2, int(ol))
		if err != nil {
			return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated object")
		}
		off += 2 + int(ol)
		var grealm hosting.ID
		copy(grealm[:], grealmB)
		grants = append(grants, Grant{Realm: grealm, Grantee: string(gb), Priv: Privilege(priv), Scope: scope, Object: string(ob)})
	}
	if off != len(raw) {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "trailing ACL bytes")
	}
	return roles, grants, nil
}

// Deny is the typed error for a failed authorization check.
func Deny(op string) error {
	return nerr.New(nerr.Forbidden, op, "permission denied")
}
