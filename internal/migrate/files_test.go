package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/version"
)

func TestParseFileName(t *testing.T) {
	f, err := ParseFileName("20260818120000_create_customers.up.sql")
	if err != nil {
		t.Fatal(err)
	}
	if f.Version != "20260818120000" || f.Name != "create_customers" || f.Direction != "up" {
		t.Fatalf("%+v", f)
	}
	if _, err := ParseFileName("0001_name.up.sql"); err == nil {
		t.Fatal("expected invalid integer prefix")
	}
	if _, err := ParseFileName("20260818120000_Create.up.sql"); err == nil {
		t.Fatal("expected uppercase reject")
	}
	if _, err := ParseFileName("20260818120000_foo.side.sql"); err == nil {
		t.Fatal("expected direction reject")
	}
}

func TestChecksumNormalization(t *testing.T) {
	plain := []byte("CREATE TABLE t (id UUID);\n")
	crlf := []byte("CREATE TABLE t (id UUID);\r\n")
	if Checksum(plain) != Checksum(crlf) {
		t.Fatal("CRLF must match LF")
	}
	withBOM := append(append([]byte{}, crlf...), utf8BOM...)
	if Checksum(plain) != Checksum(withBOM) {
		t.Fatal("trailing BOM must be stripped")
	}
	leading := append(append([]byte{}, utf8BOM...), plain...)
	if Checksum(plain) == Checksum(leading) {
		t.Fatal("leading BOM is part of the digest")
	}
	commented := []byte("CREATE TABLE t (id UUID); -- x\n")
	if Checksum(plain) == Checksum(commented) {
		t.Fatal("comments must change the digest")
	}
	if got := Checksum(plain); got != strings.ToLower(got) || len(got) != 64 {
		t.Fatalf("hex sha256: %q", got)
	}
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"add_orders":   "add_orders",
		"Add Orders":   "add_orders",
		"ADD-ORDERS!!": "add_orders",
		"add__orders":  "add_orders",
		"  x  ":        "x",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Fatalf("%q: got %q want %q", in, got, want)
		}
	}
	if Slug("___") != "" {
		t.Fatal("expected empty slug")
	}
	long := strings.Repeat("a", 80)
	if got := Slug(long); len(got) != 64 {
		t.Fatalf("len %d", len(got))
	}
}

func TestCreateWritesHeaderAndRetriesTimestamp(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.UTC)
	up, down, err := createAt(dir, "add_orders", now)
	if err != nil {
		t.Fatal(err)
	}
	wantUp := filepath.Join(dir, "20260818143000_add_orders.up.sql")
	wantDown := filepath.Join(dir, "20260818143000_add_orders.down.sql")
	if up != wantUp || down != wantDown {
		t.Fatalf("%s %s", up, down)
	}
	body, err := os.ReadFile(up)
	if err != nil {
		t.Fatal(err)
	}
	want := "-- migrate:up 20260818143000 add_orders\n-- NextSQL " + version.String + ": one statement per request; this file is split on ';'.\n-- Do not include BEGIN/COMMIT/ROLLBACK.\n"
	if string(body) != want {
		t.Fatalf("header %q", body)
	}
	up2, _, err := createAt(dir, "add_orders", now)
	if err != nil {
		t.Fatal(err)
	}
	if up2 != filepath.Join(dir, "20260818143001_add_orders.up.sql") {
		t.Fatalf("retry %s", up2)
	}
}

func TestCreateRetriesWhenAnySlugOwnsVersion(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.UTC)
	upA, _, err := createAt(dir, "aaa", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(upA) != "20260818143000_aaa.up.sql" {
		t.Fatalf("aaa %s", upA)
	}
	upB, _, err := createAt(dir, "bbb", now)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(upB) != "20260818143001_bbb.up.sql" {
		t.Fatalf("bbb %s", upB)
	}
	migs, err := Validate(dir)
	if err != nil || len(migs) != 2 {
		t.Fatalf("%v %v", migs, err)
	}
}

func TestCreateCap60(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.UTC)
	for i := 0; i < MaxCreateAttempts; i++ {
		ver := now.Add(time.Duration(i) * time.Second).Format(VersionLayout)
		if err := os.WriteFile(filepath.Join(dir, ver+"_other.up.sql"), []byte("--\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := createAt(dir, "x", now); err == nil {
		t.Fatal("expected cap")
	} else if !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("%v", err)
	}
}

func TestCreateRejectsEmptySlug(t *testing.T) {
	if _, _, err := createAt(t.TempDir(), "!!!", time.Now()); err == nil {
		t.Fatal("expected empty slug")
	}
}

func TestSentinels(t *testing.T) {
	if !nerr.HasCode(ErrDirty, nerr.Conflict) {
		t.Fatal(ErrDirty)
	}
	if !nerr.HasCode(ErrChecksum, nerr.InvalidFormat) {
		t.Fatal(ErrChecksum)
	}
	if cli.Code(ErrDirty) != cli.ExitDirty {
		t.Fatalf("dirty code %d", cli.Code(ErrDirty))
	}
	if cli.Code(ErrChecksum) != cli.ExitChecksum {
		t.Fatalf("checksum code %d", cli.Code(ErrChecksum))
	}
}
