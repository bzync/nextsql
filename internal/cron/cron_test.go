package cron

import (
	"testing"
	"time"
)

func mustParse(t *testing.T, expr string) *Expr {
	t.Helper()
	e, err := Parse(expr)
	if err != nil {
		t.Fatalf("Parse(%q): %v", expr, err)
	}
	return e
}

func TestParseRejectsMalformed(t *testing.T) {
	bad := []string{
		"",
		"* * * *",      // 4 fields
		"* * * * * *",  // 6 fields
		"60 * * * *",   // minute out of range
		"* 24 * * *",   // hour out of range
		"* * 0 * *",    // day-of-month below 1
		"* * 32 * *",   // day-of-month above 31
		"* * * 13 *",   // month above 12
		"* * * * 8",    // day-of-week above 7
		"*/0 * * * *",  // zero step
		"*/61 * * * *", // step wider than the field
		"5-1 * * * *",  // descending range
		"5/2 * * * *",  // step without a range
		"1,,2 * * * *", // empty list term
		"a * * * *",    // non-numeric
		"1-b * * * *",  // non-numeric range bound
		"* * * * 1-7",  // range containing the 7 alias
	}
	for _, expr := range bad {
		if _, err := Parse(expr); err == nil {
			t.Errorf("Parse(%q) = nil error, want error", expr)
		}
	}
}

func TestParseAcceptsAndCanonicalizes(t *testing.T) {
	e := mustParse(t, "  5,10-20/5   *  1 * 7 ")
	if got := e.String(); got != "5,10-20/5 * 1 * 7" {
		t.Fatalf("String() = %q", got)
	}
	// Re-parsing the canonical form must succeed and stay stable.
	again := mustParse(t, e.String())
	if again.String() != e.String() {
		t.Fatalf("round-trip drift: %q -> %q", e.String(), again.String())
	}
}

func at(t *testing.T, s string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	return ts.UTC()
}

func TestNext(t *testing.T) {
	cases := []struct {
		expr string
		from string
		want string
	}{
		// Every minute: strictly after, truncated to the minute.
		{"* * * * *", "2026-03-01T10:00:30Z", "2026-03-01T10:01:00Z"},
		{"* * * * *", "2026-03-01T10:00:00Z", "2026-03-01T10:01:00Z"},
		// Top of every hour.
		{"0 * * * *", "2026-03-01T10:00:00Z", "2026-03-01T11:00:00Z"},
		{"0 * * * *", "2026-03-01T10:30:00Z", "2026-03-01T11:00:00Z"},
		// 03:30 every day.
		{"30 3 * * *", "2026-03-01T04:00:00Z", "2026-03-02T03:30:00Z"},
		{"30 3 * * *", "2026-03-01T03:00:00Z", "2026-03-01T03:30:00Z"},
		// Step minutes.
		{"*/15 * * * *", "2026-03-01T10:07:00Z", "2026-03-01T10:15:00Z"},
		{"*/15 * * * *", "2026-03-01T10:45:00Z", "2026-03-01T11:00:00Z"},
		// Day-of-month only.
		{"0 0 1 * *", "2026-03-02T00:00:00Z", "2026-04-01T00:00:00Z"},
		// Month boundary: Feb 28 2026 (not a leap year) -> Mar 1.
		{"0 0 * * *", "2026-02-28T12:00:00Z", "2026-03-01T00:00:00Z"},
		// Leap year: Feb 29 2028 exists.
		{"0 0 29 2 *", "2027-01-01T00:00:00Z", "2028-02-29T00:00:00Z"},
		// Day-of-week: 2026-03-02 is a Monday.
		{"0 9 * * 1", "2026-03-01T00:00:00Z", "2026-03-02T09:00:00Z"},
		// Sunday via 0 and via 7 are equivalent; 2026-03-01 is a Sunday.
		{"0 12 * * 0", "2026-03-01T00:00:00Z", "2026-03-01T12:00:00Z"},
		{"0 12 * * 7", "2026-03-01T00:00:00Z", "2026-03-01T12:00:00Z"},
		// Both DOM and DOW restricted -> OR. The 15th OR any Monday.
		// From Wed 2026-03-04, the next Monday is 2026-03-09, before the 15th.
		{"0 0 15 * 1", "2026-03-04T00:00:00Z", "2026-03-09T00:00:00Z"},
	}
	for _, c := range cases {
		e := mustParse(t, c.expr)
		got, err := e.Next(at(t, c.from))
		if err != nil {
			t.Errorf("%q.Next(%s): %v", c.expr, c.from, err)
			continue
		}
		if want := at(t, c.want); !got.Equal(want) {
			t.Errorf("%q.Next(%s) = %s, want %s", c.expr, c.from, got.Format(time.RFC3339), c.want)
		}
	}
}

func TestNextIsDeterministicAndStrictlyIncreasing(t *testing.T) {
	e := mustParse(t, "*/7 2,14 * * *")
	cur := at(t, "2026-01-01T00:00:00Z")
	for i := 0; i < 200; i++ {
		nxt, err := e.Next(cur)
		if err != nil {
			t.Fatalf("iteration %d: %v", i, err)
		}
		if !nxt.After(cur) {
			t.Fatalf("iteration %d: Next did not advance (%s -> %s)", i, cur, nxt)
		}
		if nxt.Second() != 0 || nxt.Nanosecond() != 0 {
			t.Fatalf("iteration %d: Next not minute-aligned: %s", i, nxt)
		}
		// Recomputing from the same input yields the same output.
		again, _ := e.Next(cur)
		if !again.Equal(nxt) {
			t.Fatalf("iteration %d: Next not deterministic", i)
		}
		cur = nxt
	}
}

func TestNextUnsatisfiableFailsClosed(t *testing.T) {
	// 30 February never occurs.
	e := mustParse(t, "0 0 30 2 *")
	if _, err := e.Next(at(t, "2026-01-01T00:00:00Z")); err == nil {
		t.Fatal("expected an error for an unsatisfiable expression")
	}
}

func TestNextNilExpr(t *testing.T) {
	var e *Expr
	if _, err := e.Next(time.Now()); err == nil {
		t.Fatal("nil expr should error")
	}
	if e.String() != "" {
		t.Fatal("nil expr String should be empty")
	}
}

func FuzzParse(f *testing.F) {
	for _, s := range []string{"* * * * *", "0 3 * * 1-5", "*/15 0,12 1 * *", "bad", "0 0 30 2 *"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, expr string) {
		e, err := Parse(expr)
		if err != nil {
			return
		}
		// A parsed expression must round-trip through its canonical form.
		again, err := Parse(e.String())
		if err != nil {
			t.Fatalf("canonical form %q failed to re-parse: %v", e.String(), err)
		}
		if again.String() != e.String() {
			t.Fatalf("canonical form not stable: %q -> %q", e.String(), again.String())
		}
		// Next must never panic and must respect the strictly-after and
		// minute-alignment contract when it succeeds.
		from := time.Unix(1_700_000_000, 0).UTC()
		if nxt, err := e.Next(from); err == nil {
			if !nxt.After(from) || nxt.Second() != 0 || nxt.Nanosecond() != 0 {
				t.Fatalf("Next(%s) = %s violates contract", from, nxt)
			}
		}
	})
}
