package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestCreateRejectsReservedPrefix(t *testing.T) {
	s := testDB(t).Session()
	if _, err := s.Exec(`CREATE TABLE nsql_lock (id STRING PRIMARY KEY)`); err == nil {
		t.Fatal("expected reserved prefix error")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestCreateHistoryRequiresExactDDL(t *testing.T) {
	s := testDB(t).Session()
	if _, err := s.Exec(`CREATE TABLE nsql_schema_migrations (version STRING PRIMARY KEY, name STRING NOT NULL)`); err == nil {
		t.Fatal("expected exact DDL reject")
	} else if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if _, err := s.Exec(catalog.HistoryDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(catalog.HistoryDDL); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("second create: %v", err)
	}
}

func TestHistoryCreateGrantsTableDML(t *testing.T) {
	db := testDB(t)
	acl, err := security.CreateACL(filepath.Join(t.TempDir(), "acl"))
	if err != nil {
		t.Fatal(err)
	}
	users, err := auth.Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Upsert("mig", "pw"); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("mig", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("mig", security.PrivCreate, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	s := db.Session()
	s.SetIdentity("mig")
	s.SetACL(acl)
	s.SetAuth(users)
	if _, err := s.Exec(catalog.HistoryDDL); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO nsql_schema_migrations (version, name, checksum, execution_ms, dirty, direction) VALUES ('20260818120000', 't', 'abc', 0, 1, 'up')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`UPDATE nsql_schema_migrations SET dirty = 0 WHERE version = '20260818120000'`); err != nil {
		t.Fatal(err)
	}
	res, err := s.Exec(`SELECT version FROM nsql_schema_migrations`)
	if err != nil || len(res.Rows) != 1 {
		t.Fatalf("%+v %v", res, err)
	}
	if _, err := s.Exec(`DELETE FROM nsql_schema_migrations WHERE version = '20260818120000'`); err != nil {
		t.Fatal(err)
	}

	// Grants must not require PrivGrant; a second least-privilege user cannot GRANT.
	if _, err := s.Exec(`GRANT SELECT ON TABLE nsql_schema_migrations TO mig`); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("GRANT SQL must still require PrivGrant: %v", err)
	}
}
