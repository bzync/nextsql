package types

import (
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/bitvec"
	"github.com/bzync/nextsql/internal/float16"
	"github.com/bzync/nextsql/internal/int8vec"
	nsjson "github.com/bzync/nextsql/internal/json"
	"github.com/bzync/nextsql/internal/nerr"
)

// Value is a runtime SQL value.
type Value struct {
	Typ       Type
	Null      bool
	UUID      [16]byte
	Str       string
	Dec       Decimal
	Int       int64   // INT8/16/32/64, sign-extended regardless of declared width
	Uint      uint64  // UINT8/16/32/64, zero-extended regardless of declared width
	Flt       float64 // FLOAT32/FLOAT64; F32 held at 32-bit precision, -0 canonicalized to +0
	Time      int64   // UTC unix nanoseconds
	JSON      []byte
	Vec       []float32
	VecRef    bool     // payload lives in the table vector store
	SparseIdx []uint32 // SPARSEVECTOR non-zero indices; unused for dense vectors
	SparseVal []float32
	Bool      bool
	Lon       float64    // POINT longitude
	Lat       float64    // POINT latitude
	Box       [4]float64 // BOX west, south, east, north
	Coords    []float64  // LINESTRING / POLYGON interleaved lon, lat
	Rings     []int      // POLYGON vertex counts per ring (includes closing vertex)
	// IntervalMonths/IntervalDays are INTERVAL's calendar components (D6);
	// the third component, nanoseconds-of-time, reuses Time above
	// (disambiguated by Typ.Kind, the same pattern DATE/TIME/TIMESTAMP use).
	IntervalMonths int32
	IntervalDays   int32
	// Coll / CollKeys hold a collection value's members (Collections track,
	// docs/design-collections.md): for STRUCT, Coll is the ordered field
	// values (one per Typ.Fields entry, in the same order); for ARRAY, Coll is
	// the elements; for MAP, Coll is the values and CollKeys the parallel
	// keys, both ordered by the key's canonical Cmp order. Each member is
	// itself a fully-typed Value, so collections nest.
	Coll     []Value
	CollKeys []Value
	// Geom holds a general GEOMETRY / GEOGRAPHY value (Spatial track,
	// docs/design-spatial.md). The four fixed WGS84 shapes keep using
	// Lon/Lat/Box/Coords/Rings above; this is only for KindGeometry /
	// KindGeography.
	Geom *Geom
}

func Null(t Type) Value { return Value{Typ: t, Null: true} }

func UUIDValue(u [16]byte) Value { return Value{Typ: UUID(), UUID: u} }

func StringValue(s string) Value { return Value{Typ: String(), Str: s} }

func TextValue(s string) Value { return Value{Typ: Text(), Str: s} }

func DecimalValue(d Decimal, t Type) Value { return Value{Typ: t, Dec: d} }

func TimeValue(ns int64) Value { return Value{Typ: TimestampTZ(), Time: ns} }

func JSONValue(b []byte) Value { return Value{Typ: JSON(), JSON: append([]byte(nil), b...)} }

// BlobValue holds raw bytes. Reuses the Str field (a Go string is an
// arbitrary byte sequence, not required to be valid UTF-8) rather than
// adding a dedicated field.
func BlobValue(b []byte) Value { return Value{Typ: Blob(), Str: string(b)} }

// IntValue reconstructs an already-range-validated fixed-width integer value.
// Callers (decode paths, Coerce) must ensure n fits kind's range first; use
// NewInt for a validating constructor from untrusted/computed input.
func IntValue(kind Kind, n int64) Value { return Value{Typ: Type{Kind: kind}, Int: n} }

// NewInt is the range-checked constructor for a fixed-width integer value.
func NewInt(kind Kind, n int64) (Value, error) {
	lo, hi, ok := IntRange(kind)
	if !ok {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.NewInt", "not an integer kind")
	}
	if n < lo || n > hi {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.NewInt", kind.String()+" out of range")
	}
	return IntValue(kind, n), nil
}

func Int8Value(n int8) Value   { return IntValue(KindInt8, int64(n)) }
func Int16Value(n int16) Value { return IntValue(KindInt16, int64(n)) }
func Int32Value(n int32) Value { return IntValue(KindInt32, int64(n)) }
func Int64Value(n int64) Value { return IntValue(KindInt64, n) }

// UintValue reconstructs an already-range-validated fixed-width unsigned
// integer value. Callers (decode paths, Coerce) must ensure n fits kind's
// range first; use NewUint for a validating constructor from
// untrusted/computed input.
func UintValue(kind Kind, n uint64) Value { return Value{Typ: Type{Kind: kind}, Uint: n} }

// NewUint is the range-checked constructor for a fixed-width unsigned
// integer value.
func NewUint(kind Kind, n uint64) (Value, error) {
	hi, ok := UintRange(kind)
	if !ok {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.NewUint", "not an unsigned integer kind")
	}
	if n > hi {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.NewUint", kind.String()+" out of range")
	}
	return UintValue(kind, n), nil
}

func Uint8Value(n uint8) Value   { return UintValue(KindUint8, uint64(n)) }
func Uint16Value(n uint16) Value { return UintValue(KindUint16, uint64(n)) }
func Uint32Value(n uint32) Value { return UintValue(KindUint32, uint64(n)) }
func Uint64Value(n uint64) Value { return UintValue(KindUint64, n) }

// canonFloat collapses -0.0 to +0.0 and every NaN payload to a single
// canonical quiet NaN, so equal values always have identical bits
// (docs/design-datatypes.md D8).
func canonFloat(f float64) float64 {
	if math.IsNaN(f) {
		return math.NaN()
	}
	if f == 0 {
		return 0
	}
	return f
}

// Float32Value constructs a FLOAT32 value, rounding f to 32-bit precision and
// canonicalizing -0/NaN.
func Float32Value(f float64) Value {
	return Value{Typ: Float32(), Flt: canonFloat(float64(float32(f)))}
}

// Float64Value constructs a FLOAT64 value, canonicalizing -0/NaN.
func Float64Value(f float64) Value {
	return Value{Typ: Float64(), Flt: canonFloat(f)}
}

// FloatValue constructs a float value of kind (KindFloat32 or KindFloat64).
func FloatValue(kind Kind, f float64) Value {
	if kind == KindFloat32 {
		return Float32Value(f)
	}
	return Value{Typ: Type{Kind: kind}, Flt: canonFloat(f)}
}

// ParseFloat parses decimal or scientific-notation float text.
func ParseFloat(kind Kind, s string) (Value, error) {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseFloat", "invalid float")
	}
	return FloatValue(kind, f), nil
}

// FormatFloat renders a float in the shortest round-trippable form. NaN and
// ±Inf render as "NaN"/"Infinity"/"-Infinity".
func FormatFloat(kind Kind, f float64) string {
	switch {
	case math.IsNaN(f):
		return "NaN"
	case math.IsInf(f, 1):
		return "Infinity"
	case math.IsInf(f, -1):
		return "-Infinity"
	}
	bits := 64
	if kind == KindFloat32 {
		bits = 32
	}
	return strconv.FormatFloat(f, 'g', -1, bits)
}

// DateValue reconstructs an already-range-validated DATE value from a day
// count since the Unix epoch (1970-01-01 = 0). Reuses Value.Int, the same
// field the fixed-width signed integers use, sign-extended to int64.
func DateValue(days int32) Value { return Value{Typ: Date(), Int: int64(days)} }

// NewDate is the range-checked constructor for a DATE value from a day count.
func NewDate(days int64) (Value, error) {
	if days < math.MinInt32 || days > math.MaxInt32 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.NewDate", "DATE out of range")
	}
	return DateValue(int32(days)), nil
}

// dayNanos is the number of nanoseconds in one day, the exclusive upper
// bound of a TIME value's nanoseconds-since-midnight.
const dayNanos = int64(24 * time.Hour)

// TimeOfDayValue reconstructs an already-range-validated TIME value from
// nanoseconds since midnight. Reuses Value.Time, the same field TIMESTAMPTZ
// uses for UTC-epoch nanoseconds — disambiguated by Value.Typ.Kind at every
// read site, the same way Value.Int already serves four integer widths.
func TimeOfDayValue(ns int64) Value { return Value{Typ: TimeOfDay(), Time: ns} }

// NewTimeOfDay is the range-checked constructor for a TIME value from
// nanoseconds since midnight.
func NewTimeOfDay(ns int64) (Value, error) {
	if ns < 0 || ns >= dayNanos {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.NewTimeOfDay", "TIME out of range")
	}
	return TimeOfDayValue(ns), nil
}

// NaiveTimestampValue reconstructs a plain TIMESTAMP (no timezone) value from
// nanoseconds since 1970-01-01T00:00:00 (the civil value read literally, no
// offset). Reuses Value.Time, disambiguated by Value.Typ.Kind
// (docs/design-datatypes.md D7).
func NaiveTimestampValue(ns int64) Value { return Value{Typ: Timestamp(), Time: ns} }

