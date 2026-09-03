package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
	"github.com/bzync/nextsql/internal/upgrade/compat"
)

func TestInspectMatchesCreatedDatabase(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	eng, err := storage.Create(path, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	ident := eng.Identity()
	if err := eng.Close(); err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.OK {
		t.Fatalf("report: %+v files=%+v", rep, rep.Files)
	}
	if !rep.HasIdent || rep.Identity != ident {
		t.Fatalf("identity %+v want %+v", rep.Identity, ident)
	}
	if !compat.Compatible(compat.FamilyPage, 1) || !compat.Compatible(compat.FamilyWALCtrl, 1) || !compat.Compatible(compat.FamilyUNDOCtrl, 1) {
		t.Fatal("v1 should be compatible")
	}
	var sawPage, sawWAL, sawUNDO bool
	for _, f := range rep.Files {
		if !f.Present {
			t.Fatalf("missing %s", f.Family)
		}
		if !f.Compat || !f.MagicOK || !f.Checksum {
			t.Fatalf("%+v", f)
		}
		switch f.Family {
		case compat.FamilyPage:
			sawPage = true
		case compat.FamilyWALCtrl:
			sawWAL = true
		case compat.FamilyUNDOCtrl:
			sawUNDO = true
		}
	}
	if !sawPage || !sawWAL || !sawUNDO {
		t.Fatalf("families: %+v", rep.Files)
	}
}

func TestInspectMissingDir(t *testing.T) {
	if _, err := Inspect(filepath.Join(t.TempDir(), "nope")); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("%v", err)
	}
}

func TestInspectBadMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "nextsql.db")
	if err := os.WriteFile(path, []byte("XXXX not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	rep, err := Inspect(dir)
	if err != nil {
		t.Fatal(err)
	}
	if rep.OK {
		t.Fatal("expected not ok")
	}
	if rep.Files[0].MagicOK {
		t.Fatal("magic should fail")
	}
}
