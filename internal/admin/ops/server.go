package ops

import (
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/logging"
)

// Server is Operations mode's request handling. Construct it with New; the
// parent internal/admin package mounts Handler() on the process's one HTTP
// server — this type never binds a listener or runs its own http.Server.
type Server struct {
	cfg      Config
	log      *slog.Logger
	sessions *sessionStore
	mux      *http.ServeMux
	tls      bool

	handler http.Handler
}

// Options are optional injectables (a logger; otherwise one is built from the
// config log level).
type Options struct {
	Logger *slog.Logger
}

// New validates the config and starts the session store's sweep goroutine.
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
		tls:      cfg.TLS,
		sessions: newSessionStore(cfg.MaxSessions, cfg.IdleTimeout, cfg.SessionLifetime),
		mux:      http.NewServeMux(),
	}
	s.routes()
	s.handler = s.withBaseMiddleware(s.mux)
	return s, nil
}

// Handler is the /api/v1/* route table for Operations mode, already wrapped
// with security headers and request logging. The parent admin package mounts
// it under its own shared listener.
func (s *Server) Handler() http.Handler { return s.handler }

// Close releases Operations mode's resources: the session sweep goroutine and
// every live operator connection.
func (s *Server) Close() error {
	s.sessions.close()
	return nil
}

func (s *Server) routes() {
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
	s.mux.HandleFunc("GET /api/v1/studio/bootstrap", s.authed(s.handleStudioBootstrap))
	s.mux.HandleFunc("GET /api/v1/studio/table", s.authed(s.handleStudioTable))
	s.mux.HandleFunc("GET /api/v1/studio/workflows", s.authed(s.handleStudioWorkflows))
	s.mux.HandleFunc("GET /api/v1/studio/schema-graph", s.authed(s.handleStudioSchemaGraph))
	s.mux.HandleFunc("GET /api/v1/studio/migrations", s.authed(s.handleStudioMigrations))
	s.mux.HandleFunc("POST /api/v1/studio/query/analyze", s.authed(s.handleStudioAnalyze))
	s.mux.HandleFunc("POST /api/v1/studio/query/split", s.authed(s.handleStudioSplit))
	s.mux.HandleFunc("POST /api/v1/studio/query", s.authed(s.handleStudioQuery))
	s.mux.HandleFunc("POST /api/v1/studio/query/stream", s.authed(s.handleStudioQueryStream))
	s.mux.HandleFunc("POST /api/v1/studio/query/cancel", s.authed(s.handleStudioCancel))
	s.mux.HandleFunc("POST /api/v1/studio/reconnect", s.authed(s.handleStudioReconnect))
	s.mux.HandleFunc("POST /api/v1/studio/read-consistency", s.authed(s.handleStudioReadConsistency))
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
		s.log.Info("admin ops request",
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

// Flush preserves http.Flusher through the request-logging wrapper. Studio's
// NDJSON query endpoint depends on each bounded row batch reaching the
// browser before the full result finishes.
func (w *statusWriter) Flush() {
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func hostOf(addr string) string {
	if h, _, err := net.SplitHostPort(addr); err == nil && h != "" {
		return h
	}
	return "localhost"
}
