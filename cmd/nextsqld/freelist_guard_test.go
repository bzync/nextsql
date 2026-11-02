package main

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
)

func TestRefuseDamagedFreelist(t *testing.T) {
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Create(filepath.Join(t.TempDir(), "nextsql.db"), keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := refuseDamagedFreelist(db); err != nil {
		t.Fatalf("healthy database: %v", err)
	}
	live, _ := db.Eng.PrimaryTree()
	if err := db.Eng.Alloc.Free(live); err != nil {
		t.Fatal(err)
	}
	if err := refuseDamagedFreelist(db); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("damaged database: %v", err)
	}
}
