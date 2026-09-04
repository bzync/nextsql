package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"
)

func levelOf(level string) slog.Level {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}

// New returns a JSON slog logger. Callers must never log keys, passwords, or tokens.
func New(level string, w io.Writer) *slog.Logger {
	if w == nil {
		w = os.Stderr
	}
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: levelOf(level)})
	return slog.New(h)
}

// DefaultRingCapacity is how many of the most recent log records the in-memory
// ring retains for system.server_log. Bounded by design: memory cost is fixed
// regardless of how long the process runs or how much it logs.
const DefaultRingCapacity = 500

// Record is one retained log line — already free of secrets by the same
// "never log keys/passwords/tokens" contract New's callers follow; the ring
// adds no redaction of its own.
type Record struct {
	Seq   uint64
	Time  time.Time
	Level string
	Msg   string
	Attrs string // remaining attributes rendered "k=v k=v", sorted-stable by emission order
}

// Ring is a fixed-capacity, oldest-evicted-first buffer of recent log records.
// Safe for concurrent Handle and Snapshot.
type Ring struct {
	mu   sync.Mutex
	buf  []Record
	next int
	n    int
	seq  uint64
}

// NewRing returns an empty Ring holding at most capacity records (min 1).
func NewRing(capacity int) *Ring {
	if capacity < 1 {
		capacity = 1
	}
	return &Ring{buf: make([]Record, capacity)}
}

func (r *Ring) add(rec Record) {
	r.mu.Lock()
	r.seq++
	rec.Seq = r.seq
	r.buf[r.next] = rec
	r.next = (r.next + 1) % len(r.buf)
	if r.n < len(r.buf) {
		r.n++
	}
	r.mu.Unlock()
}

// Snapshot returns up to max of the most recent records, oldest first. max <= 0
// or larger than what is retained returns everything retained.
func (r *Ring) Snapshot(max int) []Record {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := r.n
	if max > 0 && max < n {
		n = max
	}
	out := make([]Record, 0, n)
	// The oldest retained record sits at (next-r.n); walk forward r.n slots,
	// then keep only the last n of them.
	start := (r.next - r.n + len(r.buf)) % len(r.buf)
	skip := r.n - n
	for i := 0; i < r.n; i++ {
		if i < skip {
			continue
		}
		out = append(out, r.buf[(start+i)%len(r.buf)])
	}
	return out
}

// ringHandler mirrors every record it passes through into a Ring, then
// delegates to the wrapped JSON handler so stderr output is unchanged.
type ringHandler struct {
	inner slog.Handler
	ring  *Ring
	attrs []slog.Attr
	group string
}

func (h *ringHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *ringHandler) Handle(ctx context.Context, rec slog.Record) error {
	var b strings.Builder
	writeAttr := func(a slog.Attr) {
		if a.Equal(slog.Attr{}) {
			return
		}
		if b.Len() > 0 {
			b.WriteByte(' ')
		}
		key := a.Key
		if h.group != "" {
			key = h.group + "." + key
		}
		fmt.Fprintf(&b, "%s=%v", key, a.Value.Any())
	}
	for _, a := range h.attrs {
		writeAttr(a)
	}
	rec.Attrs(func(a slog.Attr) bool {
		writeAttr(a)
		return true
	})
	h.ring.add(Record{
		Time:  rec.Time,
		Level: rec.Level.String(),
		Msg:   rec.Message,
		Attrs: b.String(),
	})
	return h.inner.Handle(ctx, rec)
}

func (h *ringHandler) WithAttrs(as []slog.Attr) slog.Handler {
	merged := make([]slog.Attr, 0, len(h.attrs)+len(as))
	merged = append(merged, h.attrs...)
	for _, a := range as {
		if h.group != "" {
			a.Key = h.group + "." + a.Key
		}
		merged = append(merged, a)
	}
	return &ringHandler{inner: h.inner.WithAttrs(as), ring: h.ring, attrs: merged, group: h.group}
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	g := name
	if h.group != "" && name != "" {
		g = h.group + "." + name
	}
	return &ringHandler{inner: h.inner.WithGroup(name), ring: h.ring, attrs: h.attrs, group: g}
}

// NewWithRing returns a JSON slog logger whose output is unchanged from New's,
// plus a Ring retaining the most recent DefaultRingCapacity records in memory
// for system.server_log. The same "never log keys/passwords/tokens" contract
// applies — the ring stores exactly what is written, with no added redaction.
func NewWithRing(level string, w io.Writer) (*slog.Logger, *Ring) {
	if w == nil {
		w = os.Stderr
	}
	ring := NewRing(DefaultRingCapacity)
	inner := slog.NewJSONHandler(w, &slog.HandlerOptions{Level: levelOf(level)})
	return slog.New(&ringHandler{inner: inner, ring: ring}), ring
}
