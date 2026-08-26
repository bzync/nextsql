package bench

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/vector"
)

// Official row-processing scales from PLAN.md §9.
var scanScales = []int{25_000, 100_000, 1_000_000, 10_000_000, 100_000_000}

// SLOOptions configures a published-number suite. Encryption, WAL, and fsync stay on.
type SLOOptions struct {
	Dir           string
	BufferPages   int
	Duration      time.Duration
	Concurrency   int
	LatencyRows   int
	MaxScanRows   int
	HybridRows    int
	VectorRows    int
	VectorDim     int
	VectorQueries int
	SkipBulkDML   bool
	Log           func(string)
}

// SLOReport is one official target measurement.
type SLOReport struct {
	Name        string
	Query       string
	Indexes     string
	Cache       string
	RowWidth    string
	Rows        int
	Ops         int64
	Elapsed     time.Duration
	P50         time.Duration
	P95         time.Duration
	P99         time.Duration
	P999        time.Duration
	QPS         float64
	TPS         float64
	RecallAt10  float64
	RecallAt100 float64
	HasRecall   bool
	RAMBytes    int64
	DiskBytes   int64
	IndexBytes  int64
	Target      string
	Met         bool
	Hardware    Hardware
}

// SLOSuite is a labeled published-number run.
type SLOSuite struct {
	Hardware Hardware
	Reports  []SLOReport
}

