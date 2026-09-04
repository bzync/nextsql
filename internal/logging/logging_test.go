package logging

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestRingEvictsOldestAndOrders(t *testing.T) {
	r := NewRing(3)
	if got := r.Snapshot(0); len(got) != 0 {
		t.Fatalf("empty ring snapshot = %d rows, want 0", len(got))
	}
	for i := 0; i < 5; i++ {
		r.add(Record{Msg: string(rune('a' + i))})
	}
	got := r.Snapshot(0)
	if len(got) != 3 {
		t.Fatalf("snapshot len = %d, want 3 (capacity)", len(got))
	}
	// Oldest first, and only the newest 3 survived (c, d, e).
	if got[0].Msg != "c" || got[1].Msg != "d" || got[2].Msg != "e" {
		t.Fatalf("snapshot order = %q/%q/%q, want c/d/e", got[0].Msg, got[1].Msg, got[2].Msg)
	}
	// Seq is monotonic and assigned by the ring.
	if got[0].Seq != 3 || got[2].Seq != 5 {
		t.Fatalf("seqs = %d..%d, want 3..5", got[0].Seq, got[2].Seq)
	}
	// max caps to the newest N, still oldest-first.
	last2 := r.Snapshot(2)
	if len(last2) != 2 || last2[0].Msg != "d" || last2[1].Msg != "e" {
		t.Fatalf("Snapshot(2) = %v, want d/e", last2)
	}
}

func TestNewWithRingMirrorsAndKeepsStderr(t *testing.T) {
	var buf bytes.Buffer
	log, ring := NewWithRing("info", &buf)

	log.Info("server listening", "addr", "127.0.0.1:7210", "tls", false)
	log.With("component", "wal").Warn("segment rotate slow", "ms", 42)
	log.Debug("suppressed at info level")

	// stderr output is still real JSON, unchanged from New's handler.
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("stderr lines = %d, want 2 (debug suppressed): %q", len(lines), buf.String())
	}
	var first map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("stderr line 0 not JSON: %v", err)
	}
	if first["msg"] != "server listening" {
		t.Fatalf("stderr msg = %v", first["msg"])
	}

	// The ring mirrored the same two records (debug not enabled).
	recs := ring.Snapshot(0)
	if len(recs) != 2 {
		t.Fatalf("ring len = %d, want 2", len(recs))
	}
	if recs[0].Msg != "server listening" || recs[0].Level != "INFO" {
		t.Fatalf("ring[0] = %+v", recs[0])
	}
	if !strings.Contains(recs[0].Attrs, "addr=127.0.0.1:7210") || !strings.Contains(recs[0].Attrs, "tls=false") {
		t.Fatalf("ring[0].Attrs = %q, want addr + tls", recs[0].Attrs)
	}
	// WithAttrs-accumulated attr is present alongside the call-site attr.
	if !strings.Contains(recs[1].Attrs, "component=wal") || !strings.Contains(recs[1].Attrs, "ms=42") {
		t.Fatalf("ring[1].Attrs = %q, want component + ms", recs[1].Attrs)
	}
	if recs[1].Level != "WARN" {
		t.Fatalf("ring[1].Level = %q, want WARN", recs[1].Level)
	}
}
