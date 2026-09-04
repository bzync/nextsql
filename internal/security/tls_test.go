package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

func TestServerMTLSRequiresVerifiedServiceCertificate(t *testing.T) {
	dir := t.TempDir()
	ca, caKey, caPEM := makeTestCA(t)
	caPath := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(caPath, caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	serverCert, serverKey := makeTestLeaf(t, ca, caKey, true, "")
	clientCert, clientKey := makeTestLeaf(t, ca, caKey, false, "nextsql://service/app")
	serverCertPath := writeTestPEM(t, dir, "server.crt", serverCert, 0o644)
	serverKeyPath := writeTestPEM(t, dir, "server.key", serverKey, 0o600)
	clientCertPath := writeTestPEM(t, dir, "client.crt", clientCert, 0o644)
	clientKeyPath := writeTestPEM(t, dir, "client.key", clientKey, 0o600)

	srv, err := ServerMTLS(serverCertPath, serverKeyPath, caPath)
	if err != nil {
		t.Fatal(err)
	}
	if srv.MinVersion != tls.VersionTLS13 || srv.ClientAuth != tls.RequireAndVerifyClientCert || srv.ClientCAs == nil {
		t.Fatalf("unexpected mTLS config: min=%d auth=%d", srv.MinVersion, srv.ClientAuth)
	}
	noCert, err := ClientTLS("localhost", caPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, serverErr, _ := testTLSHandshake(srv, noCert); serverErr == nil {
		t.Fatal("server accepted client without a certificate")
	}

	client, err := ClientMTLS("localhost", caPath, clientCertPath, clientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	state, serverErr, clientErr := testTLSHandshake(srv, client)
	if serverErr != nil || clientErr != nil {
		t.Fatalf("verified client rejected: server=%v client=%v", serverErr, clientErr)
	}
	identity, err := ServiceIdentity(state)
	if err != nil || identity != "app" {
		t.Fatalf("identity=%q err=%v", identity, err)
	}
}

func TestServerTLSReloaderRotatesCertificateAndTrustAtomically(t *testing.T) {
	dir := t.TempDir()
	ca1, caKey1, caPEM1 := makeTestCA(t)
	ca2, caKey2, caPEM2 := makeTestCA(t)
	serverCert1, serverKey1 := makeTestLeaf(t, ca1, caKey1, true, "")
	serverCert2, serverKey2 := makeTestLeaf(t, ca2, caKey2, true, "")
	clientCert1, clientKey1 := makeTestLeaf(t, ca1, caKey1, false, "nextsql://service/app")
	clientCert2, clientKey2 := makeTestLeaf(t, ca2, caKey2, false, "nextsql://service/app")

	activeCert := writeTestPEM(t, dir, "server.crt", serverCert1, 0o644)
	activeKey := writeTestPEM(t, dir, "server.key", serverKey1, 0o600)
	activeCA := writeTestPEM(t, dir, "client-ca.pem", caPEM1, 0o600)
	ca1Path := writeTestPEM(t, dir, "ca1.pem", caPEM1, 0o600)
	ca2Path := writeTestPEM(t, dir, "ca2.pem", caPEM2, 0o600)
	clientCert1Path := writeTestPEM(t, dir, "client1.crt", clientCert1, 0o644)
	clientKey1Path := writeTestPEM(t, dir, "client1.key", clientKey1, 0o600)
	clientCert2Path := writeTestPEM(t, dir, "client2.crt", clientCert2, 0o644)
	clientKey2Path := writeTestPEM(t, dir, "client2.key", clientKey2, 0o600)

	reloader, err := NewServerTLSReloader(activeCert, activeKey, activeCA, "")
	if err != nil {
		t.Fatal(err)
	}
	client1, err := ClientMTLS("localhost", ca1Path, clientCert1Path, clientKey1Path)
	if err != nil {
		t.Fatal(err)
	}
	client2, err := ClientMTLS("localhost", ca2Path, clientCert2Path, clientKey2Path)
	if err != nil {
		t.Fatal(err)
	}
	if _, serverErr, clientErr := testTLSHandshake(reloader.Config(), client1); serverErr != nil || clientErr != nil {
		t.Fatalf("first snapshot rejected: server=%v client=%v", serverErr, clientErr)
	}
	if _, serverErr, _ := testTLSHandshake(reloader.Config(), client2); serverErr == nil {
		t.Fatal("second trust root accepted before rotation")
	}

	if err := os.WriteFile(activeCert, []byte("invalid"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reloader.Reload(); err == nil {
		t.Fatal("invalid partial rotation was published")
	}
	if _, serverErr, clientErr := testTLSHandshake(reloader.Config(), client1); serverErr != nil || clientErr != nil {
		t.Fatalf("last known-good snapshot was not retained: server=%v client=%v", serverErr, clientErr)
	}

	if err := os.WriteFile(activeCert, serverCert2, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeKey, serverKey2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(activeCA, caPEM2, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, serverErr, clientErr := testTLSHandshake(reloader.Config(), client2); serverErr != nil || clientErr != nil {
		t.Fatalf("rotated snapshot rejected: server=%v client=%v", serverErr, clientErr)
	}
	if _, serverErr, _ := testTLSHandshake(reloader.Config(), client1); serverErr == nil {
		t.Fatal("removed trust root accepted after rotation")
	}
}

func TestServerTLSReloaderCRLRevocationFailsClosed(t *testing.T) {
	dir := t.TempDir()
	ca, caKey, caPEM := makeTestCA(t)
	serverCert, serverKey := makeTestLeaf(t, ca, caKey, true, "")
	clientCert, clientKey := makeTestLeaf(t, ca, caKey, false, "nextsql://service/app")
	serverCertPath := writeTestPEM(t, dir, "server.crt", serverCert, 0o644)
	serverKeyPath := writeTestPEM(t, dir, "server.key", serverKey, 0o600)
	caPath := writeTestPEM(t, dir, "ca.pem", caPEM, 0o600)
	clientCertPath := writeTestPEM(t, dir, "client.crt", clientCert, 0o644)
	clientKeyPath := writeTestPEM(t, dir, "client.key", clientKey, 0o600)
	crlPath := writeTestPEM(t, dir, "client.crl", makeTestCRL(t, ca, caKey, nil, time.Now().Add(time.Hour)), 0o644)

	reloader, err := NewServerTLSReloader(serverCertPath, serverKeyPath, caPath, crlPath)
	if err != nil {
		t.Fatal(err)
	}
	client, err := ClientMTLS("localhost", caPath, clientCertPath, clientKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, serverErr, clientErr := testTLSHandshake(reloader.Config(), client); serverErr != nil || clientErr != nil {
		t.Fatalf("unrevoked certificate rejected: server=%v client=%v", serverErr, clientErr)
	}

	if err := os.WriteFile(crlPath, makeTestCRL(t, ca, caKey, []*big.Int{big.NewInt(3)}, time.Now().Add(time.Hour)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	if _, serverErr, _ := testTLSHandshake(reloader.Config(), client); serverErr == nil {
		t.Fatal("revoked client certificate accepted")
	}

	if err := os.WriteFile(crlPath, makeTestCRL(t, ca, caKey, nil, time.Now().Add(-time.Minute)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := reloader.Reload(); err == nil {
		t.Fatal("expired CRL reload succeeded")
	}
	if _, serverErr, _ := testTLSHandshake(reloader.Config(), client); serverErr == nil {
		t.Fatal("failed CRL reload replaced the last known-good revoked snapshot")
	}
}

func TestServiceIdentityFailsClosed(t *testing.T) {
	valid, _ := url.Parse("nextsql://service/App_1")
	other, _ := url.Parse("spiffe://example.test/app")
	state := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{other, valid}}},
		VerifiedChains:   [][]*x509.Certificate{{{}}},
	}
	got, err := ServiceIdentity(state)
	if err != nil || got != "app_1" {
		t.Fatalf("identity=%q err=%v", got, err)
	}
	second, _ := url.Parse("nextsql://service/other")
	state.PeerCertificates[0].URIs = append(state.PeerCertificates[0].URIs, second)
	if _, err := ServiceIdentity(state); err == nil {
		t.Fatal("multiple identities accepted")
	}
	state.PeerCertificates[0].URIs = []*url.URL{other}
	if _, err := ServiceIdentity(state); err == nil {
		t.Fatal("missing NextSQL identity accepted")
	}
	if _, err := ServiceIdentity(tls.ConnectionState{}); err == nil {
		t.Fatal("unverified certificate state accepted")
	}
}

func testTLSHandshake(serverCfg, clientCfg *tls.Config) (tls.ConnectionState, error, error) {
	serverRaw, clientRaw := bufferedTestPipe()
	server := tls.Server(serverRaw, serverCfg)
	client := tls.Client(clientRaw, clientCfg)
	type result struct {
		state tls.ConnectionState
		err   error
	}
	serverResult := make(chan result, 1)
	go func() {
		err := server.Handshake()
		serverResult <- result{state: server.ConnectionState(), err: err}
	}()
	clientErr := client.Handshake()
	_ = clientRaw.Close()
	_ = serverRaw.Close()
	got := <-serverResult
	return got.state, got.err, clientErr
}

type testPipeConn struct {
	in, out        chan []byte
	done, peerDone chan struct{}
	closeOnce      sync.Once
	mu             sync.Mutex
	pending        []byte
}

func bufferedTestPipe() (net.Conn, net.Conn) {
	aToB := make(chan []byte, 32)
	bToA := make(chan []byte, 32)
	aDone := make(chan struct{})
	bDone := make(chan struct{})
	return &testPipeConn{in: bToA, out: aToB, done: aDone, peerDone: bDone},
		&testPipeConn{in: aToB, out: bToA, done: bDone, peerDone: aDone}
}

func (c *testPipeConn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if len(c.pending) > 0 {
		n := copy(p, c.pending)
		c.pending = c.pending[n:]
		c.mu.Unlock()
		return n, nil
	}
	c.mu.Unlock()
	select {
	case body := <-c.in:
		c.mu.Lock()
		n := copy(p, body)
		c.pending = append(c.pending[:0], body[n:]...)
		c.mu.Unlock()
		return n, nil
	case <-c.peerDone:
		return 0, io.EOF
	case <-c.done:
		return 0, net.ErrClosed
	}
}

func (c *testPipeConn) Write(p []byte) (int, error) {
	body := append([]byte(nil), p...)
	select {
	case c.out <- body:
		return len(p), nil
	case <-c.peerDone:
		return 0, io.ErrClosedPipe
	case <-c.done:
		return 0, net.ErrClosed
	}
}

func (c *testPipeConn) Close() error {
	c.closeOnce.Do(func() { close(c.done) })
	return nil
}

func (c *testPipeConn) LocalAddr() net.Addr              { return testPipeAddr("local") }
func (c *testPipeConn) RemoteAddr() net.Addr             { return testPipeAddr("remote") }
func (c *testPipeConn) SetDeadline(time.Time) error      { return nil }
func (c *testPipeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *testPipeConn) SetWriteDeadline(time.Time) error { return nil }

type testPipeAddr string

func (a testPipeAddr) Network() string { return "test" }
func (a testPipeAddr) String() string  { return string(a) }

// TestServerTLSReloaderStatus covers the system.tls read-model source
// (Manager Security view, M4 remainder): a nil reloader reports "not
// attached" rather than panicking; a loaded reloader reports the redacted
// leaf identity/validity plus the mTLS/CRL posture, with no key material
// and no network address anywhere in the returned struct; a rotation
// (Reload) is reflected in the very next Status call, matching the
// "atomic snapshot" contract the handshake path already relies on.
func TestServerTLSReloaderStatus(t *testing.T) {
	var nilReloader *ServerTLSReloader
	if _, ok := nilReloader.Status(); ok {
		t.Fatal("nil reloader reported attached")
	}

	dir := t.TempDir()
	ca, caKey, caPEM := makeTestCA(t)
	serverCert1, serverKey1 := makeTestLeaf(t, ca, caKey, true, "")
	certPath := writeTestPEM(t, dir, "server.crt", serverCert1, 0o644)
	keyPath := writeTestPEM(t, dir, "server.key", serverKey1, 0o600)

	reloader, err := NewServerTLSReloader(certPath, keyPath, "", "")
	if err != nil {
		t.Fatal(err)
	}
	st, ok := reloader.Status()
	if !ok || !st.Enabled {
		t.Fatalf("plain TLS reloader not reported enabled: %+v ok=%v", st, ok)
	}
	if st.Subject != "CN=localhost" || st.Issuer != "CN=NextSQL test CA" {
		t.Fatalf("unexpected subject/issuer: %+v", st)
	}
	if st.NotBefore.IsZero() || st.NotAfter.IsZero() || !st.NotBefore.Before(st.NotAfter) {
		t.Fatalf("unexpected validity window: %+v", st)
	}
	if len(st.DNSNames) != 1 || st.DNSNames[0] != "localhost" {
		t.Fatalf("unexpected DNS names: %+v", st.DNSNames)
	}
	if st.MTLSRequired || st.ClientCAConfigured || st.ClientCRLConfigured {
		t.Fatalf("plain TLS reloader reported mTLS/CRL posture: %+v", st)
	}

	// mTLS + CRL: both flags flip, and neither the client CA nor the CRL
	// file path/contents leak into the struct.
	caPath := writeTestPEM(t, dir, "client-ca.pem", caPEM, 0o600)
	crlPath := writeTestPEM(t, dir, "client.crl", makeTestCRL(t, ca, caKey, nil, time.Now().Add(time.Hour)), 0o644)
	mtlsReloader, err := NewServerTLSReloader(certPath, keyPath, caPath, crlPath)
	if err != nil {
		t.Fatal(err)
	}
	st, ok = mtlsReloader.Status()
	if !ok || !st.MTLSRequired || !st.ClientCAConfigured || !st.ClientCRLConfigured {
		t.Fatalf("mTLS/CRL posture not reflected: %+v ok=%v", st, ok)
	}

	// Rotation: a new leaf's Status is visible on the very next call.
	// makeTestLeaf's validity window has only second resolution (X.509
	// UTCTime), so cross a second boundary first or NotBefore/NotAfter for
	// the two leaves could collide and the assertion below would be
	// meaningless rather than merely flaky.
	time.Sleep(1100 * time.Millisecond)
	serverCert2, serverKey2 := makeTestLeaf(t, ca, caKey, true, "")
	if err := os.WriteFile(certPath, serverCert2, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, serverKey2, 0o600); err != nil {
		t.Fatal(err)
	}
	before, _ := reloader.Status()
	if err := reloader.Reload(); err != nil {
		t.Fatal(err)
	}
	after, ok := reloader.Status()
	if !ok || !after.Enabled {
		t.Fatalf("status not reported after reload: %+v ok=%v", after, ok)
	}
	if after.NotBefore.Equal(before.NotBefore) && after.NotAfter.Equal(before.NotAfter) {
		t.Fatal("Status did not reflect the rotated leaf — looks cached from before Reload")
	}
}

func makeTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "NextSQL test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
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
	return cert, key, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func makeTestLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, server bool, identity string) ([]byte, []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ext := x509.ExtKeyUsageClientAuth
	serial := int64(3)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial),
		Subject:      pkix.Name{CommonName: "client"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{ext},
	}
	if server {
		tmpl.SerialNumber = big.NewInt(2)
		tmpl.Subject.CommonName = "localhost"
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.DNSNames = []string{"localhost"}
		tmpl.IPAddresses = []net.IP{net.IPv4(127, 0, 0, 1)}
	} else if identity != "" {
		u, err := url.Parse(identity)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = []*url.URL{u}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func writeTestPEM(t *testing.T, dir, name string, body []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, body, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func makeTestCRL(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, serials []*big.Int, nextUpdate time.Time) []byte {
	t.Helper()
	thisUpdate := time.Now().Add(-time.Minute)
	if !thisUpdate.Before(nextUpdate) {
		thisUpdate = nextUpdate.Add(-time.Minute)
	}
	entries := make([]x509.RevocationListEntry, 0, len(serials))
	for _, serial := range serials {
		entries = append(entries, x509.RevocationListEntry{SerialNumber: serial, RevocationTime: time.Now().Add(-time.Minute)})
	}
	der, err := x509.CreateRevocationList(rand.Reader, &x509.RevocationList{
		Number: big.NewInt(1), ThisUpdate: thisUpdate, NextUpdate: nextUpdate,
		RevokedCertificateEntries: entries,
	}, ca, caKey)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "X509 CRL", Bytes: der})
}
