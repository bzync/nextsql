package crypto

import (
	"bytes"
	"os"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
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
	good, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	// Seed a v2 keystore too, so the recovery branch of the decoder is
	// reachable from a realistic starting point and not only from mutations
	// that happen to guess the version field.
	rec, err := GenerateDEK(env.NextRecoveryKeyVersion())
	if err != nil {
		f.Fatal(err)
	}
	if err := env.SetRecoveryKey(rec); err != nil {
		f.Fatal(err)
	}
	_ = env.Close()
	goodV2, err := os.ReadFile(path)
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add(goodV2)
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
		out, err := encodeKeystore(ks)
		if err != nil {
			return
		}
		// Anything the decoder accepts must re-encode to something the
		// decoder still accepts, with the recovery fields carried through
		// intact — a keystore that loses its recovery wrap on a rewrite
		// would silently strip the operator's second unlock path.
		back, err := decodeKeystore(out)
		if err != nil {
			t.Fatalf("re-encoded keystore no longer decodes: %v", err)
		}
		if back.RecoveryVersion != ks.RecoveryVersion {
			t.Fatalf("recovery version drifted: %d -> %d", ks.RecoveryVersion, back.RecoveryVersion)
		}
		if !bytes.Equal(back.WrappedRecovery, ks.WrappedRecovery) {
			t.Fatalf("recovery wrap drifted: %x -> %x", ks.WrappedRecovery, back.WrappedRecovery)
		}
		wantVersion := keystoreV1
		if len(ks.WrappedRecovery) > 0 {
			wantVersion = keystoreV2
		}
		if got := int(encoding.U16(out, 4)); got != wantVersion {
			t.Fatalf("encoded version = %d, want %d", got, wantVersion)
		}
	})
}
