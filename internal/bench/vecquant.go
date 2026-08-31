package bench

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/float16"
	"github.com/bzync/nextsql/internal/int8vec"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/vector"
)

// vecQuantConfig is one measured dense configuration: a column element type and
// an ANN index. Rows 1-3 compare the stored element types (unquantised HNSW
// graph); rows 4-5 keep a full-precision column but quantise the HNSW graph, so
// the graph-size / recall trade-off of a quantised index is measured directly
// against its own baseline; the last two rows build an IVF and an IVF-PQ index
// over the same F32 column. A separate SPARSE row (runOneSparseQuant) uses a
// high-dimension, low-nnz corpus rather than this dense set.
type vecQuantConfig struct {
	Label    string // report Element field
	ColElem  string // F32 / F16 / I8
	IdxQuant string // "" / F16 / I8  (WITH (QUANTIZATION = ...))
	Method   string // "" (HNSW) / "ivf" / "ivfpq"
}

// vecQuantConfigs is the fixed comparison set. Each row is measured in its own
// fresh encrypted database so on-disk size and resident heap are not
// cross-contaminated.
var vecQuantConfigs = []vecQuantConfig{
	{Label: "F32", ColElem: "F32"},
	{Label: "F16", ColElem: "F16"},
	{Label: "I8", ColElem: "I8"},
	{Label: "F32/qh-F16", ColElem: "F32", IdxQuant: "F16"},
	{Label: "F32/qh-I8", ColElem: "F32", IdxQuant: "I8"},
	{Label: "F32/ivf", ColElem: "F32", Method: "ivf"},
	{Label: "F32/ivfpq", ColElem: "F32", Method: "ivfpq"},
}

// pqSubspaces picks a product-quantisation subspace count M that divides the
// vector dimension: 16 sub-vectors where possible, then 8, 4, 2, else 1.
func pqSubspaces(dim int) int {
	for _, m := range []int{16, 8, 4, 2} {
		if dim%m == 0 {
			return m
		}
	}
	return 1
}

// ivfParams picks a LISTS / PROBES pair from the row count: roughly 2*sqrt(rows)
// posting lists with a quarter of them probed per query, a common IVF starting
// point (nprobe in the 1-25% of nlist range). Both are clamped to at least 1 so
// tiny benchmark runs still train. Note that uniformly random unit vectors — the
// benchmark's synthetic data — are close to a worst case for a coarse
// quantiser; recall on clustered real embeddings at the same probe ratio is
// materially higher.
func ivfParams(rows int) (lists, probes int) {
	lists = int(math.Sqrt(float64(rows))) * 2
	if lists < 1 {
		lists = 1
	}
	probes = lists / 4
	if probes < 1 {
		probes = 1
	}
	return lists, probes
}

// VectorQuantOptions configures the F32-vs-F16-vs-I8 size/recall/latency
// benchmark. Encryption, WAL, and fsync stay on, matching every other official
// workload. SparseDim / SparseNNZ size the separate SPARSEVECTOR corpus
// (high ambient dimension, few non-zeros); they are independent of Dim.
type VectorQuantOptions struct {
	Dir         string
	BufferPages int
	Rows        int
	Dim         int
	SparseDim   int // SPARSEVECTOR ambient dimension; default 4096
	SparseNNZ   int // non-zeros per sparse vector; default 24
	Queries     int
	Log         func(string)
}

// VectorQuantReport is one element type's measurement. Every quantised path is
// scored against the same exact-cosine ground truth over the full-precision
// vectors, so the recall gap between F32 and F16/I8 is exactly the quantisation
// penalty (HNSW approximation is common to all three).
type VectorQuantReport struct {
	Element      string
	Rows         int
	Dim          int
	ElemBytes    int
	RawPayload   int64 // rows * per-vector encoded width — the theoretical floor
	DBBytes      int64 // nextsql.db after load + index build
	IndexBytes   int64 // nextsql.db growth across CREATE VECTOR INDEX ... USING HNSW
	BuildTime    time.Duration
	HeapBytes    int64
	QuantErr     float64 // mean L2 distance between a source vector and its round-trip
	Method       string  // "hnsw" / "ivf" / "ivfpq" / "sparse"
	IVFLists     int     // IVF/IVFPQ only: posting lists trained
	IVFProbes    int     // IVF/IVFPQ only: posting lists probed per query
	IVFSubspaces int     // IVFPQ only: product-quantisation subspaces
	SparseNNZ    int     // SPARSE only: non-zeros per vector
	Queries      int
	P50          time.Duration
	P95          time.Duration
	P99          time.Duration
	P999         time.Duration
	QPS          float64
	RecallAt10   float64
	RecallAt100  float64
	Hardware     Hardware
}

