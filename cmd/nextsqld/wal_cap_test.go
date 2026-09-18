package main

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
)

// applyOps is what carries wal_max_retained_mb from configuration into the
// engine, converting the operator-facing MiB into the bytes TrimToCap takes.
// Without this the flag parses and does nothing.
func TestApplyOpsCarriesTheWALSizeCap(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "master.key")
	if _, err := crypto.CreateKeyFile(keyPath, 1); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.LoadProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(filepath.Join(dir, "nextsql.db"), keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	cfg := config.Default()
	cfg.WalMaxRetainedMB = 512
	applyOps(db, cfg)
	if got := db.WALSizeCap(); got != 512<<20 {
		t.Fatalf("WAL size cap = %d bytes, want %d", got, 512<<20)
	}

	// The default must leave the engine's historical behaviour untouched.
	cfg.WalMaxRetainedMB = 0
	applyOps(db, cfg)
	if got := db.WALSizeCap(); got != 0 {
		t.Fatalf("default config set a cap of %d bytes", got)
	}
}
