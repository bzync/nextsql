package hosting

import "testing"

func FuzzParseDeploymentBootstrap(f *testing.F) {
	f.Add([]byte("version: 1\nrealms: []\n"))
	f.Add([]byte("---\n---\n"))
	f.Add([]byte("version: &v 1\ndefault: *v\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = ParseDeploymentBootstrap(raw, t.TempDir())
	})
}
