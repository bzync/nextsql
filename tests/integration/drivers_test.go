package integration

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func TestNodeDriverUnit(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	cmd := exec.Command(node, "--test", "nextsql.test.js")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "node")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node unit: %v\n%s", err, out)
	}
}

func TestNodeDriverLiveTLS(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node not installed")
	}
	addr, _ := startTLSServer(t)
	caPath := writeClientCA(t, addr)
	cmd := exec.Command(node, "live.js")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "node")
	cmd.Env = append(os.Environ(),
		"NEXTSQL_ADDR="+addr,
		"NEXTSQL_CA="+caPath,
		"NEXTSQL_DATABASE_USER=app",
		"NEXTSQL_DATABASE_PASS=s3cret",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node live: %v\n%s", err, out)
	}
}

func TestPHPDriverUnit(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	cmd := exec.Command(php, "tests/unit.php")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "php")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("php unit: %v\n%s", err, out)
	}
}

func TestPHPDriverLiveTLS(t *testing.T) {
	php, err := exec.LookPath("php")
	if err != nil {
		t.Skip("php not installed")
	}
	addr, _ := startTLSServer(t)
	caPath := writeClientCA(t, addr)
	cmd := exec.Command(php, "tests/live.php")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "php")
	cmd.Env = append(os.Environ(),
		"NEXTSQL_ADDR="+addr,
		"NEXTSQL_CA="+caPath,
		"NEXTSQL_DATABASE_USER=app",
		"NEXTSQL_DATABASE_PASS=s3cret",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("php live: %v\n%s", err, out)
	}
}

func TestBunDriverUnit(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not installed")
	}
	cmd := exec.Command(bun, "test")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "bun")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bun unit: %v\n%s", err, out)
	}
}

func TestBunDriverLiveTLS(t *testing.T) {
	bun, err := exec.LookPath("bun")
	if err != nil {
		t.Skip("bun not installed")
	}
	addr, _ := startTLSServer(t)
	caPath := writeClientCA(t, addr)
	cmd := exec.Command(bun, "live.ts")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "bun")
	cmd.Env = append(os.Environ(),
		"NEXTSQL_ADDR="+addr,
		"NEXTSQL_CA="+caPath,
		"NEXTSQL_DATABASE_USER=app",
		"NEXTSQL_DATABASE_PASS=s3cret",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("bun live: %v\n%s", err, out)
	}
}

func TestTypeScriptCheck(t *testing.T) {
	root := repoRoot(t)

	tsc := lookupTSC(t, root)
	if tsc == nil {
		t.Skip("tsc not installed")
	}
	for _, dir := range []string{
		filepath.Join(root, "drivers", "node"),
		filepath.Join(root, "drivers", "bun"),
	} {
		cmd := exec.Command(tsc[0], append(tsc[1:], "-p", "tsconfig.json")...)
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("tsc %s: %v\n%s", dir, err, out)
		}
	}
}

func lookupTSC(t *testing.T, root string) []string {
	t.Helper()
	if p, err := exec.LookPath("tsc"); err == nil {
		return []string{p}
	}
	local := filepath.Join(root, "docs", "web", "node_modules", "typescript", "bin", "tsc")
	if info, err := os.Stat(local); err == nil && !info.IsDir() {
		return []string{local}
	}
	npx, err := exec.LookPath("npx")
	if err != nil {
		return nil
	}
	return []string{npx, "--yes", "-p", "typescript@5.6.3", "tsc"}
}

func writeClientCA(t *testing.T, _ string) string {
	t.Helper()
	// startTLSServer writes certPath in its temp dir; we cannot see it.
	// Mint a matching client CA by creating a dedicated server instead.
	// The live tests call this after startTLSServer — we need the PEM that
	// the client TLS config trusts. Recreate via a side channel: store it.
	pem := lastClientCAPEM
	if len(pem) == 0 {
		t.Fatal("client CA PEM not recorded")
	}
	path := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(path, pem, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The Python and Ruby live suites were the only official-driver live checks not
// driven from Go, so nothing ran them: they sat failing against a real server
// (an unsupported `COUNT(*)` over a system table) with no signal. Wiring them in
// here gives every official driver the same live gate, on the same in-process
// TLS server the other four use.
//
// Each gets its own server because both suites create the same `items` table,
// and startTLSServer builds a fresh database per call.

func TestPythonDriverUnit(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	cmd := exec.Command(py, "-m", "unittest", "discover", "-s", "tests", "-q")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "python")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python unit: %v\n%s", err, out)
	}
}

func TestPythonDriverLiveTLS(t *testing.T) {
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not installed")
	}
	addr, _ := startTLSServer(t)
	caPath := writeClientCA(t, addr)
	cmd := exec.Command(py, "-m", "unittest", "discover", "-s", "tests", "-q")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "python")
	cmd.Env = append(os.Environ(),
		"NEXTSQL_ADDR="+addr,
		"NEXTSQL_CA="+caPath,
		"NEXTSQL_DATABASE_USER=app",
		"NEXTSQL_DATABASE_PASS=s3cret",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("python live: %v\n%s", err, out)
	}
	// The live cases skip themselves without the address, so a green run that
	// never connected would otherwise look identical to a real pass.
	if bytes.Contains(out, []byte("skipped")) {
		t.Fatalf("python live suite skipped its live cases:\n%s", out)
	}
}

func TestRubyDriverUnit(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not installed")
	}
	cmd := exec.Command(ruby, "-Ilib", "-Itest", "test/test_protocol.rb")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "ruby")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ruby unit: %v\n%s", err, out)
	}
}

func TestRubyDriverLiveTLS(t *testing.T) {
	ruby, err := exec.LookPath("ruby")
	if err != nil {
		t.Skip("ruby not installed")
	}
	addr, _ := startTLSServer(t)
	caPath := writeClientCA(t, addr)
	cmd := exec.Command(ruby, "-Ilib", "-Itest", "test/test_live.rb")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "ruby")
	cmd.Env = append(os.Environ(),
		"NEXTSQL_ADDR="+addr,
		"NEXTSQL_CA="+caPath,
		"NEXTSQL_DATABASE_USER=app",
		"NEXTSQL_DATABASE_PASS=s3cret",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ruby live: %v\n%s", err, out)
	}
	// Match the summary field, not the substring: minitest also prints an
	// assertions-per-second rate, and "186.4040 assertions/s" contains
	// "0 assertions". The summary reads "3 runs, 16 assertions, 0 failures".
	if regexp.MustCompile(`\b0 assertions,`).Match(out) {
		t.Fatalf("ruby live suite asserted nothing:\n%s", out)
	}
}
