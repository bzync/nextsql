package bench

import (
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
)

// partitionBuckets is the fixed fan-out for the partition benchmark: eight
// single-value RANGE bands over a STRING key, one bucket per band.
const partitionBuckets = 8

// PartitionOptions configures the partition-pruning benchmark. Encryption, WAL,
// and fsync stay on, matching every other official workload.
type PartitionOptions struct {
	Dir         string
	BufferPages int
	Duration    time.Duration
	Rows        int // rows seeded into each of the partitioned and flat tables
}

// PartitionReport is one paired measurement. The partitioned and unpartitioned
// runs of the same logical query share a Pair label so the pruning benefit is
// legible in the printed table.
type PartitionReport struct {
	Name       string
	Pair       string
	Query      string
	Layout     string
	Partitions string // "1/8 (pruned)", "8/8", or "n/a"
	Rows       int
	Ops        int64
	Elapsed    time.Duration
	P50        time.Duration
	P95        time.Duration
	P99        time.Duration
	P999       time.Duration
	QPS        float64
	TPS        float64
	Speedup    float64 // flat P50 / partitioned P50, set on the partitioned row
	Hardware   Hardware
}

// PartitionSuite is a labeled partition-pruning run.
type PartitionSuite struct {
	Hardware Hardware
	Reports  []PartitionReport
}

