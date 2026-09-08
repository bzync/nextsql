package ops

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestSecurityActionSQL pins the exact statement text each structured
// security-administration request renders to. The statements must match the
// spellings docs/security.md documents, because the resulting grants are read
// back through system.users/system.roles/system.grants.
func TestSecurityActionSQL(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  securityActionRequest
		want string
	}{
		{"create user", securityActionRequest{Op: "create_user", Name: "app", Password: "hunter2"},
			"CREATE USER app IDENTIFIED BY 'hunter2'"},
		{"create user escapes quote", securityActionRequest{Op: "create_user", Name: "app", Password: "it's"},
			"CREATE USER app IDENTIFIED BY 'it''s'"},
		{"drop user", securityActionRequest{Op: "drop_user", Name: "app"}, "DROP USER app"},
		{"create role", securityActionRequest{Op: "create_role", Name: "analyst"}, "CREATE ROLE analyst"},
		{"drop role", securityActionRequest{Op: "drop_role", Name: "analyst"}, "DROP ROLE analyst"},
		{"grant role", securityActionRequest{Op: "grant_role", Role: "analyst", Grantee: "app"},
			"GRANT analyst TO app"},
		{"revoke role", securityActionRequest{Op: "revoke_role", Role: "analyst", Grantee: "app"},
			"REVOKE analyst FROM app"},
		{"grant on table", securityActionRequest{Op: "grant", Grantee: "analyst", Privileges: []string{"select"}, Scope: "table", Object: "products"},
			"GRANT SELECT ON TABLE products TO analyst"},
		{"revoke on table", securityActionRequest{Op: "revoke", Grantee: "analyst", Privileges: []string{"select"}, Scope: "table", Object: "products"},
			"REVOKE SELECT ON TABLE products FROM analyst"},
		{"multiple privileges keep order", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"insert", "update", "delete"}, Scope: "table", Object: "orders"},
			"GRANT INSERT, UPDATE, DELETE ON TABLE orders TO app"},
		{"all privileges", securityActionRequest{Op: "grant", Grantee: "app", AllPrivileges: true, Scope: "table", Object: "orders"},
			"GRANT ALL PRIVILEGES ON TABLE orders TO app"},
		{"admin on cluster", securityActionRequest{Op: "grant", Grantee: "dba", Privileges: []string{"admin"}, Scope: "cluster"},
			"GRANT ADMIN ON CLUSTER TO dba"},
		{"usage on resource group", securityActionRequest{Op: "grant", Grantee: "analyst", Privileges: []string{"usage"}, Scope: "resourcegroup", Object: "reporting"},
			"GRANT USAGE ON RESOURCE GROUP reporting TO analyst"},
		{"database scope without a name means the connected database", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"connect"}, Scope: "database"},
			"GRANT CONNECT ON DATABASE TO app"},
		{"database scope with a name", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"connect"}, Scope: "database", Object: "sales"},
			"GRANT CONNECT ON DATABASE sales TO app"},
		{"column scope with table", securityActionRequest{Op: "grant", Grantee: "analyst", Privileges: []string{"select"}, Scope: "column", ColumnTable: "products", ColumnName: "price"},
			"GRANT SELECT ON COLUMN products.price TO analyst"},
		{"column scope without table", securityActionRequest{Op: "grant", Grantee: "analyst", Privileges: []string{"select"}, Scope: "column", ColumnName: "price"},
			"GRANT SELECT ON COLUMN price TO analyst"},
		{"names are trimmed", securityActionRequest{Op: "create_role", Name: "  analyst  "}, "CREATE ROLE analyst"},
		{"privilege case is normalized", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"SeLeCt"}, Scope: "table", Object: "t"},
			"GRANT SELECT ON TABLE t TO app"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := securityActionSQL(tc.req)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// TestSecurityActionSQLRefusals covers every path that must produce an error
