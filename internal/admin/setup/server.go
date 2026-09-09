package setup

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/logging"
	"github.com/bzync/nextsql/internal/version"
)

// Server is Setup mode's request handling. Construct it with New; the parent
// internal/admin package mounts Handler() on the process's one HTTP server —
// this type never binds a listener or runs its own http.Server.
type Server struct {
	cfg   Config
	log   *slog.Logger
	token string
	run   *runner
	mux   *http.ServeMux
	tls   bool

	handler  http.Handler
	done     chan struct{}
	doneOnce sync.Once
}

// Options are optional injectables.
type Options struct {
	Logger *slog.Logger
}

// New validates the config and generates the single-run token.
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
		tls:   cfg.TLS,
		run:   newRunner(cfg.NextSQLBin, cfg.RunTimeout),
		mux:   http.NewServeMux(),
		done:  make(chan struct{}),
	}
	s.routes()
	s.handler = s.withBaseMiddleware(s.mux)
	return s, nil
}

// Handler is the /api/v1/* route table for Setup mode, already wrapped with
// security headers and request logging. The parent admin package mounts it
// under its own shared listener.
func (s *Server) Handler() http.Handler { return s.handler }

// RunTimeout is how long the parent admin package should allow for a single
// request/response cycle, to accommodate the longest `nextsql setup`
// subprocess call this mode can trigger.
func (s *Server) RunTimeout() time.Duration { return s.cfg.RunTimeout }

// Token is the single-run credential embedded in the URL the command layer
// should open. It never appears in logs.
func (s *Server) Token() string { return s.token }

// Done is closed once the operator clicks "Finish" on the completion screen
// (POST /api/v1/finish). The command layer selects on it alongside its own
// interrupt signal, so a successful GUI install can end the process without
// the operator going back to a terminal to press Ctrl+C.
func (s *Server) Done() <-chan struct{} { return s.done }

// Close releases Setup mode's resources. It never fails — no goroutine or
// network resource in this package outlives a single request.
func (s *Server) Close() error { return nil }

// URLToken is the ?token= query parameter name/value pair the first shell
// load must carry.
func (s *Server) URLQuery() string { return tokenParam + "=" + s.token }

// AuthenticateShellRequest enforces Setup mode's single-run token against a
// request for the shared shell HTML: from the cookie set on a prior load, the
// X-Installer-Token header, or (only for the very first load) the ?token=
// query string — in which case it also sets the cookie so a reload works
// without the query string. It returns false when none of those match; the
// caller (the parent admin package's shell handler) is responsible for
// writing the 403.
func (s *Server) AuthenticateShellRequest(w http.ResponseWriter, r *http.Request) bool {
	q := r.URL.Query().Get(tokenParam)
	authorized := s.checkToken(r) || (q != "" && tokenEqual(q, s.token))
	if !authorized {
		return false
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
	return true
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /api/v1/hello", s.authed(s.handleHello))
	s.mux.HandleFunc("GET /api/v1/service", s.authed(s.handleService))
	s.mux.HandleFunc("GET /api/v1/browse", s.authed(s.handleBrowse))
	s.mux.HandleFunc("POST /api/v1/lifecycle/detect", s.authed(s.handleLifecycleDetect))
	s.mux.HandleFunc("POST /api/v1/plan", s.authed(s.handlePlan))
	s.mux.HandleFunc("POST /api/v1/install", s.authed(s.handleInstall))
	s.mux.HandleFunc("POST /api/v1/finish", s.authed(s.handleFinish))
}

// lifecycleDetectRequest is deliberately narrower than the CLI: Setup mode
// only exposes the read-only inspection needed to recognize an existing
// installation. Upgrade, repair, and uninstall retain their explicit CLI
// confirmations until their dedicated UX is designed.
type lifecycleDetectRequest struct {
	DataDir string `json:"dataDir"`
	Config  string `json:"config"`
}

func (s *Server) handleLifecycleDetect(w http.ResponseWriter, r *http.Request) {
	var req lifecycleDetectRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	req.DataDir = strings.TrimSpace(req.DataDir)
	req.Config = strings.TrimSpace(req.Config)
	if req.DataDir == "" || len(req.DataDir) > 4096 || strings.IndexByte(req.DataDir, 0) >= 0 || len(req.Config) > 4096 || strings.IndexByte(req.Config, 0) >= 0 {
		writeError(w, http.StatusBadRequest, "dataDir and config must be valid paths")
		return
	}
	args := []string{"lifecycle", "detect", "--json", "--data-dir", req.DataDir}
	if req.Config != "" {
		args = append(args, "--config", req.Config)
	}
	writeJSON(w, http.StatusOK, s.run.runArgs(r.Context(), args))
}

// withBaseMiddleware applies security headers and request logging. It never
// logs the token, request bodies, or subprocess output — only method, path,
// status, duration.
func (s *Server) withBaseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
				"img-src 'self' data:; font-src 'self' data:; connect-src 'self'; "+
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
		s.log.Info("admin setup request",
			"method", r.Method, "path", r.URL.Path,
			"status", sw.status, "dur_ms", time.Since(start).Milliseconds())
	})
}

