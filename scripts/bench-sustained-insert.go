package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	nextsql "github.com/bzync/nextsql/drivers/go"
	"github.com/bzync/nextsql/internal/sql/types"
)

type RunResult struct {
	Mode            string
	Duration        time.Duration
	Concurrency     int
	TotalOps        int64
	AverageQPS      float64
	WriteBytes      int64
	BytesPerOp      float64
	WcharBytes      int64
	WalBytesWritten int64
	P50             time.Duration
	P95             time.Duration
	P99             time.Duration
	P999            time.Duration
	PerSecondOps    []int64
	PSIStart        string
	PSIEnd          string
}

func readPSI() string {
	b, err := os.ReadFile("/proc/pressure/io")
	if err != nil {
		return "n/a"
	}
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")
	return strings.Join(lines, "; ")
}

func readProcIO(pid int) (writeBytes int64, wchar int64, err error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/io", pid))
	if err != nil {
		return 0, 0, err
	}
	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		k := strings.TrimSpace(parts[0])
		v := strings.TrimSpace(parts[1])
		val, _ := strconv.ParseInt(v, 10, 64)
		if k == "write_bytes" {
			writeBytes = val
		} else if k == "wchar" {
			wchar = val
		}
	}
	return writeBytes, wchar, nil
}

func getFreePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func runSingleTest(repoDir, scratchDir, mode string, duration time.Duration, concurrency int) (*RunResult, error) {
	dataDir := filepath.Join(scratchDir, "data_"+mode)
	_ = os.RemoveAll(dataDir)
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}

	keyFile := filepath.Join(scratchDir, "bench_"+mode+".key")
	pwFile := filepath.Join(scratchDir, "bench_"+mode+".pw")
	_ = os.WriteFile(pwFile, []byte("benchpass\n"), 0o600)

	port, err := getFreePort()
	if err != nil {
		return nil, err
	}
	listenAddr := fmt.Sprintf("127.0.0.1:%d", port)

	// Initialize database using nextsql binary
	nextsqlBin := filepath.Join(repoDir, "nextsql")
	initCmd := exec.Command(nextsqlBin, "init",
		"--data-dir", dataDir,
		"--key-file", keyFile,
		"--database", "default",
		"--user", "bench",
		"--password-file", pwFile,
		"--prealloc-ahead-pages", "4096",
	)
	if out, err := initCmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("nextsql init failed: %v, out: %s", err, string(out))
	}

	// Write nextsql.conf
	confFile := filepath.Join(scratchDir, "nextsql_"+mode+".conf")
	confContent := fmt.Sprintf(`listen_addr = %s
data_dir = %s
key_file = %s
buffer_pages = 4096
wal_page_deltas = %s
log_level = error
`, listenAddr, dataDir, keyFile, mode)
	if err := os.WriteFile(confFile, []byte(confContent), 0o600); err != nil {
		return nil, err
	}

	// Start nextsqld
	nextsqldBin := filepath.Join(repoDir, "nextsqld")
	daemonLogPath := filepath.Join(scratchDir, "nextsqld_"+mode+".log")
	daemonLog, err := os.Create(daemonLogPath)
	if err != nil {
		return nil, err
	}
	defer daemonLog.Close()

	daemonCmd := exec.Command(nextsqldBin, "--config", confFile)
	daemonCmd.Stdout = daemonLog
	daemonCmd.Stderr = daemonLog
	if err := daemonCmd.Start(); err != nil {
		return nil, fmt.Errorf("nextsqld start failed: %v", err)
	}
	pid := daemonCmd.Process.Pid
	defer func() {
		_ = daemonCmd.Process.Signal(syscall.SIGTERM)
		_, _ = daemonCmd.Process.Wait()
	}()

	// Wait for listening
	var ready bool
	for i := 0; i < 50; i++ {
		c, err := net.DialTimeout("tcp", listenAddr, 100*time.Millisecond)
		if err == nil {
			c.Close()
			ready = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !ready {
		logContent, _ := os.ReadFile(daemonLogPath)
		return nil, fmt.Errorf("nextsqld failed to listen on %s; log: %s", listenAddr, string(logContent))
	}

	// Open admin connection and create table
	connCfg := nextsql.Config{
		Address:       listenAddr,
		Database:      "default",
		User:          "bench",
		Password:      "benchpass",
		InsecureNoTLS: true,
	}
	initConn, err := nextsql.Open(connCfg)
	if err != nil {
		return nil, fmt.Errorf("connect to server: %v", err)
	}
	createTableSQL := `CREATE TABLE perf (id INT64 PRIMARY KEY, k INT64, v STRING)`
	if _, err := initConn.Exec(context.Background(), createTableSQL); err != nil {
		initConn.Close()
		return nil, fmt.Errorf("create table: %v", err)
	}
	initConn.Close()

	// Measure initial proc IO
	wbStart, wcStart, err := readProcIO(pid)
	if err != nil {
		return nil, fmt.Errorf("readProcIO start: %v", err)
	}

	// Open worker connections
	conns := make([]*nextsql.Conn, concurrency)
	for i := 0; i < concurrency; i++ {
		c, err := nextsql.Open(connCfg)
		if err != nil {
			for j := 0; j < i; j++ {
				conns[j].Close()
			}
			return nil, fmt.Errorf("open worker connection %d: %v", i, err)
		}
		conns[i] = c
	}
	defer func() {
		for _, c := range conns {
			c.Close()
		}
	}()

	var totalOps int64
	var idCounter int64
	var latenciesMu sync.Mutex
	var latencies []time.Duration

	ctx, cancel := context.WithTimeout(context.Background(), duration)
	defer cancel()

	var wg sync.WaitGroup
	startWall := time.Now()

	// Time-series per-second bucket tracking
	seconds := int(math.Ceil(duration.Seconds()))
	perSecond := make([]int64, seconds)
	stopTicker := make(chan struct{})

	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		sec := 0
		var lastOps int64
		for {
			select {
			case <-stopTicker:
				return
			case <-ticker.C:
				current := atomic.LoadInt64(&totalOps)
				diff := current - lastOps
				lastOps = current
				if sec < len(perSecond) {
					perSecond[sec] = diff
					sec++
				}
			}
		}
	}()

	psiStart := readPSI()

	// Launch workers
	for w := 0; w < concurrency; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			conn := conns[workerID]
			localLats := make([]time.Duration, 0, 4096)
			payload := "sample_string_value_64_bytes_padding_1234567890_abcdefghijklmnopqrstuvwxyz"
			sql := "INSERT INTO perf (id, k, v) VALUES ($1, $2, $3)"

			for {
				if ctx.Err() != nil {
					break
				}
				id := atomic.AddInt64(&idCounter, 1)
				kVal := id % 1024

				t0 := time.Now()
				_, err := conn.Exec(ctx, sql, types.Int64Value(id), types.Int64Value(kVal), types.StringValue(payload))
				if err != nil {
					if ctx.Err() != nil {
						break
					}
					// Ignore unexpected error or break
					continue
				}
				lat := time.Since(t0)
				atomic.AddInt64(&totalOps, 1)
				if len(localLats) < 50000 {
					localLats = append(localLats, lat)
				}
			}

			latenciesMu.Lock()
			latencies = append(latencies, localLats...)
			latenciesMu.Unlock()
		}(w)
	}

	wg.Wait()
	close(stopTicker)
	elapsed := time.Since(startWall)
	psiEnd := readPSI()

	var walBytes int64
	if len(conns) > 0 {
		ctxQuery, cancelQuery := context.WithTimeout(context.Background(), 2*time.Second)
		if res, err := conns[0].Exec(ctxQuery, "SELECT value FROM system.metrics WHERE name = 'wal_bytes_written'"); err == nil && len(res.Rows) > 0 {
			if len(res.Rows[0]) > 0 {
				walBytes, _ = strconv.ParseInt(res.Rows[0][0].Str, 10, 64)
			}
		}
		cancelQuery()
	}

	// Measure final proc IO
	wbEnd, wcEnd, err := readProcIO(pid)
	if err != nil {
		return nil, fmt.Errorf("readProcIO end: %v", err)
	}

	// Calculate statistics
	ops := atomic.LoadInt64(&totalOps)
	wbDiff := wbEnd - wbStart
	wcDiff := wcEnd - wcStart
	bytesPerOp := float64(0)
	if ops > 0 {
		bytesPerOp = float64(wbDiff) / float64(ops)
	}

	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	var p50, p95, p99, p999 time.Duration
	if n := len(latencies); n > 0 {
		p50 = latencies[int(float64(n)*0.50)]
		p95 = latencies[int(float64(n)*0.95)]
		p99 = latencies[int(float64(n)*0.99)]
		p999 = latencies[int(float64(n)*0.999)]
	}

	return &RunResult{
		Mode:            mode,
		Duration:        elapsed,
		Concurrency:     concurrency,
		TotalOps:        ops,
		AverageQPS:      float64(ops) / elapsed.Seconds(),
		WriteBytes:      wbDiff,
		BytesPerOp:      bytesPerOp,
		WcharBytes:      wcDiff,
		WalBytesWritten: walBytes,
		P50:             p50,
		P95:             p95,
		P99:             p99,
		P999:            p999,
		PerSecondOps:    perSecond,
		PSIStart:        psiStart,
		PSIEnd:          psiEnd,
	}, nil
}

