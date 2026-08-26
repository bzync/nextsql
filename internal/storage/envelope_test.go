package storage

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/storage/page"
)

func TestEnvelopeRotateReencryptRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nextsql.db")
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
	eng, err := CreateWithIdentity(dbPath, id, env, 32)
	if err != nil {
		t.Fatal(err)
	}
	h, err := eng.NewSlotted()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.Page().Insert([]byte("secret-row")); err != nil {
		t.Fatal(err)
	}
	pid := h.ID()
	if err := h.Release(true); err != nil {
		t.Fatal(err)
	}
	if err := eng.Buffer.FlushAll(); err != nil {
		t.Fatal(err)
	}
	if _, err := env.RotateDomain(crypto.DomainPage); err != nil {
		t.Fatal(err)
	}
	if err := eng.File.AdoptCurrentKey(); err != nil {
		t.Fatal(err)
	}
	if err := eng.Reencrypt(); err != nil {
		t.Fatal(err)
	}
	if err := env.Retire(crypto.DomainPage, 1); err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	_ = env.Close()

	re, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), root)
	if err != nil {
		t.Fatal(err)
	}
	eng2, err := Open(dbPath, re, 32)
	if err != nil {
		t.Fatal(err)
	}
	h2, err := eng2.Buffer.Pin(pid)
	if err != nil {
		t.Fatal(err)
	}
	pg := h2.Page()
	if pg == nil {
		t.Fatal("missing page")
	}
	_, _ = page.ParseID(pg.Bytes(), pid)
	if err := h2.Release(false); err != nil {
		t.Fatal(err)
	}
	_ = eng2.Close()

	other, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), other); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("stolen keystore opened with wrong root: %v", err)
	}
}

func TestEnvelopeWrongRootCannotOpenData(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "nextsql.db")
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
	eng, err := CreateWithIdentity(dbPath, id, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	wrong, err := crypto.NewMemoryKeyProvider(other)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dbPath, wrong, 16); err == nil {
		t.Fatal("data file opened with a raw DEK that is not the page key")
	}
}
