package migrate

import (
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/lexer"
)

// Split cuts src on ';' that are not inside strings, quoted idents, or comments.
// Empty and comment-only fragments are dropped.
func Split(src string) ([]string, error) {
	lx := lexer.New(src)
	var (
		stmts []string
		start int
	)
	for {
		tok := lx.Next()
		if err := lx.Err(); err != nil {
			return nil, nerr.Wrap(nerr.Syntax, "migrate", "split", err)
		}
		if tok.Kind != lexer.Semi && tok.Kind != lexer.EOF {
			continue
		}
		frag := strings.TrimSpace(src[start:tok.Pos])
		if frag != "" && !isBlankSQL(frag) {
			stmts = append(stmts, frag)
		}
		if tok.Kind == lexer.EOF {
			return stmts, nil
		}
		start = tok.Pos + len(tok.Lit)
	}
}

func isBlankSQL(s string) bool {
	lx := lexer.New(s)
	tok := lx.Next()
	return tok.Kind == lexer.EOF && lx.Err() == nil
}