// tsNaiveLayouts are the accepted plain-TIMESTAMP text forms: ISO 8601 date +
// time with no trailing offset. A bare date is midnight. Offset-carrying text
// is rejected — a plain TIMESTAMP has no zone to reconcile it against.
var tsNaiveLayouts = []string{
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05",
	"2006-01-02",
}

// ParseNaiveTimestamp parses a plain TIMESTAMP from ISO 8601 text with no
// timezone offset.
func ParseNaiveTimestamp(s string) (Value, error) {
	s = strings.TrimSpace(s)
	for _, l := range tsNaiveLayouts {
		if t, err := time.Parse(l, s); err == nil {
			return NaiveTimestampValue(t.UnixNano()), nil
		}
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseNaiveTimestamp", "invalid timestamp")
}

// FormatNaiveTimestamp formats nanoseconds-since-epoch as a plain TIMESTAMP
// (YYYY-MM-DD HH:MM:SS[.fraction], no offset).
func FormatNaiveTimestamp(ns int64) string {
	return time.Unix(0, ns).UTC().Format("2006-01-02 15:04:05.999999999")
}

// ParseDate parses an ISO 8601 calendar date (YYYY-MM-DD).
func ParseDate(s string) (Value, error) {
	t, err := time.Parse("2006-01-02", strings.TrimSpace(s))
	if err != nil {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseDate", "invalid date")
	}
	// t is UTC midnight for the parsed date, so Unix() is an exact multiple
	// of 86400 (no remainder, so truncating division is exact for negatives too).
	return NewDate(t.Unix() / 86400)
}

// FormatDate formats a day-count-since-epoch as ISO 8601 (YYYY-MM-DD).
func FormatDate(days int32) string {
	return time.Unix(int64(days)*86400, 0).UTC().Format("2006-01-02")
}

// ParseTimeOfDay parses an ISO 8601 time-of-day (HH:MM:SS[.fraction]).
func ParseTimeOfDay(s string) (Value, error) {
	t, err := time.Parse("15:04:05.999999999", strings.TrimSpace(s))
	if err != nil {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseTimeOfDay", "invalid time")
	}
	ns := int64(t.Hour())*int64(time.Hour) + int64(t.Minute())*int64(time.Minute) +
		int64(t.Second())*int64(time.Second) + int64(t.Nanosecond())
	return NewTimeOfDay(ns)
}

// FormatTimeOfDay formats nanoseconds-since-midnight as HH:MM:SS, with a
// trimmed fractional-seconds suffix when present.
func FormatTimeOfDay(ns int64) string {
	return time.Unix(0, ns).UTC().Format("15:04:05.999999999")
}

// IntervalValue constructs an INTERVAL value directly from its 3 stored
// components (docs/design-datatypes.md D6).
func IntervalValue(months, days int32, nanos int64) Value {
	return Value{Typ: Interval(), IntervalMonths: months, IntervalDays: days, Time: nanos}
}

var intervalTimeUnitNanos = map[string]int64{
	"hour": int64(time.Hour), "hours": int64(time.Hour), "hr": int64(time.Hour), "hrs": int64(time.Hour),
	"minute": int64(time.Minute), "minutes": int64(time.Minute), "min": int64(time.Minute), "mins": int64(time.Minute),
	"second": int64(time.Second), "seconds": int64(time.Second), "sec": int64(time.Second), "secs": int64(time.Second),
	"millisecond": int64(time.Millisecond), "milliseconds": int64(time.Millisecond),
	"microsecond": int64(time.Microsecond), "microseconds": int64(time.Microsecond),
}

// ParseInterval parses a Postgres-style interval literal: one or more
// "<quantity> <unit>" pairs, e.g. "1 year 2 months 3 days 04:05:06.5"'s
// simpler cousin "1 year 2 months 3 days 4 hours 5 minutes 6.5 seconds"
// (docs/design-datatypes.md D6). year/month/day quantities must be whole
// numbers — fractional calendar units are ambiguous (how many days is "1.5
// months"?) and explicitly out of scope for this increment; hour and
// smaller units accept a decimal quantity, converted to nanoseconds via
// exact big.Int arithmetic (never float) so "0.1 seconds" is exactly
// 100000000ns, not an approximation, and any sub-nanosecond remainder
// errors rather than silently rounds.
func ParseInterval(s string) (Value, error) {
	fields := strings.Fields(strings.TrimSpace(s))
	if len(fields) == 0 || len(fields)%2 != 0 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "invalid interval")
	}
	var months, days, nanos int64
	for i := 0; i < len(fields); i += 2 {
		qtyStr, unit := fields[i], strings.ToLower(fields[i+1])
		switch unit {
		case "year", "years", "yr", "yrs":
			n, err := parseIntervalWhole(qtyStr)
			if err != nil {
				return Value{}, err
			}
			months += n * 12
		case "month", "months", "mon", "mons":
			n, err := parseIntervalWhole(qtyStr)
			if err != nil {
				return Value{}, err
			}
			months += n
		case "day", "days":
			n, err := parseIntervalWhole(qtyStr)
			if err != nil {
				return Value{}, err
			}
			days += n
		default:
			unitNanos, ok := intervalTimeUnitNanos[unit]
			if !ok {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "unknown interval unit "+unit)
			}
			n, err := decimalNanosFromUnit(qtyStr, unitNanos)
			if err != nil {
				return Value{}, err
			}
			nanos += n
		}
	}
	if months < math.MinInt32 || months > math.MaxInt32 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "interval month component out of range")
	}
	if days < math.MinInt32 || days > math.MaxInt32 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "interval day component out of range")
	}
	return IntervalValue(int32(months), int32(days), nanos), nil
}

func parseIntervalWhole(qty string) (int64, error) {
	n, err := strconv.ParseInt(strings.TrimSpace(qty), 10, 64)
	if err != nil {
		return 0, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "year/month/day quantity must be a whole number")
	}
	return n, nil
}

// decimalNanosFromUnit converts a decimal-text quantity in the given unit to
// exact nanoseconds via big.Int (coef * unitNanos / 10^scale), erroring on
// any non-zero remainder (sub-nanosecond precision) rather than rounding.
func decimalNanosFromUnit(qty string, unitNanos int64) (int64, error) {
	d, err := ParseDecimal(qty)
	if err != nil {
		return 0, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "invalid interval quantity")
	}
	num := new(big.Int).Mul(d.Coef, big.NewInt(unitNanos))
	den := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(d.Scale)), nil)
	q, rem := new(big.Int).QuoRem(num, den, new(big.Int))
	if rem.Sign() != 0 {
		return 0, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "interval quantity has sub-nanosecond precision")
	}
	if !q.IsInt64() {
		return 0, nerr.New(nerr.InvalidArgument, "types.ParseInterval", "interval quantity out of range")
	}
	return q.Int64(), nil
}

// FormatInterval renders an INTERVAL's 3 components back to the same
// "<quantity> <unit> ..." shape ParseInterval accepts, omitting zero
// components (an all-zero interval formats as "0"). Mirrors Postgres's own
// conditional-component output style.
func FormatInterval(months, days int32, nanos int64) string {
	var parts []string
	years, rem := months/12, months%12
	if years != 0 {
		parts = append(parts, intervalUnitStr(years, "year"))
	}
	if rem != 0 {
		parts = append(parts, intervalUnitStr(rem, "month"))
	}
	if days != 0 {
		parts = append(parts, intervalUnitStr(days, "day"))
	}
	if nanos != 0 {
		// Emits "<n> hours <n> minutes <n[.frac]> seconds" — the same
		// "<quantity> <unit>" shape ParseInterval accepts, not a colon
		// "HH:MM:SS" literal, which ParseInterval's grammar does not
		// understand (found and fixed before this went out the door: the
		// two were not actually round-trippable, despite this function's
		// own doc comment claiming they were — caught by
		// TestIntervalParseFormat's format/reparse round trip).
		neg := nanos < 0
		n := nanos
		if neg {
			n = -n
		}
		h := n / int64(time.Hour)
		n %= int64(time.Hour)
		m := n / int64(time.Minute)
		n %= int64(time.Minute)
		sec := n / int64(time.Second)
		frac := n % int64(time.Second)
		sign := int32(1)
		if neg {
			sign = -1
		}
		if h != 0 {
			parts = append(parts, intervalUnitStr(sign*int32(h), "hour"))
		}
		if m != 0 {
			parts = append(parts, intervalUnitStr(sign*int32(m), "minute"))
		}
		if sec != 0 || frac != 0 {
			secStr := strconv.FormatInt(sec, 10)
			if frac != 0 {
				fracStr := strings.TrimRight(strconv.FormatInt(1_000_000_000+frac, 10)[1:], "0")
				secStr += "." + fracStr
			}
			if neg {
				secStr = "-" + secStr
			}
			unit := "seconds"
			if sec == 1 && frac == 0 {
				unit = "second"
			}
			parts = append(parts, secStr+" "+unit)
		}
	}
	if len(parts) == 0 {
		return "0"
	}
	return strings.Join(parts, " ")
}

