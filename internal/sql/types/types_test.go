package types

import (
	"bytes"
	"testing"
)

func TestDecimalFromInt64(t *testing.T) {
	for _, n := range []int64{0, 1, -1, 255, 256, 9999999, -42} {
		got := DecimalFromInt64(n)
		want, err := ParseDecimal(itoa64(n))
		if err != nil {
			t.Fatal(err)
		}
		if got.Cmp(want) != 0 {
			t.Fatalf("%d: got %s want %s", n, got.String(), want.String())
		}
	}
}

func itoa64(n int64) string {
	if n == 0 {
		return "0"
	}
	var tmp [24]byte
	i := len(tmp)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		tmp[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		tmp[i] = '-'
	}
	return string(tmp[i:])
}

func TestDecimalParseCmpRescale(t *testing.T) {
	a, err := ParseDecimal("1000.5")
	if err != nil {
		t.Fatal(err)
	}
	b, err := ParseDecimal("1000.50")
	if err != nil {
		t.Fatal(err)
	}
	if a.Cmp(b) != 0 {
		t.Fatalf("1000.5 vs 1000.50: %d", a.Cmp(b))
	}
	c, err := a.Rescale(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	if c.String() != "1000.50" {
		t.Fatalf("got %s", c.String())
	}
	lossy, err := ParseDecimal("1.234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := lossy.Rescale(4, 2); err == nil {
		t.Fatal("expected scale loss")
	}
}

func TestReplaceRowColumn(t *testing.T) {
	dec, err := DecimalType(10, 0)
	if err != nil {
		t.Fatal(err)
	}
	d1, _ := ParseDecimal("42")
	d0, _ := ParseDecimal("0")
	cols := []Type{String(), String(), dec}
	raw, err := EncodeRow([]Value{StringValue("s1"), StringValue("a"), DecimalValue(d1, dec)})
	if err != nil {
		t.Fatal(err)
	}
	got, err := ReplaceRowColumn(raw, cols, 2, DecimalValue(d0, dec))
	if err != nil {
		t.Fatal(err)
	}
	row, err := DecodeRow(got, cols)
	if err != nil {
		t.Fatal(err)
	}
	if row[0].Str != "s1" || row[1].Str != "a" || row[2].Dec.String() != "0" {
		t.Fatalf("%+v", row)
	}
	into, err := ReplaceRowColumnInto(nil, raw, cols, 2, DecimalValue(d0, dec))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, into) {
		t.Fatal("ReplaceRowColumnInto mismatch")
	}
	b, null, err := PeekRowColumn(raw, cols, 1)
	if err != nil || null || string(b) != "a" {
		t.Fatalf("peek k %q null=%v err=%v", b, null, err)
	}
}

