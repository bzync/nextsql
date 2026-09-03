package backup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func mkInfo(nano int64) Info {
	return Info{Header: Header{CreatedNano: nano}}
}

func TestSelectPruneCandidatesKeepCount(t *testing.T) {
	backups := []Info{mkInfo(1), mkInfo(2), mkInfo(3), mkInfo(4), mkInfo(5)}
	got := SelectPruneCandidates(backups, RetentionPolicy{KeepCount: 2}, time.Now())
	if len(got) != 3 || got[0].Header.CreatedNano != 1 || got[2].Header.CreatedNano != 3 {
		t.Fatalf("%+v", got)
	}
}

func TestSelectPruneCandidatesKeepCountNeverEmptiesSet(t *testing.T) {
	backups := []Info{mkInfo(1), mkInfo(2)}
	// KeepCount=0 or negative still keeps at least the newest.
	got := SelectPruneCandidates(backups, RetentionPolicy{KeepCount: 0}, time.Now())
	if len(got) != 0 {
		t.Fatalf("keep-count 0 (no policy set) must keep everything: %+v", got)
	}
	got = SelectPruneCandidates(backups, RetentionPolicy{KeepCount: -5}, time.Now())
	if len(got) != 0 {
		t.Fatalf("negative keep-count treated as no policy: %+v", got)
	}
}

func TestSelectPruneCandidatesKeepCountExceedsAvailable(t *testing.T) {
	backups := []Info{mkInfo(1), mkInfo(2)}
	got := SelectPruneCandidates(backups, RetentionPolicy{KeepCount: 10}, time.Now())
	if got != nil {
		t.Fatalf("expected nothing to prune: %+v", got)
	}
}

func TestSelectPruneCandidatesKeepFor(t *testing.T) {
	now := time.Now()
	day := 24 * time.Hour
	backups := []Info{
		mkInfo(now.Add(-10 * day).UnixNano()),
		mkInfo(now.Add(-8 * day).UnixNano()),
		mkInfo(now.Add(-3 * day).UnixNano()),
		mkInfo(now.Add(-1 * day).UnixNano()),
	}
	got := SelectPruneCandidates(backups, RetentionPolicy{KeepFor: 7 * day}, now)
	if len(got) != 2 {
		t.Fatalf("expected 2 candidates older than 7 days, got %d: %+v", len(got), got)
	}
	for _, b := range got {
		if !b.Created().Before(now.Add(-7 * day)) {
			t.Fatalf("candidate not actually older than the window: %v", b.Created())
		}
	}
}

func TestSelectPruneCandidatesKeepForNeverPrunesNewest(t *testing.T) {
	now := time.Now()
	year := 365 * 24 * time.Hour
	// Every backup is ancient; even so, the single newest must survive.
	backups := []Info{
		mkInfo(now.Add(-2 * year).UnixNano()),
		mkInfo(now.Add(-2 * year).UnixNano()),
		mkInfo(now.Add(-2 * year).UnixNano()),
	}
	got := SelectPruneCandidates(backups, RetentionPolicy{KeepFor: 24 * time.Hour}, now)
	if len(got) != 2 {
		t.Fatalf("expected the newest of 3 to survive, got %d candidates: %+v", len(got), got)
	}
}

func TestSelectPruneCandidatesSingleBackupNeverPruned(t *testing.T) {
	backups := []Info{mkInfo(1)}
	if got := SelectPruneCandidates(backups, RetentionPolicy{KeepCount: 0}, time.Now()); got != nil {
		t.Fatalf("a single backup must never be a prune candidate: %+v", got)
	}
	if got := SelectPruneCandidates(nil, RetentionPolicy{KeepFor: time.Second}, time.Now()); got != nil {
		t.Fatalf("empty input: %+v", got)
	}
}

func TestSelectPruneCandidatesNoPolicyKeepsEverything(t *testing.T) {
	backups := []Info{mkInfo(1), mkInfo(2), mkInfo(3)}
	if got := SelectPruneCandidates(backups, RetentionPolicy{}, time.Now()); got != nil {
		t.Fatalf("no policy must keep everything: %+v", got)
	}
}

func TestListBackupsSkipsNonBackupDirsAndSortsByAge(t *testing.T) {
	dir := t.TempDir()
	names := []string{"b1", "b2", "b3"}
	for _, name := range names {
		dataDir, _, _, env := setupSQL(t, filepath.Join(dir, "src-"+name))
		dest := filepath.Join(dir, name)
		if _, err := Create(dataDir, dest, env, Options{SkipRestoreTest: true}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(time.Millisecond) // ensure strictly increasing CreatedNano
	}
	// An unrelated directory (no header file) must be skipped, not error.
	if err := os.MkdirAll(filepath.Join(dir, "not-a-backup"), 0o700); err != nil {
		t.Fatal(err)
	}

	got, err := ListBackups(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(names) {
		t.Fatalf("expected %d backups, got %d: %+v", len(names), len(got), got)
	}
	for i := 1; i < len(got); i++ {
		if got[i].Header.CreatedNano < got[i-1].Header.CreatedNano {
			t.Fatalf("not sorted oldest-first: %+v", got)
		}
	}
	if filepath.Base(got[0].Path) != names[0] {
		t.Fatalf("expected %s first (oldest), got %+v", names[0], got)
	}
}
