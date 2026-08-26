package fulltext

import "testing"

func FuzzTokenize(f *testing.F) {
	for _, s := range []string{
		"",
		"database performance",
		"CAFÉ",
		`"noise cancelling"`,
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
		}
		_, _ = ParseQuery(s)
		_, _ = Analyze(s)
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
