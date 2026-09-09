package limits

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	prev := ""
	for _, s := range Catalog() {
		if s.Key == "" {
			t.Fatal("a spec has no key")
		}
		if seen[s.Key] {
			t.Fatalf("duplicate limit %q", s.Key)
		}
		seen[s.Key] = true
		if s.Key <= prev {
			t.Fatalf("catalog is not sorted: %q after %q", s.Key, prev)
		}
		prev = s.Key
		if s.Min < 1 {
			// Zero is expressed by ZeroMeans, never by a Min of 0: a range
			// that already includes 0 cannot say what 0 means.
			t.Fatalf("%s: Min = %d, want >= 1 (use ZeroMeans for a 0 sentinel)", s.Key, s.Min)
		}
		if s.Max < s.Min {
			t.Fatalf("%s: Max %d < Min %d", s.Key, s.Max, s.Min)
		}
		if s.Why == "" {
			// A ceiling nobody wrote a reason for is a number nobody can
			// revise responsibly.
			t.Fatalf("%s: no rationale for its ceiling", s.Key)
		}
		if s.Class == "" || s.Unit == "" {
			t.Fatalf("%s: missing class or unit", s.Key)
		}
		if s.Default != 0 {
			if err := Check(s.Key, s.Default); err != nil {
				t.Fatalf("%s: its own default is not acceptable: %v", s.Key, err)
			}
		}
	}
}

func TestCheckBounds(t *testing.T) {
	for _, s := range Catalog() {
		if err := Check(s.Key, s.Min); err != nil {
			t.Fatalf("%s: Min rejected: %v", s.Key, err)
		}
		if err := Check(s.Key, s.Max); err != nil {
			t.Fatalf("%s: Max rejected: %v", s.Key, err)
		}
		if err := Check(s.Key, s.Max+1); err == nil {
			t.Fatalf("%s: Max+1 accepted; the ceiling does not hold", s.Key)
		} else if !nerr.HasCode(err, nerr.InvalidArgument) {
			t.Fatalf("%s: wrong code for an over-ceiling value: %v", s.Key, err)
		}
		if err := Check(s.Key, -1); err == nil {
			t.Fatalf("%s: a negative value was accepted", s.Key)
		}
		switch err := Check(s.Key, 0); {
		case s.AcceptsZero() && err != nil:
			t.Fatalf("%s: documents a zero sentinel but rejects 0: %v", s.Key, err)
		case !s.AcceptsZero() && err == nil:
			t.Fatalf("%s: accepts 0 with no documented meaning", s.Key)
		}
	}
}

func TestCheckErrorNamesTheRange(t *testing.T) {
	err := Check("buffer_pages", 1<<40)
	if err == nil {
		t.Fatal("expected rejection")
	}
	msg := err.Error()
	for _, want := range []string{"buffer_pages", "["} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error %q does not contain %q; an operator cannot fix it from this", msg, want)
		}
	}
}

func TestUnknownLimitIsAnError(t *testing.T) {
	if err := Check("max_something_nobody_enforces", 1); err == nil {
		t.Fatal("an unknown key passed validation")
	}
	if _, ok := Lookup("max_something_nobody_enforces"); ok {
		t.Fatal("Lookup invented a spec")
	}
}

func TestByClassAndClasses(t *testing.T) {
	classes := Classes()
	if len(classes) == 0 {
		t.Fatal("no classes defined")
	}
	classSeen := make(map[Class]bool)
	for _, c := range classes {
		if classSeen[c] {
			t.Fatalf("duplicate class %q", c)
		}
		classSeen[c] = true
	}
	total := 0
	for _, c := range classes {
		specs := ByClass(c)
		total += len(specs)
		for _, s := range specs {
			if s.Class != c {
				t.Fatalf("spec %s has class %s, want %s", s.Key, s.Class, c)
			}
		}
	}
	if total != len(Catalog()) {
		t.Fatalf("total specs across classes = %d, want %d", total, len(Catalog()))
	}
}

func TestSpecDefaultString(t *testing.T) {
	for _, s := range Catalog() {
		ds := s.DefaultString()
		if ds == "" {
			t.Fatalf("empty DefaultString for %s", s.Key)
		}
		if s.Default != 0 && !strings.Contains(ds, fmt.Sprintf("%d", s.Default)) {
			t.Fatalf("DefaultString %q for %s does not contain default %d", ds, s.Key, s.Default)
		}
	}
}

func TestRenderMarkdown(t *testing.T) {
	md := RenderMarkdown()
	if len(md) == 0 {
		t.Fatal("empty RenderMarkdown")
	}
	for _, s := range Catalog() {
		if !strings.Contains(md, s.Key) {
			t.Fatalf("RenderMarkdown missing key %s", s.Key)
		}
	}
}

func TestDocsLimitsMatchCatalog(t *testing.T) {
	want := RenderMarkdown()
	path := filepath.Join("..", "..", "docs", "limits.md")
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("could not read docs/limits.md: %v", err)
	}
	if string(got) != want {
		if os.Getenv("UPDATE_DOCS") == "1" {
			if err := os.WriteFile(path, []byte(want), 0o644); err != nil {
				t.Fatalf("updating docs/limits.md: %v", err)
			}
			return
		}
		t.Fatalf("docs/limits.md does not match limits.RenderMarkdown(); re-run with UPDATE_DOCS=1 or update docs/limits.md")
	}
}

