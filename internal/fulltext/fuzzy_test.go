package fulltext

import (
	"fmt"
	"testing"
)

func TestFuzzyWithin(t *testing.T) {
	if !FuzzyWithin("cat", "cat", 0) || !FuzzyWithin("cat", "cat", 1) {
		t.Fatal("equal")
	}
	if FuzzyWithin("cat", "cot", 0) {
		t.Fatal("sub needs dist 1")
	}
	if !FuzzyWithin("cat", "cot", 1) {
		t.Fatal("substitution")
	}
	if !FuzzyWithin("cat", "ca", 1) {
		t.Fatal("deletion")
	}
	if !FuzzyWithin("cat", "cats", 1) {
		t.Fatal("insertion")
	}
	if !FuzzyWithin("ab", "ba", 1) {
		t.Fatal("transposition")
	}
	if FuzzyWithin("cat", "dog", 2) {
		t.Fatal("three substitutions")
	}
	if FuzzyWithin("cat", "catalog", 2) {
		t.Fatal("catalog is four inserts")
	}
	if !FuzzyWithin("café", "cafe", 1) {
		t.Fatal("unicode substitution")
	}
	if FuzzyWithin("cat", "cot", -1) {
		t.Fatal("negative dist")
	}
}

func TestFuzzyWithinMatchesReference(t *testing.T) {
	words := []string{""}
	var add func(string, int)
	add = func(prefix string, remaining int) {
		if remaining == 0 {
			return
		}
		for _, r := range []rune("abé") {
			word := prefix + string(r)
			words = append(words, word)
			add(word, remaining-1)
		}
	}
	add("", 4)
	for _, a := range words {
		for _, b := range words {
			for max := 0; max <= MaxFuzzyDistance; max++ {
				if got, want := FuzzyWithin(a, b, max), osaReference(a, b) <= max; got != want {
					t.Fatalf("FuzzyWithin(%q, %q, %d)=%v want %v", a, b, max, got, want)
				}
			}
		}
	}
}

func osaReference(a, b string) int {
	ar, br := []rune(a), []rune(b)
	cols := len(br) + 1
	d := make([]int, (len(ar)+1)*cols)
	for i := range ar {
		d[(i+1)*cols] = i + 1
	}
	for j := range br {
		d[j+1] = j + 1
	}
	for i := 1; i <= len(ar); i++ {
		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}
			v := min(d[(i-1)*cols+j]+1, d[i*cols+j-1]+1, d[(i-1)*cols+j-1]+cost)
			if i > 1 && j > 1 && ar[i-1] == br[j-2] && ar[i-2] == br[j-1] {
				v = min(v, d[(i-2)*cols+j-2]+1)
			}
			d[i*cols+j] = v
		}
	}
	return d[len(ar)*cols+len(br)]
}

func TestAutoFuzzyDistance(t *testing.T) {
	if AutoFuzzyDistance(0) != 0 || AutoFuzzyDistance(2) != 0 {
		t.Fatal("short")
	}
	if AutoFuzzyDistance(3) != 1 || AutoFuzzyDistance(5) != 1 {
		t.Fatal("medium")
	}
	if AutoFuzzyDistance(6) != 2 || AutoFuzzyDistance(20) != 2 {
		t.Fatal("long")
	}
}

func TestFuzzyVocabularyBudgetFailClosed(t *testing.T) {
	b := NewFuzzyVocabularyBudget()
	for i := 0; i < MaxFuzzyVocabularyTerms; i++ {
		if err := b.Observe(fmt.Sprintf("term-%04d", i)); err != nil {
			t.Fatalf("term %d: %v", i, err)
		}
	}
	if err := b.Observe("term-0000"); err != nil {
		t.Fatalf("duplicate must be free: %v", err)
	}
	if err := b.Observe("overflow"); err == nil {
		t.Fatal("expected fuzzy vocabulary cap")
	}
}

