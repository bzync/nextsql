package protocol

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"strings"
	"testing"
	"time"
)

type panicReadConn struct{ net.Conn }

func (panicReadConn) Read([]byte) (int, error) {
	panic("sensitive authentication material")
}

func TestServeConnContainsPreAuthPanic(t *testing.T) {
	srv := NewServer(nil, nil)
	var logs bytes.Buffer
	srv.Log = slog.New(slog.NewTextHandler(&logs, nil))
	raw, client := net.Pipe()
	defer client.Close()
	conn := panicReadConn{raw}
	srv.conns[conn] = struct{}{}
	srv.nconn = 1
	done := make(chan struct{})
	go func() {
		srv.serveConn(context.Background(), conn)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("session did not exit after panic")
	}
	if srv.nconn != 0 || len(srv.conns) != 0 {
		t.Fatalf("leaked connection accounting: count=%d connections=%d", srv.nconn, len(srv.conns))
	}
	if _, err := client.Write([]byte("hello")); err == nil {
		t.Fatal("connection remained open after panic")
	}
	if !strings.Contains(logs.String(), "session panic") || strings.Contains(logs.String(), "sensitive") {
		t.Fatalf("missing or unsafe panic diagnostic: %s", &logs)
	}
}
