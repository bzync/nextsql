// Package security holds TLS and network abuse helpers for the native protocol.
package security

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

const serviceIdentityScheme = "nextsql"

// ServerTLSReloader publishes complete TLS snapshots atomically. A failed
// reload leaves the last known-good certificate, trust bundle, and revocation
// set active; handshakes never observe a partially loaded file set.
type ServerTLSReloader struct {
	certFile      string
	keyFile       string
	clientCAFile  string
	clientCRLFile string

	mu      sync.RWMutex
	current *tls.Config
	config  *tls.Config
}

// NewServerTLSReloader loads a server key pair and, when clientCAFile is set,
// an mTLS trust bundle plus an optional PEM CRL bundle. Call Reload after
// atomically replacing any of the files to rotate credentials without
// restarting the listener.
func NewServerTLSReloader(certFile, keyFile, clientCAFile, clientCRLFile string) (*ServerTLSReloader, error) {
	if strings.TrimSpace(clientCRLFile) != "" && strings.TrimSpace(clientCAFile) == "" {
		return nil, nerr.New(nerr.InvalidArgument, "security.NewServerTLSReloader", "client CRL requires a client CA bundle")
	}
	r := &ServerTLSReloader{
		certFile: certFile, keyFile: keyFile,
		clientCAFile: clientCAFile, clientCRLFile: clientCRLFile,
	}
	if err := r.Reload(); err != nil {
		return nil, err
	}
	r.config = &tls.Config{
		MinVersion: tls.VersionTLS13,
		GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
			r.mu.RLock()
			cfg := r.current
			r.mu.RUnlock()
			if cfg == nil {
				return nil, nerr.New(nerr.Unavailable, "security.ServerTLSReloader", "TLS configuration is unavailable")
			}
			return cfg, nil
		},
	}
	return r, nil
}

// Config returns the stable listener configuration. Each new handshake obtains
// the current immutable snapshot through GetConfigForClient.
func (r *ServerTLSReloader) Config() *tls.Config {
	if r == nil {
		return nil
	}
	return r.config
}

// Reload validates every configured file before publishing the new snapshot.
func (r *ServerTLSReloader) Reload() error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "security.ServerTLSReloader.Reload", "nil reloader")
	}
	cfg, err := loadServerTLS(r.certFile, r.keyFile, r.clientCAFile, r.clientCRLFile)
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.current = cfg
	r.mu.Unlock()
	return nil
}

// MTLS reports whether this reloader requires client certificates.
func (r *ServerTLSReloader) MTLS() bool {
	return r != nil && strings.TrimSpace(r.clientCAFile) != ""
}

// TLSStatus is a redacted snapshot of a listener's live TLS configuration,
// safe to expose to an authenticated admin session (system.tls): the leaf
// certificate's public identity/validity and the mTLS/CRL posture. It never
// carries private key material, and it never carries a network address —
// same "no addresses over SQL" convention as system.replication.leader_addr.
type TLSStatus struct {
	Enabled             bool
	Subject             string
	Issuer              string
	NotBefore, NotAfter time.Time
	DNSNames            []string
	MTLSRequired        bool
	ClientCAConfigured  bool
	ClientCRLConfigured bool
}

// Status returns the currently active leaf certificate's redacted status.
// The second return is false only when r is nil or has no loaded
// certificate yet (Reload failed before ever succeeding, which NewServerTLSReloader
// already prevents — so in practice this is false only for a nil reloader).
func (r *ServerTLSReloader) Status() (TLSStatus, bool) {
	if r == nil {
		return TLSStatus{}, false
	}
	r.mu.RLock()
	cfg := r.current
	r.mu.RUnlock()
	if cfg == nil || len(cfg.Certificates) == 0 || cfg.Certificates[0].Leaf == nil {
		return TLSStatus{}, false
	}
	leaf := cfg.Certificates[0].Leaf
	return TLSStatus{
		Enabled:             true,
		Subject:             leaf.Subject.String(),
		Issuer:              leaf.Issuer.String(),
		NotBefore:           leaf.NotBefore,
		NotAfter:            leaf.NotAfter,
		DNSNames:            append([]string(nil), leaf.DNSNames...),
		MTLSRequired:        cfg.ClientAuth == tls.RequireAndVerifyClientCert,
		ClientCAConfigured:  r.MTLS(),
		ClientCRLConfigured: strings.TrimSpace(r.clientCRLFile) != "",
	}, true
}

// ServerTLS loads a certificate and requires TLS 1.3.
func ServerTLS(certFile, keyFile string) (*tls.Config, error) {
	return loadServerTLS(certFile, keyFile, "", "")
}

// ServerMTLS loads the server key pair and a client trust bundle. Every TLS
// connection must present a certificate chaining to clientCAFile. The native
// protocol separately binds the certificate's NextSQL service URI to the
// requested database principal.
func ServerMTLS(certFile, keyFile, clientCAFile string) (*tls.Config, error) {
	return loadServerTLS(certFile, keyFile, clientCAFile, "")
}

