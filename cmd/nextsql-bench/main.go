package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/bench"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
	"github.com/bzync/nextsql/internal/version"
)

func main() {
	quick := flag.Bool("quick", false, "shorter benchmark time")
	slo := flag.Bool("slo", false, "official SLO suite with labeled hardware")
	sloMax := flag.Int("slo-max-rows", 25000, "largest bulk INSERT + scan/agg scale to seed (25K/100K/1M/10M/100M)")
	sloVec := flag.Int("slo-vectors", 256, "HNSW vectors for the recall+latency measurement")
	sloVecQueries := flag.Int("slo-vector-queries", 64, "queries in the HNSW recall+latency sample")
	sloBuffers := flag.Int("slo-buffer-pages", 4096, "buffer-pool pages for the SLO suite (16 KiB each; 1M-vector HNSW auto-raises to 131072 for atomic build)")
	sloNoDML := flag.Bool("slo-no-dml", false, "skip bulk UPDATE/DELETE after scan scales")
	workload := flag.String("workload", "all", "all|page|"+strings.Join(bench.Known(), "|"))
	duration := flag.Duration("duration", time.Second, "SQL workload duration")
	rows := flag.Int("rows", 128, "seeded rows per table")
	conc := flag.Int("concurrency", 1, "SQL worker sessions")
	flag.Parse()
	if *quick {
		testing.Init()
		if *duration > 200*time.Millisecond {
			*duration = 200 * time.Millisecond
		}
		if *rows > 32 {
			*rows = 32
		}
		if *sloMax > 64 {
			*sloMax = 64
		}
		if *sloVec > 32 {
			*sloVec = 32
		}
	}

	fmt.Printf("nextsql-bench %s (encryption + WAL + fsync enabled)\n", version.String)
	if *slo {
		if err := runSLOBenches(*duration, *conc, *sloMax, *sloVec, *sloVecQueries, *sloBuffers, *quick, *sloNoDML); err != nil {
			fatal(err)
		}
		return
	}
	if *workload == "all" || *workload == "page" {
		runPageBenches()
	}
	if *workload != "page" {
		if err := runSQLBenches(*workload, *duration, *rows, *conc); err != nil {
			fatal(err)
		}
	}
}

