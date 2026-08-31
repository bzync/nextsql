package fulltext

import (
	"bytes"
	"fmt"
	"testing"
)

func TestParseQueryPrefix(t *testing.T) {
	q, err := ParseQuery("cat*")
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != 0 || len(q.Groups) != 0 {
		t.Fatalf("prefix must not be an exact term: terms %v groups %+v", q.Terms, q.Groups)
	}
	if len(q.Prefixes) != 1 || q.Prefixes[0] != "cat" {
		t.Fatalf("prefixes %v", q.Prefixes)
	}

	exact, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	if len(exact.Prefixes) != 0 || len(exact.Terms) != 1 || exact.Terms[0] != "cat" {
		t.Fatalf("exact cat: terms %v prefixes %v", exact.Terms, exact.Prefixes)
	}

	both, err := ParseQuery("cat* dog")
	if err != nil {
		t.Fatal(err)
	}
	if len(both.Prefixes) != 1 || both.Prefixes[0] != "cat" {
		t.Fatalf("and prefixes %v", both.Prefixes)
	}
	if len(both.Groups) != 1 || both.Groups[0][0] != "dog" {
		t.Fatalf("and groups %+v", both.Groups)
	}

	dup, err := ParseQuery("cat* cat*")
	if err != nil {
		t.Fatal(err)
	}
	if len(dup.Prefixes) != 1 {
		t.Fatalf("duplicate prefixes %v", dup.Prefixes)
	}

	lead, err := ParseQuery("*cat")
	if err != nil {
		t.Fatal(err)
	}
	if len(lead.Prefixes) != 0 || len(lead.Terms) != 1 || lead.Terms[0] != "cat" {
		t.Fatalf("leading star is not a prefix: %+v", lead)
	}

	star, err := ParseQuery("*")
	if err != nil {
		t.Fatal(err)
	}
	if !star.Empty() {
		t.Fatalf("bare star must be empty: %+v", star)
	}
}

func TestParseQueryPrefixPhrase(t *testing.T) {
	q, err := ParseQuery(`"data* performance"`)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Prefixes) != 1 || q.Prefixes[0] != "data" {
		t.Fatalf("prefixes %v", q.Prefixes)
	}
	if len(q.Phrases) != 1 || q.Phrases[0][0] != "data" || q.Phrases[0][1] != "performance" {
		t.Fatalf("phrase %+v", q.Phrases)
	}
	if len(q.PhrasePrefix) != 1 || q.PhrasePrefix[0][0] != "data" || q.PhrasePrefix[0][1] != "" {
		t.Fatalf("phrase prefix %+v", q.PhrasePrefix)
	}
	pos := map[string][]uint32{
		"database":    {0},
		"performance": {1},
	}
	if !PhraseMatchSlots(q.PhraseAlts[0], q.PhrasePrefix[0], nil, pos) {
		t.Fatal("database performance should match data* performance")
	}
	if PhraseMatch(q.Phrases[0], pos) {
		t.Fatal("literal phrase still requires exact data")
	}
	pos["performance"] = []uint32{2}
	if PhraseMatchSlots(q.PhraseAlts[0], q.PhrasePrefix[0], nil, pos) {
		t.Fatal("non-adjacent")
	}
}

func TestParseQueryPrefixSkipsStemAndSynonym(t *testing.T) {
	q, err := ParseQueryWith("running*", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Prefixes) != 1 || q.Prefixes[0] != "running" {
		t.Fatalf("prefix must not stem: %v", q.Prefixes)
	}

	car, err := ParseQueryWith("car*", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(car.Prefixes) != 1 || car.Prefixes[0] != "car" {
		t.Fatalf("prefix %v", car.Prefixes)
	}
	if len(car.Terms) != 0 {
		t.Fatalf("prefix must not expand synonyms: %v", car.Terms)
	}

	the, err := ParseQueryWith("the*", EnglishV3)
	if err != nil {
		t.Fatal(err)
	}
	if len(the.Prefixes) != 1 || the.Prefixes[0] != "the" {
		t.Fatalf("prefix must not drop stops: %v", the.Prefixes)
	}

	fr, err := ParseQueryWith("l'hom*", French)
	if err != nil {
		t.Fatal(err)
	}
	if len(fr.Prefixes) != 1 || fr.Prefixes[0] != "hom" {
		t.Fatalf("french prefix elision: %v", fr.Prefixes)
	}
}

func TestQueryMatchesPrefix(t *testing.T) {
	q, err := ParseQuery("cat*")
	if err != nil {
		t.Fatal(err)
	}
	if !QueryMatches(q, map[string]uint32{"cat": 1}, map[string][]uint32{"cat": {0}}) {
		t.Fatal("exact cat")
	}
	if !QueryMatches(q, map[string]uint32{"catalog": 1}, map[string][]uint32{"catalog": {0}}) {
		t.Fatal("catalog")
	}
	if QueryMatches(q, map[string]uint32{"dog": 1}, map[string][]uint32{"dog": {0}}) {
		t.Fatal("unrelated")
	}

	exact, err := ParseQuery("cat")
	if err != nil {
		t.Fatal(err)
	}
	if QueryMatches(exact, map[string]uint32{"catalog": 1}, map[string][]uint32{"catalog": {0}}) {
		t.Fatal("exact cat must not match catalog")
	}

	and, err := ParseQuery("cat* dog")
	if err != nil {
		t.Fatal(err)
	}
	if !QueryMatches(and, map[string]uint32{"catalog": 1, "dog": 1}, map[string][]uint32{"catalog": {0}, "dog": {1}}) {
		t.Fatal("and")
	}
	if QueryMatches(and, map[string]uint32{"catalog": 1}, map[string][]uint32{"catalog": {0}}) {
		t.Fatal("and missing dog")
	}
}

func TestPrefixExpanderFailClosed(t *testing.T) {
	e := NewPrefixExpander(nil)
	for i := 0; i < MaxQueryExpansions; i++ {
		if err := e.Observe(fmt.Sprintf("t%03d", i)); err != nil {
			t.Fatalf("term %d: %v", i, err)
		}
	}
	if err := e.Observe("overflow-term"); err == nil {
		t.Fatal("term cap")
	}
	dup := NewPrefixExpander([]string{"cat"})
	if err := dup.Observe("cat"); err != nil {
		t.Fatal(err)
	}
}

func TestPostingPrefixBounds(t *testing.T) {
	start, end := PostingPrefixBounds("cat")
	in := func(k []byte) bool {
		if bytes.Compare(k, start) < 0 {
			return false
		}
		if end != nil && bytes.Compare(k, end) >= 0 {
			return false
		}
		return true
	}
	pk := []byte{1}
	if !in(PostingKey("cat", pk)) || !in(PostingKey("catalog", pk)) || !in(PostingKey("catch", pk)) {
		t.Fatal("expected prefix hits")
	}
	if in(PostingKey("ca", pk)) || in(PostingKey("dog", pk)) || in(PostingKey("bat", pk)) {
		t.Fatal("expected prefix misses")
	}
	if start[0] != kindPost {
		t.Fatalf("kind %x", start[0])
	}
}
