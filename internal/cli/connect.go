package cli

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"os"
	"strings"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

var stderr io.Writer = os.Stderr

// CheckServerMode rejects mixing local-only flags onto a server-mode command.
// NEXTSQL_DATA_DIR / NEXTSQL_KEY_FILE from the environment or dotenv are ignored.
func CheckServerMode(s Settings) error {
	if s.Explicit["data-dir"] || s.Explicit["key-file"] {
		return nerr.New(nerr.InvalidArgument, "cli", "server-mode commands do not accept --data-dir or --key-file; use a local command (init, backup, restore, status --local)")
	}
	return nil
}

// ServerConfig builds a driver Config from resolved settings.
// Every connect sets TLS or InsecureNoTLS, including loopback.
func ServerConfig(s Settings) (nextsql.Config, error) {
	if err := CheckServerMode(s); err != nil {
		return nextsql.Config{}, err
	}
	addr := strings.TrimSpace(s.Addr)
	if addr == "" {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "address is required")
	}
	lower := strings.ToLower(addr)
	if strings.Contains(lower, "://") || strings.Contains(lower, "key=") || strings.Contains(lower, "password=") {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "address must be host:port; keys and credentials must not be passed in a URL")
	}
	if strings.TrimSpace(s.User) == "" {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "user is required")
	}
	pw, err := resolvePassword(s)
	if err != nil {
		return nextsql.Config{}, err
	}
	cfg := nextsql.Config{
		Address:       addr,
		Realm:         s.Realm,
		Database:      s.Database,
		User:          s.User,
		Password:      pw,
		InsecureNoTLS: s.Insecure,
	}
	clientCert := strings.TrimSpace(s.TLSClientCert)
	clientKey := strings.TrimSpace(s.TLSClientKey)
	if (clientCert == "") != (clientKey == "") {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "--tls-client-cert and --tls-client-key must be set together")
	}
	if ca := strings.TrimSpace(s.TLSCA); ca != "" {
		serverName := strings.TrimSpace(s.TLSServerName)
		if serverName == "" {
			serverName = "localhost"
			if h, _, err := net.SplitHostPort(addr); err == nil && h != "" {
				serverName = h
			}
		}
		var tlsCfg *tls.Config
		if clientCert != "" {
			tlsCfg, err = security.ClientMTLS(serverName, ca, clientCert, clientKey)
		} else {
			tlsCfg, err = security.ClientTLS(serverName, ca)
		}
		if err != nil {
			return nextsql.Config{}, err
		}
		cfg.TLS = tlsCfg
		cfg.InsecureNoTLS = false
	}
	if clientCert != "" && cfg.TLS == nil {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "--tls-client-cert requires --tls-ca")
	}
	if cfg.TLS == nil && !cfg.InsecureNoTLS {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "TLS is required; pass --tls-ca or --insecure (loopback only)")
	}
	if cfg.InsecureNoTLS && security.RequireTLS(addr) {
		return nextsql.Config{}, nerr.New(nerr.InvalidArgument, "cli", "plaintext is only allowed on loopback")
	}
	return cfg, nil
}

// Open dials nextsqld with ServerConfig.
func Open(ctx context.Context, s Settings) (*nextsql.Conn, error) {
	if strings.TrimSpace(s.IdP) != "" {
		applied, err := applyOIDC(ctx, s)
		if err != nil {
			return nil, err
		}
		s = applied
	}
	cfg, err := ServerConfig(s)
	if err != nil {
		return nil, err
	}
	conn, err := nextsql.OpenContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return conn, nil
}

func resolvePassword(s Settings) (string, error) {
	if s.oidcCredential != "" {
		return s.oidcCredential, nil
	}
	if strings.TrimSpace(s.PasswordFile) != "" {
		return auth.ReadPasswordFile(s.PasswordFile)
	}
	if strings.TrimSpace(s.Password) != "" {
		envKey := s.inlinePasswordEnv
		if envKey == "" {
			envKey = envDatabasePass
		}
		fmt.Fprintf(stderr, "using %s from the environment; prefer NEXTSQL_DATABASE_PASSWORD_FILE\n", envKey)
		return s.Password, nil
	}
	return "", nerr.New(nerr.InvalidArgument, "cli", "password is required")
}