func intervalUnitStr(n int32, unit string) string {
	if n != 1 && n != -1 {
		unit += "s"
	}
	return strconv.FormatInt(int64(n), 10) + " " + unit
}


// justifiedNanos computes an INTERVAL's Postgres-style "justified" total
// (1 month = 30 days = 24h) as an exact big.Int, used for Cmp — not int64,
// since months/days are each a full int32 and this engine's
// nanosecond-precision time component (Postgres uses microseconds) makes
// int64 overflow reachable well within int32's legitimate range.
func justifiedNanos(months, days int32, nanos int64) (int64, error) {
	const dayNanos = int64(86400_000_000_000)
	totalDays := int64(months)*30 + int64(days)
	if totalDays != 0 {
		limit := int64(math.MaxInt64) / dayNanos
		if totalDays > limit || totalDays < -limit {
			return 0, nerr.New(nerr.InvalidArgument, "types.justifiedNanos", "interval magnitude too large to compare")
		}
	}
	dayPart := totalDays * dayNanos
	sum := dayPart + nanos
	if (nanos > 0 && sum < dayPart) || (nanos < 0 && sum > dayPart) {
		return 0, nerr.New(nerr.InvalidArgument, "types.justifiedNanos", "interval magnitude too large to compare")
	}
	return sum, nil
}

// decimalToInt converts an exact whole-number Decimal to an int64 in kind's
// range, erroring on any fractional remainder or out-of-range magnitude.
func decimalToInt(d Decimal, kind Kind) (int64, error) {
	r, err := d.Rescale(0, 0)
	if err != nil {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", "value is not a whole number")
	}
	if !r.Coef.IsInt64() {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", kind.String()+" out of range")
	}
	n := r.Coef.Int64()
	lo, hi, _ := IntRange(kind)
	if n < lo || n > hi {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", kind.String()+" out of range")
	}
	return n, nil
}

// floatToInt converts a finite whole-number float to an int64 in kind's
// range, erroring on any fractional remainder, NaN/Inf, or out-of-range
// magnitude (mirrors decimalToInt — docs/design-datatypes.md D8).
func floatToInt(f float64, kind Kind) (int64, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", "value is not finite")
	}
	if f != math.Trunc(f) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", "value is not a whole number")
	}
	lo, hi, _ := IntRange(kind)
	if f < float64(lo) || f > float64(hi) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", kind.String()+" out of range")
	}
	return int64(f), nil
}

// floatToUint mirrors floatToInt for the unsigned integer kinds.
func floatToUint(f float64, kind Kind) (uint64, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", "value is not finite")
	}
	if f != math.Trunc(f) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", "value is not a whole number")
	}
	hi, _ := UintRange(kind)
	if f < 0 || f > float64(hi) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", kind.String()+" out of range")
	}
	return uint64(f), nil
}

// decimalToUint converts an exact whole-number Decimal to a uint64 in kind's
// range, erroring on any fractional remainder, negative value, or
// out-of-range magnitude.
func decimalToUint(d Decimal, kind Kind) (uint64, error) {
	r, err := d.Rescale(0, 0)
	if err != nil {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", "value is not a whole number")
	}
	if r.Coef.Sign() < 0 || !r.Coef.IsUint64() {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", kind.String()+" out of range")
	}
	n := r.Coef.Uint64()
	hi, _ := UintRange(kind)
	if n > hi {
		return 0, nerr.New(nerr.InvalidArgument, "types.Coerce", kind.String()+" out of range")
	}
	return n, nil
}

// CharValue reconstructs an already-padded CHAR(n) value: exactly t.Precision
// runes. Callers (decode paths, Coerce) must ensure that first; use Coerce
// against a CharType(n) destination for a validating constructor from
// untrusted/computed input (docs/design-datatypes.md D4).
func CharValue(s string, t Type) Value { return Value{Typ: t, Str: s} }

// VarcharValue reconstructs an already-length-checked VARCHAR(n) value: at
// most t.Precision runes. Callers must ensure that first; use Coerce against
// a VarcharType(n) destination for a validating constructor
// (docs/design-datatypes.md D4).
func VarcharValue(s string, t Type) Value { return Value{Typ: t, Str: s} }

// padChar enforces CHAR(n)'s SQL-standard invariant: the stored value is
// always exactly n runes. Shorter input is right-padded with U+0020; longer
// input errors unless every rune past position n is itself a space, in which
// case the excess is silently trimmed (docs/design-datatypes.md D4).
func padChar(s string, n uint16) (string, error) {
	r := []rune(s)
	if len(r) < int(n) {
		out := make([]rune, int(n))
		copy(out, r)
		for i := len(r); i < int(n); i++ {
			out[i] = ' '
		}
		return string(out), nil
	}
	if len(r) > int(n) {
		for _, c := range r[n:] {
			if c != ' ' {
				return "", nerr.New(nerr.InvalidArgument, "types.Coerce", "value too long for CHAR("+itoa(int(n))+")")
			}
		}
		return string(r[:n]), nil
	}
	return s, nil
}

// checkVarchar enforces VARCHAR(n)'s length ceiling: input over n runes
// errors rather than silently truncating (docs/design-datatypes.md D4,
// mirroring D2/D3's "narrowing... never wraps, only errors" convention).
func checkVarchar(s string, n uint16) error {
	if len([]rune(s)) > int(n) {
		return nerr.New(nerr.InvalidArgument, "types.Coerce", "value too long for VARCHAR("+itoa(int(n))+")")
	}
	return nil
}

// EnumValue builds an ENUM value: label must be one of t.EnumLabels. Str
// holds the label, Int holds its 0-based declaration ordinal (the sort key,
// docs/design-datatypes.md D11).
func EnumValue(label string, t Type) (Value, error) {
	ord := t.EnumOrdinal(label)
	if ord < 0 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.EnumValue", "value is not a member of the ENUM label set")
	}
	return Value{Typ: t, Str: label, Int: int64(ord)}, nil
}

// EnumValueByOrdinal is the decode-side constructor: ord must be in range for
// t.EnumLabels. Callers on the wire/disk path use this.
func EnumValueByOrdinal(ord int, t Type) (Value, error) {
	if ord < 0 || ord >= len(t.EnumLabels) {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.EnumValueByOrdinal", "ENUM ordinal out of range")
	}
	return Value{Typ: t, Str: t.EnumLabels[ord], Int: int64(ord)}, nil
}

// ParseHexBlob decodes hex text (as produced by Value.String on a BLOB, or
// the `X'...'` literal's contents) into a BLOB value. Mirrors ParseUUID's
// text-parsing shape.
func ParseHexBlob(s string) (Value, error) {
	raw, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseHexBlob", "invalid hex blob")
	}
	return BlobValue(raw), nil
}

func VectorValue(v []float32, t Type) Value {
	cp := make([]float32, len(v))
	copy(cp, v)
	return Value{Typ: t, Vec: cp}
}

// SparseValue is a SPARSEVECTOR runtime value: parallel index/value lists of
// the non-zero coordinates. Indices must be strictly ascending, below t.Precision,
// and values finite and non-zero; use Coerce or ValidateSparse to enforce that.
func SparseValue(idx []uint32, val []float32, t Type) Value {
	return Value{
		Typ:       t,
		SparseIdx: append([]uint32(nil), idx...),
		SparseVal: append([]float32(nil), val...),
	}
}

// VectorRef is a heap-row placeholder: dimension is known, floats are not inline.
func VectorRef(t Type) Value {
	return Value{Typ: t, VecRef: true}
}

// ValidateVector rejects NaN and Inf elements.
func ValidateVector(v []float32) error {
	for _, f := range v {
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nerr.New(nerr.InvalidArgument, "types.ValidateVector", "VECTOR element is not finite")
		}
	}
	return nil
}

// ValidateSparse rejects a malformed SPARSEVECTOR: length mismatch, too many
// non-zeros, an index at or above dim, an out-of-order or duplicate index, or a
// zero / non-finite value.
func ValidateSparse(dim uint32, idx []uint32, val []float32) error {
	if dim == 0 || dim > uint32(MaxSparseSQLDim) {
		return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "SPARSEVECTOR dimension out of range")
	}
	if len(idx) != len(val) {
		return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse index/value length mismatch")
	}
	if len(idx) > MaxSparseSQLNNZ {
		return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "too many non-zero coordinates")
	}
	for i, ix := range idx {
		if ix >= dim {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse index out of range")
		}
		if i > 0 && idx[i-1] >= ix {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse indices not strictly ascending")
		}
		f := val[i]
		if f == 0 {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse value is zero")
		}
		if math.IsNaN(float64(f)) || math.IsInf(float64(f), 0) {
			return nerr.New(nerr.InvalidArgument, "types.ValidateSparse", "sparse value is not finite")
		}
	}
	return nil
}

