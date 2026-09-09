// Package upgrade replays retained release fixtures against the binaries built
// from the current tree.
//
// A fixture (testdata/<label>-clean.tar.gz, testdata/<label>-dirty.tar.gz) is a
// real data directory written by a *released* build: pages, WAL, UNDO, the
// keystore, and the auth/ACL sidecars, plus the query results and the
// compatibility catalog that release itself reported. Nothing here is
// synthesised from current code, which is the point — a format, catalog, or
// recovery change that stranded data written by a shipped binary cannot be
// caught by a test that also writes the fixture with current code.
//
// Each fixture is driven through the real CLI and server, not the in-process
// engine, so the protocol, authentication, and RBAC layers an operator crosses
// on an upgrade are covered too. Cut a new fixture per release with
// scripts/make-upgrade-fixture.sh and commit the archives.
package upgrade

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// fixtureUser is the bootstrap user every fixture is created with, and
// reporterUser/reporterPassword are the least-privilege principal
// testdata/fixture.sql creates. The password unlocks nothing but the archives
// it ships in.
const (
	fixtureUser        = "app"
	reporterUser       = "fixture_reporter"
	reporterPassword   = "fixture-only-password-1"
	serverStartTimeout = 60 * time.Second
)

// bins are the binaries built from the current tree, shared by every test.
var bins struct {
	nextsql  string
	nextsqld string
}

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "nextsql-upgrade-bin")
	if err != nil {
		fmt.Fprintln(os.Stderr, "upgrade:", err)
		os.Exit(1)
	}
	bins.nextsql = filepath.Join(dir, "nextsql")
	bins.nextsqld = filepath.Join(dir, "nextsqld")
	for _, b := range []struct{ out, pkg string }{
		{bins.nextsql, "./cmd/nextsql"},
		{bins.nextsqld, "./cmd/nextsqld"},
	} {
		cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
		cmd.Dir = repoRoot()
		if out, err := cmd.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "upgrade: build %s: %v\n%s\n", b.pkg, err, out)
			os.RemoveAll(dir)
			os.Exit(1)
		}
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func repoRoot() string {
	// tests/upgrade -> repository root.
	wd, err := os.Getwd()
	if err != nil {
		panic(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// manifest is the fixture descriptor written by scripts/make-upgrade-fixture.sh.
type manifest struct {
	Label            string `json:"label"`
	Kind             string `json:"kind"`
	GeneratorVersion string `json:"generator_version"`
	Created          string `json:"created"`
	DataDir          string `json:"data_dir"`
	KeyFile          string `json:"key_file"`
	PasswordFile     string `json:"password_file"`
	User             string `json:"user"`
}

// fixture is one extracted archive.
type fixture struct {
	archive  string
	root     string
	man      manifest
	expected []recordedQuery
	// generated is the compatibility catalog the *generating* release
	// reported: family -> [min, max] readable window.
	generated map[string][2]uint16
}

type recordedQuery struct {
	SQL    string
	Result string
}

// dataDir resolves the manifest's data directory against the extracted root.
// A restored copy is addressed absolutely, so an absolute path wins.
func (f *fixture) dataDir() string {
	if filepath.IsAbs(f.man.DataDir) {
		return f.man.DataDir
	}
	return filepath.Join(f.root, f.man.DataDir)
}

func (f *fixture) keyFile() string  { return filepath.Join(f.root, f.man.KeyFile) }
func (f *fixture) password() string { return filepath.Join(f.root, f.man.PasswordFile) }

func TestRetainedReleaseFixtures(t *testing.T) {
	archives, err := filepath.Glob(filepath.Join("testdata", "*.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(archives)
	if len(archives) == 0 {
		// A release that retains no fixture is exactly the gap this package
		// exists to close, so an empty testdata directory is a failure, not a
		// skip.
		t.Fatal("no retained release fixtures in testdata; cut one with scripts/make-upgrade-fixture.sh")
	}
	for _, archive := range archives {
		t.Run(strings.TrimSuffix(filepath.Base(archive), ".tar.gz"), func(t *testing.T) {
			f := extractFixture(t, archive)
			runFixture(t, f)
		})
	}
}

// runFixture drives one retained fixture through the current binaries.
func runFixture(t *testing.T, f *fixture) {
	t.Helper()

	// 1. The current binary's own preflight must accept the old directory,
	//    and every on-disk version it finds must be inside the window this
	//    binary declares it can read.
	before := diagnose(t, f.dataDir())
	if before.status != "ok" {
		t.Fatalf("diagnose status = %q, want ok\n%s", before.status, before.raw)
	}
	if len(before.onDisk) == 0 {
		t.Fatalf("diagnose reported no on-disk families\n%s", before.raw)
	}
	for family, ver := range before.onDisk {
		win, ok := before.catalog[family]
		if !ok {
			t.Fatalf("on-disk family %q is not in this binary's compatibility catalog", family)
		}
		if ver < win[0] || ver > win[1] {
			t.Fatalf("on-disk %s v%d outside this binary's window [%d,%d]", family, ver, win[0], win[1])
		}
	}

	// 2. Replay every result the generating release recorded. A dirty fixture
	//    was SIGKILLed with acknowledged commits still in the WAL, so this is
	//    also an assertion that the current recovery pass replays an older
	//    release's redo in full.
	srv := startServer(t, f)
	for _, q := range f.expected {
		got, err := query(t, srv.addr, f.password(), fixtureUser, q.SQL)
		if err != nil {
			t.Fatalf("recorded query failed after upgrade: %s\n%v", q.SQL, err)
		}
		if got != q.Result {
			t.Fatalf("recorded query changed across the upgrade: %s\n  %s recorded: %s\n  current:   %s",
				q.SQL, f.man.Label, q.Result, got)
		}
	}

	// 3. The auth and ACL sidecars must survive with their boundary intact:
	//    the fixture's least-privilege principal keeps exactly the grants the
	//    old release wrote — an upgrade that widened them would be a silent
	//    privilege escalation.
	reporterPW := filepath.Join(t.TempDir(), "reporter.pw")
	writeFile(t, reporterPW, reporterPassword+"\n")
	if _, err := query(t, srv.addr, reporterPW, reporterUser, "SELECT id FROM customers ORDER BY id"); err != nil {
		t.Fatalf("granted read denied after upgrade: %v", err)
	}
	if _, err := query(t, srv.addr, reporterPW, reporterUser, "SELECT id FROM orders"); err == nil {
		t.Fatal("ungranted read allowed after upgrade: privileges widened")
	} else if !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("ungranted read failed for the wrong reason: %v", err)
	}

	// 4. The upgraded database must still be writable, including through the
	//    structures the old release created (clustered heap, FK, secondary
	//    index, JSON path index, full-text index).
	for _, stmt := range []string{
		`INSERT INTO customers (id, name, region, joined) VALUES (900, 'post-upgrade', 'emea', '2026-09-08T00:00:00Z')`,
		`INSERT INTO orders (id, customer_id, total, note, metadata) VALUES (900, 900, 3.5, 'written after the upgrade', '{"category":"books","qty":9}')`,
		`UPDATE customers SET region = 'apac' WHERE id = 900`,
		`CREATE TABLE post_upgrade (id INT64 PRIMARY KEY, note STRING)`,
		`INSERT INTO post_upgrade (id, note) VALUES (1, 'new table on an old database')`,
	} {
		if _, err := query(t, srv.addr, f.password(), fixtureUser, stmt); err != nil {
			t.Fatalf("write against the upgraded database failed: %s\n%v", stmt, err)
		}
	}
	assertQuery(t, srv, f, `SELECT id, region FROM customers WHERE id = 900`,
		`{"columns":["id","region"],"rows":[["900","apac"]],"affected":0}`)
	assertQuery(t, srv, f, `SELECT id FROM orders SEARCH note FOR 'upgrade' ORDER BY id`,
		`{"columns":["id"],"rows":[["900"]],"affected":0}`)
	assertQuery(t, srv, f, `SELECT id, metadata.qty FROM orders WHERE metadata.category = 'books' ORDER BY id`,
		`{"columns":["id","?"],"rows":[["10","2"],["12","1"],["900","9"]],"affected":0}`)

	// A cascading delete has to reach an index the old release built.
	if _, err := query(t, srv.addr, f.password(), fixtureUser, `DELETE FROM customers WHERE id = 900`); err != nil {
		t.Fatalf("cascading delete on an upgraded database failed: %v", err)
	}
	assertQuery(t, srv, f, `SELECT COUNT(*) FROM orders WHERE id = 900`,
		`{"columns":["count"],"rows":[["0"]],"affected":0}`)

	srv.stop(t)

	// 5. Restart under the current binary: the writes above must be durable,
	//    and the fixture's own rows unchanged.
	srv2 := startServer(t, f)
	assertQuery(t, srv2, f, `SELECT id, note FROM post_upgrade ORDER BY id`,
		`{"columns":["id","note"],"rows":[["1","new table on an old database"]],"affected":0}`)
	for _, q := range f.expected {
		got, err := query(t, srv2.addr, f.password(), fixtureUser, q.SQL)
		if err != nil {
			t.Fatalf("recorded query failed after restart: %s\n%v", q.SQL, err)
		}
		if got != q.Result {
			t.Fatalf("recorded query changed after a post-upgrade restart: %s\n  want: %s\n  got:  %s", q.SQL, q.Result, got)
		}
	}
	srv2.stop(t)

	// 6. Rollback preservation. Writing with the current binary must not push
	//    any on-disk family past what the generating release could read, or
	//    the operator's way back is gone. Checked against the catalog that
	//    release printed into the archive, not against an assumption.
	after := diagnose(t, f.dataDir())
	if after.status != "ok" {
		t.Fatalf("diagnose after writes: status = %q, want ok\n%s", after.status, after.raw)
	}
	if len(f.generated) == 0 {
		t.Fatalf("fixture %s recorded no compatibility catalog", f.man.Label)
	}
	for family, ver := range after.onDisk {
		win, ok := f.generated[family]
		if !ok {
			// A family the old release did not *print* is not automatically a
			// family it could not read: a structure can predate its own
			// catalog entry. Only an explicitly recorded, source-verified
			// window rescues that case; anything else is a genuinely new
			// format and still fails.
			win, ok = preCatalogWindow(family, f.man.Label)
			if !ok {
				t.Fatalf("this binary wrote family %q, which %s did not know: rollback to %s would fail closed",
					family, f.man.Label, f.man.Label)
			}
		}
		if ver < win[0] || ver > win[1] {
			t.Fatalf("this binary left %s at v%d, outside %s's readable window [%d,%d]: rollback would fail closed",
				family, ver, f.man.Label, win[0], win[1])
		}
	}

	// 6b. Independently of any window, replaying a shipped fixture must not
	//     move an on-disk version at all. The windows above would still pass
	//     an in-window bump; this catches a format that silently advances
	//     itself just by being opened and written to.
	for family, beforeVer := range before.onDisk {
		afterVer, ok := after.onDisk[family]
		if !ok {
			t.Fatalf("family %q disappeared from the on-disk report after writes", family)
		}
		if afterVer != beforeVer {
			t.Fatalf("opening and writing moved %s from v%d to v%d; a shipped fixture's on-disk versions must not change on their own",
				family, beforeVer, afterVer)
		}
	}

	// 7. Backup and restore of the upgraded database, with the current binary.
	//    An operator's rollback plan is a restore, so the old directory has to
	//    survive the whole round trip.
	work := t.TempDir()
	backupDir := filepath.Join(work, "backup")
	run(t, bins.nextsql, "backup", "--data-dir", f.dataDir(), "--key-file", f.keyFile(), "--out", backupDir)
	restored := filepath.Join(work, "restored")
	run(t, bins.nextsql, "restore", "--data-dir", restored, "--from", backupDir, "--key-file", f.keyFile())

	rf := &fixture{root: f.root, man: f.man, expected: f.expected, generated: f.generated}
	rf.man.DataDir = restored
	srv3 := startServer(t, rf)
	for _, q := range f.expected {
		got, err := query(t, srv3.addr, f.password(), fixtureUser, q.SQL)
		if err != nil {
			t.Fatalf("recorded query failed on the restored database: %s\n%v", q.SQL, err)
		}
		if got != q.Result {
			t.Fatalf("recorded query changed on the restored database: %s\n  want: %s\n  got:  %s", q.SQL, q.Result, got)
		}
	}
	assertQuery(t, srv3, f, `SELECT id, note FROM post_upgrade ORDER BY id`,
		`{"columns":["id","note"],"rows":[["1","new table on an old database"]],"affected":0}`)
	srv3.stop(t)

	// 8. Real rollback, when the released binaries are available. The check
	//    above is evidence recorded by the old release; this is the old
	//    release itself.
	rollBackWithReleasedBinary(t, f)
}

// rollBackWithReleasedBinary runs the generating release's own binaries against
// the directory the current binary just wrote. It needs those binaries, so it
// is opt-in:
//
//	NEXTSQL_UPGRADE_OLD_BINDIR=/path/to/<label>/bin go test ./tests/upgrade
//
// The directory must hold the nextsql and nextsqld built from the fixture's
// label; a mismatched label is skipped rather than reported as a failure.
func rollBackWithReleasedBinary(t *testing.T, f *fixture) {
	t.Helper()
	binDir := os.Getenv("NEXTSQL_UPGRADE_OLD_BINDIR")
	if binDir == "" {
		t.Logf("rollback with the released %s binaries not checked: set NEXTSQL_UPGRADE_OLD_BINDIR", f.man.Label)
		return
	}
	oldCLI := filepath.Join(binDir, "nextsql")
	oldServer := filepath.Join(binDir, "nextsqld")
	for _, b := range []string{oldCLI, oldServer} {
		if st, err := os.Stat(b); err != nil || st.IsDir() {
			t.Skipf("NEXTSQL_UPGRADE_OLD_BINDIR does not contain an executable %s", filepath.Base(b))
		}
	}
	out, err := exec.Command(oldCLI, "version").CombinedOutput()
	if err != nil {
		t.Fatalf("released nextsql version: %v\n%s", err, out)
	}
	if want := strings.TrimPrefix(f.man.Label, "v"); !strings.Contains(string(out), want) {
		t.Skipf("NEXTSQL_UPGRADE_OLD_BINDIR is %q, not fixture label %s", strings.TrimSpace(string(out)), f.man.Label)
	}

	if out, err := exec.Command(oldCLI, "diagnose", "--data-dir", f.dataDir()).CombinedOutput(); err != nil {
		t.Fatalf("released %s cannot diagnose a directory the current binary wrote: %v\n%s", f.man.Label, err, out)
	} else if !strings.Contains(string(out), "status ok") {
		t.Fatalf("released %s diagnose did not report status ok after the upgrade:\n%s", f.man.Label, out)
	}

	srv := startServerWith(t, f, oldServer, oldCLI)
	defer srv.stop(t)
	for _, q := range f.expected {
		got, err := queryWith(t, oldCLI, srv.addr, f.password(), fixtureUser, q.SQL)
		if err != nil {
			t.Fatalf("released %s cannot read back its own row after the upgrade: %s\n%v", f.man.Label, q.SQL, err)
		}
		if got != q.Result {
			t.Fatalf("released %s reads a different result after the upgrade: %s\n  want: %s\n  got:  %s",
				f.man.Label, q.SQL, q.Result, got)
		}
	}
}

// --- fixture extraction ------------------------------------------------------

func extractFixture(t *testing.T, archive string) *fixture {
	t.Helper()
	root := t.TempDir()
	untar(t, archive, root)

	f := &fixture{archive: archive, root: root}
	raw, err := os.ReadFile(filepath.Join(root, "manifest.json"))
	if err != nil {
		t.Fatalf("fixture %s has no manifest: %v", archive, err)
	}
	if err := json.Unmarshal(raw, &f.man); err != nil {
		t.Fatalf("fixture %s manifest: %v", archive, err)
	}
	if f.man.DataDir == "" || f.man.KeyFile == "" || f.man.PasswordFile == "" {
		t.Fatalf("fixture %s manifest is incomplete: %+v", archive, f.man)
	}

	expected, err := os.Open(filepath.Join(root, "expected.jsonl"))
	if err != nil {
		t.Fatalf("fixture %s has no recorded results: %v", archive, err)
	}
	defer expected.Close()
	sc := bufio.NewScanner(expected)
	sc.Buffer(make([]byte, 0, 64*1024), 8<<20)
	for sc.Scan() {
		line := sc.Text()
		if strings.TrimSpace(line) == "" {
			continue
		}
		sql, result, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("fixture %s: malformed expected.jsonl line %q", archive, line)
		}
		f.expected = append(f.expected, recordedQuery{SQL: sql, Result: result})
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if len(f.expected) == 0 {
		t.Fatalf("fixture %s recorded no results", archive)
	}

	// The generating release's compatibility catalog, from the diagnose output
	// it wrote at cut time.
	old, err := os.ReadFile(filepath.Join(root, "diagnose.txt"))
	if err != nil {
		t.Fatalf("fixture %s has no recorded diagnose output: %v", archive, err)
	}
	f.generated = parseDiagnose(string(old)).catalog
	return f
}

func untar(t *testing.T, archive, dest string) {
	t.Helper()
	fh, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.Clean(hdr.Name)
		if name == "." {
			continue
		}
		// Fixture archives are repository data, but an extractor that trusts
		// its input is a bad pattern to leave in a test either way.
		if strings.HasPrefix(name, "..") || filepath.IsAbs(name) {
			t.Fatalf("fixture %s contains an escaping path %q", archive, hdr.Name)
		}
		target := filepath.Join(dest, name)
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o700); err != nil {
				t.Fatal(err)
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				t.Fatal(err)
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				t.Fatal(err)
			}
			if err := out.Close(); err != nil {
				t.Fatal(err)
			}
		default:
			t.Fatalf("fixture %s contains an unexpected entry type %q for %s", archive, string(hdr.Typeflag), hdr.Name)
		}
	}
}

// --- diagnose parsing --------------------------------------------------------

type diagnoseReport struct {
	raw     string
	status  string
	catalog map[string][2]uint16 // family -> min, max readable
	onDisk  map[string]uint16    // family -> version found on disk
}

var (
	catalogLine = regexp.MustCompile(`^\s{2}(\w+)\s+magic=\S+\s+current=\d+\s+min=(\d+)\s+max=(\d+)`)
	onDiskLine  = regexp.MustCompile(`^\s{2}(\w+)\s+v(\d+)\s+(\S+)`)
)

// parseDiagnose reads the "compatibility catalog" and "on-disk families"
// sections of `nextsql diagnose`. Both sections have been part of that output
// since v0.0.1, which is what lets a fixture carry the generating release's
// own view of itself.
func parseDiagnose(out string) diagnoseReport {
	rep := diagnoseReport{
		raw:     out,
		catalog: map[string][2]uint16{},
		onDisk:  map[string]uint16{},
	}
	section := ""
	for _, line := range strings.Split(out, "\n") {
		switch {
		case strings.HasPrefix(line, "compatibility catalog"):
			section = "catalog"
			continue
		case strings.HasPrefix(line, "on-disk families"):
			section = "ondisk"
			continue
		case strings.TrimSpace(line) == "":
			continue
		case !strings.HasPrefix(line, "  "):
			if name, val, ok := strings.Cut(line, " "); ok && name == "status" {
				rep.status = strings.TrimSpace(val)
			}
			section = ""
			continue
		}
		switch section {
		case "catalog":
			if m := catalogLine.FindStringSubmatch(line); m != nil {
				min, _ := strconv.ParseUint(m[2], 10, 16)
				max, _ := strconv.ParseUint(m[3], 10, 16)
				rep.catalog[m[1]] = [2]uint16{uint16(min), uint16(max)}
			}
		case "ondisk":
			if m := onDiskLine.FindStringSubmatch(line); m != nil {
				ver, _ := strconv.ParseUint(m[2], 10, 16)
				rep.onDisk[m[1]] = uint16(ver)
			}
		}
	}
	return rep
}

func diagnose(t *testing.T, dataDir string) diagnoseReport {
	t.Helper()
	out, err := exec.Command(bins.nextsql, "diagnose", "--data-dir", dataDir).CombinedOutput()
	if err != nil {
		t.Fatalf("diagnose %s: %v\n%s", dataDir, err, out)
	}
	return parseDiagnose(string(out))
}

// --- server plumbing ---------------------------------------------------------

type server struct {
	addr string
	cmd  *exec.Cmd
	log  string
	// exited carries the single Wait result, so the startup loop can notice a
	// server that died instead of waiting out the whole timeout.
	exited chan error
}

func startServer(t *testing.T, f *fixture) *server {
	t.Helper()
	return startServerWith(t, f, bins.nextsqld, bins.nextsql)
}

func startServerWith(t *testing.T, f *fixture, serverBin, cliBin string) *server {
	t.Helper()
	addr := freeAddr(t)
	logPath := filepath.Join(t.TempDir(), "server.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(serverBin,
		"--data-dir", f.dataDir(),
		"--key-file", f.keyFile(),
		"--listen", addr,
		"--user", fixtureUser,
		"--password-file", f.password(),
		"--no-env",
	)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	logFile.Close()
	srv := &server{addr: addr, cmd: cmd, log: logPath, exited: make(chan error, 1)}
	go func() { srv.exited <- cmd.Wait() }()

	deadline := time.Now().Add(serverStartTimeout)
	for {
		if _, err := queryWith(t, cliBin, addr, f.password(), fixtureUser, "SELECT 1"); err == nil {
			return srv
		}
		select {
		case err := <-srv.exited:
			t.Fatalf("server exited during startup: %v\n%s", err, readFile(t, logPath))
		default:
		}
		if time.Now().After(deadline) {
			srv.stop(t)
			t.Fatalf("server did not become ready on %s\n%s", addr, readFile(t, logPath))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (s *server) stop(t *testing.T) {
	t.Helper()
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if err := s.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal server: %v", err)
	}
	select {
	case err := <-s.exited:
		// A clean shutdown is part of what an upgrade has to survive: a
		// non-zero exit means the old database could not be closed.
		if err != nil {
			t.Fatalf("server did not exit cleanly: %v\n%s", err, readFile(t, s.log))
		}
	case <-time.After(2 * time.Minute):
		_ = s.cmd.Process.Kill()
		t.Fatalf("server did not shut down cleanly\n%s", readFile(t, s.log))
	}
	s.cmd = nil
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

func query(t *testing.T, addr, passwordFile, user, sql string) (string, error) {
	t.Helper()
	return queryWith(t, bins.nextsql, addr, passwordFile, user, sql)
}

func queryWith(t *testing.T, cli, addr, passwordFile, user, sql string) (string, error) {
	t.Helper()
	cmd := exec.Command(cli, "exec",
		"--addr", addr,
		"--user", user,
		"--password-file", passwordFile,
		"--insecure", "--no-env", "--json",
		"-c", sql,
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

func assertQuery(t *testing.T, srv *server, f *fixture, sql, want string) {
	t.Helper()
	got, err := query(t, srv.addr, f.password(), fixtureUser, sql)
	if err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	if got != want {
		t.Fatalf("%s\n  want: %s\n  got:  %s", sql, want, got)
	}
}

func run(t *testing.T, bin string, args ...string) {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		return fmt.Sprintf("(no log: %v)", err)
	}
	return string(b)
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestDirtyFixtureCarriesUnreplayedRedo asserts that a retained "dirty" archive
// is actually dirty. Both archives of a label are cut from the same corpus by
// the same generator run; the clean one is shut down normally, so its recovery
// boundary sits at the tip of its log, while the dirty one is SIGKILLed, so the
// boundary the archive records is behind the work it must replay. Without this,
// a generator change that started closing the server cleanly would leave a
// green suite that no longer exercised recovery at all — the same trap as a
// fault-injection test whose fault stops firing.
func TestDirtyFixtureCarriesUnreplayedRedo(t *testing.T) {
	archives, err := filepath.Glob(filepath.Join("testdata", "*.tar.gz"))
	if err != nil {
		t.Fatal(err)
	}
	type pair struct {
		clean, dirty   *diagnoseReport
		cleanQ, dirtyQ int
	}
	labels := map[string]*pair{}
	for _, archive := range archives {
		base := strings.TrimSuffix(filepath.Base(archive), ".tar.gz")
		label, kind, ok := strings.Cut(base, "-")
		if !ok {
			t.Fatalf("fixture %s is not named <label>-<clean|dirty>.tar.gz", archive)
		}
		p := labels[label]
		if p == nil {
			p = &pair{}
			labels[label] = p
		}
		rep := parseDiagnose(string(readArchiveFile(t, archive, "diagnose.txt")))
		queries := len(strings.Split(strings.TrimSpace(string(readArchiveFile(t, archive, "expected.jsonl"))), "\n"))
		switch kind {
		case "clean":
			p.clean, p.cleanQ = &rep, queries
		case "dirty":
			p.dirty, p.dirtyQ = &rep, queries
		default:
			t.Fatalf("fixture %s has unknown kind %q", archive, kind)
		}
	}
	if len(labels) == 0 {
		t.Fatal("no retained release fixtures in testdata")
	}
	for label, p := range labels {
		t.Run(label, func(t *testing.T) {
			if p.clean == nil || p.dirty == nil {
				t.Fatalf("%s must retain both a clean and a dirty archive", label)
			}
			cleanCP, dirtyCP := p.clean.value(t, "checkpoint_lsn"), p.dirty.value(t, "checkpoint_lsn")
			if dirtyCP >= cleanCP {
				t.Fatalf("%s dirty archive records checkpoint_lsn %d, not behind the clean archive's %d: "+
					"it was not killed with work outstanding, so replaying it does not exercise recovery",
					label, dirtyCP, cleanCP)
			}
			if p.dirtyQ <= p.cleanQ {
				t.Fatalf("%s dirty archive recorded %d results, clean recorded %d: "+
					"testdata/dirty.sql did not run, so the archive has no post-checkpoint writes to replay",
					label, p.dirtyQ, p.cleanQ)
			}
		})
	}
}

// value returns a scalar `name <value>` line from a diagnose report.
func (r diagnoseReport) value(t *testing.T, name string) uint64 {
	t.Helper()
	for _, line := range strings.Split(r.raw, "\n") {
		if k, v, ok := strings.Cut(line, " "); ok && k == name {
			n, err := strconv.ParseUint(strings.TrimSpace(v), 10, 64)
			if err != nil {
				t.Fatalf("diagnose %s = %q: %v", name, v, err)
			}
			return n
		}
	}
	t.Fatalf("diagnose output has no %s line:\n%s", name, r.raw)
	return 0
}

// readArchiveFile returns one top-level file from a fixture archive without
// unpacking the multi-hundred-megabyte data directory beside it.
func readArchiveFile(t *testing.T, archive, name string) []byte {
	t.Helper()
	fh, err := os.Open(archive)
	if err != nil {
		t.Fatal(err)
	}
	defer fh.Close()
	gz, err := gzip.NewReader(fh)
	if err != nil {
		t.Fatal(err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if filepath.Clean(hdr.Name) != name {
			continue
		}
		b, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	t.Fatalf("fixture %s has no %s", archive, name)
	return nil
}

// preCatalogFamilies records on-disk families that existed before they were
// listed in the compatibility catalog. The generating release printed no
// window for such a family, but its code still enforced one, so rollback
// safety is checked against that enforced window rather than failing on the
// absence of a printed entry.
//
// Every entry must be justified from the generating release's own source, and
// the window must be what that release's code actually accepted — not what
// the current binary can write. Adding an entry here widens nothing for a
// genuinely new format: an uncatalogued family with no entry still fails.
var preCatalogFamilies = map[string]map[string][2]uint16{
	// v0.0.1 wrote and read the NSKS keystore but did not carry it in its
	// catalog (12 families, no "keystore"). Its internal/crypto hardcoded
	// keystoreVersion = 1 and decodeKeystore rejected any other value
	// outright, so v1 rolls back and v2 does not. v2 is written only when an
	// operator configures a recovery key, which is the documented opt-in that
	// gives up v0.0.1 rollback for that database. See docs/security.md
	// "Recovery keys" and TODO.md log #242.
	"keystore": {
		"v0.0.1": {1, 1},
	},
}

func preCatalogWindow(family, label string) ([2]uint16, bool) {
	byRelease, ok := preCatalogFamilies[family]
	if !ok {
		return [2]uint16{}, false
	}
	win, ok := byRelease[label]
	return win, ok
}
