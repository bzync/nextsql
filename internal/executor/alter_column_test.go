package executor

import (
	"strings"
	"testing"
)

// ALTER TABLE had no ALTER COLUMN at all: a column's nullability and default
// were fixed at CREATE TABLE, and the only way to change either was to rebuild
// the table.
func TestAlterColumnSetNotNull(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, sess, "INSERT INTO t VALUES (1, 5)")
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN n SET NOT NULL")
	if _, err := sess.Exec("INSERT INTO t (id) VALUES (2)"); err == nil {
		t.Fatal("a NULL was accepted after SET NOT NULL")
	}
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN n DROP NOT NULL")
	mustExec(t, sess, "INSERT INTO t (id) VALUES (2)")
}

// Existing rows must already satisfy the constraint: nothing re-checks them
// afterwards, so admitting it over a NULL would leave the table holding a row
// no write could reproduce.
func TestAlterColumnSetNotNullRefusesExistingNull(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64)")
	mustExec(t, sess, "INSERT INTO t (id) VALUES (1)")
	_, err := sess.Exec("ALTER TABLE t ALTER COLUMN n SET NOT NULL")
	if err == nil {
		t.Fatal("SET NOT NULL was accepted over an existing NULL")
	}
	if !strings.Contains(err.Error(), "NULL") {
		t.Fatalf("error does not explain the cause: %v", err)
	}
	// The refused statement must not have changed the column.
	mustExec(t, sess, "INSERT INTO t (id) VALUES (2)")
}

func TestAlterColumnDefault(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, s STRING)")
	mustExec(t, sess, "INSERT INTO t (id) VALUES (1)")
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN s SET DEFAULT 'dflt'")
	mustExec(t, sess, "INSERT INTO t (id) VALUES (2)")
	// A default supplies a value for writes that omit the column; rows already
	// written keep what they were written with.
	got := rowsOf(t, sess, "SELECT id, s FROM t ORDER BY id")
	if len(got) != 2 || got[0][1] != "NULL" || got[1][1] != "dflt" {
		t.Fatalf("rows after SET DEFAULT = %v", got)
	}
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN s DROP DEFAULT")
	mustExec(t, sess, "INSERT INTO t (id) VALUES (3)")
	got = rowsOf(t, sess, "SELECT s FROM t WHERE id = 3")
	if len(got) != 1 || got[0][0] != "NULL" {
		t.Fatalf("row after DROP DEFAULT = %v", got)
	}
}

func TestAlterColumnRefusals(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, n INT64, ts TIMESTAMPTZ)")
	for _, sql := range []string{
		// A primary key column is NOT NULL by definition.
		"ALTER TABLE t ALTER COLUMN id DROP NOT NULL",
		"ALTER TABLE t ALTER COLUMN nope SET NOT NULL",
		// A default must satisfy the same rules a default written at CREATE
		// TABLE satisfies.
		"ALTER TABLE t ALTER COLUMN n SET DEFAULT NOW()",
		"ALTER TABLE t ALTER COLUMN n SET DEFAULT 'not a number'",
		"ALTER TABLE t ALTER COLUMN n SET SOMETHING",
		"ALTER TABLE t ALTER COLUMN n",
	} {
		if _, err := sess.Exec(sql); err == nil {
			t.Fatalf("accepted an invalid statement: %s", sql)
		}
	}
	// The valid function default for the column's own type still works.
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN ts SET DEFAULT NOW()")
}

func TestAlterColumnSurvivesReopenAndShowsInDDL(t *testing.T) {
	sess := newCheckSession(t)
	mustExec(t, sess, "CREATE TABLE t (id INT64 PRIMARY KEY, s STRING)")
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN s SET NOT NULL")
	mustExec(t, sess, "ALTER TABLE t ALTER COLUMN s SET DEFAULT 'x'")
	ddl := rowsOf(t, sess, "SELECT ddl FROM system.table_ddl WHERE table_name = 't' AND object_type = 'TABLE'")
	if len(ddl) != 1 || !strings.Contains(ddl[0][0], "NOT NULL") || !strings.Contains(ddl[0][0], "DEFAULT") {
		t.Fatalf("rendered DDL lost the column change: %v", ddl)
	}
	cols := rowsOf(t, sess, "SELECT column_name, not_null FROM system.columns WHERE table_name = 't' AND column_name = 's'")
	if len(cols) != 1 || cols[0][1] != "TRUE" {
		t.Fatalf("system.columns did not report the change: %v", cols)
	}
}
