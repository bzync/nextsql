package backup

import (
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// Info is one backup found by ListBackups: its directory and header.
type Info struct {
	Path   string
	Header Header
}

// Created returns when the backup was taken.
func (i Info) Created() time.Time { return time.Unix(0, i.Header.CreatedNano).UTC() }

// ListBackups finds every backup directly under baseDir — each immediate
// subdirectory that ReadHeader succeeds on — sorted oldest first. Entries
// that are not backup directories (no header, or an unrelated directory)
// are skipped rather than treated as errors, so a backup root that also
// holds other files/directories does not need to be exclusively backups.
func ListBackups(baseDir string) ([]Info, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "backup.ListBackups", "read", err)
	}
	out := make([]Info, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		path := filepath.Join(baseDir, e.Name())
		hdr, err := ReadHeader(path)
		if err != nil {
			continue
		}
		out = append(out, Info{Path: path, Header: hdr})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Header.CreatedNano < out[j].Header.CreatedNano })
	return out, nil
}

// RetentionPolicy selects which backups a retention pass would remove.
// Exactly one of KeepCount / KeepFor should be set (KeepCount takes
// priority if both are); the zero value keeps everything.
type RetentionPolicy struct {
	// KeepCount, if > 0, keeps the KeepCount newest backups and prunes
	// every older one.
	KeepCount int
	// KeepFor, if > 0, keeps every backup created within KeepFor of now and
	// prunes older ones.
	KeepFor time.Duration
}

// SelectPruneCandidates applies policy to backups (as returned by
// ListBackups — already sorted oldest first) and returns the ones the
// policy would remove, oldest first. It never selects the single newest
// backup, regardless of policy: a retention pass that could remove every
// backup would leave nothing to restore from, which is a strictly worse
// outcome than keeping one stale one — the CLI still requires --confirm
// before actually deleting anything selected here.
func SelectPruneCandidates(backups []Info, policy RetentionPolicy, now time.Time) []Info {
	if len(backups) <= 1 {
		return nil
	}
	switch {
	case policy.KeepCount > 0:
		keep := policy.KeepCount
		if keep < 1 {
			keep = 1 // always keep at least the newest
		}
		if keep >= len(backups) {
			return nil
		}
		return append([]Info(nil), backups[:len(backups)-keep]...)
	case policy.KeepFor > 0:
		cutoff := now.Add(-policy.KeepFor)
		var candidates []Info
		// backups is sorted oldest-first; the last element (newest) is
		// never a candidate regardless of age. Once one entry is at or
		// after the cutoff, every later one (newer, by sort order) is too.
		for _, b := range backups[:len(backups)-1] {
			if !b.Created().Before(cutoff) {
				break
			}
			candidates = append(candidates, b)
		}
		return candidates
	default:
		return nil // no policy set: keep everything
	}
}
