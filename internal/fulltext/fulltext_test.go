package fulltext

import (
	"math"
	"testing"
)

func TestTokenizeNormalize(t *testing.T) {
	got := Tokenize("Database Performance! CAFÉ don't  SQL2")
	want := []string{"database", "performance", "café", "don't", "sql2"}
	if len(got) != len(want) {
		t.Fatalf("got %v", terms(got))
	}
	for i, w := range want {
		if got[i].Term != w || got[i].Pos != uint32(i) {
			t.Fatalf("tok %d: %+v want %s", i, got[i], w)
		}
	}
	if Tokenize("") != nil && len(Tokenize("")) != 0 {
		t.Fatal("empty")
	}
	if n := Tokenize("noise-cancelling"); len(n) != 2 || n[0].Term != "noise" || n[1].Term != "cancelling" {
		t.Fatalf("hyphen %+v", n)
	}
}

func TestAnalyzePositions(t *testing.T) {
	doc, err := Analyze("cat sat cat")
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len != 3 || len(doc.Terms) != 2 {
		t.Fatalf("%+v", doc)
	}
	if doc.Terms[0].Term != "cat" || doc.Terms[0].TF != 2 || len(doc.Terms[0].Pos) != 2 || doc.Terms[0].Pos[0] != 0 || doc.Terms[0].Pos[1] != 2 {
		t.Fatalf("cat %+v", doc.Terms[0])
	}
}

func TestAnalyzeFieldsPositions(t *testing.T) {
	doc, err := AnalyzeFields([]string{"database", "performance tuning"}, Simple)
	if err != nil {
		t.Fatal(err)
	}
	if doc.Len != 3 {
		t.Fatalf("len %+v", doc)
	}
	pos := map[string][]uint32{}
	tf := map[string]uint32{}
	for _, tp := range doc.Terms {
		pos[tp.Term] = tp.Pos
		tf[tp.Term] = tp.TF
	}
	if tf["database"] != 1 || tf["performance"] != 1 || tf["tuning"] != 1 {
		t.Fatalf("tf %v", tf)
	}
	if pos["database"][0] != 0 || pos["performance"][0] != FieldPositionGap || pos["tuning"][0] != FieldPositionGap+1 {
		t.Fatalf("pos %v", pos)
	}
	if PhraseMatch([]string{"database", "performance"}, pos) {
		t.Fatal("phrase must not cross fields")
	}
	if !PhraseMatch([]string{"performance", "tuning"}, pos) {
		t.Fatal("in-field phrase")
	}
	empty, err := AnalyzeFields([]string{"", "cat sat"}, Simple)
	if err != nil {
		t.Fatal(err)
	}
	if empty.Len != 2 || empty.Terms[0].Pos[0] != FieldPositionGap {
		t.Fatalf("empty field band %+v", empty)
	}
	merged, err := AnalyzeFields([]string{"cat sat", "the cat"}, Simple)
	if err != nil {
		t.Fatal(err)
	}
	var cat TermPosting
	for _, tp := range merged.Terms {
		if tp.Term == "cat" {
			cat = tp
		}
	}
	if cat.TF != 2 || len(cat.Pos) != 2 || cat.Pos[0] != 0 || cat.Pos[1] != FieldPositionGap+1 {
		t.Fatalf("merged cat %+v", cat)
	}
	if _, err := AnalyzeFields(make([]string, MaxFields+1), Simple); err == nil {
		t.Fatal("expected too many fields")
	}
	one, err := AnalyzeFields([]string{"cat sat cat"}, Simple)
	if err != nil {
		t.Fatal(err)
	}
	single, err := Analyze("cat sat cat")
	if err != nil {
		t.Fatal(err)
	}
	if one.Len != single.Len || len(one.Terms) != len(single.Terms) {
		t.Fatalf("single-field shortcut %+v vs %+v", one, single)
	}
}

func TestParseQueryPhrase(t *testing.T) {
	q, err := ParseQuery(`wireless "noise cancelling" headphones`)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != 4 {
		t.Fatalf("terms %v", q.Terms)
	}
	if len(q.Phrases) != 1 || q.Phrases[0][0] != "noise" || q.Phrases[0][1] != "cancelling" {
		t.Fatalf("phrases %+v", q.Phrases)
	}
	pos := map[string][]uint32{
		"noise":      {1},
		"cancelling": {2},
	}
	if !PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("expected phrase")
	}
	pos["cancelling"] = []uint32{4}
	if PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("non-adjacent")
	}
}

func TestPostingRoundTrip(t *testing.T) {
	pk := []byte{0xAA, 0xBB}
	k := PostingKey("cat", pk)
	term, gotPK, err := SplitPostingKey(k)
	if err != nil || term != "cat" || string(gotPK) != string(pk) {
		t.Fatalf("%s %x %v", term, gotPK, err)
	}
	raw := EncodePosting(2, []uint32{0, 4})
	tf, pos, err := DecodePosting(raw)
	if err != nil || tf != 2 || len(pos) != 2 || pos[1] != 4 {
		t.Fatalf("%d %v %v", tf, pos, err)
	}
	st := EncodeStats(Stats{Docs: 3, Tokens: 12})
	got, err := DecodeStats(st)
	if err != nil || got.Docs != 3 || got.Tokens != 12 {
		t.Fatalf("%+v %v", got, err)
	}
	dl := EncodeDocLen(7)
	n, err := DecodeDocLen(dl)
	if err != nil || n != 7 {
		t.Fatalf("%d %v", n, err)
	}
	doc, err := Analyze("cat sat cat")
	if err != nil {
		t.Fatal(err)
	}
	pairs := EncodeDocPairs(pk, doc)
	if len(pairs) != 3 { // cat, sat, doclen
		t.Fatalf("%d pairs", len(pairs))
	}
}

