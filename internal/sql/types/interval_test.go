package types

import (
	"bytes"
	"testing"
)

func TestIntervalParseFormat(t *testing.T) {
	cases := []struct {
		in     string
		months int32
		days   int32
		nanos  int64
	}{
		{"1 year", 12, 0, 0},
		{"1 year 2 months", 14, 0, 0},
		{"3 days", 0, 3, 0},
		{"1 year 2 months 3 days", 14, 3, 0},
		{"4 hours", 0, 0, 4 * int64(3600e9)},
		{"1 hour 2 minutes 3 seconds", 0, 0, int64(3600e9) + 2*int64(60e9) + 3*int64(1e9)},
		{"0.5 seconds", 0, 0, 500_000_000},
		{"-1 month", -1, 0, 0},
		{"-3 days", 0, -3, 0},
	}
	for _, c := range cases {
		v, err := ParseInterval(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if v.Typ.Kind != KindInterval || v.IntervalMonths != c.months || v.IntervalDays != c.days || v.Time != c.nanos {
			t.Fatalf("%q -> months=%d days=%d nanos=%d, want months=%d days=%d nanos=%d",
				c.in, v.IntervalMonths, v.IntervalDays, v.Time, c.months, c.days, c.nanos)
		}
	}
	// Fractional year/month/day is rejected (ambiguous, out of scope).
	if _, err := ParseInterval("1.5 months"); err == nil {
		t.Fatal("expected fractional month to be rejected")
	}
	// Sub-nanosecond precision is rejected, not rounded.
	if _, err := ParseInterval("0.0000000001 seconds"); err == nil {
		t.Fatal("expected sub-nanosecond precision to be rejected")
	}
	// Unknown unit rejected.
	if _, err := ParseInterval("1 fortnight"); err == nil {
		t.Fatal("expected unknown unit to be rejected")
	}
	// Odd number of fields rejected.
	if _, err := ParseInterval("1 year 2"); err == nil {
		t.Fatal("expected malformed interval to be rejected")
	}

	// Format round-trips through Coerce (isolated text coercion).
	v, _ := ParseInterval("1 year 2 months 3 days 4 hours 5 minutes 6.5 seconds")
	s := v.String()
	v2, err := Coerce(StringValue(s), Interval())
	if err != nil {
		t.Fatal(err)
	}
	if v2.IntervalMonths != v.IntervalMonths || v2.IntervalDays != v.IntervalDays || v2.Time != v.Time {
		t.Fatalf("format/reparse round trip: %q -> %+v, want %+v", s, v2, v)
	}
	if FormatInterval(0, 0, 0) != "0" {
		t.Fatalf("zero interval format: %q", FormatInterval(0, 0, 0))
	}
}

// TestIntervalCoerceIsolated covers CAST from/to text and isolation from
// every numeric family (docs/design-datatypes.md D6, D1-D8 precedent).
func TestIntervalCoerceIsolated(t *testing.T) {
	if _, err := Coerce(IntValue(KindInt32, 5), Interval()); err == nil {
		t.Fatal("expected INT->INTERVAL to be rejected")
	}
	if _, err := Coerce(DecimalValue(DecimalFromInt64(5), Type{Kind: KindDecimal}), Interval()); err == nil {
		t.Fatal("expected DECIMAL->INTERVAL to be rejected")
	}
	v, _ := ParseInterval("1 day")
	if _, err := Coerce(v, Type{Kind: KindDecimal}); err == nil {
		t.Fatal("expected INTERVAL->DECIMAL to be rejected")
	}
	s, err := Coerce(v, String())
	if err != nil || s.Str != "1 day" {
		t.Fatalf("interval->string: %+v %v", s, err)
	}
}

// TestIntervalCmpJustified covers Postgres's own "justified" comparison
// heuristic (1 month = 30 days = 24h) — the entire point of choosing
// Postgres-style storage (docs/design-datatypes.md D6): two intervals
// unequal in their raw fields can compare equal.
func TestIntervalCmpJustified(t *testing.T) {
	oneMonth, _ := ParseInterval("1 month")
	thirtyDays, _ := ParseInterval("30 days")
	if c, err := oneMonth.Cmp(thirtyDays); err != nil || c != 0 {
		t.Fatalf("expected 1 month == 30 days: %d %v", c, err)
	}
	twentyNineDays, _ := ParseInterval("29 days")
	if c, err := twentyNineDays.Cmp(oneMonth); err != nil || c >= 0 {
		t.Fatalf("expected 29 days < 1 month: %d %v", c, err)
	}
	oneDay, _ := ParseInterval("24 hours")
	oneDayLit, _ := ParseInterval("1 day")
	if c, err := oneDay.Cmp(oneDayLit); err != nil || c != 0 {
		t.Fatalf("expected 24 hours == 1 day: %d %v", c, err)
	}
	neg, _ := ParseInterval("-1 day")
	pos, _ := ParseInterval("1 day")
	if c, err := neg.Cmp(pos); err != nil || c >= 0 {
		t.Fatalf("expected -1 day < 1 day: %d %v", c, err)
	}
}

// TestIntervalRowAndKeyRoundTrip covers heap-row (16-byte exact) and
// sortable-key (8-byte justified, canonicalizing) encode/decode.
func TestIntervalRowAndKeyRoundTrip(t *testing.T) {
	a, _ := ParseInterval("1 year 2 months 3 days 4 hours")
	vals := []Value{a, Null(Interval())}
	enc, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeRow(enc, []Type{Interval(), Interval()})
	if err != nil {
		t.Fatal(err)
	}
	// Heap-row round trip is exact: the original (months, days, nanos), not
	// the justified canonicalization.
	if got[0].IntervalMonths != a.IntervalMonths || got[0].IntervalDays != a.IntervalDays || got[0].Time != a.Time {
		t.Fatalf("interval row round trip: %+v want %+v", got[0], a)
	}
	if !got[1].Null {
		t.Fatal("expected null interval")
	}

	small, _ := ParseInterval("1 day")
	big, _ := ParseInterval("1 month")
	ksmall, _ := EncodeKey([]Value{small})
	kbig, _ := EncodeKey([]Value{big})
	if bytes.Compare(ksmall, kbig) >= 0 {
		t.Fatal("expected 1 day's key to sort before 1 month's (30 days), by justified order")
	}
	// Sortable-key decode canonicalizes to (0 months, N days, remainder
	// nanos) — same justified total, not necessarily the same raw fields
	// (documented deliberate limitation, like FLOAT's -0.0 -> +0.0).
	kgot, err := DecodeKey(kbig, []Type{Interval()})
	if err != nil {
		t.Fatal(err)
	}
	if kgot[0].IntervalMonths != 0 || kgot[0].IntervalDays != 30 || kgot[0].Time != 0 {
		t.Fatalf("interval key round trip canonicalization: %+v", kgot[0])
	}
	cmp, err := kgot[0].Cmp(big)
	if err != nil || cmp != 0 {
		t.Fatalf("canonicalized key value must still compare equal to the original: %d %v", cmp, err)
	}
}

func TestIntervalTypeString(t *testing.T) {
	if Interval().String() != "INTERVAL" {
		t.Fatalf("Interval().String() = %q", Interval().String())
	}
}
