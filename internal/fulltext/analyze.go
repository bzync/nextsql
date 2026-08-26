package fulltext

import (
	"sort"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/nerr"
)

// TermPosting is one term's frequency and positions inside a document.
type TermPosting struct {
	Term string
	TF   uint32
	Pos  []uint32
}

// Doc is the inverted-index view of one text value.
type Doc struct {
	Terms []TermPosting
	Len   uint32
}

// Query is a SEARCH string after tokenization.
// Unquoted tokens are required terms (AND). Double-quoted groups are phrases
// that must appear at consecutive positions.
type Query struct {
	Terms   []string
	Phrases [][]string
}

// Analyze tokenizes document text and groups positions by term.
func Analyze(text string) (Doc, error) {
	toks, err := tokenize(text, MaxDocTokens, true)
	if err != nil {
		return Doc{}, err
	}
	if len(toks) == 0 {
		return Doc{}, nil
	}
	idx := make(map[string]int, len(toks))
	var terms []TermPosting
	for _, t := range toks {
		i, ok := idx[t.Term]
		if !ok {
			idx[t.Term] = len(terms)
			terms = append(terms, TermPosting{Term: t.Term, Pos: []uint32{t.Pos}})
			continue
		}
		terms[i].Pos = append(terms[i].Pos, t.Pos)
	}
	for i := range terms {
		terms[i].TF = uint32(len(terms[i].Pos))
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Term < terms[j].Term })
	return Doc{Terms: terms, Len: uint32(len(toks))}, nil
}

// ParseQuery tokenizes a SEARCH string. Quoted spans become phrases.
func ParseQuery(s string) (Query, error) {
	if utf8.RuneCountInString(s) > 1<<20 {
		return Query{}, nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "query too long")
	}
	var q Query
	seen := make(map[string]struct{})
	add := func(part string, phrase bool) error {
		toks, err := tokenize(part, MaxQueryTokens, true)
		if err != nil {
			return err
		}
		var phraseToks []string
		for _, t := range toks {
			if _, ok := seen[t.Term]; !ok {
				if len(q.Terms) >= MaxQueryTokens {
					return nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "too many query tokens")
				}
				seen[t.Term] = struct{}{}
				q.Terms = append(q.Terms, t.Term)
			}
			if phrase {
				phraseToks = append(phraseToks, t.Term)
			}
		}
		if phrase && len(phraseToks) > 1 {
			q.Phrases = append(q.Phrases, phraseToks)
		}
		return nil
	}
	inQuote := false
	start := 0
	for i := 0; i < len(s); {
		r, w := utf8.DecodeRuneInString(s[i:])
		if r == '"' {
			if err := add(s[start:i], inQuote); err != nil {
				return Query{}, err
			}
			inQuote = !inQuote
			i += w
			start = i
			continue
		}
		i += w
	}
	if err := add(s[start:], inQuote); err != nil {
		return Query{}, err
	}
	return q, nil
}

// PhraseMatch reports whether phrase occurs at consecutive positions.
func PhraseMatch(phrase []string, pos map[string][]uint32) bool {
	if len(phrase) == 0 {
		return true
	}
	if len(phrase) == 1 {
		return len(pos[phrase[0]]) > 0
	}
	first := pos[phrase[0]]
	if len(first) == 0 {
		return false
	}
	for _, start := range first {
		ok := true
		for i := 1; i < len(phrase); i++ {
			if !hasPos(pos[phrase[i]], start+uint32(i)) {
				ok = false
				break
			}
		}
		if ok {
			return true
		}
	}
	return false
}

func hasPos(xs []uint32, want uint32) bool {
	for _, x := range xs {
		if x == want {
			return true
		}
	}
	return false
}