// RunSLO measures official latency, scan, hybrid, and vector targets.
func RunSLO(opt SLOOptions) (*SLOSuite, error) {
	if opt.Dir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "bench.RunSLO", "dir is required")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 2048
	}
	if opt.Duration <= 0 {
		opt.Duration = 2 * time.Second
	}
	if opt.Concurrency < 1 {
		opt.Concurrency = 1
	}
	if opt.LatencyRows < 8 {
		opt.LatencyRows = 25_000
	}
	if opt.MaxScanRows < 8 {
		opt.MaxScanRows = 25_000
	}
	if opt.HybridRows < 8 {
		opt.HybridRows = 256
	}
	if opt.VectorRows < 8 {
		opt.VectorRows = 256
	}
	if opt.VectorDim < 2 {
		opt.VectorDim = 8
	}
	if opt.VectorQueries < 1 {
		opt.VectorQueries = 16
	}
	// HNSW CREATE INDEX is an atomic no-steal build: dirty index pages cannot
	// be evicted until the transaction commits. A corrected 1M-vector build
	// needs this bounded pool to retain its dirty pages. Grow a smaller caller
	// request deterministically instead of allowing a multi-hour run to fail
	// with the misleading "all frames are pinned" exhaustion error.
	opt.BufferPages = normalizeSLOBufferPages(opt.VectorRows, opt.BufferPages)

	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		return nil, err
	}
	path := filepath.Join(opt.Dir, "nextsql.db")
	db, err := executor.Create(path, keys, opt.BufferPages)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{
		MaxInflight: opt.Concurrency * 2,
		MaxQueue:    opt.Concurrency * 8,
		QueueWait:   2 * time.Second,
	}))

	hw := detectHardware(opt.Dir, opt.LatencyRows, opt.Concurrency, opt.BufferPages)
	s := db.Session()
	s.SetLimits(sloLimits())

	logf(opt, "seeding kv (%d rows)", opt.LatencyRows)
	if err := seedKV(s, opt.LatencyRows); err != nil {
		return nil, err
	}
	if _, err := s.Exec(`SELECT COUNT(*) FROM kv`); err != nil {
		return nil, err
	}

	suite := &SLOSuite{Hardware: hw}
	pointQ := `SELECT n FROM kv WHERE id = $1`
	indexQ := `SELECT id FROM kv WHERE n = $1`
	insertQ := `INSERT INTO kv (id, n) VALUES ($1, $2)`
	updateQ := `UPDATE kv SET n = $1 WHERE id = $2`
	indexPlan := explainPlan(s, `SELECT id FROM kv WHERE n = 10`)

	rep, err := measureSQL(db, opt, hw, "point", pointQ, "PRIMARY KEY (id)", "warm (heap + pk in buffer)", "STRING PK + DECIMAL(12,2)", opt.LatencyRows, func(sess *executor.Session, i int) error {
		_, err := sess.Exec(fmt.Sprintf(`SELECT n FROM kv WHERE id = 'k%d'`, i%opt.LatencyRows))
		return err
	})
	if err != nil {
		return suite, err
	}
	rep.Target = "p50<0.5ms p95<1ms p99<3ms"
	rep.Met = rep.P50 < 500*time.Microsecond && rep.P95 < time.Millisecond && rep.P99 < 3*time.Millisecond
	suite.Reports = append(suite.Reports, rep)

	rep, err = measureSQL(db, opt, hw, "index", indexQ, "ix_kv_n ON kv(n); plan="+indexPlan, "warm", "STRING PK + DECIMAL(12,2)", opt.LatencyRows, func(sess *executor.Session, i int) error {
		_, err := sess.Exec(fmt.Sprintf(`SELECT id FROM kv WHERE n = %d`, i%opt.LatencyRows))
		return err
	})
	if err != nil {
		return suite, err
	}
	rep.Target = "p50<1ms p95<3ms p99<5ms"
	rep.Met = rep.P50 < time.Millisecond && rep.P95 < 3*time.Millisecond && rep.P99 < 5*time.Millisecond
	suite.Reports = append(suite.Reports, rep)

	nextID := opt.LatencyRows
	rep, err = measureSQL(db, opt, hw, "insert", insertQ, "PRIMARY KEY (id)", "n/a (durable write)", "STRING PK + DECIMAL(12,2)", opt.LatencyRows, func(sess *executor.Session, i int) error {
		id := nextID + i
		_, err := sess.Exec(fmt.Sprintf(`INSERT INTO kv (id, n) VALUES ('n%d', %d)`, id, id))
		return err
	})
	if err != nil {
		return suite, err
	}
	rep.TPS = rep.QPS
	rep.Target = "p50<2ms p95<5ms p99<10ms"
	rep.Met = rep.P50 < 2*time.Millisecond && rep.P95 < 5*time.Millisecond && rep.P99 < 10*time.Millisecond
	suite.Reports = append(suite.Reports, rep)

	rep, err = measureSQL(db, opt, hw, "update", updateQ, "PRIMARY KEY (id)", "warm", "STRING PK + DECIMAL(12,2)", opt.LatencyRows, func(sess *executor.Session, i int) error {
		_, err := sess.Exec(fmt.Sprintf(`UPDATE kv SET n = %d WHERE id = 'k%d'`, i, i%opt.LatencyRows))
		return err
	})
	if err != nil {
		return suite, err
	}
	rep.TPS = rep.QPS
	rep.Target = "p50<2ms p95<5ms p99<10ms"
	rep.Met = rep.P50 < 2*time.Millisecond && rep.P95 < 5*time.Millisecond && rep.P99 < 10*time.Millisecond
	suite.Reports = append(suite.Reports, rep)

	var scans []int
	for _, n := range scanScales {
		if n <= opt.MaxScanRows {
			scans = append(scans, n)
		}
	}
	if len(scans) == 0 {
		scans = []int{opt.MaxScanRows}
	}
	grown := 0
	var insertElapsed time.Duration
	if err := mustExec(s, `CREATE TABLE scan (id STRING PRIMARY KEY, k STRING NOT NULL, n DECIMAL(10,0) NOT NULL)`); err != nil {
		return suite, err
	}
	if err := mustExec(s, `CREATE TABLE dim (k STRING PRIMARY KEY)`); err != nil {
		return suite, err
	}
	if err := mustExec(s, `INSERT INTO dim (k) VALUES ('a'),('b'),('c'),('d'),('e'),('f'),('g'),('h'),('i'),('j')`); err != nil {
		return suite, err
	}
	for _, n := range scans {
		logf(opt, "bulk INSERT scan to %d rows", n)
		t0 := time.Now()
		if err := seedScan(s, grown, n); err != nil {
			return suite, err
		}
		insertElapsed += time.Since(t0)
		grown = n
		rep = insertLoadReport(hw, n, insertElapsed)
		suite.Reports = append(suite.Reports, rep)
		logf(opt, "%s elapsed=%s rows=%d tps=%.0f", rep.Name, rep.Elapsed.Round(time.Millisecond), n, rep.TPS)

		cache := scanCache(n, opt.BufferPages)
		rep, err = timeOnce(s, hw, fmt.Sprintf("scan-%s", scaleName(n)), `SELECT COUNT(*) FROM scan`, "heap scan", cache, "STRING PK + STRING + DECIMAL ~40B", n, `SELECT COUNT(*) FROM scan`)
		if err != nil {
			return suite, err
		}
		rep.Target = scanTarget(n)
		rep.Met = scanMet(n, rep.Elapsed)
		suite.Reports = append(suite.Reports, rep)
		logf(opt, "%s elapsed=%s met=%v", rep.Name, rep.Elapsed.Round(time.Millisecond), rep.Met)

		rep, err = timeOnce(s, hw, fmt.Sprintf("agg-%s", scaleName(n)), `SELECT k, COUNT(*) FROM scan GROUP BY k`, "heap scan + hash agg", cache, "STRING PK + STRING + DECIMAL ~40B", n, `SELECT k, COUNT(*) FROM scan GROUP BY k`)
		if err != nil {
			return suite, err
		}
		rep.Target = scanTarget(n)
		rep.Met = scanMet(n, rep.Elapsed)
		suite.Reports = append(suite.Reports, rep)
		logf(opt, "%s elapsed=%s met=%v", rep.Name, rep.Elapsed.Round(time.Millisecond), rep.Met)

		// Keys are unpadded decimal strings, so s0..s5000 is not a 5K
		// lexical interval once keys have different digit counts. Keep both
		// bounds at the same width near the scale's midpoint to measure a true
		// 5K PK range without crossing a decimal digit boundary.
		span := n / 4
		if span > 5000 {
			span = 5000
		}
		lo := n / 2
		hi := lo + span
		rq := fmt.Sprintf(`SELECT COUNT(*) FROM scan WHERE id >= 's%012d' AND id < 's%012d'`, lo, hi)
		rangePlan := explainPlan(s, rq)
		if !strings.Contains(rangePlan, "IndexScan") {
			return suite, fmt.Errorf("range-%s: expected IndexScan, plan=%s", scaleName(n), rangePlan)
		}
		check, err := s.Exec(rq)
		if err != nil {
			return suite, err
		}
		if check == nil || len(check.Rows) != 1 || len(check.Rows[0]) != 1 || check.Rows[0][0].String() != strconv.Itoa(span) {
			return suite, fmt.Errorf("range-%s: expected count %d, got %+v", scaleName(n), span, check)
		}
		rep, err = timeOnce(s, hw, fmt.Sprintf("range-%s", scaleName(n)), rq, "pk range", cache, "STRING PK + STRING + DECIMAL ~40B", n, rq)
		if err != nil {
			return suite, err
		}
		rep.Target = scanTarget(n)
		rep.Met = scanMet(n, rep.Elapsed)
		suite.Reports = append(suite.Reports, rep)

		jq := `SELECT COUNT(*) FROM scan JOIN dim ON scan.k = dim.k`
		rep, err = timeOnce(s, hw, fmt.Sprintf("join-%s", scaleName(n)), jq, "heap + hash join dim(10)", cache, "STRING PK + STRING + DECIMAL ~40B", n, jq)
		if err != nil {
			return suite, err
		}
		rep.Target = scanTarget(n)
		rep.Met = scanMet(n, rep.Elapsed)
		suite.Reports = append(suite.Reports, rep)
		logf(opt, "range/join-%s elapsed=%s/%s", scaleName(n), suite.Reports[len(suite.Reports)-2].Elapsed.Round(time.Millisecond), rep.Elapsed.Round(time.Millisecond))
	}

	if grown > 0 && !opt.SkipBulkDML {
		logf(opt, "bulk UPDATE %d rows", grown)
		t0 := time.Now()
		if err := bulkUpdateScan(s); err != nil {
			return suite, err
		}
		rep = dmlLoadReport(hw, "update", grown, time.Since(t0),
			`UPDATE scan SET n = 0  (PK cursor, COMMIT every 8192)`)
		suite.Reports = append(suite.Reports, rep)
		logf(opt, "%s elapsed=%s rows=%d tps=%.0f", rep.Name, rep.Elapsed.Round(time.Millisecond), grown, rep.TPS)

		logf(opt, "bulk DELETE %d rows", grown)
		t0 = time.Now()
		if err := bulkDeleteScan(s); err != nil {
			return suite, err
		}
		rep = dmlLoadReport(hw, "delete", grown, time.Since(t0),
			`DELETE FROM scan  (PK cursor, COMMIT every 8192)`)
		suite.Reports = append(suite.Reports, rep)
		logf(opt, "%s elapsed=%s rows=%d tps=%.0f", rep.Name, rep.Elapsed.Round(time.Millisecond), grown, rep.TPS)
	}

	logf(opt, "seeding hybrid (%d rows)", opt.HybridRows)
	if err := seedHybrid(s, opt.HybridRows); err != nil {
		return suite, err
	}
	hq := `SELECT name, price FROM products WHERE metadata.category = 'headphones' AND price <= 15000 SEARCH description FOR 'wireless noise cancelling' NEAREST embedding TO (1, 0, 0, 0, 0, 0, 0, 0) LIMIT 10`
	rep, err = measureSQL(db, opt, hw, "hybrid", hq, "json path + fulltext + hnsw", "warm indexes", "UUID + STRING + DECIMAL + TEXT + JSON + VECTOR<F32,8>", opt.HybridRows, func(sess *executor.Session, i int) error {
		_, err := sess.Exec(hq)
		return err
	})
	if err != nil {
		return suite, err
	}
	rep.Target = "p50<50ms p95<100ms p99<250ms"
	rep.Met = rep.P50 < 50*time.Millisecond && rep.P95 < 100*time.Millisecond && rep.P99 < 250*time.Millisecond
	suite.Reports = append(suite.Reports, rep)

	logf(opt, "seeding vectors (%d x %d-d)", opt.VectorRows, opt.VectorDim)
	vecs, indexBytes, err := seedVectors(s, path, opt.VectorRows, opt.VectorDim, opt)
	if err != nil {
		return suite, err
	}
	logf(opt, "HNSW index built (%d bytes)", indexBytes)
	vrep, err := measureVector(s, hw, opt, vecs)
	if err != nil {
		return suite, err
	}
	vrep.IndexBytes = indexBytes
	suite.Reports = append(suite.Reports, vrep)
	return suite, nil
}

