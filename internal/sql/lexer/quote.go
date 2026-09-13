package lexer

import (
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// QuoteIdentifiers returns src with every identifier written as a quoted
// identifier, and leaves everything else -- keywords, literals, parameters,
// punctuation, whitespace and comments -- byte for byte as it was.
//
// A quoted and an unquoted identifier lex to the same token: an unquoted one
// is folded to lower case, and the quoted form of that folded name keeps it.
// So the result parses exactly as src does today. What changes is that a word
// which later becomes a reserved keyword still parses as the identifier it
// was. That matters for SQL stored as text and parsed again on every use: a
// view body written with `bool` as a column name stopped parsing the day
// `bool` became a type keyword. The result is checked to lex to the identical
// token sequence before it is returned, so a quoting mistake fails closed
// instead of changing what the text means.
func QuoteIdentifiers(src string) (string, error) {
	l := New(src)
	var b strings.Builder
	b.Grow(len(src) + len(src)/8)
	last := 0
	var want []Token
	for {
		tok := l.Next()
		if l.err != nil {
			return "", l.err
		}
		if tok.Kind == EOF {
			break
		}
		want = append(want, tok)
		if tok.Kind != Ident {
			continue
		}
		// gap is every source byte since the previous identifier written here.
		// Empty after one means the output ends with that identifier's
		// closing quote; otherwise its own last byte decides.
		gap := src[last:tok.Pos]
		b.WriteString(gap)
		if (len(gap) == 0 && last > 0) || (len(gap) > 0 && gap[len(gap)-1] == '"') {
			// Two quoted identifiers with nothing between them would read
			// as one identifier holding an escaped quote.
			b.WriteByte(' ')
		}
		b.WriteByte('"')
		b.WriteString(strings.ReplaceAll(tok.Lit, `"`, `""`))
		b.WriteByte('"')
		last = l.i
	}
	b.WriteString(src[last:])
	out := b.String()

	check := New(out)
	for i := 0; ; i++ {
		tok := check.Next()
		if check.err != nil {
			return "", nerr.Wrap(nerr.Internal, "lexer.QuoteIdentifiers", "quoted text does not lex", check.err)
		}
		if tok.Kind == EOF {
			if i != len(want) {
				return "", nerr.New(nerr.Internal, "lexer.QuoteIdentifiers", "quoted text has a different token count")
			}
			return out, nil
		}
		if i >= len(want) || tok.Kind != want[i].Kind || tok.Lit != want[i].Lit {
			return "", nerr.New(nerr.Internal, "lexer.QuoteIdentifiers", "quoted text lexes differently")
		}
	}
}
