package fulltext

import "testing"

func presentSet(terms ...string) func(string) bool {
	m := make(map[string]struct{}, len(terms))
	for _, t := range terms {
		m[t] = struct{}{}
	}
	return func(term string) bool {
		_, ok := m[term]
		return ok
	}
}

func TestApplyTypoToleranceMissing(t *testing.T) {
	q, err := ParseQuery("databse")
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, presentSet("database", "performance"))
	if len(got.Terms) != 0 || len(got.Groups) != 0 {
		t.Fatalf("missing term must leave exact groups: terms %v groups %+v", got.Terms, got.Groups)
	}
	if len(got.Fuzzies) != 1 || got.Fuzzies[0].Term != "databse" || got.Fuzzies[0].Dist != 1 {
		t.Fatalf("fuzzies %+v", got.Fuzzies)
	}

	same := ApplyTypoTolerance(q, presentSet("databse"))
	if len(same.Fuzzies) != 0 || len(same.Terms) != 1 || same.Terms[0] != "databse" {
		t.Fatalf("present term stays exact: terms %v fuzzies %+v", same.Terms, same.Fuzzies)
	}
}

func TestApplyTypoTolerancePresentExactUnchanged(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, presentSet("cat", "cot"))
	if len(got.Fuzzies) != 0 || len(got.Terms) != 1 || got.Terms[0] != "cat" {
		t.Fatalf("cat must stay exact when indexed: %+v", got)
	}
}

func TestApplyTypoToleranceShortStaysExactMiss(t *testing.T) {
	for _, tok := range []string{"ab", "cat", "cats"} {
		q, err := ParseQuery(tok)
		if err != nil {
			t.Fatal(err)
		}
		got := ApplyTypoTolerance(q, presentSet("dog"))
		if len(got.Fuzzies) != 0 || len(got.Terms) != 1 || got.Terms[0] != tok {
			t.Fatalf("short token %q must not expand: terms %v fuzzies %+v", tok, got.Terms, got.Fuzzies)
		}
	}
}

func TestAutoTypoDistance(t *testing.T) {
	if AutoTypoDistance(0) != 0 || AutoTypoDistance(4) != 0 {
		t.Fatal("short")
	}
	if AutoTypoDistance(5) != 1 || AutoTypoDistance(8) != 1 {
		t.Fatal("medium")
	}
	if AutoTypoDistance(9) != 2 || AutoTypoDistance(20) != 2 {
		t.Fatal("long")
	}
	if AutoTypoDistance(3) >= AutoFuzzyDistance(3) && AutoFuzzyDistance(3) != 0 {
		if AutoTypoDistance(3) != 0 {
			t.Fatal("typo must be stricter than explicit fuzzy on short tokens")
		}
	}
}

func TestApplyTypoTolerancePrefixAndFuzzyUnchanged(t *testing.T) {
	q, err := ParseQuery("cat* dog~ databse")
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, presentSet("catalog", "dog"))
	if len(got.Prefixes) != 1 || got.Prefixes[0] != "cat" {
		t.Fatalf("prefixes %v", got.Prefixes)
	}
	if len(got.Fuzzies) != 2 {
		t.Fatalf("expected dog~ plus typo databse, got %+v", got.Fuzzies)
	}
	if got.Fuzzies[0].Term != "dog" {
		t.Fatalf("explicit fuzzy first: %+v", got.Fuzzies)
	}
	if got.Fuzzies[1].Term != "databse" || got.Fuzzies[1].Dist != 1 {
		t.Fatalf("typo fuzzy %+v", got.Fuzzies[1])
	}
	if len(got.Terms) != 0 {
		t.Fatalf("databse must not stay exact: %v", got.Terms)
	}
}

func TestApplyTypoTolerancePhrase(t *testing.T) {
	q, err := ParseQuery(`"databse performance"`)
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, presentSet("database", "performance"))
	if len(got.PhraseFuzzy) != 1 || got.PhraseFuzzy[0][0].Term != "databse" || got.PhraseFuzzy[0][0].Dist != 1 {
		t.Fatalf("phrase fuzzy %+v", got.PhraseFuzzy)
	}
	if got.PhraseFuzzy[0][1].Term != "" {
		t.Fatalf("performance is present: %+v", got.PhraseFuzzy[0])
	}
	if len(got.Fuzzies) != 1 || got.Fuzzies[0].Term != "databse" {
		t.Fatalf("unquoted group from phrase: %+v terms %v", got.Fuzzies, got.Terms)
	}
	if len(got.Terms) != 1 || got.Terms[0] != "performance" {
		t.Fatalf("performance stays exact: %v", got.Terms)
	}

	pos := map[string][]uint32{
		"database":    {0},
		"performance": {1},
	}
	tf := map[string]uint32{"database": 1, "performance": 1}
	if !QueryMatches(got, tf, pos) {
		t.Fatal("typo phrase should match database performance")
	}
}

