package cli

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/nerr"
)

func TestParseDotenv(t *testing.T) {
	in := `
# comment
NEXTSQL_DATABASE_USER=app
export NEXTSQL_ADDR=127.0.0.1:7210
NEXTSQL_DATABASE="prod db"
NEXTSQL_EMPTY=
`
	m, err := parseDotenv(strings.NewReader(in))
	if err != nil {
		t.Fatal(err)
	}
	if m["NEXTSQL_DATABASE_USER"] != "app" || m["NEXTSQL_ADDR"] != "127.0.0.1:7210" {
		t.Fatalf("%v", m)
	}
	if m["NEXTSQL_DATABASE"] != "prod db" {
		t.Fatalf("quotes: %v", m)
	}
	if m["NEXTSQL_EMPTY"] != "" {
		t.Fatalf("empty: %q", m["NEXTSQL_EMPTY"])
	}
}

func TestParseDotenvRejectsBareLine(t *testing.T) {
	if _, err := parseDotenv(strings.NewReader("not-a-pair\n")); err == nil {
		t.Fatal("expected error")
	}
}

func TestDiscoverWalkUpAndCwdLocal(t *testing.T) {
	root := t.TempDir()
	child := filepath.Join(root, "proj", "app")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proj", ".env"), []byte("NEXTSQL_DATABASE_USER=from-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "proj", ".env.local"), []byte("NEXTSQL_DATABASE_USER=parent-local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(child, ".env.local"), []byte("NEXTSQL_DATABASE_PASSWORD_FILE=/tmp/pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	base, local := discoverEnvFiles(child)
	if base != filepath.Join(root, "proj", ".env") {
		t.Fatalf("base %q", base)
	}
	if local != filepath.Join(child, ".env.local") {
		t.Fatalf("local %q", local)
	}

	base, local = discoverEnvFiles(filepath.Join(root, "proj"))
	if local != filepath.Join(root, "proj", ".env.local") {
		t.Fatalf("cwd local %q", local)
	}
	_ = base
}

