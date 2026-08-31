package fulltext

import "testing"

func FuzzTokenize(f *testing.F) {
	for _, s := range []string{
		"",
		"database performance",
		"CAFÉ",
		`"noise cancelling"`,
		"cat*",
		"*cat",
		"a*",
		`"data* performance"`,
		"*",
		"cat~",
		"cat~1",
		"cat~2",
		"cat~3",
		"~cat",
		"c~t",
		`"databas~ performance"`,
		"~",
		"cat*~",
		"cat~*",
		"a\x00b",
		string([]byte{0, 1, 255}),
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		toks := Tokenize(s)
		if len(toks) > MaxDocTokens {
			t.Fatalf("cap %d", len(toks))
		}
		for _, tok := range toks {
			if tok.Term == "" || len(tok.Term) > MaxTermRunes*4 {
				t.Fatalf("%+v", tok)
			}
			if tok.Start < 0 || tok.End > len(s) || tok.Start > tok.End {
				t.Fatalf("span %+v len=%d", tok, len(s))
			}
		}
		if q, err := ParseQuery(s); err == nil {
			_, _ = Highlight(s, q, Simple, DefaultHighlightPre, DefaultHighlightPost)
			_, _ = Snippet(s, q, Simple, DefaultSnippetRunes, DefaultHighlightPre, DefaultHighlightPost)
		}
		_, _ = ParseQuery(s)
		_, _ = Analyze(s)
		_, _ = ParseQueryWith(s, EnglishV1)
		_, _ = AnalyzeWith(s, EnglishV1)
		_, _ = ParseQueryWith(s, EnglishV2)
		_, _ = AnalyzeWith(s, EnglishV2)
		_, _ = ParseQueryWith(s, EnglishV3)
		_, _ = AnalyzeWith(s, EnglishV3)
		_ = stemEnglish(s)
	})
}

func FuzzDecodePosting(f *testing.F) {
	f.Add(EncodePosting(1, []uint32{0, 2}))
	f.Add([]byte{})
	f.Add([]byte{1, 0, 0, 0, 1, 0, 0, 0, 1})
	f.Fuzz(func(t *testing.T, raw []byte) {
		tf, pos, err := DecodePosting(raw)
		if err != nil {
			return
		}
		enc := EncodePosting(tf, pos)
		tf2, pos2, err := DecodePosting(enc)
		if err != nil || tf2 != tf || len(pos2) != len(pos) {
			t.Fatalf("roundtrip %d %v", tf, err)
		}
	})
}
