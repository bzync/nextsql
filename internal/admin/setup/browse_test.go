package setup

import (
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestHandleBrowseRequiresToken(t *testing.T) {
	_, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	resp, err := http.Get(hs.URL + "/api/v1/browse")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("GET /api/v1/browse without token: got %d, want 403", resp.StatusCode)
	}
}

func TestHandleBrowseListsDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.key"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Another.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/browse?dir="+url.QueryEscape(dir), nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/browse: got %d", resp.StatusCode)
	}
	var got browseResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Dir != filepath.Clean(dir) {
		t.Errorf("Dir = %q, want %q", got.Dir, filepath.Clean(dir))
	}
	if got.Parent == "" {
		t.Error("expected a non-empty Parent for a non-root directory")
	}
	if len(got.Entries) != 3 {
		t.Fatalf("Entries = %d, want 3: %+v", len(got.Entries), got.Entries)
	}
	// Directories first, then alphabetical (case-insensitive) among files:
	// "sub" (dir), "Another.txt", "root.key".
	want := []struct {
		name  string
		isDir bool
	}{
		{"sub", true},
		{"Another.txt", false},
		{"root.key", false},
	}
	for i, w := range want {
		if got.Entries[i].Name != w.name || got.Entries[i].IsDir != w.isDir {
			t.Errorf("Entries[%d] = %+v, want name=%s isDir=%v", i, got.Entries[i], w.name, w.isDir)
		}
		wantPath := filepath.Join(dir, w.name)
		if got.Entries[i].Path != wantPath {
			t.Errorf("Entries[%d].Path = %q, want %q", i, got.Entries[i].Path, wantPath)
		}
	}
}

// TestHandleBrowseNotExistYetStaysOnRequestedPath is the regression case
// for a real bug: a data-directory field reading "/var/lib/nextsql/" was
// showing the operator's *home* directory's dotfiles in its dropdown,
// because ReadDir failing with "not exist yet" (the normal case for a data
// directory that's about to be created) used to fall back to listing home
// instead — the response's Dir still said "/var/lib/nextsql", but the
// entries actually belonged to somewhere else entirely, with nothing
// telling the operator that had happened. Not existing yet must report an
// empty listing for the exact path asked about, never silently substitute
// a different, unrelated directory.
func TestHandleBrowseNotExistYetStaysOnRequestedPath(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist", "yet")
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/browse?dir="+url.QueryEscape(dir), nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/browse (not-yet-existing dir): got %d, want 200", resp.StatusCode)
	}
	var got browseResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Dir != filepath.Clean(dir) {
		t.Errorf("Dir = %q, want the requested path %q unchanged — got a substituted directory instead", got.Dir, filepath.Clean(dir))
	}
	if len(got.Entries) != 0 {
		t.Errorf("Entries = %+v, want empty for a directory that doesn't exist yet", got.Entries)
	}
}

// TestHandleBrowseFallsBackOnPermissionDenied covers the *other* kind of
// unreadable path — one that does exist but genuinely can't be listed —
// where falling back to home (unlike the not-yet-existing case above) is
// still the right call so the picker doesn't dead-end entirely.
func TestHandleBrowseFallsBackOnPermissionDenied(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permission bits, so this case can't be produced as root")
	}
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) }) // let t.TempDir() clean up afterward
	if _, err := os.ReadDir(dir); err == nil {
		t.Skip("this environment can still read a 0000 directory (e.g. running privileged) — nothing to test")
	}

	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/browse?dir="+url.QueryEscape(dir), nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("GET /api/v1/browse (permission denied): got %d, want 200 or 400", resp.StatusCode)
	}
	if resp.StatusCode == http.StatusOK {
		var got browseResult
		if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
			t.Fatal(err)
		}
		if got.Dir == filepath.Clean(dir) {
			t.Error("expected a fallback away from the permission-denied directory")
		}
	}
}

func TestHandleBrowseEmptyDirDefaultsToHome(t *testing.T) {
	home := homeDir()
	if home == "" {
		t.Skip("no home directory available in this environment")
	}
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/browse", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/browse (no dir): got %d", resp.StatusCode)
	}
	var got browseResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Dir != filepath.Clean(home) {
		t.Errorf("Dir = %q, want home %q", got.Dir, filepath.Clean(home))
	}
}

func TestExpandHome(t *testing.T) {
	home := homeDir()
	if home == "" {
		t.Skip("no home directory available in this environment")
	}
	cases := []struct {
		in, want string
	}{
		{"~", home},
		{"~/", home}, // filepath.Join("~/"'s remainder, "") cleans the trailing slash away
		{"~/Documents", filepath.Join(home, "Documents")},
		{"~/a/b", filepath.Join(home, "a", "b")},
		{"~otheruser", "~otheruser"},   // left alone, matches most GUI pickers' scope
		{"/etc/nextsql", "/etc/nextsql"}, // untouched, no leading ~
		{"", ""},
	}
	for _, c := range cases {
		if got := expandHome(c.in); got != c.want {
			t.Errorf("expandHome(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestHandleBrowseExpandsTilde exercises "~" end to end through the HTTP
// handler (not just the expandHome helper) — this is the case that used to
// silently fail: ReadDir("~") errors as a literal relative path, so it only
// "worked" by accident via the not-found fallback, which also masked real
// typos as the same fallback. See expandHome's doc comment.
func TestHandleBrowseExpandsTilde(t *testing.T) {
	home := homeDir()
	if home == "" {
		t.Skip("no home directory available in this environment")
	}
	s, hs := newTestHTTPServer(t, `printf '{"ok":true}'`)
	req, _ := http.NewRequest("GET", hs.URL+"/api/v1/browse?dir=~", nil)
	req.Header.Set(tokenHeader, s.Token())
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/v1/browse?dir=~: got %d", resp.StatusCode)
	}
	var got browseResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.Dir != filepath.Clean(home) {
		t.Errorf("Dir = %q, want home %q", got.Dir, filepath.Clean(home))
	}
}
