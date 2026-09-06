package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

// port80Deps isolates grantPort80's OS interactions so its branching --
// unsupported platform, missing tool, failing command, success -- is unit
// testable without actually invoking setcap or depending on the real
// executable path.
type port80Deps struct {
	goos       string
	lookPath   func(string) (string, error)
	executable func() (string, error)
	realpath   func(string) (string, error)
	run        func(setcapPath, target string) ([]byte, error)
}

func defaultPort80Deps() port80Deps {
	return port80Deps{
		goos:       runtime.GOOS,
		lookPath:   exec.LookPath,
		executable: os.Executable,
		realpath:   filepath.EvalSymlinks,
		run: func(setcapPath, target string) ([]byte, error) {
			return exec.Command(setcapPath, "cap_net_bind_service=+ep", target).CombinedOutput()
		},
	}
}

// grantPort80 lets an unprivileged nextsql-admin process bind well-known
// ports (so the Admin UI is reachable at e.g. http://127.0.0.1/ with no
// port number) without ever running the server itself as root. It is
// strictly opt-in -- an operator runs "nextsql-admin --grant-port-80" once,
// typically via sudo since only root can add file capabilities -- and it
// only ever touches this executable's own file capabilities on disk, never
// the calling process's live privileges. The default listener
// (127.0.0.1:7220) is unaffected either way; this only makes --listen
// 127.0.0.1:80 possible afterward.
func grantPort80(d port80Deps) (string, error) {
	if d.goos != "linux" {
		return "", nerr.New(nerr.Unavailable, "nextsql-admin.grantPort80",
			"--grant-port-80 is only implemented on Linux (file capabilities); "+
				"on other platforms, run nextsql-admin with administrator privileges to bind port 80, "+
				"or keep the default 127.0.0.1:7220 listener")
	}
	setcapPath, err := d.lookPath("setcap")
	if err != nil {
		return "", nerr.New(nerr.Unavailable, "nextsql-admin.grantPort80",
			"the \"setcap\" tool was not found on PATH "+
				"(install it first, e.g. \"apt install libcap2-bin\" or \"dnf install libcap\", then retry)")
	}
	self, err := d.executable()
	if err != nil {
		return "", nerr.Wrap(nerr.Internal, "nextsql-admin.grantPort80", "resolve this executable's path", err)
	}
	if real, err := d.realpath(self); err == nil {
		self = real
	}
	out, err := d.run(setcapPath, self)
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return "", nerr.New(nerr.Forbidden, "nextsql-admin.grantPort80",
			fmt.Sprintf("setcap failed (%s) — rerun as root, e.g. \"sudo nextsql-admin --grant-port-80\"", msg))
	}
	return fmt.Sprintf(
		"granted %s permission to bind privileged ports (e.g. 127.0.0.1:80) without root.\n"+
			"Start it with: nextsql-admin --listen 127.0.0.1:80", self), nil
}
