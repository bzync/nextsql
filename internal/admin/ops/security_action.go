package ops

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// Security administration (users, roles, grants) for Operations mode.
//
// Every operation here is an ordinary documented SQL statement issued on the
// operator's own authenticated connection, exactly like the maintenance,
// config, backup, and cluster action routes. The server-side RBAC that
// governs CREATE USER / DROP USER / CREATE ROLE / DROP ROLE / GRANT / REVOKE
// (internal/executor/security.go) is therefore the only authority: this
// handler adds no privilege of its own and can grant nothing the signed-in
// operator could not already grant by typing the same statement. It exists
// so that administering RBAC does not require hand-writing SQL.
//
// Requests are structured, never raw SQL: the statement text is rendered
// here from a closed set of operations, privileges, and scopes, and every
// interpolated name must first pass validIdent. That keeps the interpolation
// provably safe rather than relying on the server to reject a malformed
// statement after the fact — the same reasoning as maintenanceActionSQL.

const maxPasswordLen = 512

// securityPrivileges is the closed set of privilege keywords GRANT/REVOKE
// accepts. It deliberately omits "alter": internal/sql/parser's privilege
// list parser accepts a fixed keyword set plus a bare-identifier fallback,
// and "alter" lexes as its own reserved keyword outside that set, so
// `GRANT ALTER ON ...` does not parse even though the privilege exists.
// This mirrors GRANT_PRIVILEGES in the Studio builder
// (internal/admin/frontend/src/studio/resultTools.ts).
var securityPrivileges = map[string]bool{
	"connect": true, "select": true, "insert": true, "update": true,
	"delete": true, "create": true, "drop": true, "index": true,
	"execute": true, "usage": true, "grant": true, "backup": true,
	"restore": true, "replication": true, "cdc": true, "admin": true,
}

type securityActionRequest struct {
	Op string `json:"op"`

	// create_user / drop_user / create_role / drop_role
	Name string `json:"name"`
	// create_user only. Never logged and never echoed back in an error.
	Password string `json:"password"`

	// grant_role / revoke_role
	Role string `json:"role"`

	// grant / revoke
	Grantee       string   `json:"grantee"`
	AllPrivileges bool     `json:"all_privileges"`
	Privileges    []string `json:"privileges"`
	Scope         string   `json:"scope"`
	Object        string   `json:"object"`
	ColumnTable   string   `json:"column_table"`
	ColumnName    string   `json:"column_name"`
}

// quoteSQLString renders a SQL string literal. NextSQL's lexer takes bytes
// literally between quotes and recognizes only '' as an escape, so doubling
// the quote is the complete and only escaping needed.
func quoteSQLString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// securityActionSQL renders one security-administration request to the exact
// statement text docs/security.md documents. It returns an error rather than
// a statement whenever any name is not a plain identifier, so nothing
// unvalidated is ever interpolated.
func securityActionSQL(req securityActionRequest) (string, error) {
	const op = "ops.securityAction"
	name := strings.TrimSpace(req.Name)

	switch req.Op {
	case "create_user":
		if !validIdent(name) {
			return "", nerr.New(nerr.InvalidArgument, op, "user name must be a plain identifier")
		}
		if req.Password == "" {
			return "", nerr.New(nerr.InvalidArgument, op, "password is required")
		}
		if len(req.Password) > maxPasswordLen {
			return "", nerr.New(nerr.InvalidArgument, op, "password is too long")
		}
		return "CREATE USER " + name + " IDENTIFIED BY " + quoteSQLString(req.Password), nil
	case "drop_user":
		if !validIdent(name) {
			return "", nerr.New(nerr.InvalidArgument, op, "user name must be a plain identifier")
		}
		return "DROP USER " + name, nil
	case "create_role":
		if !validIdent(name) {
			return "", nerr.New(nerr.InvalidArgument, op, "role name must be a plain identifier")
		}
		return "CREATE ROLE " + name, nil
	case "drop_role":
		if !validIdent(name) {
			return "", nerr.New(nerr.InvalidArgument, op, "role name must be a plain identifier")
		}
		return "DROP ROLE " + name, nil
	case "grant_role", "revoke_role":
		role := strings.TrimSpace(req.Role)
		grantee := strings.TrimSpace(req.Grantee)
		if !validIdent(role) {
			return "", nerr.New(nerr.InvalidArgument, op, "role must be a plain identifier")
		}
		if !validIdent(grantee) {
			return "", nerr.New(nerr.InvalidArgument, op, "grantee must be a plain identifier")
		}
		// A role may be granted to a user or to another role — nested roles
		// are expanded transitively — but never to itself. The engine does
		// not reject that (GrantRole only checks the role exists), and the
		// fixpoint expansion tolerates the cycle, so it would silently record
		// a self-membership that means nothing. Refuse it here instead.
		if strings.EqualFold(role, grantee) {
			return "", nerr.New(nerr.InvalidArgument, op, "a role cannot be granted to itself")
		}
		if req.Op == "grant_role" {
			return "GRANT " + role + " TO " + grantee, nil
		}
		return "REVOKE " + role + " FROM " + grantee, nil
	case "grant", "revoke":
		return securityGrantSQL(req)
	default:
		return "", nerr.New(nerr.InvalidArgument, op, "unknown security op")
	}
}

