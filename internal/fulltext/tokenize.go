package fulltext

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	// MaxTermRunes is the longest accepted token after normalization.
	MaxTermRunes = 128
	// MaxDocTokens is the most tokens stored for one document.
	MaxDocTokens = 100_000
	// MaxQueryTokens is the most tokens accepted in a SEARCH query.
	MaxQueryTokens = 64
)

// Token is one normalized term at a document position.
type Token struct {
	Term string
	Pos  uint32
}

// Tokenize splits s into lowercase letter/digit tokens. It never errors:
// oversize documents are truncated. NULs are dropped from terms.
func Tokenize(s string) []Token {
	toks, _ := tokenize(s, MaxDocTokens, false)
	return toks
}

func tokenize(s string, max int, failOver bool) ([]Token, error) {
	if max <= 0 {
		return nil, nerr.New(nerr.InvalidArgument, "fulltext.tokenize", "invalid token limit")
	}
	out := make([]Token, 0, 8)
	var b strings.Builder
	var pos uint32
	flush := func() {
		if b.Len() == 0 {
			return
		}
		term := normalizeTerm(b.String())
		b.Reset()
		if term == "" {
			return
		}
		out = append(out, Token{Term: term, Pos: pos})
		pos++
	}
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			i++
			flush()
			continue
		}
		i += w
		if isTermRune(r) {
			if r == 0 {
				continue
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		flush()
	}
	flush()
	if len(out) > max {
		if failOver {
			return nil, nerr.New(nerr.InvalidArgument, "fulltext.tokenize", "too many tokens")
		}
		out = out[:max]
	}
	return out, nil
}

func isTermRune(r rune) bool {
	if r == '\'' {
		return true
	}
	return unicode.IsLetter(r) || unicode.IsDigit(r)
}

func normalizeTerm(s string) string {
	s = strings.Trim(s, "'")
	if s == "" {
		return ""
	}
	if strings.ContainsRune(s, 0) {
		s = strings.ReplaceAll(s, "\x00", "")
	}
	n := 0
	for range s {
		n++
		if n > MaxTermRunes {
			return ""
		}
	}
	return s
}