func loadServerTLS(certFile, keyFile, clientCAFile, clientCRLFile string) (*tls.Config, error) {
	const op = "security.loadServerTLS"
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "load key pair", err)
	}
	if len(cert.Certificate) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, op, "server certificate chain is empty")
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, op, "parse server certificate", err)
	}
	now := time.Now()
	if now.Before(leaf.NotBefore) || !now.Before(leaf.NotAfter) {
		return nil, nerr.New(nerr.InvalidArgument, op, "server certificate is not currently valid")
	}
	cert.Leaf = leaf
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{cert}}
	if strings.TrimSpace(clientCAFile) == "" {
		if strings.TrimSpace(clientCRLFile) != "" {
			return nil, nerr.New(nerr.InvalidArgument, op, "client CRL requires a client CA bundle")
		}
		return cfg, nil
	}
	pool, authorities, err := certPoolAndCertificatesFromFile(op, clientCAFile)
	if err != nil {
		return nil, err
	}
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.ClientCAs = pool
	if strings.TrimSpace(clientCRLFile) != "" {
		revocations, err := revocationListsFromFile(op, clientCRLFile, authorities, now)
		if err != nil {
			return nil, err
		}
		cfg.VerifyConnection = revocations.verifyConnection
	}
	return cfg, nil
}

// ClientTLS builds a TLS 1.3 client config. caFile may be empty to use the system pool.
func ClientTLS(serverName, caFile string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: serverName,
	}
	if caFile != "" {
		pool, err := certPoolFromFile("security.ClientTLS", caFile)
		if err != nil {
			return nil, err
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
}

// ClientMTLS builds a TLS 1.3 client config and installs a client certificate.
// Certificate keys are loaded only from the explicit key file and are never
// accepted in connection URLs.
func ClientMTLS(serverName, caFile, certFile, keyFile string) (*tls.Config, error) {
	cfg, err := ClientTLS(serverName, caFile)
	if err != nil {
		return nil, err
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, "security.ClientMTLS", "load client key pair", err)
	}
	cfg.Certificates = []tls.Certificate{cert}
	return cfg, nil
}

func certPoolFromFile(op, path string) (*x509.CertPool, error) {
	pool, _, err := certPoolAndCertificatesFromFile(op, path)
	return pool, err
}

func certPoolAndCertificatesFromFile(op, path string) (*x509.CertPool, []*x509.Certificate, error) {
	if strings.TrimSpace(path) == "" {
		return nil, nil, nerr.New(nerr.InvalidArgument, op, "CA certificate path is required")
	}
	pemBytes, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, nerr.Wrap(nerr.IO, op, "read CA", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, nil, nerr.New(nerr.InvalidArgument, op, "invalid CA certificate")
	}
	var certificates []*x509.Certificate
	rest := pemBytes
	for len(bytes.TrimSpace(rest)) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || block.Type != "CERTIFICATE" {
			return nil, nil, nerr.New(nerr.InvalidArgument, op, "invalid CA certificate bundle")
		}
		parsed, err := x509.ParseCertificates(block.Bytes)
		if err != nil || len(parsed) == 0 {
			return nil, nil, nerr.New(nerr.InvalidArgument, op, "invalid CA certificate")
		}
		certificates = append(certificates, parsed...)
		rest = next
	}
	return pool, certificates, nil
}

type revocationLists map[string][]*x509.RevocationList

func revocationListsFromFile(op, path string, authorities []*x509.Certificate, now time.Time) (revocationLists, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, op, "read client CRL", err)
	}
	lists := make(revocationLists)
	rest := body
	count := 0
	for len(bytes.TrimSpace(rest)) > 0 {
		block, next := pem.Decode(rest)
		if block == nil || (block.Type != "X509 CRL" && block.Type != "CRL") {
			return nil, nerr.New(nerr.InvalidArgument, op, "invalid client CRL bundle")
		}
		list, err := x509.ParseRevocationList(block.Bytes)
		if err != nil {
			return nil, nerr.Wrap(nerr.InvalidArgument, op, "parse client CRL", err)
		}
		if now.Before(list.ThisUpdate) || list.NextUpdate.IsZero() || !now.Before(list.NextUpdate) {
			return nil, nerr.New(nerr.InvalidArgument, op, "client CRL is not currently valid")
		}
		verified := false
		for _, authority := range authorities {
			if bytes.Equal(authority.RawSubject, list.RawIssuer) && list.CheckSignatureFrom(authority) == nil {
				verified = true
				break
			}
		}
		if !verified {
			return nil, nerr.New(nerr.InvalidArgument, op, "client CRL issuer is not in the client CA bundle")
		}
		lists[string(list.RawIssuer)] = append(lists[string(list.RawIssuer)], list)
		count++
		rest = next
	}
	if count == 0 {
		return nil, nerr.New(nerr.InvalidArgument, op, "client CRL bundle is empty")
	}
	return lists, nil
}

