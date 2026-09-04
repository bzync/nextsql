package installgui

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/version"
)

// Server is the installer's bounded HTTP service. Construct it with New, run
// it with Serve, and stop it with Shutdown/Close.
type Server struct {
	cfg   Config
	log   *slog.Logger
	token string
	run   *runner
	mux   *http.ServeMux

	http *http.Server
	ln   net.Listener
	tls  bool
}

// Options are optional injectables.
type Options struct {
	Logger *slog.Logger
}

// New validates the config, generates the single-run token, and binds the
// listener. Binding happens here so a caller fails startup atomically on an
// unusable address.
func New(cfg Config, opt Options) (*Server, error) {
	cfg = cfg.withDefaults()
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	log := opt.Logger
	if log == nil {
		log = logging.New(cfg.LogLevel, os.Stderr)
	}
	token, err := generateToken()
	if err != nil {
		return nil, err
	}

	s := &Server{
		cfg:   cfg,
		log:   log,
		token: token,
		run:   newRunner(cfg.NextSQLBin, cfg.RunTimeout),
		mux:   http.NewServeMux(),
	}
	s.routes()

	hs := &http.Server{
		Handler:           s.withBaseMiddleware(s.mux),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       20 * time.Second,
		WriteTimeout:      cfg.RunTimeout + 30*time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if cfg.ListenTLSCert != "" {
		tlsCfg, err := security.ServerTLS(cfg.ListenTLSCert, cfg.ListenTLSKey)
		if err != nil {
			return nil, err
		}
		hs.TLSConfig = tlsCfg
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "installgui.New", "listen", err)
	}
	s.http = hs
	s.ln = ln
	s.tls = hs.TLSConfig != nil
	return s, nil
}

// Addr is the bound listener address (useful when port 0 was requested).
func (s *Server) Addr() net.Addr { return s.ln.Addr() }

// Token is the single-run credential embedded in the URL Serve's caller
// should open. It never appears in logs.
func (s *Server) Token() string { return s.token }

// URL is the full first-page URL to open in a browser, including the token.
func (s *Server) URL() string {
	scheme := "http"
	if s.tls {
		scheme = "https"
	}
	return scheme + "://" + s.Addr().String() + "/?" + tokenParam + "=" + s.token
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

// Shutdown gracefully stops the listener.
func (s *Server) Shutdown(ctx context.Context) error {
	if s.http == nil {
		return nil
	}
	return s.http.Shutdown(ctx)
}

// Close immediately stops the listener.
func (s *Server) Close() error {
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
	s.mux.HandleFunc("GET /{$}", s.serveShell)
	s.mux.Handle("GET /assets/", assetHandler())
	s.mux.HandleFunc("GET /api/v1/hello", s.authed(s.handleHello))
	s.mux.HandleFunc("POST /api/v1/plan", s.authed(s.handlePlan))
	s.mux.HandleFunc("POST /api/v1/install", s.authed(s.handleInstall))
}

// withBaseMiddleware applies security headers and request logging. It never
// logs the token, request bodies, or subprocess output — only method, path,
// status, duration.
func (s *Server) withBaseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self'; connect-src 'self'; "+
				"base-uri 'none'; frame-ancestors 'none'; object-src 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("X-Frame-Options", "DENY")
		if r.URL.Path == "/api/v1/plan" || r.URL.Path == "/api/v1/install" {
			h.Set("Cache-Control", "no-store")
		}

		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(sw, r)
		s.log.Info("installer request",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
	})
}

// authed enforces the single-run token: from the cookie set on first load,
// or from the X-Installer-Token header the bundled JS attaches, or (only for
// the very first GET /) from the ?token= query string. Everything else is
// 403 with no further detail.
func (s *Server) authed(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.checkToken(r) {
			writeError(w, http.StatusForbidden, "missing or invalid "+tokenHeader)
			return
		}
		h(w, r)
	}
}

func (s *Server) checkToken(r *http.Request) bool {
	if c, err := r.Cookie(tokenCookie); err == nil && tokenEqual(c.Value, s.token) {
		return true
	}
	if h := r.Header.Get(tokenHeader); h != "" && tokenEqual(h, s.token) {
		return true
	}
	return false
}

// serveShell serves the wizard shell. The very first load carries ?token=
// (from the URL nextsql-install printed/opened); on success it sets the
// cookie so a reload works without the query string, then everything else
// (assets, API) relies on the cookie or the header.
func (s *Server) serveShell(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get(tokenParam)
	authorized := s.checkToken(r) || (q != "" && tokenEqual(q, s.token))
	if !authorized {
		writeError(w, http.StatusForbidden, "missing or invalid token")
		return
	}
	if q != "" {
		http.SetCookie(w, &http.Cookie{
			Name:     tokenCookie,
			Value:    s.token,
			Path:     "/",
			HttpOnly: false, // the bundled JS must read it back to set the API header
			SameSite: http.SameSiteStrictMode,
			Secure:   s.tls,
		})
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

func (s *Server) handleHello(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		NextSQLVersion string   `json:"nextsql_version"`
		Phase          int      `json:"phase"`
		Defaults       Defaults `json:"defaults"`
	}{version.String, version.Phase, detectDefaults()})
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	s.handleRun(w, r, true)
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.handleRun(w, r, false)
}

func (s *Server) handleRun(w http.ResponseWriter, r *http.Request, dryRun bool) {
	var p Params
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if err := p.Validate(); err != nil {
		writeJSON(w, http.StatusOK, runResult{Error: err.Error()})
		return
	}
	result := s.run.run(r.Context(), p, dryRun)
	writeJSON(w, http.StatusOK, result)
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{msg})
}
