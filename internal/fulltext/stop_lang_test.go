package fulltext

import "testing"

func TestFrenchStopV1Membership(t *testing.T) {
	if len(frenchStopV1) < 100 {
		t.Fatalf("dictionary size %d", len(frenchStopV1))
	}
	for _, w := range []string{"le", "la", "les", "de", "et", "à", "dans", "une"} {
		if !isFrenchStopV1(w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, w := range []string{"cheval", "maison", "homme", "son", "est"} {
		if isFrenchStopV1(w) {
			t.Errorf("unexpected %q", w)
		}
	}
}

func TestGermanStopV1Membership(t *testing.T) {
	if len(germanStopV1) < 100 {
		t.Fatalf("dictionary size %d", len(germanStopV1))
	}
	for _, w := range []string{"der", "die", "das", "und", "in", "nicht", "für"} {
		if !isGermanStopV1(w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, w := range []string{"katze", "kaufen", "haus"} {
		if isGermanStopV1(w) {
			t.Errorf("unexpected %q", w)
		}
	}
}

func TestSpanishStopV1Membership(t *testing.T) {
	if len(spanishStopV1) < 200 {
		t.Fatalf("dictionary size %d", len(spanishStopV1))
	}
	for _, w := range []string{"de", "la", "el", "en", "y", "los", "una", "está"} {
		if !isSpanishStopV1(w) {
			t.Errorf("missing %q", w)
		}
	}
	for _, w := range []string{"casa", "trabajar", "gato"} {
		if isSpanishStopV1(w) {
			t.Errorf("unexpected %q", w)
		}
	}
}
