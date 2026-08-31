package fulltext

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	// DefaultHighlightPre wraps a matching original token in HIGHLIGHT/SNIPPET.
	DefaultHighlightPre = "<mark>"
	// DefaultHighlightPost closes DefaultHighlightPre.
	DefaultHighlightPost = "</mark>"
	// DefaultSnippetRunes is the SNIPPET window when width is omitted.
	DefaultSnippetRunes = 160
	// MinSnippetRunes is the smallest accepted SNIPPET width.
	MinSnippetRunes = 16
	// MaxSnippetRunes is the largest accepted SNIPPET width.
	MaxSnippetRunes = 4096
	// MaxHighlightMarkerRunes bounds each HIGHLIGHT/SNIPPET marker string.
	MaxHighlightMarkerRunes = 32
	// SnippetEllipsis marks a truncated SNIPPET edge.
	SnippetEllipsis  = "…"
	snippetSnapRunes = 16
)

// Span is a byte range in original document text (exclusive End).
type Span struct {
	Start int
	End   int
}

// MatchSpans returns original-text ranges of tokens whose analyzed form
// participates in q. Analyzer transformations (stem/stop/elision) keep the
// source span so "running" is marked for query "runs".
func MatchSpans(text string, q Query, a Analyzer) ([]Span, error) {
	if q.Empty() {
		return nil, nil
	}
	toks, err := tokenize(text, MaxDocTokens, true)
	if err != nil {
		return nil, err
	}
	toks, err = applyAnalyzer(toks, a, false)
	if err != nil {
		return nil, err
	}
	out := make([]Span, 0, 4)
	for _, tok := range toks {
		if tok.Start < 0 || tok.End > len(text) || tok.Start >= tok.End {
			continue
		}
		if !q.HighlightsTerm(tok.Term) {
			continue
		}
		out = append(out, Span{Start: tok.Start, End: tok.End})
	}
	return mergeSpans(out), nil
}

// Highlight wraps every matching original token in text with pre/post.
// Empty pre and post leave the text unchanged aside from validation.
func Highlight(text string, q Query, a Analyzer, pre, post string) (string, error) {
	if err := validateMarkers(pre, post); err != nil {
		return "", err
	}
	spans, err := MatchSpans(text, q, a)
	if err != nil {
		return "", err
	}
	return wrapSpans(text, spans, pre, post), nil
}

// Snippet returns a bounded window of text around the densest cluster of
// matches, wrapped with pre/post. Width is in Unicode code points. A
// truncated edge is marked with SnippetEllipsis.
func Snippet(text string, q Query, a Analyzer, width int, pre, post string) (string, error) {
	if err := validateMarkers(pre, post); err != nil {
		return "", err
	}
	if width < MinSnippetRunes || width > MaxSnippetRunes {
		return "", nerr.New(nerr.InvalidArgument, "fulltext.Snippet", "snippet width out of range")
	}
	spans, err := MatchSpans(text, q, a)
	if err != nil {
		return "", err
	}
	runes := []rune(text)
	if len(runes) <= width {
		return wrapSpans(text, spans, pre, post), nil
	}
	rspans := byteSpansToRunes(text, spans)
	start, end := bestWindow(len(runes), rspans, width)
	start, end = snapWindow(runes, start, end, rspans)
	bStart := runeByteOffset(text, start)
	bEnd := runeByteOffset(text, end)
	fragSpans := clipSpans(spans, bStart, bEnd)
	frag := wrapSpans(text[bStart:bEnd], fragSpans, pre, post)
	if start > 0 {
		frag = SnippetEllipsis + frag
	}
	if end < len(runes) {
		frag += SnippetEllipsis
	}
	return frag, nil
}

func validateMarkers(pre, post string) error {
	if utf8.RuneCountInString(pre) > MaxHighlightMarkerRunes || utf8.RuneCountInString(post) > MaxHighlightMarkerRunes {
		return nerr.New(nerr.InvalidArgument, "fulltext.Highlight", "highlight marker too long")
	}
	if strings.ContainsRune(pre, 0) || strings.ContainsRune(post, 0) {
		return nerr.New(nerr.InvalidArgument, "fulltext.Highlight", "highlight marker contains NUL")
	}
	return nil
}

func mergeSpans(spans []Span) []Span {
	if len(spans) < 2 {
		return spans
	}
	sort.Slice(spans, func(i, j int) bool {
		if spans[i].Start != spans[j].Start {
			return spans[i].Start < spans[j].Start
		}
		return spans[i].End < spans[j].End
	})
	out := spans[:0]
	cur := spans[0]
	for _, sp := range spans[1:] {
		if sp.Start <= cur.End {
			if sp.End > cur.End {
				cur.End = sp.End
			}
			continue
		}
		out = append(out, cur)
		cur = sp
	}
	return append(out, cur)
}

