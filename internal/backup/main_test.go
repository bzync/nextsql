package backup

import (
	"os"
	"testing"

	"github.com/bzync/nextsql/internal/storage/file"
)

// Every database this suite creates otherwise reserves the production ~256 MiB
// allocation runway with fallocate, in real blocks. These tests copy, seal and
// restore whole databases — TestListBackupsSkipsNonBackupDirsAndSortsByAge
// alone builds three sources plus three backups — so the runway dominated the
// suite's I/O while proving nothing about backup: on ext4 the package went
// from 201s to 3.3s with it shrunk.
//
// This is a per-suite decision, not a blanket one. internal/storage/btree does
// bulk inserts, which is what the runway exists for, and is measurably worse
// without it (104s at the default, 115s at 64 pages), so it keeps the default.
// The runway itself is covered at its production default, including that
// fallocate reserves real blocks rather than a sparse file, by
// internal/storage/file.
func TestMain(m *testing.M) {
	file.SetCapacityAhead(64) // 1 MiB
	os.Exit(m.Run())
}