func TestParseQueryFuzzy(t *testing.T) {
	q, err := ParseQuery("cat~")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != 0 || len(q.Groups) != 0 || len(q.Prefixes) != 0 {
		t.Fatalf("fuzzy must not be exact: terms %v prefixes %v", q.Terms, q.Prefixes)
	}
	if len(q.Fuzzies) != 1 || q.Fuzzies[0].Term != "cat" || q.Fuzzies[0].Dist != 1 {
		t.Fatalf("fuzzies %+v", q.Fuzzies)
	}

	exact, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.Fuzzies) != 0 || len(exact.Terms) != 1 || exact.Terms[0] != "cat" {
		t.Fatalf("exact cat: %+v", exact)
	}

	d2, err := ParseQuery("database~")
	if err != nil {
		t.Fatal(err)
	}
	if len(d2.Fuzzies) != 1 || d2.Fuzzies[0].Dist != 2 {
		t.Fatalf("auto dist 2: %+v", d2.Fuzzies)
	}

	short, err := ParseQuery("ab~")
	if err != nil {
		t.Fatal(err)
	}
	if len(short.Fuzzies) != 1 || short.Fuzzies[0].Dist != 0 || short.Fuzzies[0].Term != "ab" {
		t.Fatalf("short auto: %+v", short.Fuzzies)
	}

	ex1, err := ParseQuery("cat~1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ex1.Fuzzies) != 1 || ex1.Fuzzies[0].Term != "cat" || ex1.Fuzzies[0].Dist != 1 || len(ex1.Terms) != 0 {
		t.Fatalf("explicit 1: %+v terms %v", ex1.Fuzzies, ex1.Terms)
	}

	ex2, err := ParseQuery("cat~2")
	if err != nil {
		t.Fatal(err)
	}
	if len(ex2.Fuzzies) != 1 || ex2.Fuzzies[0].Dist != 2 {
		t.Fatalf("explicit 2: %+v", ex2.Fuzzies)
	}

	if _, err := ParseQuery("cat~3"); err == nil {
		t.Fatal("expected ~3 fail closed")
	}
	if _, err := ParseQuery("cat~0"); err == nil {
		t.Fatal("expected ~0 fail closed")
	}
	if _, err := ParseQuery("cat*~"); err == nil {
		t.Fatal("expected mixed *~ fail closed")
	}
	if _, err := ParseQuery("cat~*"); err == nil {
		t.Fatal("expected mixed ~* fail closed")
	}

	both, err := ParseQuery("cat~ dog")
	if err != nil {
		t.Fatal(err)
	}
	if len(both.Fuzzies) != 1 || both.Fuzzies[0].Term != "cat" {
		t.Fatalf("and fuzzies %+v", both.Fuzzies)
	}
	if len(both.Groups) != 1 || both.Groups[0][0] != "dog" {
		t.Fatalf("and groups %+v", both.Groups)
	}

	dup, err := ParseQuery("cat~ cat~")
	if err != nil {
		t.Fatal(err)
	}
	if len(dup.Fuzzies) != 1 {
		t.Fatalf("duplicate fuzzies %+v", dup.Fuzzies)
	}

	lead, err := ParseQuery("~cat")
	if err != nil {
		t.Fatal(err)
	}
	if len(lead.Fuzzies) != 0 || len(lead.Terms) != 1 || lead.Terms[0] != "cat" {
		t.Fatalf("leading tilde is not fuzzy: %+v", lead)
	}

	bare, err := ParseQuery("~")
	if err != nil {
		t.Fatal(err)
	}
	if !bare.Empty() {
		t.Fatalf("bare tilde must be empty: %+v", bare)
	}
}