func wrapSpans(text string, spans []Span, pre, post string) string {
	if len(spans) == 0 || (pre == "" && post == "") {
		return text
	}
	var b strings.Builder
	b.Grow(len(text) + len(spans)*(len(pre)+len(post)))
	pos := 0
	for _, sp := range spans {
		if sp.Start < pos {
			if sp.End <= pos {
				continue
			}
			sp.Start = pos
		}
		if sp.Start > len(text) {
			break
		}
		if sp.End > len(text) {
			sp.End = len(text)
		}
		if sp.Start >= sp.End {
			continue
		}
		b.WriteString(text[pos:sp.Start])
		b.WriteString(pre)
		b.WriteString(text[sp.Start:sp.End])
		b.WriteString(post)
		pos = sp.End
	}
	b.WriteString(text[pos:])
	return b.String()
}

type runeSpan struct {
	Start int
	End   int
}

func byteSpansToRunes(text string, spans []Span) []runeSpan {
	out := make([]runeSpan, 0, len(spans))
	for _, sp := range spans {
		if sp.Start < 0 || sp.End > len(text) || sp.Start >= sp.End {
			continue
		}
		out = append(out, runeSpan{
			Start: utf8.RuneCountInString(text[:sp.Start]),
			End:   utf8.RuneCountInString(text[:sp.End]),
		})
	}
	return out
}

func bestWindow(n int, spans []runeSpan, width int) (int, int) {
	if n <= width {
		return 0, n
	}
	cands := []int{0, n - width}
	for _, sp := range spans {
		cands = append(cands,
			clamp(sp.Start-width/4, 0, n-width),
			clamp(sp.Start, 0, n-width),
			clamp(sp.End-width, 0, n-width),
		)
	}
	bestStart, bestCover, bestOverlap := 0, -1, -1
	for _, s := range cands {
		e := s + width
		cover, overlap := 0, 0
		for _, sp := range spans {
			lo := sp.Start
			if lo < s {
				lo = s
			}
			hi := sp.End
			if hi > e {
				hi = e
			}
			if hi > lo {
				cover++
				overlap += hi - lo
			}
		}
		if cover > bestCover || cover == bestCover && (overlap > bestOverlap || overlap == bestOverlap && s < bestStart) {
			bestStart, bestCover, bestOverlap = s, cover, overlap
		}
	}
	return bestStart, bestStart + width
}

func snapWindow(runes []rune, start, end int, spans []runeSpan) (int, int) {
	width := end - start
	if start > 0 {
		lim := start + snippetSnapRunes
		if lim > end {
			lim = end
		}
		for i := start; i < lim; i++ {
			if unicode.IsSpace(runes[i]) {
				next := i + 1
				if windowCovers(next, start+width, spans) {
					start = next
					end = start + width
					if end > len(runes) {
						end = len(runes)
						start = end - width
					}
				}
				break
			}
		}
	}
	if end < len(runes) {
		lim := end - snippetSnapRunes
		if lim < start {
			lim = start
		}
		for i := end - 1; i >= lim; i-- {
			if unicode.IsSpace(runes[i]) {
				if windowCovers(start, i, spans) {
					end = i
				}
				break
			}
		}
	}
	if start < 0 {
		start = 0
	}
	if end > len(runes) {
		end = len(runes)
	}
	if start > end {
		start = end
	}
	return start, end
}

func windowCovers(start, end int, spans []runeSpan) bool {
	for _, sp := range spans {
		lo := sp.Start
		if lo < start {
			lo = start
		}
		hi := sp.End
		if hi > end {
			hi = end
		}
		if hi > lo {
			return true
		}
	}
	return len(spans) == 0
}

func clipSpans(spans []Span, bStart, bEnd int) []Span {
	out := make([]Span, 0, len(spans))
	for _, sp := range spans {
		lo := sp.Start
		if lo < bStart {
			lo = bStart
		}
		hi := sp.End
		if hi > bEnd {
			hi = bEnd
		}
		if hi > lo {
			out = append(out, Span{Start: lo - bStart, End: hi - bStart})
		}
	}
	return out
}

func runeByteOffset(s string, n int) int {
	if n <= 0 {
		return 0
	}
	i := 0
	for count := 0; count < n && i < len(s); count++ {
		_, w := utf8.DecodeRuneInString(s[i:])
		i += w
	}
	return i
}

func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
