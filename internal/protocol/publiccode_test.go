package protocol

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

// A client that asks for nothing must see byte-identical frames to the ones the
// pre-capability server wrote. These are the exact shapes NSQL v1 clients parse.
func TestV1ShapesAreByteIdentical(t *testing.T) {
	ok := EncodeHelloOK(HelloOK{Version: Version, AuthMethod: 1, Secret: 0x0102030405060708})
	if len(ok) != 11 {
		t.Fatalf("hello-ok for a v1 client is %d bytes, want 11", len(ok))
	}
	got, err := EncodeError(errorFrom(nerr.New(nerr.Deadlock, "op", "boom"), false), Limits{})
	if err != nil {
		t.Fatal(err)
	}
	want, err := appendU16String(nil, "deadlock", DefaultMaxName)
	if err != nil {
		t.Fatal(err)
	}
	if want, err = appendU16String(want, "boom", DefaultMaxName); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("error frame for a v1 client changed shape:\n got %x\nwant %x", got, want)
	}
}

// The negotiated shape carries the stable name without disturbing the legacy
// class, and a v1 decoder is what must reject it — never a v1 client's frame.
func TestNegotiatedErrorCarriesPublicCode(t *testing.T) {
	msg := errorFrom(nerr.New(nerr.Serialization, "op", "retry me"), true)
	if msg.Code != "serialization" {
		t.Fatalf("legacy class changed to %q; existing retry checks would break", msg.Code)
	}
	if msg.PublicCode != "ERR_SERIALIZATION" {
		t.Fatalf("public code = %q, want ERR_SERIALIZATION", msg.PublicCode)
	}
	buf, err := EncodeError(msg, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	back, err := DecodeError(buf, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if back != msg {
		t.Fatalf("round trip = %+v, want %+v", back, msg)
	}
}

// A class outside the documented table has no reserved public spelling, so the
// field is omitted rather than invented (nerr.PublicCodeFor fails closed).
func TestUndocumentedClassEmitsNoPublicCode(t *testing.T) {
	msg := errorFrom(&nerr.Error{Code: nerr.Code("some_future_class"), Message: "x"}, true)
	if msg.PublicCode != "" {
		t.Fatalf("public code = %q for an undocumented class, want empty", msg.PublicCode)
	}
}

// A new client must keep working against a server that ignores the request bit,
// which is exactly the two-field shape.
func TestNewClientDecodesLegacyErrorShape(t *testing.T) {
	buf, err := appendU16String(nil, "conflict", DefaultMaxName)
	if err != nil {
		t.Fatal(err)
	}
	if buf, err = appendU16String(buf, "nope", DefaultMaxName); err != nil {
		t.Fatal(err)
	}
	msg, err := DecodeError(buf, Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Code != "conflict" || msg.PublicCode != "" {
		t.Fatalf("legacy decode = %+v, want conflict with no public code", msg)
	}
}

func TestHelloOKFlagsRoundTrip(t *testing.T) {
	in := HelloOK{Version: Version, AuthMethod: 2, Secret: 42, Flags: FlagPublicErrorCodes}
	buf := EncodeHelloOK(in)
	if len(buf) != 13 {
		t.Fatalf("negotiated hello-ok is %d bytes, want 13", len(buf))
	}
	out, err := DecodeHelloOK(buf)
	if err != nil {
		t.Fatal(err)
	}
	if out != in {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
	// A v1 hello-ok still decodes, with no capabilities claimed.
	out, err = DecodeHelloOK(EncodeHelloOK(HelloOK{Version: Version, AuthMethod: 2, Secret: 42}))
	if err != nil {
		t.Fatal(err)
	}
	if out.Flags != 0 {
		t.Fatalf("v1 hello-ok claimed flags %#x", out.Flags)
	}
	if _, err := DecodeHelloOK(make([]byte, 12)); err == nil {
		t.Fatal("a 12-byte hello-ok must be rejected")
	}
}

// The server must never echo a bit it does not implement: a client treats an
// echoed bit as a guarantee.
func TestAcceptCapsNarrowsToImplementedBits(t *testing.T) {
	caps := acceptCaps(0xFFFF)
	if uint16(caps) != ServerFlags {
		t.Fatalf("accepted %#x, want exactly %#x", uint16(caps), ServerFlags)
	}
	if !caps.publicErrorCodes() {
		t.Fatal("public error codes not accepted")
	}
	if acceptCaps(FlagCancel).publicErrorCodes() {
		t.Fatal("a cancel-only hello must not negotiate the taxonomy")
	}
	if acceptCaps(0) != 0 {
		t.Fatal("a v1 hello must negotiate nothing")
	}
}

// Every documented class must have a reserved spelling that parses back, or a
// client cannot map what the server sends onto its own retry logic.
func TestEveryDocumentedClassRoundTripsThroughTheWire(t *testing.T) {
	for _, c := range []nerr.Code{
		nerr.InvalidArgument, nerr.InvalidFormat, nerr.Corruption, nerr.NotFound,
		nerr.AlreadyExists, nerr.PageFull, nerr.Exhausted, nerr.Crypto, nerr.IO,
		nerr.Internal, nerr.Unavailable, nerr.Conflict, nerr.Deadlock,
		nerr.Serialization, nerr.Syntax, nerr.Unauthorized, nerr.Forbidden,
		nerr.Protocol, nerr.Canceled, nerr.ForeignKey,
	} {
		msg := errorFrom(nerr.New(c, "op", "m"), true)
		if msg.PublicCode == "" {
			t.Fatalf("%s has no public code on the wire", c)
		}
		buf, err := EncodeError(msg, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		back, err := DecodeError(buf, Limits{})
		if err != nil {
			t.Fatal(err)
		}
		parsed, ok := nerr.ParsePublicCode(back.PublicCode)
		if !ok || parsed != c {
			t.Fatalf("%s round trip: %q parsed to %q ok=%v", c, back.PublicCode, parsed, ok)
		}
	}
}

func FuzzDecodeError(f *testing.F) {
	legacy, _ := EncodeError(ErrorMsg{Code: "conflict", Message: "m"}, DefaultLimits())
	f.Add(legacy)
	negotiated, _ := EncodeError(ErrorMsg{Code: "conflict", Message: "m", PublicCode: "ERR_CONFLICT"}, DefaultLimits())
	f.Add(negotiated)
	f.Add([]byte{0, 0})
	f.Add([]byte{0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		msg, err := DecodeError(b, DefaultLimits())
		if err != nil {
			return
		}
		// Anything accepted must re-encode to the same bytes, or the two
		// shapes are not distinguishable and a peer could be fed a frame
		// that means one thing on encode and another on decode.
		out, err := EncodeError(msg, DefaultLimits())
		if err != nil {
			t.Fatalf("re-encoding an accepted error failed: %v", err)
		}
		if !bytes.Equal(out, b) {
			t.Fatalf("round trip changed bytes:\n got %x\nwant %x", out, b)
		}
	})
}

func FuzzDecodeHelloOK(f *testing.F) {
	f.Add(EncodeHelloOK(HelloOK{Version: 1, AuthMethod: 1, Secret: 7}))
	f.Add(EncodeHelloOK(HelloOK{Version: 1, AuthMethod: 1, Secret: 7, Flags: FlagPublicErrorCodes}))
	f.Add([]byte{0, 1})
	f.Fuzz(func(t *testing.T, b []byte) {
		h, err := DecodeHelloOK(b)
		if err != nil {
			return
		}
		if len(b) != 11 && len(b) != 13 {
			t.Fatalf("accepted a %d-byte hello-ok", len(b))
		}
		if len(b) == 11 && h.Flags != 0 {
			t.Fatal("v1 hello-ok decoded with capabilities set")
		}
		if !bytes.Equal(EncodeHelloOK(h), b) {
			t.Fatalf("round trip changed bytes: %x -> %x", b, EncodeHelloOK(h))
		}
	})
}