// DenseToSparse drops zeros from a dense vector. Non-finite values fail closed.
func DenseToSparse(vec []float32) (idx []uint32, val []float32, err error) {
	if err := ValidateVector(vec); err != nil {
		return nil, nil, err
	}
	for i, f := range vec {
		if f == 0 {
			continue
		}
		idx = append(idx, uint32(i))
		val = append(val, f)
	}
	if len(idx) > MaxSparseSQLNNZ {
		return nil, nil, nerr.New(nerr.InvalidArgument, "types.DenseToSparse", "too many non-zero coordinates")
	}
	return idx, val, nil
}

// DecimalFromFloat stores f with 8 decimal places.
func DecimalFromFloat(f float64) (Value, error) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.DecimalFromFloat", "not finite")
	}
	s := strconv.FormatFloat(f, 'f', 8, 64)
	d, err := ParseDecimal(s)
	if err != nil {
		return Value{}, err
	}
	return DecimalValue(d, Type{Kind: KindDecimal, Precision: 38, Scale: 8}), nil
}

func BoolValue(b bool) Value { return Value{Typ: Bool(), Bool: b} }

// StructValue builds a STRUCT value: fields must be positionally aligned with
// t.Fields. This is the reconstruct-from-parts constructor (decode paths,
// executor); use Coerce against a StructType for validation of untrusted or
// computed input (docs/design-collections.md C1).
func StructValue(t Type, fields []Value) Value {
	return Value{Typ: t, Coll: append([]Value(nil), fields...)}
}

// ArrayValue builds an ARRAY value from its elements (docs/design-collections.md C2).
func ArrayValue(t Type, elems []Value) Value {
	return Value{Typ: t, Coll: append([]Value(nil), elems...)}
}

// MapValue builds a MAP value from parallel key/value slices. Callers that
// build a map from untrusted input should route through Coerce, which sorts by
// canonical key order and rejects duplicate keys (docs/design-collections.md C3).
func MapValue(t Type, keys, vals []Value) Value {
	return Value{Typ: t, CollKeys: append([]Value(nil), keys...), Coll: append([]Value(nil), vals...)}
}

// Clone returns a deep copy of v.
func (v Value) Clone() Value {
	if v.JSON != nil {
		v.JSON = append([]byte(nil), v.JSON...)
	}
	if v.Vec != nil {
		v.Vec = append([]float32(nil), v.Vec...)
	}
	if v.SparseIdx != nil {
		v.SparseIdx = append([]uint32(nil), v.SparseIdx...)
	}
	if v.SparseVal != nil {
		v.SparseVal = append([]float32(nil), v.SparseVal...)
	}
	if v.Dec.Coef != nil {
		v.Dec = v.Dec.Clone()
	}
	if v.Coords != nil {
		v.Coords = append([]float64(nil), v.Coords...)
	}
	if v.Rings != nil {
		v.Rings = append([]int(nil), v.Rings...)
	}
	if v.Coll != nil {
		cp := make([]Value, len(v.Coll))
		for i := range v.Coll {
			cp[i] = v.Coll[i].Clone()
		}
		v.Coll = cp
	}
	if v.CollKeys != nil {
		cp := make([]Value, len(v.CollKeys))
		for i := range v.CollKeys {
			cp[i] = v.CollKeys[i].Clone()
		}
		v.CollKeys = cp
	}
	if v.Geom != nil {
		v.Geom = v.Geom.Clone()
	}
	return v
}

func NewUUID() (Value, error) {
	var u [16]byte
	if _, err := rand.Read(u[:]); err != nil {
		return Value{}, nerr.Wrap(nerr.Internal, "types.NewUUID", "random", err)
	}
	u[6] = (u[6] & 0x0f) | 0x40
	u[8] = (u[8] & 0x3f) | 0x80
	return UUIDValue(u), nil
}

func Now() Value { return TimeValue(time.Now().UTC().UnixNano()) }

func ParseUUID(s string) (Value, error) {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "-", "")
	if len(s) != 32 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseUUID", "invalid UUID")
	}
	raw, err := hex.DecodeString(s)
	if err != nil || len(raw) != 16 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseUUID", "invalid UUID")
	}
	var u [16]byte
	copy(u[:], raw)
	return UUIDValue(u), nil
}

func FormatUUID(u [16]byte) string {
	s := hex.EncodeToString(u[:])
	return s[0:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}

func ParseTimestamp(s string) (Value, error) {
	s = strings.TrimSpace(s)
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999Z07:00",
		"2006-01-02 15:04:05Z07:00",
		"2006-01-02 15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if ts, err := time.Parse(l, s); err == nil {
			return TimeValue(ts.UTC().UnixNano()), nil
		}
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.ParseTimestamp", "invalid timestamptz")
}

func (v Value) String() string {
	if v.Null {
		return "NULL"
	}
	switch v.Typ.Kind {
	case KindUUID:
		return FormatUUID(v.UUID)
	case KindString, KindText, KindChar, KindVarchar, KindEnum:
		return v.Str
	case KindDecimal:
		return v.Dec.String()
	case KindTimestampTZ:
		return time.Unix(0, v.Time).UTC().Format(time.RFC3339Nano)
	case KindTimestamp:
		return FormatNaiveTimestamp(v.Time)
	case KindJSON:
		if v.JSON == nil {
			return "null"
		}
		txt, err := nsjson.ToText(v.JSON)
		if err != nil {
			return "?"
		}
		return string(txt)
	case KindVector:
		if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
			var b strings.Builder
			b.WriteByte('{')
			for i, idx := range v.SparseIdx {
				if i > 0 {
					b.WriteByte(',')
				}
				b.WriteString(strconv.FormatUint(uint64(idx), 10))
				b.WriteByte(':')
				var f float32
				if i < len(v.SparseVal) {
					f = v.SparseVal[i]
				}
				b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
			}
			b.WriteByte('}')
			return b.String()
		}
		var b strings.Builder
		b.WriteByte('[')
		for i, f := range v.Vec {
			if i > 0 {
				b.WriteByte(',')
			}
			b.WriteString(strconv.FormatFloat(float64(f), 'g', -1, 32))
		}
		b.WriteByte(']')
		return b.String()
	case KindBool:
		if v.Bool {
			return "TRUE"
		}
		return "FALSE"
	case KindPoint:
		return formatPoint(v.Lon, v.Lat)
	case KindBox:
		return formatBox(v.Box)
	case KindLine:
		return formatLine(v.Coords)
	case KindPolygon:
		return formatPolygon(v.Coords, v.Rings)
	case KindBlob:
		return hex.EncodeToString([]byte(v.Str))
	case KindInt8, KindInt16, KindInt32, KindInt64:
		return strconv.FormatInt(v.Int, 10)
	case KindUint8, KindUint16, KindUint32, KindUint64:
		return strconv.FormatUint(v.Uint, 10)
	case KindFloat32, KindFloat64:
		return FormatFloat(v.Typ.Kind, v.Flt)
	case KindDate:
		return FormatDate(int32(v.Int))
	case KindTime:
		return FormatTimeOfDay(v.Time)
	case KindInterval:
		return FormatInterval(v.IntervalMonths, v.IntervalDays, v.Time)
	case KindArray:
		var b strings.Builder
		b.WriteByte('[')
		for i, e := range v.Coll {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(memberString(e))
		}
		b.WriteByte(']')
		return b.String()
	case KindStruct:
		var b strings.Builder
		b.WriteByte('{')
		for i, e := range v.Coll {
			if i > 0 {
				b.WriteString(", ")
			}
			if i < len(v.Typ.Fields) {
				b.WriteString(v.Typ.Fields[i].Name)
				b.WriteString(": ")
			}
			b.WriteString(memberString(e))
		}
		b.WriteByte('}')
		return b.String()
	case KindMap:
		var b strings.Builder
		b.WriteByte('{')
		for i := range v.Coll {
			if i > 0 {
				b.WriteString(", ")
			}
			if i < len(v.CollKeys) {
				b.WriteString(memberString(v.CollKeys[i]))
			}
			b.WriteString(": ")
			b.WriteString(memberString(v.Coll[i]))
		}
		b.WriteByte('}')
		return b.String()
	case KindGeometry, KindGeography:
		if v.Geom == nil {
			return "NULL"
		}
		return FormatGeomWKT(v.Geom)
	default:
		return "?"
	}
}

