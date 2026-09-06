package setup

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// runner shells out to the `nextsql` binary. It is the only place
// setup touches a process boundary that can see key material — and
// even here it never sees any: the admin password crosses into a mode-0600
// temp file for the lifetime of one subprocess call and is removed
// immediately after, and stdout/stderr from the child are never logged
// (only surfaced to the operator's own browser tab, over loopback, on their
// own request).
type runner struct {
	bin     string
	timeout time.Duration
}

func newRunner(bin string, timeout time.Duration) *runner {
	return &runner{bin: bin, timeout: timeout}
}

// runResult is what plan/install handlers hand back to the browser: the raw
// JSON `nextsql setup --json` printed, or a failure with whatever it wrote
// to stderr (nextsql never writes secrets to stderr on failure — same
// contract every other NextSQL CLI failure path already relies on).
type runResult struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  string          `json:"error,omitempty"`
	// Service is set only after a real (non-dry-run) install where the
	// operator checked "start at boot" — see server.go's post-install
	// handling. Its own failure never flips OK to false: the database
	// install this follows has already succeeded independently of it.
	Service *serviceOutcome `json:"service,omitempty"`
}

// serviceOutcome is the result of the best-effort `systemctl enable --now`
// step handleRun runs after a successful install (see server.go). Skipped
// (Error set, Enabled false) is the expected outcome on any host that
// wasn't set up by a packaged installer first — it is not a fault.
//
// Enabled and Active are reported separately and deliberately: `systemctl
// enable --now` exits 0 once the unit is registered to start at boot and a
// start has been *issued*, even when the started process then immediately
// exits (confirmed live — a Type=simple unit whose ExecStart fails right
// away still gets a 0 exit from `enable --now`). So Enabled=true only means
// "will start at boot"; Active is a second, separate post-check
// (DetectService again, after EnableService returns) of whether it is
// actually running right now. A caller must not read Enabled alone as "the
// server is up."
type serviceOutcome struct {
	Enabled bool   `json:"enabled"`
	Active  bool   `json:"active"`
	Error   string `json:"error,omitempty"`
}

// run executes `nextsql setup <args...>`, writing p.AdminPassword to a
// private temp file for the duration of the call when an admin user is set.
func (rn *runner) run(ctx context.Context, p Params, dryRun bool) runResult {
	var passwordFile string
	if p.AdminUser != "" {
		f, err := os.CreateTemp("", "nextsql-admin-setup-pw-*")
		if err != nil {
			return runResult{Error: nerr.Wrap(nerr.IO, "setup.runner", "create password temp file", err).Error()}
		}
		passwordFile = f.Name()
		defer func() { _ = os.Remove(passwordFile) }()
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return runResult{Error: nerr.Wrap(nerr.IO, "setup.runner", "chmod password temp file", err).Error()}
		}
		if _, err := f.WriteString(p.AdminPassword); err != nil {
			_ = f.Close()
			return runResult{Error: nerr.Wrap(nerr.IO, "setup.runner", "write password temp file", err).Error()}
		}
		if err := f.Close(); err != nil {
			return runResult{Error: nerr.Wrap(nerr.IO, "setup.runner", "close password temp file", err).Error()}
		}
	}

	cctx, cancel := context.WithTimeout(ctx, rn.timeout)
	defer cancel()

	args := p.toArgs(dryRun, passwordFile)
	cmd := exec.CommandContext(cctx, rn.bin, args...)
	// A minimal, explicit environment: no inherited NEXTSQL_* variables from
	// whatever launched nextsql-admin should silently change what gets
	// installed. PATH is kept so the child can resolve its own dependencies.
	cmd.Env = []string{"PATH=" + os.Getenv("PATH")}
	if home, ok := os.LookupEnv("HOME"); ok {
		cmd.Env = append(cmd.Env, "HOME="+home)
	}
	if userProfile, ok := os.LookupEnv("USERPROFILE"); ok {
		cmd.Env = append(cmd.Env, "USERPROFILE="+userProfile)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if err != nil {
		msg := stderr.String()
		if msg == "" {
			msg = err.Error()
		}
		return runResult{Error: msg}
	}

	out := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(out) {
		return runResult{Error: "nextsql setup produced no valid JSON output"}
	}
	return runResult{OK: true, Result: json.RawMessage(out)}
}

// ResolveNextSQLBin finds the `nextsql` binary: next to this process's own
// executable first (how every packaging artifact ships it), then PATH. An
// explicit override always wins. The command layer calls this before New so
// a missing binary fails fast with a clear message.
func ResolveNextSQLBin(override string) (string, error) {
	name := "nextsql"
	if os.PathSeparator == '\\' {
		name = "nextsql.exe"
	}
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", nerr.Wrap(nerr.NotFound, "setup.ResolveNextSQLBin", "--nextsql-bin", err)
		}
		return override, nil
	}
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), name)
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, nil
		}
	}
	if found, err := exec.LookPath(name); err == nil {
		return found, nil
	}
	return "", nerr.New(nerr.NotFound, "setup.ResolveNextSQLBin",
		"could not find the nextsql binary next to nextsql-admin or on PATH; pass --nextsql-bin")
}
