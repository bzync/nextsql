package crypto

import (
	"os"
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func FuzzDecodeKeystore(f *testing.F) {
	path := f.TempDir() + "/db.keys"
	root, err := GenerateDEK(1)
	if err != nil {
		f.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		f.Fatal(err)
	}
	env, err := CreateEnvelope(path, id, root)
	if err != nil {
		f.Fatal(err)
	}
	_ = env.Close()
	good, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte("NSKS"))
	f.Add([]byte("NSSH"))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		ks, err := decodeKeystore(raw)
		if err != nil {
			return
		}
		if ks.Shredded {
			return
		}
		_, _ = encodeKeystore(ks)
	})
}
