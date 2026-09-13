// Package docs pins the prose to the engine.
//
// The list of statements NextSQL accepts is written out in four documents that
// serve different readers -- docs/sql.md (the dialect reference), USAGE.md (the
// operator manual), docs/web/content/docs/sql.md (the product site), and
// skills/using-nextsql/reference.md (the agent reference). Nothing kept them in
// agreement, so each new statement had to be remembered four times and a
// removed one could survive in a corner: `SHOW REALMS` was still advertised
// months after the parser stopped accepting it (log #280).
//
// These tests close that by deriving the truth from the parser rather than from
// another document: every statement the parser accepts must appear in all four.
package docs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/sql/parser"
)

// statementDocs are the documents that enumerate the statement surface.
var statementDocs = []string{
	"docs/sql.md",
	"USAGE.md",
	"docs/web/content/docs/sql.md",
	"skills/using-nextsql/reference.md",
}

// statements maps each statement keyword to a minimal example that must parse.
// A bare keyword is not a usable probe -- `EXPLAIN` alone fails on its *inner*
// statement with the same "expected a statement" the dispatcher uses for a word
// it does not know -- so each entry is a complete statement instead.
var statements = map[string]string{
	"ALTER":     "ALTER TABLE t ADD COLUMN c INT64",
	"ANALYZE":   "ANALYZE t",
	"BACKUP":    "BACKUP DATABASE",
	"BEGIN":     "BEGIN",
	"CANCEL":    "CANCEL QUERY 'q'",
	"CLUSTER":   "CLUSTER DRAIN",
	"COMMIT":    "COMMIT",
	"CREATE":    "CREATE TABLE t (id INT64 PRIMARY KEY)",
	"DELETE":    "DELETE FROM t",
	"DROP":      "DROP TABLE t",
	"EXPLAIN":   "EXPLAIN SELECT 1",
	"GRANT":     "GRANT SELECT ON TABLE t TO u",
	"INSERT":    "INSERT INTO t VALUES (1)",
	"MAINTAIN":  "MAINTAIN TABLE t",
	"REBUILD":   "REBUILD INDEX ix",
	"RELEASE":   "RELEASE SAVEPOINT s",
	"RESET":     "RESET RESOURCE GROUP",
	"REVOKE":    "REVOKE SELECT ON TABLE t FROM u",
	"ROLLBACK":  "ROLLBACK",
	"RUN":       "RUN WORKFLOW w()",
	"SAVEPOINT": "SAVEPOINT s",
	"SELECT":    "SELECT 1",
	"SET":       "SET RESOURCE GROUP g",
	"SHOW":      "SHOW TABLES",
	"SUBSCRIBE": "SUBSCRIBE TO t",
	"UPDATE":    "UPDATE t SET c = 1",
	"UPSERT":    "UPSERT INTO t VALUES (1)",
	"VERIFY":    "VERIFY BACKUP 'b'",
	"WITH":      "WITH c AS (SELECT 1 AS a FROM t) SELECT a FROM c",
}

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func readDoc(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(b)
}

// Every documented statement must actually parse.
func TestDocumentedStatementsParse(t *testing.T) {
	for kw, sql := range statements {
		if _, err := parser.Parse(sql); err != nil {
			t.Errorf("%s is documented as a statement but %q does not parse: %v", kw, sql, err)
		}
	}
}

// A word the list does not claim must not quietly become a statement: if one
// of these starts parsing, it is a new surface that needs documenting in all
// four places.
func TestUndocumentedWordsAreNotStatements(t *testing.T) {
	for _, kw := range []string{"MERGE", "TRUNCATE", "VACUUM", "CALL", "REPLACE"} {
		if _, err := parser.Parse(kw + " t"); err == nil {
			t.Errorf("%s now parses as a statement: add it to `statements` and to every document in statementDocs", kw)
		}
	}
}

// Every statement the parser accepts must appear in every statement document.
func TestEveryStatementIsDocumented(t *testing.T) {
	for _, doc := range statementDocs {
		body := strings.ToUpper(readDoc(t, doc))
		for kw := range statements {
			if !strings.Contains(body, kw) {
				t.Errorf("%s does not mention the %s statement", doc, kw)
			}
		}
	}
}

// `SHOW REALMS` went with multi-realm hosting (log #244) but stayed in the
// dialect reference until log #280. No current statement document may name it.
func TestRemovedShowRealmsIsNotAdvertised(t *testing.T) {
	if _, err := parser.Parse("SHOW REALMS"); err == nil {
		t.Fatal("SHOW REALMS parses; this test's premise is stale")
	}
	for _, doc := range statementDocs {
		if strings.Contains(strings.ToUpper(readDoc(t, doc)), "SHOW REALMS") {
			t.Errorf("%s names SHOW REALMS, which the parser rejects", doc)
		}
	}
}
