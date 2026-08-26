// Package security holds TLS and network abuse helpers for the native protocol.
package security

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// ServerTLS loads a certificate and requires TLS 1.3.
func ServerTLS(certFile, keyFile string) (*tls.Config, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, nerr.Wrap(nerr.InvalidArgument, "security.ServerTLS", "load key pair", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{cert},
	}, nil
}

// ClientTLS builds a TLS 1.3 client config. caFile may be empty to use the system pool.
func ClientTLS(serverName, caFile string) (*tls.Config, error) {
	cfg := &tls.Config{
		MinVersion: tls.VersionTLS13,
		ServerName: serverName,
	}
	if caFile != "" {
		pemBytes, err := os.ReadFile(caFile)
		if err != nil {
			return nil, nerr.Wrap(nerr.IO, "security.ClientTLS", "read CA", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pemBytes) {
			return nil, nerr.New(nerr.InvalidArgument, "security.ClientTLS", "invalid CA certificate")
		}
		cfg.RootCAs = pool
	}
	return cfg, nil
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
