// Package browseropen launches the operator's default web browser at a URL.
// It is used by anything that needs a human to complete a step in a browser
// tab — an OIDC login redirect, the GUI installer's first-run wizard — and
// nowhere else; it never inspects or modifies the URL.
package browseropen

import (
	"os/exec"
	"runtime"

	"github.com/bzync/nextsql/internal/nerr"
)

// Open launches url with the platform's default handler. The child process
// is detached (Start, not Run) so a slow or hung browser never blocks the
// caller; its exit is reaped in the background.
func Open(url string) error {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd, args = "xdg-open", []string{url}
	}
	c := exec.Command(cmd, args...)
	if err := c.Start(); err != nil {
		return nerr.Wrap(nerr.Unavailable, "browseropen", "launch browser", err)
	}
	go func() { _ = c.Wait() }()
	return nil
}
