package manager

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// Server is the Manager's bounded HTTP service. Construct it with New, run it
// with Serve, and stop it with Shutdown/Close.
type Server struct {
	cfg      Config
	log      *slog.Logger
	sessions *sessionStore
	mux      *http.ServeMux

	http *http.Server
	ln   net.Listener
	tls  bool
}

// Options are optional injectables (a logger; otherwise one is built from the
// config log level).
type Options struct {
	Logger *slog.Logger
}

// New validates the config and binds the Manager listener. Binding happens
// here so a caller can fail startup atomically on an unusable address.
func New(cfg Config, opt Options) (*Server, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := opt.Logger
	if log == nil {
		log = logging.New(cfg.LogLevel, os.Stderr)
	}

	s := &Server{
		cfg:      cfg,
		log:      log,
		sessions: newSessionStore(cfg.MaxSessions, cfg.IdleTimeout, cfg.SessionLifetime),
		mux:      http.NewServeMux(),
	}
	s.routes()

	hs := &http.Server{
		Handler:           s.withBaseMiddleware(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if cfg.ListenTLSCert != "" {
		tlsCfg, err := security.ServerTLS(cfg.ListenTLSCert, cfg.ListenTLSKey)
		if err != nil {
			s.sessions.close()
			return nil, err
		}
		hs.TLSConfig = tlsCfg
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		s.sessions.close()
		return nil, nerr.Wrap(nerr.IO, "manager.New", "listen", err)
	}
	s.http = hs
	s.ln = ln
	s.tls = hs.TLSConfig != nil
	return s, nil
}

// Addr is the bound listener address (useful when port 0 was requested).
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// TLS reports whether the Manager listener terminates TLS.
func (s *Server) TLS() bool { return s.tls }

// Serve runs until Shutdown/Close or a listener failure.
func (s *Server) Serve() error {
	var err error
	if s.tls {
		err = s.http.ServeTLS(s.ln, "", "")
	} else {
		err = s.http.Serve(s.ln)
	}
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

// Shutdown gracefully stops the listener and drops every session.
func (s *Server) Shutdown(ctx context.Context) error {
	s.sessions.close()
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// Close immediately stops the listener and drops every session.
func (s *Server) Close() error {
	s.sessions.close()
	if s.http == nil {
		return nil
	}
	return s.http.Close()
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	s.mux.HandleFunc("POST /api/v1/session", s.handleLogin)
	s.mux.HandleFunc("GET /api/v1/session", s.authed(s.handleWhoami))
	s.mux.HandleFunc("DELETE /api/v1/session", s.authed(s.handleLogout))
	s.mux.HandleFunc("GET /api/v1/overview", s.authed(s.handleOverview))
	s.mux.HandleFunc("GET /api/v1/databases", s.authed(s.handleDatabases))
	s.mux.HandleFunc("GET /api/v1/activity", s.authed(s.handleActivity))
	s.mux.HandleFunc("GET /api/v1/security", s.authed(s.handleSecurity))
	s.mux.HandleFunc("GET /api/v1/cluster", s.authed(s.handleCluster))
	s.mux.HandleFunc("POST /api/v1/cluster/action", s.authed(s.handleClusterAction))
	s.mux.HandleFunc("GET /api/v1/maintenance", s.authed(s.handleMaintenance))
	s.mux.HandleFunc("POST /api/v1/maintenance/action", s.authed(s.handleMaintenanceAction))
	s.mux.HandleFunc("GET /api/v1/config", s.authed(s.handleConfig))
	s.mux.HandleFunc("POST /api/v1/config/action", s.authed(s.handleConfigAction))
	s.mux.HandleFunc("GET /api/v1/backups", s.authed(s.handleBackups))
	s.mux.HandleFunc("POST /api/v1/backups/action", s.authed(s.handleBackupAction))
	s.mux.HandleFunc("GET /api/v1/diagnostics", s.authed(s.handleDiagnostics))
	s.mux.HandleFunc("GET /api/v1/diagnostics/bundle", s.authed(s.handleDiagnosticsBundle))
	s.mux.Handle("GET /assets/", assetHandler())
	s.mux.HandleFunc("GET /{$}", serveShell)
}

// withBaseMiddleware applies security headers and request logging to every
// response. It never logs credentials — only method, path, status, duration.
func (s *Server) withBaseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		// script-src stays locked to 'self' (no inline/eval script — the UI
		// is a bundled static file). style-src allows 'unsafe-inline' because
		// the component library sets element styles at runtime (animations);
		// that permits no code execution. font/img data: URIs are the bundled
		// assets.
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; "+
				"base-uri 'none'; frame-ancestors 'none'; object-src 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			h.Set("Cache-Control", "no-store")
		}

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Info("manager request",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
	})
}

// authed wraps an API handler with cookie-session lookup and CSRF enforcement.
// The resolved *session is passed via the request context.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, *session)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(sessionCookie)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "not authenticated")
			return
		}
		sess := s.sessions.get(c.Value)
		if sess == nil {
			clearSessionCookie(w, s.tls)
			writeError(w, http.StatusUnauthorized, "session expired or invalid")
			return
		}
		// CSRF: state-changing methods must present the per-session token.
		// Safe methods (GET/HEAD) rely on the SameSite=Strict session cookie
		// — and this also lets the SPA bootstrap a still-valid cookie session
		// via GET /api/v1/session to recover the token after a page reload.
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !sess.checkCSRF(r.Header.Get(csrfHeader)) {
				writeError(w, http.StatusForbidden, "missing or invalid "+csrfHeader)
				return
			}
		}
		sess.touch()
		h(w, r, sess)
	}
}

type statusWriter struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (w *statusWriter) WriteHeader(code int) {
	if !w.wrote {
		w.status = code
		w.wrote = true
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	w.wrote = true
	return w.ResponseWriter.Write(b)
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil && h != "" {
		return h
	}
	return "localhost"
}