func TestApplyTypoToleranceSynonymGroup(t *testing.T) {
	q, err := ParseQueryWith("car", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Groups) != 1 || len(q.Groups[0]) < 2 {
		t.Fatalf("expected synonym group, got %+v", q.Groups)
	}
	// automobile (stemmed) is in the vocabulary: do not rewrite the group.
	kept := ApplyTypoTolerance(q, presentSet("automobil"))
	if len(kept.Fuzzies) != 0 || len(kept.Groups) != 1 {
		t.Fatalf("any alternative present keeps the group: fuzzies %+v groups %+v", kept.Fuzzies, kept.Groups)
	}
	miss := ApplyTypoTolerance(q, presentSet("dog"))
	if len(miss.Fuzzies) != 0 {
		t.Fatalf("short synonym head must not typo: %+v from %+v", miss.Fuzzies, q.Groups)
	}

	dbq, err := ParseQueryWith("database", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	rewritten := ApplyTypoTolerance(dbq, presentSet("dog"))
	if len(rewritten.Fuzzies) != 1 || rewritten.Fuzzies[0].Term != dbq.Groups[0][0] || rewritten.Fuzzies[0].Dist != 1 {
		t.Fatalf("long missing synonym group rewrites first alt: %+v from %+v", rewritten.Fuzzies, dbq.Groups)
	}
}

func TestApplyTypoToleranceNilPresent(t *testing.T) {
	q, err := ParseQuery("database")
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, nil)
	if len(got.Fuzzies) != 1 || got.Fuzzies[0].Term != "database" || got.Fuzzies[0].Dist != 1 {
		t.Fatalf("nil present treats terms as absent: %+v", got.Fuzzies)
	}
}

func TestQueryMatchesTypo(t *testing.T) {
	q, err := ParseQuery("databse")
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, presentSet("database"))
	if !QueryMatches(got, map[string]uint32{"database": 1}, map[string][]uint32{"database": {0}}) {
		t.Fatal("databse should match database when databse is absent")
	}

	exact, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	kept := ApplyTypoTolerance(exact, presentSet("cat", "cot"))
	if QueryMatches(kept, map[string]uint32{"cot": 1}, map[string][]uint32{"cot": {0}}) {
		t.Fatal("exact cat must not match cot when cat is indexed")
	}

	and, err := ParseQuery("databse dog")
	if err != nil {
		t.Fatal(err)
	}
	rewritten := ApplyTypoTolerance(and, presentSet("database", "dog"))
	if !QueryMatches(rewritten, map[string]uint32{"database": 1, "dog": 1}, map[string][]uint32{"database": {0}, "dog": {1}}) {
		t.Fatal("and")
	}
	if QueryMatches(rewritten, map[string]uint32{"database": 1}, map[string][]uint32{"database": {0}}) {
		t.Fatal("and missing dog")
	}
}

func TestQueryScoreTypoBestMatch(t *testing.T) {
	q, err := ParseQuery("databse")
	if err != nil {
		t.Fatal(err)
	}
	got := ApplyTypoTolerance(q, presentSet("database"))
	tf := map[string]uint32{"database": 3}
	df := map[string]uint64{"database": 1}
	pos := map[string][]uint32{"database": {0}}
	if !QueryMatches(got, tf, pos) {
		t.Fatal("match")
	}
	score := QueryScore(got, tf, df, 4, 4, 2)
	only := QueryScore(ParseQueryMust(t, "database~"), tf, df, 4, 4, 2)
	if score != only {
		t.Fatalf("typo must score like explicit fuzzy, got %v want %v", score, only)
	}
}

func ParseQueryMust(t *testing.T, s string) Query {
	t.Helper()
	q, err := ParseQuery(s)
	if err != nil {
		t.Fatal(err)
	}
	return q
}
