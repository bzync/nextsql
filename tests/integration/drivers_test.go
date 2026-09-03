package integration

import (
	"os"
	"os/exec"
	"path/filepath"
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

func TestDenoDriverUnit(t *testing.T) {
	deno, err := exec.LookPath("deno")
	if err != nil {
		t.Skip("deno not installed")
	}
	cmd := exec.Command(deno, "test", "--allow-net", "--allow-read", "--allow-write", "nextsql_test.js")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "deno")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deno unit: %v\n%s", err, out)
	}
}

func TestDenoDriverLiveTLS(t *testing.T) {
	deno, err := exec.LookPath("deno")
	if err != nil {
		t.Skip("deno not installed")
	}
	addr, _ := startTLSServer(t)
	caPath := writeClientCA(t, addr)
	cmd := exec.Command(deno, "run", "--allow-net", "--allow-env", "--allow-read", "live.ts")
	cmd.Dir = filepath.Join(repoRoot(t), "drivers", "deno")
	cmd.Env = append(os.Environ(),
		"NEXTSQL_ADDR="+addr,
		"NEXTSQL_CA="+caPath,
		"NEXTSQL_DATABASE_USER=app",
		"NEXTSQL_DATABASE_PASS=s3cret",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("deno live: %v\n%s", err, out)
	}
}

func TestTypeScriptCheck(t *testing.T) {
	root := repoRoot(t)
	if deno, err := exec.LookPath("deno"); err == nil {
		cmd := exec.Command(deno, "check", "mod.ts", "usage.ts", "live.ts")
		cmd.Dir = filepath.Join(root, "drivers", "deno")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("deno check: %v\n%s", err, out)
		}
	} else {
		t.Log("deno not installed; skip deno check")
	}

	tsc := lookupTSC(t, root)
	if tsc == nil {
		if _, err := exec.LookPath("deno"); err != nil {
			t.Skip("tsc and deno not installed")
		}
		return
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