// authed enforces the single-run token: from the cookie set on first load,
// or from the X-Installer-Token header the bundled JS attaches. Everything
// else is 403 with no further detail.
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

func (s *Server) handleHello(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		NextSQLVersion string   `json:"nextsql_version"`
		Phase          int      `json:"phase"`
		Defaults       Defaults `json:"defaults"`
	}{version.String, version.Phase, detectDefaults()})
}

// handleService is read-only — DetectService never enables, starts, or
// writes anything (see service.go). It's a GET, unlike plan/install, since
// it takes no operator input and has no dry-run/commit distinction.
func (s *Server) handleService(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, DetectService(r.Context()))
}

func (s *Server) handlePlan(w http.ResponseWriter, r *http.Request) {
	s.handleRun(w, r, true)
}

func (s *Server) handleInstall(w http.ResponseWriter, r *http.Request) {
	s.handleRun(w, r, false)
}

func (s *Server) handleFinish(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		OK bool `json:"ok"`
	}{true})
	s.doneOnce.Do(func() { close(s.done) })
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
	if !dryRun && result.OK && p.EnableService {
		result.Service = s.maybeEnableService(r.Context(), result.Result)
	}
	writeJSON(w, http.StatusOK, result)
}

// setupResultForService is the handful of `nextsql setup --json` fields
// needed to decide whether enabling a service is safe — a local echo of
// cmd/nextsql's own setupResult/setupHealth shapes, not a shared type,
// since this package does not import anything from cmd/nextsql either.
type setupResultForService struct {
	ConfigPath  string `json:"config_path"`
	Initialized bool   `json:"initialized"`
	Health      *struct {
		OK bool `json:"ok"`
	} `json:"health"`
}

// maybeEnableService runs after a successful, non-dry-run `nextsql setup`
// when the operator asked for "start at boot". It only ever enables a unit
// that (a) already exists in this operator's own privilege scope and (b)
// already points its --config at the exact path this run just wrote — never
// a unit it has to guess about, and never one it authors itself. Every
// refusal path returns a clear, non-fatal reason rather than silently doing
// nothing; the caller (handleRun) always still reports OK:true for the
// install itself regardless of this outcome.
func (s *Server) maybeEnableService(ctx context.Context, raw json.RawMessage) *serviceOutcome {
	var summary setupResultForService
	if err := json.Unmarshal(raw, &summary); err != nil {
		return &serviceOutcome{Error: "could not read the setup result to check the service unit"}
	}
	if !summary.Initialized {
		return &serviceOutcome{Error: "skipped: no database was initialized this run"}
	}
	if summary.Health == nil || !summary.Health.OK {
		return &serviceOutcome{Error: "skipped: health check did not pass"}
	}
	svc := DetectService(ctx)
	switch {
	case !svc.Supported:
		return &serviceOutcome{Error: "service management is not available on this host"}
	case !svc.UnitFound:
		return &serviceOutcome{Error: "no nextsql service unit is installed on this host yet — install via a packaged (.deb/.tar.gz/.run) installer first, or run `systemctl enable --now nextsql` yourself after registering one"}
	case svc.ConfigPath != "" && svc.ConfigPath != summary.ConfigPath:
		return &serviceOutcome{Error: "the installed service unit points at a different configuration (" + svc.ConfigPath + "); refusing to enable it against a mismatched config"}
	}
	if err := EnableService(ctx, svc.Scope); err != nil {
		return &serviceOutcome{Error: err.Error()}
	}
	// `systemctl enable --now` exits 0 once the unit is registered and a
	// start has been issued, even if the started process exits immediately
	// after (confirmed live against a real Type=simple unit) — so Enabled
	// alone does not mean "running." WaitActive re-checks separately, with
	// a short bounded grace period for a slow start (WAL recovery, catalog
	// decode) rather than judging on the instant enable --now returns.
	if !WaitActive(ctx, svc.Scope, 4, 500*time.Millisecond) {
		scopeFlag := ""
		if svc.Scope == "user" {
			scopeFlag = "--user "
		}
		return &serviceOutcome{
			Enabled: true,
			Active:  false,
			Error:   "enabled to start at boot, but it did not stay running — check `systemctl " + scopeFlag + "status nextsql` for why",
		}
	}
	return &serviceOutcome{Enabled: true, Active: true}
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
