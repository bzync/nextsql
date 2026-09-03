package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// initBaseDeployment bootstraps a fresh deployment (default realm
// "default", database "default") the way TestInitLegacyRefusalDoesNotCreateRootFiles
// and friends already do, returning the paths realm/database create tests
// build on.
func initBaseDeployment(t *testing.T) (dir, keyFile, instanceKeyFile string) {
	t.Helper()
	dir = t.TempDir()
	secrets := t.TempDir()
	keyFile = filepath.Join(secrets, "database.key")
	instanceKeyFile = filepath.Join(secrets, "instance.key")
	if err := initDB([]string{
		"--data-dir", dir,
		"--key-file", keyFile,
		"--instance-key-file", instanceKeyFile,
	}); err != nil {
		t.Fatal(err)
	}
	return dir, keyFile, instanceKeyFile
}

func openTestRegistry(t *testing.T, dir, instanceKeyFile string) hosting.Manifest {
	t.Helper()
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	reg, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	return reg.Manifest()
}

func TestRealmCreateAddsRealmAndActivatesDatabase(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	dbKeyFile := filepath.Join(t.TempDir(), "customer-b.key")

	if err := createRealm([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", "Customer-B", "--database", "Production", "--database-key-file", dbKeyFile,
		"--buffer-pages", "8",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(dbKeyFile); err != nil {
		t.Fatalf("database key file was not created: %v", err)
	}

	m := openTestRegistry(t, dir, instanceKeyFile)
	var found *hosting.Realm
	for i := range m.Realms {
		if m.Realms[i].Name == "customer-b" {
			found = &m.Realms[i]
		}
	}
	if found == nil {
		t.Fatalf("realm customer-b not found: %+v", m.Realms)
	}
	if len(found.Databases) != 1 || found.Databases[0].Name != "production" {
		t.Fatalf("unexpected databases: %+v", found.Databases)
	}
	db := found.Databases[0]
	if db.State != hosting.StateActive {
		t.Fatalf("database was not activated: %+v", db)
	}
	if db.Layout != hosting.LayoutManaged {
		t.Fatalf("expected LayoutManaged, got %v", db.Layout)
	}

	// The physical file exists at the ID-based managed path and opens.
	managedPath := hosting.ManagedDatabasePath(dir, found.ID, db.ID)
	root, err := crypto.ReadKeyFile(dbKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	env, err := crypto.OpenEnvelope(crypto.KeystorePath(managedPath), root)
	if err != nil {
		t.Fatalf("managed database keystore did not open: %v", err)
	}
	defer env.Close()
	if env.Identity() != db.Identity {
		t.Fatal("managed database identity does not match the registry record")
	}
}

func TestRealmCreateIsIdempotentOnRetry(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	dbKeyFile := filepath.Join(t.TempDir(), "customer-b.key")
	args := []string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", "customer-b", "--database", "production", "--database-key-file", dbKeyFile,
	}
	if err := createRealm(args); err != nil {
		t.Fatal(err)
	}
	// A second, identical call (as a caller would retry after a crash, or
	// simply re-run the same command) must succeed as a no-op, not error.
	if err := createRealm(args); err != nil {
		t.Fatalf("idempotent retry failed: %v", err)
	}
	m := openTestRegistry(t, dir, instanceKeyFile)
	count := 0
	for _, r := range m.Realms {
		if r.Name == "customer-b" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly one customer-b realm, found %d", count)
	}
}

func TestRealmCreateResumesAfterCrashBeforePhysicalFile(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	dbKeyFile := filepath.Join(t.TempDir(), "customer-b.key")

	// Simulate a crash between the durable PROVISIONING registry write and
	// the physical database file ever being created: call the registry
	// primitive directly, the way createRealm's first half would, but
	// never reach activateManagedDatabase.
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := reg.CreateRealm("customer-b", "production", ident, dbKeyFile); err != nil {
		t.Fatal(err)
	}
	if err := reg.Close(); err != nil {
		t.Fatal(err)
	}
	root.Zero()
	if _, err := os.Stat(dbKeyFile); !os.IsNotExist(err) {
		t.Fatalf("test setup: database key file should not exist yet: %v", err)
	}

	// The CLI command, run fresh, must recognize the resumable
	// PROVISIONING record (reusing its already-durable identity) and
	// finish the job rather than erroring or duplicating it.
	if err := createRealm([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", "customer-b", "--database", "production", "--database-key-file", dbKeyFile,
	}); err != nil {
		t.Fatalf("resume after simulated crash: %v", err)
	}

	m := openTestRegistry(t, dir, instanceKeyFile)
	for _, r := range m.Realms {
		if r.Name != "customer-b" {
			continue
		}
		if len(r.Databases) != 1 || r.Databases[0].State != hosting.StateActive {
			t.Fatalf("resume did not activate the database: %+v", r.Databases)
		}
		if r.Databases[0].Identity != ident {
			t.Fatal("resume must reuse the already-registered identity, not generate a new one")
		}
		return
	}
	t.Fatal("realm customer-b not found after resume")
}

func TestDatabaseCreateAddsToExistingRealm(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	m := openTestRegistry(t, dir, instanceKeyFile)
	defaultRealmName := m.Realms[0].Name

	dbKeyFile := filepath.Join(t.TempDir(), "staging.key")
	if err := createDatabase([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", defaultRealmName, "--name", "Staging", "--database-key-file", dbKeyFile,
	}); err != nil {
		t.Fatal(err)
	}

	m2 := openTestRegistry(t, dir, instanceKeyFile)
	var realm hosting.Realm
	for _, r := range m2.Realms {
		if r.Name == defaultRealmName {
			realm = r
		}
	}
	if len(realm.Databases) != 2 {
		t.Fatalf("expected 2 databases in %s, got %d: %+v", defaultRealmName, len(realm.Databases), realm.Databases)
	}
	var staging *hosting.Database
	for i := range realm.Databases {
		if realm.Databases[i].Name == "staging" {
			staging = &realm.Databases[i]
		}
	}
	if staging == nil || staging.State != hosting.StateActive {
		t.Fatalf("staging database not active: %+v", realm.Databases)
	}
}

func TestDatabaseCreateRejectsUnknownRealm(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	dbKeyFile := filepath.Join(t.TempDir(), "x.key")
	err := createDatabase([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", "does-not-exist", "--name", "x", "--database-key-file", dbKeyFile,
	})
	if !nerr.HasCode(err, nerr.NotFound) {
		t.Fatalf("expected NotFound, got %v", err)
	}
}

func TestRealmCreateRequiresRealmAndDatabase(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	err := createRealm([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--database-key-file", filepath.Join(t.TempDir(), "x.key"),
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("expected InvalidArgument for missing --realm/--database, got %v", err)
	}
}

func TestRealmCreateRequiresDeploymentLock(t *testing.T) {
	dir, keyFile, instanceKeyFile := initBaseDeployment(t)
	held, err := hosting.AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	err = createRealm([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", "customer-b", "--database", "production",
		"--database-key-file", filepath.Join(t.TempDir(), "x.key"),
	})
	if !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("expected Unavailable under an existing deployment lock, got %v", err)
	}
}
