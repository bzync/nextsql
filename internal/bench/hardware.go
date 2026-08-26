package bench

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/version"
)

func detectHardware(dir string, rows, conc, buffers int) Hardware {
	hw := Hardware{
		GOOS:        runtime.GOOS,
		GOARCH:      runtime.GOARCH,
		NumCPU:      runtime.NumCPU(),
		GOMAXPROCS:  runtime.GOMAXPROCS(0),
		CPU:         cpuModel(),
		RAM:         ramString(),
		Storage:     dir,
		Filesystem:  filesystemOf(dir),
		Encryption:  "AES-256-GCM on",
		Durability:  "WAL + fsync on",
		Version:     version.String,
		Phase:       version.Phase,
		Concurrency: conc,
		RowCount:    rows,
		BufferPages: buffers,
	}
	return hw
}

func cpuModel() string {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return runtime.GOARCH
	}
	for _, line := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(line, "model name") {
			_, rest, ok := strings.Cut(line, ":")
			if ok {
				return strings.TrimSpace(rest)
			}
		}
	}
	return runtime.GOARCH
}

func ramString() string {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return "unknown"
	}
	for _, line := range strings.Split(string(b), "\n") {
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			break
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			break
		}
		return formatBytes(kb * 1024)
	}
	return "unknown"
}

func formatBytes(n uint64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GiB", float64(n)/float64(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MiB", float64(n)/float64(1<<20))
	default:
		return fmt.Sprintf("%d B", n)
	}
}

func filesystemOf(path string) string {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "unknown"
	}
	probe := abs
	for {
		if _, err := os.Stat(probe); err == nil {
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	data, err := os.ReadFile("/proc/self/mountinfo")
	if err != nil {
		return "unknown"
	}
	bestLen := -1
	bestFS := "unknown"
	for _, line := range strings.Split(string(data), "\n") {
		sep := strings.Index(line, " - ")
		if sep < 0 {
			continue
		}
		left := strings.Fields(line[:sep])
		right := strings.Fields(line[sep+3:])
		if len(left) < 5 || len(right) < 1 {
			continue
		}
		mp := left[4]
		if !strings.HasPrefix(probe, mp) {
			continue
		}
		if mp != "/" && !strings.HasPrefix(probe, mp+"/") && probe != mp {
			continue
		}
		if len(mp) >= bestLen {
			bestLen = len(mp)
			bestFS = right[0]
		}
	}
	return bestFS
}
