package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang.org/x/crypto/ocsp"
)

func generateTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func generateTestLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial int64, responderURL string) (*x509.Certificate, *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "Test Leaf"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}
	if responderURL != "" {
		tmpl.OCSPServer = []string{responderURL}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert, key
}

func makeOCSPResponse(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serial *big.Int, status int, thisUpdate, nextUpdate time.Time) []byte {
	t.Helper()
	tmpl := ocsp.Response{
		Status:       status,
		SerialNumber: serial,
		ThisUpdate:   thisUpdate,
		NextUpdate:   nextUpdate,
	}
	if status == ocsp.Revoked {
		tmpl.RevokedAt = time.Now().Add(-10 * time.Minute)
		tmpl.RevocationReason = ocsp.KeyCompromise
	}
	respDER, err := ocsp.CreateResponse(ca, ca, tmpl, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return respDER
}

func TestParseOCSPMode(t *testing.T) {
	tests := []struct {
		in      string
		want    OCSPMode
		wantErr bool
	}{
		{"", OCSPModeDisabled, false},
		{"disabled", OCSPModeDisabled, false},
		{"DISABLED", OCSPModeDisabled, false},
		{"optional", OCSPModeOptional, false},
		{"enforce", OCSPModeEnforce, false},
		{"required", OCSPModeEnforce, false},
		{"invalid", "", true},
	}
	for _, tc := range tests {
		got, err := ParseOCSPMode(tc.in)
		if tc.wantErr && err == nil {
			t.Errorf("ParseOCSPMode(%q) expected error, got nil", tc.in)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("ParseOCSPMode(%q) unexpected error: %v", tc.in, err)
		}
		if got != tc.want {
			t.Errorf("ParseOCSPMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestVerifyOCSP_Disabled(t *testing.T) {
	ca, caKey := generateTestCA(t)
	leaf, _ := generateTestLeaf(t, ca, caKey, 10, "")
	cfg := OCSPConfig{Mode: OCSPModeDisabled}
	if err := VerifyOCSP(leaf, ca, nil, cfg); err != nil {
		t.Fatalf("disabled mode should succeed: %v", err)
	}
}

func TestVerifyOCSP_Stapled(t *testing.T) {
	ca, caKey := generateTestCA(t)
	leaf, _ := generateTestLeaf(t, ca, caKey, 20, "")
	now := time.Now()

	// Good stapled response
	goodResp := makeOCSPResponse(t, ca, caKey, leaf.SerialNumber, ocsp.Good, now.Add(-time.Minute), now.Add(time.Hour))
	cfg := OCSPConfig{Mode: OCSPModeEnforce}
	if err := VerifyOCSP(leaf, ca, goodResp, cfg); err != nil {
		t.Fatalf("good stapled response rejected: %v", err)
	}

	// Revoked stapled response
	revokedResp := makeOCSPResponse(t, ca, caKey, leaf.SerialNumber, ocsp.Revoked, now.Add(-time.Minute), now.Add(time.Hour))
	if err := VerifyOCSP(leaf, ca, revokedResp, cfg); err == nil {
		t.Fatal("revoked stapled response accepted")
	}

	// Expired stapled response: in enforce mode without a responder, fails
	expiredResp := makeOCSPResponse(t, ca, caKey, leaf.SerialNumber, ocsp.Good, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err := VerifyOCSP(leaf, ca, expiredResp, cfg); err == nil {
		t.Fatal("expired stapled response accepted without responder")
	}
}

func TestVerifyOCSP_LiveResponder(t *testing.T) {
	ca, caKey := generateTestCA(t)
	now := time.Now()

	var respBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	}))
	defer srv.Close()

	leaf, _ := generateTestLeaf(t, ca, caKey, 30, srv.URL)

	// Good response from responder
	respBytes = makeOCSPResponse(t, ca, caKey, leaf.SerialNumber, ocsp.Good, now.Add(-time.Minute), now.Add(time.Hour))
	cfg := OCSPConfig{
		Mode:       OCSPModeEnforce,
		HTTPClient: srv.Client(),
	}
	if err := VerifyOCSP(leaf, ca, nil, cfg); err != nil {
		t.Fatalf("good responder response rejected: %v", err)
	}

	// Cached response should be good even if server goes offline
	srv.CloseClientConnections()
	if err := VerifyOCSP(leaf, ca, nil, cfg); err != nil {
		t.Fatalf("cached good response rejected: %v", err)
	}

	// New cert serial 31 - revoked
	leafRevoked, _ := generateTestLeaf(t, ca, caKey, 31, srv.URL)
	respBytes = makeOCSPResponse(t, ca, caKey, leafRevoked.SerialNumber, ocsp.Revoked, now.Add(-time.Minute), now.Add(time.Hour))
	if err := VerifyOCSP(leafRevoked, ca, nil, cfg); err == nil {
		t.Fatal("revoked responder response accepted")
	}
}

func TestVerifyOCSP_LiveResponderRejectsInvalidTimeWindow(t *testing.T) {
	ca, caKey := generateTestCA(t)
	now := time.Now().UTC()

	var respBytes []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(respBytes)
	}))
	defer srv.Close()

	leaf, _ := generateTestLeaf(t, ca, caKey, 32, srv.URL)
	cfg := OCSPConfig{Mode: OCSPModeEnforce, Now: func() time.Time { return now }, HTTPClient: srv.Client()}

	respBytes = makeOCSPResponse(t, ca, caKey, leaf.SerialNumber, ocsp.Good, now.Add(-2*time.Hour), now.Add(-time.Hour))
	if err := VerifyOCSP(leaf, ca, nil, cfg); err == nil {
		t.Fatal("expired live responder response accepted")
	}

	respBytes = makeOCSPResponse(t, ca, caKey, leaf.SerialNumber, ocsp.Good, now.Add(time.Hour), now.Add(2*time.Hour))
	if err := VerifyOCSP(leaf, ca, nil, cfg); err == nil {
		t.Fatal("not-yet-valid live responder response accepted")
	}
}