// RunPartition measures the effect of physical RANGE partitioning against an
// unpartitioned table holding the same rows. It reports a pruned single-bucket
// COUNT, a pruned single-bucket SUM over the heap, an unpruned full SUM
// (partitioning overhead check), and routed vs plain INSERT.
func RunPartition(opt PartitionOptions) (*PartitionSuite, error) {
	if opt.Dir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "bench.RunPartition", "dir is required")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 512
	}
	if opt.Duration <= 0 {
		opt.Duration = time.Second
	}
	if opt.Rows < partitionBuckets {
		opt.Rows = 20_000
	}
	// Keep the seeded row count an exact multiple of the fan-out so every band
	// holds the same number of rows and the point-lookup keys always resolve.
	opt.Rows -= opt.Rows % partitionBuckets

	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		return nil, err
	}
	db, err := executor.Create(filepath.Join(opt.Dir, "nextsql.db"), keys, opt.BufferPages)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	db.SetAdmission(scheduler.NewAdmission(scheduler.AdmissionConfig{
		MaxInflight: 4,
		MaxQueue:    16,
		QueueWait:   2 * time.Second,
	}))

	hw := detectHardware(opt.Dir, opt.Rows, 1, opt.BufferPages)
	s := db.Session()
	s.SetLimits(sloLimits())
	if err := seedPartitionBench(s, opt.Rows); err != nil {
		return nil, err
	}

	partLayout := fmt.Sprintf("PARTITION BY RANGE (bucket), %d single-value bands, PRIMARY KEY (bucket, id)", partitionBuckets)
	flatLayout := "unpartitioned, PRIMARY KEY (id), bucket a plain column"

	suite := &PartitionSuite{Hardware: hw}

	// 1. Single-bucket scan: the predicate pins only the partition key. The
	//    partitioned plan prunes to one band and scans a heap holding 1/N of the
	//    rows; the flat plan walks the secondary index over every matching row.
	if err := suite.readPair(db, opt, hw, "bucket-scan", "single-bucket COUNT(*)",
		`SELECT COUNT(*) FROM part_kv WHERE bucket = <b>`, partLayout,
		partitionSpan(s, `SELECT COUNT(*) FROM part_kv WHERE bucket = '3'`),
		`SELECT COUNT(*) FROM flat_kv WHERE bucket = <b>`, flatLayout,
		func(sess *executor.Session, i int) error {
			_, err := sess.Exec(fmt.Sprintf(`SELECT COUNT(*) FROM part_kv WHERE bucket = '%d'`, i%partitionBuckets))
			return err
		},
		func(sess *executor.Session, i int) error {
			_, err := sess.Exec(fmt.Sprintf(`SELECT COUNT(*) FROM flat_kv WHERE bucket = '%d'`, i%partitionBuckets))
			return err
		}); err != nil {
		return suite, err
	}

	// 2. Single-bucket range: same pruning, but the projection forces a heap
	//    walk rather than a metadata count.
	if err := suite.readPair(db, opt, hw, "bucket-agg", "single-bucket SUM over heap",
		`SELECT SUM(n) FROM part_kv WHERE bucket = <b>`, partLayout,
		partitionSpan(s, `SELECT SUM(n) FROM part_kv WHERE bucket = '3'`),
		`SELECT SUM(n) FROM flat_kv WHERE bucket = <b>`, flatLayout,
		func(sess *executor.Session, i int) error {
			_, err := sess.Exec(fmt.Sprintf(`SELECT SUM(n) FROM part_kv WHERE bucket = '%d'`, i%partitionBuckets))
			return err
		},
		func(sess *executor.Session, i int) error {
			_, err := sess.Exec(fmt.Sprintf(`SELECT SUM(n) FROM flat_kv WHERE bucket = '%d'`, i%partitionBuckets))
			return err
		}); err != nil {
		return suite, err
	}

	// 3. Full aggregate: no partition-key predicate, every band is visited. This
	//    is the partitioning overhead check — it must stay close to the flat scan.
	if err := suite.readPair(db, opt, hw, "full-agg", "unpruned SUM over heap",
		`SELECT SUM(n) FROM part_kv`, partLayout, partitionSpan(s, `SELECT SUM(n) FROM part_kv`),
		`SELECT SUM(n) FROM flat_kv`, flatLayout,
		func(sess *executor.Session, _ int) error {
			_, err := sess.Exec(`SELECT SUM(n) FROM part_kv`)
			return err
		},
		func(sess *executor.Session, _ int) error {
			_, err := sess.Exec(`SELECT SUM(n) FROM flat_kv`)
			return err
		}); err != nil {
		return suite, err
	}

	// 4. Routed INSERT: the partitioned write routes to one band's heap and
	//    maintains that band's local index; the flat write maintains the single
	//    global secondary index.
	ropt := Options{Dir: opt.Dir, BufferPages: opt.BufferPages, Duration: opt.Duration, Concurrency: 1, Rows: opt.Rows}
	nextPart := opt.Rows
	nextFlat := opt.Rows
	pRep, err := measure(db, ropt, hw, "insert-part", func(sess *executor.Session, _ int) error {
		nextPart++
		_, err := sess.Exec(fmt.Sprintf(`INSERT INTO part_kv (bucket, id, n, note) VALUES ('%d', 'w%d', %d, 'x')`, nextPart%partitionBuckets, nextPart, nextPart))
		return err
	})
	if err != nil {
		return suite, err
	}
	fRep, err := measure(db, ropt, hw, "insert-flat", func(sess *executor.Session, _ int) error {
		nextFlat++
		_, err := sess.Exec(fmt.Sprintf(`INSERT INTO flat_kv (bucket, id, n, note) VALUES ('%d', 'w%d', %d, 'x')`, nextFlat%partitionBuckets, nextFlat, nextFlat))
		return err
	})
	if err != nil {
		return suite, err
	}
	partIns := PartitionReport{
		Name: "insert-part", Pair: "routed INSERT", Query: `INSERT INTO part_kv (...) VALUES (...)`,
		Layout: partLayout, Partitions: "n/a", Rows: opt.Rows, Ops: pRep.Ops, Elapsed: pRep.Elapsed,
		P50: pRep.P50, P95: pRep.P95, P99: pRep.P99, P999: pRep.P999, QPS: pRep.QPS, TPS: pRep.QPS, Hardware: hw,
	}
	flatIns := PartitionReport{
		Name: "insert-flat", Pair: "routed INSERT", Query: `INSERT INTO flat_kv (...) VALUES (...)`,
		Layout: flatLayout, Partitions: "n/a", Rows: opt.Rows, Ops: fRep.Ops, Elapsed: fRep.Elapsed,
		P50: fRep.P50, P95: fRep.P95, P99: fRep.P99, P999: fRep.P999, QPS: fRep.QPS, TPS: fRep.QPS, Hardware: hw,
	}
	if pRep.P50 > 0 {
		partIns.Speedup = float64(fRep.P50) / float64(pRep.P50)
	}
	suite.Reports = append(suite.Reports, partIns, flatIns)

	return suite, nil
}

// readPair measures the partitioned and flat variant of one read query inside a
// long-lived read-only transaction (which bypasses the SELECT result cache, so
// every execution does real work) and appends both reports. Speedup =
// flatP50 / partitionedP50 is set on the partitioned row.
func (suite *PartitionSuite) readPair(db *executor.DB, opt PartitionOptions, hw Hardware, name, pairLabel,
	partQuery, partLayout, partSpan, flatQuery, flatLayout string, partFn, flatFn workFn) error {
	pOps, pLat, err := measureReadInTxn(db, opt.Duration, partFn)
	if err != nil {
		return err
	}
	fOps, fLat, err := measureReadInTxn(db, opt.Duration, flatFn)
	if err != nil {
		return err
	}
	pP50, pP95, pP99, pP999 := latencyPct(pLat)
	fP50, fP95, fP99, fP999 := latencyPct(fLat)
	part := PartitionReport{
		Name: name + "-part", Pair: pairLabel, Query: partQuery, Layout: partLayout, Partitions: partSpan,
		Rows: opt.Rows, Ops: pOps, Elapsed: opt.Duration, P50: pP50, P95: pP95, P99: pP99, P999: pP999, Hardware: hw,
	}
	flat := PartitionReport{
		Name: name + "-flat", Pair: pairLabel, Query: flatQuery, Layout: flatLayout, Partitions: "n/a",
		Rows: opt.Rows, Ops: fOps, Elapsed: opt.Duration, P50: fP50, P95: fP95, P99: fP99, P999: fP999, Hardware: hw,
	}
	if opt.Duration > 0 {
		part.QPS = float64(pOps) / opt.Duration.Seconds()
		flat.QPS = float64(fOps) / opt.Duration.Seconds()
	}
	if pP50 > 0 {
		part.Speedup = float64(fP50) / float64(pP50)
	}
	suite.Reports = append(suite.Reports, part, flat)
	return nil
}