func TestParseQueryFuzzyPhrase(t *testing.T) {
	q, err := ParseQuery(`"databas~ performance"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Fuzzies) != 1 || q.Fuzzies[0].Term != "databas" {
		t.Fatalf("fuzzies %+v", q.Fuzzies)
	}
	if len(q.Phrases) != 1 || q.Phrases[0][0] != "databas" || q.Phrases[0][1] != "performance" {
		t.Fatalf("phrase %+v", q.Phrases)
	}
	if len(q.PhraseFuzzy) != 1 || q.PhraseFuzzy[0][0].Term != "databas" || q.PhraseFuzzy[0][1].Term != "" {
		t.Fatalf("phrase fuzzy %+v", q.PhraseFuzzy)
	}
	pos := map[string][]uint32{
		"database":    {0},
		"performance": {1},
	}
	if !PhraseMatchSlots(q.PhraseAlts[0], q.PhrasePrefix[0], q.PhraseFuzzy[0], pos) {
		t.Fatal("database performance should match databas~ performance")
	}
	if PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("literal phrase still requires exact databas")
	}
	pos["performance"] = []uint32{2}
	if PhraseMatchSlots(q.PhraseAlts[0], q.PhrasePrefix[0], q.PhraseFuzzy[0], pos) {
		t.Fatal("non-adjacent")
	}
}

func TestParseQueryFuzzySkipsStemAndSynonym(t *testing.T) {
	q, err := ParseQueryWith("running~", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Fuzzies) != 1 || q.Fuzzies[0].Term != "running" {
		t.Fatalf("fuzzy must not stem: %+v", q.Fuzzies)
	}

	car, err := ParseQueryWith("car~", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(car.Fuzzies) != 1 || car.Fuzzies[0].Term != "car" {
		t.Fatalf("fuzzy %v", car.Fuzzies)
	}
	if len(car.Terms) != 0 {
		t.Fatalf("fuzzy must not expand synonyms: %v", car.Terms)
	}

	the, err := ParseQueryWith("the~", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(the.Fuzzies) != 1 || the.Fuzzies[0].Term != "the" {
		t.Fatalf("fuzzy must not drop stops: %+v", the.Fuzzies)
	}

	fr, err := ParseQueryWith("l'homm~", French)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Fuzzies) != 1 || fr.Fuzzies[0].Term != "homm" {
		t.Fatalf("french fuzzy elision: %+v", fr.Fuzzies)
	}
}

func TestQueryMatchesFuzzy(t *testing.T) {
	q, err := ParseQuery("cat~")
	if err != nil {
		t.Fatal(err)
	}
	if !QueryMatches(q, map[string]uint32{"cat": 1}, map[string][]uint32{"cat": {0}}) {
		t.Fatal("exact cat")
	}
	if !QueryMatches(q, map[string]uint32{"cot": 1}, map[string][]uint32{"cot": {0}}) {
		t.Fatal("cot")
	}
	if QueryMatches(q, map[string]uint32{"catalog": 1}, map[string][]uint32{"catalog": {0}}) {
		t.Fatal("catalog is not within auto dist 1 of cat")
	}
	if QueryMatches(q, map[string]uint32{"dog": 1}, map[string][]uint32{"dog": {0}}) {
		t.Fatal("unrelated")
	}

	exact, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	if QueryMatches(exact, map[string]uint32{"cot": 1}, map[string][]uint32{"cot": {0}}) {
		t.Fatal("exact cat must not match cot")
	}

	and, err := ParseQuery("cat~ dog")
	if err != nil {
		t.Fatal(err)
	}
	if !QueryMatches(and, map[string]uint32{"cot": 1, "dog": 1}, map[string][]uint32{"cot": {0}, "dog": {1}}) {
		t.Fatal("and")
	}
	if QueryMatches(and, map[string]uint32{"cot": 1}, map[string][]uint32{"cot": {0}}) {
		t.Fatal("and missing dog")
	}

	wide, err := ParseQuery("cat~2")
	if err != nil {
		t.Fatal(err)
	}
	if !QueryMatches(wide, map[string]uint32{"ca": 1}, map[string][]uint32{"ca": {0}}) {
		t.Fatal("explicit dist 2 deletion")
	}
}

func TestQueryScoreFuzzyBestMatch(t *testing.T) {
	q, err := ParseQuery("cat~")
	if err != nil {
		t.Fatal(err)
	}
	tf := map[string]uint32{"cat": 1, "cot": 3}
	df := map[string]uint64{"cat": 1, "cot": 1}
	pos := map[string][]uint32{"cat": {0}, "cot": {1}}
	if !QueryMatches(q, tf, pos) {
		t.Fatal("match")
	}
	got := QueryScore(q, tf, df, 4, 4, 2)
	onlyCot := QueryScore(q, map[string]uint32{"cot": 3}, df, 4, 4, 2)
	if got != onlyCot {
		t.Fatalf("must score best alternative, got %v want %v", got, onlyCot)
	}
}

func TestFuzzyExpanderFailClosed(t *testing.T) {
	e := NewPrefixExpander(nil)
	for i := 0; i < MaxQueryExpansions; i++ {
		if err := e.Observe(fmt.Sprintf("t%03d", i)); err != nil {
			t.Fatalf("term %d: %v", i, err)
		}
	}
	if err := e.Observe("overflow-term"); err == nil {
		t.Fatal("term cap")
	}
}