func (r revocationLists) verifyConnection(state tls.ConnectionState) error {
	if len(state.VerifiedChains) == 0 {
		return nerr.New(nerr.Unauthorized, "security.verifyClientRevocation", "verified client certificate required")
	}
	for _, chain := range state.VerifiedChains {
		if r.verifyChain(chain, time.Now()) == nil {
			return nil
		}
	}
	return nerr.New(nerr.Unauthorized, "security.verifyClientRevocation", "client certificate is revoked or lacks current revocation coverage")
}

func (r revocationLists) verifyChain(chain []*x509.Certificate, now time.Time) error {
	if len(chain) < 2 {
		return nerr.New(nerr.Unauthorized, "security.verifyClientRevocation", "client certificate chain has no revocation issuer")
	}
	for i := 0; i < len(chain)-1; i++ {
		certificate, issuer := chain[i], chain[i+1]
		lists := r[string(certificate.RawIssuer)]
		covered := false
		for _, list := range lists {
			if now.Before(list.ThisUpdate) || list.NextUpdate.IsZero() || !now.Before(list.NextUpdate) || list.CheckSignatureFrom(issuer) != nil {
				continue
			}
			covered = true
			for _, entry := range list.RevokedCertificateEntries {
				if entry.SerialNumber != nil && certificate.SerialNumber.Cmp(entry.SerialNumber) == 0 {
					return nerr.New(nerr.Unauthorized, "security.verifyClientRevocation", "client certificate is revoked")
				}
			}
		}
		if !covered {
			return nerr.New(nerr.Unauthorized, "security.verifyClientRevocation", "client certificate lacks current revocation coverage")
		}
	}
	return nil
}

// ServiceIdentity returns the single NextSQL service principal carried by a
// verified client certificate. The native URI form is:
//
//	nextsql://service/<principal>
//
// The principal is deliberately narrow and case-normalized so a certificate
// cannot create an ambiguous mapping to the password/RBAC user namespace.
func ServiceIdentity(state tls.ConnectionState) (string, error) {
	if len(state.VerifiedChains) == 0 || len(state.PeerCertificates) == 0 {
		return "", nerr.New(nerr.Unauthorized, "security.ServiceIdentity", "verified client certificate required")
	}
	var identity string
	for _, uri := range state.PeerCertificates[0].URIs {
		name, ok := servicePrincipal(uri)
		if !ok {
			continue
		}
		if identity != "" {
			return "", nerr.New(nerr.Unauthorized, "security.ServiceIdentity", "multiple service identities are not allowed")
		}
		identity = name
	}
	if identity == "" {
		return "", nerr.New(nerr.Unauthorized, "security.ServiceIdentity", "client certificate has no NextSQL service identity")
	}
	return identity, nil
}

func servicePrincipal(uri *url.URL) (string, bool) {
	if uri == nil || !strings.EqualFold(uri.Scheme, serviceIdentityScheme) || uri.Host != "service" ||
		uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" || uri.Opaque != "" {
		return "", false
	}
	name := strings.ToLower(strings.TrimPrefix(uri.Path, "/"))
	if name == "" || len(name) > 128 || strings.Contains(name, "/") {
		return "", false
	}
	for _, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '.' || r == '_' || r == '-' {
			continue
		}
		return "", false
	}
	return name, true
}

// IsLoopback reports whether addr (host:port or host) is a loopback destination.
func IsLoopback(addr string) bool {
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	}
	host = strings.TrimSpace(host)
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// RequireTLS reports whether a listener address must use TLS.
// Loopback may run plaintext for local development; remote production must not.
func RequireTLS(listenAddr string) bool {
	return !IsLoopback(listenAddr)
}

// WriteSelfSigned writes a TLS 1.3-capable self-signed cert for tests and local use.
func WriteSelfSigned(certPath, keyPath, serverName string) error {
	certPEM, keyPEM, err := SelfSignedPEM(serverName)
	if err != nil {
		return err
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nerr.Wrap(nerr.IO, "security.WriteSelfSigned", "write cert", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nerr.Wrap(nerr.IO, "security.WriteSelfSigned", "write key", err)
	}
	return nil
}

func SelfSignedPEM(serverName string) (certPEM, keyPEM []byte, err error) {
	if serverName == "" {
		serverName = "localhost"
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, nerr.Wrap(nerr.Internal, "security.SelfSignedPEM", "keygen", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, nerr.Wrap(nerr.Internal, "security.SelfSignedPEM", "serial", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: serverName, Organization: []string{"NextSQL test"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{serverName, "localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, nerr.Wrap(nerr.Internal, "security.SelfSignedPEM", "create", err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, nerr.Wrap(nerr.Internal, "security.SelfSignedPEM", "marshal key", err)
	}
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return certPEM, keyPEM, nil
}

// ClientTLSFromPEM builds a TLS 1.3 client config that trusts certPEM.
func ClientTLSFromPEM(serverName string, certPEM []byte) (*tls.Config, error) {
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(certPEM) {
		return nil, nerr.New(nerr.InvalidArgument, "security.ClientTLSFromPEM", "invalid certificate")
	}
	if serverName == "" {
		serverName = "localhost"
	}
	return &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: serverName,
		RootCAs:    pool,
	}, nil
}