// measureReadInTxn runs fn in a loop for dur inside one explicit read-only
// transaction and returns the op count and per-op latencies in nanoseconds.
func measureReadInTxn(db *executor.DB, dur time.Duration, fn workFn) (int64, []int64, error) {
	s := db.Session()
	s.SetLimits(sloLimits())
	if _, err := s.Exec(`BEGIN`); err != nil {
		return 0, nil, err
	}
	defer func() { _, _ = s.Exec(`ROLLBACK`) }()
	var (
		ops  int64
		lat  []int64
		stop = time.Now().Add(dur)
	)
	for i := 0; time.Now().Before(stop); i++ {
		t0 := time.Now()
		if err := fn(s, i); err != nil {
			return 0, nil, err
		}
		lat = append(lat, time.Since(t0).Nanoseconds())
		ops++
	}
	return ops, lat, nil
}

// partitionSpan reports how many bands EXPLAIN keeps for sql, e.g. "1/8 (pruned)".
func partitionSpan(s *executor.Session, sql string) string {
	plan := explainPlan(s, sql)
	switch {
	case strings.Contains(plan, "partitions=[] (pruned all)"):
		return "0/" + strconv.Itoa(partitionBuckets) + " (pruned all)"
	case strings.Contains(plan, "partitions=all["):
		return strconv.Itoa(partitionBuckets) + "/" + strconv.Itoa(partitionBuckets)
	case strings.Contains(plan, "partitions=["):
		start := strings.Index(plan, "partitions=[") + len("partitions=[")
		end := strings.Index(plan[start:], "]")
		if end < 0 {
			return "?"
		}
		list := plan[start : start+end]
		n := 1
		if list != "" {
			n = strings.Count(list, ",") + 1
		}
		return fmt.Sprintf("%d/%d (pruned)", n, partitionBuckets)
	default:
		return "n/a"
	}
}

func seedPartitionBench(s *executor.Session, rows int) error {
	var band strings.Builder
	band.WriteString(`CREATE TABLE part_kv (
		bucket STRING NOT NULL,
		id STRING NOT NULL,
		n DECIMAL(12,2) NOT NULL,
		note TEXT,
		PRIMARY KEY (bucket, id)
	) PARTITION BY RANGE (bucket) (`)
	for b := 1; b < partitionBuckets; b++ {
		fmt.Fprintf(&band, "PARTITION p%d VALUES LESS THAN ('%d'), ", b-1, b)
	}
	fmt.Fprintf(&band, "PARTITION p%d VALUES LESS THAN MAXVALUE)", partitionBuckets-1)
	if err := mustExec(s, band.String()); err != nil {
		return err
	}
	// flat_kv is the common non-partitioned alternative: one table keyed by the
	// natural id, with bucket as an ordinary column. A predicate on bucket is a
	// full heap scan. (A clustered PRIMARY KEY (bucket, id) would instead give a
	// prefix range scan and close most of the read gap — see docs/partitioning.md.)
	if err := mustExec(s, `CREATE TABLE flat_kv (
		bucket STRING NOT NULL,
		id STRING NOT NULL,
		n DECIMAL(12,2) NOT NULL,
		note TEXT,
		PRIMARY KEY (id)
	)`); err != nil {
		return err
	}

	build := func(table string) func(start, end int) string {
		return func(start, end int) string {
			var b strings.Builder
			fmt.Fprintf(&b, `INSERT INTO %s (bucket, id, n, note) VALUES `, table)
			for i := start; i < end; i++ {
				if i > start {
					b.WriteByte(',')
				}
				fmt.Fprintf(&b, `('%d', 'k%05d', %d, 'row %d')`, i%partitionBuckets, i, i, i)
			}
			return b.String()
		}
	}
	if err := insertBatches(s, rows, 512, 8192, build("part_kv")); err != nil {
		return err
	}
	// flat_kv deliberately has no secondary index on bucket: the comparison is
	// physical partition pruning against a plain table, i.e. the work partitioning
	// removes without adding (and maintaining) a secondary index.
	return insertBatches(s, rows, 512, 8192, build("flat_kv"))
}
