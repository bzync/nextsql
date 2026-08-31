package fulltext

import (
	"strings"
	"testing"
)

func TestStemEnglishFixtures(t *testing.T) {
	pairs := map[string]string{
		"cats":          "cat",
		"running":       "run",
		"runs":          "run",
		"ran":           "ran",
		"hopping":       "hop",
		"hoping":        "hope",
		"relational":    "relat",
		"universities":  "univers",
		"university":    "univers",
		"allowance":     "allow",
		"consignment":   "consign",
		"knackered":     "knacker",
		"ponies":        "poni",
		"cries":         "cri",
		"ties":          "tie",
		"skis":          "ski",
		"skies":         "sky",
		"dying":         "die",
		"lying":         "lie",
		"news":          "news",
		"inning":        "inning",
		"proceed":       "proceed",
		"generate":      "generat",
		"communication": "communic",
		"electrical":    "electr",
		"hopeful":       "hope",
		"goodness":      "good",
		"activate":      "activ",
		"adoption":      "adopt",
		"adjustment":    "adjust",
		"dependent":     "depend",
		"traditional":   "tradit",
		"hops":          "hop",
		"hoped":         "hope",
		"sky":           "sky",
		"exceed":        "exceed",
		"singly":        "singl",
		"early":         "earli",
		"don't":         "don't",
		"café":          "café",
		"sql2":          "sql2",
		"a":             "a",
		"the":           "the",
	}
	for in, want := range pairs {
		if got := stemEnglish(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestStemEnglishDeterministic(t *testing.T) {
	for _, s := range []string{"running", "cats", "universities", "don't", "CAFÉ"} {
		a := stemEnglish(s)
		b := stemEnglish(s)
		if a != b {
			t.Fatalf("nondeterministic %q: %q %q", s, a, b)
		}
	}
}

func TestAnalyzeEnglishStems(t *testing.T) {
	doc, err := AnalyzeWith("the running cats", EnglishV1)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]uint32{}
	for _, t := range doc.Terms {
		got[t.Term] = t.TF
	}
	if got["run"] != 1 || got["cat"] != 1 || got["the"] != 1 {
		t.Fatalf("terms %+v", doc.Terms)
	}
	if _, ok := got["running"]; ok {
		t.Fatal("unstemmed running")
	}
	simple, err := Analyze("the running cats")
	if err != nil {
		t.Fatal(err)
	}
	foundRunning := false
	for _, t := range simple.Terms {
		if t.Term == "running" {
			foundRunning = true
		}
	}
	if !foundRunning {
		t.Fatal("simple analyzer must keep running")
	}
}

func TestParseQueryEnglishPhrase(t *testing.T) {
	q, err := ParseQueryWith(`"running cats"`, EnglishV1)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Phrases) != 1 || q.Phrases[0][0] != "run" || q.Phrases[0][1] != "cat" {
		t.Fatalf("phrase %+v", q.Phrases)
	}
	pos := map[string][]uint32{"run": {0}, "cat": {1}}
	if !PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("stemmed phrase should match consecutive stems")
	}
}

func TestQueryExpansionCapsFailClosed(t *testing.T) {
	b := &expandBudget{}
	if err := b.account("x", 1); err != nil {
		t.Fatal(err)
	}
	over := &expandBudget{terms: MaxQueryExpansions}
	if err := over.account("x", 1); err == nil {
		t.Fatal("term cap")
	}
	over = &expandBudget{bytes: MaxQueryExpansionBytes}
	if err := over.account("x", 1); err == nil {
		t.Fatal("byte cap")
	}
	over = &expandBudget{work: MaxQueryExpandWork}
	if err := over.account("x", 1); err == nil {
		t.Fatal("work cap")
	}
	var q strings.Builder
	for i := 0; i < MaxQueryTokens+1; i++ {
		q.WriteString("cat ")
	}
	if _, err := ParseQuery(q.String()); err == nil {
		t.Fatal("query token cap")
	}
}

func TestLookupAnalyzer(t *testing.T) {
	a, err := LookupAnalyzer("english")
	if err != nil || a != EnglishV3 {
		t.Fatalf("%+v %v", a, err)
	}
	a, err = LookupAnalyzer("SIMPLE")
	if err != nil || a != Simple {
		t.Fatalf("%+v %v", a, err)
	}
	if _, err := LookupAnalyzer("klingon"); err == nil {
		t.Fatal("unknown")
	}
	if err := (Analyzer{ID: 99}).Validate(); err == nil {
		t.Fatal("bad id")
	}
	if err := (Analyzer{ID: AnalyzerEnglish, Version: 4}).Validate(); err == nil {
		t.Fatal("bad english version")
	}
	if err := EnglishV1.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := EnglishV2.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := EnglishV3.Validate(); err != nil {
		t.Fatal(err)
	}
}
