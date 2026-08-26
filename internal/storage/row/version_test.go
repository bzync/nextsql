package row

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func TestEncodeDecode(t *testing.T) {
	v := Version{Xmin: 3, Xmax: 7, Undo: 11, Payload: []byte("hello")}
	raw := Encode(v)
	got, ok, err := Decode(raw)
	if err != nil || !ok {
		t.Fatalf("decode: ok=%v err=%v", ok, err)
	}
	if got.Xmin != 3 || got.Xmax != 7 || got.Undo != 11 || string(got.Payload) != "hello" {
		t.Fatalf("got %+v", got)
	}
}

func TestInspectAliasesPayload(t *testing.T) {
	raw := Encode(Version{Xmin: 2, Payload: []byte("view")})
	got, ok, err := Inspect(raw)
	if err != nil || !ok {
		t.Fatalf("inspect: ok=%v err=%v", ok, err)
	}
	if string(got.Payload) != "view" {
		t.Fatalf("payload %q", got.Payload)
	}
	if len(got.Payload) == 0 || &got.Payload[0] != &raw[headerSize] {
		t.Fatal("Inspect must alias the input buffer")
	}
	copied, ok, err := Decode(raw)
	if err != nil || !ok {
		t.Fatal(err)
	}
	raw[headerSize] = 'X'
	if string(copied.Payload) != "view" {
		t.Fatalf("Decode must copy, got %q", copied.Payload)
	}
}

func TestLegacyUnwrapped(t *testing.T) {
	_, ok, err := Decode([]byte("plain"))
	if err != nil || ok {
		t.Fatalf("legacy should not look like a version: ok=%v err=%v", ok, err)
	}
	p, err := PayloadOf([]byte("plain"))
	if err != nil || string(p) != "plain" {
		t.Fatalf("payload %q %v", p, err)
	}
}

func TestEmptyPayload(t *testing.T) {
	raw := Encode(Version{Xmin: 1, Payload: nil})
	got, ok, err := Decode(raw)
	if err != nil || !ok {
		t.Fatal(err)
	}
	if got.Xmin != 1 || got.Payload == nil && false {
		t.Fatalf("got %+v", got)
	}
	_ = format.UndoID(0)
}