func normalizeSLOBufferPages(vectorRows, requested int) int {
	if vectorRows >= 1_000_000 && requested < 131_072 {
		return 131_072
	}
	return requested
}

func explainPlan(s *executor.Session, sql string) string {
	res, err := s.Exec("EXPLAIN " + sql)
	if err != nil || res == nil || len(res.Rows) == 0 {
		return "unknown"
	}
	var b strings.Builder
	for i, row := range res.Rows {
		if len(row) == 0 {
			continue
		}
		if i > 0 {
			b.WriteByte('|')
		}
		b.WriteString(row[0].String())
	}
	return b.String()
}

func sloLimits() scheduler.Limits {
	l := scheduler.DefaultLimits()
	l.Time = 8 * time.Hour
	l.Memory = 512 << 20
	l.ResultBytes = 256 << 20
	l.IO = 256 << 30
	l.BatchSize = scheduler.Batch4096
	if l.Workers < 4 {
		l.Workers = 4
	}
	return l
}

func bulkUpdateScan(s *executor.Session) error {
	_, err := s.BulkSetDecimal("scan", "n", "0")
	return err
}

func bulkDeleteScan(s *executor.Session) error {
	_, err := s.BulkDeleteAll("scan")
	return err
}

func logf(opt SLOOptions, format string, args ...any) {
	if opt.Log != nil {
		opt.Log(fmt.Sprintf(format, args...))
	}
}

