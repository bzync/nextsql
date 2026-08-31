package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

func TestValidateOKFixture(t *testing.T) {
	migs, err := Validate(filepath.Join("testdata", "ok"))
	if err != nil {
		t.Fatal(err)
	}
	if len(migs) != 3 {
		t.Fatalf("versions %d", len(migs))
	}
	if migs[0].Version != "20260818120000" || migs[0].Down == nil {
		t.Fatalf("%+v", migs[0])
	}
	if migs[2].Version != "20260818120200" || migs[2].Down != nil {
		t.Fatalf("forward-only %+v", migs[2])
	}
	if migs[0].Up.Checksum == "" {
		t.Fatal("missing checksum")
	}
}

func TestValidateMultiStatement(t *testing.T) {
	migs, err := Validate(filepath.Join("testdata", "multi"))
	if err != nil || len(migs) != 1 {
		t.Fatalf("%v %v", migs, err)
	}
}

func TestValidateFixtureRejects(t *testing.T) {
	cases := []struct {
		dir, want string
		code      nerr.Code
	}{
		{"testdata/invalid/down_only", "down without up", nerr.InvalidArgument},
		{"testdata/invalid/duplicate", "duplicate version", nerr.InvalidArgument},
		{"testdata/invalid/bad_name", "invalid filename", nerr.InvalidArgument},
		{"testdata/invalid/begin", "BEGIN/COMMIT/ROLLBACK", nerr.InvalidArgument},
		{"testdata/invalid/grant", "GRANT/REVOKE", nerr.InvalidArgument},
		{"testdata/invalid/set_tenant", "parse", nerr.Syntax},
		{"testdata/invalid/create_user", "CREATE/DROP USER", nerr.InvalidArgument},
		{"testdata/invalid/syntax", "parse", nerr.Syntax},
	}
	for _, tc := range cases {
		t.Run(tc.dir, func(t *testing.T) {
			_, err := Validate(tc.dir)
			if err == nil {
				t.Fatal("expected error")
			}
			if !nerr.HasCode(err, tc.code) {
				t.Fatalf("code %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%v", err)
			}
		})
	}
}

func TestValidateRejectsMoreSecurityAndTxn(t *testing.T) {
	cases := []string{
		"COMMIT",
		"ROLLBACK",
		"RESET TENANT",
		"REVOKE SELECT ON products FROM analyst",
		"DROP USER app",
		"CREATE ROLE analyst",
		"DROP ROLE analyst",
		"EXPLAIN BEGIN",
	}
	for _, src := range cases {
		dir := t.TempDir()
		name := "20260818120000_x.up.sql"
		if err := os.WriteFile(filepath.Join(dir, name), []byte(src+";\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Validate(dir); err == nil {
			t.Fatalf("accepted %s", src)
		}
	}
}

func TestValidateAllowsImplementedDDL(t *testing.T) {
	cases := []string{
		"ALTER TABLE customers ADD name STRING",
		"CREATE DATABASE app",
		"DROP TABLE customers",
		"DROP INDEX ix_customers_name",
		"REBUILD INDEX ix_customers_name",
	}
	for _, src := range cases {
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "20260818120000_x.up.sql"), []byte(src+";\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := Validate(dir); err != nil {
			t.Fatalf("%s: %v", src, err)
		}
	}
}

func TestValidateEmptyDir(t *testing.T) {
	migs, err := Validate(t.TempDir())
	if err != nil || len(migs) != 0 {
		t.Fatalf("%v %v", migs, err)
	}
}

func TestValidateMissingDir(t *testing.T) {
	_, err := Validate(filepath.Join(t.TempDir(), "missing"))
	if !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("%v", err)
	}
}

func TestValidateTooManyStatements(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < MaxStatementsPerFile+1; i++ {
		b.WriteString("ANALYZE;\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_x.up.sql"), []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Validate(dir)
	if err == nil || !strings.Contains(err.Error(), "more than 32") {
		t.Fatalf("%v", err)
	}
}

func TestValidateStatementSize(t *testing.T) {
	dir := t.TempDir()
	stmt := "ANALYZE " + strings.Repeat("x", security.MaxSQLBytes)
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_x.up.sql"), []byte(stmt), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Validate(dir)
	if err == nil || !strings.Contains(err.Error(), "1 MiB") {
		t.Fatalf("%v", err)
	}
}

func TestValidateCRLFParses(t *testing.T) {
	dir := t.TempDir()
	src := "CREATE TABLE t (id UUID PRIMARY KEY);\r\n"
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_t.up.sql"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	migs, err := Validate(dir)
	if err != nil || len(migs) != 1 {
		t.Fatalf("%v %v", migs, err)
	}
}

func TestCreateThenValidate(t *testing.T) {
	dir := t.TempDir()
	if _, _, err := Create(dir, "add_orders"); err != nil {
		t.Fatal(err)
	}
	migs, err := Validate(dir)
	if err != nil || len(migs) != 1 || migs[0].Down == nil {
		t.Fatalf("%v %v", migs, err)
	}
}
