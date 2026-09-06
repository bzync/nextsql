package dockerentry

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

const (
	waitAttempts = 300
	waitInterval = time.Second
	dialTimeout  = time.Second
)

// Runtime is the process/FS/network surface the wrapper needs. Tests swap
// the hooks; production uses DefaultRuntime.
type Runtime struct {
	NextSQL  string
	NextSQLd string
	Stderr   io.Writer
	Stdout   io.Writer
	Sleep    func(time.Duration)
	Dial     func(network, address string, timeout time.Duration) (net.Conn, error)
	Run      func(name string, args []string) error
	Exec     func(argv0 string, argv, envv []string) error
	Environ  func() []string
}

// DefaultRuntime binds the wrapper to this process: binaries next to the
// entrypoint, real sleep/dial/exec, inherited environment.
func DefaultRuntime() Runtime {
	dir := "/usr/local/bin"
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	return Runtime{
		NextSQL:  filepath.Join(dir, "nextsql"),
		NextSQLd: filepath.Join(dir, "nextsqld"),
		Stderr:   os.Stderr,
		Stdout:   os.Stdout,
		Sleep:    time.Sleep,
		Dial:     net.DialTimeout,
		Run: func(name string, args []string) error {
			cmd := exec.Command(name, args...)
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			cmd.Env = os.Environ()
			return cmd.Run()
		},
		Exec:    defaultExec,
		Environ: os.Environ,
	}
}

func (rt Runtime) logf(format string, args ...any) {
	if rt.Stderr == nil {
		return
	}
	fmt.Fprintf(rt.Stderr, "nextsql: "+format+"\n", args...)
}

func (rt Runtime) sleep() {
	if rt.Sleep != nil {
		rt.Sleep(waitInterval)
	}
}