func seedKV(s *executor.Session, n int) error {
	if err := mustExec(s, `CREATE TABLE kv (id STRING PRIMARY KEY, n DECIMAL(12,2) NOT NULL)`); err != nil {
		return err
	}
	decTy := types.Type{Kind: types.KindDecimal, Precision: 12, Scale: 2}
	if err := insertValueBatches(s, "kv", n, 256, 4096, func(start, end int) [][]types.Value {
		rows := make([][]types.Value, 0, end-start)
		for i := start; i < end; i++ {
			d, _ := types.ParseDecimal(strconv.Itoa(i))
			rows = append(rows, []types.Value{
				types.StringValue("k" + strconv.Itoa(i)),
				types.DecimalValue(d, decTy),
			})
		}
		return rows
	}); err != nil {
		return err
	}
	return mustExec(s, `CREATE INDEX ix_kv_n ON kv (n)`)
}

func seedScan(s *executor.Session, from, to int) error {
	if to <= from {
		return nil
	}
	return insertEncodedBatches(s, "scan", to-from, 4096, 524288, func(start, end int) (keys, vals [][]byte) {
		n := end - start
		keys = make([][]byte, n)
		vals = make([][]byte, n)
		var kb, vb []byte
		for i := 0; i < n; i++ {
			kb, vb = encodeBenchScan(from+start+i, kb[:0], vb[:0])
			keys[i] = append([]byte(nil), kb...)
			vals[i] = append([]byte(nil), vb...)
		}
		return keys, vals
	})
}