// memberString renders one collection member for Value.String — NULL members
// as the literal NULL, everything else via its own String().
func memberString(v Value) string {
	if v.Null {
		return "NULL"
	}
	return v.String()
}

func (v Value) Cmp(o Value) (int, error) {
	if v.Null || o.Null {
		return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "NULL comparison")
	}
	if v.Typ.Kind != o.Typ.Kind && !stringish(v.Typ.Kind, o.Typ.Kind) && !intish(v.Typ.Kind, o.Typ.Kind) && !uintish(v.Typ.Kind, o.Typ.Kind) && !floatish(v.Typ.Kind, o.Typ.Kind) {
		return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "type mismatch")
	}
	switch v.Typ.Kind {
	case KindUUID:
		return bytes.Compare(v.UUID[:], o.UUID[:]), nil
	case KindString, KindText:
		return strings.Compare(v.Str, o.Str), nil
	case KindChar, KindVarchar:
		// Plain rune-for-rune compare of the already-stored (padded, for
		// CHAR) bytes. VARCHAR of different declared n compares exactly like
		// STRING/TEXT. Comparing two CHAR values of different declared n
		// directly (same Kind, so executor.eval's coerce-to-common-Kind
		// guard does not fire) does not re-pad the shorter to the longer's
		// width first, so it can diverge from strict SQL PADSPACE semantics
		// across differing widths — an accepted, out-of-scope limitation
		// per docs/design-datatypes.md D4, not a bug to fix here.
		return strings.Compare(v.Str, o.Str), nil
	case KindBlob:
		// Canonical order: plain byte-lexicographic comparison (see
		// docs/design-datatypes.md D1). No text/collation semantics apply.
		return bytes.Compare([]byte(v.Str), []byte(o.Str)), nil
	case KindInt8, KindInt16, KindInt32, KindInt64:
		switch {
		case v.Int < o.Int:
			return -1, nil
		case v.Int > o.Int:
			return 1, nil
		default:
			return 0, nil
		}
	case KindUint8, KindUint16, KindUint32, KindUint64:
		switch {
		case v.Uint < o.Uint:
			return -1, nil
		case v.Uint > o.Uint:
			return 1, nil
		default:
			return 0, nil
		}
	case KindFloat32, KindFloat64:
		return cmpFloatTotal(v.Flt, o.Flt), nil
	case KindEnum:
		// Declaration-order comparison over the stored ordinals, NOT
		// lexicographic (docs/design-datatypes.md D11).
		switch {
		case v.Int < o.Int:
			return -1, nil
		case v.Int > o.Int:
			return 1, nil
		default:
			return 0, nil
		}
	case KindDecimal:
		return v.Dec.Cmp(o.Dec), nil
	case KindTimestampTZ, KindTime, KindTimestamp:
		switch {
		case v.Time < o.Time:
			return -1, nil
		case v.Time > o.Time:
			return 1, nil
		default:
			return 0, nil
		}
	case KindDate:
		switch {
		case v.Int < o.Int:
			return -1, nil
		case v.Int > o.Int:
			return 1, nil
		default:
			return 0, nil
		}
	case KindBool:
		switch {
		case !v.Bool && o.Bool:
			return -1, nil
		case v.Bool && !o.Bool:
			return 1, nil
		default:
			return 0, nil
		}
	case KindJSON:
		return bytes.Compare(v.JSON, o.JSON), nil
	case KindPoint:
		if v.Lon < o.Lon {
			return -1, nil
		}
		if v.Lon > o.Lon {
			return 1, nil
		}
		switch {
		case v.Lat < o.Lat:
			return -1, nil
		case v.Lat > o.Lat:
			return 1, nil
		default:
			return 0, nil
		}
	case KindBox:
		for i := 0; i < 4; i++ {
			if v.Box[i] < o.Box[i] {
				return -1, nil
			}
			if v.Box[i] > o.Box[i] {
				return 1, nil
			}
		}
		return 0, nil
	case KindLine, KindPolygon:
		if n := len(v.Rings) - len(o.Rings); n != 0 {
			if n < 0 {
				return -1, nil
			}
			return 1, nil
		}
		for i := range v.Rings {
			if v.Rings[i] < o.Rings[i] {
				return -1, nil
			}
			if v.Rings[i] > o.Rings[i] {
				return 1, nil
			}
		}
		n := len(v.Coords)
		if len(o.Coords) < n {
			n = len(o.Coords)
		}
		for i := 0; i < n; i++ {
			if v.Coords[i] < o.Coords[i] {
				return -1, nil
			}
			if v.Coords[i] > o.Coords[i] {
				return 1, nil
			}
		}
		switch {
		case len(v.Coords) < len(o.Coords):
			return -1, nil
		case len(v.Coords) > len(o.Coords):
			return 1, nil
		default:
			return 0, nil
		}
	case KindInterval:
		// Postgres's own "justified" heuristic (1 month = 30 days = 24h),
		// not a comparison of the raw fields — two intervals unequal in
		// their raw (months, days, nanos) can compare equal here (e.g. `1
		// month` = `30 days`), matching Postgres's documented interval_cmp
		// behavior exactly (docs/design-datatypes.md D6).
		vn, err := justifiedNanos(v.IntervalMonths, v.IntervalDays, v.Time)
		if err != nil {
			return 0, err
		}
		on, err := justifiedNanos(o.IntervalMonths, o.IntervalDays, o.Time)
		if err != nil {
			return 0, err
		}
		switch {
		case vn < on:
			return -1, nil
		case vn > on:
			return 1, nil
		default:
			return 0, nil
		}
	case KindStruct:
		// Field-by-field lexicographic in declaration order; the first
		// non-equal field decides. Requires the same declared field list
		// (docs/design-collections.md C1).
		if len(v.Coll) != len(o.Coll) {
			return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "STRUCT shape mismatch")
		}
		for i := range v.Coll {
			c, err := cmpMember(v.Coll[i], o.Coll[i])
			if err != nil {
				return 0, err
			}
			if c != 0 {
				return c, nil
			}
		}
		return 0, nil
	case KindArray:
		// Element-by-element lexicographic; a shorter array that is a prefix
		// of a longer one sorts first (docs/design-collections.md C2).
		n := len(v.Coll)
		if len(o.Coll) < n {
			n = len(o.Coll)
		}
		for i := 0; i < n; i++ {
			c, err := cmpMember(v.Coll[i], o.Coll[i])
			if err != nil {
				return 0, err
			}
			if c != 0 {
				return c, nil
			}
		}
		switch {
		case len(v.Coll) < len(o.Coll):
			return -1, nil
		case len(v.Coll) > len(o.Coll):
			return 1, nil
		default:
			return 0, nil
		}
	case KindGeometry, KindGeography:
		// Canonical-EWKB byte order — a deterministic total order, not
		// geometrically meaningful (docs/design-spatial.md §2.5).
		ab, err := encodeSortableGeneralGeo(v)
		if err != nil {
			return 0, err
		}
		bb, err := encodeSortableGeneralGeo(o)
		if err != nil {
			return 0, err
		}
		return bytes.Compare(ab, bb), nil
	case KindMap:
		// Entries are held in canonical key order, so a straight pairwise
		// walk over (key, value) is a well-defined total order
		// (docs/design-collections.md C3).
		n := len(v.Coll)
		if len(o.Coll) < n {
			n = len(o.Coll)
		}
		for i := 0; i < n; i++ {
			if c, err := cmpMember(v.CollKeys[i], o.CollKeys[i]); err != nil || c != 0 {
				return c, err
			}
			if c, err := cmpMember(v.Coll[i], o.Coll[i]); err != nil || c != 0 {
				return c, err
			}
		}
		switch {
		case len(v.Coll) < len(o.Coll):
			return -1, nil
		case len(v.Coll) > len(o.Coll):
			return 1, nil
		default:
			return 0, nil
		}
	default:
		return 0, nerr.New(nerr.InvalidArgument, "types.Value.Cmp", "type is not comparable")
	}
}

// cmpMember compares two collection members with NULL ordered before every
// present value (matching the sortable-key framing in row.go: end < null <
// present).
func cmpMember(a, b Value) (int, error) {
	switch {
	case a.Null && b.Null:
		return 0, nil
	case a.Null:
		return -1, nil
	case b.Null:
		return 1, nil
	default:
		return a.Cmp(b)
	}
}

func stringish(a, b Kind) bool {
	return (a == KindString || a == KindText) && (b == KindString || b == KindText)
}

