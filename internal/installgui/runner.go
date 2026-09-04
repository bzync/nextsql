package installgui

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
// installgui touches a process boundary that can see key material — and
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
}

// run executes `nextsql setup <args...>`, writing p.AdminPassword to a
// private temp file for the duration of the call when an admin user is set.
func (rn *runner) run(ctx context.Context, p Params, dryRun bool) runResult {
	var passwordFile string
	if p.AdminUser != "" {
		f, err := os.CreateTemp("", "nextsql-install-pw-*")
		if err != nil {
			return runResult{Error: nerr.Wrap(nerr.IO, "installgui.runner", "create password temp file", err).Error()}
		}
		passwordFile = f.Name()
		defer func() { _ = os.Remove(passwordFile) }()
		if err := f.Chmod(0o600); err != nil {
			_ = f.Close()
			return runResult{Error: nerr.Wrap(nerr.IO, "installgui.runner", "chmod password temp file", err).Error()}
		}
		if _, err := f.WriteString(p.AdminPassword); err != nil {
			_ = f.Close()
			return runResult{Error: nerr.Wrap(nerr.IO, "installgui.runner", "write password temp file", err).Error()}
		}
		if err := f.Close(); err != nil {
			return runResult{Error: nerr.Wrap(nerr.IO, "installgui.runner", "close password temp file", err).Error()}
		}
	}

	cctx, cancel := context.WithTimeout(ctx, rn.timeout)
	defer cancel()

	args := p.toArgs(dryRun, passwordFile)
	cmd := exec.CommandContext(cctx, rn.bin, args...)
	// A minimal, explicit environment: no inherited NEXTSQL_* variables from
	// whatever launched nextsql-install should silently change what gets
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

// resolveNextSQLBin finds the `nextsql` binary: next to this process's own
// executable first (how every packaging artifact ships it), then PATH. An
// explicit override always wins.
func resolveNextSQLBin(override string) (string, error) {
	name := "nextsql"
	if os.PathSeparator == '\\' {
		name = "nextsql.exe"
	}
	if override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", nerr.Wrap(nerr.NotFound, "installgui.resolveNextSQLBin", "--nextsql-bin", err)
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
	return "", nerr.New(nerr.NotFound, "installgui.resolveNextSQLBin",
		"could not find the nextsql binary next to nextsql-install or on PATH; pass --nextsql-bin")
}
