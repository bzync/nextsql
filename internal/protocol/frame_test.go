package protocol

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestFrameRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteFrame(&buf, TypeReady, []byte("ok"), DefaultMaxPacket); err != nil {
		t.Fatal(err)
	}
	typ, payload, err := ReadFrame(&buf, DefaultMaxPacket)
	if err != nil {
		t.Fatal(err)
	}
	if typ != TypeReady || string(payload) != "ok" {
		t.Fatalf("%v %q", typ, payload)
	}
}

func TestReadFrameRejectsHugeLength(t *testing.T) {
	hdr := make([]byte, HeaderSize)
	copy(hdr[0:4], Magic)
	hdr[4] = 1 // version 1
	hdr[6] = byte(TypeQuery)
	// length = 1<<30
	hdr[8] = 0
	hdr[9] = 0
	hdr[10] = 0
	hdr[11] = 0x40
	_, _, err := ReadFrame(bytes.NewReader(hdr), 1024)
	if !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("got %v", err)
	}
}

func TestReadFrameRejectsBadMagic(t *testing.T) {
	hdr := bytes.Repeat([]byte("xxxx"), 3)
	if _, _, err := ReadFrame(bytes.NewReader(hdr), 1024); !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("got %v", err)
	}
}

// TestReadFrameClassifiesTransportFailureAsIO asserts a genuine transport
// failure (connection closed mid-read) is reported as nerr.IO, not
// nerr.Protocol — nothing about the frame's contents was ever examined, so
// it cannot be a protocol violation. This distinction matters to callers
// like nextsql.Cluster's router, which uses the IO code specifically to
// recognize "this connection just broke, stop trusting it and retry
// elsewhere" without risking misclassifying a real protocol error (bad
// magic, unsupported version, oversized packet — all still nerr.Protocol,
// see the tests above) as a transient, retryable condition.
func TestReadFrameClassifiesTransportFailureAsIO(t *testing.T) {
	// Header short-read: fewer bytes than HeaderSize, so io.ReadFull fails
	// with io.ErrUnexpectedEOF before any header field is examined.
	short := make([]byte, HeaderSize-1)
	if _, _, err := ReadFrame(bytes.NewReader(short), 1024); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("header short-read: got %v, want nerr.IO", err)
	}

	// Payload short-read: a valid, fully-formed header declaring a payload
	// longer than what's actually available.
	hdr := make([]byte, HeaderSize)
	copy(hdr[0:4], Magic)
	hdr[4] = byte(Version)
	hdr[6] = byte(TypeQuery)
	hdr[8], hdr[9], hdr[10], hdr[11] = 10, 0, 0, 0 // declares 10 payload bytes (little-endian u32)
	if _, _, err := ReadFrame(bytes.NewReader(hdr), 1024); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("payload short-read: got %v, want nerr.IO", err)
	}
}

func FuzzReadFrame(f *testing.F) {
	var buf bytes.Buffer
	_ = WriteFrame(&buf, TypeHello, []byte{1, 2, 3}, DefaultMaxPacket)
	f.Add(buf.Bytes())
	f.Add([]byte("NSQL"))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, payload, err := ReadFrame(bytes.NewReader(raw), 4096)
		if err != nil {
			if payload != nil {
				t.Fatalf("error with payload")
			}
			return
		}
	})
}
