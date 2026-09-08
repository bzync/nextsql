package dockerentry

import (
	"bytes"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeConn struct{}

func (fakeConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (fakeConn) Write(b []byte) (int, error)      { return len(b), nil }
func (fakeConn) Close() error                     { return nil }
func (fakeConn) LocalAddr() net.Addr              { return nil }
func (fakeConn) RemoteAddr() net.Addr             { return nil }
func (fakeConn) SetDeadline(time.Time) error      { return nil }
func (fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (fakeConn) SetWriteDeadline(time.Time) error { return nil }

type recorder struct {
	Runtime
	runs [][]string
	exec []string
	env  []string
	log  bytes.Buffer
}

func newRecorder() *recorder {
	r := &recorder{
		env: []string{"NEXTSQL_SERVER_PASS=secret"},
	}
	r.Runtime = Runtime{
		NextSQL:  "/usr/local/bin/nextsql",
		NextSQLd: "/usr/local/bin/nextsqld",
		Stderr:   &r.log,
		Stdout:   io.Discard,
		Sleep:    func(time.Duration) {},
		Dial: func(string, string, time.Duration) (net.Conn, error) {
			return nil, errors.New("refused")
		},
		Run: func(name string, args []string) error {
			r.runs = append(r.runs, append([]string{name}, args...))
			return nil
		},
		Exec: func(argv0 string, argv, envv []string) error {
			r.exec = append([]string{argv0}, argv[1:]...)
			r.env = envv
			return nil
		},
		Environ: func() []string { return r.env },
	}
	return r
}

func TestLoadEnvDefaults(t *testing.T) {
	env := LoadEnv(func(string) string { return "" })
	if env.DataDir != defaultDataDir || env.KeyFile != defaultKeyFile || env.Listen != defaultListen {
		t.Fatalf("defaults: %+v", env)
	}
	if env.PasswordFile != defaultPasswordFile {
		t.Fatalf("password file default: %q", env.PasswordFile)
	}
	if env.ConfigFile != filepath.Join(defaultDataDir, confFileName) {
		t.Fatalf("config file default: %q", env.ConfigFile)
	}
	if env.Profile != "" || env.Preset != "" {
		t.Fatalf("profile/preset default: %+v", env)
	}
}

func TestLoadEnvOverrides(t *testing.T) {
	vals := map[string]string{
		"NEXTSQL_DATA_DIR": "/data",
		"NEXTSQL_LISTEN":   "127.0.0.1:9",
		"NEXTSQL_NODE_ID":  "node-a",
		"NEXTSQL_PROFILE":  "production",
		"NEXTSQL_PRESET":   "high-performance",
	}
	env := LoadEnv(func(k string) string { return vals[k] })
	if env.DataDir != "/data" || env.Listen != "127.0.0.1:9" || env.NodeID != "node-a" {
		t.Fatalf("overrides: %+v", env)
	}
	if env.Profile != "production" || env.Preset != "high-performance" {
		t.Fatalf("profile/preset overrides: %+v", env)
	}
	if env.ConfigFile != "/data/nextsql.conf" {
		t.Fatalf("config file follows data dir: %q", env.ConfigFile)
	}
}

func TestServerArgsTLSValidation(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		env     Env
		want    int
		contain string
	}{
		{
			name:    "cert without key",
			env:     Env{TLSCert: "/c"},
			want:    exitUsage,
			contain: "must be set together",
		},
		{
			name:    "key without cert",
			env:     Env{TLSKey: "/k"},
			want:    exitUsage,
			contain: "must be set together",
		},
		{
			name:    "client CA without TLS",
			env:     Env{TLSClientCA: "/ca"},
			want:    exitTLS,
			contain: "NEXTSQL_TLS_CLIENT_CA requires",
		},
		{
			name:    "CRL without CA",
			env:     Env{TLSCert: "/c", TLSKey: "/k", TLSClientCRL: "/crl"},
			want:    exitTLS,
			contain: "NEXTSQL_TLS_CLIENT_CRL requires",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.env.serverArgs()
			var e *Error
			if !errors.As(err, &e) || e.Code != tc.want || !strings.Contains(e.Message, tc.contain) {
				t.Fatalf("got %#v", err)
			}
		})
	}
}

func TestServerArgsFlagOrder(t *testing.T) {
	env := Env{
		DataDir:       "/data",
		KeyFile:       "/key",
		Listen:        "0.0.0.0:7210",
		AuthFile:      "/auth",
		TLSCert:       "/c",
		TLSKey:        "/k",
		TLSClientCA:   "/ca",
		TLSClientCRL:  "/crl",
		BufferPages:   "128",
		NodeID:        "node-a",
		RaftBind:      "node-a:7300",
		RaftJoin:      "node-a=node-a:7300",
		RaftBootstrap: "1",
	}
	args, err := env.serverArgs()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"--data-dir", "/data",
		"--key-file", "/key",
		"--listen", "0.0.0.0:7210",
		"--auth-file", "/auth",
		"--tls-cert", "/c", "--tls-key", "/k",
		"--tls-client-ca", "/ca",
		"--tls-client-crl", "/crl",
		"--buffer-pages", "128",
		"--node-id", "node-a",
		"--raft-bind", "node-a:7300",
		"--raft-join", "node-a=node-a:7300",
		"--raft-bootstrap",
	}
	if strings.Join(args, " ") != strings.Join(want, " ") {
		t.Fatalf("args:\n got %q\nwant %q", args, want)
	}
}