// textlike reports whether k is one of STRING/TEXT/CHAR/VARCHAR — the
// "string family" for Coerce purposes. CHAR/VARCHAR are deliberately treated
// as close STRING/TEXT siblings rather than an isolated family the way
// BLOB/Int/Uint/Date/Time are from each other and from strings: every
// STRING/TEXT source-side coercion path (into DECIMAL, INT/UINT, UUID, DATE,
// TIME, JSON, BLOB, geo) also accepts CHAR/VARCHAR input, and CHAR/VARCHAR as
// a *destination* accepts anything STRING/TEXT already accepts (any source
// Kind, via Value.String()) — see docs/design-datatypes.md D4.
func textlike(k Kind) bool {
	return k == KindString || k == KindText || k == KindChar || k == KindVarchar
}

// textSource returns the string payload of a STRING/TEXT/CHAR/VARCHAR value
// for use as a coercion source. CHAR's significant content excludes the space
// padding added at store time (SQL PADSPACE), so it is right-trimmed here;
// STRING/TEXT/VARCHAR are returned verbatim (docs/design-datatypes.md D4).
func textSource(v Value) string {
	if v.Typ.Kind == KindChar {
		return strings.TrimRight(v.Str, " ")
	}
	return v.Str
}

// intish reports whether a and b are both fixed-width integer kinds
// (possibly different widths — every width is stored sign-extended in
// Value.Int, so cross-width comparison is always well-defined).
func intish(a, b Kind) bool {
	return IsInt(a) && IsInt(b)
}

// uintish reports whether a and b are both fixed-width unsigned integer
// kinds (possibly different widths — every width is stored zero-extended in
// Value.Uint, so cross-width comparison is always well-defined). Mirrors
// intish; signed and unsigned kinds stay isolated from each other for direct
// Cmp (a mismatched pair is coerced to a common Kind by the caller first —
// see executor.eval's binary-comparison path — before Cmp ever sees it).
func uintish(a, b Kind) bool {
	return IsUint(a) && IsUint(b)
}

// floatish reports whether a and b are both IEEE-754 float kinds (F32/F64) —
// both are held widened in Value.Flt, so cross-width comparison is well
// defined (a FLOAT32 value's Flt is already rounded to 32-bit precision).
func floatish(a, b Kind) bool {
	return IsFloat(a) && IsFloat(b)
}

