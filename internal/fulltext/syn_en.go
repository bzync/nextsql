package fulltext

import "sort"

const (
	// MaxSynonymAlts caps extra alternatives emitted for one query token
	// (fail closed). Dictionary v1 groups are smaller; this is an abuse limit.
	MaxSynonymAlts = 8
)

// englishSynonymV1Groups is English synonym dictionary revision 1: tight
// bidirectional search equivalents (not broad hypernyms). Surface forms are
// compiled through Porter2 so inflections share a group. Applied at query
// time by english analyzer v3; index terms stay 1:1 like v2.
var englishSynonymV1Groups = [][]string{
	{"car", "automobile"},
	{"database", "db"},
	{"buy", "purchase"},
	{"big", "large"},
	{"fast", "quick", "rapid"},
	{"movie", "film"},
	{"phone", "telephone"},
	{"child", "kid"},
	{"jump", "leap"},
	{"begin", "start"},
	{"end", "finish"},
	{"error", "mistake"},
	{"help", "assist"},
	{"show", "display"},
	{"smart", "intelligent"},
}

// englishSynonymV1 maps a Porter2 stem to the sorted unique stem group
// (including itself). Built once from englishSynonymV1Groups.
var englishSynonymV1 = compileStemSynonyms(englishSynonymV1Groups, stemEnglish)

func compileStemSynonyms(groups [][]string, stem func(string) string) map[string][]string {
	out := make(map[string][]string, 64)
	for _, g := range groups {
		stems := uniqueSortedStems(g, stem)
		if len(stems) < 2 {
			continue
		}
		merged := stems
		for _, s := range stems {
			if prev := out[s]; len(prev) > 0 {
				merged = mergeSortedUnique(merged, prev)
			}
		}
		for _, s := range merged {
			out[s] = merged
		}
	}
	return out
}

func uniqueSortedStems(words []string, stem func(string) string) []string {
	seen := make(map[string]struct{}, len(words))
	out := make([]string, 0, len(words))
	for _, w := range words {
		s := stem(w)
		if s == "" {
			s = w
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

func mergeSortedUnique(a, b []string) []string {
	out := make([]string, 0, len(a)+len(b))
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		switch {
		case a[i] == b[j]:
			out = append(out, a[i])
			i++
			j++
		case a[i] < b[j]:
			out = append(out, a[i])
			i++
		default:
			out = append(out, b[j])
			j++
		}
	}
	out = append(out, a[i:]...)
	out = append(out, b[j:]...)
	return out
}

func englishExpandsSynonyms(a Analyzer) bool {
	return a.ID == AnalyzerEnglish && a.Version >= AnalyzerEnglishV3
}

// synonymAlts returns extra stems for term (not including term). Empty when
// term is not in the dictionary or the analyzer does not expand synonyms.
func synonymAlts(term string, a Analyzer) []string {
	if !englishExpandsSynonyms(a) {
		return nil
	}
	group := englishSynonymV1[term]
	if len(group) < 2 {
		return nil
	}
	out := make([]string, 0, len(group)-1)
	for _, s := range group {
		if s != term {
			out = append(out, s)
		}
	}
	return out
}
