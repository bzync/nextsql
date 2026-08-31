package hosting

import (
	"os"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestDataDirLockIsExclusiveAndReusable(t *testing.T) {
	dir := t.TempDir()
	first, err := AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := AcquireDataDirLock(dir); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("second lock: %v", err)
	}
	if st, err := os.Stat(LockPath(dir)); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("lock mode: %v %v", st, err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
}
