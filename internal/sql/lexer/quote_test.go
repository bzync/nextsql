package lexer

import (
	"testing"
)

func tokens(t *testing.T, src string) []Token {
	t.Helper()
	l := New(src)
	var out []Token
	for {
		tok := l.Next()
		if l.Err() != nil {
			t.Fatalf("lex %q: %v", src, l.Err())
		}
		if tok.Kind == EOF {
			return out
		}
		tok.Pos = 0
		out = append(out, tok)
	}
}

func TestQuoteIdentifiersPreservesTheTokenStream(t *testing.T) {
	for _, src := range []string{
		`SELECT id, Name FROM users WHERE qty > 5`,
		`SELECT u.id, o.total FROM users u JOIN orders o ON u.id = o.user_id`,
		`SELECT "Mixed Case", "has""quote" FROM t -- a comment with bool in it` + "\n" + `WHERE x = 'bool literal'`,
		`SELECT COUNT(*) AS n, UPPER(name) FROM t /* block bool */ GROUP BY name ORDER BY n DESC LIMIT 10`,
		`SELECT tags.0, meta.category FROM docs WHERE body = X'DEADBEEF'`,
		`WITH c AS (SELECT a FROM t) SELECT a FROM c UNION ALL SELECT b FROM u`,
		`SELECT * FROM t SEARCH body FOR 'x' FACET category`,
	} {
		out, err := QuoteIdentifiers(src)
		if err != nil {
			t.Fatalf("%q: %v", src, err)
		}
		a, b := tokens(t, src), tokens(t, out)
		if len(a) != len(b) {
			t.Fatalf("%q -> %q changed token count", src, out)
		}
		for i := range a {
			if a[i] != b[i] {
				t.Fatalf("%q -> %q changed token %d: %+v vs %+v", src, out, i, a[i], b[i])
			}
		}
	}
}

// The reason it exists: a word that is an identifier today keeps parsing as an
// identifier after it becomes a keyword. `bool` became one in log #284.
func TestQuotedIdentifierSurvivesBecomingAKeyword(t *testing.T) {
	out, err := QuoteIdentifiers(`SELECT flag FROM t`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `SELECT "flag" FROM "t"` {
		t.Fatalf("got %q", out)
	}
	for _, tok := range tokens(t, `SELECT "bool" FROM t`) {
		if tok.Kind == KwBool {
			t.Fatal(`a quoted "bool" lexed as the keyword`)
		}
	}
}

func TestQuoteIdentifiersRejectsUnlexableInput(t *testing.T) {
	if _, err := QuoteIdentifiers(`SELECT 'unterminated`); err == nil {
		t.Fatal("unlexable input was accepted")
	}
}

func FuzzQuoteIdentifiers(f *testing.F) {
	for _, s := range []string{`SELECT a FROM t`, `SELECT "x""y" FROM t WHERE b = 'c'`, `select -- c` + "\n" + `1`, `SELECT $1, X'00'`} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		l := New(src)
		for tok := l.Next(); tok.Kind != EOF; tok = l.Next() {
		}
		lexable := l.Err() == nil
		out, err := QuoteIdentifiers(src)
		if err != nil {
			if lexable {
				t.Fatalf("lexable input %q failed to quote: %v", src, err)
			}
			return
		}
		// Idempotent: quoting already-quoted text changes nothing further.
		again, err := QuoteIdentifiers(out)
		if err != nil || again != out {
			t.Fatalf("not idempotent: %q -> %q -> %q (%v)", src, out, again, err)
		}
	})
}
