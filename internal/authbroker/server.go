package authbroker

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// HTTPServer owns the broker's bounded HTTP server and its separate listener.
// It is shared by the standalone command and nextsqld's embedded mode so both
// deployment forms have identical TLS and timeout behavior.
type HTTPServer struct {
	server *http.Server
	ln     net.Listener
	tls    bool
}

// NewHTTPServer binds the broker listener. Binding happens before Serve so a
// caller can fail startup atomically when the configured address is unusable.
func NewHTTPServer(cfg Config, handler http.Handler) (*HTTPServer, error) {
	if handler == nil {
		return nil, nerr.New(nerr.InvalidArgument, "authbroker.NewHTTPServer", "handler is required")
	}
	if security.RequireTLS(cfg.Listen) && cfg.TLSCert == "" {
		return nil, nerr.New(nerr.InvalidArgument, "authbroker.NewHTTPServer", "a non-loopback listen address requires tls_cert and tls_key")
	}
	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      20 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if cfg.TLSCert != "" {
		tlsCfg, err := security.ServerTLS(cfg.TLSCert, cfg.TLSKey)
		if err != nil {
			return nil, err
		}
		srv.TLSConfig = tlsCfg
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "authbroker.NewHTTPServer", "listen", err)
	}
	return &HTTPServer{server: srv, ln: ln, tls: srv.TLSConfig != nil}, nil
}

// Addr is the bound address. It is useful when port zero was requested.
func (s *HTTPServer) Addr() net.Addr { return s.ln.Addr() }

// TLS reports whether the listener requires TLS.
func (s *HTTPServer) TLS() bool { return s.tls }

// Serve runs until Shutdown is called or the listener fails.
func (s *HTTPServer) Serve() error {
	var err error
	if s.tls {
		// ServeTLS performs the one and only TLS wrapping. The old standalone
		// path wrapped the listener before calling ServeTLS, which could result
		// in a double TLS handshake.
		err = s.server.ServeTLS(s.ln, "", "")
	} else {
		err = s.server.Serve(s.ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the broker listener.
func (s *HTTPServer) Shutdown(ctx context.Context) error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

// Close immediately closes the listener and active HTTP connections.
func (s *HTTPServer) Close() error {
	if s == nil || s.server == nil {
		return nil
	}
	return s.server.Close()
}
