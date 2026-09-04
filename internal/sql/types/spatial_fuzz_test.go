package types

import "testing"

// FuzzParseGeneralWKT feeds arbitrary text to the WKT/EWKT parser (an
// untrusted decoder per SKILLS.md). A successful parse must round-trip
// through EWKB and the heap-row / sortable-key codecs without panicking.
func FuzzParseGeneralWKT(f *testing.F) {
	for _, s := range []string{
		"POINT(1 2)", "SRID=4326;POINT(1 2)",
		"LINESTRING(0 0, 1 1)", "POLYGON((0 0, 1 0, 1 1, 0 0))",
		"MULTIPOINT((0 0), (1 1))", "MULTIPOINT(0 0, 1 1)",
		"MULTILINESTRING((0 0, 1 1))", "MULTIPOLYGON(((0 0, 1 0, 1 1, 0 0)))",
		"GEOMETRYCOLLECTION(POINT(1 2), LINESTRING(0 0, 1 1))",
		"GEOMETRYCOLLECTION EMPTY", "POINT()", "POLYGON(())", "SRID=;POINT(1 2)",
		"POINT(1 2 3)", "((((",
		"SRID=99999;POINT(1 2)", "SRID=65535;POINT(1 2)", "SRID=-1;POINT(1 2)",
		"SRID=4294967296;POINT(1 2)",
	} {
		f.Add(s)
	}
	gt, _ := GeometryType(0, 4326)
	f.Fuzz(func(t *testing.T, src string) {
		g, err := ParseGeneralWKT(src, 4326)
		if err != nil {
			return
		}
		ewkb, err := EncodeEWKB(g)
		if err != nil {
			t.Fatalf("EncodeEWKB after successful parse: %v", err)
		}
		if _, _, err := DecodeEWKB(ewkb); err != nil {
			t.Fatalf("EWKB does not round-trip: %v", err)
		}
		val := Value{Typ: gt, Geom: g}
		raw, err := EncodeRow([]Value{val})
		if err != nil {
			t.Fatalf("EncodeRow: %v", err)
		}
		got, err := DecodeRow(raw, []Type{gt})
		if err != nil {
			t.Fatalf("DecodeRow: %v", err)
		}
		if c, err := got[0].Cmp(val); err != nil || c != 0 {
			t.Fatalf("heap round trip mismatch: %v", err)
		}
		key, err := EncodeKey([]Value{val})
		if err != nil {
			t.Fatalf("EncodeKey: %v", err)
		}
		if _, err := DecodeKey(key, []Type{gt}); err != nil {
			t.Fatalf("DecodeKey: %v", err)
		}
	})
}

// FuzzDecodeEWKB feeds arbitrary bytes to the EWKB decoder.
func FuzzDecodeEWKB(f *testing.F) {
	seed := func(wkt string) {
		if g, err := ParseGeneralWKT(wkt, 4326); err == nil {
			if b, err := EncodeEWKB(g); err == nil {
				f.Add(b)
			}
		}
	}
	seed("POINT(1 2)")
	seed("POLYGON((0 0, 1 0, 1 1, 0 0))")
	seed("GEOMETRYCOLLECTION(POINT(1 2), MULTIPOLYGON(((0 0, 1 0, 1 1, 0 0))))")
	f.Add([]byte{0x01, 0x01, 0, 0, 0})
	f.Fuzz(func(t *testing.T, raw []byte) {
		g, n, err := DecodeEWKB(raw)
		if err != nil {
			return
		}
		if n > len(raw) {
			t.Fatalf("consumed %d of %d bytes", n, len(raw))
		}
		// A decoded geometry must re-encode without panicking.
		if _, err := EncodeEWKB(g); err != nil {
			t.Fatalf("re-encode after decode: %v", err)
		}
	})
}

// FuzzParseGeoJSON feeds arbitrary text to the GeoJSON decoder.
func FuzzParseGeoJSON(f *testing.F) {
	for _, s := range []string{
		`{"type":"Point","coordinates":[1,2]}`,
		`{"type":"LineString","coordinates":[[0,0],[1,1]]}`,
		`{"type":"Polygon","coordinates":[[[0,0],[1,0],[1,1],[0,0]]]}`,
		`{"type":"MultiPoint","coordinates":[[0,0],[1,1]]}`,
		`{"type":"GeometryCollection","geometries":[{"type":"Point","coordinates":[1,2]}]}`,
		`{}`, `{"type":"Bogus"}`, `not json`, `null`, `[]`,
		`{"type":"Point","coordinates":[1]}`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, src string) {
		g, err := ParseGeoJSON([]byte(src), 4326)
		if err != nil {
			return
		}
		if _, err := EncodeEWKB(g); err != nil {
			t.Fatalf("EncodeEWKB after successful GeoJSON parse: %v", err)
		}
		if _, err := GeomToGeoJSON(g); err != nil {
			t.Fatalf("GeomToGeoJSON round trip: %v", err)
		}
	})
}