// VectorQuantSuite is a labeled quantised-vector run: one report per element type.
type VectorQuantSuite struct {
	Hardware Hardware
	Reports  []VectorQuantReport
}

// RunVectorQuant seeds the same vector set into an F32, an F16, and an I8 column,
// builds an HNSW index over each (plus F32-column variants with an F16/I8
// quantised HNSW graph and with IVF / IVF-PQ indexes), then measures a
// SPARSEVECTOR inverted index on a high-dimension, low-nnz corpus. It reports
// payload/index/database size, build time, resident heap, quantisation error,
// and NEAREST latency + recall@10/@100.
func RunVectorQuant(opt VectorQuantOptions) (*VectorQuantSuite, error) {
	if opt.Dir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "bench.RunVectorQuant", "dir is required")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 4096
	}
	if opt.Rows < 8 {
		opt.Rows = 2000
	}
	if opt.Dim < 2 {
		opt.Dim = 128
	}
	if opt.Dim > 8192 {
		opt.Dim = 8192
	}
	if opt.SparseDim < 1 {
		opt.SparseDim = 4096
	}
	if opt.SparseDim > int(types.MaxSparseSQLDim) {
		opt.SparseDim = int(types.MaxSparseSQLDim)
	}
	if opt.SparseNNZ < 1 {
		opt.SparseNNZ = 24
	}
	if opt.SparseNNZ > opt.SparseDim {
		opt.SparseNNZ = opt.SparseDim
	}
	if opt.SparseNNZ > types.MaxSparseSQLNNZ {
		opt.SparseNNZ = types.MaxSparseSQLNNZ
	}
	if opt.Queries < 1 {
		opt.Queries = 64
	}

	hw := detectHardware(opt.Dir, opt.Rows, 1, opt.BufferPages)
	suite := &VectorQuantSuite{Hardware: hw}

	// Ground truth: exact cosine flat search over the full-precision source
	// vectors, shared across every element type.
	truth := make([][]float32, opt.Rows)
	cands := make([]vector.Candidate, opt.Rows)
	for i := range truth {
		truth[i] = detVec(opt.Dim, i)
		cands[i] = vector.Candidate{PK: []byte(fmt.Sprintf("v%d", i)), Vec: truth[i]}
	}

	for _, cfg := range vecQuantConfigs {
		rep, err := runOneVectorQuant(opt, hw, cfg, truth, cands)
		if err != nil {
			return suite, fmt.Errorf("bench.RunVectorQuant %s: %w", cfg.Label, err)
		}
		suite.Reports = append(suite.Reports, rep)
	}
	sparse, err := runOneSparseQuant(opt, hw)
	if err != nil {
		return suite, fmt.Errorf("bench.RunVectorQuant SPARSE: %w", err)
	}
	suite.Reports = append(suite.Reports, sparse)
	return suite, nil
}

