package fulltext

import (
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	// MaxFuzzyDistance is the largest Levenshtein/OSA distance a SEARCH
	// fuzzy group may request (~2). AUTO never exceeds this.
	MaxFuzzyDistance = 2
	// MaxFuzzyVocabularyTerms bounds the number of distinct indexed or
	// sequential-scan vocabulary terms that one fuzzy or typo-tolerant SEARCH
	// may inspect. Matching terms remain subject to the tighter query-expansion
	// term and byte caps. The separate scan cap prevents a missing token from
	// turning an arbitrarily large vocabulary into unbounded edit-distance work.
	MaxFuzzyVocabularyTerms = MaxQueryExpandWork
)

// FuzzyVocabularyBudget accounts distinct terms inspected by fuzzy or typo
// matching. Duplicate terms (for example, the same term in several partition
// indexes) are charged once.
type FuzzyVocabularyBudget struct {
	seen map[string]struct{}
}

// NewFuzzyVocabularyBudget returns a fail-closed fuzzy vocabulary budget.
func NewFuzzyVocabularyBudget() *FuzzyVocabularyBudget {
	return &FuzzyVocabularyBudget{seen: make(map[string]struct{})}
}

// Observe records one distinct vocabulary term before edit-distance work.
func (b *FuzzyVocabularyBudget) Observe(term string) error {
	if b == nil || term == "" {
		return nil
	}
	if _, ok := b.seen[term]; ok {
		return nil
	}
	if len(b.seen) >= MaxFuzzyVocabularyTerms {
		return nerr.New(nerr.InvalidArgument, "fulltext.fuzzy", "fuzzy vocabulary scan exceeded limit")
	}
	b.seen[term] = struct{}{}
	return nil
}

// FuzzyTerm is one query-time fuzzy group: any indexed term within Dist
// Damerau-Levenshtein (optimal string alignment) edits of Term satisfies
// the slot.
type FuzzyTerm struct {
	Term string
	Dist int
}

// AutoFuzzyDistance is the default max edit distance for a trailing '~'
// with no explicit 1 or 2. Short tokens stay exact so adversarial queries
// such as 'a~' cannot expand to the whole dictionary.
func AutoFuzzyDistance(runes int) int {
	switch {
	case runes <= 2:
		return 0
	case runes <= 5:
		return 1
	default:
		return MaxFuzzyDistance
	}
}

func resolveFuzzyDist(term string, explicit int) int {
	if explicit != 0 {
		if explicit > MaxFuzzyDistance {
			return MaxFuzzyDistance
		}
		if explicit < 0 {
			return 0
		}
		return explicit
	}
	return AutoFuzzyDistance(utf8.RuneCountInString(term))
}

// FuzzyWithin reports whether the OSA Damerau-Levenshtein distance between
// a and b (Unicode runes) is at most max. Distances above MaxFuzzyDistance
// are clamped. Equal strings always match.
func FuzzyWithin(a, b string, max int) bool {
	if max < 0 {
		return false
	}
	if a == b {
		return true
	}
	if max == 0 {
		return false
	}
	if max > MaxFuzzyDistance {
		max = MaxFuzzyDistance
	}
	ar := []rune(a)
	br := []rune(b)
	na, nb := len(ar), len(br)
	if na > nb {
		ar, br = br, ar
		na, nb = nb, na
	}
	if nb-na > max {
		return false
	}
	// OSA needs the previous two rows for adjacent transpositions. Keeping
	// three bounded rows avoids allocating a full MaxTermRunes² matrix for
	// every vocabulary term considered by a fuzzy query.
	prevPrev := make([]int, nb+1)
	prev := make([]int, nb+1)
	cur := make([]int, nb+1)
	for j := 0; j <= nb; j++ {
		prev[j] = j
	}
	for i := 1; i <= na; i++ {
		cur[0] = i
		rowMin := max + 1
		for j := 1; j <= nb; j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			del := prev[j] + 1
			ins := cur[j-1] + 1
			sub := prev[j-1] + cost
			v := del
			if ins < v {
				v = ins
			}
			if sub < v {
				v = sub
			}
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				tr := prevPrev[j-2] + 1
				if tr < v {
					v = tr
				}
			}
			cur[j] = v
			if v < rowMin {
				rowMin = v
			}
		}
		if rowMin > max {
			return false
		}
		prevPrev, prev, cur = prev, cur, prevPrev
	}
	return prev[nb] <= max
}
