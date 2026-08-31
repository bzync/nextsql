package fulltext

import (
	"sort"
	"strings"
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
// Unquoted tokens are required terms (AND). Synonym alternatives for one
// input token are a disjunction at that position (Groups). A trailing ASCII
// '*' on a token is a prefix group (Prefixes): any indexed term with that
// prefix satisfies the slot (OR), still AND with other groups. A trailing
// ASCII '~' is a fuzzy group (Fuzzies): any indexed term within Dist OSA
// edits satisfies the slot. ApplyTypoTolerance may rewrite unadorned
// groups whose terms are all absent from the vocabulary into AUTO fuzzy
// groups. Double-quoted groups are phrases that must appear at consecutive
// positions; PhraseAlts holds per-slot alternatives when a phrase token
// expanded; PhrasePrefix holds a prefix constraint for a phrase slot
// (empty = exact); PhraseFuzzy holds a fuzzy constraint for a phrase slot
// (empty Term = not fuzzy).
type Query struct {
	Terms        []string
	Groups       [][]string
	Prefixes     []string
	Fuzzies      []FuzzyTerm
	Phrases      [][]string
	PhraseAlts   [][][]string
	PhrasePrefix [][]string
	PhraseFuzzy  [][]FuzzyTerm
}

// Analyze tokenizes document text with the simple analyzer.
func Analyze(text string) (Doc, error) {
	return AnalyzeWith(text, Simple)
}

// AnalyzeWith tokenizes document text with a and groups positions by term.
func AnalyzeWith(text string, a Analyzer) (Doc, error) {
	toks, err := tokenize(text, MaxDocTokens, true)
	if err != nil {
		return Doc{}, err
	}
	toks, err = applyAnalyzer(toks, a, false)
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

// AnalyzeFields tokenizes one or more fields as a single BM25 document.
// Positions of field i start at i*FieldPositionGap so consecutive-position
// phrases cannot match across a field boundary. Combined token count is
// fail-closed at MaxDocTokens. More than MaxFields texts fail closed.
func AnalyzeFields(texts []string, a Analyzer) (Doc, error) {
	if len(texts) == 0 {
		return Doc{}, nil
	}
	if len(texts) > MaxFields {
		return Doc{}, nerr.New(nerr.InvalidArgument, "fulltext.AnalyzeFields", "too many full-text fields")
	}
	if len(texts) == 1 {
		return AnalyzeWith(texts[0], a)
	}
	idx := make(map[string]int)
	var terms []TermPosting
	var total uint32
	for i, text := range texts {
		doc, err := AnalyzeWith(text, a)
		if err != nil {
			return Doc{}, err
		}
		if uint64(total)+uint64(doc.Len) > uint64(MaxDocTokens) {
			return Doc{}, nerr.New(nerr.InvalidArgument, "fulltext.AnalyzeFields", "document too long")
		}
		total += doc.Len
		base := uint32(i) * FieldPositionGap
		for _, t := range doc.Terms {
			pos := make([]uint32, len(t.Pos))
			for j, p := range t.Pos {
				pos[j] = p + base
			}
			k, ok := idx[t.Term]
			if !ok {
				idx[t.Term] = len(terms)
				terms = append(terms, TermPosting{Term: t.Term, Pos: pos})
				continue
			}
			terms[k].Pos = append(terms[k].Pos, pos...)
		}
	}
	if total == 0 {
		return Doc{}, nil
	}
	for i := range terms {
		terms[i].TF = uint32(len(terms[i].Pos))
	}
	sort.Slice(terms, func(i, j int) bool { return terms[i].Term < terms[j].Term })
	return Doc{Terms: terms, Len: total}, nil
}

// ParseQuery tokenizes a SEARCH string with the simple analyzer.
func ParseQuery(s string) (Query, error) {
	return ParseQueryWith(s, Simple)
}

// ParseQueryWith tokenizes a SEARCH string with a. Quoted spans become phrases.
func ParseQueryWith(s string, a Analyzer) (Query, error) {
	if utf8.RuneCountInString(s) > 1<<20 {
		return Query{}, nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "query too long")
	}
	if err := a.Validate(); err != nil {
		return Query{}, err
	}
	var q Query
	seen := make(map[string]struct{})
	seenGroup := make(map[string]struct{})
	seenPrefix := make(map[string]struct{})
	seenFuzzy := make(map[string]struct{})
	add := func(part string, phrase bool) error {
		toks, err := tokenizeQuery(part, MaxQueryTokens, true)
		if err != nil {
			return err
		}
		toks, err = applyAnalyzer(toks, a, true)
		if err != nil {
			return err
		}
		var phraseToks []string
		var phraseSlots [][]string
		var phrasePfx []string
		var phraseFz []FuzzyTerm
		i := 0
		for i < len(toks) {
			j := i + 1
			for j < len(toks) && toks[j].Pos == toks[i].Pos {
				j++
			}
			if toks[i].Prefix {
				pfx := toks[i].Term
				if err := addQueryPrefix(&q, pfx, seenPrefix); err != nil {
					return err
				}
				if phrase && pfx != "" {
					phraseToks = append(phraseToks, pfx)
					phraseSlots = append(phraseSlots, nil)
					phrasePfx = append(phrasePfx, pfx)
					phraseFz = append(phraseFz, FuzzyTerm{})
				}
				i = j
				continue
			}
			if toks[i].Fuzzy {
				term := toks[i].Term
				dist := resolveFuzzyDist(term, toks[i].Dist)
				if err := addQueryFuzzy(&q, term, dist, seenFuzzy); err != nil {
					return err
				}
				if phrase && term != "" {
					phraseToks = append(phraseToks, term)
					phraseSlots = append(phraseSlots, nil)
					phrasePfx = append(phrasePfx, "")
					phraseFz = append(phraseFz, FuzzyTerm{Term: term, Dist: dist})
				}
				i = j
				continue
			}
			alts := uniqueTokenTerms(toks[i:j])
			if err := addQueryGroup(&q, alts, seen, seenGroup); err != nil {
				return err
			}
			if phrase && len(alts) > 0 {
				phraseToks = append(phraseToks, alts[0])
				phraseSlots = append(phraseSlots, alts)
				phrasePfx = append(phrasePfx, "")
				phraseFz = append(phraseFz, FuzzyTerm{})
			}
			i = j
		}
		if phrase && len(phraseToks) > 1 {
			q.Phrases = append(q.Phrases, phraseToks)
			q.PhraseAlts = append(q.PhraseAlts, phraseSlots)
			q.PhrasePrefix = append(q.PhrasePrefix, phrasePfx)
			q.PhraseFuzzy = append(q.PhraseFuzzy, phraseFz)
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

func uniqueTokenTerms(toks []Token) []string {
	seen := make(map[string]struct{}, len(toks))
	out := make([]string, 0, len(toks))
	for _, t := range toks {
		if t.Term == "" {
			continue
		}
		if _, ok := seen[t.Term]; ok {
			continue
		}
		seen[t.Term] = struct{}{}
		out = append(out, t.Term)
	}
	return out
}

func addQueryPrefix(q *Query, pfx string, seen map[string]struct{}) error {
	if pfx == "" {
		return nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "empty prefix")
	}
	if _, ok := seen[pfx]; ok {
		return nil
	}
	seen[pfx] = struct{}{}
	q.Prefixes = append(q.Prefixes, pfx)
	return nil
}

func addQueryFuzzy(q *Query, term string, dist int, seen map[string]struct{}) error {
	if term == "" {
		return nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "empty fuzzy")
	}
	if dist < 0 || dist > MaxFuzzyDistance {
		return nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "fuzzy distance out of range")
	}
	if _, ok := seen[term]; ok {
		return nil
	}
	seen[term] = struct{}{}
	q.Fuzzies = append(q.Fuzzies, FuzzyTerm{Term: term, Dist: dist})
	return nil
}

func addQueryGroup(q *Query, alts []string, seen, seenGroup map[string]struct{}) error {
	if len(alts) == 0 {
		return nil
	}
	for _, t := range alts {
		if _, ok := seen[t]; ok {
			continue
		}
		if len(q.Terms) >= MaxQueryExpansions {
			return nerr.New(nerr.InvalidArgument, "fulltext.ParseQuery", "query expansion exceeded limits")
		}
		seen[t] = struct{}{}
		q.Terms = append(q.Terms, t)
	}
	key := groupKey(alts)
	if _, ok := seenGroup[key]; ok {
		return nil
	}
	seenGroup[key] = struct{}{}
	q.Groups = append(q.Groups, alts)
	return nil
}

func groupKey(alts []string) string {
	cp := append([]string(nil), alts...)
	sort.Strings(cp)
	return strings.Join(cp, "\x1f")
}

// MatchGroups is the AND-of-OR conjunction for q. Empty Groups falls back
// to one singleton group per unique term (legacy Query values).
func (q Query) MatchGroups() [][]string {
	if len(q.Groups) > 0 {
		return q.Groups
	}
	groups := make([][]string, len(q.Terms))
	for i, t := range q.Terms {
		groups[i] = []string{t}
	}
	return groups
}

func (q Query) phraseSlots() [][][]string {
	if len(q.PhraseAlts) == len(q.Phrases) && len(q.Phrases) > 0 {
		return q.PhraseAlts
	}
	out := make([][][]string, len(q.Phrases))
	for i, ph := range q.Phrases {
		slots := make([][]string, len(ph))
		for j, t := range ph {
			slots[j] = []string{t}
		}
		out[i] = slots
	}
	return out
}

func (q Query) phrasePrefixes() [][]string {
	if len(q.PhrasePrefix) == len(q.Phrases) {
		return q.PhrasePrefix
	}
	return make([][]string, len(q.Phrases))
}

func (q Query) phraseFuzzies() [][]FuzzyTerm {
	if len(q.PhraseFuzzy) == len(q.Phrases) {
		return q.PhraseFuzzy
	}
	return make([][]FuzzyTerm, len(q.Phrases))
}

// Empty reports a SEARCH with no required terms, prefixes, or fuzzy groups.
func (q Query) Empty() bool {
	return len(q.Terms) == 0 && len(q.Prefixes) == 0 && len(q.Fuzzies) == 0
}

// PrefixMatchesTerm reports whether term satisfies any of q's prefix groups.
func (q Query) PrefixMatchesTerm(term string) bool {
	for _, pfx := range q.Prefixes {
		if strings.HasPrefix(term, pfx) {
			return true
		}
	}
	return false
}

// FuzzyMatchesTerm reports whether term satisfies any of q's fuzzy groups.
func (q Query) FuzzyMatchesTerm(term string) bool {
	for _, f := range q.Fuzzies {
		if FuzzyWithin(term, f.Term, f.Dist) {
			return true
		}
	}
	return false
}

// HighlightsTerm reports whether an analyzed document term should be
// marked by HIGHLIGHT/SNIPPET. Exact/synonym groups, prefix groups,
// unquoted fuzzy/typo groups, and phrase-slot prefix/fuzzy/exact
// alternatives all count.
func (q Query) HighlightsTerm(term string) bool {
	if term == "" {
		return false
	}
	for _, g := range q.MatchGroups() {
		for _, t := range g {
			if t == term {
				return true
			}
		}
	}
	if q.PrefixMatchesTerm(term) || q.FuzzyMatchesTerm(term) {
		return true
	}
	prefixes := q.phrasePrefixes()
	fuzzies := q.phraseFuzzies()
	for i, ph := range q.phraseSlots() {
		for j, alts := range ph {
			if pfx := slotPrefix(prefixes[i], j); pfx != "" && strings.HasPrefix(term, pfx) {
				return true
			}
			if fz := slotFuzzy(fuzzies[i], j); fz.Term != "" && FuzzyWithin(term, fz.Term, fz.Dist) {
				return true
			}
			for _, t := range alts {
				if t == term {
					return true
				}
			}
		}
	}
	return false
}

// QueryMatches reports whether a document satisfies q's required groups and
// phrases. tf/pos are the document's term frequencies and positions.
func QueryMatches(q Query, tf map[string]uint32, pos map[string][]uint32) bool {
	groups := q.MatchGroups()
	if len(groups) == 0 && len(q.Prefixes) == 0 && len(q.Fuzzies) == 0 {
		return false
	}
	for _, g := range groups {
		ok := false
		for _, t := range g {
			if tf[t] > 0 {
				ok = true
				break
			}
		}
		if !ok {
			return false
		}
	}
	for _, pfx := range q.Prefixes {
		if !hasPrefixTerm(tf, pfx) {
			return false
		}
	}
	for _, f := range q.Fuzzies {
		if !hasFuzzyTerm(tf, f) {
			return false
		}
	}
	slots := q.phraseSlots()
	prefixes := q.phrasePrefixes()
	fuzzies := q.phraseFuzzies()
	for i, ph := range slots {
		var pfx []string
		if i < len(prefixes) {
			pfx = prefixes[i]
		}
		var fz []FuzzyTerm
		if i < len(fuzzies) {
			fz = fuzzies[i]
		}
		if !PhraseMatchSlots(ph, pfx, fz, pos) {
			return false
		}
	}
	return true
}

func hasPrefixTerm(tf map[string]uint32, pfx string) bool {
	if pfx == "" {
		return false
	}
	for t, freq := range tf {
		if freq > 0 && strings.HasPrefix(t, pfx) {
			return true
		}
	}
	return false
}

func hasFuzzyTerm(tf map[string]uint32, f FuzzyTerm) bool {
	if f.Term == "" {
		return false
	}
	for t, freq := range tf {
		if freq > 0 && FuzzyWithin(t, f.Term, f.Dist) {
			return true
		}
	}
	return false
}

// UniformWeights reports whether every weight is the default 1 (including
// a nil or empty slice). Unweighted SEARCH keeps Phase 10 BM25.
func UniformWeights(weights []float64) bool {
	for _, w := range weights {
		if w != 1 {
			return false
		}
	}
	return true
}

// FieldOf is the SEARCH/FULLTEXT field index of a stored position.
func FieldOf(pos uint32) int {
	return int(pos / FieldPositionGap)
}

// WeightedTF is the BM25 term frequency after per-field SEARCH weights.
// Position band i uses weights[i]; a missing or default-1 weight list is
// the unweighted occurrence count. Positions past the last weight keep
// weight 1 so a truncated list cannot drop postings.
func WeightedTF(pos []uint32, weights []float64) float64 {
	if len(pos) == 0 {
		return 0
	}
	if UniformWeights(weights) {
		return float64(len(pos))
	}
	var tf float64
	for _, p := range pos {
		w := 1.0
		if f := FieldOf(p); f >= 0 && f < len(weights) {
			w = weights[f]
		}
		tf += w
	}
	return tf
}

func termTF(term string, tf map[string]uint32, pos map[string][]uint32, weights []float64) float64 {
	if UniformWeights(weights) || pos == nil {
		return float64(tf[term])
	}
	if ps, ok := pos[term]; ok {
		return WeightedTF(ps, weights)
	}
	return float64(tf[term])
}

// QueryScore is the BM25 score of a matching document. Each AND-group
// contributes the best alternative's term score so synonym expansion does
// not double-count one concept. Prefix and fuzzy groups score the best
// matching term.
func QueryScore(q Query, tf map[string]uint32, df map[string]uint64, dl uint32, avg float64, nDocs uint64) float64 {
	return QueryScoreWeighted(q, tf, nil, nil, df, dl, avg, nDocs)
}

// QueryScoreWeighted is QueryScore with optional per-field weights. A nil
// or all-1 weight list is identical to QueryScore. Weights scale each
// occurrence's contribution to tf (title WEIGHT 3 counts as three
// occurrences) so unweighted multi-field BM25 stays compatible.
func QueryScoreWeighted(q Query, tf map[string]uint32, pos map[string][]uint32, weights []float64, df map[string]uint64, dl uint32, avg float64, nDocs uint64) float64 {
	var score float64
	for _, g := range q.MatchGroups() {
		var best float64
		for _, t := range g {
			if tf[t] == 0 {
				continue
			}
			s := ScoreTF(termTF(t, tf, pos, weights), dl, avg, IDF(nDocs, df[t]))
			if s > best {
				best = s
			}
		}
		score += best
	}
	for _, pfx := range q.Prefixes {
		var best float64
		for t, freq := range tf {
			if freq == 0 || !strings.HasPrefix(t, pfx) {
				continue
			}
			s := ScoreTF(termTF(t, tf, pos, weights), dl, avg, IDF(nDocs, df[t]))
			if s > best {
				best = s
			}
		}
		score += best
	}
	for _, f := range q.Fuzzies {
		var best float64
		for t, freq := range tf {
			if freq == 0 || !FuzzyWithin(t, f.Term, f.Dist) {
				continue
			}
			s := ScoreTF(termTF(t, tf, pos, weights), dl, avg, IDF(nDocs, df[t]))
			if s > best {
				best = s
			}
		}
		score += best
	}
	return score
}

// PhraseMatch reports whether phrase occurs at consecutive positions.
func PhraseMatch(phrase []string, pos map[string][]uint32) bool {
	if len(phrase) == 0 {
		return true
	}
	slots := make([][]string, len(phrase))
	for i, t := range phrase {
		slots[i] = []string{t}
	}
	return PhraseMatchAny(slots, pos)
}

// PhraseMatchAny reports whether a phrase of OR-groups occurs at consecutive
// positions (any alternative at slot i may occupy start+i).
func PhraseMatchAny(phrase [][]string, pos map[string][]uint32) bool {
	return PhraseMatchSlots(phrase, nil, nil, pos)
}

// PhraseMatchSlots is PhraseMatchAny with an optional prefix or fuzzy
// constraint per slot. A non-empty prefix at slot i matches any term with
// that prefix at position start+i; a non-empty fuzzy constraint matches
// any term within Dist OSA edits, in addition to the exact alternatives.
func PhraseMatchSlots(phrase [][]string, prefixes []string, fuzzies []FuzzyTerm, pos map[string][]uint32) bool {
	n := len(phrase)
	if n == 0 {
		return true
	}
	slotHit := func(i int, want uint32) bool {
		if i < len(phrase) {
			for _, t := range phrase[i] {
				if hasPos(pos[t], want) {
					return true
				}
			}
		}
		var pfx string
		if i < len(prefixes) {
			pfx = prefixes[i]
		}
		if pfx != "" {
			for t, ps := range pos {
				if strings.HasPrefix(t, pfx) && hasPos(ps, want) {
					return true
				}
			}
		}
		var fz FuzzyTerm
		if i < len(fuzzies) {
			fz = fuzzies[i]
		}
		if fz.Term != "" {
			for t, ps := range pos {
				if FuzzyWithin(t, fz.Term, fz.Dist) && hasPos(ps, want) {
					return true
				}
			}
		}
		return false
	}
	starts := make([]uint32, 0, 4)
	seen := make(map[uint32]struct{})
	collect := func(ps []uint32) {
		for _, p := range ps {
			if _, ok := seen[p]; ok {
				continue
			}
			seen[p] = struct{}{}
			starts = append(starts, p)
		}
	}
	if n > 0 {
		for _, t := range phrase[0] {
			collect(pos[t])
		}
	}
	var pfx0 string
	if len(prefixes) > 0 {
		pfx0 = prefixes[0]
	}
	if pfx0 != "" {
		for t, ps := range pos {
			if strings.HasPrefix(t, pfx0) {
				collect(ps)
			}
		}
	}
	var fz0 FuzzyTerm
	if len(fuzzies) > 0 {
		fz0 = fuzzies[0]
	}
	if fz0.Term != "" {
		for t, ps := range pos {
			if FuzzyWithin(t, fz0.Term, fz0.Dist) {
				collect(ps)
			}
		}
	}
	if len(starts) == 0 {
		return false
	}
	if n == 1 {
		return true
	}
	for _, start := range starts {
		ok := true
		for i := 1; i < n; i++ {
			if !slotHit(i, start+uint32(i)) {
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

// PrefixExpander bounds distinct prefix-, fuzzy-, or typo-matched terms
// against the fail-closed query-expansion caps. Existing exact query terms
// are pre-seeded so a prefix, fuzzy, or typo group that overlaps them does
// not double-count.
type PrefixExpander struct {
	seen map[string]struct{}
	b    expandBudget
}

// NewPrefixExpander starts a prefix expansion budget from the exact
// terms already in a parsed Query.
func NewPrefixExpander(terms []string) *PrefixExpander {
	e := &PrefixExpander{seen: make(map[string]struct{}, len(terms))}
	for _, t := range terms {
		if t == "" {
			continue
		}
		if _, ok := e.seen[t]; ok {
			continue
		}
		e.seen[t] = struct{}{}
		e.b.terms++
		e.b.bytes += len(t)
	}
	return e
}

// Observe records a distinct matching term. Duplicate terms are free.
// A new term that would exceed the expansion caps fails closed.
func (e *PrefixExpander) Observe(term string) error {
	if e == nil || term == "" {
		return nil
	}
	if _, ok := e.seen[term]; ok {
		return nil
	}
	if err := e.b.account(term, 1); err != nil {
		return err
	}
	e.seen[term] = struct{}{}
	return nil
}
