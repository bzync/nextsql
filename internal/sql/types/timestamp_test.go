package types

import (
	"bytes"
	"testing"
)

// TestNaiveTimestampParseFormat covers D7 (Datatype expansion track): plain
// TIMESTAMP parses ISO 8601 date+time with no offset, a bare date is midnight,
// and offset-carrying text is rejected.
func TestNaiveTimestampParseFormat(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"2024-01-15 13:45:00", "2024-01-15 13:45:00"},
		{"2024-01-15T13:45:00", "2024-01-15 13:45:00"},
		{"2024-01-15", "2024-01-15 00:00:00"},
		{"2024-01-15 13:45:00.5", "2024-01-15 13:45:00.5"},
		{"1969-12-31 23:59:59", "1969-12-31 23:59:59"},
	}
	for _, c := range cases {
		v, err := ParseNaiveTimestamp(c.in)
		if err != nil {
			t.Fatalf("ParseNaiveTimestamp(%q): %v", c.in, err)
		}
		if v.Typ.Kind != KindTimestamp {
			t.Fatalf("kind = %v", v.Typ.Kind)
		}
		if got := v.String(); got != c.want {
			t.Fatalf("%q -> %q, want %q", c.in, got, c.want)
		}
	}
	for _, bad := range []string{"2024-01-15T13:45:00Z", "2024-01-15 13:45:00+02:00", "not-a-ts", "2024-13-01 00:00:00"} {
		if _, err := ParseNaiveTimestamp(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}

// TestNaiveTimestampRowAndKeyRoundTrip covers heap-row + sortable-key
// encode/decode and the straddle-the-epoch ordering (sign-bit-flip).
func TestNaiveTimestampRowAndKeyRoundTrip(t *testing.T) {
	pre, _ := ParseNaiveTimestamp("1969-12-31 23:59:59")
	post, _ := ParseNaiveTimestamp("1970-01-01 00:00:01")

	enc, err := EncodeRow([]Value{pre, Null(Timestamp()), post})
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, []Type{Timestamp(), Timestamp(), Timestamp()})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Time != pre.Time || !got[1].Null || got[2].Time != post.Time {
		t.Fatalf("row round trip: %+v", got)
	}

	a, _ := EncodeKey([]Value{pre})
	b, _ := EncodeKey([]Value{post})
	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("pre-epoch timestamp must sort before post-epoch")
	}
	kgot, err := DecodeKey(a, []Type{Timestamp()})
	if err != nil || kgot[0].Time != pre.Time {
		t.Fatalf("key round trip: %+v %v", kgot, err)
	}
}

// TestNaiveTimestampIsolatedFromTZ: a plain TIMESTAMP does not implicitly
// convert to or from TIMESTAMPTZ (that needs an assumed zone) — text only.
func TestNaiveTimestampIsolatedFromTZ(t *testing.T) {
	ts, _ := ParseNaiveTimestamp("2024-01-15 13:45:00")
	if _, err := Coerce(ts, TimestampTZ()); err == nil {
		t.Fatal("TIMESTAMP -> TIMESTAMPTZ should be rejected")
	}
	tz := TimeValue(0)
	if _, err := Coerce(tz, Timestamp()); err == nil {
		t.Fatal("TIMESTAMPTZ -> TIMESTAMP should be rejected")
	}
	// Text both ways works.
	if _, err := Coerce(StringValue("2024-01-15 13:45:00"), Timestamp()); err != nil {
		t.Fatalf("text -> TIMESTAMP: %v", err)
	}
	s, err := Coerce(ts, String())
	if err != nil || s.Str != "2024-01-15 13:45:00" {
		t.Fatalf("TIMESTAMP -> text: %q %v", s.Str, err)
	}
}
