// Package bench is the official NextSQL workload runner used by
// nextsql-bench. Encryption, WAL, fsync, checksums, and MVCC stay on.
package bench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/scheduler"
)

// Options configures an official run.
type Options struct {
	Dir         string
	BufferPages int
	Duration    time.Duration
	Concurrency int
	Rows        int
	Workloads   []string
}

// Hardware is the required context for any published number.
type Hardware struct {
	GOOS        string
	GOARCH      string
	NumCPU      int
	GOMAXPROCS  int
	CPU         string
	RAM         string
	Storage     string
	Filesystem  string
	Encryption  string
	Durability  string
	Version     string
	Phase       int
	Concurrency int
	RowCount    int
	BufferPages int
}

// Report is one workload measurement.
type Report struct {
	Workload     string
	Ops          int64
	Errors       int64
	Elapsed      time.Duration
	QPS          float64
	TPS          float64
	P50          time.Duration
	P95          time.Duration
	P99          time.Duration
	P999         time.Duration
	AllocBytes   uint64
	Allocs       uint64
	HeapAlloc    uint64
	DiskBytes    int64
	WALBytes     int64
	EncryptNs    int64
	EncryptBytes int64
	EncryptPct   float64
	Hardware     Hardware
}

func defaultWorkloads() []string {
	return []string{
		"point", "range", "insert", "update", "delete",
		"txn", "join", "agg", "json", "fulltext", "vector", "hybrid",
	}
}

// Run executes official SQL workloads against a throwaway encrypted database.
func Run(opt Options) ([]Report, error) {
	if opt.Dir == "" {
		return nil, nerr.New(nerr.InvalidArgument, "bench.Run", "dir is required")
	}
	if opt.BufferPages < 1 {
		opt.BufferPages = 64
	}
	if opt.Duration <= 0 {
		opt.Duration = time.Second
	}
	if opt.Concurrency < 1 {
		opt.Concurrency = 1
	}
	if opt.Rows < 8 {
		opt.Rows = 64
	}
	names := opt.Workloads
	if len(names) == 0 {
		names = defaultWorkloads()
	}
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

	hw := detectHardware(opt.Dir, opt.Rows, opt.Concurrency, opt.BufferPages)

	s := db.Session()
	s.SetLimits(scheduler.DefaultLimits())
	if err := seed(s, opt.Rows); err != nil {
		return nil, err
	}

	var out []Report
	for _, name := range names {
		fn, ok := workloads[name]
		if !ok {
			return out, nerr.New(nerr.InvalidArgument, "bench.Run", "unknown workload")
		}
		rep, err := measure(db, opt, hw, name, fn)
		if err != nil {
			return out, err
		}
		out = append(out, rep)
	}
	return out, nil
}

func seed(s *executor.Session, n int) error {
	stmts := []string{
		`CREATE TABLE kv (id STRING PRIMARY KEY, n DECIMAL(12,2) NOT NULL, note TEXT, meta JSON)`,
		`CREATE TABLE orders (id STRING PRIMARY KEY, k STRING NOT NULL)`,
		`CREATE TABLE lines (id STRING PRIMARY KEY, k STRING NOT NULL, qty DECIMAL(10,0) NOT NULL)`,
		`CREATE TABLE products (
			id UUID PRIMARY KEY DEFAULT UUID(),
			name STRING NOT NULL,
			price DECIMAL(12,2),
			description TEXT,
			metadata JSON,
			embedding VECTOR<F32,8>
		)`,
	}
	for _, q := range stmts {
		if _, err := s.Exec(q); err != nil {
			return err
		}
	}
	for i := 0; i < n; i++ {
		cat := "headphones"
		if i%5 == 0 {
			cat = "home"
		}
		q := fmt.Sprintf(
			`INSERT INTO kv (id, n, note, meta) VALUES ('k%d', %d, 'note %d', '{"category":"%s","i":%d}')`,
			i, i, i, cat, i,
		)
		if _, err := s.Exec(q); err != nil {
			return err
		}
		if _, err := s.Exec(fmt.Sprintf(`INSERT INTO orders (id, k) VALUES ('o%d', 'k%d')`, i, i%16)); err != nil {
			return err
		}
		if _, err := s.Exec(fmt.Sprintf(`INSERT INTO lines (id, k, qty) VALUES ('l%d', 'k%d', %d)`, i, i%16, i%7+1)); err != nil {
			return err
		}
		pq := fmt.Sprintf(
			`INSERT INTO products (name, price, description, metadata, embedding) VALUES ('p%d', %d, 'wireless noise cancelling item %d', '{"category":"%s"}', (%d, %d, 0, 0, 0, 0, 0, 0))`,
			i, 1000+i*10, i, cat, i%3, 3-i%3,
		)
		if _, err := s.Exec(pq); err != nil {
			return err
		}
	}
	for _, q := range []string{
		`CREATE INDEX ix_kv_n ON kv (n)`,
		`CREATE INDEX ix_prod_cat ON products (metadata.category)`,
		`CREATE FULLTEXT INDEX ix_prod_desc ON products (description)`,
		`CREATE VECTOR INDEX ix_prod_emb ON products (embedding) USING HNSW`,
	} {
		if _, err := s.Exec(q); err != nil {
			return err
		}
	}
	return nil
}

