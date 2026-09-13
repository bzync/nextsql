// Package browseropen launches the operator's default web browser at a URL.
// It is used by anything that needs a human to complete a step in a browser
// tab — an OIDC login redirect, the GUI installer's first-run wizard — and
// nowhere else; it never inspects or modifies the URL.
package browseropen

import (
	"os"
	"os/exec"
	"runtime"

	"github.com/bzync/nextsql/internal/nerr"
)

// Open launches url with the platform's default handler. The child process
// is detached (Start, not Run) so a slow or hung browser never blocks the
// caller; its exit is reaped in the background.
func Open(url string) error {
	cmd, args := opener(runtime.GOOS, isWSL(), exec.LookPath, url)
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		return nerr.Wrap(nerr.Unavailable, "browseropen", "launch browser", err)
	}
	go func() { _ = c.Wait() }()
	return nil
}

// opener picks the command that hands url to the desktop browser. Under WSL
// the browser belongs to the Windows host and xdg-open is usually absent, so
// wslview (from wslu) is preferred and explorer.exe, reachable through WSL's
// Windows interop, is the fallback. WSL 2 forwards loopback, so a 127.0.0.1
// URL served inside the distribution opens in the host browser.
func opener(goos string, wsl bool, lookPath func(string) (string, error), url string) (string, []string) {
	switch {
	case goos == "darwin":
		return "open", []string{url}
	case wsl:
		if _, err := lookPath("wslview"); err == nil {
			return "wslview", []string{url}
		}
		return "explorer.exe", []string{url}
	default:
		return "xdg-open", []string{url}
	}
}

// isWSL reports whether this Linux process runs inside Windows Subsystem for
// Linux. WSL sets WSL_DISTRO_NAME in every distribution's environment and
// WSL_INTEROP when Windows interop is available.
func isWSL() bool {
	return runtime.GOOS == "linux" && (os.Getenv("WSL_DISTRO_NAME") != "" || os.Getenv("WSL_INTEROP") != "")
}
