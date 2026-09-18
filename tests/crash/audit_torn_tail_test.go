package crash

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// TestNextsqldStartsAfterATornAuditTail reproduces the field failure this
// test exists for: a deployment whose nextsql.audit was left with a partly
// written final record could not be started again at all, because startup
// verified the chain and refused the whole file over its last line. Under a
// container supervisor that is an unbounded restart loop, and everything
// that depends on the database stays down with it.
//
// The daemon must instead quarantine the unacknowledged final record, start,
// and say in its own chain that it did so.
func TestNextsqldStartsAfterATornAuditTail(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and runs nextsqld")
	}
	bins := buildCrashBinaries(t)
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	keyFile := filepath.Join(dir, "root.key")
	passFile := filepath.Join(dir, "pw")
	if err := os.WriteFile(passFile, []byte("torn-tail-test-password-1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	run(t, bins.nextsql, "init",
		"--data-dir", dataDir, "--key-file", keyFile,
		"--user", "app", "--password-file", passFile, "--database", "app")

	auditPath := filepath.Join(dataDir, "nextsql.audit")

	// One clean lifecycle, so the chain has real records in it.
	srv := startDaemon(t, bins, dataDir, keyFile, passFile)
	srv.stop(t)

	intact, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(intact) == 0 {
		t.Fatal("expected the first run to have written audit records")
	}
	trimmed := bytes.TrimRight(intact, "\n")
	lastStart := bytes.LastIndexByte(trimmed, '\n') + 1
	if lastStart <= 0 {
		t.Fatalf("expected more than one audit record, got %q", intact)
	}
	// Leave the final record half-written, exactly as an interrupted append
	// leaves it.
	torn := intact[:lastStart+15]
	dropped := torn[lastStart:]
	if err := os.WriteFile(auditPath, torn, 0o600); err != nil {
		t.Fatal(err)
	}

	// Precondition: this is the state the report described.
	var before auditReport
	mustVerify(t, bins.nextsql, auditPath, &before)
	if before.Verified || before.Problem != "malformed JSON line" {
		t.Fatalf("expected a malformed final line before repair: %+v", before)
	}
	if before.FirstBadLine != before.Lines {
		t.Fatalf("expected the damage to be the last line: %+v", before)
	}

	// The daemon must now come up rather than crash-loop.
	srv = startDaemon(t, bins, dataDir, keyFile, passFile)
	srv.stop(t)

	var after auditReport
	mustVerify(t, bins.nextsql, auditPath, &after)
	if !after.Verified {
		t.Fatalf("audit chain still does not verify after restart: %+v", after)
	}

	// The repair is in the chain, not only in the process log.
	repaired, err := os.ReadFile(auditPath)
	if err != nil {
		t.Fatal(err)
	}
	var repairLine string
	for _, line := range strings.Split(strings.TrimRight(string(repaired), "\n"), "\n") {
		var ev struct {
			Action string `json:"action"`
			Object string `json:"object"`
		}
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("repaired chain has an unreadable line: %v", err)
		}
		if ev.Action == "audit.torn_tail.repair" {
			repairLine = ev.Object
		}
	}
	if repairLine == "" {
		t.Fatal("the repair was not recorded in the audit chain")
	}
	for _, want := range []string{"kind=truncated", "dropped_bytes=15", "quarantine=nextsql.audit.torn-"} {
		if !strings.Contains(repairLine, want) {
			t.Fatalf("repair record %q is missing %q", repairLine, want)
		}
	}

	// And the dropped bytes were preserved, not destroyed.
	entries, err := filepath.Glob(filepath.Join(dataDir, "nextsql.audit.torn-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected exactly one quarantine file, got %v", entries)
	}
	quarantined, err := os.ReadFile(entries[0])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(quarantined, dropped) {
		t.Fatalf("quarantine holds %q, dropped %q", quarantined, dropped)
	}
	if !bytes.HasPrefix(repaired, intact[:lastStart]) {
		t.Fatal("repair did not preserve the records that verified")
	}
}

type auditReport struct {
	Lines        int    `json:"lines"`
	Chained      int    `json:"chained"`
	Verified     bool   `json:"verified"`
	FirstBadLine int    `json:"first_bad_line"`
	Problem      string `json:"problem"`
}

func mustVerify(t *testing.T, cli, path string, into *auditReport) {
	t.Helper()
	// A failed verification exits non-zero but still prints the report.
	out, _ := exec.Command(cli, "audit", "verify", "--file", path, "--json").Output()
	if err := json.Unmarshal(bytes.TrimSpace(out), into); err != nil {
		t.Fatalf("audit verify --json: %v (output %q)", err, out)
	}
}

type crashBinaries struct{ nextsql, nextsqld string }

var (
	crashBinOnce sync.Once
	crashBins    crashBinaries
	crashBinErr  error
)

func buildCrashBinaries(t *testing.T) crashBinaries {
	t.Helper()
	crashBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "nextsql-crash-bin")
		if err != nil {
			crashBinErr = err
			return
		}
		crashBins = crashBinaries{
			nextsql:  filepath.Join(dir, "nextsql"),
			nextsqld: filepath.Join(dir, "nextsqld"),
		}
		for _, b := range []struct{ out, pkg string }{
			{crashBins.nextsql, "./cmd/nextsql"},
			{crashBins.nextsqld, "./cmd/nextsqld"},
		} {
			cmd := exec.Command("go", "build", "-o", b.out, b.pkg)
			cmd.Dir = filepath.Join("..", "..")
			if out, cerr := cmd.CombinedOutput(); cerr != nil {
				crashBinErr = fmt.Errorf("build %s: %v\n%s", b.pkg, cerr, out)
				return
			}
		}
	})
	if crashBinErr != nil {
		t.Fatal(crashBinErr)
	}
	return crashBins
}

func run(t *testing.T, bin string, args ...string) string {
	t.Helper()
	out, err := exec.Command(bin, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", filepath.Base(bin), strings.Join(args, " "), err, out)
	}
	return string(out)
}

type daemon struct {
	cmd    *exec.Cmd
	addr   string
	log    string
	exited chan error
}

func startDaemon(t *testing.T, bins crashBinaries, dataDir, keyFile, passFile string) *daemon {
	t.Helper()
	addr := freeLoopbackAddr(t)
	logPath := filepath.Join(t.TempDir(), "nextsqld.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bins.nextsqld,
		"--data-dir", dataDir, "--key-file", keyFile,
		"--listen", addr, "--user", "app", "--password-file", passFile,
		"--no-env",
	)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	logFile.Close()
	d := &daemon{cmd: cmd, addr: addr, log: logPath, exited: make(chan error, 1)}
	go func() { d.exited <- cmd.Wait() }()

	deadline := time.Now().Add(60 * time.Second)
	for {
		probe := exec.Command(bins.nextsql, "exec", "--addr", addr, "--user", "app",
			"--password-file", passFile, "--insecure", "-c", "SELECT 1")
		if err := probe.Run(); err == nil {
			return d
		}
		select {
		case err := <-d.exited:
			body, _ := os.ReadFile(logPath)
			t.Fatalf("nextsqld exited during startup: %v\n%s", err, body)
		default:
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			body, _ := os.ReadFile(logPath)
			t.Fatalf("nextsqld did not become ready on %s\n%s", addr, body)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (d *daemon) stop(t *testing.T) {
	t.Helper()
	if err := d.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case <-d.exited:
	case <-time.After(30 * time.Second):
		_ = d.cmd.Process.Kill()
		<-d.exited
		t.Fatal("nextsqld did not exit on SIGTERM")
	}
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}
