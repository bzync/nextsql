package json

import (
	"bytes"
	"strings"
	"testing"
)

func TestFromTextRoundTrip(t *testing.T) {
	cases := []string{
		`null`,
		`true`,
		`false`,
		`0`,
		`-1`,
		`42`,
		`1.5`,
		`1e2`,
		`""`,
		`"hello"`,
		`"quote \" and \\ slash"`,
		`[]`,
		`[1,2,3]`,
		`{}`,
		`{"a":1}`,
		`{"category":"electronics","n":2}`,
		`{"nested":{"x":true},"arr":[null,"z"]}`,
		`"café"`,
		`"\uD83D\uDE00"`,
	}
	for _, src := range cases {
		doc, err := FromText([]byte(src))
		if err != nil {
			t.Fatalf("%s: %v", src, err)
		}
		if err := Validate(doc); err != nil {
			t.Fatalf("%s validate: %v", src, err)
		}
		if !IsDoc(doc) {
			t.Fatalf("%s: not NSJB", src)
		}
		if bytes.Contains(doc[5:], []byte(src)) && strings.ContainsAny(src, "{}") && src != `{}` && src != `[]` {
			// object/array text must not be stored as UTF-8 JSON
			t.Fatalf("stored plaintext JSON for %s: %q", src, doc)
		}
		out, err := ToText(doc)
		if err != nil {
			t.Fatalf("%s ToText: %v", src, err)
		}
		// Re-parse text form and compare binary (canonical key order).
		doc2, err := FromText(out)
		if err != nil {
			t.Fatalf("%s reparse %s: %v", src, out, err)
		}
		if !bytes.Equal(doc, doc2) {
			t.Fatalf("%s binary changed: %q vs %q (%s)", src, doc, doc2, out)
		}
	}
}

func TestObjectKeyOrderCanonical(t *testing.T) {
	a, err := FromText([]byte(`{"b":1,"a":2}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := FromText([]byte(`{"a":2,"b":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(a, b) {
		t.Fatalf("key order should be canonical\n%x\n%x", a, b)
	}
	txt, err := ToText(a)
	if err != nil {
		t.Fatal(err)
	}
	if string(txt) != `{"a":2,"b":1}` {
		t.Fatalf("got %s", txt)
	}
}

func TestDuplicateKeyLastWins(t *testing.T) {
	doc, err := FromText([]byte(`{"a":1,"a":2}`))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Extract(doc, []string{"a"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != KindInt || got.Int != 2 {
		t.Fatalf("%+v", got)
	}
}

func TestExtractPath(t *testing.T) {
	doc, err := FromText([]byte(`{"category":"electronics","specs":{"color":"red"},"tags":["a","b"],"ok":true,"n":null}`))
	if err != nil {
		t.Fatal(err)
	}
	s, err := Extract(doc, []string{"category"})
	if err != nil || s.Kind != KindString || s.Str != "electronics" {
		t.Fatalf("%+v %v", s, err)
	}
	c, err := Extract(doc, []string{"specs", "color"})
	if err != nil || c.Kind != KindString || c.Str != "red" {
		t.Fatalf("%+v %v", c, err)
	}
	tag, err := Extract(doc, []string{"tags", "1"})
	if err != nil || tag.Kind != KindString || tag.Str != "b" {
		t.Fatalf("%+v %v", tag, err)
	}
	miss, err := Extract(doc, []string{"missing"})
	if err != nil || miss.Kind != KindMissing {
		t.Fatalf("missing %+v %v", miss, err)
	}
	n, err := Extract(doc, []string{"n"})
	if err != nil || n.Kind != KindNull {
		t.Fatalf("null %+v %v", n, err)
	}
	obj, err := Extract(doc, []string{"specs"})
	if err != nil || obj.Kind != KindObject || !IsDoc(obj.Raw) {
		t.Fatalf("obj %+v %v", obj, err)
	}
	inner, err := Extract(obj.Raw, []string{"color"})
	if err != nil || inner.Str != "red" {
		t.Fatalf("inner %+v %v", inner, err)
	}
}

func TestPartialDecodeSkipsSiblings(t *testing.T) {
	// Large unused sibling must not need full materialization of its text.
	big := `{"keep":"yes","blob":"` + strings.Repeat("x", 10000) + `"}`
	doc, err := FromText([]byte(big))
	if err != nil {
		t.Fatal(err)
	}
	got, err := Extract(doc, []string{"keep"})
	if err != nil || got.Str != "yes" {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestDepthLimit(t *testing.T) {
	var b strings.Builder
	for i := 0; i < MaxDepth+2; i++ {
		b.WriteByte('[')
	}
	b.WriteByte('1')
	for i := 0; i < MaxDepth+2; i++ {
		b.WriteByte(']')
	}
	_, err := FromText([]byte(b.String()))
	if err == nil {
		t.Fatal("expected depth error")
	}
}

func TestRejectMalformed(t *testing.T) {
	for _, src := range []string{
		``,
		`{`,
		`[1,]`,
		`{"a"}`,
		`{"a":1,}`,
		`'x'`,
		`01`,
		`-`,
		`{"a":1} trailing`,
		"\"\x00\"",
		`{"a":` + strings.Repeat("[", MaxDepth+1) + strings.Repeat("]", MaxDepth+1) + `}`,
	} {
		if _, err := FromText([]byte(src)); err == nil {
			t.Fatalf("accepted %q", src)
		}
	}
}

func TestValidateRejectsTruncated(t *testing.T) {
	doc, err := FromText([]byte(`{"a":[1,2,3]}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := Validate(doc[:len(doc)-1]); err == nil {
		t.Fatal("expected truncated")
	}
	if err := Validate([]byte("NSJB\x02")); err == nil {
		t.Fatal("expected version error")
	}
	if err := Validate([]byte(`{"a":1}`)); err == nil {
		t.Fatal("expected not binary")
	}
}

func TestNotPlaintextStoredForm(t *testing.T) {
	src := []byte(`{"category":"electronics"}`)
	doc, err := FromText(src)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(doc, src) {
		t.Fatalf("binary still contains UTF-8 JSON: %q", doc)
	}
	if !bytes.HasPrefix(doc, []byte(Magic)) {
		t.Fatal("missing magic")
	}
}

func TestSetRemoveContains(t *testing.T) {
	doc, err := FromText([]byte(`{"a":{"n":1},"items":[1,2,3]}`))
	if err != nil {
		t.Fatal(err)
	}
	ten, _ := FromText([]byte(`10`))
	doc, err = Set(doc, []string{"a", "n"}, ten)
	if err != nil {
		t.Fatal(err)
	}
	value, err := Extract(doc, []string{"a", "n"})
	if err != nil || value.Kind != KindInt || value.Int != 10 {
		t.Fatalf("set result=%+v err=%v", value, err)
	}
	doc, err = Remove(doc, []string{"items", "1"})
	if err != nil {
		t.Fatal(err)
	}
	n, found, err := ArrayLength(doc, []string{"items"})
	if err != nil || !found || n != 2 {
		t.Fatalf("remove array length=%d found=%v err=%v", n, found, err)
	}
	target, _ := FromText([]byte(`{"a":{"n":10}}`))
	ok, err := Contains(doc, target)
	if err != nil || !ok {
		t.Fatalf("contains=%v err=%v", ok, err)
	}
}
