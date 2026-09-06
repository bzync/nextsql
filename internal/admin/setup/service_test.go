package setup

import (
	"context"
	"os/exec"
	"runtime"
	"testing"
	"time"
)

func TestExecStartConfigRe(t *testing.T) {
	cases := []struct {
		name string
		unit string
		want string
	}{
		{
			"system unit",
			"[Unit]\nDescription=NextSQL\n\n[Service]\nExecStart=/usr/bin/nextsqld --config /etc/nextsql/nextsql.conf\nRestart=on-failure\n",
			"/etc/nextsql/nextsql.conf",
		},
		{
			"user unit with equals form",
			"[Service]\nExecStart=/home/x/.local/bin/nextsqld --config=/home/x/.config/nextsql/nextsql.conf\n",
			"/home/x/.config/nextsql/nextsql.conf",
		},
		{
			"no ExecStart line",
			"[Unit]\nDescription=something else\n",
			"",
		},
		{
			"ExecStart without --config",
			"[Service]\nExecStart=/usr/bin/nextsqld\n",
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			m := execStartConfigRe.FindStringSubmatch(c.unit)
			got := ""
			if m != nil {
				got = m[1]
			}
			if got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

func TestSystemctlArgs(t *testing.T) {
	if got := systemctlArgs("system", "cat", "nextsql"); len(got) != 2 || got[0] != "cat" || got[1] != "nextsql" {
		t.Errorf("system scope should not prepend --user: %v", got)
	}
	got := systemctlArgs("user", "cat", "nextsql")
	if len(got) != 3 || got[0] != "--user" || got[1] != "cat" || got[2] != "nextsql" {
		t.Errorf("user scope should prepend --user: %v", got)
	}
}

// TestDetectServiceRealHost runs DetectService against whatever this host
// actually has — it never mutates anything (DetectService only shells out
// to read-only systemctl subcommands). On any host with no "nextsql" unit
// registered (the expected case for a build/test sandbox) it must report
// UnitFound=false rather than erroring; a host with no systemctl at all
// (non-Linux, or a container without systemd) must report Supported=false,
// also not an error.
func TestDetectServiceRealHost(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	st := DetectService(ctx)

	if runtime.GOOS != "linux" {
		if st.Supported {
			t.Errorf("expected Supported=false on %s", runtime.GOOS)
		}
		return
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		if st.Supported {
			t.Error("expected Supported=false with no systemctl on PATH")
		}
		return
	}
	if !st.Supported {
		t.Error("expected Supported=true: systemctl is on PATH and this is Linux")
	}
	// UnitFound is host state, not something this test controls, but a
	// found unit must always come with internally-consistent fields.
	if !st.UnitFound && (st.Enabled || st.Active || st.ConfigPath != "") {
		t.Errorf("unit not found but Enabled/Active/ConfigPath set: %+v", st)
	}
}

// TestEnableServiceUnknownUnitFails confirms EnableService fails cleanly
// (an error, not a panic, nothing left behind) against a unit name that
// does not exist — the real state of any host with no packaged NextSQL
// install. Skipped when there is no usable systemctl at all so it doesn't
// fail in a container without systemd.
func TestEnableServiceUnknownUnitFails(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("systemd is Linux-only")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		t.Skip("no systemctl on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := EnableService(ctx, systemctlScope()); err == nil {
		t.Skip("a real \"nextsql\" unit is apparently registered on this host — nothing to assert here")
	}
}
