package bench

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestDetVecDistinctNormalized(t *testing.T) {
	seen := make(map[[8]float32]struct{}, 1024)
	for i := 0; i < 1024; i++ {
		v := detVec(8, i)
		var key [8]float32
		copy(key[:], v)
		if _, ok := seen[key]; ok {
			t.Fatalf("duplicate deterministic vector at row %d", i)
		}
		seen[key] = struct{}{}
		var norm float64
		for _, f := range v {
			norm += float64(f) * float64(f)
		}
		if math.Abs(norm-1) > 1e-5 {
			t.Fatalf("row %d norm=%g", i, norm)
		}
	}
}

func TestVecLitUsesDecimalNotation(t *testing.T) {
	lit := vecLit([]float32{1e-7, -2.5e-8, 0.25})
	if strings.ContainsAny(lit, "eE") {
		t.Fatalf("vector literal contains exponent: %s", lit)
	}
}

func TestSLOBufferPagesForLargeHNSW(t *testing.T) {
	if got := normalizeSLOBufferPages(1_000_000, 32_768); got != 131_072 {
		t.Fatalf("large HNSW buffer pages=%d, want 131072", got)
	}
	if got := normalizeSLOBufferPages(100_000, 8_192); got != 8_192 {
		t.Fatalf("smaller HNSW buffer pages=%d, want 8192", got)
	}
}

func TestOfficialWorkloads(t *testing.T) {
	reps, err := Run(Options{
		Dir:         t.TempDir(),
		BufferPages: 16,
		Duration:    40 * time.Millisecond,
		Concurrency: 1,
		Rows:        16,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(reps) != len(Known()) {
		t.Fatalf("got %d reports", len(reps))
	}
	seen := map[string]bool{}
	for _, r := range reps {
		if r.Ops < 1 {
			t.Fatalf("%s: zero ops (errors=%d)", r.Workload, r.Errors)
		}
		if r.Errors > r.Ops {
			t.Fatalf("%s: errors %d ops %d", r.Workload, r.Errors, r.Ops)
		}
		if r.Hardware.Encryption == "" || r.Hardware.Durability == "" {
			t.Fatalf("%s missing hardware context", r.Workload)
		}
		if r.QPS <= 0 || r.P50 <= 0 {
			t.Fatalf("%s: qps=%v p50=%v", r.Workload, r.QPS, r.P50)
		}
		seen[r.Workload] = true
	}
	for _, name := range Known() {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
}

func TestSLOSuite(t *testing.T) {
	suite, err := RunSLO(SLOOptions{
		Dir:           t.TempDir(),
		BufferPages:   32,
		Duration:      30 * time.Millisecond,
		Concurrency:   1,
		LatencyRows:   32,
		MaxScanRows:   32,
		HybridRows:    16,
		VectorRows:    16,
		VectorDim:     8,
		VectorQueries: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if suite == nil || len(suite.Reports) < 7 {
		t.Fatalf("reports=%d", len(suite.Reports))
	}
	if suite.Hardware.CPU == "" || suite.Hardware.Encryption == "" || suite.Hardware.Filesystem == "" {
		t.Fatalf("incomplete hardware %+v", suite.Hardware)
	}
	seen := map[string]bool{}
	for _, r := range suite.Reports {
		if r.Query == "" || r.Indexes == "" || r.RowWidth == "" {
			t.Fatalf("%s missing published context", r.Name)
		}
		if r.Ops < 1 {
			t.Fatalf("%s: zero ops", r.Name)
		}
		seen[r.Name] = true
	}
	for _, name := range []string{"point", "index", "insert", "update", "hybrid", "vector"} {
		if !seen[name] {
			t.Fatalf("missing %s", name)
		}
	}
	if !seen["scan-32"] && !seen["scan-25K"] {
		// tiny scale uses the numeric name
		found := false
		for name := range seen {
			if len(name) >= 4 && name[:4] == "scan" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing scan report in %v", seen)
		}
	}
	foundInsert := false
	for _, r := range suite.Reports {
		if len(r.Name) >= 7 && r.Name[:7] == "insert-" {
			foundInsert = true
			if r.TPS <= 0 || r.Rows < 1 {
				t.Fatalf("bulk insert report %+v", r)
			}
		}
	}
	if !foundInsert {
		t.Fatalf("missing bulk insert-<scale> report in %v", seen)
	}
	if !seen["update-32"] && !hasPrefix(seen, "update-") {
		t.Fatalf("missing bulk update-<scale> report in %v", seen)
	}
	if !seen["delete-32"] && !hasPrefix(seen, "delete-") {
		t.Fatalf("missing bulk delete-<scale> report in %v", seen)
	}
	var vec *SLOReport
	for i := range suite.Reports {
		if suite.Reports[i].Name == "vector" {
			vec = &suite.Reports[i]
		}
	}
	if vec == nil || !vec.HasRecall || vec.RecallAt10 <= 0 {
		t.Fatalf("vector recall missing: %+v", vec)
	}
}

func hasPrefix(seen map[string]bool, prefix string) bool {
	for name := range seen {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			return true
		}
	}
	return false
}

func TestUnknownWorkload(t *testing.T) {
	_, err := Run(Options{
		Dir:         t.TempDir(),
		BufferPages: 8,
		Duration:    time.Millisecond,
		Rows:        8,
		Workloads:   []string{"nope"},
	})
	if err == nil {
		t.Fatal("expected unknown workload")
	}
}
