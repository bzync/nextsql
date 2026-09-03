package protocol

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/url"
	"testing"
)

func TestTerminateConnectionsIncludesPreAuthHandshake(t *testing.T) {
	srv := NewServer(nil, nil)
	serverConn, clientConn := net.Pipe()
	defer clientConn.Close()
	srv.mu.Lock()
	srv.conns[serverConn] = struct{}{}
	srv.nconn = 1
	srv.mu.Unlock()
	if got := srv.TerminateConnections(); got != 1 {
		t.Fatalf("terminated=%d", got)
	}
	if _, err := clientConn.Write([]byte("hello")); err == nil {
		t.Fatal("pre-auth connection remained open")
	}
}

func TestMatchServiceIdentity(t *testing.T) {
	u, err := url.Parse("nextsql://service/app")
	if err != nil {
		t.Fatal(err)
	}
	state := tls.ConnectionState{
		PeerCertificates: []*x509.Certificate{{URIs: []*url.URL{u}}},
		VerifiedChains:   [][]*x509.Certificate{{{}}},
	}
	if err := matchServiceIdentity(state, "App"); err != nil {
		t.Fatal(err)
	}
	if err := matchServiceIdentity(state, "other"); err == nil {
		t.Fatal("certificate identity was not bound to the requested principal")
	}
	if err := matchServiceIdentity(tls.ConnectionState{}, "app"); err == nil {
		t.Fatal("unverified certificate accepted")
	}
}

func TestTokenIdentitySourceIsDerivedFromVerifiedKey(t *testing.T) {
	hints := map[uint32]string{7: "oidc", 8: "attacker-value"}
	for _, tc := range []struct {
		name     string
		base     string
		keyID    uint32
		verified bool
		want     string
	}{
		{name: "native token", base: "native", keyID: 6, verified: true, want: "token"},
		{name: "mtls token", base: "mtls+native", keyID: 6, verified: true, want: "mtls+token"},
		{name: "oidc", base: "native", keyID: 7, verified: true, want: "oidc"},
		{name: "mtls oidc", base: "mtls+native", keyID: 7, verified: true, want: "mtls+oidc"},
		{name: "unverified cannot self label", base: "native", keyID: 7, verified: false, want: "token"},
		{name: "unknown configured value fails generic", base: "native", keyID: 8, verified: true, want: "token"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := tokenIdentitySource(tc.base, tc.keyID, tc.verified, hints); got != tc.want {
				t.Fatalf("source=%q want %q", got, tc.want)
			}
		})
	}
}
