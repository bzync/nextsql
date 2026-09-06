package browseropen

import "testing"

// TestOpenUnavailableCommand exercises the failure path without actually
// launching a browser: on a host missing the platform opener binary, Open
// must return an error rather than panicking or hanging.
func TestOpenUnavailableCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Open("http://127.0.0.1:1/"); err == nil {
		t.Fatal("expected an error when no browser-opener command is on PATH")
	}
}
