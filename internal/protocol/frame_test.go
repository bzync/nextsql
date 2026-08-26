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
