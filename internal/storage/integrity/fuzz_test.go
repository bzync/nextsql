package integrity

import "testing"

func FuzzDecodeRegistry(f *testing.F) {
	f.Add([]byte("NSQI"))
	f.Add(encodeRegistry(nil))
	f.Fuzz(func(t *testing.T, raw []byte) {
		pages, err := decodeRegistry(raw)
		if err != nil {
			return
		}
		_ = encodeRegistry(pages)
	})
}