// cmpFloatTotal is the canonical total order over floats (docs/design-datatypes.md
// D8): -Inf < negative reals < ±0 < positive reals < +Inf < NaN. All NaN
// compare equal; -0 and +0 compare equal (and are canonicalized to +0 on
// write anyway).
func cmpFloatTotal(a, b float64) int {
	an, bn := math.IsNaN(a), math.IsNaN(b)
	switch {
	case an && bn:
		return 0
	case an:
		return 1
	case bn:
		return -1
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

func Coerce(v Value, dest Type) (Value, error) {
	if v.Null {
		return Null(dest), nil
	}
	if v.Typ.Kind == dest.Kind {
		switch dest.Kind {
		case KindDecimal:
			d, err := v.Dec.Rescale(int(dest.Precision), int(dest.Scale))
			if err != nil {
				return Value{}, err
			}
			return DecimalValue(d, dest), nil
		case KindFloat32, KindFloat64:
			return FloatValue(dest.Kind, v.Flt), nil
		case KindEnum:
			// Re-resolve the label against the destination's label list: two
			// ENUM columns with different declared labels are different types.
			return EnumValue(v.Str, dest)
		case KindVector:
			if v.VecRef {
				if dest.Precision != 0 && v.Typ.Precision != dest.Precision {
					return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
				}
				if dest.VecElem == VecSparse && v.Typ.VecElem != VecSparse && v.Typ.VecElem != 0 {
					return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce a dense VECTOR reference to SPARSEVECTOR")
				}
				if dest.VecElem != VecSparse && v.Typ.VecElem == VecSparse {
					return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot widen a SPARSEVECTOR to another vector type")
				}
				v.Typ = dest
				return v, nil
			}
			if dest.VecElem == VecSparse {
				return coerceSparse(v, dest)
			}
			if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot widen a SPARSEVECTOR to another vector type")
			}
			if err := ValidateVector(v.Vec); err != nil {
				return Value{}, err
			}
			if uint16(len(v.Vec)) != dest.Precision {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
			}
			if dest.VecElem != v.Typ.VecElem {
				switch dest.VecElem {
				case VecF16:
					v.Vec = float16.Quantize(v.Vec)
				case VecI8:
					v.Vec = int8vec.Quantize(v.Vec)
				case VecBit:
					if err := bitvec.Validate(v.Vec); err != nil {
						return Value{}, err
					}
				default:
					if v.Typ.VecElem == VecBit {
						return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot widen a BITVECTOR to another vector type")
					}
				}
			}
			v.Typ = dest
			return v, nil
		case KindJSON:
			if err := nsjson.Validate(v.JSON); err != nil {
				return Value{}, err
			}
			v.Typ = dest
			return v, nil
		case KindChar:
			s, err := padChar(v.Str, dest.Precision)
			if err != nil {
				return Value{}, err
			}
			return CharValue(s, dest), nil
		case KindVarchar:
			if err := checkVarchar(v.Str, dest.Precision); err != nil {
				return Value{}, err
			}
			return VarcharValue(v.Str, dest), nil
		case KindStruct, KindArray, KindMap:
			return coerceCollection(v, dest)
		case KindGeometry, KindGeography:
			return coerceGeneralGeo(v, dest)
		default:
			v.Typ = dest
			return v, nil
		}
	}
	switch dest.Kind {
	case KindString, KindText:
		out := dest
		return Value{Typ: out, Str: v.String()}, nil
	case KindChar:
		// Any source STRING/TEXT already accepts is accepted here too, via its
		// canonical text form, then space-padded to exactly n runes
		// (docs/design-datatypes.md D4).
		s, err := padChar(v.String(), dest.Precision)
		if err != nil {
			return Value{}, err
		}
		return CharValue(s, dest), nil
	case KindVarchar:
		s := v.String()
		if err := checkVarchar(s, dest.Precision); err != nil {
			return Value{}, err
		}
		return VarcharValue(s, dest), nil
	case KindUUID:
		if textlike(v.Typ.Kind) {
			return ParseUUID(textSource(v))
		}
	case KindTimestampTZ:
		// Pre-existing gap, found and fixed while implementing D6: Coerce
		// had no STRING/TEXT -> TIMESTAMPTZ case at all — ParseTimestamp
		// was defined but never called from anywhere in the non-test
		// codebase. This went undetected because every existing test
		// populates TIMESTAMPTZ via DEFAULT NOW() or a driver-native
		// datetime object sent directly as Kind.TimestampTZ over the wire
		// (bypassing text coercion entirely) — a plain SQL string literal
		// INSERT into a TIMESTAMPTZ column, e.g. `VALUES ('2024-01-01T00:00:00Z')`,
		// has never worked in this codebase. Found only because D6's own
		// tests needed a TIMESTAMPTZ column populated by literal text.
		if textlike(v.Typ.Kind) {
			return ParseTimestamp(textSource(v))
		}
	case KindEnum:
		// CAST to ENUM validates label-set membership (docs/design-datatypes.md
		// D11). Isolated from every family but text, mirroring D1-D8.
		if textlike(v.Typ.Kind) {
			return EnumValue(textSource(v), dest)
		}
	case KindDecimal:
		switch v.Typ.Kind {
		case KindString, KindText, KindChar, KindVarchar:
			d, err := ParseDecimal(textSource(v))
			if err != nil {
				return Value{}, err
			}
			d, err = d.Rescale(int(dest.Precision), int(dest.Scale))
			if err != nil {
				return Value{}, err
			}
			return DecimalValue(d, dest), nil
		case KindInt8, KindInt16, KindInt32, KindInt64:
			d, err := DecimalFromInt64(v.Int).Rescale(int(dest.Precision), int(dest.Scale))
			if err != nil {
				return Value{}, err
			}
			return DecimalValue(d, dest), nil
		case KindUint8, KindUint16, KindUint32, KindUint64:
			d, err := DecimalFromUint64(v.Uint).Rescale(int(dest.Precision), int(dest.Scale))
			if err != nil {
				return Value{}, err
			}
			return DecimalValue(d, dest), nil
		case KindFloat32, KindFloat64:
			if math.IsNaN(v.Flt) || math.IsInf(v.Flt, 0) {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce a non-finite float to DECIMAL")
			}
			bits := 64
			if v.Typ.Kind == KindFloat32 {
				bits = 32
			}
			d, err := ParseDecimal(strconv.FormatFloat(v.Flt, 'f', -1, bits))
			if err != nil {
				return Value{}, err
			}
			if dest.Precision != 0 || dest.Scale != 0 {
				d, err = d.Rescale(int(dest.Precision), int(dest.Scale))
				if err != nil {
					return Value{}, err
				}
				return DecimalValue(d, dest), nil
			}
			return DecimalValue(d, Type{Kind: KindDecimal, Scale: uint16(d.Scale)}), nil
		}
	case KindFloat32, KindFloat64:
		// The float families and the exact-numeric families (int, uint,
		// decimal) plus decimal/scientific text form one coercible numeric
		// group (docs/design-datatypes.md D8). Widening into a float is
		// permitted even when not exactly representable — floats are inexact
		// by design. Isolated from BLOB/UUID/BOOL/JSON/geo/date/time.
		switch v.Typ.Kind {
		case KindFloat32, KindFloat64:
			return FloatValue(dest.Kind, v.Flt), nil
		case KindInt8, KindInt16, KindInt32, KindInt64:
			return FloatValue(dest.Kind, float64(v.Int)), nil
		case KindUint8, KindUint16, KindUint32, KindUint64:
			return FloatValue(dest.Kind, float64(v.Uint)), nil
		case KindDecimal:
			f, err := strconv.ParseFloat(v.Dec.String(), 64)
			if err != nil {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce DECIMAL to float")
			}
			return FloatValue(dest.Kind, f), nil
		case KindString, KindText, KindChar, KindVarchar:
			return ParseFloat(dest.Kind, textSource(v))
		}
	case KindInt8, KindInt16, KindInt32, KindInt64:
		// Deliberately isolated from BLOB/UUID/BOOL/JSON/geo: only other
		// numeric-ish sources (another int width, an unsigned width, DECIMAL,
		// or decimal text) coerce here (docs/design-datatypes.md D2/D3).
		// Narrowing always range checks and errors rather than wrapping.
		switch v.Typ.Kind {
		case KindInt8, KindInt16, KindInt32, KindInt64:
			return NewInt(dest.Kind, v.Int)
		case KindUint8, KindUint16, KindUint32, KindUint64:
			if v.Uint > math.MaxInt64 {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", dest.Kind.String()+" out of range")
			}
			return NewInt(dest.Kind, int64(v.Uint))
		case KindDecimal:
			n, err := decimalToInt(v.Dec, dest.Kind)
			if err != nil {
				return Value{}, err
			}
			return IntValue(dest.Kind, n), nil
		case KindFloat32, KindFloat64:
			n, err := floatToInt(v.Flt, dest.Kind)
			if err != nil {
				return Value{}, err
			}
			return IntValue(dest.Kind, n), nil
		case KindString, KindText, KindChar, KindVarchar:
			d, err := ParseDecimal(textSource(v))
			if err != nil {
				return Value{}, err
			}
			n, err := decimalToInt(d, dest.Kind)
			if err != nil {
				return Value{}, err
			}
			return IntValue(dest.Kind, n), nil
		}
	case KindUint8, KindUint16, KindUint32, KindUint64:
		// Mirrors the KindInt8...64 case above (docs/design-datatypes.md D3):
		// isolated from BLOB/UUID/BOOL/JSON/geo, reachable from a signed
		// width, another unsigned width, DECIMAL, or decimal text. Narrowing
		// and negative-to-unsigned always range check and error.
		switch v.Typ.Kind {
		case KindUint8, KindUint16, KindUint32, KindUint64:
			return NewUint(dest.Kind, v.Uint)
		case KindInt8, KindInt16, KindInt32, KindInt64:
			if v.Int < 0 {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", dest.Kind.String()+" out of range")
			}
			return NewUint(dest.Kind, uint64(v.Int))
		case KindDecimal:
			n, err := decimalToUint(v.Dec, dest.Kind)
			if err != nil {
				return Value{}, err
			}
			return UintValue(dest.Kind, n), nil
		case KindFloat32, KindFloat64:
			n, err := floatToUint(v.Flt, dest.Kind)
			if err != nil {
				return Value{}, err
			}
			return UintValue(dest.Kind, n), nil
		case KindString, KindText, KindChar, KindVarchar:
			d, err := ParseDecimal(textSource(v))
			if err != nil {
				return Value{}, err
			}
			n, err := decimalToUint(d, dest.Kind)
			if err != nil {
				return Value{}, err
			}
			return UintValue(dest.Kind, n), nil
		}
	case KindPoint, KindBox, KindLine, KindPolygon:
		if textlike(v.Typ.Kind) {
			g, err := ParseWKT(textSource(v))
			if err != nil {
				return Value{}, err
			}
			if g.Typ.Kind != dest.Kind {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce "+g.Typ.String()+" to "+dest.String())
			}
			return g, nil
		}
		// CAST bridge: unwrap a GEOMETRY / GEOGRAPHY of the matching subtype
		// back to a fixed WGS84 shape (docs/design-spatial.md §2.7). BOX has
		// no OGC equivalent so it is text-only.
		if IsGeneralSpatial(v.Typ.Kind) && v.Geom != nil && dest.Kind != KindBox {
			return geomToFixedShape(v.Geom, dest)
		}
	case KindJSON:
		if textlike(v.Typ.Kind) {
			return JSONFromText(textSource(v))
		}
		if v.Typ.Kind == KindArray || v.Typ.Kind == KindStruct || v.Typ.Kind == KindMap {
			return collectionToJSON(v)
		}
	case KindBlob:
		// Deliberately not a byte-for-byte passthrough: STRING/TEXT and BLOB
		// stay isolated families (docs/design-datatypes.md D1), so a source
		// string must be hex text, same as CAST-from-text for UUID above.
		if textlike(v.Typ.Kind) {
			return ParseHexBlob(textSource(v))
		}
	case KindDate:
		// Isolated from everything but text, same as D1-D3 precedent: no
		// implicit day-count-integer reinterpretation (docs/design-datatypes.md D5).
		if textlike(v.Typ.Kind) {
			return ParseDate(textSource(v))
		}
	case KindTime:
		// Isolated from everything but text, mirroring KindDate above.
		if textlike(v.Typ.Kind) {
			return ParseTimeOfDay(textSource(v))
		}
	case KindTimestamp:
		// Isolated from every family but text, including TIMESTAMPTZ:
		// converting a naive timestamp to/from a zoned one needs an assumed
		// zone this engine does not carry (docs/design-datatypes.md D7).
		if textlike(v.Typ.Kind) {
			return ParseNaiveTimestamp(textSource(v))
		}
	case KindInterval:
		// Isolated from every family but text, same D1-D8 isolation
		// precedent (docs/design-datatypes.md D6) — no implicit numeric
		// reinterpretation (an INTERVAL is not "a number of nanoseconds"
		// to the type system, even though that's its internal storage).
		if textlike(v.Typ.Kind) {
			return ParseInterval(textSource(v))
		}
	case KindGeometry, KindGeography:
		// Text (WKT / EWKT), and the CAST bridge from the four fixed WGS84
		// shapes (docs/design-spatial.md §2.7).
		if textlike(v.Typ.Kind) {
			g, err := ParseGeneralWKT(textSource(v), uint32(dest.Precision))
			if err != nil {
				return Value{}, err
			}
			return coerceGeneralGeo(Value{Typ: Type{Kind: dest.Kind}, Geom: g}, dest)
		}
		if bridged, ok := fixedShapeToGeom(v); ok {
			return coerceGeneralGeo(Value{Typ: Type{Kind: dest.Kind}, Geom: bridged}, dest)
		}
	}
	return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce "+v.Typ.String()+" to "+dest.String())
}

func coerceSparse(v Value, dest Type) (Value, error) {
	if dest.VecElem != VecSparse || dest.Precision < 1 {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.coerceSparse", "destination is not a SPARSEVECTOR")
	}
	if v.Typ.VecElem == VecBit {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot coerce a BITVECTOR to SPARSEVECTOR")
	}
	if v.Typ.VecElem == VecSparse || len(v.SparseIdx) > 0 {
		if v.Typ.Precision != 0 && v.Typ.Precision != dest.Precision {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
		}
		if err := ValidateSparse(uint32(dest.Precision), v.SparseIdx, v.SparseVal); err != nil {
			return Value{}, err
		}
		return SparseValue(v.SparseIdx, v.SparseVal, dest), nil
	}
	if uint16(len(v.Vec)) != dest.Precision {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "VECTOR dimension mismatch")
	}
	idx, val, err := DenseToSparse(v.Vec)
	if err != nil {
		return Value{}, err
	}
	if err := ValidateSparse(uint32(dest.Precision), idx, val); err != nil {
		return Value{}, err
	}
	return SparseValue(idx, val, dest), nil
}

