package migrate

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
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

func TestCreateMigration(t *testing.T) {
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
	upFile, err := ParseFileName(filepath.Base(up))
	if err != nil {
		t.Fatal(err)
	}
	downFile, err := ParseFileName(filepath.Base(down))
	if err != nil {
		t.Fatal(err)
	}
	if upFile.Version != downFile.Version || upFile.Name != "add_orders" || downFile.Name != "add_orders" {
		t.Fatalf("up=%+v down=%+v", upFile, downFile)
	}
	if _, err := parseMigrationVersion(upFile.Version); err != nil {
		t.Fatalf("invalid generated timestamp %q: %v", upFile.Version, err)
	}
}

func TestMultipleCreatesWithinSameSecond(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.UTC)
	for i := 0; i < 10; i++ {
		up, _, err := createAt(dir, "same_name", now)
		if err != nil {
			t.Fatalf("create %d: %v", i, err)
		}
		want := now.Add(time.Duration(i) * time.Second).Format(VersionLayout)
		if got := strings.SplitN(filepath.Base(up), "_", 2)[0]; got != want {
			t.Fatalf("create %d version %s; want %s", i, got, want)
		}
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

func TestCreate100MigrationsRapidly(t *testing.T) {
	testCreateMigrationsRapidly(t, 100)
}

func TestCreate500MigrationsRapidly(t *testing.T) {
	testCreateMigrationsRapidly(t, 500)
}

func testCreateMigrationsRapidly(t *testing.T, count int) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("migration_%03d", i)
		if _, _, err := createAt(dir, name, now); err != nil {
			t.Fatalf("migration %d failed: %v", i, err)
		}
	}
	migrations, err := Validate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != count {
		t.Fatalf("got %d migrations; want %d", len(migrations), count)
	}
	for i, migration := range migrations {
		want := now.Add(time.Duration(i) * time.Second).Format(VersionLayout)
		if migration.Version != want {
			t.Fatalf("migration %d version %s; want %s", i, migration.Version, want)
		}
		if migration.Down == nil {
			t.Fatalf("migration %d has no down file", i)
		}
	}
}

func TestMigrationVersionsAreStrictlyIncreasing(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 18, 14, 30, 0, 0, time.UTC)
	var previous string
	for i := 0; i < 100; i++ {
		up, _, err := createAt(dir, fmt.Sprintf("ordered_%03d", i), now)
		if err != nil {
			t.Fatal(err)
		}
		file, err := ParseFileName(filepath.Base(up))
		if err != nil {
			t.Fatal(err)
		}
		if previous != "" && file.Version <= previous {
			t.Fatalf("version %s is not greater than %s", file.Version, previous)
		}
		previous = file.Version
	}
}

func TestCreateAfterFutureMigrationVersion(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	future := "20260828110000"
	for _, direction := range []string{"up", "down"} {
		path := filepath.Join(dir, future+"_future."+direction+".sql")
		if err := os.WriteFile(path, []byte("-- existing\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	up, _, err := createAt(dir, "after_future", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(up); got != "20260828110001_after_future.up.sql" {
		t.Fatalf("got %s", got)
	}
}

func TestConcurrentMigrationCreate(t *testing.T) {
	const count = 100
	dir := t.TempDir()
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	start := make(chan struct{})
	type result struct {
		up   string
		down string
		err  error
	}
	results := make(chan result, count)
	for i := 0; i < count; i++ {
		go func(i int) {
			<-start
			up, down, err := createAt(dir, fmt.Sprintf("concurrent_%03d", i), now)
			results <- result{up: up, down: down, err: err}
		}(i)
	}
	close(start)

	versions := make(map[string]struct{}, count)
	for i := 0; i < count; i++ {
		res := <-results
		if res.err != nil {
			t.Fatalf("concurrent create failed: %v", res.err)
		}
		up, err := ParseFileName(filepath.Base(res.up))
		if err != nil {
			t.Fatal(err)
		}
		down, err := ParseFileName(filepath.Base(res.down))
		if err != nil {
			t.Fatal(err)
		}
		if up.Version != down.Version {
			t.Fatalf("partial pair: up=%s down=%s", up.Version, down.Version)
		}
		if _, exists := versions[up.Version]; exists {
			t.Fatalf("duplicate version %s", up.Version)
		}
		versions[up.Version] = struct{}{}
	}
	migrations, err := Validate(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != count {
		t.Fatalf("got %d migrations; want %d", len(migrations), count)
	}
	for _, migration := range migrations {
		if migration.Down == nil {
			t.Fatalf("migration %s is missing its down file", migration.Version)
		}
	}
}

func TestAllocatorPreservesExistingMigrationOrdering(t *testing.T) {
	dir := t.TempDir()
	versions := []string{"20260101100000", "20260101100100"}
	for i, version := range versions {
		for _, direction := range []string{"up", "down"} {
			name := fmt.Sprintf("%s_existing_%d.%s.sql", version, i, direction)
			if err := os.WriteFile(filepath.Join(dir, name), []byte("-- existing\n"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
	up, _, err := createAt(dir, "new", time.Date(2026, 1, 1, 10, 0, 30, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(up); got != "20260101100101_new.up.sql" {
		t.Fatalf("got %s", got)
	}
	migrations, err := Validate(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := make([]string, len(migrations))
	for i := range migrations {
		got[i] = migrations[i].Version
	}
	if !sort.StringsAreSorted(got) {
		t.Fatalf("versions are not sorted: %v", got)
	}
}

func TestAllocatorIgnoresUnrelatedFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.txt"), []byte("not a migration\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC)
	up, _, err := createAt(dir, "created", now)
	if err != nil {
		t.Fatal(err)
	}
	if got := filepath.Base(up); got != "20260828090000_created.up.sql" {
		t.Fatalf("got %s", got)
	}
}

func TestCreateCleansUpWhenDownFileCreationFails(t *testing.T) {
	dir := t.TempDir()
	up := filepath.Join(dir, "20260828090000_cleanup.up.sql")
	down := filepath.Join(dir, "20260828090000_cleanup.down.sql")
	create := func(path, body string) error {
		if path == down {
			return os.ErrPermission
		}
		return writeFileExcl(path, body)
	}
	failedPath, collision, err := createMigrationPair(up, down, "up", "down", create, os.Remove)
	if !errors.Is(err, os.ErrPermission) || collision || failedPath != down {
		t.Fatalf("failed=%s collision=%t err=%v", failedPath, collision, err)
	}
	if _, err := os.Stat(up); !os.IsNotExist(err) {
		t.Fatalf("up file was not cleaned up: %v", err)
	}
	if _, err := os.Stat(down); !os.IsNotExist(err) {
		t.Fatalf("down file unexpectedly exists: %v", err)
	}
}

func TestCreateReportsTimestampExhaustion(t *testing.T) {
	dir := t.TempDir()
	for _, direction := range []string{"up", "down"} {
		path := filepath.Join(dir, "99991231235959_last."+direction+".sql")
		if err := os.WriteFile(path, []byte("-- last\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	_, _, err := createAt(dir, "overflow", time.Date(2026, 8, 28, 9, 0, 0, 0, time.UTC))
	if !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("got %v; want exhausted", err)
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
