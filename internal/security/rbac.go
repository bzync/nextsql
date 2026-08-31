package security

import (
	"os"
	"strings"
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	aclMagic   = "NSAC"
	aclVersion = 1
	maxACLName = 128
	maxRoles   = 4096
	maxGrants  = 16384
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
)

// Grant is one persisted authorization.
type Grant struct {
	Grantee string
	Priv    Privilege
	Scope   ScopeKind
	Object  string
}

// ACL is a least-privilege role catalog. A missing grant is a deny.
type ACL struct {
	mu     sync.Mutex
	path   string
	roles  map[string][]string // role -> members (users or roles)
	grants []Grant
}

func CreateACL(path string) (*ACL, error) {
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "security.CreateACL", "ACL file exists")
	}
	a := &ACL{path: path, roles: make(map[string][]string)}
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

func (a *ACL) AddUser(user string) error {
	name, err := normalizeName(user)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.roles[userKey(name)]; !ok {
		// users are principals; they need no role entry until granted a role
	}
	_ = name
	return a.persist()
}

func (a *ACL) CreateRole(role string) error {
	name, err := normalizeName(role)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.roles[name]; ok {
		return nerr.New(nerr.AlreadyExists, "security.CreateRole", "role exists")
	}
	if len(a.roles) >= maxRoles {
		return nerr.New(nerr.InvalidArgument, "security.CreateRole", "too many roles")
	}
	a.roles[name] = nil
	return a.persist()
}

func (a *ACL) DropRole(role string) error {
	name, err := normalizeName(role)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if _, ok := a.roles[name]; !ok {
		return nerr.New(nerr.NotFound, "security.DropRole", "unknown role")
	}
	delete(a.roles, name)
	kept := a.grants[:0]
	for _, g := range a.grants {
		if g.Grantee != name {
			kept = append(kept, g)
		}
	}
	a.grants = kept
	return a.persist()
}

func (a *ACL) DropUser(user string) error {
	name, err := normalizeName(user)
	if err != nil {
		return err
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	for role, members := range a.roles {
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
		if g.Grantee != name {
			kept = append(kept, g)
		}
	}
	a.grants = kept
	return a.persist()
}

func (a *ACL) GrantRole(role, member string) error {
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
	if _, ok := a.roles[r]; !ok {
		return nerr.New(nerr.NotFound, "security.GrantRole", "unknown role")
	}
	for _, existing := range a.roles[r] {
		if existing == m {
			return nil
		}
	}
	a.roles[r] = append(a.roles[r], m)
	return a.persist()
}

func (a *ACL) RevokeRole(role, member string) error {
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
	members := a.roles[r]
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
	a.roles[r] = out
	return a.persist()
}

func (a *ACL) Grant(grantee string, priv Privilege, scope ScopeKind, object string) error {
	name, err := normalizeName(grantee)
	if err != nil {
		return err
	}
	if priv == 0 || scope == 0 {
		return nerr.New(nerr.InvalidArgument, "security.Grant", "privilege and scope are required")
	}
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.grants) >= maxGrants {
		return nerr.New(nerr.InvalidArgument, "security.Grant", "too many grants")
	}
	for _, g := range a.grants {
		if g.Grantee == name && g.Priv == priv && g.Scope == scope && g.Object == object {
			return nil
		}
	}
	a.grants = append(a.grants, Grant{Grantee: name, Priv: priv, Scope: scope, Object: object})
	return a.persist()
}