// instead of a statement. Nothing that fails validation may reach the server
// as SQL: a name that is not a plain identifier is the only thing standing
// between a JSON string and text interpolated into a hand-built statement.
func TestSecurityActionSQLRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  securityActionRequest
	}{
		{"unknown op", securityActionRequest{Op: "make_admin", Name: "app"}},
		{"empty op", securityActionRequest{Name: "app"}},
		{"user name with a space", securityActionRequest{Op: "create_user", Name: "a b", Password: "x"}},
		{"user name with a quote", securityActionRequest{Op: "create_user", Name: "a'b", Password: "x"}},
		{"user name with a semicolon", securityActionRequest{Op: "drop_user", Name: "app; DROP USER root"}},
		{"user name starting with a digit", securityActionRequest{Op: "create_user", Name: "1app", Password: "x"}},
		{"empty user name", securityActionRequest{Op: "create_user", Name: "", Password: "x"}},
		{"missing password", securityActionRequest{Op: "create_user", Name: "app"}},
		{"password too long", securityActionRequest{Op: "create_user", Name: "app", Password: strings.Repeat("x", maxPasswordLen+1)}},
		{"role name with a dash", securityActionRequest{Op: "create_role", Name: "read-only"}},
		{"grant role with bad grantee", securityActionRequest{Op: "grant_role", Role: "analyst", Grantee: "a b"}},
		{"role granted to itself", securityActionRequest{Op: "grant_role", Role: "analyst", Grantee: "analyst"}},
		{"role revoked from itself", securityActionRequest{Op: "revoke_role", Role: "analyst", Grantee: "analyst"}},
		{"self-grant differing only in case", securityActionRequest{Op: "grant_role", Role: "Analyst", Grantee: "analyst"}},
		{"grant with no privileges", securityActionRequest{Op: "grant", Grantee: "app", Scope: "table", Object: "t"}},
		{"grant with unknown privilege", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"superuser"}, Scope: "table", Object: "t"}},
		{"grant with alter, which does not parse", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"alter"}, Scope: "table", Object: "t"}},
		{"grant with unknown scope", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"select"}, Scope: "galaxy", Object: "t"}},
		{"table scope without a name", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"select"}, Scope: "table"}},
		{"schema scope without a name", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"select"}, Scope: "schema"}},
		{"resource group without a name", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"usage"}, Scope: "resourcegroup"}},
		{"column scope without a column", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"select"}, Scope: "column", ColumnTable: "t"}},
		{"object name with injection", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"select"}, Scope: "table", Object: "t TO root; --"}},
		{"column table with injection", securityActionRequest{Op: "grant", Grantee: "app", Privileges: []string{"select"}, Scope: "column", ColumnTable: "t\"", ColumnName: "c"}},
		{"grantee with injection", securityActionRequest{Op: "grant", Grantee: "app; DROP USER root", Privileges: []string{"select"}, Scope: "table", Object: "t"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := securityActionSQL(tc.req)
			if err == nil {
				t.Fatalf("expected a refusal, got statement %q", got)
			}
			if got != "" {
				t.Fatalf("a refused request must render no statement, got %q", got)
			}
		})
	}
}

// TestSecurityActionSQLNeverLeaksPassword guards the one request field that
// carries a secret: a validation failure must not echo the password back in
// the error a caller sees.
func TestSecurityActionSQLNeverLeaksPassword(t *testing.T) {
	const secret = "sup3r-s3cret-value"
	_, err := securityActionSQL(securityActionRequest{Op: "create_user", Name: "bad name", Password: secret})
	if err == nil {
		t.Fatal("expected a refusal for an invalid user name")
	}
	if strings.Contains(err.Error(), secret) {
		t.Fatalf("error leaked the password: %v", err)
	}
}

// TestSecurityActionRequiresAuthAndCSRF pins that RBAC administration sits
// behind the same session + CSRF gate as every other mutating route. Without
// it, a page in the operator's browser could drive user creation and grants
// cross-site.
func TestSecurityActionRequiresAuthAndCSRF(t *testing.T) {
	s := testServer(t)
	body := `{"op":"create_role","name":"analyst"}`

	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/v1/security/action", strings.NewReader(body)))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no session: want 401, got %d", rec.Code)
	}

	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/security/action", strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
	rec = httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("no CSRF: want 403, got %d", rec.Code)
	}
}

// TestSecurityActionRejectsMalformedBody covers the decode path: an invalid
// or unknown request must be refused before any statement is built, never
// passed through to the connection.
func TestSecurityActionRejectsMalformedBody(t *testing.T) {
	s := testServer(t)
	sess, err := s.sessions.create(nil, "op", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name string
		body string
		want int
	}{
		{"not JSON", `{`, http.StatusBadRequest},
		{"unknown op", `{"op":"escalate"}`, http.StatusBadRequest},
		{"injection in name", `{"op":"create_role","name":"a; DROP USER root"}`, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest("POST", "/api/v1/security/action", strings.NewReader(tc.body))
			req.AddCookie(&http.Cookie{Name: sessionCookie, Value: sess.id})
			req.Header.Set(csrfHeader, sess.csrf)
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("want %d, got %d (%s)", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}
