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
	// MaxFields is the most STRING/TEXT columns in one FULLTEXT index or SEARCH,
	// and the most columns in one FACET list.
	MaxFields = 8
	// MaxFacetValues is the most distinct values tracked for one FACET column.
	// A column that exceeds this fails closed.
	MaxFacetValues = 1024
	// MaxFieldWeight is the largest SEARCH field weight. Zero, negative,
	// non-finite, and larger values fail closed. Omitted weights are 1.
	MaxFieldWeight = 64
	// FieldPositionGap separates token positions of consecutive FULLTEXT
	// fields so a phrase cannot match across a field boundary. Each field
	// occupies a reserved band even when it is NULL or empty.
	FieldPositionGap = MaxDocTokens + 2
)

// Token is one normalized term at a document position.
// Prefix is query-only: a trailing ASCII '*' on a SEARCH token
// (cat* → Term "cat", Prefix true). Fuzzy is query-only: a trailing
// ASCII '~' (cat~ → Term "cat", Fuzzy true) with optional explicit
// distance 1 or 2 (cat~1). Dist 0 means AUTO. Document analysis never
// sets Prefix, Fuzzy, or Dist.
// Start and End are byte offsets into the original string of the source
// span that produced Term (exclusive End). They are used by highlight
// and snippet generation to wrap the original text, not the folded term.
type Token struct {
	Term   string
	Pos    uint32
	Start  int
	End    int
	Prefix bool
	Fuzzy  bool
	Dist   int
}

// Tokenize splits s into lowercase letter/digit tokens. It never errors:
// oversize documents are truncated. NULs are dropped from terms.
func Tokenize(s string) []Token {
	toks, _ := tokenize(s, MaxDocTokens, false)
	return toks
}

func tokenize(s string, max int, failOver bool) ([]Token, error) {
	return tokenizeMode(s, max, failOver, false)
}

func tokenizeQuery(s string, max int, failOver bool) ([]Token, error) {
	return tokenizeMode(s, max, failOver, true)
}

func tokenizeMode(s string, max int, failOver, query bool) ([]Token, error) {
	if max <= 0 {
		return nil, nerr.New(nerr.InvalidArgument, "fulltext.tokenize", "invalid token limit")
	}
	out := make([]Token, 0, 8)
	var b strings.Builder
	var pos uint32
	var tokStart int
	flush := func(end int) {
		if b.Len() == 0 {
			return
		}
		term := normalizeTerm(b.String())
		b.Reset()
		if term == "" {
			return
		}
		if end < tokStart {
			end = tokStart
		}
		out = append(out, Token{Term: term, Pos: pos, Start: tokStart, End: end})
		pos++
	}
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && w == 1 {
			flush(i)
			i++
			continue
		}
		i += w
		if isTermRune(r) {
			if r == 0 {
				continue
			}
			if b.Len() == 0 {
				tokStart = i - w
			}
			b.WriteRune(unicode.ToLower(r))
			continue
		}
		emitted := len(out)
		flush(i - w)
		if !query || (r != '*' && r != '~') {
			continue
		}
		if len(out) == 0 {
			continue
		}
		tok := &out[len(out)-1]
		if len(out) == emitted {
			if tok.Prefix || tok.Fuzzy {
				return nil, nerr.New(nerr.InvalidArgument, "fulltext.tokenize", "multiple prefix or fuzzy operators")
			}
			continue
		}
		if tok.Prefix || tok.Fuzzy {
			return nil, nerr.New(nerr.InvalidArgument, "fulltext.tokenize", "multiple prefix or fuzzy operators")
		}
		if r == '*' {
			tok.Prefix = true
			continue
		}
		tok.Fuzzy = true
		if i >= len(s) {
			continue
		}
		next, nw := utf8.DecodeRuneInString(s[i:])
		if next < '0' || next > '9' {
			continue
		}
		rest := i + nw
		if rest < len(s) {
			n2, _ := utf8.DecodeRuneInString(s[rest:])
			if isTermRune(n2) {
				continue
			}
		}
		if next != '1' && next != '2' {
			return nil, nerr.New(nerr.InvalidArgument, "fulltext.tokenize", "fuzzy distance must be 1 or 2")
		}
		tok.Dist = int(next - '0')
		i += nw
	}
	flush(len(s))
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