func TestOCSPCacheOrderingRemainsBounded(t *testing.T) {
	cache := &ocspCache{entries: make(map[string]cachedOCSPStatus)}
	now := time.Now().UTC()
	entry := cachedOCSPStatus{status: ocsp.Good, nextUpdate: now.Add(time.Hour)}

	for i := 0; i < maxOCSPCacheEntries*3; i++ {
		cache.put("same", entry)
	}
	if got := len(cache.order); got != 1 {
		t.Fatalf("repeated refresh grew cache order to %d entries", got)
	}

	for i := 0; i < maxOCSPCacheEntries*2; i++ {
		cache.put(big.NewInt(int64(i)).String(), entry)
	}
	if got := len(cache.entries); got > maxOCSPCacheEntries {
		t.Fatalf("cache entries = %d, max %d", got, maxOCSPCacheEntries)
	}
	if got := len(cache.order); got > maxOCSPCacheEntries {
		t.Fatalf("cache order = %d, max %d", got, maxOCSPCacheEntries)
	}
}

func TestVerifyOCSP_EnforceVsOptional(t *testing.T) {
	ca, caKey := generateTestCA(t)
	// Leaf without responder URL and without stapled response
	leaf, _ := generateTestLeaf(t, ca, caKey, 40, "")

	enforceCfg := OCSPConfig{Mode: OCSPModeEnforce}
	if err := VerifyOCSP(leaf, ca, nil, enforceCfg); err == nil {
		t.Fatal("enforce mode without responder should fail")
	}

	optionalCfg := OCSPConfig{Mode: OCSPModeOptional}
	if err := VerifyOCSP(leaf, ca, nil, optionalCfg); err != nil {
		t.Fatalf("optional mode without responder should succeed, got: %v", err)
	}
}

func TestServerTLSReloader_OCSP_HandshakeIntegration(t *testing.T) {
	dir := t.TempDir()
	ca, caKey := generateTestCA(t)
	now := time.Now()

	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.Raw})
	caPath := writeTestPEM(t, dir, "ca.pem", caPEM, 0o600)

	serverCertPEM, serverKeyPEM := makeTestLeaf(t, ca, caKey, true, "")
	serverCertPath := writeTestPEM(t, dir, "server.crt", serverCertPEM, 0o644)
	serverKeyPath := writeTestPEM(t, dir, "server.key", serverKeyPEM, 0o600)

	var ocspRespBytes []byte
	ocspSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/ocsp-response")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(ocspRespBytes)
	}))
	defer ocspSrv.Close()

	// Client 1: good certificate
	client1, client1Key := generateTestLeaf(t, ca, caKey, 101, ocspSrv.URL)
	client1CertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: client1.Raw})
	client1KeyDER, _ := x509.MarshalECPrivateKey(client1Key)
	client1KeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: client1KeyDER})
	client1CertPath := writeTestPEM(t, dir, "client1.crt", client1CertPEM, 0o644)
	client1KeyPath := writeTestPEM(t, dir, "client1.key", client1KeyPEM, 0o600)

	// Set OCSP response to Good
	ocspRespBytes = makeOCSPResponse(t, ca, caKey, client1.SerialNumber, ocsp.Good, now.Add(-time.Minute), now.Add(time.Hour))

	reloader, err := NewServerTLSReloader(serverCertPath, serverKeyPath, caPath, "", WithOCSP(OCSPConfig{
		Mode:       OCSPModeEnforce,
		HTTPClient: ocspSrv.Client(),
	}))
	if err != nil {
		t.Fatal(err)
	}

	clientConfig, err := ClientMTLS("localhost", caPath, client1CertPath, client1KeyPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, serverErr, clientErr := testTLSHandshake(reloader.Config(), clientConfig); serverErr != nil || clientErr != nil {
		t.Fatalf("good client certificate handshake rejected: server=%v client=%v", serverErr, clientErr)
	}

	// Client 2: revoked certificate
	client2, client2Key := generateTestLeaf(t, ca, caKey, 102, ocspSrv.URL)
	client2CertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: client2.Raw})
	client2KeyDER, _ := x509.MarshalECPrivateKey(client2Key)
	client2KeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: client2KeyDER})
	client2CertPath := writeTestPEM(t, dir, "client2.crt", client2CertPEM, 0o644)
	client2KeyPath := writeTestPEM(t, dir, "client2.key", client2KeyPEM, 0o600)

	ocspRespBytes = makeOCSPResponse(t, ca, caKey, client2.SerialNumber, ocsp.Revoked, now.Add(-time.Minute), now.Add(time.Hour))

	client2Config, err := ClientMTLS("localhost", caPath, client2CertPath, client2KeyPath)
	if err != nil {
		t.Fatal(err)
	}

	if _, serverErr, _ := testTLSHandshake(reloader.Config(), client2Config); serverErr == nil {
		t.Fatal("revoked client certificate handshake succeeded")
	}
}