func TestJoinPeersSkipsEmpty(t *testing.T) {
	env := Env{JoinWait: " node-b:7300, ,node-c:7300,"}
	got := env.joinPeers()
	if len(got) != 2 || got[0] != "node-b:7300" || got[1] != "node-c:7300" {
		t.Fatalf("got %q", got)
	}
}

func TestFirstStartRequiresUser(t *testing.T) {
	dir := t.TempDir()
	rec := newRecorder()
	code := run(Env{DataDir: dir, KeyFile: "/key", Listen: "127.0.0.1:1"}, rec.Runtime)
	if code != exitUsage || !strings.Contains(rec.log.String(), "NEXTSQL_SERVER_USER") {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
}

func TestFirstStartRequiresPassword(t *testing.T) {
	dir := t.TempDir()
	rec := newRecorder()
	code := run(Env{DataDir: dir, KeyFile: "/key", Listen: "127.0.0.1:1", ServerUser: "app"}, rec.Runtime)
	if code != exitUsage || !strings.Contains(rec.log.String(), "NEXTSQL_SERVER_PASSWORD_FILE") {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
}

func TestSetupWithPasswordFileThenExec(t *testing.T) {
	dir := t.TempDir()
	pw := filepath.Join(dir, "pw")
	if err := os.WriteFile(pw, []byte("secret\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	env := Env{
		DataDir:      dir,
		KeyFile:      "/key",
		Listen:       "0.0.0.0:7210",
		ServerUser:   "app",
		PasswordFile: pw,
		Profile:      "production",
		Preset:       "conservative",
		TLSCert:      "/c",
		TLSKey:       "/k",
		ConfigFile:   filepath.Join(dir, confFileName),
		NodeID:       "solo",
	}
	code := run(env, rec.Runtime)
	if code != 0 {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
	if len(rec.runs) != 1 {
		t.Fatalf("runs: %q", rec.runs)
	}
	got := strings.Join(rec.runs[0], " ")
	want := "/usr/local/bin/nextsql setup --data-dir " + dir + " --key-file /key --user app --password-file " + pw +
		" --config-out " + filepath.Join(dir, confFileName) + " --listen 0.0.0.0:7210" +
		" --profile production --preset conservative --tls-cert /c --tls-key /k"
	if got != want {
		t.Fatalf("setup args:\n got %q\nwant %q", got, want)
	}
	if rec.exec[0] != "/usr/local/bin/nextsqld" {
		t.Fatalf("exec %q", rec.exec)
	}
}

func TestSetupWithServerPassWritesTempPasswordFile(t *testing.T) {
	dir := t.TempDir()
	rec := newRecorder()
	var seenPath string
	rec.Run = func(name string, args []string) error {
		rec.runs = append(rec.runs, append([]string{name}, args...))
		for i, a := range args {
			if a == "--password-file" && i+1 < len(args) {
				seenPath = args[i+1]
			}
		}
		// The password file must still exist (and hold the password) while
		// `nextsql setup` runs.
		b, err := os.ReadFile(seenPath)
		if err != nil {
			t.Fatalf("password file unreadable during setup: %v", err)
		}
		if string(b) != "secret\n" {
			t.Fatalf("password file content %q", b)
		}
		return nil
	}
	env := Env{DataDir: dir, KeyFile: "/key", Listen: "0.0.0.0:7210", ServerUser: "app", ServerPass: "secret", PasswordFile: filepath.Join(dir, "missing")}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
	if seenPath == "" {
		t.Fatal("no --password-file passed to setup")
	}
	if _, err := os.Stat(seenPath); !os.IsNotExist(err) {
		t.Fatalf("temp password file not cleaned up: %v", err)
	}
}

func TestSetupConfigPassedToServerWhenPresent(t *testing.T) {
	dir := t.TempDir()
	conf := filepath.Join(dir, confFileName)
	rec := newRecorder()
	rec.Run = func(name string, args []string) error {
		rec.runs = append(rec.runs, append([]string{name}, args...))
		// emulate `nextsql setup` writing the generated config
		return os.WriteFile(conf, []byte("deployment_profile=production\n"), 0o640)
	}
	env := Env{DataDir: dir, KeyFile: "/key", Listen: "0.0.0.0:7210", ServerUser: "app", ServerPass: "secret", PasswordFile: filepath.Join(dir, "missing")}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
	if len(rec.exec) < 3 || rec.exec[1] != "--config" || rec.exec[2] != conf {
		t.Fatalf("expected --config %s first in server args, got %q", conf, rec.exec)
	}
}

func TestExistingConfigPassedToServerWithoutReinit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, dbFileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	conf := filepath.Join(dir, confFileName)
	if err := os.WriteFile(conf, []byte("deployment_profile=production\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	env := Env{DataDir: dir, KeyFile: "/key", Listen: "0.0.0.0:7210"}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
	if len(rec.runs) != 0 {
		t.Fatalf("unexpected setup on existing db: %q", rec.runs)
	}
	if len(rec.exec) < 3 || rec.exec[1] != "--config" || rec.exec[2] != conf {
		t.Fatalf("expected --config %s first, got %q", conf, rec.exec)
	}
}

func TestExistingDBSkipsInit(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, dbFileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	env := Env{DataDir: dir, KeyFile: "/key", Listen: "0.0.0.0:7210"}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("code=%d", code)
	}
	if len(rec.runs) != 0 {
		t.Fatalf("unexpected runs %q", rec.runs)
	}
}

func TestSeedToBackupOnce(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, dbFileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	seed := t.TempDir()
	rec := newRecorder()
	env := Env{DataDir: dir, KeyFile: "/key", Listen: "0.0.0.0:7210", SeedTo: seed}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
	if len(rec.runs) != 1 || rec.runs[0][1] != "backup" {
		t.Fatalf("runs %q", rec.runs)
	}
	if !strings.Contains(rec.log.String(), "producing seed backup") {
		t.Fatalf("log %q", rec.log.String())
	}

	if err := os.WriteFile(filepath.Join(seed, verifiedMarker), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec2 := newRecorder()
	if code := run(env, rec2.Runtime); code != 0 {
		t.Fatalf("second code=%d", code)
	}
	if len(rec2.runs) != 0 {
		t.Fatalf("backup repeated: %q", rec2.runs)
	}
}

func TestRestoreFromSeed(t *testing.T) {
	data := t.TempDir()
	seed := t.TempDir()
	if err := os.WriteFile(filepath.Join(seed, verifiedMarker), []byte("ok"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(seed, "payload"), []byte("p"), 0o644); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	rec.Run = func(name string, args []string) error {
		rec.runs = append(rec.runs, append([]string{name}, args...))
		// emulate `nextsql restore --data-dir <new>` creating the dir
		var dest string
		for i, a := range args {
			if a == "--data-dir" && i+1 < len(args) {
				dest = args[i+1]
			}
		}
		if dest == "" {
			t.Fatalf("restore missing --data-dir: %q", args)
		}
		if err := os.MkdirAll(dest, 0o700); err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dest, dbFileName), []byte("restored"), 0o600)
	}
	env := Env{DataDir: data, KeyFile: "/key", Listen: "0.0.0.0:7210", SeedFrom: seed}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("code=%d log=%q", code, rec.log.String())
	}
	if rec.runs[0][1] != "restore" {
		t.Fatalf("runs %q", rec.runs)
	}
	got, err := os.ReadFile(filepath.Join(data, dbFileName))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "restored" {
		t.Fatalf("relocated db %q", got)
	}
}

func TestWaitForFileTimeout(t *testing.T) {
	rec := newRecorder()
	err := waitForFile(filepath.Join(t.TempDir(), "nope"), "seed backup", rec.Runtime)
	var e *Error
	if !errors.As(err, &e) || e.Code != exitFail || !strings.Contains(e.Message, "timed out waiting for seed backup") {
		t.Fatalf("got %#v", err)
	}
}

func TestWaitForTCPSuccessAndTimeout(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	rec := newRecorder()
	rec.Dial = net.DialTimeout
	if err := waitForTCP(ln.Addr().String(), rec.Runtime); err != nil {
		t.Fatal(err)
	}

	rec.Dial = func(string, string, time.Duration) (net.Conn, error) {
		return nil, errors.New("refused")
	}
	err = waitForTCP("127.0.0.1:1", rec.Runtime)
	var e *Error
	if !errors.As(err, &e) || !strings.Contains(e.Message, "timed out waiting for 127.0.0.1:1") {
		t.Fatalf("got %#v", err)
	}
}

func TestJoinWaitOnlyWhenBootstrap(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, dbFileName), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := newRecorder()
	dialed := 0
	rec.Dial = func(string, string, time.Duration) (net.Conn, error) {
		dialed++
		return nil, errors.New("refused")
	}
	env := Env{DataDir: dir, KeyFile: "/key", Listen: "0.0.0.0:7210", JoinWait: "127.0.0.1:1"}
	if code := run(env, rec.Runtime); code != 0 {
		t.Fatalf("non-bootstrap should skip wait: code=%d", code)
	}
	if dialed != 0 {
		t.Fatalf("dialed without bootstrap: %d", dialed)
	}

	rec2 := newRecorder()
	rec2.Dial = func(string, string, time.Duration) (net.Conn, error) {
		return fakeConn{}, nil
	}
	env.RaftBootstrap = "1"
	env.JoinWait = "node-b:7300,node-c:7300"
	if code := run(env, rec2.Runtime); code != 0 {
		t.Fatalf("bootstrap wait: code=%d log=%q", code, rec2.log.String())
	}
	if !strings.Contains(rec2.log.String(), "waiting for raft peer node-b:7300") {
		t.Fatalf("log %q", rec2.log.String())
	}
}

func TestCopyAndRelocate(t *testing.T) {
	src := t.TempDir()
	sub := filepath.Join(src, "nested")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "f"), []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	if err := copyTree(src, dst); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(dst, "nested", "f"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "hello" {
		t.Fatalf("copied %q", got)
	}

	from := t.TempDir()
	to := t.TempDir()
	if err := os.WriteFile(filepath.Join(from, "moved"), []byte("m"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := relocateDirContents(from, to); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(to, "moved")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(from, "moved")); !os.IsNotExist(err) {
		t.Fatalf("source still present: %v", err)
	}
}