func (a *ACL) Revoke(grantee string, priv Privilege, scope ScopeKind, object string) error {
	name, err := normalizeName(grantee)
	if err != nil {
		return err
	}
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	kept := a.grants[:0]
	found := false
	for _, g := range a.grants {
		if g.Grantee == name && g.Priv == priv && g.Scope == scope && g.Object == object {
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

// Allowed reports whether user may exercise priv on scope/object.
// Cluster ADMIN is a superuser. Empty object on a grant matches any object
// at that scope. Table grants do not imply column grants unless object is empty.
func (a *ACL) Allowed(user string, priv Privilege, scope ScopeKind, object string) bool {
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
	return a.allowedForLocked(a.expandLocked(name), priv, scope, object)
}

// AllowedScoped is Allowed further narrowed to the privileges reachable through
// the named roles, as carried by a short-lived credential's role scope. The
// user must be a member of every listed role; an empty roles slice is
// identical to Allowed. A listed role the user does not hold denies every
// check (the credential cannot escalate).
func (a *ACL) AllowedScoped(user string, roles []string, priv Privilege, scope ScopeKind, object string) bool {
	if a == nil {
		return true
	}
	if len(roles) == 0 {
		return a.Allowed(user, priv, scope, object)
	}
	name := strings.ToLower(strings.TrimSpace(user))
	if name == "" {
		return false
	}
	object = strings.ToLower(strings.TrimSpace(object))
	a.mu.Lock()
	defer a.mu.Unlock()
	member := a.expandLocked(name)
	principals := make(map[string]struct{})
	for _, role := range roles {
		r := strings.ToLower(strings.TrimSpace(role))
		if r == "" {
			return false
		}
		if _, ok := member[r]; !ok {
			return false
		}
		for k := range a.expandLocked(r) {
			principals[k] = struct{}{}
		}
	}
	return a.allowedForLocked(principals, priv, scope, object)
}

func (a *ACL) allowedForLocked(principals map[string]struct{}, priv Privilege, scope ScopeKind, object string) bool {
	if a.hasLocked(principals, PrivAdmin, ScopeCluster, "") {
		return true
	}
	if a.hasLocked(principals, PrivAdmin, ScopeAdmin, "") {
		return true
	}
	if a.hasLocked(principals, priv, scope, object) {
		return true
	}
	if object != "" && a.hasLocked(principals, priv, scope, "") {
		return true
	}
	if scope == ScopeColumn {
		table, _, ok := strings.Cut(object, ".")
		if ok && a.hasLocked(principals, priv, ScopeTable, table) {
			return true
		}
		if a.hasLocked(principals, priv, ScopeTable, "") {
			return true
		}
	}
	return false
}

func (a *ACL) hasLocked(principals map[string]struct{}, priv Privilege, scope ScopeKind, object string) bool {
	for _, g := range a.grants {
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

func (a *ACL) expandLocked(user string) map[string]struct{} {
	out := map[string]struct{}{user: {}}
	changed := true
	for changed {
		changed = false
		for role, members := range a.roles {
			if _, already := out[role]; already {
				continue
			}
			for _, m := range members {
				if _, ok := out[m]; ok {
					out[role] = struct{}{}
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
	default:
		return 0, nerr.New(nerr.InvalidArgument, "security.ParseScope", "unknown scope")
	}
}

func encodeACL(roles map[string][]string, grants []Grant) ([]byte, error) {
	if len(roles) > maxRoles || len(grants) > maxGrants {
		return nil, nerr.New(nerr.InvalidArgument, "security.encodeACL", "ACL exceeds limit")
	}
	n := 4 + 2 + 4
	for role, members := range roles {
		n += 2 + len(role) + 2
		for _, m := range members {
			n += 2 + len(m)
		}
	}
	n += 4
	for _, g := range grants {
		n += 2 + len(g.Grantee) + 2 + 1 + 2 + len(g.Object)
	}
	buf := make([]byte, n)
	copy(buf[0:4], aclMagic)
	encoding.PutU16(buf, 4, aclVersion)
	encoding.PutU32(buf, 6, uint32(len(roles)))
	off := 10
	for role, members := range roles {
		encoding.PutU16(buf, off, uint16(len(role)))
		off += 2
		copy(buf[off:], role)
		off += len(role)
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

func decodeACL(raw []byte) (map[string][]string, []Grant, error) {
	if len(raw) < 10 {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "truncated ACL")
	}
	if string(raw[0:4]) != aclMagic {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "bad ACL magic")
	}
	if encoding.U16(raw, 4) != aclVersion {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "unsupported ACL version")
	}
	nrole := encoding.U32(raw, 6)
	if nrole > maxRoles {
		return nil, nil, nerr.New(nerr.InvalidFormat, "security.decodeACL", "role count exceeds limit")
	}
	roles := make(map[string][]string, nrole)
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
		roles[string(nameb)] = members
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
		if scope < ScopeCluster || scope > ScopeAdmin {
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
	return roles, grants, nil
}

// Deny is the typed error for a failed authorization check.
func Deny(op string) error {
	return nerr.New(nerr.Forbidden, op, "permission denied")
}
