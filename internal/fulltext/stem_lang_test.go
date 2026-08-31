package fulltext

import "testing"

func TestStemFrenchFixtures(t *testing.T) {
	pairs := map[string]string{
		"continu":      "continu",
		"continua":     "continu",
		"continuait":   "continu",
		"continuation": "continu",
		"continue":     "continu",
		"continuer":    "continu",
		"continuez":    "continu",
		"main":         "main",
		"mains":        "main",
		"maintenaient": "mainten",
		"maintenait":   "mainten",
		"maintenir":    "mainten",
		"maison":       "maison",
		"maisons":      "maison",
		"majesté":      "majest",
		"malade":       "malad",
		"malades":      "malad",
		"l'homme":      "homm",
		"chevaux":      "cheval",
		"jouer":        "jou",
		"fameusement":  "fameux",
	}
	for in, want := range pairs {
		if got := stemFrench(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestStemGermanFixtures(t *testing.T) {
	pairs := map[string]string{
		"kaufen":       "kauf",
		"kaufe":        "kauf",
		"kaufst":       "kauf",
		"katze":        "katz",
		"katzen":       "katz",
		"kategorie":    "kategori",
		"kategorien":   "kategori",
		"kategorisch":  "kategor",
		"aufenthalt":   "aufenthalt",
		"auferstehung": "aufersteh",
		"auffassung":   "auffass",
		"auffaßt":      "auffasst",
		"kater":        "kat",
	}
	for in, want := range pairs {
		if got := stemGerman(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestStemSpanishFixtures(t *testing.T) {
	pairs := map[string]string{
		"checa":        "chec",
		"checar":       "chec",
		"chica":        "chic",
		"chicas":       "chic",
		"chicago":      "chicag",
		"chicharrones": "chicharron",
		"torá":         "tor",
		"total":        "total",
		"totales":      "total",
		"trabajaba":    "trabaj",
		"trabajador":   "trabaj",
		"trabajando":   "trabaj",
		"trabajadores": "trabaj",
	}
	for in, want := range pairs {
		if got := stemSpanish(in); got != want {
			t.Errorf("%q: got %q want %q", in, got, want)
		}
	}
}

func TestStemLanguageDeterministic(t *testing.T) {
	for _, fn := range []struct {
		name string
		stem func(string) string
		in   string
	}{
		{"fr", stemFrench, "continuation"},
		{"de", stemGerman, "kategorien"},
		{"es", stemSpanish, "trabajando"},
	} {
		a := fn.stem(fn.in)
		b := fn.stem(fn.in)
		if a != b {
			t.Fatalf("%s nondeterministic %q: %q %q", fn.name, fn.in, a, b)
		}
	}
}

func TestAnalyzeFrenchStopsThenStems(t *testing.T) {
	doc, err := AnalyzeWith("les chevaux dans la maison", French)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]uint32{}
	for _, tp := range doc.Terms {
		got[tp.Term] = tp.TF
	}
	if _, ok := got["les"]; ok {
		t.Fatalf("les should be dropped: %+v", doc.Terms)
	}
	if _, ok := got["dans"]; ok {
		t.Fatalf("dans should be dropped: %+v", doc.Terms)
	}
	if got["cheval"] != 1 {
		t.Fatalf("cheval %+v", doc.Terms)
	}
	if got["maison"] != 1 {
		t.Fatalf("maison %+v", doc.Terms)
	}
}

func TestAnalyzeGermanStopsThenStems(t *testing.T) {
	doc, err := AnalyzeWith("die katzen auf dem mat", German)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]struct{}{}
	for _, tp := range doc.Terms {
		got[tp.Term] = struct{}{}
	}
	for _, stop := range []string{"die", "auf", "dem"} {
		if _, ok := got[stop]; ok {
			t.Fatalf("%s should be dropped: %+v", stop, doc.Terms)
		}
	}
	if _, ok := got["katz"]; !ok {
		t.Fatalf("katz %+v", doc.Terms)
	}
}

func TestAnalyzeSpanishStopsThenStems(t *testing.T) {
	doc, err := AnalyzeWith("los trabajadores en la casa", Spanish)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]struct{}{}
	for _, tp := range doc.Terms {
		got[tp.Term] = struct{}{}
	}
	for _, stop := range []string{"los", "en", "la"} {
		if _, ok := got[stop]; ok {
			t.Fatalf("%s should be dropped: %+v", stop, doc.Terms)
		}
	}
	if _, ok := got["trabaj"]; !ok {
		t.Fatalf("trabaj %+v", doc.Terms)
	}
}

func TestParseQueryFrenchElision(t *testing.T) {
	q, err := ParseQueryWith("l'homme", French)
	if err != nil {
		t.Fatal(err)
	}
	if len(q.Terms) != 1 {
		t.Fatalf("terms %v", q.Terms)
	}
	doc, err := AnalyzeWith("l'homme", French)
	if err != nil {
		t.Fatal(err)
	}
	if len(doc.Terms) != 1 || doc.Terms[0].Term != q.Terms[0] {
		t.Fatalf("index %v query %v", doc.Terms, q.Terms)
	}
}

func TestLookupLanguageAnalyzers(t *testing.T) {
	for _, name := range []string{"french", "FRENCH", "german", "spanish"} {
		a, err := LookupAnalyzer(name)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := a.Validate(); err != nil {
			t.Fatalf("%s validate: %v", name, err)
		}
		if a.Name() == "" || a.Name() == "simple" {
			t.Fatalf("%s name %q", name, a.Name())
		}
	}
	if _, err := LookupAnalyzer("klingon"); err == nil {
		t.Fatal("unknown")
	}
	if err := (Analyzer{ID: AnalyzerFrench, Version: 9}).Validate(); err == nil {
		t.Fatal("bad french version")
	}
	if LookupAnalyzerMust := func() Analyzer {
		a, _ := LookupAnalyzer("french")
		return a
	}(); LookupAnalyzerMust != French {
		t.Fatalf("french current %+v", LookupAnalyzerMust)
	}
}