type workFn func(s *executor.Session, i int) error

var workloads = map[string]workFn{
	"point": func(s *executor.Session, i int) error {
		return mustExec(s, fmt.Sprintf(`SELECT n FROM kv WHERE id = 'k%d'`, i))
	},
	"range": func(s *executor.Session, i int) error {
		return mustExec(s, fmt.Sprintf(`SELECT id FROM kv WHERE n BETWEEN %d AND %d`, i%32, i%32+8))
	},
	"insert": func(s *executor.Session, i int) error {
		return mustExec(s, fmt.Sprintf(`INSERT INTO kv (id, n, note, meta) VALUES ('n%d', %d, 'x', '{"a":1}')`, i, i))
	},
	"update": func(s *executor.Session, i int) error {
		return mustExec(s, fmt.Sprintf(`UPDATE kv SET n = %d WHERE id = 'k%d'`, i, i))
	},
	"delete": func(s *executor.Session, i int) error { return deleteReinsert(s, i) },
	"txn":    func(s *executor.Session, i int) error { return txnOp(s, i) },
	"join": func(s *executor.Session, i int) error {
		return mustExec(s, `SELECT orders.id, lines.qty FROM orders JOIN lines ON orders.k = lines.k`)
	},
	"agg": func(s *executor.Session, i int) error { return mustExec(s, `SELECT COUNT(*) FROM kv`) },
	"json": func(s *executor.Session, i int) error {
		return mustExec(s, `SELECT id FROM kv WHERE meta.category = 'headphones'`)
	},
	"fulltext": func(s *executor.Session, i int) error {
		return mustExec(s, `SELECT name FROM products SEARCH description FOR 'wireless noise' LIMIT 10`)
	},
	"vector": func(s *executor.Session, i int) error {
		return mustExec(s, `SELECT name FROM products NEAREST embedding TO (1, 0, 0, 0, 0, 0, 0, 0) LIMIT 10`)
	},
	"hybrid": func(s *executor.Session, i int) error {
		return mustExec(s, `SELECT name, price FROM products WHERE metadata.category = 'headphones' AND price <= 15000 SEARCH description FOR 'wireless noise cancelling' NEAREST embedding TO (1, 0, 0, 0, 0, 0, 0, 0) LIMIT 10`)
	},
}

func mustExec(s *executor.Session, sql string) error {
	_, err := s.Exec(sql)
	return err
}

func deleteReinsert(s *executor.Session, i int) error {
	id := fmt.Sprintf("k%d", i)
	if _, err := s.Exec(fmt.Sprintf(`DELETE FROM kv WHERE id = '%s'`, id)); err != nil {
		return err
	}
	return mustExec(s, fmt.Sprintf(`INSERT INTO kv (id, n, note, meta) VALUES ('%s', %d, 'note', '{"category":"headphones"}')`, id, i))
}

func txnOp(s *executor.Session, i int) error {
	if _, err := s.Exec(`BEGIN`); err != nil {
		return err
	}
	if err := mustExec(s, fmt.Sprintf(`SELECT n FROM kv WHERE id = 'k%d'`, i)); err != nil {
		_, _ = s.Exec(`ROLLBACK`)
		return err
	}
	if err := mustExec(s, fmt.Sprintf(`UPDATE kv SET note = 'txn %d' WHERE id = 'k%d'`, i, i)); err != nil {
		_, _ = s.Exec(`ROLLBACK`)
		return err
	}
	_, err := s.Exec(`COMMIT`)
	return err
}

