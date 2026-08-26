package integration

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
)

func TestSQLCatalogAndDataSurviveRestart(t *testing.T) {
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
	db, err := executor.Create(path, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE items (id UUID PRIMARY KEY DEFAULT UUID(), sku STRING NOT NULL, qty DECIMAL(10,0))`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO items (sku, qty) VALUES ('A-1', 3), ('B-2', 9)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = executor.Open(path, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	s = db.Session()
	res, err := s.Exec(`SELECT sku, qty FROM items WHERE sku = 'B-2'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != "B-2" {
		t.Fatalf("%+v", res.Rows)
	}
}