func runOneVectorQuant(opt VectorQuantOptions, hw Hardware, cfg vecQuantConfig, truth [][]float32, cands []vector.Candidate) (VectorQuantReport, error) {
	elem := cfg.ColElem
	slug := strings.ToLower(cfg.Label)
	slug = strings.NewReplacer("/", "-", " ", "-").Replace(slug)
	dir := filepath.Join(opt.Dir, "elem-"+slug)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return VectorQuantReport{}, err
	}
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return VectorQuantReport{}, err
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		return VectorQuantReport{}, err
	}
	path := filepath.Join(dir, "nextsql.db")
	db, err := executor.Create(path, keys, opt.BufferPages)
	if err != nil {
		return VectorQuantReport{}, err
	}
	defer db.Close()
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{
		MaxInflight: 4,
		MaxQueue:    16,
		QueueWait:   2 * time.Second,
	}))
	s := db.Session()
	s.SetLimits(sloLimits())

	vqLogf(opt, "[%s] seeding %d x %d-d vectors", cfg.Label, opt.Rows, opt.Dim)
	ddl := fmt.Sprintf(`CREATE TABLE docs (id STRING PRIMARY KEY, embedding VECTOR<%s,%d>)`, elem, opt.Dim)
	if err := mustExec(s, ddl); err != nil {
		return VectorQuantReport{}, err
	}
	batch := 128
	if opt.Rows < batch {
		batch = opt.Rows
	}
	if err := insertBatches(s, opt.Rows, batch, 8192, func(start, end int) string {
		var b strings.Builder
		b.WriteString(`INSERT INTO docs (id, embedding) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			fmt.Fprintf(&b, `('v%d', %s)`, i, vecLit(truth[i]))
		}
		return b.String()
	}); err != nil {
		return VectorQuantReport{}, err
	}

	// The data file is grown in large chunks and can be sparse, so on-disk sizes
	// come from the allocator's high-water page count, not os.Stat. Pages consumed
	// by the vector payload store are already accounted for before the index build.
	beforePages := allocatedPages(db)
	method := "hnsw"
	var ivfLists, ivfProbes, ivfSubspaces int
	var ddlIdx string
	if cfg.Method == "ivf" {
		method = "ivf"
		ivfLists, ivfProbes = ivfParams(opt.Rows)
		vqLogf(opt, "[%s] building IVF index (LISTS=%d, PROBES=%d)", cfg.Label, ivfLists, ivfProbes)
		ddlIdx = fmt.Sprintf(
			`CREATE VECTOR INDEX ix_docs_emb ON docs (embedding) USING IVF WITH (LISTS = %d, PROBES = %d)`,
			ivfLists, ivfProbes)
	} else if cfg.Method == "ivfpq" {
		method = "ivfpq"
		ivfLists, ivfProbes = ivfParams(opt.Rows)
		ivfSubspaces = pqSubspaces(opt.Dim)
		vqLogf(opt, "[%s] building IVF-PQ index (LISTS=%d, PROBES=%d, SUBSPACES=%d)", cfg.Label, ivfLists, ivfProbes, ivfSubspaces)
		ddlIdx = fmt.Sprintf(
			`CREATE VECTOR INDEX ix_docs_emb ON docs (embedding) USING IVFPQ WITH (LISTS = %d, PROBES = %d, SUBSPACES = %d)`,
			ivfLists, ivfProbes, ivfSubspaces)
	} else {
		vqLogf(opt, "[%s] building HNSW index", cfg.Label)
		ddlIdx = `CREATE VECTOR INDEX ix_docs_emb ON docs (embedding) USING HNSW`
		if cfg.IdxQuant != "" {
			ddlIdx += fmt.Sprintf(` WITH (QUANTIZATION = '%s')`, cfg.IdxQuant)
		}
	}
	t0 := time.Now()
	if err := mustExec(s, ddlIdx); err != nil {
		return VectorQuantReport{}, err
	}
	buildTime := time.Since(t0)
	indexBytes := (allocatedPages(db) - beforePages) * int64(format.PhysicalPageSize)

	// Warm the SQL plan so the first measured NEAREST does not also pay compile.
	if _, err := execNearest(s, detVec(opt.Dim, -1), 10); err != nil {
		return VectorQuantReport{}, err
	}

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heap := int64(ms.HeapAlloc)

	var (
		lat     []int64
		r10sum  float64
		r100sum float64
		qn      int
	)
	for qi := 0; qi < opt.Queries; qi++ {
		// A query namespace disjoint from the row seeds, kept full precision.
		q := detVec(opt.Dim, -qi-1)
		truth10, err := vector.FlatSearch(q, vector.MetricCosine, cands, 10, 1)
		if err != nil {
			return VectorQuantReport{}, err
		}
		truth100, err := vector.FlatSearch(q, vector.MetricCosine, cands, 100, 1)
		if err != nil {
			return VectorQuantReport{}, err
		}
		start := time.Now()
		res10, err := execNearest(s, q, 10)
		lat = append(lat, time.Since(start).Nanoseconds())
		if err != nil {
			return VectorQuantReport{}, err
		}
		res100, err := execNearest(s, q, 100)
		if err != nil {
			return VectorQuantReport{}, err
		}
		r10sum += vector.RecallAt(truth10, hitsFromResult(res10), 10)
		r100sum += vector.RecallAt(truth100, hitsFromResult(res100), 100)
		qn++
	}
	p50, p95, p99, p999 := latencyPct(lat)

	dbBytes := allocatedPages(db) * int64(format.PhysicalPageSize)

	elemBytes, perVec := 4, opt.Dim*4
	switch elem {
	case "F16":
		elemBytes, perVec = 2, float16.Bytes(opt.Dim)
	case "I8":
		elemBytes, perVec = 1, int8vec.Bytes(opt.Dim)
	}

	// For a quantised-graph config the on-disk element width and quantisation
	// error that matter are the index's, not the (full-precision) column's.
	errElem := elem
	if cfg.IdxQuant != "" {
		errElem = cfg.IdxQuant
	}

	rep := VectorQuantReport{
		Element:      cfg.Label,
		Rows:         opt.Rows,
		Dim:          opt.Dim,
		ElemBytes:    elemBytes,
		RawPayload:   int64(opt.Rows) * int64(perVec),
		DBBytes:      dbBytes,
		IndexBytes:   indexBytes,
		BuildTime:    buildTime,
		HeapBytes:    heap,
		QuantErr:     meanQuantErr(errElem, truth),
		Method:       method,
		IVFLists:     ivfLists,
		IVFProbes:    ivfProbes,
		IVFSubspaces: ivfSubspaces,
		Queries:      qn,
		P50:          p50,
		P95:          p95,
		P99:          p99,
		P999:         p999,
		Hardware:     hw,
	}
	if qn > 0 {
		rep.RecallAt10 = r10sum / float64(qn)
		rep.RecallAt100 = r100sum / float64(qn)
	}
	if secs := sumNS(lat).Seconds(); secs > 0 {
		rep.QPS = float64(qn) / secs
	}
	return rep, nil
}

// runOneSparseQuant measures CREATE VECTOR INDEX … USING SPARSE on a
// high-dimension, low-nnz corpus. The ambient dimension and nnz are independent
// of the dense --vecquant-dim set: a 128-d dense vector is not a sparse
// retrieval workload. Recall is scored against exact-cosine SparseFlat over the
// same sparse source vectors (SQL NEAREST defaults to COSINE, which re-ranks
// the inverted-index inner-product candidates against full-precision payloads).
func runOneSparseQuant(opt VectorQuantOptions, hw Hardware) (VectorQuantReport, error) {
	dim, nnz := opt.SparseDim, opt.SparseNNZ
	dir := filepath.Join(opt.Dir, "elem-sparse")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return VectorQuantReport{}, err
	}
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return VectorQuantReport{}, err
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		return VectorQuantReport{}, err
	}
	path := filepath.Join(dir, "nextsql.db")
	db, err := executor.Create(path, keys, opt.BufferPages)
	if err != nil {
		return VectorQuantReport{}, err
	}
	defer db.Close()
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{
		MaxInflight: 4,
		MaxQueue:    16,
		QueueWait:   2 * time.Second,
	}))
	s := db.Session()
	s.SetLimits(sloLimits())

	truth := make([]vector.SparseVec, opt.Rows)
	cands := make([]vector.SparseCand, opt.Rows)
	var rawPayload int64
	for i := range truth {
		sv, err := detSparseVec(dim, nnz, i)
		if err != nil {
			return VectorQuantReport{}, err
		}
		truth[i] = sv
		cands[i] = vector.SparseCand{PK: []byte(fmt.Sprintf("v%d", i)), Vec: sv}
		enc, err := vector.EncodeSparse(sv)
		if err != nil {
			return VectorQuantReport{}, err
		}
		rawPayload += int64(len(enc))
	}

	vqLogf(opt, "[SPARSE] seeding %d x %d-d sparse vectors (nnz=%d)", opt.Rows, dim, nnz)
	ddl := fmt.Sprintf(`CREATE TABLE docs (id STRING PRIMARY KEY, embedding SPARSEVECTOR<%d>)`, dim)
	if err := mustExec(s, ddl); err != nil {
		return VectorQuantReport{}, err
	}
	batch := 128
	if opt.Rows < batch {
		batch = opt.Rows
	}
	if err := insertSparseBatches(s, opt.Rows, batch, 8192, dim, truth); err != nil {
		return VectorQuantReport{}, err
	}

	beforePages := allocatedPages(db)
	vqLogf(opt, "[SPARSE] building inverted index")
	t0 := time.Now()
	if err := mustExec(s, `CREATE VECTOR INDEX ix_docs_emb ON docs (embedding) USING SPARSE`); err != nil {
		return VectorQuantReport{}, err
	}
	buildTime := time.Since(t0)
	indexBytes := (allocatedPages(db) - beforePages) * int64(format.PhysicalPageSize)

	warm, err := detSparseVec(dim, nnz, -1)
	if err != nil {
		return VectorQuantReport{}, err
	}
	if _, err := execSparseNearest(s, warm, 10); err != nil {
		return VectorQuantReport{}, err
	}

	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	heap := int64(ms.HeapAlloc)

	var (
		lat     []int64
		r10sum  float64
		r100sum float64
		qn      int
	)
	for qi := 0; qi < opt.Queries; qi++ {
		q, err := detSparseVec(dim, nnz, -qi-1)
		if err != nil {
			return VectorQuantReport{}, err
		}
		truth10, err := vector.SparseFlat(q, vector.MetricCosine, cands, 10)
		if err != nil {
			return VectorQuantReport{}, err
		}
		truth100, err := vector.SparseFlat(q, vector.MetricCosine, cands, 100)
		if err != nil {
			return VectorQuantReport{}, err
		}
		start := time.Now()
		res10, err := execSparseNearest(s, q, 10)
		lat = append(lat, time.Since(start).Nanoseconds())
		if err != nil {
			return VectorQuantReport{}, err
		}
		res100, err := execSparseNearest(s, q, 100)
		if err != nil {
			return VectorQuantReport{}, err
		}
		r10sum += vector.RecallAt(truth10, hitsFromResult(res10), 10)
		r100sum += vector.RecallAt(truth100, hitsFromResult(res100), 100)
		qn++
	}
	p50, p95, p99, p999 := latencyPct(lat)
	dbBytes := allocatedPages(db) * int64(format.PhysicalPageSize)

	rep := VectorQuantReport{
		Element:    "SPARSE",
		Rows:       opt.Rows,
		Dim:        dim,
		ElemBytes:  0,
		RawPayload: rawPayload,
		DBBytes:    dbBytes,
		IndexBytes: indexBytes,
		BuildTime:  buildTime,
		HeapBytes:  heap,
		QuantErr:   0,
		Method:     "sparse",
		SparseNNZ:  nnz,
		Queries:    qn,
		P50:        p50,
		P95:        p95,
		P99:        p99,
		P999:       p999,
		Hardware:   hw,
	}
	if qn > 0 {
		rep.RecallAt10 = r10sum / float64(qn)
		rep.RecallAt100 = r100sum / float64(qn)
	}
	if secs := sumNS(lat).Seconds(); secs > 0 {
		rep.QPS = float64(qn) / secs
	}
	return rep, nil
}

func insertSparseBatches(s *executor.Session, n, batch, commitEvery, dim int, vecs []vector.SparseVec) error {
	vt, err := types.VectorSparse(uint16(dim))
	if err != nil {
		return err
	}
	if n <= 0 {
		return nil
	}
	if _, err := s.Exec(`BEGIN`); err != nil {
		return err
	}
	inTxn := 0
	for i := 0; i < n; i += batch {
		end := i + batch
		if end > n {
			end = n
		}
		var b strings.Builder
		b.WriteString(`INSERT INTO docs (id, embedding) VALUES `)
		params := make([]executor.Param, 0, (end-i)*2)
		for j := i; j < end; j++ {
			if j > i {
				b.WriteByte(',')
			}
			p := (j - i) * 2
			fmt.Fprintf(&b, `($%d, $%d)`, p+1, p+2)
			params = append(params,
				executor.Param{Value: types.StringValue(fmt.Sprintf("v%d", j))},
				executor.Param{Value: types.SparseValue(vecs[j].Indices, vecs[j].Values, vt)},
			)
		}
		if _, err := s.ExecContext(context.Background(), b.String(), params); err != nil {
			_, _ = s.Exec(`ROLLBACK`)
			return err
		}
		inTxn += end - i
		if inTxn >= commitEvery {
			if _, err := s.Exec(`COMMIT`); err != nil {
				return err
			}
			if _, err := s.Exec(`BEGIN`); err != nil {
				return err
			}
			inTxn = 0
		}
	}
	_, err = s.Exec(`COMMIT`)
	return err
}

func execSparseNearest(s *executor.Session, q vector.SparseVec, k int) (*executor.Result, error) {
	vt, err := types.VectorSparse(uint16(q.Dim))
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf(`SELECT id FROM docs NEAREST embedding TO $1 LIMIT %d`, k)
	return s.ExecContext(context.Background(), sql, []executor.Param{
		{Value: types.SparseValue(q.Indices, q.Values, vt)},
	})
}

// detSparseVec is a deterministic high-dimension sparse vector: nnz distinct
// coordinates drawn from SplitMix64(seed), with strictly-positive weights so
// inverted-index inner-product ranking stays aligned with SparseFlat. Queries
// use a negative seed namespace, matching detVec.
func detSparseVec(dim, nnz, seed int) (vector.SparseVec, error) {
	if dim < 1 || nnz < 1 {
		return vector.SparseVec{}, nerr.New(nerr.InvalidArgument, "bench.detSparseVec", "dim and nnz must be ≥ 1")
	}
	if nnz > dim {
		nnz = dim
	}
	idx := make([]uint32, 0, nnz)
	val := make([]float32, 0, nnz)
	used := make(map[uint32]struct{}, nnz)
	x := uint64(int64(seed)) + 0x9e3779b97f4a7c15
	attempts := 0
	maxAttempts := nnz*16 + 64
	for len(idx) < nnz {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		i := uint32(z % uint64(dim))
		attempts++
		if _, ok := used[i]; ok {
			if attempts > maxAttempts {
				for j := uint32(0); int(j) < dim && len(idx) < nnz; j++ {
					if _, taken := used[j]; taken {
						continue
					}
					used[j] = struct{}{}
					idx = append(idx, j)
					val = append(val, sparseWeight(z, j))
				}
				break
			}
			continue
		}
		used[i] = struct{}{}
		idx = append(idx, i)
		val = append(val, sparseWeight(z, i))
	}
	return vector.NewSparseVec(uint32(dim), idx, val)
}

func sparseWeight(z uint64, i uint32) float32 {
	x := z ^ (uint64(i) * 0x9e3779b97f4a7c15)
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	x ^= x >> 31
	return float32(float64(x>>11)/float64(uint64(1)<<53)) + 0.05
}

// meanQuantErr is the mean Euclidean distance between a source vector and its
// quantised round-trip. It is deterministic (no timing), so it gives the
// benchmark a stable ordering assertion: 0 for F32, small for F16, larger for I8.
func meanQuantErr(elem string, vecs [][]float32) float64 {
	if elem == "F32" || len(vecs) == 0 {
		return 0
	}
	var sum float64
	for _, v := range vecs {
		var rt []float32
		switch elem {
		case "F16":
			rt = float16.Quantize(v)
		case "I8":
			rt = int8vec.Quantize(v)
		default:
			return 0
		}
		var d float64
		for j := range v {
			e := float64(v[j]) - float64(rt[j])
			d += e * e
		}
		sum += math.Sqrt(d)
	}
	return sum / float64(len(vecs))
}

// allocatedPages is the allocator's high-water mark minus the current freelist —
// the number of live pages backing the database.
func allocatedPages(db *executor.DB) int64 {
	if db == nil || db.Eng == nil || db.Eng.Alloc == nil {
		return 0
	}
	live := int64(db.Eng.Alloc.Next()) - int64(db.Eng.Alloc.FreeCount())
	if live < 0 {
		return 0
	}
	return live
}

func vqLogf(opt VectorQuantOptions, format string, args ...any) {
	if opt.Log != nil {
		opt.Log(fmt.Sprintf(format, args...))
	}
}