func TestDiscoverStopsAtMaxWalk(t *testing.T) {
	root := t.TempDir()
	dir := root
	for i := 0; i < maxEnvWalk; i++ {
		dir = filepath.Join(dir, "d")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("NEXTSQL_DATABASE_USER=too-far\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	base, _ := discoverEnvFiles(dir)
	if base != "" {
		t.Fatalf("walked past max: %q", base)
	}
}

func TestResolvePriorityAndEmptyEnv(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NEXTSQL_DATABASE_USER=file-user\nNEXTSQL_ADDR=10.0.0.1:1\nNEXTSQL_DATABASE=from-env\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".env.local"), []byte("NEXTSQL_DATABASE_USER=local-user\nNEXTSQL_DATABASE_PASSWORD_FILE=/run/pw\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envDatabaseUser, "")
	t.Setenv(envAddr, "env-addr:9")

	s, err := Resolve(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "local-user" {
		t.Fatalf("user %q", s.User)
	}
	if s.Addr != "env-addr:9" {
		t.Fatalf("addr %q", s.Addr)
	}
	if s.PasswordFile != "/run/pw" || s.Database != "from-env" {
		t.Fatalf("%+v", s)
	}
}

func TestResolveExplicitFlagWinsEmpty(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NEXTSQL_DATABASE_USER=file-user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(envDatabaseUser, "env-user")
	fs := testServerFlags()
	if err := fs.Parse([]string{"--user="}); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "" {
		t.Fatalf("explicit empty lost: %q", s.User)
	}
}

func TestResolveNoEnvSkipsFiles(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NEXTSQL_DATABASE_USER=file-user\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := testServerFlags()
	if err := fs.Parse([]string{"--no-env"}); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "" {
		t.Fatalf("no-env still loaded user %q", s.User)
	}
	if s.Addr != config.DefaultListenAddr {
		t.Fatalf("default addr %q", s.Addr)
	}
}

func TestResolveEnvFileOnly(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte("NEXTSQL_DATABASE_USER=ignored\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(dir, "other.env")
	if err := os.WriteFile(other, []byte("NEXTSQL_DATABASE_USER=only-file\nNEXTSQL_INSECURE=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := testServerFlags()
	if err := fs.Parse([]string{"--env-file", other}); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "only-file" || !s.Insecure {
		t.Fatalf("%+v", s)
	}
}

func TestResolveNextSQLDotenvSurface(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	dotenv := strings.Join([]string{
		"NEXTSQL_ADDR=127.0.0.1:7210",
		"NEXTSQL_DATABASE=app_db",
		"NEXTSQL_REALM_NAME=customer_a",
		"NEXTSQL_INSTANCE_KEY_FILE=/run/keys/instance.key",
		"NEXTSQL_DATABASE_USER=db_user",
		"NEXTSQL_DATABASE_PASS=db_password",
		"NEXTSQL_INSECURE=true",
		"NEXTSQL_TLS_SERVER_NAME=db.internal",
		"NEXTSQL_MIGRATION_DIR=./schema/migrations",
		"NEXTSQL_SERVER_USER=server_admin",
		"NEXTSQL_SERVER_PASS=server_password",
		"NEXTSQL_SERVER_PASSWORD_FILE=/run/server.pw",
		"NEXTSQL_HOSTING_CONFIRM=true",
		"NEXTSQL_HOSTING_MANIFEST_FILE=/etc/nextsql/hosting.yaml",
		"",
	}, "\n")
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(dotenv), 0o600); err != nil {
		t.Fatal(err)
	}

	s, err := Resolve(testServerFlags(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Addr != "127.0.0.1:7210" || s.Database != "app_db" || s.User != "db_user" {
		t.Fatalf("connection settings: addr=%q database=%q user=%q", s.Addr, s.Database, s.User)
	}
	if s.Realm != "customer_a" || s.InstanceKeyFile != "/run/keys/instance.key" || !s.HostingConfirm || s.HostingManifest != "/etc/nextsql/hosting.yaml" {
		t.Fatalf("hosting settings: realm=%q instance_key=%q confirm=%t manifest=%q", s.Realm, s.InstanceKeyFile, s.HostingConfirm, s.HostingManifest)
	}
	if !s.Supplied["realm"] || !s.Supplied["database"] || !s.Supplied["instance-key-file"] || !s.Supplied["confirm"] || !s.Supplied["hosting-manifest"] {
		t.Fatalf("hosting settings were not marked supplied: %+v", s.Supplied)
	}
	if s.Password != "db_password" || !s.Insecure {
		t.Fatal("password or insecure setting was not resolved")
	}
	if s.TLSServerName != "db.internal" || s.MigrationsDir != "./schema/migrations" {
		t.Fatalf("TLS/migration settings: server_name=%q migrations=%q", s.TLSServerName, s.MigrationsDir)
	}
	if s.User != "db_user" || s.Password != "db_password" {
		t.Fatal("server credentials overrode the regular database account")
	}
	if s.ServerUser != "server_admin" || s.ServerPass != "server_password" || s.ServerPassFile != "/run/server.pw" {
		t.Fatalf("server credentials were not resolved independently: %+v", s)
	}
}

func TestRemovedRealmAndTenantEnvironmentNamesAreIgnored(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv("NEXTSQL_REALM", "legacy-realm")
	t.Setenv("NEXTSQL_TENANT", "legacy-tenant")

	s, err := Resolve(testServerFlags(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.Realm != "" || s.Supplied["realm"] {
		t.Fatalf("removed NEXTSQL_REALM was accepted: realm=%q supplied=%v", s.Realm, s.Supplied["realm"])
	}
}

func TestResolveServerCredentialsNeverBecomeClientFallback(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	if err := os.WriteFile(filepath.Join(dir, ".env"), []byte(
		"NEXTSQL_SERVER_USER=server_admin\nNEXTSQL_SERVER_PASS=server_password\n",
	), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(testServerFlags(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "" || s.Password != "" {
		t.Fatalf("server credentials leaked into client login: user=%q password=%t", s.User, s.Password != "")
	}
	if s.ServerUser != "server_admin" || s.ServerPass != "server_password" {
		t.Fatalf("server credentials missing: %+v", s)
	}
}

func TestResolveRejectsAmbiguousLegacyClientEnvironmentNames(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv("NEXTSQL_USER", "legacy-user")
	t.Setenv("NEXTSQL_PASSWORD", "legacy-password")
	t.Setenv("NEXTSQL_PASSWORD_FILE", "/run/legacy.pw")
	s, err := Resolve(testServerFlags(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "" || s.Password != "" || s.PasswordFile != "" {
		t.Fatalf("ambiguous legacy client variables were accepted: %+v", s)
	}
}

func TestResolveEnvFileMissing(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	fs := testServerFlags()
	if err := fs.Parse([]string{"--env-file", filepath.Join(dir, "missing.env")}); err != nil {
		t.Fatal(err)
	}
	if _, err := Resolve(fs, nil); err == nil {
		t.Fatal("expected missing env-file error")
	}
}

func TestResolveFlagBeatsEnv(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv(envDatabaseUser, "env-user")
	t.Setenv(envInsecure, "true")
	fs := testServerFlags()
	if err := fs.Parse([]string{"--user", "flag-user", "--insecure=false"}); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "flag-user" || s.Insecure {
		t.Fatalf("%+v", s)
	}
}

func TestResolveNoEnvWinsOverEnvFile(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	path := filepath.Join(dir, "only.env")
	if err := os.WriteFile(path, []byte("NEXTSQL_DATABASE_USER=from-file\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fs := testServerFlags()
	if err := fs.Parse([]string{"--no-env", "--env-file", path}); err != nil {
		t.Fatal(err)
	}
	s, err := Resolve(fs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "" {
		t.Fatalf("no-env loaded env-file: %q", s.User)
	}
}

func TestResolveInsecureFromEnv(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv(envInsecure, "yes")
	s, err := Resolve(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Insecure {
		t.Fatal("expected insecure")
	}
}

func TestResolveInsecureInvalid(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	chdir(t, dir)
	t.Setenv(envInsecure, "maybe")
	if _, err := Resolve(nil, nil); err == nil {
		t.Fatal("expected invalid insecure")
	}
}

func TestServerConfigPasswordFileWins(t *testing.T) {
	clearClientEnv(t)
	dir := t.TempDir()
	pwPath := filepath.Join(dir, "pw")
	if err := os.WriteFile(pwPath, []byte("file-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	stderr = &buf
	t.Cleanup(func() { stderr = os.Stderr })

	s := Defaults()
	s.User = "app"
	s.Insecure = true
	s.PasswordFile = pwPath
	s.Password = "inline-secret"
	cfg, err := ServerConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "file-secret" {
		t.Fatalf("password %q", cfg.Password)
	}
	if buf.Len() != 0 {
		t.Fatalf("unexpected warning %q", buf.String())
	}
}

func TestServerConfigInlinePasswordWarns(t *testing.T) {
	var buf bytes.Buffer
	stderr = &buf
	t.Cleanup(func() { stderr = os.Stderr })

	s := Defaults()
	s.User = "app"
	s.Insecure = true
	s.Password = "inline-secret"
	cfg, err := ServerConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Password != "inline-secret" {
		t.Fatalf("password %q", cfg.Password)
	}
	if !strings.Contains(buf.String(), "using NEXTSQL_DATABASE_PASS from the environment; prefer NEXTSQL_DATABASE_PASSWORD_FILE") {
		t.Fatalf("warning %q", buf.String())
	}
}

func TestServerConfigRejectsURLAddress(t *testing.T) {
	for _, addr := range []string{
		"nextsql://127.0.0.1:7210",
		"127.0.0.1:7210?key=abc",
		"127.0.0.1:7210?password=secret",
	} {
		s := Defaults()
		s.Addr = addr
		s.User = "app"
		s.Password = "x"
		s.Insecure = true
		_, err := ServerConfig(s)
		if !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("%s: %v", addr, err)
		}
		if err != nil && !strings.Contains(err.Error(), "host:port") {
			t.Fatalf("%s: %v", addr, err)
		}
	}
}

func TestServerConfigRequiresTLSOrInsecure(t *testing.T) {
	s := Defaults()
	s.User = "app"
	s.Password = "x"
	_, err := ServerConfig(s)
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestServerConfigRejectsInsecureRemote(t *testing.T) {
	s := Defaults()
	s.Addr = "db.example.com:7210"
	s.User = "app"
	s.Password = "x"
	s.Insecure = true
	_, err := ServerConfig(s)
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestServerConfigRequiresMTLSClientKeyPair(t *testing.T) {
	s := Defaults()
	s.Addr = "127.0.0.1:7210"
	s.User = "app"
	s.Password = "secret"
	s.Insecure = true
	s.TLSClientCert = "/tmp/client.crt"
	if _, err := ServerConfig(s); err == nil || !strings.Contains(err.Error(), "must be set together") {
		t.Fatalf("err=%v", err)
	}
}

func TestServerConfigIgnoresKeyFileFromEnv(t *testing.T) {
	s := Defaults()
	s.User = "app"
	s.Password = "x"
	s.Insecure = true
	s.KeyFile = "/etc/nextsql/root.key"
	s.DataDir = "/var/lib/nextsql"
	if _, err := ServerConfig(s); err != nil {
		t.Fatal(err)
	}
}

func TestServerConfigRejectsLocalFlags(t *testing.T) {
	s := Defaults()
	s.User = "app"
	s.Password = "x"
	s.Insecure = true
	s.Explicit["data-dir"] = true
	s.DataDir = "/var/lib/nextsql"
	err := CheckServerMode(s)
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "status --local") {
		t.Fatalf("%v", err)
	}
}

func TestResolveParentLocalNotInherited(t *testing.T) {
	clearClientEnv(t)
	root := t.TempDir()
	child := filepath.Join(root, "app")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env"), []byte("NEXTSQL_DATABASE_USER=walked\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".env.local"), []byte("NEXTSQL_DATABASE_USER=parent-local\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	chdir(t, child)
	s, err := Resolve(nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if s.User != "walked" {
		t.Fatalf("inherited parent .env.local: %q", s.User)
	}
}

func TestCode(t *testing.T) {
	tests := []struct {
		err  error
		want int
	}{
		{nil, ExitOK},
		{nerr.New(nerr.InvalidArgument, "x", "bad flag"), ExitUsage},
		{errors.New("flag: help requested"), ExitUsage},
		{nerr.New(nerr.IO, "nextsql.Open", "dial"), ExitConnect},
		{nerr.New(nerr.Unauthorized, "auth", "denied"), ExitConnect},
		{nerr.New(nerr.Protocol, "proto", "bad frame"), ExitConnect},
		{nerr.New(nerr.Unavailable, "ha", "no leader"), ExitConnect},
		{ErrDirty, ExitDirty},
		{nerr.Wrap(nerr.Conflict, "migrate", "database is dirty", ErrDirty), ExitDirty},
		{nerr.New(nerr.Conflict, "executor", "pk"), ExitSQL},
		{ErrChecksum, ExitChecksum},
		{nerr.Wrap(nerr.InvalidFormat, "migrate", "migration checksum mismatch", ErrChecksum), ExitChecksum},
		{nerr.New(nerr.InvalidFormat, "status", "damaged headers"), ExitSQL},
		{nerr.New(nerr.Syntax, "sql", "bad"), ExitSQL},
		{nerr.New(nerr.Forbidden, "acl", "denied"), ExitSQL},
		{nerr.New(nerr.AlreadyExists, "btree", "pk"), ExitSQL},
		{ErrValidation, ExitValidation},
		{ErrApply, ExitSQL},
		{nerr.Wrap(nerr.InvalidArgument, "migrate", "apply", ErrApply), ExitSQL},
		{LocalMissing("nextsql init", "--data-dir and --key-file are required"), ExitLocal},
		{nerr.Wrap(nerr.InvalidArgument, "cli", "missing", ErrLocalMissing), ExitLocal},
	}
	for _, tt := range tests {
		if got := Code(tt.err); got != tt.want {
			t.Errorf("Code(%v)=%d want %d", tt.err, got, tt.want)
		}
	}
}

func TestLocalMissingKeepsInvalidArgument(t *testing.T) {
	err := LocalMissing("nextsql init", "--data-dir and --key-file are required")
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if !strings.Contains(err.Error(), "--data-dir and --key-file are required") {
		t.Fatalf("%v", err)
	}
}

func testServerFlags() *flag.FlagSet {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	fs.String("addr", config.DefaultListenAddr, "")
	fs.String("user", "", "")
	fs.String("password-file", "", "")
	fs.String("database", "", "")
	fs.String("tls-ca", "", "")
	fs.String("tls-server-name", "", "")
	fs.String("tls-client-cert", "", "")
	fs.String("tls-client-key", "", "")
	fs.Bool("insecure", false, "")
	fs.String("dir", defaultMigrationsDir, "")
	fs.String("data-dir", "", "")
	fs.String("key-file", "", "")
	fs.String("instance-key-file", "", "")
	fs.String("realm", "", "")
	fs.Int("buffer-pages", config.DefaultBufferPages, "")
	fs.Bool("confirm", false, "")
	fs.String("env-file", "", "")
	fs.Bool("no-env", false, "")
	return fs
}

func chdir(t *testing.T, dir string) {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(wd); err != nil {
			t.Errorf("chdir: %v", err)
		}
	})
}

func clearClientEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		envAddr, envDatabaseUser, envDatabasePassFile, envDatabasePass, envServerUser, envServerPass, envServerPassFile, envDatabase,
		envTLSCA, envTLSServerName, envTLSClientCert, envTLSClientKey, envInsecure, envMigrationDir,
		envDataDir, envKeyFile, envInstanceKey, envRealmName, envBufferPages, envHostingConfirm,
	} {
		t.Setenv(k, "")
		_ = os.Unsetenv(k)
	}
}
