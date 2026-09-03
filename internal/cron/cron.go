// Package cron parses standard 5-field cron expressions and computes the
// next matching minute in UTC.
//
// It is deliberately minimal — the smallest surface that covers real
// scheduling needs without the ambiguous corners of full Vixie cron:
//
//   - Five fields: minute hour day-of-month month day-of-week.
//   - Numeric values only. No month/weekday names, no "@hourly" macros,
//     no seconds field, no "L"/"W"/"#" qualifiers.
//   - Per field: "*", a single value, an inclusive range "a-b", a
//     comma-separated list of those, and a step "*/n" or "a-b/n".
//   - Day-of-week is 0-6 with Sunday = 0; a bare "7" is also accepted for
//     Sunday.
//   - When both day-of-month and day-of-week are restricted (neither is a
//     bare "*"), a day matches if EITHER field matches, following
//     Vixie-cron semantics. When only one is restricted, only that one
//     applies.
//   - All evaluation is in UTC, matching the rest of the engine's
//     canonical-UTC timestamp handling.
package cron

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// MaxExprBytes bounds a stored cron expression. A realistic expression is a
// few dozen bytes; the cap only rejects pathological fully-enumerated
// lists, and keeps the persisted schedule descriptor bounded.
const MaxExprBytes = 256

// searchHorizon bounds Next's forward scan so an unsatisfiable expression
// (for example "0 0 30 2 *" — the 30th of February) fails closed instead
// of scanning forever.
const searchHorizon = 5 * 366 * 24 * time.Hour

type field struct {
	mask uint64 // bit v set => value v matches
	star bool   // field text was exactly "*"
}

// Expr is a parsed, validated cron expression. The zero value is not
// usable; obtain one from Parse.
type Expr struct {
	minute field
	hour   field
	dom    field
	month  field
	dow    field
	src    string
}

// String returns the canonical single-space-separated form of the
// expression, suitable for durable storage and round-tripping through
// Parse.
func (e *Expr) String() string {
	if e == nil {
		return ""
	}
	return e.src
}

var fieldBounds = [5]struct{ lo, hi int }{
	{0, 59}, // minute
	{0, 23}, // hour
	{1, 31}, // day of month
	{1, 12}, // month
	{0, 6},  // day of week (Sunday = 0)
}

// Parse validates and compiles a 5-field cron expression. It returns an
// error for any malformed field, out-of-range value, or empty match set.
func Parse(expr string) (*Expr, error) {
	if len(expr) > MaxExprBytes {
		return nil, fmt.Errorf("cron: expression exceeds %d bytes", MaxExprBytes)
	}
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return nil, fmt.Errorf("cron: expected 5 fields, got %d", len(parts))
	}
	var fields [5]field
	for i, p := range parts {
		f, err := parseField(p, fieldBounds[i].lo, fieldBounds[i].hi, i == 4)
		if err != nil {
			return nil, err
		}
		fields[i] = f
	}
	return &Expr{
		minute: fields[0],
		hour:   fields[1],
		dom:    fields[2],
		month:  fields[3],
		dow:    fields[4],
		src:    strings.Join(parts, " "),
	}, nil
}

func parseField(text string, lo, hi int, isDOW bool) (field, error) {
	f := field{star: text == "*"}
	for _, term := range strings.Split(text, ",") {
		if term == "" {
			return field{}, fmt.Errorf("cron: empty term in %q", text)
		}
		rangePart := term
		step := 1
		if slash := strings.IndexByte(term, '/'); slash >= 0 {
			rangePart = term[:slash]
			s, err := strconv.Atoi(term[slash+1:])
			if err != nil || s < 1 || s > hi-lo+1 {
				return field{}, fmt.Errorf("cron: invalid step in %q", term)
			}
			step = s
		}
		start, end, err := parseRange(rangePart, term, lo, hi, isDOW, step != 1)
		if err != nil {
			return field{}, err
		}
		if start < lo || end > hi || start > end {
			return field{}, fmt.Errorf("cron: term %q out of range %d-%d", term, lo, hi)
		}
		for v := start; v <= end; v += step {
			f.mask |= 1 << uint(v)
		}
	}
	if f.mask == 0 {
		return field{}, fmt.Errorf("cron: field %q matches nothing", text)
	}
	return f, nil
}

func parseRange(rangePart, term string, lo, hi int, isDOW, hasStep bool) (int, int, error) {
	switch {
	case rangePart == "*":
		return lo, hi, nil
	case strings.IndexByte(rangePart, '-') >= 0:
		d := strings.IndexByte(rangePart, '-')
		a, errA := strconv.Atoi(rangePart[:d])
		b, errB := strconv.Atoi(rangePart[d+1:])
		if errA != nil || errB != nil {
			return 0, 0, fmt.Errorf("cron: invalid range in %q", term)
		}
		return a, b, nil
	default:
		if hasStep {
			// "n/step" is not standard; a step needs "*" or a range.
			return 0, 0, fmt.Errorf("cron: step requires * or a range in %q", term)
		}
		v, err := strconv.Atoi(rangePart)
		if err != nil {
			return 0, 0, fmt.Errorf("cron: invalid value in %q", term)
		}
		if isDOW && v == 7 {
			v = 0
		}
		return v, v, nil
	}
}

// Next returns the earliest whole minute strictly after t whose UTC
// wall-clock value matches e. It returns an error only when no match
// exists within a bounded horizon (an unsatisfiable expression).
func (e *Expr) Next(t time.Time) (time.Time, error) {
	if e == nil {
		return time.Time{}, fmt.Errorf("cron: nil expression")
	}
	t = t.UTC()
	next := time.Date(t.Year(), t.Month(), t.Day(), t.Hour(), t.Minute(), 0, 0, time.UTC).Add(time.Minute)
	limit := t.Add(searchHorizon)
	for !next.After(limit) {
		if e.matches(next) {
			return next, nil
		}
		next = next.Add(time.Minute)
	}
	return time.Time{}, fmt.Errorf("cron: %q has no match within %s of %s", e.src, searchHorizon, t.Format(time.RFC3339))
}

func (e *Expr) matches(t time.Time) bool {
	if e.minute.mask&(1<<uint(t.Minute())) == 0 {
		return false
	}
	if e.hour.mask&(1<<uint(t.Hour())) == 0 {
		return false
	}
	if e.month.mask&(1<<uint(int(t.Month()))) == 0 {
		return false
	}
	domHit := e.dom.mask&(1<<uint(t.Day())) != 0
	dowHit := e.dow.mask&(1<<uint(int(t.Weekday()))) != 0
	switch {
	case e.dom.star && e.dow.star:
		return true
	case e.dom.star:
		return dowHit
	case e.dow.star:
		return domHit
	default:
		return domHit || dowHit
	}
}
