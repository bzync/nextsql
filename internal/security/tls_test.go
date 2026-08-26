package security

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestIsLoopback(t *testing.T) {
	if !IsLoopback("127.0.0.1:7210") || !IsLoopback("localhost:7210") || !IsLoopback("[::1]:1") {
		t.Fatal("loopback rejected")
	}
	if IsLoopback("8.8.8.8:7210") || IsLoopback("db.example.com:7210") {
		t.Fatal("remote treated as loopback")
	}
	if !RequireTLS("db.example.com:7210") || RequireTLS("127.0.0.1:7210") {
		t.Fatal("TLS policy")
	}
}

func TestSelfSignedTLS13(t *testing.T) {
	dir := t.TempDir()
	cert := filepath.Join(dir, "tls.crt")
	key := filepath.Join(dir, "tls.key")
	if err := WriteSelfSigned(cert, key, "localhost"); err != nil {
		t.Fatal(err)
	}
	srv, err := ServerTLS(cert, key)
	if err != nil {
		t.Fatal(err)
	}
	if srv.MinVersion != tls.VersionTLS13 {
		t.Fatalf("min version %d", srv.MinVersion)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srv)
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	done := make(chan error, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer c.Close()
		buf := make([]byte, 4)
		_, err = c.Read(buf)
		done <- err
	}()
	pemBytes, err := os.ReadFile(cert)
	if err != nil {
		t.Fatal(err)
	}
	cli, err := ClientTLSFromPEM("localhost", pemBytes)
	if err != nil {
		t.Fatal(err)
	}
	c, err := tls.Dial("tcp", ln.Addr().String(), cli)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	_ = c.Close()
	<-done
}
