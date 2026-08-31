package fulltext

import "testing"

func TestEnglishStopV1Membership(t *testing.T) {
	if len(englishStopV1) != 33 {
		t.Fatalf("dictionary size %d want 33", len(englishStopV1))
	}
	for _, w := range []string{"the", "a", "and", "of", "to", "in", "on", "is", "it", "not"} {
		if !isEnglishStopV1(w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, w := range []string{"cat", "database", "running", "thee", "THE", "don't"} {
		if isEnglishStopV1(w) {
			t.Errorf("unexpected %q", w)
		}
	}
}

func TestAnalyzeEnglishDropsStops(t *testing.T) {
	doc, err := AnalyzeWith("the cat sat on the mat", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len != 3 {
		t.Fatalf("len %d want 3 (stop words dropped)", doc.Len)
	}
	got := map[string][]uint32{}
	for _, tp := range doc.Terms {
		got[tp.Term] = tp.Pos
	}
	if _, ok := got["the"]; ok {
		t.Fatalf("the should be dropped: %+v", doc.Terms)
	}
	if _, ok := got["on"]; ok {
		t.Fatalf("on should be dropped: %+v", doc.Terms)
	}
	if len(got["cat"]) != 1 || got["cat"][0] != 0 {
		t.Fatalf("cat %+v", got["cat"])
	}
	if len(got["sat"]) != 1 || got["sat"][0] != 1 {
		t.Fatalf("sat %+v", got["sat"])
	}
	if len(got["mat"]) != 1 || got["mat"][0] != 2 {
		t.Fatalf("mat %+v", got["mat"])
	}

	v1, err := AnalyzeWith("the cat sat on the mat", EnglishV1)
	if err != nil {
		t.Fatal(err)
	}
	if v1.Len != 6 {
		t.Fatalf("v1 must keep stop words: len %d", v1.Len)
	}
	foundThe := false
	for _, tp := range v1.Terms {
		if tp.Term == "the" {
			foundThe = true
		}
	}
	if !foundThe {
		t.Fatal("english v1 must keep the")
	}

	simple, err := Analyze("the cat sat")
	if err != nil {
		t.Fatal(err)
	}
	if simple.Len != 3 {
		t.Fatalf("simple must keep stop words: len %d", simple.Len)
	}
}

func TestAnalyzeEnglishStopsThenStems(t *testing.T) {
	doc, err := AnalyzeWith("the running cats", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len != 2 {
		t.Fatalf("len %d want 2", doc.Len)
	}
	got := map[string]uint32{}
	for _, tp := range doc.Terms {
		got[tp.Term] = tp.TF
	}
	if got["run"] != 1 || got["cat"] != 1 {
		t.Fatalf("terms %+v", doc.Terms)
	}
	if _, ok := got["the"]; ok {
		t.Fatal("the")
	}
}

func TestParseQueryEnglishDropsStops(t *testing.T) {
	q, err := ParseQueryWith("the cat sat", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != 2 || q.Terms[0] != "cat" || q.Terms[1] != "sat" {
		t.Fatalf("terms %v", q.Terms)
	}

	empty, err := ParseQueryWith("the the the", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(empty.Terms) != 0 || len(empty.Phrases) != 0 {
		t.Fatalf("stop-only query %+v", empty)
	}

	simple, err := ParseQuery("the cat")
	if err != nil {
		t.Fatal(err)
	}
	if len(simple.Terms) != 2 {
		t.Fatalf("simple must keep the: %v", simple.Terms)
	}
}

func TestParseQueryEnglishPhraseDropsStops(t *testing.T) {
	q, err := ParseQueryWith(`"the running cats"`, EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Phrases) != 1 || len(q.Phrases[0]) != 2 || q.Phrases[0][0] != "run" || q.Phrases[0][1] != "cat" {
		t.Fatalf("phrase %+v", q.Phrases)
	}
	doc, err := AnalyzeWith("the running cats", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	pos := map[string][]uint32{}
	for _, tp := range doc.Terms {
		pos[tp.Term] = tp.Pos
	}
	if !PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("filtered phrase should match consecutive remaining terms")
	}
}

func TestEnglishStopWorkCounts(t *testing.T) {
	b := &expandBudget{}
	if err := b.addWork(1); err != nil {
		t.Fatal(err)
	}
	over := &expandBudget{work: MaxQueryExpandWork}
	if err := over.addWork(1); err == nil {
		t.Fatal("work cap")
	}
	q, err := ParseQueryWith("the the the cat", EnglishV2)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != 1 || q.Terms[0] != "cat" {
		t.Fatalf("terms %v", q.Terms)
	}
}
