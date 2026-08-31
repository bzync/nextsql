package hosting

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
)

func TestDeploymentBootstrapParseBuildAndRegistryReapply(t *testing.T) {
	dir := t.TempDir()
	keysDir := filepath.Join(dir, "keys")
	if err := os.MkdirAll(keysDir, 0o700); err != nil {
		t.Fatal(err)
	}
	keyA := filepath.Join(keysDir, "a.key")
	keyB := filepath.Join(keysDir, "b.key")
	createRootFile(t, keyA)
	createRootFile(t, keyB)
	raw := []byte(`version: 1
default:
  realm: Customer-A
  database: Production
realms:
  - name: Customer-B
    databases:
      - name: Analytics
        key_file: keys/b.key
  - name: Customer-A
    databases:
      - name: Production
        key_file: keys/a.key
`)
	bootstrap, err := ParseDeploymentBootstrap(raw, dir)
	if err != nil {
		t.Fatal(err)
	}
	if bootstrap.DefaultRealm != "customer-a" || bootstrap.DefaultDatabase != "production" || len(bootstrap.Realms) != 2 {
		t.Fatalf("unexpected bootstrap: %+v", bootstrap)
	}
	if bootstrap.Realms[0].Name != "customer-a" || bootstrap.Realms[0].Databases[0].KeyFile != keyA {
		t.Fatalf("bootstrap was not normalized deterministically: %+v", bootstrap)
	}

	registryDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(registryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	root := testRoot(t)
	defer root.Zero()
	build := func(deployment ID) (Manifest, error) {
		return bootstrap.RegistryManifest(deployment, StateProvisioning)
	}
	reg, created, err := EnsureManifest(Path(registryDir), root, build)
	if err != nil {
		t.Fatal(err)
	}
	if !created {
		t.Fatal("new declarative registry was not published")
	}
	first := reg.Manifest()
	for _, realm := range first.Realms {
		for _, database := range realm.Databases {
			if database.State != StateProvisioning || database.Layout != LayoutManaged || database.KeyRef == "" {
				t.Fatalf("database published inconsistently: %+v", database)
			}
		}
	}
	if err := reg.SetAllDatabaseStates(StateActive); err != nil {
		t.Fatal(err)
	}
	active := reg.Manifest()
	if active.Generation <= first.Generation {
		t.Fatalf("activation did not advance generation: first=%d active=%d", first.Generation, active.Generation)
	}
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}

	reapplied, created, err := EnsureManifest(Path(registryDir), root, build)
	if err != nil {
		t.Fatal(err)
	}
	defer reapplied.Close()
	if created || reapplied.Manifest().Generation != active.Generation {
		t.Fatalf("reapply mutated registry: created=%t got=%d want=%d", created, reapplied.Manifest().Generation, active.Generation)
	}

	// Renaming a non-default database changes its stable derived identity. A
	// reapply that carries a different identity than the published registry must
	// fail closed as a conflict rather than silently rebinding storage.
	mutated := cloneBootstrap(bootstrap)
	mutated.Realms[1].Databases[0].Name = "renamed"
	if _, _, err := EnsureManifest(Path(registryDir), root, func(deployment ID) (Manifest, error) {
		return mutated.RegistryManifest(deployment, StateProvisioning)
	}); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("immutable identity mutation was accepted: %v", err)
	}
}

func TestDeploymentBootstrapRejectsUnknownDuplicateAndUnsafeYAML(t *testing.T) {
	dir := t.TempDir()
	keyA := filepath.Join(dir, "a.key")
	keyB := filepath.Join(dir, "b.key")
	createRootFile(t, keyA)
	copyFile(t, keyA, keyB)
	base := `version: 1
default: {realm: a, database: one}
realms:
  - name: a
    databases:
      - {name: one, key_file: a.key}
`
	if _, err := ParseDeploymentBootstrap([]byte(base+"unknown: true\n"), dir); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("unknown field: %v", err)
	}
	duplicateRoot := strings.Replace(base, "      - {name: one, key_file: a.key}\n", "      - {name: one, key_file: a.key}\n      - {name: two, key_file: b.key}\n", 1)
	if _, err := ParseDeploymentBootstrap([]byte(duplicateRoot), dir); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("duplicate root material: %v", err)
	}
	if _, err := ParseDeploymentBootstrap([]byte(base+"---\nversion: 1\n"), dir); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("multiple documents: %v", err)
	}
	alias := `version: 1
default: {realm: a, database: one}
realms:
  - &realm
    name: a
    databases:
      - {name: one, key_file: a.key}
`
	if _, err := ParseDeploymentBootstrap([]byte(alias), dir); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("YAML anchor: %v", err)
	}
}

func TestManagedDatabasePathUsesOnlyStableIDs(t *testing.T) {
	deployment, err := newID()
	if err != nil {
		t.Fatal(err)
	}
	realm := deriveRealmID(deployment, "realm")
	identity := deriveDatabaseIdentity(deployment, "realm", "database")
	got := ManagedDatabasePath("/data", realm, ID(identity.Database))
	want := filepath.Join("/data", "realms", realm.String(), "databases", ID(identity.Database).String(), "nextsql.db")
	if got != want {
		t.Fatalf("ManagedDatabasePath=%q want %q", got, want)
	}
}

func cloneBootstrap(in DeploymentBootstrap) DeploymentBootstrap {
	out := in
	out.Realms = make([]BootstrapRealm, len(in.Realms))
	for i := range in.Realms {
		out.Realms[i] = in.Realms[i]
		out.Realms[i].Databases = append([]BootstrapDatabase(nil), in.Realms[i].Databases...)
	}
	return out
}

func createRootFile(t *testing.T, path string) {
	t.Helper()
	root, err := crypto.CreateKeyFile(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	root.Zero()
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	raw, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, raw, 0o600); err != nil {
		t.Fatal(err)
	}
}
