package fulltext

import (
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	// AnalyzerSimple is the Phase 10 tokenizer: Unicode lowercase, no stemmer.
	AnalyzerSimple uint8 = 0
	// AnalyzerEnglish is tokenizer + Snowball English (Porter2) stemmer.
	AnalyzerEnglish uint8 = 1
	// AnalyzerFrench is tokenizer + Snowball French + stop-word dictionary v1.
	AnalyzerFrench uint8 = 2
	// AnalyzerGerman is tokenizer + Snowball German + stop-word dictionary v1.
	AnalyzerGerman uint8 = 3
	// AnalyzerSpanish is tokenizer + Snowball Spanish + stop-word dictionary v1.
	AnalyzerSpanish uint8 = 4

	// AnalyzerEnglishV1 is English stemming without stop-word filtering.
	AnalyzerEnglishV1 uint16 = 1
	// AnalyzerEnglishV2 is English stemming plus stop-word dictionary v1.
	AnalyzerEnglishV2 uint16 = 2
	// AnalyzerEnglishV3 is English stemming plus stop-word dictionary v1
	// plus synonym dictionary v1 (query-time OR expansion).
	// CREATE FULLTEXT INDEX … ANALYZER = 'english' writes this revision.
	AnalyzerEnglishV3 uint16 = 3

	// AnalyzerFrenchV1 is Snowball French plus stop-word dictionary v1.
	AnalyzerFrenchV1 uint16 = 1
	// AnalyzerGermanV1 is Snowball German plus stop-word dictionary v1.
	AnalyzerGermanV1 uint16 = 1
	// AnalyzerSpanishV1 is Snowball Spanish plus stop-word dictionary v1.
	AnalyzerSpanishV1 uint16 = 1

	// MaxQueryExpansions bounds the number of terms a SEARCH string may
	// produce after analyzer expansion (stemming is 1:1; synonym dictionaries
	// emit extra alternatives per token; prefix, fuzzy, and typo-tolerance
	// queries add one term per distinct matching indexed term).
	MaxQueryExpansions = 256
	// MaxQueryExpansionBytes bounds the summed UTF-8 byte length of expanded
	// query terms.
	MaxQueryExpansionBytes = 8192
	// MaxQueryExpandWork bounds analyzer work units on a SEARCH string
	// (one unit per input token plus one per emitted term; dropped stop
	// words still consume one work unit; each synonym alternative consumes
	// one work unit; each distinct prefix-, fuzzy-, or typo-matched term
	// consumes one work unit). Fail closed.
	MaxQueryExpandWork = 4096
)

// Analyzer is a versioned tokenization pipeline. Zero value is simple v1,
// matching catalog rows written before analyzer metadata existed.
type Analyzer struct {
	ID      uint8
	Version uint16
}

// Simple is the default analyzer (no stemming).
var Simple = Analyzer{}

// EnglishV1 is Snowball English stemming without stop-word filtering.
var EnglishV1 = Analyzer{ID: AnalyzerEnglish, Version: AnalyzerEnglishV1}

// EnglishV2 is Snowball English stemming plus stop-word dictionary v1.
var EnglishV2 = Analyzer{ID: AnalyzerEnglish, Version: AnalyzerEnglishV2}

// EnglishV3 is English v2 plus synonym dictionary v1 (query-time expansion).
var EnglishV3 = Analyzer{ID: AnalyzerEnglish, Version: AnalyzerEnglishV3}

// English is the current shipped English analyzer (v3).
var English = EnglishV3

// French is Snowball French plus stop-word dictionary v1.
var French = Analyzer{ID: AnalyzerFrench, Version: AnalyzerFrenchV1}

// German is Snowball German plus stop-word dictionary v1.
var German = Analyzer{ID: AnalyzerGerman, Version: AnalyzerGermanV1}

// Spanish is Snowball Spanish plus stop-word dictionary v1.
var Spanish = Analyzer{ID: AnalyzerSpanish, Version: AnalyzerSpanishV1}

// LookupAnalyzer resolves a DDL analyzer name. Unknown names fail closed.
func LookupAnalyzer(name string) (Analyzer, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "simple":
		return Simple, nil
	case "english":
		return English, nil
	case "french":
		return French, nil
	case "german":
		return German, nil
	case "spanish":
		return Spanish, nil
	default:
		return Analyzer{}, nerr.New(nerr.InvalidArgument, "fulltext.LookupAnalyzer", "unknown full-text analyzer")
	}
}

// Name is the DDL identifier for a, or empty when a is unknown.
func (a Analyzer) Name() string {
	switch a.ID {
	case AnalyzerSimple:
		return "simple"
	case AnalyzerEnglish:
		if a.Version == AnalyzerEnglishV1 || a.Version == AnalyzerEnglishV2 || a.Version == AnalyzerEnglishV3 {
			return "english"
		}
	case AnalyzerFrench:
		if a.Version == AnalyzerFrenchV1 {
			return "french"
		}
	case AnalyzerGerman:
		if a.Version == AnalyzerGermanV1 {
			return "german"
		}
	case AnalyzerSpanish:
		if a.Version == AnalyzerSpanishV1 {
			return "spanish"
		}
	}
	return ""
}

