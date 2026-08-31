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

func TestPartitionBench(t *testing.T) {
	suite, err := RunPartition(PartitionOptions{
		Dir:         t.TempDir(),
		BufferPages: 64,
		Duration:    40 * time.Millisecond,
		Rows:        512,
	})
	if err != nil {
		t.Fatal(err)
	}
	if suite == nil || len(suite.Reports) != 8 {
		t.Fatalf("reports=%d, want 8", len(suite.Reports))
	}
	byName := map[string]PartitionReport{}
	for _, r := range suite.Reports {
		if r.Ops < 1 {
			t.Fatalf("%s: zero ops", r.Name)
		}
		if r.Hardware.Encryption == "" || r.Hardware.Durability == "" {
			t.Fatalf("%s missing hardware context", r.Name)
		}
		byName[r.Name] = r
	}
	// The single-bucket queries must prune to exactly one band; the unpruned
	// full aggregate must keep every band.
	for _, n := range []string{"bucket-scan-part", "bucket-agg-part"} {
		if got := byName[n].Partitions; got != "1/8 (pruned)" {
			t.Fatalf("%s partitions=%q, want 1/8 (pruned)", n, got)
		}
	}
	if got := byName["full-agg-part"].Partitions; got != "8/8" {
		t.Fatalf("full-agg-part partitions=%q, want 8/8", got)
	}
	// Pruning must not make the unpruned full aggregate pathologically slower
	// than the flat scan (partitioning overhead sanity, generous CI bound).
	if fp, pp := byName["full-agg-flat"].P50, byName["full-agg-part"].P50; fp > 0 && pp > fp*8 {
		t.Fatalf("full-agg-part p50 %v >> flat %v", pp, fp)
	}
}

