package fulltext

import (
	"strings"
	"testing"
)

func TestTokenizeSpans(t *testing.T) {
	text := "the CAT sat"
	toks := Tokenize(text)
	if len(toks) != 3 {
		t.Fatalf("toks %v", terms(toks))
	}
	want := []string{"the", "CAT", "sat"}
	for i, w := range want {
		got := text[toks[i].Start:toks[i].End]
		if got != w {
			t.Fatalf("span %d: %q want %q (tok %+v)", i, got, w, toks[i])
		}
	}
	hy := Tokenize("noise-cancelling")
	if len(hy) != 2 || "noise-cancelling"[hy[0].Start:hy[0].End] != "noise" || "noise-cancelling"[hy[1].Start:hy[1].End] != "cancelling" {
		t.Fatalf("hyphen %+v", hy)
	}
}

func TestHighlightExact(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Highlight("the cat sat", q, Simple, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "the <mark>cat</mark> sat" {
		t.Fatalf("%q", got)
	}
}

func TestHighlightPreservesOriginalCase(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Highlight("the CAT sat", q, Simple, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "the <mark>CAT</mark> sat" {
		t.Fatalf("%q", got)
	}
}

func TestHighlightPrefixFuzzyTypo(t *testing.T) {
	cases := []struct {
		query, text, want string
	}{
		{"cat*", "the catalog of dogs", "the <mark>catalog</mark> of dogs"},
		{"cat~", "the cot sat", "the <mark>cot</mark> <mark>sat</mark>"},
		{"databse", "database performance tuning", "<mark>database</mark> performance tuning"},
		{`"databse performance"`, "database performance tuning", "<mark>database</mark> <mark>performance</mark> tuning"},
	}
	for _, tc := range cases {
		q, err := ParseQuery(tc.query)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		q = ApplyTypoTolerance(q, func(term string) bool {
			doc, err := Analyze(tc.text)
			if err != nil {
				t.Fatal(err)
			}
			for _, t := range doc.Terms {
				if t.Term == term {
					return true
				}
			}
			return false
		})
		got, err := Highlight(tc.text, q, Simple, DefaultHighlightPre, DefaultHighlightPost)
		if err != nil {
			t.Fatalf("%s: %v", tc.query, err)
		}
		if got != tc.want {
			t.Fatalf("%s: %q want %q", tc.query, got, tc.want)
		}
	}
}

func TestHighlightEnglishStemAndSynonym(t *testing.T) {
	q, err := ParseQueryWith("runs", English)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Highlight("running dogs", q, English, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "<mark>running</mark> dogs" {
		t.Fatalf("stem %q", got)
	}
	q, err = ParseQueryWith("car", English)
	if err != nil {
		t.Fatal(err)
	}
	got, err = Highlight("red automobile", q, English, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "red <mark>automobile</mark>" {
		t.Fatalf("synonym %q", got)
	}
}

func TestHighlightEnglishDropsStops(t *testing.T) {
	q, err := ParseQueryWith("the cat", English)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Highlight("the cat sat", q, English, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "the <mark>cat</mark> sat" {
		t.Fatalf("%q", got)
	}
}

func TestHighlightCustomMarkersAndEmptyQuery(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Highlight("the cat sat", q, Simple, "**", "**")
	if err != nil {
		t.Fatal(err)
	}
	if got != "the **cat** sat" {
		t.Fatalf("%q", got)
	}
	empty, err := ParseQuery("   ")
	if err != nil {
		t.Fatal(err)
	}
	got, err = Highlight("the cat sat", empty, Simple, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "the cat sat" {
		t.Fatalf("empty query %q", got)
	}
}

func TestHighlightMarkerLimits(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	long := strings.Repeat("x", MaxHighlightMarkerRunes+1)
	if _, err := Highlight("cat", q, Simple, long, ""); err == nil {
		t.Fatal("expected marker length error")
	}
	if _, err := Highlight("cat", q, Simple, "a\x00b", ""); err == nil {
		t.Fatal("expected NUL marker error")
	}
}

func TestSnippetWindow(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	b.WriteString(strings.Repeat("aaa ", 40))
	b.WriteString("the cat sat")
	b.WriteString(strings.Repeat(" zzz", 40))
	text := b.String()
	got, err := Snippet(text, q, Simple, 32, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, "<mark>cat</mark>") {
		t.Fatalf("missing mark %q", got)
	}
	if !strings.HasPrefix(got, SnippetEllipsis) || !strings.HasSuffix(got, SnippetEllipsis) {
		t.Fatalf("ellipsis %q", got)
	}
}

func TestSnippetShortTextNoEllipsis(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	got, err := Snippet("the cat sat", q, Simple, DefaultSnippetRunes, DefaultHighlightPre, DefaultHighlightPost)
	if err != nil {
		t.Fatal(err)
	}
	if got != "the <mark>cat</mark> sat" {
		t.Fatalf("%q", got)
	}
}

func TestSnippetWidthBounds(t *testing.T) {
	q, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Snippet("the cat sat", q, Simple, MinSnippetRunes-1, "", ""); err == nil {
		t.Fatal("expected min width error")
	}
	if _, err := Snippet("the cat sat", q, Simple, MaxSnippetRunes+1, "", ""); err == nil {
		t.Fatal("expected max width error")
	}
}

func TestHighlightsTermPrefixAndFuzzy(t *testing.T) {
	q, err := ParseQuery("cat* dog~ databse")
	if err != nil {
		t.Fatal(err)
	}
	q = ApplyTypoTolerance(q, func(string) bool { return false })
	if !q.HighlightsTerm("catalog") || !q.HighlightsTerm("dot") || !q.HighlightsTerm("database") {
		t.Fatalf("highlights %+v", q)
	}
	if q.HighlightsTerm("horse") {
		t.Fatal("horse")
	}
}
