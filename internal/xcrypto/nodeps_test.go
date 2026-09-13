package xcrypto

import (
	"os/exec"
	"strings"
	"testing"
)

// GO-2026-5932 has no fix version. Scanners that match the whole
// golang.org/x/crypto module will flag any binary that records it,
// including binaries that never imported openpgp.
func TestPublishedCommandsDoNotDependOnXCrypto(t *testing.T) {
	t.Parallel()
	cmd := exec.Command("go", "list", "-deps", "-f", "{{if .Module}}{{.Module.Path}}{{end}}",
		"github.com/bzync/nextsql/cmd/nextsql",
		"github.com/bzync/nextsql/cmd/nextsqld",
		"github.com/bzync/nextsql/cmd/nextsql-entrypoint")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("go list: %v\n%s", err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == "golang.org/x/crypto" {
			t.Fatal("published command depends on golang.org/x/crypto; GO-2026-5932 is unfixed for every version of that module")
		}
	}
}
