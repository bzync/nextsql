package migrate

import (
	"strings"
	"testing"
)

func TestSplitOneAndMany(t *testing.T) {
	stmts, err := Split("CREATE TABLE t (id UUID);")
	if err != nil || len(stmts) != 1 || !strings.Contains(stmts[0], "CREATE TABLE") {
		t.Fatalf("%v %v", stmts, err)
	}
	stmts, err = Split(`
CREATE TABLE customers (id UUID PRIMARY KEY);
CREATE INDEX ix_customers_id ON customers (id);
`)
	if err != nil || len(stmts) != 2 {
		t.Fatalf("%v %v", stmts, err)
	}
}

func TestSplitIgnoresSemiInStringIdentComment(t *testing.T) {
	src := `
INSERT INTO t (name) VALUES ('a;b'); -- also ; here
INSERT INTO t (name) VALUES ("x;y");
/* block ; comment */
ANALYZE
`
	stmts, err := Split(src)
	if err != nil {
		t.Fatal(err)
	}
	if len(stmts) != 3 {
		t.Fatalf("%d %#v", len(stmts), stmts)
	}
	if !strings.Contains(stmts[0], "'a;b'") || !strings.Contains(stmts[1], `"x;y"`) {
		t.Fatalf("%#v", stmts)
	}
}

func TestSplitEscapedQuotes(t *testing.T) {
	src := `INSERT INTO t (s) VALUES ('it''s;fine'); INSERT INTO t (s) VALUES ("a""b;c");`
	stmts, err := Split(src)
	if err != nil || len(stmts) != 2 {
		t.Fatalf("%v %v", stmts, err)
	}
}

func TestSplitDropsEmptyAndComments(t *testing.T) {
	stmts, err := Split("-- only comments\n/* and a block */\n;\n")
	if err != nil || len(stmts) != 0 {
		t.Fatalf("%v %v", stmts, err)
	}
}

func TestSplitUnterminated(t *testing.T) {
	if _, err := Split("INSERT INTO t (s) VALUES ('oops"); err == nil {
		t.Fatal("expected unterminated string")
	}
	if _, err := Split("ANALYZE /*"); err == nil {
		t.Fatal("expected unterminated comment")
	}
}