func TestReadScaleBench(t *testing.T) {
	suite, err := RunReadScale(ReadScaleOptions{
		Dir:         t.TempDir(),
		BufferPages: 64,
		Duration:    150 * time.Millisecond,
		Readers:     2,
		Rows:        512,
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"strong-1n", "stale-1n", "stale-2n", "stale-3n", "bounded-3n"}
	if suite == nil || len(suite.Reports) != len(want) {
		t.Fatalf("reports=%d, want %d", len(suite.Reports), len(want))
	}
	byName := map[string]ReadScaleReport{}
	for i, r := range suite.Reports {
		if r.Name != want[i] {
			t.Fatalf("report %d = %q, want %q", i, r.Name, want[i])
		}
		if r.Ops < 1 {
			t.Fatalf("%s: zero ops (errors=%d)", r.Name, r.Errors)
		}
		if r.Errors > 0 {
			t.Fatalf("%s: %d read errors", r.Name, r.Errors)
		}
		if r.QPS <= 0 || r.P50 <= 0 {
			t.Fatalf("%s: qps=%v p50=%v", r.Name, r.QPS, r.P50)
		}
		if r.Hardware.Encryption == "" || r.Hardware.Durability == "" {
			t.Fatalf("%s missing hardware context", r.Name)
		}
		byName[r.Name] = r
	}
	if byName["stale-3n"].Nodes != 3 || byName["stale-1n"].Nodes != 1 {
		t.Fatalf("node counts: %+v", byName)
	}
	if byName["stale-3n"].Readers != 3*byName["stale-1n"].Readers {
		t.Fatalf("reader counts: 1n=%d 3n=%d", byName["stale-1n"].Readers, byName["stale-3n"].Readers)
	}
	// The stale-1n phase is the aggregate-QPS baseline: its ratio is 1.0 by
	// construction.
	if s := byName["stale-1n"].AggQPSRatio; s < 0.99 || s > 1.01 {
		t.Fatalf("stale-1n AggQPSRatio=%v, want ~1", s)
	}
	// Leader-only phases: the leader serves every read.
	if r := byName["stale-1n"]; r.LeaderOps != r.Ops {
		t.Fatalf("stale-1n leader ops %d != total %d", r.LeaderOps, r.Ops)
	}
	// Follower routing moves read load off the leader: with 3 serving nodes the
	// leader must serve a strict minority of the reads.
	if r := byName["stale-3n"]; r.LeaderOps*2 >= r.Ops {
		t.Fatalf("stale-3n leader served %d of %d reads (not offloaded)", r.LeaderOps, r.Ops)
	}
	// STRONG reads pay a Raft read barrier that STALE reads skip; the published
	// numbers (docs/ha.md) show STALE ~2x STRONG single-node. Absolute QPS is
	// too timing-sensitive to assert on a loaded CI box, so this test only
	// checks structure, the leader read-offload, and error-free execution.
}

func TestVectorQuantBench(t *testing.T) {
	suite, err := RunVectorQuant(VectorQuantOptions{
		Dir:       t.TempDir(),
		Rows:      240,
		Dim:       32,
		SparseDim: 256,
		SparseNNZ: 8,
		Queries:   12,
	})
	if err != nil {
		t.Fatal(err)
	}
	if suite == nil || len(suite.Reports) != 8 {
		t.Fatalf("reports=%d, want 8", len(suite.Reports))
	}
	byElem := map[string]VectorQuantReport{}
	for i, r := range suite.Reports {
		if want := []string{"F32", "F16", "I8", "F32/qh-F16", "F32/qh-I8", "F32/ivf", "F32/ivfpq", "SPARSE"}[i]; r.Element != want {
			t.Fatalf("report %d = %q, want %q", i, r.Element, want)
		}
		if r.Queries != 12 || r.P50 <= 0 || r.QPS <= 0 {
			t.Fatalf("%s: queries=%d p50=%v qps=%v", r.Element, r.Queries, r.P50, r.QPS)
		}
		if r.RecallAt10 <= 0 || r.RecallAt10 > 1 {
			t.Fatalf("%s: recall@10=%v", r.Element, r.RecallAt10)
		}
		if r.RawPayload <= 0 || r.IndexBytes <= 0 {
			t.Fatalf("%s: raw-payload=%d index=%d", r.Element, r.RawPayload, r.IndexBytes)
		}
		if r.Hardware.Encryption == "" || r.Hardware.Durability == "" {
			t.Fatalf("%s missing hardware context", r.Element)
		}
		byElem[r.Element] = r
	}
	// Per-element on-disk width shrinks monotonically: F32 4 B, F16 2 B, I8 1 B.
	if byElem["F32"].ElemBytes != 4 || byElem["F16"].ElemBytes != 2 || byElem["I8"].ElemBytes != 1 {
		t.Fatalf("element widths: %d/%d/%d", byElem["F32"].ElemBytes, byElem["F16"].ElemBytes, byElem["I8"].ElemBytes)
	}
	if !(byElem["F32"].RawPayload > byElem["F16"].RawPayload && byElem["F16"].RawPayload > byElem["I8"].RawPayload) {
		t.Fatalf("raw payload not shrinking: F32=%d F16=%d I8=%d",
			byElem["F32"].RawPayload, byElem["F16"].RawPayload, byElem["I8"].RawPayload)
	}
	// Quantisation error is deterministic: none for F32, some for F16, more for I8.
	if byElem["F32"].QuantErr != 0 {
		t.Fatalf("F32 quant-err=%v, want 0", byElem["F32"].QuantErr)
	}
	if !(byElem["F16"].QuantErr > 0 && byElem["I8"].QuantErr > byElem["F16"].QuantErr) {
		t.Fatalf("quant-err not ordered: F16=%v I8=%v", byElem["F16"].QuantErr, byElem["I8"].QuantErr)
	}
	// A quantised HNSW graph keeps a full-precision column (ElemBytes 4, zero
	// column round-trip error is not asserted since QuantErr reflects the index
	// encoding) and re-ranks, so recall stays in range while its own traversal
	// quantisation error is non-zero.
	for _, label := range []string{"F32/qh-F16", "F32/qh-I8"} {
		qh := byElem[label]
		if qh.ElemBytes != 4 {
			t.Fatalf("%s: column width %d, want 4", label, qh.ElemBytes)
		}
		if qh.QuantErr <= 0 {
			t.Fatalf("%s: index quant-err=%v, want > 0", label, qh.QuantErr)
		}
		if qh.RecallAt10 < 0.5 {
			t.Fatalf("%s: recall@10=%v too low after re-rank", label, qh.RecallAt10)
		}
	}
	// The IVF row builds a coarse-quantiser index over a full-precision F32
	// column: no vector quantisation (QuantErr 0, ElemBytes 4), trained LISTS /
	// PROBES recorded, and it reports the same size/latency/recall fields.
	ivf := byElem["F32/ivf"]
	if ivf.Method != "ivf" {
		t.Fatalf("F32/ivf: method=%q, want ivf", ivf.Method)
	}
	if ivf.ElemBytes != 4 || ivf.QuantErr != 0 {
		t.Fatalf("F32/ivf: elem=%d quant-err=%v, want 4 / 0", ivf.ElemBytes, ivf.QuantErr)
	}
	if ivf.IVFLists < 1 || ivf.IVFProbes < 1 || ivf.IVFProbes > ivf.IVFLists {
		t.Fatalf("F32/ivf: lists=%d probes=%d", ivf.IVFLists, ivf.IVFProbes)
	}
	if ivf.IndexBytes <= 0 {
		t.Fatalf("F32/ivf: index=%d", ivf.IndexBytes)
	}
	// The IVF-PQ row builds a coarse quantiser plus a product-quantisation
	// codebook over an F32 column: it reports LISTS / PROBES / SUBSPACES and the
	// same size/latency/recall fields, and the exact re-rank keeps recall usable.
	pq := byElem["F32/ivfpq"]
	if pq.Method != "ivfpq" {
		t.Fatalf("F32/ivfpq: method=%q, want ivfpq", pq.Method)
	}
	if pq.ElemBytes != 4 || pq.QuantErr != 0 {
		t.Fatalf("F32/ivfpq: elem=%d quant-err=%v, want 4 / 0", pq.ElemBytes, pq.QuantErr)
	}
	if pq.IVFLists < 1 || pq.IVFProbes < 1 || pq.IVFProbes > pq.IVFLists {
		t.Fatalf("F32/ivfpq: lists=%d probes=%d", pq.IVFLists, pq.IVFProbes)
	}
	if pq.IVFSubspaces < 1 || pq.Dim%pq.IVFSubspaces != 0 {
		t.Fatalf("F32/ivfpq: subspaces=%d must divide dim=%d", pq.IVFSubspaces, pq.Dim)
	}
	if pq.IndexBytes <= 0 {
		t.Fatalf("F32/ivfpq: index=%d", pq.IndexBytes)
	}
	// The SPARSE row uses a high-dimension, low-nnz corpus (not the dense
	// 32-d set): lossless encoding (QuantErr 0), raw NSSV payload far below a
	// dense float32 floor, and COSINE re-rank recall in range.
	sp := byElem["SPARSE"]
	if sp.Method != "sparse" {
		t.Fatalf("SPARSE: method=%q, want sparse", sp.Method)
	}
	if sp.Dim != 256 || sp.SparseNNZ != 8 {
		t.Fatalf("SPARSE: dim=%d nnz=%d, want 256 / 8", sp.Dim, sp.SparseNNZ)
	}
	if sp.ElemBytes != 0 || sp.QuantErr != 0 {
		t.Fatalf("SPARSE: elem=%d quant-err=%v, want 0 / 0", sp.ElemBytes, sp.QuantErr)
	}
	denseFloor := int64(sp.Rows) * int64(sp.Dim) * 4
	if sp.RawPayload <= 0 || sp.RawPayload >= denseFloor {
		t.Fatalf("SPARSE: raw-payload=%d, want 0 < p < dense-floor %d", sp.RawPayload, denseFloor)
	}
	if sp.IndexBytes <= 0 {
		t.Fatalf("SPARSE: index=%d", sp.IndexBytes)
	}
	if sp.RecallAt10 < 0.5 {
		t.Fatalf("SPARSE: recall@10=%v too low for inverted-index + COSINE re-rank", sp.RecallAt10)
	}
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