func insertEncodedBatches(s *executor.Session, table string, n, batch, commitEvery int, build func(start, end int) (keys, vals [][]byte)) error {
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
		keys, vals := build(i, end)
		if keys == nil {
			_, _ = s.Exec(`ROLLBACK`)
			return nerr.New(nerr.Internal, "bench.insertEncoded", "encode")
		}
		if _, err := s.InsertEncoded(table, keys, vals); err != nil {
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
	_, err := s.Exec(`COMMIT`)
	return err
}

func insertValueBatches(s *executor.Session, table string, n, batch, commitEvery int, build func(start, end int) [][]types.Value) error {
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
		if _, err := s.InsertRows(table, build(i, end)); err != nil {
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
	_, err := s.Exec(`COMMIT`)
	return err
}

func seedHybrid(s *executor.Session, n int) error {
	if err := mustExec(s, `CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2),
		description TEXT,
		metadata JSON,
		embedding VECTOR<F32,8>
	)`); err != nil {
		return err
	}
	if err := insertBatches(s, n, 32, 1024, func(start, end int) string {
		var b strings.Builder
		b.WriteString(`INSERT INTO products (name, price, description, metadata, embedding) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			cat := "headphones"
			if i%5 == 0 {
				cat = "home"
			}
			fmt.Fprintf(&b, `('p%d', %d, 'wireless noise cancelling item %d', '{"category":"%s"}', (%d, %d, 0, 0, 0, 0, 0, 0))`,
				i, 1000+i*10, i, cat, i%3, 3-i%3)
		}
		return b.String()
	}); err != nil {
		return err
	}
	for _, q := range []string{
		`CREATE INDEX ix_prod_cat ON products (metadata.category)`,
		`CREATE FULLTEXT INDEX ix_prod_desc ON products (description)`,
		`CREATE VECTOR INDEX ix_prod_emb ON products (embedding) USING HNSW`,
	} {
		if err := mustExec(s, q); err != nil {
			return err
		}
	}
	return nil
}

func seedVectors(s *executor.Session, dbPath string, n, dim int, opt SLOOptions) ([][]float32, int64, error) {
	ddl := fmt.Sprintf(`CREATE TABLE docs (id STRING PRIMARY KEY, embedding VECTOR<F32,%d>)`, dim)
	if err := mustExec(s, ddl); err != nil {
		return nil, 0, err
	}
	vecs := make([][]float32, n)
	batch := 256
	commitEvery := 16384
	if n < batch {
		batch = n
	}
	logf(opt, "inserting %d vectors (batch=%d commitEvery=%d)", n, batch, commitEvery)
	if err := insertBatches(s, n, batch, commitEvery, func(start, end int) string {
		var b strings.Builder
		b.WriteString(`INSERT INTO docs (id, embedding) VALUES `)
		for i := start; i < end; i++ {
			if i > start {
				b.WriteByte(',')
			}
			v := detVec(dim, i)
			vecs[i] = v
			fmt.Fprintf(&b, `('v%d', %s)`, i, vecLit(v))
		}
		return b.String()
	}); err != nil {
		return nil, 0, err
	}
	before, err := os.Stat(dbPath)
	if err != nil {
		return nil, 0, err
	}
	logf(opt, "building HNSW index")
	if err := mustExec(s, `CREATE VECTOR INDEX ix_docs_emb ON docs (embedding) USING HNSW`); err != nil {
		return nil, 0, err
	}
	after, err := os.Stat(dbPath)
	if err != nil {
		return nil, 0, err
	}
	return vecs, after.Size() - before.Size(), nil
}

func insertBatches(s *executor.Session, n, batch, commitEvery int, build func(start, end int) string) error {
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
		if _, err := s.Exec(build(i, end)); err != nil {
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
	_, err := s.Exec(`COMMIT`)
	return err
}

func measureSQL(db *executor.DB, opt SLOOptions, hw Hardware, name, query, indexes, cache, width string, rows int, fn workFn) (SLOReport, error) {
	ropt := Options{Dir: opt.Dir, BufferPages: opt.BufferPages, Duration: opt.Duration, Concurrency: opt.Concurrency, Rows: rows}
	rep, err := measure(db, ropt, hw, name, fn)
	if err != nil {
		return SLOReport{}, err
	}
	return SLOReport{
		Name:     name,
		Query:    query,
		Indexes:  indexes,
		Cache:    cache,
		RowWidth: width,
		Rows:     rows,
		Ops:      rep.Ops,
		Elapsed:  rep.Elapsed,
		P50:      rep.P50,
		P95:      rep.P95,
		P99:      rep.P99,
		P999:     rep.P999,
		QPS:      rep.QPS,
		Hardware: hw,
	}, nil
}

func insertLoadReport(hw Hardware, rows int, elapsed time.Duration) SLOReport {
	return dmlLoadReport(hw, "insert", rows, elapsed,
		"InsertEncoded scan  batch=4096 commit=524288  WAL+fsync+encryption")
}

func dmlLoadReport(hw Hardware, kind string, rows int, elapsed time.Duration, query string) SLOReport {
	rep := SLOReport{
		Name:     fmt.Sprintf("%s-%s", kind, scaleName(rows)),
		Query:    query,
		Indexes:  "PRIMARY KEY (id)",
		Cache:    "n/a (durable bulk " + kind + ", WAL + fsync)",
		RowWidth: "STRING PK + STRING + DECIMAL ~40B",
		Rows:     rows,
		Ops:      int64(rows),
		Elapsed:  elapsed,
		P50:      elapsed,
		P95:      elapsed,
		P99:      elapsed,
		P999:     elapsed,
		Target:   "measured (bulk; no PLAN.md time SLO)",
		Met:      rows > 0 && elapsed > 0,
		Hardware: hw,
	}
	if elapsed > 0 {
		rep.TPS = float64(rows) / elapsed.Seconds()
		rep.QPS = rep.TPS
	}
	return rep
}

func timeOnce(s *executor.Session, hw Hardware, name, query, indexes, cache, width string, rows int, sql string) (SLOReport, error) {
	t0 := time.Now()
	if _, err := s.Exec(sql); err != nil {
		return SLOReport{}, err
	}
	elapsed := time.Since(t0)
	return SLOReport{
		Name:     name,
		Query:    query,
		Indexes:  indexes,
		Cache:    cache,
		RowWidth: width,
		Rows:     rows,
		Ops:      1,
		Elapsed:  elapsed,
		P50:      elapsed,
		P95:      elapsed,
		P99:      elapsed,
		P999:     elapsed,
		Hardware: hw,
	}, nil
}

func measureVector(s *executor.Session, hw Hardware, opt SLOOptions, vecs [][]float32) (SLOReport, error) {
	n := len(vecs)
	dim := opt.VectorDim
	cands := make([]vector.Candidate, n)
	for i := 0; i < n; i++ {
		cands[i] = vector.Candidate{PK: []byte(fmt.Sprintf("v%d", i)), Vec: vecs[i]}
	}
	var (
		lat     []int64
		r10sum  float64
		r100sum float64
		qcount  int
	)
	nq := opt.VectorQueries
	if nq > n {
		nq = n
	}
	// CREATE INDEX already keeps a process-local graph, but the first NEAREST
	// still pays SQL plan compile. Bind $1 so later queries reuse that plan.
	// The report labels this a warm-graph SLO, so discard the first execution.
	// A single cold sample previously became both p95 and p99 on small query
	// counts (docs/ops.md 1M baseline 36.78 ms).
	if nq > 0 {
		if _, err := execNearest(s, detVec(dim, -1), 10); err != nil {
			return SLOReport{}, err
		}
	}
	for qi := 0; qi < nq; qi++ {
		// Negative seeds use a deterministic query namespace disjoint from the
		// non-negative row seeds, avoiding the easier indexed-vector self-query.
		q := detVec(dim, -qi-1)
		t0 := time.Now()
		res, err := execNearest(s, q, 10)
		d := time.Since(t0)
		if err != nil {
			return SLOReport{}, err
		}
		lat = append(lat, d.Nanoseconds())
		truth10, err := vector.FlatSearch(q, vector.MetricCosine, cands, 10, 1)
		if err != nil {
			return SLOReport{}, err
		}
		approx10 := hitsFromResult(res)
		r10sum += vector.RecallAt(truth10, approx10, 10)
		truth100, err := vector.FlatSearch(q, vector.MetricCosine, cands, 100, 1)
		if err != nil {
			return SLOReport{}, err
		}
		res100, err := execNearest(s, q, 100)
		if err != nil {
			return SLOReport{}, err
		}
		r100sum += vector.RecallAt(truth100, hitsFromResult(res100), 100)
		qcount++
	}
	p50, p95, p99, p999 := latencyPct(lat)
	rep := SLOReport{
		Name:        "vector",
		Query:       fmt.Sprintf("NEAREST embedding TO $q LIMIT 10  (%d x %d-d HNSW)", n, dim),
		Indexes:     "ix_docs_emb HNSW",
		Cache:       "warm graph",
		RowWidth:    fmt.Sprintf("STRING PK + VECTOR<F32,%d>", dim),
		Rows:        n,
		Ops:         int64(qcount),
		Elapsed:     opt.Duration,
		P50:         p50,
		P95:         p95,
		P99:         p99,
		P999:        p999,
		RecallAt10:  r10sum / float64(qcount),
		RecallAt100: r100sum / float64(qcount),
		HasRecall:   true,
		Target:      "p50<10ms p95<25ms p99<50ms with recall@10>0",
		Hardware:    hw,
	}
	rep.Met = p50 < 10*time.Millisecond && p95 < 25*time.Millisecond && p99 < 50*time.Millisecond && rep.RecallAt10 > 0
	if qcount > 0 {
		rep.QPS = float64(qcount) / (time.Duration(sumNS(lat)).Seconds())
	}
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	rep.RAMBytes = int64(ms.HeapAlloc)
	if st, err := os.Stat(filepath.Join(opt.Dir, "nextsql.db")); err == nil {
		rep.DiskBytes = st.Size()
	}
	return rep, nil
}

func execNearest(s *executor.Session, q []float32, k int) (*executor.Result, error) {
	vt, err := types.VectorF32(uint16(len(q)))
	if err != nil {
		return nil, err
	}
	sql := fmt.Sprintf(`SELECT id FROM docs NEAREST embedding TO $1 LIMIT %d`, k)
	return s.ExecContext(context.Background(), sql, []executor.Param{{Value: types.VectorValue(q, vt)}})
}

func hitsFromResult(res *executor.Result) []vector.Hit {
	if res == nil {
		return nil
	}
	out := make([]vector.Hit, 0, len(res.Rows))
	for i, row := range res.Rows {
		if len(row) == 0 {
			continue
		}
		out = append(out, vector.Hit{PK: []byte(row[0].String()), Dist: float64(i)})
	}
	return out
}

func detVec(dim, i int) []float32 {
	v := make([]float32, dim)
	// SplitMix64 gives the SLO data set distinct, reproducible directions.
	// A sparse i%dim generator collapses a million-row, 8-d data set to only
	// eight vectors and makes recall measure tie-breaking rather than ANN quality.
	x := uint64(i) + 0x9e3779b97f4a7c15
	var norm float64
	for j := range v {
		x += 0x9e3779b97f4a7c15
		z := x
		z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
		z = (z ^ (z >> 27)) * 0x94d049bb133111eb
		z ^= z >> 31
		f := float32(float64(z>>11)/float64(uint64(1)<<53)*2 - 1)
		v[j] = f
		norm += float64(f) * float64(f)
	}
	scale := float32(1 / math.Sqrt(norm))
	for j := range v {
		v[j] *= scale
	}
	return v
}

func vecLit(v []float32) string {
	var b strings.Builder
	b.WriteByte('(')
	for i, f := range v {
		if i > 0 {
			b.WriteString(", ")
		}
		// The SQL numeric lexer intentionally accepts decimal notation only.
		// Normalized pseudo-random components can be small enough for %g to
		// choose an exponent, so force a lossless float32 decimal spelling.
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(')')
	return b.String()
}

func scaleName(n int) string {
	switch {
	case n >= 1_000_000_000:
		return "1B"
	case n >= 1_000_000:
		return fmt.Sprintf("%dM", n/1_000_000)
	case n >= 1000:
		return fmt.Sprintf("%dK", n/1000)
	default:
		return strconv.Itoa(n)
	}
}

func scanCache(rows, bufferPages int) string {
	if bufferPages < 1 {
		bufferPages = 2048
	}
	const rowBytes = 80
	fit := int64(bufferPages) * int64(format.LogicalPageSize)
	need := int64(rows) * rowBytes
	mib := bufferPages * format.LogicalPageSize / (1024 * 1024)
	if need <= fit {
		return fmt.Sprintf("working set fits in %d-page buffer (%d MiB)", bufferPages, mib)
	}
	return fmt.Sprintf("%d-page buffer (%d MiB; working set exceeds buffer)", bufferPages, mib)
}

func scanTarget(n int) string {
	switch {
	case n <= 25_000:
		return "<1s"
	case n <= 100_000:
		return "<1s"
	case n <= 1_000_000:
		return "<1s"
	case n <= 10_000_000:
		return "<5s"
	case n <= 100_000_000:
		return "<30-60s"
	default:
		return "<60s"
	}
}

func scanMet(n int, d time.Duration) bool {
	switch {
	case n <= 100_000:
		return d < time.Second
	case n <= 1_000_000:
		return d < time.Second
	case n <= 10_000_000:
		return d < 5*time.Second
	case n <= 100_000_000:
		return d < 60*time.Second
	default:
		return d < 60*time.Second
	}
}

func sumNS(ns []int64) time.Duration {
	var t int64
	for _, v := range ns {
		t += v
	}
	return time.Duration(t)
}
