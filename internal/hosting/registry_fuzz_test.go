package hosting

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func FuzzDecodeManifest(f *testing.F) {
	deployment, err := newID()
	if err != nil {
		f.Fatal(err)
	}
	identity, err := format.NewIdentity()
	if err != nil {
		f.Fatal(err)
	}
	realmID := deriveRealmID(deployment, "default")
	seed, err := EncodeManifest(Manifest{
		DeploymentID:    deployment,
		Generation:      1,
		DefaultRealm:    realmID,
		DefaultDatabase: ID(identity.Database),
		Realms: []Realm{{
			ID:    realmID,
			Name:  "default",
			State: StateActive,
			Databases: []Database{{
				ID:       ID(identity.Database),
				Name:     "default",
				State:    StateActive,
				Layout:   LayoutLegacyDefault,
				Identity: identity,
			}},
		}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NSRM\x01\x00"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		manifest, err := DecodeManifest(raw)
		if err != nil {
			return
		}
		encoded, err := EncodeManifest(manifest)
		if err != nil {
			t.Fatalf("decoded manifest did not re-encode: %v", err)
		}
		if _, err := DecodeManifest(encoded); err != nil {
			t.Fatalf("re-encoded manifest did not decode: %v", err)
		}
	})
}
