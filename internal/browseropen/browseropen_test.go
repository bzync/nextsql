package browseropen

import (
	"errors"
	"runtime"
	"slices"
	"testing"
)

// TestOpenUnavailableCommand exercises the failure path without actually
// launching a browser: on a host missing the platform opener binary, Open
// must return an error rather than panicking or hanging.
func TestOpenUnavailableCommand(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if err := Open("http://127.0.0.1:1/"); err == nil {
		t.Fatal("expected an error when no browser-opener command is on PATH")
	}
}

func TestOpenerChoosesPlatformCommand(t *testing.T) {
	const url = "http://127.0.0.1:7443/"
	found := func(string) (string, error) { return "/usr/bin/wslview", nil }
	missing := func(string) (string, error) { return "", errors.New("not found") }
	cases := []struct {
		name     string
		goos     string
		wsl      bool
		lookPath func(string) (string, error)
		want     string
	}{
		{"linux", "linux", false, found, "xdg-open"},
		{"darwin", "darwin", false, missing, "open"},
		{"wsl with wslu", "linux", true, found, "wslview"},
		{"wsl without wslu", "linux", true, missing, "explorer.exe"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cmd, args := opener(tc.goos, tc.wsl, tc.lookPath, url)
			if cmd != tc.want || !slices.Equal(args, []string{url}) {
				t.Fatalf("opener = %q %v, want %q [%s]", cmd, args, tc.want, url)
			}
		})
	}
}

func TestIsWSLReadsDistributionEnvironment(t *testing.T) {
	t.Setenv("WSL_DISTRO_NAME", "")
	t.Setenv("WSL_INTEROP", "")
	if isWSL() {
		t.Fatal("isWSL = true with no WSL environment")
	}
	t.Setenv("WSL_DISTRO_NAME", "Ubuntu")
	if got, want := isWSL(), runtime.GOOS == "linux"; got != want {
		t.Fatalf("isWSL = %v with WSL_DISTRO_NAME set, want %v", got, want)
	}
}
