package upgrade

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
)

func TestCatalogCoversKnownFamilies(t *testing.T) {
	seen := map[Family]bool{}
	for _, s := range Catalog() {
		if s.Family == FamilyCatalog {
			if s.Current != 4 || s.MinReadable != 1 || s.MaxReadable != 4 {
				t.Fatalf("%s: %+v", s.Family, s)
			}
		} else if s.Current != 1 || s.MinReadable != 1 || s.MaxReadable != 1 {
			t.Fatalf("%s: %+v", s.Family, s)
		}
		if s.Magic == "" {
			t.Fatalf("%s missing magic", s.Family)
		}
		seen[s.Family] = true
	}
	for _, f := range []Family{
		FamilyPage, FamilyEnvelope, FamilyWAL, FamilyWALCtrl,
		FamilyUNDO, FamilyUNDOCtrl, FamilyCatalog, FamilyBackup,
		FamilyExport, FamilyProtocol, FamilyRepl, FamilyIsolated,
	} {
		if !seen[f] {
			t.Fatalf("missing %s", f)
		}
	}
}

func TestCatalogFamilyWindow(t *testing.T) {
	if err := Check(FamilyCatalog, 1); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 2); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 3); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 4); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 5); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("v5: %v", err)
	}
}

func TestCheckRejectsUnknownAndOutOfRange(t *testing.T) {
	if err := Check(FamilyPage, 1); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyPage, 0); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("old: %v", err)
	}
	if err := Check(FamilyPage, 2); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("new: %v", err)
	}
	if err := Check(Family("nope"), 1); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("unknown: %v", err)
	}
}

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
	if !Compatible(FamilyPage, 1) || !Compatible(FamilyWALCtrl, 1) || !Compatible(FamilyUNDOCtrl, 1) {
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
		case FamilyPage:
			sawPage = true
		case FamilyWALCtrl:
			sawWAL = true
		case FamilyUNDOCtrl:
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