func TestDecodeFailsClosed(t *testing.T) {
	if _, _, err := DecodePosting(nil); err == nil {
		t.Fatal("empty posting")
	}
	if _, _, err := DecodePosting([]byte{2, 0, 0, 0, 0, 0, 0, 0, 0}); err == nil {
		t.Fatal("bad version")
	}
	if _, err := DecodeStats([]byte{1}); err == nil {
		t.Fatal("short stats")
	}
	if _, _, err := SplitPostingKey([]byte{kindPost, 'a'}); err == nil {
		t.Fatal("no nul")
	}
}

func TestBM25Fixture(t *testing.T) {
	// Corpus:
	//   d1: "the cat sat"           dl=3  tf(cat)=1
	//   d2: "the cat sat on the mat" dl=6  tf(cat)=1
	//   d3: "dogs and cats"          dl=3  tf(cat)=0
	const n, df = 3, 2
	avg := AvgDL(Stats{Docs: 3, Tokens: 12})
	if avg != 4 {
		t.Fatalf("avgdl %v", avg)
	}
	idf := IDF(n, df)
	wantIDF := math.Log(1 + (3-2+0.5)/(2+0.5))
	if math.Abs(idf-wantIDF) > 1e-12 {
		t.Fatalf("idf %v want %v", idf, wantIDF)
	}
	s1 := Score(1, 3, avg, idf)
	s2 := Score(1, 6, avg, idf)
	// Shorter document with the same tf must rank higher.
	if s1 <= s2 || s1 <= 0 || s2 <= 0 {
		t.Fatalf("scores %v %v", s1, s2)
	}
	// Hand expansion of BM25 for d1.
	denom := 1 + K1*(1-B+B*3/4)
	want := idf * (1 * (K1 + 1) / denom)
	if math.Abs(s1-want) > 1e-12 {
		t.Fatalf("d1 %v want %v", s1, want)
	}
	if Score(0, 3, avg, idf) != 0 {
		t.Fatal("zero tf")
	}
}

func TestWeightedTF(t *testing.T) {
	pos := []uint32{0, FieldPositionGap, FieldPositionGap + 1}
	if got := WeightedTF(pos, nil); got != 3 {
		t.Fatalf("unweighted %v", got)
	}
	if got := WeightedTF(pos, []float64{1, 1}); got != 3 {
		t.Fatalf("uniform %v", got)
	}
	if got := WeightedTF(pos, []float64{3, 1}); got != 5 {
		t.Fatalf("title×3 %v", got)
	}
	if got := WeightedTF(nil, []float64{3, 1}); got != 0 {
		t.Fatal("empty")
	}
	if !UniformWeights(nil) || !UniformWeights([]float64{1, 1}) || UniformWeights([]float64{3, 1}) {
		t.Fatal("uniform")
	}
}

func TestCheckFieldWeight(t *testing.T) {
	for _, w := range []float64{0.5, 1, 3, 64} {
		if err := CheckFieldWeight(w); err != nil {
			t.Fatalf("%v: %v", w, err)
		}
	}
	for _, w := range []float64{0, -1, 64.1, math.Inf(1), math.NaN()} {
		if err := CheckFieldWeight(w); err == nil {
			t.Fatalf("expected reject %v", w)
		}
	}
}

func TestQueryScoreWeighted(t *testing.T) {
	q, err := ParseQuery("database")
	if err != nil {
		t.Fatal(err)
	}
	df := map[string]uint64{"database": 2}
	titlePos := map[string][]uint32{"database": {0}}
	bodyPos := map[string][]uint32{"database": {FieldPositionGap}}
	tf := map[string]uint32{"database": 1}
	const n uint64 = 2
	avg := AvgDL(Stats{Docs: 2, Tokens: 12})
	unweightedTitle := QueryScore(q, tf, df, 9, avg, n)
	unweightedBody := QueryScore(q, tf, df, 3, avg, n)
	if unweightedBody <= unweightedTitle {
		t.Fatalf("shorter body should win unweighted: title=%v body=%v", unweightedTitle, unweightedBody)
	}
	w := []float64{3, 1}
	weightedTitle := QueryScoreWeighted(q, tf, titlePos, w, df, 9, avg, n)
	weightedBody := QueryScoreWeighted(q, tf, bodyPos, w, df, 3, avg, n)
	if weightedTitle <= weightedBody {
		t.Fatalf("title WEIGHT 3 should win: title=%v body=%v", weightedTitle, weightedBody)
	}
	same := QueryScoreWeighted(q, tf, titlePos, nil, df, 9, avg, n)
	if same != unweightedTitle {
		t.Fatalf("nil weights %v want %v", same, unweightedTitle)
	}
}

func TestLessHit(t *testing.T) {
	a := Hit{PK: []byte{1}, Score: 2}
	b := Hit{PK: []byte{2}, Score: 1}
	if !LessHit(a, b) {
		t.Fatal("score")
	}
	c := Hit{PK: []byte{0}, Score: 2}
	if !LessHit(c, a) {
		t.Fatal("pk tie-break")
	}
}

func terms(toks []Token) []string {
	out := make([]string, len(toks))
	for i, t := range toks {
		out[i] = t.Term
	}
	return out
}
