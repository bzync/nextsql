package diskio

import (
	"errors"
	"github.com/bzync/nextsql/internal/nerr"
	"os"
	"path/filepath"
	"testing"
)

func TestInjectedWriteAndSyncFailClosed(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	restore := SetFaultForTest(func(Fault) error { return errors.New("enospc") })
	defer restore()
	if err := WriteFullAt(f, []byte("x"), 0); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("write=%v", err)
	}
	if err := Sync(f); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("sync=%v", err)
	}
}

func TestInjectedReadAndDirectorySyncFailClosed(t *testing.T) {
	dir := t.TempDir()
	f, err := os.Create(filepath.Join(dir, "f"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.Write(make([]byte, 8)); err != nil {
		t.Fatal(err)
	}
	restore := SetFaultForTest(func(Fault) error { return errors.New("EIO") })
	defer restore()
	if err := ReadFullAt(f, make([]byte, 8), 0); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("read=%v", err)
	}
	if err := SyncDir(dir); !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("syncdir=%v", err)
	}
}

// A ShortWrite must leave the prefix it names on disk: a torn record the
// caller cannot repair in place is the failure mode being reproduced, and a
// fault that quietly consumed nothing would not reproduce it.
func TestShortWriteLeavesThePrefixOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	restore := SetFaultForTest(func(fa Fault) error {
		return &ShortWrite{Consumed: 3, Err: errors.New("ENOSPC")}
	})
	buf := []byte("abcdefgh")
	// Sequential first, landing at offset 0 and advancing to 3; then a
	// positional write of the same buffer at 3.
	n, err := Write(f, buf)
	if n != 3 || !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("Write = (%d, %v), want (3, an IO failure)", n, err)
	}
	if n, err := WriteAt(f, buf, 3); n != 3 || !nerr.HasCode(err, nerr.IO) {
		t.Fatalf("WriteAt = (%d, %v), want (3, an IO failure)", n, err)
	}
	restore()

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcabc" {
		t.Fatalf("file = %q, want the two consumed prefixes %q", got, "abcabc")
	}
}

// The fault descriptor must identify what is failing, so a test can fail one
// file without failing another that shares the process.
func TestFaultDescribesTheOperation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	var seen []Fault
	restore := SetFaultForTest(func(fa Fault) error {
		seen = append(seen, fa)
		return nil
	})
	if _, err := WriteAt(f, []byte("x"), 4096); err != nil {
		t.Fatal(err)
	}
	if _, err := Write(f, []byte("y")); err != nil {
		t.Fatal(err)
	}
	if err := Sync(f); err != nil {
		t.Fatal(err)
	}
	restore()

	want := []Fault{
		{Op: "write", Path: path, Off: 4096},
		{Op: "write", Path: path, Off: -1},
		{Op: "sync", Path: path, Off: 0},
	}
	if len(seen) != len(want) {
		t.Fatalf("saw %+v, want %+v", seen, want)
	}
	for i := range want {
		if seen[i] != want[i] {
			t.Fatalf("fault %d = %+v, want %+v", i, seen[i], want[i])
		}
	}
}