// Validate reports whether a is a shipped analyzer revision.
func (a Analyzer) Validate() error {
	switch a.ID {
	case AnalyzerSimple:
		if a.Version > 1 {
			return nerr.New(nerr.InvalidArgument, "fulltext.Validate", "unknown simple analyzer version")
		}
		return nil
	case AnalyzerEnglish:
		if a.Version != AnalyzerEnglishV1 && a.Version != AnalyzerEnglishV2 && a.Version != AnalyzerEnglishV3 {
			return nerr.New(nerr.InvalidArgument, "fulltext.Validate", "unknown english analyzer version")
		}
		return nil
	case AnalyzerFrench:
		if a.Version != AnalyzerFrenchV1 {
			return nerr.New(nerr.InvalidArgument, "fulltext.Validate", "unknown french analyzer version")
		}
		return nil
	case AnalyzerGerman:
		if a.Version != AnalyzerGermanV1 {
			return nerr.New(nerr.InvalidArgument, "fulltext.Validate", "unknown german analyzer version")
		}
		return nil
	case AnalyzerSpanish:
		if a.Version != AnalyzerSpanishV1 {
			return nerr.New(nerr.InvalidArgument, "fulltext.Validate", "unknown spanish analyzer version")
		}
		return nil
	default:
		return nerr.New(nerr.InvalidArgument, "fulltext.Validate", "unknown full-text analyzer")
	}
}

type expandBudget struct {
	terms int
	bytes int
	work  int
}

func (b *expandBudget) account(term string, work int) error {
	if b == nil {
		return nil
	}
	if work < 1 {
		work = 1
	}
	b.work += work
	b.terms++
	b.bytes += len(term)
	if b.terms > MaxQueryExpansions || b.bytes > MaxQueryExpansionBytes || b.work > MaxQueryExpandWork {
		return nerr.New(nerr.InvalidArgument, "fulltext.expand", "query expansion exceeded limits")
	}
	return nil
}

func (b *expandBudget) addWork(work int) error {
	if b == nil {
		return nil
	}
	if work < 1 {
		work = 1
	}
	b.work += work
	if b.work > MaxQueryExpandWork {
		return nerr.New(nerr.InvalidArgument, "fulltext.expand", "query expansion exceeded limits")
	}
	return nil
}

// applyAnalyzer maps tokens through a. Stop words are dropped and remaining
// terms are re-packed to consecutive positions so BM25 length and phrase
// matching stay aligned between index and query. Query analysis accounts
// expansion against the fail-closed CPU/memory caps; dropped stop words
// still consume work units. Synonym alternatives (english v3) are emitted
// at query time only, at the same position as the original stem.
// Prefix and fuzzy tokens skip stop-word filtering, stemming, and synonym
// expansion (a truncated or misspelled word is not a complete token);
// French elision still applies.
func applyAnalyzer(toks []Token, a Analyzer, query bool) ([]Token, error) {
	if err := a.Validate(); err != nil {
		return nil, err
	}
	if len(toks) == 0 || a.ID == AnalyzerSimple {
		return toks, nil
	}
	var budget *expandBudget
	if query {
		budget = &expandBudget{}
	}
	dropStops := dropsStops(a)
	out := make([]Token, 0, len(toks))
	var pos uint32
	for _, t := range toks {
		term := t.Term
		if a.ID == AnalyzerFrench {
			term = frenchElide(term)
			if term == "" {
				term = t.Term
			}
		}
		if t.Prefix || t.Fuzzy {
			if err := budget.account(term, 1); err != nil {
				return nil, err
			}
			out = append(out, Token{Term: term, Pos: pos, Start: t.Start, End: t.End, Prefix: t.Prefix, Fuzzy: t.Fuzzy, Dist: t.Dist})
			pos++
			continue
		}
		if dropStops && isStop(term, a) {
			if err := budget.addWork(1); err != nil {
				return nil, err
			}
			continue
		}
		stemmed, work := analyzeTerm(term, a)
		if stemmed == "" {
			stemmed = term
		}
		term = stemmed
		if err := budget.account(term, work); err != nil {
			return nil, err
		}
		out = append(out, Token{Term: term, Pos: pos, Start: t.Start, End: t.End})
		if query {
			alts := synonymAlts(term, a)
			if len(alts) > MaxSynonymAlts {
				return nil, nerr.New(nerr.InvalidArgument, "fulltext.expand", "query expansion exceeded limits")
			}
			for _, syn := range alts {
				if err := budget.account(syn, 1); err != nil {
					return nil, err
				}
				out = append(out, Token{Term: syn, Pos: pos, Start: t.Start, End: t.End})
			}
		}
		pos++
	}
	return out, nil
}

func analyzeTerm(term string, a Analyzer) (string, int) {
	switch a.ID {
	case AnalyzerEnglish:
		return stemEnglish(term), 1
	case AnalyzerFrench:
		return stemFrench(term), 1
	case AnalyzerGerman:
		return stemGerman(term), 1
	case AnalyzerSpanish:
		return stemSpanish(term), 1
	default:
		return term, 1
	}
}

func dropsStops(a Analyzer) bool {
	switch a.ID {
	case AnalyzerEnglish:
		return englishDropsStops(a)
	case AnalyzerFrench, AnalyzerGerman, AnalyzerSpanish:
		return a.Version >= 1
	}
	return false
}

func isStop(term string, a Analyzer) bool {
	switch a.ID {
	case AnalyzerEnglish:
		return isEnglishStopV1(term)
	case AnalyzerFrench:
		return isFrenchStopV1(term)
	case AnalyzerGerman:
		return isGermanStopV1(term)
	case AnalyzerSpanish:
		return isSpanishStopV1(term)
	}
	return false
}
