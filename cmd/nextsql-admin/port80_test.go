package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestGrantPort80UnsupportedPlatform(t *testing.T) {
	_, err := grantPort80(port80Deps{goos: "darwin"})
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "Linux") {
		t.Fatalf("error should mention Linux-only support: %v", err)
	}
}

func TestGrantPort80MissingSetcap(t *testing.T) {
	_, err := grantPort80(port80Deps{
		goos:     "linux",
		lookPath: func(string) (string, error) { return "", errors.New("not found") },
	})
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("want unavailable, got %v", err)
	}
	if !strings.Contains(err.Error(), "setcap") {
		t.Fatalf("error should name the missing tool: %v", err)
	}
}

func TestGrantPort80ExecutableResolutionFails(t *testing.T) {
	_, err := grantPort80(port80Deps{
		goos:       "linux",
		lookPath:   func(string) (string, error) { return "/usr/sbin/setcap", nil },
		executable: func() (string, error) { return "", errors.New("boom") },
	})
	if !nerr.HasCode(err, nerr.Internal) {
		t.Fatalf("want internal, got %v", err)
	}
}

func TestGrantPort80CommandFails(t *testing.T) {
	_, err := grantPort80(port80Deps{
		goos:       "linux",
		lookPath:   func(string) (string, error) { return "/usr/sbin/setcap", nil },
		executable: func() (string, error) { return "/usr/bin/nextsql-admin", nil },
		realpath:   func(p string) (string, error) { return p, nil },
		run: func(setcapPath, target string) ([]byte, error) {
			return []byte("Failed to set capabilities: Operation not permitted"), errors.New("exit status 1")
		},
	})
	if !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("want forbidden, got %v", err)
	}
	if !strings.Contains(err.Error(), "Operation not permitted") || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("error should surface the setcap output and a sudo hint: %v", err)
	}
}

func TestGrantPort80Success(t *testing.T) {
	msg, err := grantPort80(port80Deps{
		goos:       "linux",
		lookPath:   func(string) (string, error) { return "/usr/sbin/setcap", nil },
		executable: func() (string, error) { return "/usr/bin/nextsql-admin", nil },
		realpath:   func(p string) (string, error) { return p, nil },
		run:        func(setcapPath, target string) ([]byte, error) { return []byte("ok"), nil },
	})
	if err != nil {
		t.Fatalf("grantPort80: %v", err)
	}
	if !strings.Contains(msg, "/usr/bin/nextsql-admin") || !strings.Contains(msg, "127.0.0.1:80") {
		t.Fatalf("success message missing expected content: %q", msg)
	}
}

func TestGrantPort80UsesRealpathWhenResolvable(t *testing.T) {
	msg, err := grantPort80(port80Deps{
		goos:       "linux",
		lookPath:   func(string) (string, error) { return "/usr/sbin/setcap", nil },
		executable: func() (string, error) { return "/usr/bin/nextsql-admin-symlink", nil },
		realpath:   func(string) (string, error) { return "/opt/nextsql/nextsql-admin", nil },
		run: func(setcapPath, target string) ([]byte, error) {
			if target != "/opt/nextsql/nextsql-admin" {
				t.Fatalf("setcap target = %q, want the resolved real path", target)
			}
			return nil, nil
		},
	})
	if err != nil {
		t.Fatalf("grantPort80: %v", err)
	}
	if !strings.Contains(msg, "/opt/nextsql/nextsql-admin") {
		t.Fatalf("success message should report the resolved path: %q", msg)
	}
}
