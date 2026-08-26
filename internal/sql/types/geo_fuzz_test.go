package types

import "testing"

func FuzzParseWKT(f *testing.F) {
	seeds := []string{
		"POINT(-73.98 40.75)",
		"BOX(-74 40, -73 41)",
		"LINESTRING(-74 40, -73 41)",
		"POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40))",
		"POLYGON((-74 40, -73 40, -73 41, -74 41, -74 40), (-73.9 40.2, -73.8 40.2, -73.8 40.3, -73.9 40.3, -73.9 40.2))",
		"POINT(200 0)",
		"LINESTRING(1)",
		"POLYGON((0 0, 1 0))",
		"POINT",
		"(",
		string([]byte{0, 1, 255}),
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		v, err := ParseWKT(src)
		if err != nil {
			return
		}
		raw, err := EncodeRow([]Value{v})
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeRow(raw, []Type{v.Typ})
		if err != nil {
			t.Fatal(err)
		}
		if cmp, err := got[0].Cmp(v); err != nil || cmp != 0 {
			t.Fatalf("round trip %v %v", got[0], err)
		}
	})
}
