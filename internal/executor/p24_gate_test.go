package executor

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
)

func TestP24SearchQualityFixtures(t *testing.T) {
	db := testDB(t)
	s := db.Session()
	execOK(t, s, `CREATE TABLE p24_quality (id STRING PRIMARY KEY, body TEXT)`)
	execOK(t, s, `INSERT INTO p24_quality (id, body) VALUES
		('short', 'database performance'),
		('long', 'database performance tuning reference manual'),
		('reverse', 'performance database'),
		('plural', 'cats only'),
		('en', 'the fast automobile runs'),
		('fr', 'les chevaux rapides'),
		('de', 'die katzen laufen'),
		('es', 'los trabajadores trabajando')`)

	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_simple ON p24_quality (body)`)
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR '"database performance"'`), "short", "long")
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'cat'`))
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'data*'`), "reverse", "short", "long")
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'databas~'`), "reverse", "short", "long")
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'databse performance'`), "reverse", "short", "long")

	execOK(t, s, `DROP INDEX ix_p24_simple`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_en ON p24_quality (body) WITH (ANALYZER = 'english')`)
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR '"quick car running"'`), "en")

	execOK(t, s, `DROP INDEX ix_p24_en`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_fr ON p24_quality (body) WITH (ANALYZER = 'french')`)
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'cheval'`), "fr")

	execOK(t, s, `DROP INDEX ix_p24_fr`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_de ON p24_quality (body) WITH (ANALYZER = 'german')`)
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'katze'`), "de")

	execOK(t, s, `DROP INDEX ix_p24_de`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_es ON p24_quality (body) WITH (ANALYZER = 'spanish')`)
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_quality SEARCH body FOR 'trabajar'`), "es")
}

func TestP24FuzzyVocabularyCap(t *testing.T) {
	db, err := Create(filepath.Join(t.TempDir(), "nextsql.db"), testKeys(t), 256)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s := db.Session()
	execOK(t, s, `CREATE TABLE p24_adversarial (id STRING PRIMARY KEY, body TEXT)`)
	const termsPerRow = 64
	for first := 0; first <= fulltext.MaxFuzzyVocabularyTerms; first += termsPerRow {
		last := first + termsPerRow
		if last > fulltext.MaxFuzzyVocabularyTerms+1 {
			last = fulltext.MaxFuzzyVocabularyTerms + 1
		}
		var body strings.Builder
		for i := first; i < last; i++ {
			fmt.Fprintf(&body, "vocab%04d ", i)
		}
		execOK(t, s, fmt.Sprintf(`INSERT INTO p24_adversarial (id, body) VALUES ('row%04d', '%s')`, first/termsPerRow, body.String()))
	}

	if _, err := s.Exec(`SELECT id FROM p24_adversarial SEARCH body FOR 'zzzzz~2'`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("seq-scan fuzzy vocabulary cap: %v", err)
	}
	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_adversarial ON p24_adversarial (body)`)
	if _, err := s.Exec(`SELECT id FROM p24_adversarial SEARCH body FOR 'zzzzz~2'`); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("indexed fuzzy vocabulary cap: %v", err)
	}
}

func TestP24EncryptedCrashRecovery(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keys := testKeys(t)
	db, err := Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	const committedMarker = "p24secretzxqdatabase"
	const uncommittedMarker = "p24uncommittedzxq"
	execOK(t, s, `CREATE TABLE p24_recovery (id STRING PRIMARY KEY, body TEXT)`)
	execOK(t, s, `CREATE FULLTEXT INDEX ix_p24_recovery ON p24_recovery (body) WITH (ANALYZER = 'english')`)
	execOK(t, s, `INSERT INTO p24_recovery (id, body) VALUES ('committed', 'the fast automobile runs `+committedMarker+`')`)
	execOK(t, s, `BEGIN`)
	execOK(t, s, `UPDATE p24_recovery SET body = 'the slow bicycle stopped `+uncommittedMarker+`' WHERE id = 'committed'`)
	db.Eng.Kill()

	assertP24Ciphertext(t, path, committedMarker, uncommittedMarker)
	assertP24Ciphertext(t, path+".wal", committedMarker, uncommittedMarker)
	assertP24Ciphertext(t, path+".undo", committedMarker, uncommittedMarker)

	db, err = Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	s = db.Session()
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_recovery SEARCH body FOR '"quick car running"'`), "committed")
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_recovery SEARCH body FOR '`+committedMarker+`'`), "committed")
	assertP24IDs(t, execOK(t, s, `SELECT id FROM p24_recovery SEARCH body FOR '`+uncommittedMarker+`'`))
	plan := execOK(t, s, `EXPLAIN SELECT id FROM p24_recovery SEARCH body FOR 'car'`)
	if !explainHas(plan, "ix_p24_recovery") || !explainHas(plan, "analyzer=english") {
		t.Fatalf("recovered analyzer plan: %+v", explainOps(plan))
	}
}

func assertP24IDs(t *testing.T, got *Result, want ...string) {
	t.Helper()
	if len(got.Rows) != len(want) {
		t.Fatalf("ids=%v want=%v", titles(got), want)
	}
	for i := range want {
		if got.Rows[i][0].Null || got.Rows[i][0].Str != want[i] {
			t.Fatalf("ids=%v want=%v", titles(got), want)
		}
	}
}

func assertP24Ciphertext(t *testing.T, root string, markers ...string) {
	t.Helper()
	if _, err := os.Stat(root); os.IsNotExist(err) {
		return
	} else if err != nil {
		t.Fatal(err)
	}
	if err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, marker := range markers {
			if bytes.Contains(raw, []byte(marker)) {
				t.Errorf("plaintext full-text marker in %s", path)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
