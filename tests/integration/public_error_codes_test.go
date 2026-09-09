package integration

import (
	"context"
	"crypto/tls"
	"errors"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/protocol"
)

// The official Go driver negotiates the taxonomy, and a real server error then
// arrives with both spellings: the unchanged legacy class that existing retry
// logic reads, and the stable public name.
func TestGoDriverReceivesPublicErrorCode(t *testing.T) {
	addr, clientTLS := startTLSServer(t)
	conn := openApp(t, addr, clientTLS)
	if !conn.PublicErrorCodes() {
		t.Fatal("driver did not negotiate the public error taxonomy")
	}
	_, err := conn.Query(context.Background(), "SELECT * FROM no_such_table")
	if err == nil {
		t.Fatal("expected an error from an unknown table")
	}
	var e *nerr.Error
	if !errors.As(err, &e) {
		t.Fatalf("error is %T, not the long-standing *nerr.Error the driver has always returned", err)
	}
	if e.Code == "" {
		t.Fatalf("legacy class missing: %+v", e)
	}
	public, ok := nerr.PublicCodeFor(e.Code)
	if !ok {
		t.Fatalf("server returned undocumented class %q", e.Code)
	}
	if e.Public != public {
		t.Fatalf("Public = %q, want %q for class %q", e.Public, public, e.Code)
	}
	// Legacy retry checks must still work unchanged.
	if !nerr.HasCode(err, e.Code) {
		t.Fatal("nerr.HasCode stopped matching the legacy class")
	}
}

// Negative control: a client that never sets the capability bit must receive
// the exact NSQL v1 frames. This is hand-rolled rather than driven through the
// driver on purpose — the driver now always negotiates, so only a raw v1 client
// can prove the old shape is still what an un-upgraded deployment sees.
func TestV1ClientStillSeesTheLegacyErrorFrame(t *testing.T) {
	addr, clientTLS := startTLSServer(t)
	lim := protocol.Limits{}
	raw, err := tls.Dial("tcp", addr, clientTLS)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = raw.Close() }()

	// Flags is deliberately zero: this is the pre-capability Hello.
	hello, err := protocol.EncodeHello(protocol.Hello{
		Version:  protocol.Version,
		Database: "production",
		User:     "app",
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteFrame(raw, protocol.TypeHello, hello, 0); err != nil {
		t.Fatal(err)
	}
	typ, body, err := protocol.ReadFrame(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if typ != protocol.TypeHelloOK {
		t.Fatalf("frame type %v, want hello-ok", typ)
	}
	if len(body) != 11 {
		t.Fatalf("hello-ok is %d bytes for a v1 client, want the 11-byte v1 shape", len(body))
	}
	ok, err := protocol.DecodeHelloOK(body)
	if err != nil {
		t.Fatal(err)
	}
	if ok.Flags != 0 {
		t.Fatalf("server claimed capabilities %#x to a client that requested none", ok.Flags)
	}

	// Wrong password: the first real error frame this connection can provoke.
	authPayload, err := protocol.EncodeAuth(protocol.Auth{Password: "wrong"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if err := protocol.WriteFrame(raw, protocol.TypeAuth, authPayload, 0); err != nil {
		t.Fatal(err)
	}
	typ, body, err = protocol.ReadFrame(raw, 0)
	if err != nil {
		t.Fatal(err)
	}
	if typ != protocol.TypeError {
		t.Fatalf("frame type %v, want error", typ)
	}
	msg, err := protocol.DecodeError(body, lim)
	if err != nil {
		t.Fatal(err)
	}
	if msg.PublicCode != "" {
		t.Fatalf("server sent public code %q to a v1 client", msg.PublicCode)
	}
	if _, ok := nerr.ParsePublicCode(string(msg.Code)); ok {
		t.Fatalf("legacy class field was replaced with a public code: %q", msg.Code)
	}
}
