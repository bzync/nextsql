package admin

import (
	"context"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/bzync/nextsql/internal/admin/ops"
	"github.com/bzync/nextsql/internal/admin/setup"
	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
)

// Server is NextSQL Admin's one HTTP service: the shared listener, TLS
// termination, the shared embedded frontend shell, and the active mode's
// route table mounted underneath it.
type Server struct {
	cfg  Config
	log  *slog.Logger
	mode Mode

	setupApp *setup.Server // non-nil only when mode == ModeSetup
	opsApp   *ops.Server   // non-nil only when mode == ModeOperate

	mux *http.ServeMux
	http *http.Server
	ln   net.Listener
	tls  bool
}

// Options are optional injectables.
type Options struct {
	Logger *slog.Logger
}

// New resolves the mode (auto-detecting when cfg.Mode is empty), builds the
// active mode's sub-app, and binds the one process listener.
func New(cfg Config, opt Options) (*Server, error) {
	cfg = cfg.withDefaults()

	mode := cfg.Mode
	if mode == "" {
		detected, err := detectMode(context.Background(), cfg.NextSQLBin, cfg.DataDirHint)
		if err != nil {
			return nil, err
		}
		mode = detected
	}
	cfg.Listen = cfg.listenDefault(mode)

	if err := cfg.validate(mode); err != nil {
		return nil, err
	}

	log := opt.Logger
	if log == nil {
		log = logging.New(cfg.LogLevel, os.Stderr)
	}

	s := &Server{cfg: cfg, log: log, mode: mode, mux: http.NewServeMux()}

	switch mode {
	case ModeSetup:
		app, err := setup.New(cfg.setupConfig(), setup.Options{Logger: log})
		if err != nil {
			return nil, err
		}
		s.setupApp = app
	case ModeOperate:
		app, err := ops.New(cfg.opsConfig(), ops.Options{Logger: log})
		if err != nil {
			return nil, err
		}
		s.opsApp = app
	}
	s.routes()

	writeTimeout := 30 * time.Second
	if mode == ModeSetup {
		writeTimeout = s.setupApp.RunTimeout() + 30*time.Second
	}
	hs := &http.Server{
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       60 * time.Second,
	}
	if cfg.ListenTLSCert != "" {
		tlsCfg, err := security.ServerTLS(cfg.ListenTLSCert, cfg.ListenTLSKey)
		if err != nil {
			_ = s.closeApps()
			return nil, err
		}
		hs.TLSConfig = tlsCfg
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		_ = s.closeApps()
		return nil, nerr.Wrap(nerr.IO, "admin.New", "listen", err)
	}
	s.http = hs
	s.ln = ln
	s.tls = hs.TLSConfig != nil
	return s, nil
}

// Mode is the resolved mode this process is serving.
func (s *Server) Mode() Mode { return s.mode }

// Addr is the bound listener address (useful when port 0 was requested).
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// SetupURL is the full first-page URL to open in a browser, including the
// Setup-mode token. ok is false outside Setup mode.
func (s *Server) SetupURL() (url string, ok bool) {
	if s.mode != ModeSetup {
		return "", false
	}
	scheme := "http"
	if s.tls {
		scheme = "https"
	}
	return scheme + "://" + s.Addr().String() + "/?" + s.setupApp.URLQuery(), true
}

// Done is closed once the operator clicks "Finish" on Setup mode's
// completion screen, so the command layer can end the process without going
// back to a terminal to press Ctrl+C. In Operations mode it is never closed.
func (s *Server) Done() <-chan struct{} {
	if s.mode == ModeSetup {
		return s.setupApp.Done()
	}
	return nil
}

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

// Shutdown gracefully stops the listener and releases the active mode's
// resources.
func (s *Server) Shutdown(ctx context.Context) error {
	_ = s.closeApps()
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// Close immediately stops the listener and releases the active mode's
// resources.
func (s *Server) Close() error {
	_ = s.closeApps()
	if s.http == nil {
		return nil
	}
	return s.http.Close()
}

func (s *Server) closeApps() error {
	if s.setupApp != nil {
		return s.setupApp.Close()
	}
	if s.opsApp != nil {
		return s.opsApp.Close()
	}
	return nil
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /healthz", s.withHeaders(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	}))
	s.mux.HandleFunc("GET /api/v1/mode", s.withHeaders(s.handleMode))
	s.mux.HandleFunc("GET /{$}", s.withHeaders(s.serveShell))
	s.mux.Handle("GET /assets/", s.withHeadersHandler(assetHandler()))
	s.mux.Handle("GET /icons/", s.withHeadersHandler(iconsHandler()))

	// PWA static files: manifest, service worker, and touch icons, each
	// reachable at a fixed top-level path so the service worker's default
	// scope covers the whole origin. sw.js gets no-cache so a new release's
	// worker (and its own build-hashed cache name) is picked up promptly.
	s.mux.HandleFunc("GET /manifest.webmanifest", s.withHeaders(
		servePWAFile("manifest.webmanifest", "application/manifest+json; charset=utf-8", "public, max-age=300")))
	s.mux.HandleFunc("GET /sw.js", s.withHeaders(
		servePWAFile("sw.js", "application/javascript; charset=utf-8", "no-cache")))
	s.mux.HandleFunc("GET /favicon.ico", s.withHeaders(
		servePWAFile("favicon.ico", "image/x-icon", "public, max-age=300")))
	s.mux.HandleFunc("GET /apple-icon.png", s.withHeaders(
		servePWAFile("apple-icon.png", "image/png", "public, max-age=300")))

	var sub http.Handler
	switch s.mode {
	case ModeSetup:
		sub = s.setupApp.Handler()
	case ModeOperate:
		sub = s.opsApp.Handler()
	}
	// The active sub-app's own Handler already applies its own security
	// headers + request logging (setup/ops keep those close to their
	// routes); mounting it directly under the shared "/api/v1/" subtree
	// avoids double-wrapping while still reaching every /api/v1/* path this
	// top mux does not claim more specifically (mode is registered above).
	s.mux.Handle("/api/v1/", sub)
}

// handleMode is intentionally unauthenticated: the frontend calls it first,
// before it knows whether to render the Setup wizard or the Operations/
// Studio shell.
func (s *Server) handleMode(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		Mode string `json:"mode"`
	}{string(s.mode)})
}

// serveShell serves the shared embedded SPA shell. In Setup mode the very
// first load must carry the single-run token (query string, then cookie);
// Operations mode's shell has always been unauthenticated — the login form
// lives inside the bundle itself.
func (s *Server) serveShell(w http.ResponseWriter, r *http.Request) {
	if s.mode == ModeSetup {
		if !s.setupApp.AuthenticateShellRequest(w, r) {
			writeError(w, http.StatusForbidden, "missing or invalid token")
			return
		}
	}
	b, err := webFS.ReadFile("web/index.html")
	if err != nil {
		http.Error(w, "shell missing", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	_, _ = w.Write(b)
}

// withHeaders applies the shared security headers + request logging to a
// top-level route (mode/shell/assets/healthz) the same way each sub-app
// already applies them to its own routes.
func (s *Server) withHeaders(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.withHeadersHandler(h).ServeHTTP(w, r)
	}
}

func (s *Server) withHeadersHandler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; "+
				"worker-src 'self'; manifest-src 'self'; "+
				"base-uri 'none'; frame-ancestors 'none'; object-src 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Info("admin request",
			"mode", s.mode, "method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
	})
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
