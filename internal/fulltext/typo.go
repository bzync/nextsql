package fulltext

import "unicode/utf8"

// ApplyTypoTolerance rewrites unadorned exact groups whose terms are all
// absent from present into AUTO-distance fuzzy groups. Prefix and explicit
// fuzzy groups are unchanged. Short tokens (AUTO distance 0) stay exact
// misses so 'ab' cannot expand. present reports whether a term occurs in
// the searchable vocabulary (index postings, or the seq-scan corpus).
// A nil present treats every term as absent.
func ApplyTypoTolerance(q Query, present func(string) bool) Query {
	if present == nil {
		present = func(string) bool { return false }
	}
	groups := q.MatchGroups()
	slots := q.phraseSlots()
	prefixes := q.phrasePrefixes()
	fuzzies := q.phraseFuzzies()
	if !typoNeeded(groups, slots, prefixes, fuzzies, present) {
		return q
	}
	seenFz := make(map[string]struct{}, len(q.Fuzzies)+len(groups))
	outFz := append([]FuzzyTerm(nil), q.Fuzzies...)
	for _, f := range outFz {
		if f.Term != "" {
			seenFz[f.Term] = struct{}{}
		}
	}
	newGroups := make([][]string, 0, len(groups))
	var terms []string
	seenTerm := make(map[string]struct{}, len(q.Terms))
	for _, g := range groups {
		if term, dist, ok := typoRewrite(g, present); ok {
			if _, dup := seenFz[term]; !dup {
				seenFz[term] = struct{}{}
				outFz = append(outFz, FuzzyTerm{Term: term, Dist: dist})
			}
			continue
		}
		newGroups = append(newGroups, g)
		for _, t := range g {
			if t == "" {
				continue
			}
			if _, ok := seenTerm[t]; ok {
				continue
			}
			seenTerm[t] = struct{}{}
			terms = append(terms, t)
		}
	}
	out := q
	out.Groups = newGroups
	out.Terms = terms
	out.Fuzzies = outFz
	out.PhraseFuzzy = rewritePhraseTypos(slots, prefixes, fuzzies, present)
	return out
}

func typoNeeded(groups [][]string, slots [][][]string, prefixes [][]string, fuzzies [][]FuzzyTerm, present func(string) bool) bool {
	for _, g := range groups {
		if _, _, ok := typoRewrite(g, present); ok {
			return true
		}
	}
	for i, ph := range slots {
		var pfx []string
		if i < len(prefixes) {
			pfx = prefixes[i]
		}
		var fz []FuzzyTerm
		if i < len(fuzzies) {
			fz = fuzzies[i]
		}
		for j, alts := range ph {
			if slotPrefix(pfx, j) != "" || slotFuzzy(fz, j).Term != "" {
				continue
			}
			if _, _, ok := typoRewrite(alts, present); ok {
				return true
			}
		}
	}
	return false
}

func typoRewrite(alts []string, present func(string) bool) (term string, dist int, ok bool) {
	if len(alts) == 0 || anyPresent(alts, present) {
		return "", 0, false
	}
	term = alts[0]
	if term == "" {
		return "", 0, false
	}
	dist = AutoTypoDistance(utf8.RuneCountInString(term))
	if dist == 0 {
		return "", 0, false
	}
	return term, dist, true
}

// AutoTypoDistance is the max edit distance for automatic typo tolerance
// on an unadorned token. It is stricter than AutoFuzzyDistance so short
// exact queries stay Phase 10 compatible (cats does not match cat).
func AutoTypoDistance(runes int) int {
	switch {
	case runes <= 4:
		return 0
	case runes <= 8:
		return 1
	default:
		return MaxFuzzyDistance
	}
}

func anyPresent(alts []string, present func(string) bool) bool {
	for _, t := range alts {
		if t != "" && present(t) {
			return true
		}
	}
	return false
}

func rewritePhraseTypos(slots [][][]string, prefixes [][]string, fuzzies [][]FuzzyTerm, present func(string) bool) [][]FuzzyTerm {
	if len(slots) == 0 {
		return fuzzies
	}
	out := make([][]FuzzyTerm, len(slots))
	changed := false
	for i, ph := range slots {
		var pfx []string
		if i < len(prefixes) {
			pfx = prefixes[i]
		}
		var src []FuzzyTerm
		if i < len(fuzzies) {
			src = fuzzies[i]
		}
		row := make([]FuzzyTerm, len(ph))
		if len(src) > 0 {
			copy(row, src)
		}
		for j, alts := range ph {
			if slotPrefix(pfx, j) != "" || row[j].Term != "" {
				continue
			}
			term, dist, ok := typoRewrite(alts, present)
			if !ok {
				continue
			}
			row[j] = FuzzyTerm{Term: term, Dist: dist}
			changed = true
		}
		out[i] = row
	}
	if !changed {
		return fuzzies
	}
	return out
}

func slotPrefix(pfx []string, i int) string {
	if i < len(pfx) {
		return pfx[i]
	}
	return ""
}

func slotFuzzy(fz []FuzzyTerm, i int) FuzzyTerm {
	if i < len(fz) {
		return fz[i]
	}
	return FuzzyTerm{}
}
