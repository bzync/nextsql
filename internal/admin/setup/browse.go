package setup

import (
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// browseEntry is one child of a directory listing returned by
// GET /api/v1/browse — enough for the wizard's path-picker fields (data
// directory, key file, TLS cert/key) to render and navigate with. It never
// carries file contents.
type browseEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	IsDir bool   `json:"isDir"`
}

// browseResult is GET /api/v1/browse's response: the directory that was
// actually listed (which may differ from the requested one — see
// handleBrowse), its parent (omitted at the root), and its immediate
// children.
type browseResult struct {
	Dir     string        `json:"dir"`
	Parent  string        `json:"parent,omitempty"`
	Entries []browseEntry `json:"entries"`
}

// handleBrowse lists the immediate children of a directory on this
// machine, for the wizard's path-picker fields. It is the server-side half
// of "select a path" — the browser's own file input can never hand JS a
// real filesystem path (only a bare filename), so the picker instead asks
// this already-local, already-trusted process to list directories the way
// a native "Browse…" dialog would.
//
// It only ever reads directory entry names via os.ReadDir plus each
// entry's directory bit — never file contents — and is gated behind the
// same single-run operator token as every other /api/v1 route, so it
// discloses nothing an operator sitting at this machine's own shell
// couldn't already see with `ls`.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	dir := r.URL.Query().Get("dir")
	if dir == "" {
		dir = homeDir()
	}
	dir = expandHome(dir)
	dir = filepath.Clean(dir)

	entries, err := os.ReadDir(dir)
	if err != nil && os.IsNotExist(err) {
		// Not existing *yet* is the normal case for most of these fields —
		// the data directory, key file, and TLS cert/key almost always
		// name something this wizard (or a later step) is about to
		// create, not something that already has to be there. Report it
		// as "nothing here yet" against the exact path the operator
		// typed, rather than silently substituting a different,
		// unrelated directory's contents: that previously made the
		// picker show the operator's home-directory dotfiles under a
		// data-directory field that read "/var/lib/nextsql/", with
		// nothing telling them what they were actually looking at.
		writeJSON(w, http.StatusOK, browseResult{Dir: dir, Parent: parentOf(dir), Entries: []browseEntry{}})
		return
	}
	if err != nil {
		// A genuinely bad request — permission denied, not a directory,
		// etc. — still shouldn't dead-end the picker entirely: fall back
		// to the operator's home directory once, same as the wizard's own
		// default suggestions (see defaults.go).
		if home := homeDir(); home != "" && home != dir {
			if fallback, fbErr := os.ReadDir(home); fbErr == nil {
				dir, entries, err = home, fallback, nil
			}
		}
		if err != nil {
			writeError(w, http.StatusBadRequest, "cannot list "+dir+": "+classifyBrowseErr(err))
			return
		}
	}

	out := make([]browseEntry, 0, len(entries))
	for _, e := range entries {
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			// Follow one level of symlink to decide the folder/file icon;
			// a broken link just keeps ReadDir's (false) answer.
			if info, statErr := os.Stat(filepath.Join(dir, e.Name())); statErr == nil {
				isDir = info.IsDir()
			}
		}
		out = append(out, browseEntry{Name: e.Name(), Path: filepath.Join(dir, e.Name()), IsDir: isDir})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].IsDir != out[j].IsDir {
			return out[i].IsDir // directories first
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	writeJSON(w, http.StatusOK, browseResult{Dir: dir, Parent: parentOf(dir), Entries: out})
}

// parentOf is filepath.Dir, except the root of a tree (where Dir(dir)==dir)
// reports no parent — there's nowhere higher for the picker to go.
func parentOf(dir string) string {
	parent := filepath.Dir(dir)
	if parent == dir {
		return ""
	}
	return parent
}

func homeDir() string {
	h, _ := os.UserHomeDir()
	return h
}

// expandHome resolves a leading "~" the same way a shell or native file
// picker would ("~" and "~/..." only — "~otheruser" is left alone, matching
// most GUI pickers' scope). Done explicitly here rather than left to
// handleBrowse's not-found fallback: ReadDir("~") always fails as a
// literal, unhelpfully-named relative path, and a fallback tuned for "the
// operator mistyped something" shouldn't also be what makes "~" work —
// that would silently redirect a real typo (e.g. "~/Documnets") to the
// home directory too, instead of resolving it to the real (missing)
// subdirectory and reporting it honestly.
func expandHome(dir string) string {
	home := homeDir()
	if home == "" || dir == "" {
		return dir
	}
	if dir == "~" {
		return home
	}
	if rest, ok := strings.CutPrefix(dir, "~/"); ok {
		return filepath.Join(home, rest)
	}
	if rest, ok := strings.CutPrefix(dir, `~\`); ok {
		return filepath.Join(home, rest)
	}
	return dir
}

func classifyBrowseErr(err error) string {
	switch {
	case os.IsNotExist(err):
		return "no such directory"
	case os.IsPermission(err):
		return "permission denied"
	default:
		return "not a directory, or unreadable"
	}
}