// securityGrantSQL renders a privilege GRANT/REVOKE. The privilege and scope
// spellings match security.Privilege.String()/ScopeKind.String() and the
// Studio builder exactly, so a grant made here reads back identically in
// system.grants.
func securityGrantSQL(req securityActionRequest) (string, error) {
	const op = "ops.securityAction"
	grantee := strings.TrimSpace(req.Grantee)
	if !validIdent(grantee) {
		return "", nerr.New(nerr.InvalidArgument, op, "grantee must be a plain identifier")
	}
	verb, prep := "GRANT", "TO"
	if req.Op == "revoke" {
		verb, prep = "REVOKE", "FROM"
	}

	var privClause string
	switch {
	case req.AllPrivileges:
		privClause = "ALL PRIVILEGES"
	case len(req.Privileges) > 0:
		parts := make([]string, 0, len(req.Privileges))
		for _, p := range req.Privileges {
			p = strings.ToLower(strings.TrimSpace(p))
			if !securityPrivileges[p] {
				return "", nerr.New(nerr.InvalidArgument, op, "unknown privilege")
			}
			parts = append(parts, strings.ToUpper(p))
		}
		privClause = strings.Join(parts, ", ")
	default:
		return "", nerr.New(nerr.InvalidArgument, op, "select at least one privilege, or all privileges")
	}

	object := strings.TrimSpace(req.Object)
	named := func(keyword string, required bool) (string, error) {
		if object == "" {
			if required {
				return "", nerr.New(nerr.InvalidArgument, op, strings.ToLower(keyword)+" name is required")
			}
			return keyword, nil
		}
		if !validIdent(object) {
			return "", nerr.New(nerr.InvalidArgument, op, strings.ToLower(keyword)+" name must be a plain identifier")
		}
		return keyword + " " + object, nil
	}

	var scopeClause string
	var err error
	switch req.Scope {
	case "cluster":
		scopeClause = "CLUSTER"
	case "backup":
		scopeClause = "BACKUP"
	case "replication":
		scopeClause = "REPLICATION"
	case "administration":
		scopeClause = "ADMINISTRATION"
	case "database":
		// DATABASE alone means the connected database.
		scopeClause, err = named("DATABASE", false)
	case "schema":
		scopeClause, err = named("SCHEMA", true)
	case "table":
		scopeClause, err = named("TABLE", true)
	case "function":
		scopeClause, err = named("FUNCTION", true)
	case "resourcegroup":
		if object == "" {
			return "", nerr.New(nerr.InvalidArgument, op, "resource group name is required")
		}
		if !validIdent(object) {
			return "", nerr.New(nerr.InvalidArgument, op, "resource group name must be a plain identifier")
		}
		scopeClause = "RESOURCE GROUP " + object
	case "column":
		column := strings.TrimSpace(req.ColumnName)
		if !validIdent(column) {
			return "", nerr.New(nerr.InvalidArgument, op, "column name must be a plain identifier")
		}
		table := strings.TrimSpace(req.ColumnTable)
		if table == "" {
			scopeClause = "COLUMN " + column
			break
		}
		if !validIdent(table) {
			return "", nerr.New(nerr.InvalidArgument, op, "table name must be a plain identifier")
		}
		scopeClause = "COLUMN " + table + "." + column
	default:
		return "", nerr.New(nerr.InvalidArgument, op, "unknown scope")
	}
	if err != nil {
		return "", err
	}
	return verb + " " + privClause + " ON " + scopeClause + " " + prep + " " + grantee, nil
}

// handleSecurityAction issues one security-administration statement on the
// operator's own connection. A caller without the required privilege is
// refused by the server, surfaced here as 403 — the Admin process never
// decides who may administer RBAC.
func (s *Server) handleSecurityAction(w http.ResponseWriter, r *http.Request, sess *session) {
	var req securityActionRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxActionBody)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	sql, err := securityActionSQL(req)
	if err != nil {
		writeError(w, http.StatusBadRequest, userError(err))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	res, err := sess.query(ctx, sql)
	if err != nil {
		status := http.StatusBadGateway
		switch {
		case nerr.HasCode(err, nerr.Unauthorized), nerr.HasCode(err, nerr.Forbidden):
			status = http.StatusForbidden
		case nerr.HasCode(err, nerr.NotFound):
			status = http.StatusNotFound
		case nerr.HasCode(err, nerr.AlreadyExists), nerr.HasCode(err, nerr.InvalidArgument), nerr.HasCode(err, nerr.Unavailable):
			status = http.StatusConflict
		}
		writeError(w, status, userError(err))
		return
	}
	writeJSON(w, http.StatusOK, res)
}
