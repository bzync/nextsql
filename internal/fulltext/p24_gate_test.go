package fulltext

import (
	"math"
	"testing"
)

func TestP24BM25PhraseCompatibilityGolden(t *testing.T) {
	// Phase-10 compatibility fixture. These constants intentionally do not
	// derive from Score so a change to the BM25 contract is visible here.
	const (
		wantIDF   = 0.4700036292457356
		wantShort = 0.5235483465015789
		wantLong  = 0.39019169220400696
	)
	idf := IDF(3, 2)
	if math.Abs(idf-wantIDF) > 1e-15 {
		t.Fatalf("IDF changed: %.17g", idf)
	}
	if got := Score(1, 3, 4, idf); math.Abs(got-wantShort) > 1e-15 {
		t.Fatalf("short-document score changed: %.17g", got)
	}
	if got := Score(1, 6, 4, idf); math.Abs(got-wantLong) > 1e-15 {
		t.Fatalf("long-document score changed: %.17g", got)
	}

	q, err := ParseQuery(`database "performance tuning"`)
	if err != nil {
		t.Fatal(err)
	}
	adjacent := map[string][]uint32{
		"database":    {0},
		"performance": {1},
		"tuning":      {2},
	}
	if !QueryMatches(q, map[string]uint32{"database": 1, "performance": 1, "tuning": 1}, adjacent) {
		t.Fatal("Phase-10 adjacent phrase no longer matches")
	}
	nonAdjacent := map[string][]uint32{
		"database":    {0},
		"performance": {1},
		"tuning":      {3},
	}
	if QueryMatches(q, map[string]uint32{"database": 1, "performance": 1, "tuning": 1}, nonAdjacent) {
		t.Fatal("Phase-10 non-adjacent phrase matched")
	}
}
