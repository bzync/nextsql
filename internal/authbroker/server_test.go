package authbroker_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/authbroker"
)

func TestHTTPServerLoopbackLifecycle(t *testing.T) {
	cfg := authbroker.Default()
	cfg.Listen = "127.0.0.1:0"
	srv, err := authbroker.NewHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok\n")
	}))
	if err != nil {
		t.Fatal(err)
	}
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()

	resp, err := http.Get("http://" + srv.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatalf("Serve after clean shutdown: %v", err)
	}
}

func TestHTTPServerRequiresTLSOffLoopback(t *testing.T) {
	cfg := authbroker.Default()
	cfg.Listen = "0.0.0.0:0"
	if _, err := authbroker.NewHTTPServer(cfg, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})); err == nil {
		t.Fatal("non-loopback broker listener accepted without TLS")
	}
	if _, err := authbroker.NewHTTPServer(cfg, nil); err == nil {
		t.Fatal("nil broker handler accepted")
	}
}

func TestHTTPServerTLSIsWrappedExactlyOnce(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := writeServerCertificate(t, dir)
	cfg := authbroker.Default()
	cfg.Listen = "127.0.0.1:0"
	cfg.TLSCert, cfg.TLSKey = certPath, keyPath
	srv, err := authbroker.NewHTTPServer(cfg, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "tls-ok\n")
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve() }()
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{
		MinVersion: tls.VersionTLS13,
		// This test proves listener composition, not certificate trust. The
		// production client still verifies the configured server certificate.
		InsecureSkipVerify: true, //nolint:gosec
	}}}
	resp, err := client.Get("https://" + srv.Addr().String())
	if err != nil {
		t.Fatalf("single TLS handshake failed: %v", err)
	}
	_ = resp.Body.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

func writeServerCertificate(t *testing.T, dir string) (string, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, priv)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	certPath := filepath.Join(dir, "server.crt")
	keyPath := filepath.Join(dir, "server.key")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		t.Fatal(err)
	}
	return certPath, keyPath
}
