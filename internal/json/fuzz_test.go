package json

import "testing"

func FuzzFromText(f *testing.F) {
	seeds := []string{
		`null`,
		`true`,
		`false`,
		`0`,
		`-12`,
		`1.5`,
		`1e-2`,
		`""`,
		`"abc"`,
		`"\u0041"`,
		`[]`,
		`[1,2,3]`,
		`{}`,
		`{"a":1}`,
		`{"category":"electronics","nested":{"x":[true,null]}}`,
		`{`,
		`[`,
		`"`,
		`01`,
		string([]byte{0, 1, 255}),
		`{"a":` + string(make([]byte, 64)),
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, src []byte) {
		doc, err := FromText(src)
		if err != nil {
			if doc != nil {
				t.Fatalf("error with non-nil doc")
			}
			return
		}
		if err := Validate(doc); err != nil {
			t.Fatalf("parse ok but validate failed: %v", err)
		}
		txt, err := ToText(doc)
		if err != nil {
			t.Fatalf("ToText: %v", err)
		}
		doc2, err := FromText(txt)
		if err != nil {
			t.Fatalf("reparse %q: %v", txt, err)
		}
		if err := Validate(doc2); err != nil {
			t.Fatalf("reparse validate: %v", err)
		}
		_, _ = Extract(doc, []string{"a"})
		_, _ = Extract(doc, []string{"a", "b", "0"})
	})
}

func FuzzValidate(f *testing.F) {
	good, err := FromText([]byte(`{"a":[1,true,null],"b":"x"}`))
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte("NSJB\x01"))
	f.Add([]byte("NSJB\x99xxxx"))
	f.Add([]byte{0, 1, 2, 3, 4, 5})
	f.Fuzz(func(t *testing.T, data []byte) {
		err := Validate(data)
		if err != nil {
			return
		}
		if _, err := ToText(data); err != nil {
			t.Fatalf("valid doc failed ToText: %v", err)
		}
		if _, err := Extract(data, []string{"x"}); err != nil {
			t.Fatalf("valid doc failed Extract: %v", err)
		}
	})
}