func measure(db *executor.DB, opt Options, hw Hardware, name string, fn workFn) (Report, error) {
	beforeDisk := dirSize(opt.Dir)
	beforeWAL := int64(0)
	if db.Eng != nil && db.Eng.WAL != nil {
		beforeWAL = db.Eng.WAL.BytesWritten()
	}
	crypto0 := metrics.Default().Snapshot()
	var mem0 runtime.MemStats
	runtime.ReadMemStats(&mem0)

	var (
		ops    atomic.Int64
		errs   atomic.Int64
		mu     sync.Mutex
		lat    []int64
		stop   = time.Now().Add(opt.Duration)
		nextID atomic.Int64
	)
	nextID.Store(int64(opt.Rows))

	ctx := context.Background()
	var wg sync.WaitGroup
	for w := 0; w < opt.Concurrency; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s := db.Session()
			s.SetLimits(scheduler.DefaultLimits())
			for time.Now().Before(stop) {
				i := int(nextID.Add(1))
				if name == "point" || name == "range" || name == "update" || name == "delete" || name == "txn" {
					i = i % opt.Rows
				}
				t0 := time.Now()
				err := fn(s, i)
				d := time.Since(t0)
				if err != nil {
					errs.Add(1)
					continue
				}
				ops.Add(1)
				mu.Lock()
				lat = append(lat, d.Nanoseconds())
				mu.Unlock()
				if ctx.Err() != nil {
					return
				}
			}
		}()
	}
	wg.Wait()

	elapsed := opt.Duration
	if elapsed <= 0 {
		elapsed = time.Nanosecond
	}
	var mem1 runtime.MemStats
	runtime.ReadMemStats(&mem1)
	crypto1 := metrics.Default().Snapshot()
	afterWAL := beforeWAL
	if db.Eng != nil && db.Eng.WAL != nil {
		afterWAL = db.Eng.WAL.BytesWritten()
	}
	encNs := (crypto1.SealNs + crypto1.OpenNs) - (crypto0.SealNs + crypto0.OpenNs)
	encB := (crypto1.SealBytes + crypto1.OpenBytes) - (crypto0.SealBytes + crypto0.OpenBytes)
	rep := Report{
		Workload:     name,
		Ops:          ops.Load(),
		Errors:       errs.Load(),
		Elapsed:      elapsed,
		AllocBytes:   mem1.TotalAlloc - mem0.TotalAlloc,
		Allocs:       mem1.Mallocs - mem0.Mallocs,
		HeapAlloc:    mem1.HeapAlloc,
		DiskBytes:    dirSize(opt.Dir) - beforeDisk,
		WALBytes:     afterWAL - beforeWAL,
		EncryptNs:    encNs,
		EncryptBytes: encB,
		Hardware:     hw,
	}
	if elapsed.Seconds() > 0 {
		rep.QPS = float64(rep.Ops) / elapsed.Seconds()
		if name == "txn" || name == "insert" || name == "update" || name == "delete" {
			rep.TPS = rep.QPS
		}
		if encNs > 0 {
			rep.EncryptPct = 100 * float64(encNs) / float64(elapsed.Nanoseconds())
		}
	}
	rep.P50, rep.P95, rep.P99, rep.P999 = latencyPct(lat)
	return rep, nil
}

func latencyPct(ns []int64) (p50, p95, p99, p999 time.Duration) {
	if len(ns) == 0 {
		return 0, 0, 0, 0
	}
	sort.Slice(ns, func(i, j int) bool { return ns[i] < ns[j] })
	pick := func(p float64) time.Duration {
		idx := int(float64(len(ns)-1) * p)
		if idx < 0 {
			idx = 0
		}
		if idx >= len(ns) {
			idx = len(ns) - 1
		}
		return time.Duration(ns[idx])
	}
	return pick(0.50), pick(0.95), pick(0.99), pick(0.999)
}

func dirSize(root string) int64 {
	var n int64
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err == nil && info != nil && !info.IsDir() {
			n += info.Size()
		}
		return nil
	})
	return n
}

// Known lists official SQL workload names.
func Known() []string { return append([]string(nil), defaultWorkloads()...) }
