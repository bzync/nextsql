package setup

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// ServiceStatus is what the wizard shows for the optional "start
// automatically at boot" checkbox (PROJECT.md §46 "service registration").
// Detection is entirely read-only — it never enables, starts, or writes
// anything (see EnableService for the one mutating call) — and it never
// authors a systemd unit itself: that stays the packaged OS installer's job
// (packaging/linux/nextsql.service / nextsql.user.service). This wizard only
// offers to flip an *already-installed, already-matching* unit on, the same
// manual step packaging/README.md documents (`systemctl enable --now
// nextsql`), so a from-source or non-packaged `nextsql-admin` run simply
// reports no unit found rather than inventing one.
type ServiceStatus struct {
	// Supported is false on any non-Linux host or one with no `systemctl` on
	// PATH — this is an optional convenience, never a requirement, and a
	// false Supported is not an error.
	Supported bool `json:"supported"`
	// Scope is which systemctl invocation this process's own privilege
	// level would act on: "system" if running as root (matching
	// detectDefaults' own os.Geteuid()==0 check), "user" otherwise — the
	// same split packaging/linux/tarball/install.sh uses.
	Scope string `json:"scope"`
	// UnitFound reports whether a "nextsql" unit already exists in that
	// scope. False is the expected, unremarkable case for anyone who didn't
	// install via a packaged (.deb/.tar.gz/.run) installer first.
	UnitFound bool `json:"unitFound"`
	// ConfigPath is the --config path the unit's ExecStart= line actually
	// resolves to, parsed from `systemctl cat`. Empty if UnitFound is false
	// or the line couldn't be parsed. EnableService's caller compares this
	// against the config `nextsql setup` just wrote before ever enabling —
	// enabling a unit that points at a different configuration would start
	// the wrong database, or silently do nothing if that path doesn't exist
	// (the shipped units' own ConditionPathExists).
	ConfigPath string `json:"configPath"`
	Enabled    bool   `json:"enabled"`
	Active     bool   `json:"active"`
}

func systemctlScope() string {
	if runtime.GOOS != "linux" {
		return "system"
	}
	if os.Geteuid() == 0 {
		return "system"
	}
	return "user"
}

func systemctlArgs(scope string, rest ...string) []string {
	if scope == "user" {
		return append([]string{"--user"}, rest...)
	}
	return rest
}

// execStartConfigRe pulls the value following --config out of a unit's
// ExecStart= line, e.g. "ExecStart=/usr/bin/nextsqld --config /etc/nextsql/nextsql.conf".
var execStartConfigRe = regexp.MustCompile(`(?m)^ExecStart=.*--config[= ]+(\S+)`)

// DetectService probes for an existing "nextsql" systemd unit in the scope
// this process's own privilege level would act on. It only ever runs
// read-only systemctl subcommands (cat / is-enabled / is-active) — never
// enable, start, or edit. Any failure (systemctl missing, unit not found,
// permission denied) collapses to a zero-value-ish ServiceStatus rather than
// an error: the caller's job is to explain unavailability, not to treat it
// as a fault.
func DetectService(ctx context.Context) ServiceStatus {
	st := ServiceStatus{Scope: systemctlScope()}
	if runtime.GOOS != "linux" {
		return st
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return st
	}
	st.Supported = true

	out, err := exec.CommandContext(ctx, "systemctl", systemctlArgs(st.Scope, "cat", "nextsql")...).Output()
	if err != nil {
		// No unit installed in this scope. Not an error — the expected
		// state for anyone who didn't install via a packaged installer.
		return st
	}
	st.UnitFound = true
	if m := execStartConfigRe.FindStringSubmatch(string(out)); m != nil {
		st.ConfigPath = m[1]
	}

	if err := exec.CommandContext(ctx, "systemctl", systemctlArgs(st.Scope, "is-enabled", "--quiet", "nextsql")...).Run(); err == nil {
		st.Enabled = true
	}
	if err := exec.CommandContext(ctx, "systemctl", systemctlArgs(st.Scope, "is-active", "--quiet", "nextsql")...).Run(); err == nil {
		st.Active = true
	}
	return st
}

// WaitActive polls `systemctl is-active` for the "nextsql" unit in scope, a
// few times with a short pause, and reports whether it settled into the
// active state. It exists because `systemctl enable --now` itself exits 0
// once a start has been *issued*, even for a unit whose process exits
// immediately after (confirmed live) — a caller that wants to know "is it
// actually up" has to check separately, and a brand-new process (WAL
// recovery, catalog decode) may need a moment past the instant `enable
// --now` returns before systemd reflects "active". Bounded: at most
// attempts checks, waitBetween apart — never an unbounded wait.
func WaitActive(ctx context.Context, scope string, attempts int, waitBetween time.Duration) bool {
	for i := 0; i < attempts; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(waitBetween):
			}
		}
		if err := exec.CommandContext(ctx, "systemctl", systemctlArgs(scope, "is-active", "--quiet", "nextsql")...).Run(); err == nil {
			return true
		}
	}
	return false
}

// EnableService runs `systemctl [--user] enable --now nextsql` — the one
// mutating call in this file, gated behind the wizard's explicit checkbox
// and only ever invoked by the server after a successful, health-verified
// `nextsql setup` run against a unit whose own --config already matches the
// configuration just written (see server.go's post-install check). A
// failure here is always reported back as a separate, non-fatal field: the
// database install it follows has already succeeded independently of it.
func EnableService(ctx context.Context, scope string) error {
	out, err := exec.CommandContext(ctx, "systemctl", systemctlArgs(scope, "enable", "--now", "nextsql")...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return nerr.Wrap(nerr.IO, "setup.EnableService", "systemctl enable --now nextsql", errors.New(msg))
	}
	return nil
}
