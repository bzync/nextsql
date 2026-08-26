package integration

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/storage/btree"
)

func TestRestartPersistsSlottedRecord(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keyPath := filepath.Join(dir, "master.key")
	if _, err := crypto.CreateKeyFile(keyPath, 1); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.LoadProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	e, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	h, err := e.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	id := h.ID()
	if _, err := h.Page().Insert([]byte("integration-restart")); err != nil {
		t.Fatal(err)
	}
	if err := h.Release(true); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = storage.Open(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	h, err = e.Pin(id)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Release(false)
	got, err := h.Page().Get(0)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "integration-restart" {
		t.Fatalf("got %q", got)
	}
}

func TestRestartPersistsBTree(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	keyPath := filepath.Join(dir, "master.key")
	if _, err := crypto.CreateKeyFile(keyPath, 1); err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.LoadProvider(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	e, err := storage.Create(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := btree.Create(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("pk-1"), []byte("row-1")); err != nil {
		t.Fatal(err)
	}
	if err := tr.Insert([]byte("pk-2"), []byte("row-2")); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = storage.Open(path, keys, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tr, err = btree.Open(e)
	if err != nil {
		t.Fatal(err)
	}
	got, err := tr.Lookup([]byte("pk-1"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "row-1" {
		t.Fatalf("got %q", got)
	}
	if err := tr.Delete([]byte("pk-2")); err != nil {
		t.Fatal(err)
	}
	n := 0
	if err := tr.Range(nil, nil, func(k, v []byte) error {
		n++
		if string(k) != "pk-1" || string(v) != "row-1" {
			t.Fatalf("scan %q %q", k, v)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("scan count %d", n)
	}
	if _, err := tr.Lookup([]byte("pk-2")); !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("deleted key: %v", err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
}
