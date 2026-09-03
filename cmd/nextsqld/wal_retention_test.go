package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/backup"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/storage/format"
)

// testDB opens a fresh, minimal database in its own temp directory.
func testDB(t *testing.T) *executor.DB {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	env, err := crypto.NewMemoryKeyProvider(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// setupArchivedDB creates a real database, attaches a real DirArchiver, does
// a write, checkpoints, and closes it — the same shape TestPITRByTime in
// internal/backup/backup_test.go uses, which is how a WAL segment ends up
// genuinely archived: no manual archive-index construction. A checkpoint is
// what actually offers WAL segments to the archiver (docs/wal.md
// "Checkpoints" step 4) — Close alone does not.
func setupArchivedDB(t *testing.T) (archDir string) {
	t.Helper()
	dir := t.TempDir()
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dataDir, config.DataFileName)
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	env, err := crypto.CreateEnvelope(crypto.KeystorePath(dbPath), id, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, id, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	archDir = filepath.Join(dir, "walarch")
	arch, err := backup.NewDirArchiver(archDir, env)
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetArchiver(arch)
	if _, err := db.Session().Exec(`CREATE TABLE t (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().Exec(`INSERT INTO t (id) VALUES ('1')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return archDir
}

func TestWALRetentionTickAdvancesHorizonFromArchivedSegment(t *testing.T) {
	archDir := setupArchivedDB(t)

	// walRetentionTick only needs *some* *executor.DB to call
	// SetWALRetentionHorizon on — it doesn't have to be the one that
	// produced the archive.
	db := testDB(t)

	// A zero retention window: now-0 == now is always at or after the
	// archive's own timestamp (it already happened, in the past), so the
	// archived segment qualifies — something should be found and applied.
	// (retention is how long to KEEP; the most permissive window treats
	// anything already archived as prunable.)
	found, err := walRetentionTick(db, archDir, 0, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("expected an archived segment to satisfy the retention window")
	}

	// A window so wide (retention far longer than how long ago the archive
	// was produced) that nothing qualifies yet: not an error, just nothing
	// to advance to. (a large retention means "keep everything from the
	// last year", so nothing this fresh is prunable.)
	found, err = walRetentionTick(db, archDir, 365*24*time.Hour, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if found {
		t.Fatal("expected no archived segment to satisfy a much longer retention window")
	}
}

func TestWALRetentionTickNoArchiveIsNotFoundNotError(t *testing.T) {
	db := testDB(t)
	found, err := walRetentionTick(db, filepath.Join(t.TempDir(), "never-archived"), time.Hour, time.Now())
	if err != nil {
		t.Fatalf("an empty/nonexistent archive dir must not be an error: %v", err)
	}
	if found {
		t.Fatal("expected nothing to advance to")
	}
}

func TestStartWALRetentionUpdaterNoopWithoutPolicyOrArchive(t *testing.T) {
	// No archive dir, no retention configured, or a nil db: none of these
	// should start a goroutine or panic. There is nothing directly
	// observable here beyond "it returns promptly and doesn't crash" —
	// startWALRetentionUpdater's real behavior is exercised by
	// walRetentionTick's own tests above (it is a thin ticker loop around
	// that function).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	startWALRetentionUpdater(ctx, nil, "/tmp/whatever", 1000, log)
	startWALRetentionUpdater(ctx, &executor.DB{}, "", 1000, log)
	startWALRetentionUpdater(ctx, &executor.DB{}, "/tmp/whatever", 0, log)
}