func runSLOBenches(d time.Duration, conc, maxRows, vecs, vectorQueries, bufferPages int, quick, skipDML bool) error {
	dir, err := os.MkdirTemp("", "nextsql-bench-slo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	latRows := 25000
	hyb := 256
	if quick {
		latRows = 32
		hyb = 16
	} else if maxRows < latRows {
		latRows = maxRows
	}
	if d < time.Second && !quick {
		d = 2 * time.Second
	}
	opt := bench.SLOOptions{
		Dir:           dir,
		BufferPages:   bufferPages,
		Duration:      d,
		Concurrency:   conc,
		LatencyRows:   latRows,
		MaxScanRows:   maxRows,
		HybridRows:    hyb,
		VectorRows:    vecs,
		VectorDim:     8,
		VectorQueries: vectorQueries,
		SkipBulkDML:   skipDML,
		Log: func(msg string) {
			fmt.Fprintf(os.Stderr, "nextsql-bench: %s\n", msg)
		},
	}
	if quick {
		opt.BufferPages = 64
		opt.VectorQueries = 4
	}
	suite, err := bench.RunSLO(opt)
	if err != nil {
		return err
	}
	hw := suite.Hardware
	fmt.Printf("\nSLO suite  cpu=%s  ram=%s  fs=%s  os=%s/%s  cpus=%d  buffers=%d\n",
		hw.CPU, hw.RAM, hw.Filesystem, hw.GOOS, hw.GOARCH, hw.NumCPU, hw.BufferPages)
	fmt.Printf("encryption=%s  durability=%s  conc=%d\n\n", hw.Encryption, hw.Durability, hw.Concurrency)
	fmt.Printf("%-10s %8s %10s %10s %10s %10s %8s  %s\n",
		"name", "rows", "p50", "p95", "p99", "elapsed", "met", "target")
	for _, r := range suite.Reports {
		met := "no"
		if r.Met {
			met = "yes"
		}
		fmt.Printf("%-10s %8d %10s %10s %10s %10s %8s  %s\n",
			r.Name, r.Rows,
			r.P50.Round(time.Microsecond), r.P95.Round(time.Microsecond), r.P99.Round(time.Microsecond),
			r.Elapsed.Round(time.Millisecond), met, r.Target)
		fmt.Printf("           query=%s\n           indexes=%s  cache=%s  width=%s\n", r.Query, r.Indexes, r.Cache, r.RowWidth)
		if r.TPS > 0 && (strings.HasPrefix(r.Name, "insert-") || strings.HasPrefix(r.Name, "update-") || strings.HasPrefix(r.Name, "delete-")) {
			fmt.Printf("           tps=%.0f  (bulk rows/s)\n", r.TPS)
		}
		if r.HasRecall {
			fmt.Printf("           recall@10=%.3f  recall@100=%.3f  qps=%.0f", r.RecallAt10, r.RecallAt100, r.QPS)
			if r.RAMBytes > 0 {
				fmt.Printf("  heap=%s", formatBytes(r.RAMBytes))
			}
			if r.DiskBytes > 0 {
				fmt.Printf("  db=%s", formatBytes(r.DiskBytes))
			}
			if r.IndexBytes > 0 {
				fmt.Printf("  index=%s", formatBytes(r.IndexBytes))
			}
			fmt.Println()
		}
	}
	return nil
}

func runSQLBenches(name string, d time.Duration, rows, conc int) error {
	dir, err := os.MkdirTemp("", "nextsql-bench-sql-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	opt := bench.Options{Dir: dir, BufferPages: 64, Duration: d, Concurrency: conc, Rows: rows}
	if name != "all" {
		opt.Workloads = []string{name}
	}
	reps, err := bench.Run(opt)
	if err != nil {
		return err
	}
	if len(reps) == 0 {
		return nil
	}
	hw := reps[0].Hardware
	fmt.Printf("\nSQL workloads  os=%s arch=%s cpus=%d gomaxprocs=%d  %s  %s  rows=%d conc=%d buffers=%d\n",
		hw.GOOS, hw.GOARCH, hw.NumCPU, hw.GOMAXPROCS, hw.Encryption, hw.Durability, hw.RowCount, hw.Concurrency, hw.BufferPages)
	fmt.Printf("%-10s %8s %10s %10s %10s %10s %10s %10s %8s %10s %8s\n",
		"workload", "ops", "qps", "tps", "p50", "p95", "p99", "p99.9", "allocs", "walB", "enc%")
	for _, r := range reps {
		fmt.Printf("-%-9s %8d %10.1f %10.1f %10s %10s %10s %10s %8d %10d %7.2f\n",
			r.Workload, r.Ops, r.QPS, r.TPS,
			r.P50.Round(time.Microsecond), r.P95.Round(time.Microsecond),
			r.P99.Round(time.Microsecond), r.P999.Round(time.Microsecond),
			r.Allocs, r.WALBytes, r.EncryptPct)
	}
	return nil
}

func runPageBenches() {
	run := func(name string, fn func(b *testing.B)) {
		res := testing.Benchmark(fn)
		fmt.Printf("%-18s %s\n", name, res.String())
	}

	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		fatal(err)
	}
	plain := slotted("bench")
	sealed, err := crypto.SealPage(dek, 2, 1, plain)
	if err != nil {
		fatal(err)
	}

	run("PageEncrypt", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(format.LogicalPageSize)
		for i := 0; i < b.N; i++ {
			if _, err := crypto.SealPage(dek, 2, uint64(i+1), plain); err != nil {
				b.Fatal(err)
			}
		}
	})
	run("PageDecrypt", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(format.PhysicalPageSize)
		for i := 0; i < b.N; i++ {
			if _, err := crypto.OpenPage(keys, 2, sealed); err != nil {
				b.Fatal(err)
			}
		}
	})
	run("PageEncodeDecode", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(format.LogicalPageSize)
		for i := 0; i < b.N; i++ {
			got, err := page.Parse(plain)
			if err != nil {
				b.Fatal(err)
			}
			got.Finalize()
		}
	})
	run("SlottedInsert", func(b *testing.B) {
		rec := []byte("insert-bench")
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			p := page.New(1, format.PageTypeSlotted)
			if _, err := p.Insert(rec); err != nil {
				b.Fatal(err)
			}
		}
	})
	run("SlottedLookup", func(b *testing.B) {
		p := page.New(1, format.PageTypeSlotted)
		slot, err := p.Insert([]byte("lookup-bench"))
		if err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			if _, err := p.Get(slot); err != nil {
				b.Fatal(err)
			}
		}
	})

	dir, err := os.MkdirTemp("", "nextsql-bench-")
	if err != nil {
		fatal(err)
	}
	defer os.RemoveAll(dir)
	eng, err := storage.Create(filepath.Join(dir, "nextsql.db"), keys, 4)
	if err != nil {
		fatal(err)
	}
	defer eng.Close()
	h, err := eng.NewSlotted()
	if err != nil {
		fatal(err)
	}
	if _, err := h.Page().Insert([]byte("io")); err != nil {
		fatal(err)
	}
	id := h.ID()
	raw := h.Page().CloneBytes()
	if err := h.Release(true); err != nil {
		fatal(err)
	}
	if err := eng.Sync(); err != nil {
		fatal(err)
	}

	run("PageWrite", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(format.PhysicalPageSize)
		for i := 0; i < b.N; i++ {
			if err := eng.File.WriteLogical(id, raw); err != nil {
				b.Fatal(err)
			}
		}
	})
	run("PageRead", func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(format.PhysicalPageSize)
		for i := 0; i < b.N; i++ {
			if _, err := eng.File.ReadLogical(id); err != nil {
				b.Fatal(err)
			}
		}
	})
	run("BufferHit", func(b *testing.B) {
		h, err := eng.Pin(id)
		if err != nil {
			b.Fatal(err)
		}
		if err := h.Release(false); err != nil {
			b.Fatal(err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			h, err := eng.Pin(id)
			if err != nil {
				b.Fatal(err)
			}
			if err := h.Release(false); err != nil {
				b.Fatal(err)
			}
		}
	})
	run("BufferMiss", func(b *testing.B) {
		h2, err := eng.NewSlotted()
		if err != nil {
			b.Fatal(err)
		}
		id2 := h2.ID()
		if err := h2.Release(true); err != nil {
			b.Fatal(err)
		}
		if err := eng.Sync(); err != nil {
			b.Fatal(err)
		}
		ids := [2]format.PageID{id, id2}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			h, err := eng.Pin(ids[i%2])
			if err != nil {
				b.Fatal(err)
			}
			if err := h.Release(false); err != nil {
				b.Fatal(err)
			}
		}
	})
}

func slotted(rec string) []byte {
	p := page.New(2, format.PageTypeSlotted)
	_, _ = p.Insert([]byte(rec))
	p.Finalize()
	return p.Bytes()
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "nextsql-bench: %v\n", err)
	os.Exit(1)
}

func formatBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1fGiB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1fMiB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1fKiB", float64(n)/(1<<10))
	default:
		return fmt.Sprintf("%dB", n)
	}
}
