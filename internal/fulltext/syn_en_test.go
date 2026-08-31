package fulltext

import (
	"sort"
	"testing"
)

func TestEnglishSynonymV1Membership(t *testing.T) {
	if len(englishSynonymV1Groups) != 15 {
		t.Fatalf("group count %d want 15", len(englishSynonymV1Groups))
	}
	seenStem := map[string]string{}
	for _, g := range englishSynonymV1Groups {
		if len(g) < 2 {
			t.Fatalf("group too small %v", g)
		}
		stems := uniqueSortedStems(g, stemEnglish)
		if len(stems) < 2 {
			t.Fatalf("stems collapsed %v → %v", g, stems)
		}
		if len(stems)-1 > MaxSynonymAlts {
			t.Fatalf("group exceeds alt cap %v", stems)
		}
		for _, s := range stems {
			if prev, ok := seenStem[s]; ok {
				t.Fatalf("stem %q overlaps %q and %q", s, prev, g[0])
			}
			seenStem[s] = g[0]
			if isEnglishStopV1(s) {
				t.Fatalf("stop word in synonym dict %q", s)
			}
		}
		for _, w := range g {
			if isEnglishStopV1(w) {
				t.Fatalf("stop surface form %q", w)
			}
		}
	}
	if len(englishSynonymV1) != len(seenStem) {
		t.Fatalf("compiled map %d stems want %d", len(englishSynonymV1), len(seenStem))
	}
	car := stemEnglish("car")
	auto := stemEnglish("automobile")
	if car == auto {
		t.Fatal("car/automobile collapsed")
	}
	got := append([]string(nil), englishSynonymV1[car]...)
	sort.Strings(got)
	if len(got) != 2 || got[0] != auto && got[0] != car {
		t.Fatalf("car group %v", got)
	}
	if !containsStr(got, car) || !containsStr(got, auto) {
		t.Fatalf("car group %v want %q %q", got, car, auto)
	}
	if _, ok := englishSynonymV1["cat"]; ok {
		t.Fatal("cat is not a synonym")
	}
}

func containsStr(xs []string, w string) bool {
	for _, x := range xs {
		if x == w {
			return true
		}
	}
	return false
}

func TestAnalyzeEnglishNoIndexSynonyms(t *testing.T) {
	doc, err := AnalyzeWith("the car sat", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string][]uint32{}
	for _, tp := range doc.Terms {
		got[tp.Term] = tp.Pos
	}
	if _, ok := got["car"]; !ok {
		t.Fatalf("missing car: %+v", doc.Terms)
	}
	if _, ok := got[stemEnglish("automobile")]; ok {
		t.Fatalf("index must not emit synonyms: %+v", doc.Terms)
	}
	if doc.Len != 2 {
		t.Fatalf("len %d want 2 (the dropped)", doc.Len)
	}

	v2, err := AnalyzeWith("the car sat", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if v2.Len != doc.Len || len(v2.Terms) != len(doc.Terms) {
		t.Fatalf("v3 index must match v2: v2 %+v v3 %+v", v2.Terms, doc.Terms)
	}
}

func TestParseQueryEnglishSynonyms(t *testing.T) {
	q, err := ParseQueryWith("car", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) < 2 || q.Terms[0] != "car" {
		t.Fatalf("terms %v", q.Terms)
	}
	auto := stemEnglish("automobile")
	if !containsStr(q.Terms, auto) {
		t.Fatalf("missing automobile stem %q in %v", auto, q.Terms)
	}
	if len(q.Groups) != 1 || len(q.Groups[0]) < 2 {
		t.Fatalf("groups %+v", q.Groups)
	}

	v2, err := ParseQueryWith("car", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(v2.Terms) != 1 || v2.Terms[0] != "car" {
		t.Fatalf("v2 must not expand: %v", v2.Terms)
	}

	simple, err := ParseQuery("car")
	if err != nil {
		t.Fatal(err)
	}
	if len(simple.Terms) != 1 || simple.Terms[0] != "car" {
		t.Fatalf("simple must not expand: %v", simple.Terms)
	}

	both, err := ParseQueryWith("car automobile", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(both.Groups) != 1 {
		t.Fatalf("duplicate synonym groups should collapse: %+v", both.Groups)
	}
}

func TestParseQueryEnglishSynonymPhrase(t *testing.T) {
	q, err := ParseQueryWith(`"red car"`, EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Phrases) != 1 || q.Phrases[0][0] != "red" || q.Phrases[0][1] != "car" {
		t.Fatalf("phrase %+v", q.Phrases)
	}
	if len(q.PhraseAlts) != 1 || len(q.PhraseAlts[0]) != 2 || len(q.PhraseAlts[0][1]) < 2 {
		t.Fatalf("phrase alts %+v", q.PhraseAlts)
	}
	auto := stemEnglish("automobile")
	pos := map[string][]uint32{
		"red": {0},
		auto:  {1},
	}
	if !PhraseMatchAny(q.PhraseAlts[0], pos) {
		t.Fatal("red automobile should match red car phrase")
	}
	if PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("literal phrase must still require car at pos 1")
	}
}

func TestQueryMatchesSynonymDisjunction(t *testing.T) {
	q, err := ParseQueryWith("automobile", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	tf := map[string]uint32{"car": 1}
	pos := map[string][]uint32{"car": {0}}
	if !QueryMatches(q, tf, pos) {
		t.Fatal("car document should match automobile query")
	}
	if QueryMatches(q, map[string]uint32{"dog": 1}, map[string][]uint32{"dog": {0}}) {
		t.Fatal("unrelated document")
	}
}

func TestEnglishSynonymWorkCounts(t *testing.T) {
	q, err := ParseQueryWith("car", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) < 2 {
		t.Fatalf("terms %v", q.Terms)
	}
	over := &expandBudget{terms: MaxQueryExpansions}
	if err := over.account("x", 1); err == nil {
		t.Fatal("term cap")
	}
}