func main() {
	dur := flag.Duration("duration", 20*time.Second, "duration per run")
	conc := flag.Int("concurrency", 64, "number of concurrent client connections")
	settle := flag.Duration("settle", 5*time.Second, "settle gap between alternating runs")
	flag.Parse()

	repoDir, err := filepath.Abs(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "abs dir: %v\n", err)
		os.Exit(1)
	}
	scratchDir := filepath.Join(repoDir, ".bench_sustained")
	_ = os.RemoveAll(scratchDir)
	_ = os.MkdirAll(scratchDir, 0o755)
	defer os.RemoveAll(scratchDir)

	fmt.Printf("=== Sustained Insert Throughput Characterization ===\n")
	fmt.Printf("Repository: %s\n", repoDir)
	fmt.Printf("Scratch on ext4: %s\n", scratchDir)
	fmt.Printf("Duration: %v per run, Concurrency: %d, Settle gap: %v\n\n", *dur, *conc, *settle)

	// Alternating order: auto -> off -> off -> auto
	sequence := []string{"auto", "off", "off", "auto"}
	var results []*RunResult

	for idx, mode := range sequence {
		fmt.Printf("[%d/%d] Starting run: wal_page_deltas = %s (duration %v, conc %d)...\n",
			idx+1, len(sequence), mode, *dur, *conc)
		res, err := runSingleTest(repoDir, scratchDir, mode, *dur, *conc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Run failed: %v\n", err)
			os.Exit(1)
		}
		results = append(results, res)
		fmt.Printf("    Completed: %d ops, %.1f ops/s, %.1f bytes/insert (proc I/O), WAL logged: %d bytes (%.1f B/op), P50=%v, P99=%v\n",
			res.TotalOps, res.AverageQPS, res.BytesPerOp, res.WalBytesWritten, float64(res.WalBytesWritten)/float64(res.TotalOps), res.P50, res.P99)
		fmt.Printf("    Per-second ops: %v\n", res.PerSecondOps)
		fmt.Printf("    Host I/O pressure: [%s] -> [%s]\n", res.PSIStart, res.PSIEnd)

		if idx < len(sequence)-1 {
			fmt.Printf("    Syncing filesystem and settling for %v...\n", *settle)
			_ = exec.Command("sync").Run()
			time.Sleep(*settle)
			_ = exec.Command("sync").Run()
		}
	}

	fmt.Printf("\n=== Summary Table ===\n")
	fmt.Printf("%-6s %-6s %10s %10s %14s %14s %12s %10s %10s\n",
		"Run", "Mode", "Total Ops", "Avg QPS", "Bytes/Insert", "WAL B/Insert", "Total Write", "P50", "P99")
	for i, r := range results {
		walPerOp := float64(0)
		if r.TotalOps > 0 {
			walPerOp = float64(r.WalBytesWritten) / float64(r.TotalOps)
		}
		fmt.Printf("#%-5d %-6s %10d %10.1f %14.1f %14.1f %12s %10v %10v\n",
			i+1, r.Mode, r.TotalOps, r.AverageQPS, r.BytesPerOp, walPerOp,
			fmt.Sprintf("%.1f MiB", float64(r.WriteBytes)/(1024*1024)), r.P50, r.P99)
	}

	// Compare averages
	var autoOps, autoBytes, autoQPS float64
	var offOps, offBytes, offQPS float64
	var autoCount, offCount float64

	for _, r := range results {
		if r.Mode == "auto" {
			autoOps += float64(r.TotalOps)
			autoBytes += r.BytesPerOp
			autoQPS += r.AverageQPS
			autoCount++
		} else {
			offOps += float64(r.TotalOps)
			offBytes += r.BytesPerOp
			offQPS += r.AverageQPS
			offCount++
		}
	}

	fmt.Printf("\n=== Comparison ===\n")
	fmt.Printf("wal_page_deltas=auto (page deltas):\n")
	fmt.Printf("  Average QPS:          %.1f ops/s\n", autoQPS/autoCount)
	fmt.Printf("  Average Bytes/Insert: %.1f bytes\n", autoBytes/autoCount)
	fmt.Printf("wal_page_deltas=off (full images):\n")
	fmt.Printf("  Average QPS:          %.1f ops/s\n", offQPS/offCount)
	fmt.Printf("  Average Bytes/Insert: %.1f bytes\n", offBytes/offCount)
	fmt.Printf("Write Amplification Reduction: %.2fx\n", (offBytes/offCount)/(autoBytes/autoCount))
}