func TestRowRoundTrip(t *testing.T) {
	dec, err := DecimalType(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	d, _ := ParseDecimal("12.50")
	d, _ = d.Rescale(12, 2)
	u, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	vals := []Value{
		u,
		StringValue("hello"),
		Null(Text()),
		DecimalValue(d, dec),
		Now(),
		mustJSON(t, `{"a":1}`),
	}
	raw, err := EncodeRow(vals)
	if err != nil {
		t.Fatal(err)
	}
	cols := []Type{UUID(), String(), Text(), dec, TimestampTZ(), JSON()}
	got, err := DecodeRow(raw, cols)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(vals) {
		t.Fatalf("len %d", len(got))
	}
	if got[0].UUID != vals[0].UUID || got[1].Str != "hello" || !got[2].Null {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	if got[3].Dec.Cmp(d) != 0 {
		t.Fatalf("decimal %s", got[3].Dec.String())
	}
	if !bytes.Equal(got[5].JSON, vals[5].JSON) {
		t.Fatalf("json %q", got[5].JSON)
	}
	if got[5].String() != `{"a":1}` {
		t.Fatalf("json text %s", got[5].String())
	}
	col1, err := DecodeRowColumn(raw, cols, 1)
	if err != nil || col1.Str != "hello" {
		t.Fatalf("column 1 %q %v", col1.Str, err)
	}
	col2, err := DecodeRowColumn(raw, cols, 2)
	if err != nil || !col2.Null {
		t.Fatalf("column 2 null %v %v", col2.Null, err)
	}
}

func TestEncodeKeyOrder(t *testing.T) {
	a, _ := EncodeKey([]Value{StringValue("a")})
	b, _ := EncodeKey([]Value{StringValue("b")})
	if bytes.Compare(a, b) >= 0 {
		t.Fatalf("string key order")
	}
	d1, _ := ParseDecimal("10")
	d2, _ := ParseDecimal("20")
	k1, _ := EncodeKey([]Value{DecimalValue(d1, Type{Kind: KindDecimal, Precision: 12, Scale: 0})})
	k2, _ := EncodeKey([]Value{DecimalValue(d2, Type{Kind: KindDecimal, Precision: 12, Scale: 0})})
	if bytes.Compare(k1, k2) >= 0 {
		t.Fatalf("decimal key order")
	}
}

func TestDecodeKeyRoundTrip(t *testing.T) {
	dec, err := DecimalType(12, 2)
	if err != nil {
		t.Fatal(err)
	}
	dneg, _ := ParseDecimal("-256.50")
	dneg, _ = dneg.Rescale(12, 2)
	dpos, _ := ParseDecimal("10.25")
	dpos, _ = dpos.Rescale(12, 2)
	u, err := NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	vals := []Value{u, StringValue("a\x00b"), DecimalValue(dneg, dec), DecimalValue(dpos, dec), Null(String()), BoolValue(true)}
	cols := []Type{UUID(), String(), dec, dec, String(), Bool()}
	raw, err := EncodeKey(vals)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeKey(raw, cols)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(vals) {
		t.Fatalf("len %d", len(got))
	}
	if got[0].UUID != u.UUID || got[1].Str != "a\x00b" || !got[4].Null || !got[5].Bool {
		t.Fatalf("%+v", got)
	}
	if got[2].Dec.Cmp(dneg) != 0 || got[3].Dec.Cmp(dpos) != 0 {
		t.Fatalf("dec %s %s", got[2].Dec.String(), got[3].Dec.String())
	}
	n1, _ := EncodeKey([]Value{DecimalValue(mustDecVal(t, "-256"), Type{Kind: KindDecimal})})
	n2, _ := EncodeKey([]Value{DecimalValue(mustDecVal(t, "-1"), Type{Kind: KindDecimal})})
	if bytes.Compare(n1, n2) >= 0 {
		t.Fatalf("negative decimal order")
	}
	p := []byte{1, 2, 3}
	end := PrefixEnd(p)
	if bytes.Compare(p, end) >= 0 {
		t.Fatalf("prefix end %v %v", p, end)
	}
}

func mustDecVal(t *testing.T, s string) Decimal {
	t.Helper()
	d, err := ParseDecimal(s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func mustJSON(t *testing.T, src string) Value {
	t.Helper()
	v, err := JSONFromText(src)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestExtractJSONPath(t *testing.T) {
	doc := mustJSON(t, `{"category":"electronics","n":2}`)
	got, err := ExtractJSON(doc.JSON, []string{"category"})
	if err != nil || got.Str != "electronics" {
		t.Fatalf("%+v %v", got, err)
	}
	n, err := ExtractJSON(doc.JSON, []string{"n"})
	if err != nil || n.Dec.String() != "2" {
		t.Fatalf("%+v %v", n, err)
	}
	miss, err := ExtractJSON(doc.JSON, []string{"nope"})
	if err != nil || !miss.Null {
		t.Fatalf("missing %+v %v", miss, err)
	}
}

func TestCoerceJSONRejectsTextBlob(t *testing.T) {
	v, err := Coerce(StringValue(`{"a":1}`), JSON())
	if err != nil {
		t.Fatal(err)
	}
	if v.String() != `{"a":1}` {
		t.Fatalf("%s", v.String())
	}
	if bytes.Equal(v.JSON, []byte(`{"a":1}`)) {
		t.Fatal("stored UTF-8 JSON text")
	}
	if _, err := Coerce(StringValue(`{`), JSON()); err == nil {
		t.Fatal("expected invalid JSON")
	}
}
