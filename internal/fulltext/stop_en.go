package fulltext

// englishStopV1 is English stop-word dictionary revision 1: the classic
// Lucene EnglishAnalyzer / Snowball-small set (33 terms). It is compiled in,
// locale-free, and applied by english analyzer v2 before stemming. english v1
// indexes do not filter stop words.
var englishStopV1 = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {},
	"be": {}, "but": {}, "by": {},
	"for": {},
	"if":  {}, "in": {}, "into": {}, "is": {}, "it": {},
	"no": {}, "not": {},
	"of": {}, "on": {}, "or": {},
	"such": {},
	"that": {}, "the": {}, "their": {}, "then": {}, "there": {},
	"these": {}, "they": {}, "this": {}, "to": {},
	"was": {}, "will": {}, "with": {},
}

func isEnglishStopV1(term string) bool {
	_, ok := englishStopV1[term]
	return ok
}

func englishDropsStops(a Analyzer) bool {
	return a.ID == AnalyzerEnglish && a.Version >= AnalyzerEnglishV2
}
