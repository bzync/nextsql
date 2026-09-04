package compat

import (
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestCatalogCoversKnownFamilies(t *testing.T) {
	seen := map[Family]bool{}
	for _, s := range Catalog() {
		if s.Family == FamilyCatalog {
			if s.Current != 12 || s.MinReadable != 1 || s.MaxReadable != 12 {
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
	if err := Check(FamilyCatalog, 5); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 6); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 7); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 8); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 9); err != nil {
		t.Fatal(err)
	}
	if err := Check(FamilyCatalog, 10); err != nil {
		t.Fatalf("v10: %v", err)
	}
	if err := Check(FamilyCatalog, 11); err != nil {
		t.Fatalf("v11: %v", err)
	}
	if err := Check(FamilyCatalog, 12); err != nil {
		t.Fatalf("v12: %v", err)
	}
	if err := Check(FamilyCatalog, 13); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("v13: %v", err)
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

func TestCheckErrorNamesActualAndSupportedVersions(t *testing.T) {
	// Guards the actionable-error improvement made alongside the
	// migration-strategy write-up (docs/storage-format.md): the message
	// must name the actual version and the min/max this binary supports,
	// not just say "unsupported", so an operator can tell a too-old file
	// (needs offline dump/reload) apart from a too-new one (needs a
	// newer binary) without cross-referencing Catalog() by hand.
	err := Check(FamilyPage, 2)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"page", "2", "newer", "1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}

	err = Check(FamilyPage, 0)
	if err == nil {
		t.Fatal("expected an error")
	}
	msg = err.Error()
	for _, want := range []string{"page", "0", "older", "1"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q missing %q", msg, want)
		}
	}
}
