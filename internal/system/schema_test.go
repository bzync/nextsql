package system

import (
	"bytes"
	"os"
	"testing"

	"github.com/bzync/nextsql/internal/version"
)

// since_version records the release a capability first shipped in. Deriving it
// from version.String makes every existing capability claim it appeared in
// whatever build is running, which silently rewrites the history of the table
// the moment the engine version is bumped.
func TestCapabilitySinceVersionsAreLiterals(t *testing.T) {
	src, err := os.ReadFile("schema.go")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(src, []byte("version.String)")) {
		t.Fatal("a capability's since_version is derived from version.String; it must be the literal release it shipped in")
	}
	for _, row := range Capabilities() {
		since := row[3].Str
		if since == "" {
			t.Fatalf("capability %q has no since_version", row[0].Str)
		}
		if since == version.String && version.String != "0.0.1" {
			t.Fatalf("capability %q reports the running build's version %q as its since_version", row[0].Str, since)
		}
	}
}
