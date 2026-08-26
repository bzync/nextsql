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