// coerceCollection coerces a STRUCT/ARRAY/MAP value to another collection type
// of the same Kind, coercing each member to the destination's declared member
// type (docs/design-collections.md). MAP entries are re-sorted into canonical
// key order and duplicate keys are rejected.
func coerceCollection(v Value, dest Type) (Value, error) {
	switch dest.Kind {
	case KindStruct:
		if len(dest.Fields) != len(v.Coll) {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "STRUCT field count mismatch")
		}
		out := make([]Value, len(dest.Fields))
		for i, f := range dest.Fields {
			if i < len(v.Typ.Fields) && v.Typ.Fields[i].Name != "" && v.Typ.Fields[i].Name != f.Name {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "STRUCT field name mismatch ("+v.Typ.Fields[i].Name+" vs "+f.Name+")")
			}
			cv, err := Coerce(v.Coll[i], f.Type)
			if err != nil {
				return Value{}, err
			}
			out[i] = cv
		}
		return StructValue(dest, out), nil
	case KindArray:
		if len(dest.Elem) != 1 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "ARRAY missing element type")
		}
		if len(v.Coll) > MaxCollectionLen {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "ARRAY exceeds MaxCollectionLen")
		}
		out := make([]Value, len(v.Coll))
		for i := range v.Coll {
			cv, err := Coerce(v.Coll[i], dest.Elem[0])
			if err != nil {
				return Value{}, err
			}
			out[i] = cv
		}
		return ArrayValue(dest, out), nil
	case KindMap:
		if len(dest.Key) != 1 || len(dest.Elem) != 1 {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "MAP missing key/value type")
		}
		if len(v.Coll) != len(v.CollKeys) {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "MAP key/value count mismatch")
		}
		if len(v.Coll) > MaxCollectionLen {
			return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "MAP exceeds MaxCollectionLen")
		}
		keys := make([]Value, len(v.CollKeys))
		vals := make([]Value, len(v.Coll))
		for i := range v.CollKeys {
			if v.CollKeys[i].Null {
				return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "MAP key cannot be NULL")
			}
			ck, err := Coerce(v.CollKeys[i], dest.Key[0])
			if err != nil {
				return Value{}, err
			}
			cv, err := Coerce(v.Coll[i], dest.Elem[0])
			if err != nil {
				return Value{}, err
			}
			keys[i], vals[i] = ck, cv
		}
		ok, ov, err := CanonicalizeMap(keys, vals)
		if err != nil {
			return Value{}, err
		}
		return MapValue(dest, ok, ov), nil
	default:
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "not a collection destination")
	}
}

// CanonicalizeMap returns keys/vals reordered into ascending canonical key
// order (Value.Cmp) so that two MAPs with the same entries encode and compare
// identically regardless of construction order, and errors on a duplicate key
// (docs/design-collections.md C3).
func CanonicalizeMap(keys, vals []Value) ([]Value, []Value, error) {
	if len(keys) != len(vals) {
		return nil, nil, nerr.New(nerr.InvalidArgument, "types.CanonicalizeMap", "key/value count mismatch")
	}
	idx := make([]int, len(keys))
	for i := range idx {
		idx[i] = i
	}
	var cmpErr error
	sort.SliceStable(idx, func(a, b int) bool {
		c, err := keys[idx[a]].Cmp(keys[idx[b]])
		if err != nil {
			cmpErr = err
		}
		return c < 0
	})
	if cmpErr != nil {
		return nil, nil, cmpErr
	}
	ok := make([]Value, len(keys))
	ov := make([]Value, len(vals))
	for i, j := range idx {
		ok[i], ov[i] = keys[j], vals[j]
		if i > 0 {
			c, err := ok[i-1].Cmp(ok[i])
			if err != nil {
				return nil, nil, err
			}
			if c == 0 {
				return nil, nil, nerr.New(nerr.InvalidArgument, "types.CanonicalizeMap", "duplicate MAP key")
			}
		}
	}
	return ok, ov, nil
}

// JSONFromText parses UTF-8 JSON into the compact binary stored form.
func JSONFromText(s string) (Value, error) {
	doc, err := nsjson.FromText([]byte(s))
	if err != nil {
		return Value{}, err
	}
	return JSONValue(doc), nil
}

// collectionToJSON coerces an ARRAY/STRUCT/MAP value into JSON: it renders v
// as a generic JSON tree, marshals that to text, and reuses JSONFromText's
// already-validated text->NSJB path rather than hand-rolling a second binary
// encoder for the same document. This is the general fix for coercing any
// collection (nested to any depth) into a JSON column — the driver wire
// protocol sends plain arrays/maps as native ARRAY/MAP values now, and those
// must still be insertable into a JSON column exactly as a JSON-text literal
// already was.
func collectionToJSON(v Value) (Value, error) {
	tree, err := valueToJSONAny(v)
	if err != nil {
		return Value{}, err
	}
	raw, err := json.Marshal(tree)
	if err != nil {
		return Value{}, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot encode collection as JSON: "+err.Error())
	}
	return JSONFromText(string(raw))
}

// valueToJSONAny converts v into a plain Go value (nil / bool / json.Number /
// string / []any / map[string]any) suitable for json.Marshal, recursing into
// nested ARRAY/STRUCT/MAP members. Kinds with no native JSON representation
// (UUID, BLOB, VECTOR, GEOMETRY/GEOGRAPHY, DATE, TIME, TIMESTAMP[TZ],
// INTERVAL, BOX/POINT/LINE/POLYGON) fall back to their existing text form,
// same as CAST-to-string would produce.
func valueToJSONAny(v Value) (any, error) {
	if v.Null {
		return nil, nil
	}
	switch v.Typ.Kind {
	case KindBool:
		return v.Bool, nil
	case KindInt8, KindInt16, KindInt32, KindInt64:
		return json.Number(strconv.FormatInt(v.Int, 10)), nil
	case KindUint8, KindUint16, KindUint32, KindUint64:
		return json.Number(strconv.FormatUint(v.Uint, 10)), nil
	case KindFloat32, KindFloat64:
		if math.IsNaN(float64(v.Flt)) || math.IsInf(float64(v.Flt), 0) {
			return nil, nerr.New(nerr.InvalidArgument, "types.Coerce", "cannot represent NaN/Infinity in JSON")
		}
		return json.Number(FormatFloat(v.Typ.Kind, v.Flt)), nil
	case KindDecimal:
		return json.Number(v.Dec.String()), nil
	case KindString, KindText, KindChar, KindVarchar, KindEnum:
		return v.Str, nil
	case KindJSON:
		// Already JSON: decode back into a generic tree so it nests as a
		// real subdocument instead of being double-encoded as a string.
		if v.JSON == nil {
			return nil, nil
		}
		txt, err := nsjson.ToText(v.JSON)
		if err != nil {
			return nil, err
		}
		var out any
		if err := json.Unmarshal(txt, &out); err != nil {
			return nil, err
		}
		return out, nil
	case KindArray:
		out := make([]any, len(v.Coll))
		for i, e := range v.Coll {
			cv, err := valueToJSONAny(e)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	case KindStruct:
		out := make(map[string]any, len(v.Coll))
		for i, e := range v.Coll {
			name := "?"
			if i < len(v.Typ.Fields) {
				name = v.Typ.Fields[i].Name
			}
			cv, err := valueToJSONAny(e)
			if err != nil {
				return nil, err
			}
			out[name] = cv
		}
		return out, nil
	case KindMap:
		out := make(map[string]any, len(v.Coll))
		for i := range v.Coll {
			if i >= len(v.CollKeys) {
				break
			}
			cv, err := valueToJSONAny(v.Coll[i])
			if err != nil {
				return nil, err
			}
			out[memberString(v.CollKeys[i])] = cv
		}
		return out, nil
	default:
		return v.String(), nil
	}
}

// ExtractJSON reads a path from a binary JSON document without decoding unused siblings.
func ExtractJSON(doc []byte, path []string) (Value, error) {
	if len(doc) == 0 {
		return Null(JSON()), nil
	}
	r, err := nsjson.Extract(doc, path)
	if err != nil {
		return Value{}, err
	}
	switch r.Kind {
	case nsjson.KindMissing, nsjson.KindNull:
		return Null(JSON()), nil
	case nsjson.KindBool:
		return BoolValue(r.Bool), nil
	case nsjson.KindInt:
		return DecimalValue(Decimal{Coef: big.NewInt(r.Int), Scale: 0}, Type{Kind: KindDecimal}), nil
	case nsjson.KindNumber:
		d, err := ParseDecimal(r.Str)
		if err != nil {
			return Value{}, err
		}
		return DecimalValue(d, Type{Kind: KindDecimal, Scale: uint16(d.Scale)}), nil
	case nsjson.KindString:
		return StringValue(r.Str), nil
	case nsjson.KindArray, nsjson.KindObject:
		return JSONValue(r.Raw), nil
	default:
		return Null(JSON()), nil
	}
}

func EncodeUUID(u [16]byte) []byte { return append([]byte(nil), u[:]...) }

func PutF32s(dst []byte, v []float32) {
	for i, f := range v {
		binary.LittleEndian.PutUint32(dst[i*4:], math.Float32bits(f))
	}
}

func F32s(src []byte) []float32 {
	n := len(src) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(src[i*4:]))
	}
	return out
}
