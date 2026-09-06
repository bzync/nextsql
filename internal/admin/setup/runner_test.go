package setup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFakeNextSQL writes an executable shell script standing in for the
// `nextsql` binary and returns its path. The script echoes back the
// password-file's contents (if --password-file is given) inside its JSON,
// so tests can assert the runner wrote and cleaned up that temp file
// correctly without ever putting the password on argv.
func writeFakeNextSQL(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunnerRunSuccess(t *testing.T) {
	bin := writeFakeNextSQL(t, `
pwfile=""
while [ $# -gt 0 ]; do
  case "$1" in
    --password-file) pwfile="$2"; shift ;;
  esac
  shift
done
pw=""
[ -n "$pwfile" ] && pw="$(cat "$pwfile")"
printf '{"ok":true,"password_seen":"%s"}' "$pw"
`)
	rn := newRunner(bin, 5*time.Second)
	p := Params{DataDir: "/d", KeyFile: "/k", AdminUser: "app", AdminPassword: "hunter2-secret"}

	res := rn.run(context.Background(), p, true)
	if res.Error != "" {
		t.Fatalf("unexpected error: %s", res.Error)
	}
	if !res.OK {
		t.Fatalf("expected OK result, got %+v", res)
	}
	var decoded struct {
		OK           bool   `json:"ok"`
		PasswordSeen string `json:"password_seen"`
	}
	if err := json.Unmarshal(res.Result, &decoded); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if decoded.PasswordSeen != "hunter2-secret" {
		t.Errorf("password file content = %q, want the admin password", decoded.PasswordSeen)
	}
}

func TestRunnerRunFailurePropagatesStderr(t *testing.T) {
	bin := writeFakeNextSQL(t, `echo "boom: something went wrong" 1>&2; exit 1`)
	rn := newRunner(bin, 5*time.Second)
	res := rn.run(context.Background(), Params{DataDir: "/d", KeyFile: "/k"}, true)
	if res.OK {
		t.Fatalf("expected failure, got OK result")
	}
	if !strings.Contains(res.Error, "boom: something went wrong") {
		t.Errorf("error = %q, want stderr content", res.Error)
	}
}

func TestRunnerRunInvalidJSON(t *testing.T) {
	bin := writeFakeNextSQL(t, `printf 'not json'`)
	rn := newRunner(bin, 5*time.Second)
	res := rn.run(context.Background(), Params{DataDir: "/d", KeyFile: "/k"}, true)
	if res.OK {
		t.Fatalf("expected failure for invalid JSON, got OK")
	}
}

func TestRunnerNoPasswordFileWithoutAdminUser(t *testing.T) {
	bin := writeFakeNextSQL(t, `
for a in "$@"; do
  if [ "$a" = "--password-file" ]; then echo "unexpected --password-file" 1>&2; exit 1; fi
done
printf '{"ok":true}'
`)
	rn := newRunner(bin, 5*time.Second)
	res := rn.run(context.Background(), Params{DataDir: "/d", KeyFile: "/k"}, false)
	if !res.OK {
		t.Fatalf("expected OK, got %+v", res)
	}
}

func TestResolveNextSQLBinOverride(t *testing.T) {
	bin := writeFakeNextSQL(t, `exit 0`)
	got, err := ResolveNextSQLBin(bin)
	if err != nil {
		t.Fatal(err)
	}
	if got != bin {
		t.Errorf("got %q, want %q", got, bin)
	}
}

func TestResolveNextSQLBinOverrideMissing(t *testing.T) {
	if _, err := ResolveNextSQLBin(filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected an error for a missing override path")
	}
}

func TestResolveNextSQLBinNotFound(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := ResolveNextSQLBin(""); err == nil {
		t.Fatal("expected an error when nextsql cannot be found anywhere")
	}
}
