package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestInitBootstrapsDeploymentRegistry(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "database.key")
	instanceKeyFile := filepath.Join(t.TempDir(), "instance.key")
	args := []string{
		"--data-dir", dir,
		"--key-file", keyFile,
		"--instance-key-file", instanceKeyFile,
		"--realm", "Customer-A",
		"--database", "Production",
		"--buffer-pages", "8",
	}
	if err := initDB(args); err != nil {
		t.Fatal(err)
	}
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	realm, db, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}
	if realm.Name != "customer-a" || db.Name != "production" || db.State != hosting.StateActive {
		t.Fatalf("unexpected bootstrap: realm=%+v database=%+v", realm, db)
	}
	if _, err := os.Stat(filepath.Join(dir, "nextsql.db")); err != nil {
		t.Fatal(err)
	}
	if err := initDB(args); !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("second init: %v", err)
	}
}

func TestInitResolvesHostingFromDotenv(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	keyFile := filepath.Join(secrets, "database.key")
	instanceKeyFile := filepath.Join(secrets, "instance.key")
	envFile := filepath.Join(t.TempDir(), "hosting.env")
	body := strings.Join([]string{
		"NEXTSQL_DATA_DIR=" + dir,
		"NEXTSQL_KEY_FILE=" + keyFile,
		"NEXTSQL_INSTANCE_KEY_FILE=" + instanceKeyFile,
		"NEXTSQL_REALM_NAME=dotenv-realm",
		"NEXTSQL_DATABASE=dotenv-db",
		"NEXTSQL_SERVER_USER=admin",
		"NEXTSQL_SERVER_PASS=secret",
		"NEXTSQL_BUFFER_PAGES=8",
		"",
	}, "\n")
	if err := os.WriteFile(envFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := initDB([]string{"--env-file", envFile}); err != nil {
		t.Fatal(err)
	}
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	registry, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	realm, database, err := registry.Default()
	if err != nil {
		t.Fatal(err)
	}
	if realm.Name != "dotenv-realm" || database.Name != "dotenv-db" || database.State != hosting.StateActive {
		t.Fatalf("dotenv bootstrap: realm=%+v database=%+v", realm, database)
	}
	users, err := auth.Open(filepath.Join(dir, config.AuthFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Verify("admin", "secret"); err != nil {
		t.Fatal(err)
	}
}

func TestInitResumesProvisioningAfterCredentialError(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	keyFile := filepath.Join(secrets, "database.key")
	instanceKeyFile := filepath.Join(secrets, "instance.key")
	args := []string{
		"--data-dir", dir,
		"--key-file", keyFile,
		"--instance-key-file", instanceKeyFile,
		"--realm", "default",
		"--database", "default",
		"--buffer-pages", "8",
		"--user", "admin",
	}
	if err := initDB(args); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("missing password file: %v", err)
	}
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	reg, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	_, db, err := reg.Default()
	_ = reg.Close()
	if err != nil || db.State != hosting.StateProvisioning {
		t.Fatalf("failed init was published: database=%+v err=%v", db, err)
	}

	passwordFile := filepath.Join(secrets, "admin.pw")
	if err := os.WriteFile(passwordFile, []byte("secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args = append(args, "--password-file", passwordFile)
	if err := initDB(args); err != nil {
		t.Fatal(err)
	}
	users, err := auth.Open(filepath.Join(dir, "nextsql.users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := users.Verify("admin", "secret"); err != nil {
		t.Fatal(err)
	}
}

func TestInitLegacyRefusalDoesNotCreateRootFiles(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "nextsql.db"), []byte("legacy"), 0o600); err != nil {
		t.Fatal(err)
	}
	secrets := t.TempDir()
	keyFile := filepath.Join(secrets, "database.key")
	instanceKeyFile := filepath.Join(secrets, "instance.key")
	err := initDB([]string{
		"--data-dir", dir,
		"--key-file", keyFile,
		"--instance-key-file", instanceKeyFile,
	})
	if !nerr.HasCode(err, nerr.AlreadyExists) {
		t.Fatalf("legacy init: %v", err)
	}
	for _, path := range []string{keyFile, instanceKeyFile} {
		if _, statErr := os.Stat(path); !os.IsNotExist(statErr) {
			t.Fatalf("refused init created %s: %v", path, statErr)
		}
	}
}

func TestHostingAdoptRegistersExistingIdentityAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	keyFile := filepath.Join(secrets, "database.key")
	instanceKeyFile := filepath.Join(secrets, "instance.key")
	want := createLegacyDatabase(t, dir, keyFile)
	if err := os.WriteFile(filepath.Join(dir, "unregistered-sibling"), []byte("leave me"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := []string{
		"--data-dir", dir,
		"--key-file", keyFile,
		"--instance-key-file", instanceKeyFile,
		"--realm", "Customer-A",
		"--database", "Production",
		"--buffer-pages", "8",
		"--confirm",
	}
	if err := adoptLegacyDatabase(args); err != nil {
		t.Fatal(err)
	}
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	registry, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	realm, database, err := registry.Default()
	_ = registry.Close()
	if err != nil {
		t.Fatal(err)
	}
	if realm.Name != "customer-a" || database.Name != "production" || database.State != hosting.StateActive || database.Identity != want {
		t.Fatalf("unexpected adoption: realm=%+v database=%+v", realm, database)
	}
	if err := adoptLegacyDatabase(args); err != nil {
		t.Fatalf("idempotent rerun: %v", err)
	}
	mismatch := append([]string(nil), args...)
	for i := range mismatch {
		if mismatch[i] == "Production" {
			mismatch[i] = "Other"
		}
	}
	if err := adoptLegacyDatabase(mismatch); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("mismatched rerun: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(dir, "unregistered-sibling")); err != nil || string(got) != "leave me" {
		t.Fatalf("sibling file changed: %q %v", got, err)
	}
}

func TestHostingStorageCapsCLI(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	keyFile := filepath.Join(secrets, "database.key")
	instanceKeyFile := filepath.Join(secrets, "instance.key")
	createLegacyDatabase(t, dir, keyFile)
	if err := adoptLegacyDatabase([]string{
		"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile,
		"--realm", "Customer-A", "--database", "Production", "--buffer-pages", "8", "--confirm",
	}); err != nil {
		t.Fatal(err)
	}

	base := []string{"--data-dir", dir, "--key-file", keyFile, "--instance-key-file", instanceKeyFile}

	// A running deployment (data-dir lock held) blocks a cap change.
	held, err := hosting.AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := setRealmStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--cap-bytes", "1000", "--confirm")); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("cap change under deployment lock: %v", err)
	}
	if err := held.Close(); err != nil {
		t.Fatal(err)
	}

	// Mutating verbs require --confirm.
	if err := setRealmStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--cap-bytes", "1000")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("missing --confirm: %v", err)
	}

	if err := setRealmStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--cap-bytes", "1000000", "--confirm")); err != nil {
		t.Fatal(err)
	}
	// A per-database cap above the realm cap is rejected.
	if err := setDatabaseStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--database", "production", "--cap-bytes", "2000000", "--confirm")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("db cap over realm cap: %v", err)
	}
	if err := setDatabaseStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--database", "production", "--cap-bytes", "500000", "--confirm")); err != nil {
		t.Fatal(err)
	}
	// Lowering the realm cap below the database cap is rejected.
	if err := setRealmStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--cap-bytes", "100000", "--confirm")); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("realm cap below db cap: %v", err)
	}

	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	reg, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	m := reg.Manifest()
	_ = reg.Close()
	if m.Realms[0].StorageCapBytes != 1000000 || m.Realms[0].Databases[0].StorageCapBytes != 500000 {
		t.Fatalf("caps not persisted: %+v", m.Realms[0])
	}

	// Clearing caps round-trips through 0.
	if err := setDatabaseStorageCap(append(append([]string(nil), base...), "--realm", "customer-a", "--database", "production", "--cap-bytes", "0", "--confirm")); err != nil {
		t.Fatal(err)
	}
	if err := showHostingRegistry(base); err != nil {
		t.Fatal(err)
	}

	// Realm-root delegation: admin sets a secret, then a realm-root secret
	// holder sets its own database's cap (bounded by the realm cap) but has no
	// path to the realm cap.
	secretFile := filepath.Join(secrets, "realm-root.secret")
	if err := os.WriteFile(secretFile, []byte("realm-root-secret-value-123\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	rootArgs := append(append([]string(nil), base...), "--realm", "customer-a", "--database", "production", "--realm-secret-file", secretFile, "--cap-bytes", "700000", "--confirm")
	// No delegation yet.
	if err := setDatabaseStorageCap(rootArgs); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("realm-root before delegation: %v", err)
	}
	if err := setRealmRootAuth(append(append([]string(nil), base...), "--realm", "customer-a", "--secret-file", secretFile, "--confirm")); err != nil {
		t.Fatal(err)
	}
	if err := setDatabaseStorageCap(rootArgs); err != nil {
		t.Fatalf("realm-root cap set: %v", err)
	}
	// Above the realm cap (1000000) is rejected for the realm root too.
	over := append(append([]string(nil), base...), "--realm", "customer-a", "--database", "production", "--realm-secret-file", secretFile, "--cap-bytes", "3000000", "--confirm")
	if err := setDatabaseStorageCap(over); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("realm-root over ceiling: %v", err)
	}
	root2, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root2.Zero()
	reg2, err := hosting.Open(hosting.Path(dir), root2)
	if err != nil {
		t.Fatal(err)
	}
	m2 := reg2.Manifest()
	_ = reg2.Close()
	if m2.Realms[0].Databases[0].StorageCapBytes != 700000 || m2.Realms[0].StorageCapBytes != 1000000 {
		t.Fatalf("realm-root path changed the wrong cap: %+v", m2.Realms[0])
	}
	if m2.Realms[0].RealmRootAuthHash == ([32]byte{}) {
		t.Fatal("realm-root delegation not persisted")
	}
	// Clearing delegation revokes access.
	if err := setRealmRootAuth(append(append([]string(nil), base...), "--realm", "customer-a", "--clear", "--confirm")); err != nil {
		t.Fatal(err)
	}
	if err := setDatabaseStorageCap(rootArgs); !nerr.HasCode(err, nerr.Forbidden) {
		t.Fatalf("realm-root after clear: %v", err)
	}
}

func TestHostingAdoptRequiresOfflineLockAndConfirmation(t *testing.T) {
	dir := t.TempDir()
	keyFile := filepath.Join(t.TempDir(), "database.key")
	createLegacyDatabase(t, dir, keyFile)
	args := []string{"--data-dir", dir, "--key-file", keyFile}
	if err := adoptLegacyDatabase(args); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("missing confirmation: %v", err)
	}
	lock, err := hosting.AcquireDataDirLock(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := adoptLegacyDatabase(append(args, "--confirm")); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("online adoption: %v", err)
	}
	if _, err := os.Stat(hosting.Path(dir)); !os.IsNotExist(err) {
		t.Fatalf("locked adoption published registry: %v", err)
	}
}

func TestHostingAdoptResolvesDotenvIncludingConfirmation(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	keyFile := filepath.Join(secrets, "database.key")
	instanceKeyFile := filepath.Join(secrets, "instance.key")
	want := createLegacyDatabase(t, dir, keyFile)
	envFile := filepath.Join(t.TempDir(), "hosting.env")
	body := strings.Join([]string{
		"NEXTSQL_DATA_DIR=" + dir,
		"NEXTSQL_KEY_FILE=" + keyFile,
		"NEXTSQL_INSTANCE_KEY_FILE=" + instanceKeyFile,
		"NEXTSQL_REALM_NAME=dotenv-realm",
		"NEXTSQL_DATABASE=dotenv-db",
		"NEXTSQL_BUFFER_PAGES=8",
		"NEXTSQL_HOSTING_CONFIRM=true",
		"",
	}, "\n")
	if err := os.WriteFile(envFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := adoptLegacyDatabase([]string{"--env-file", envFile}); err != nil {
		t.Fatal(err)
	}
	root, err := crypto.ReadKeyFile(instanceKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	registry, err := hosting.Open(hosting.Path(dir), root)
	if err != nil {
		t.Fatal(err)
	}
	defer registry.Close()
	realm, database, err := registry.Default()
	if err != nil {
		t.Fatal(err)
	}
	if realm.Name != "dotenv-realm" || database.Name != "dotenv-db" || database.Identity != want || database.State != hosting.StateActive {
		t.Fatalf("dotenv adoption: realm=%+v database=%+v", realm, database)
	}
}

func createLegacyDatabase(t *testing.T, dir, keyFile string) format.Identity {
	t.Helper()
	root, err := crypto.CreateKeyFile(keyFile, 1)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Zero()
	identity, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(dir, config.DataFileName)
	envelope, err := crypto.CreateEnvelope(crypto.KeystorePath(dbPath), identity, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, identity, envelope, 8)
	if err != nil {
		_ = envelope.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		_ = envelope.Close()
		t.Fatal(err)
	}
	if err := envelope.Close(); err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestHostingMigrateTenantPublishesOnlyVerifiedIsolatedDatabase(t *testing.T) {
	sourceDir := t.TempDir()
	secrets := t.TempDir()
	sourceKey := filepath.Join(secrets, "source.key")
	createLegacyDatabase(t, sourceDir, sourceKey)
	sourceRoot, err := crypto.ReadKeyFile(sourceKey)
	if err != nil {
		t.Fatal(err)
	}
	sourcePath := filepath.Join(sourceDir, config.DataFileName)
	sourceEnvelope, err := crypto.OpenEnvelope(crypto.KeystorePath(sourcePath), sourceRoot)
	if err != nil {
		t.Fatal(err)
	}
	sourceDB, err := executor.Open(sourcePath, sourceEnvelope, 8)
	if err != nil {
		t.Fatal(err)
	}
	sourceSession := sourceDB.Session()
	if _, err := sourceSession.Exec(`CREATE TABLE orders (
		id STRING PRIMARY KEY,
		tenant_id STRING NOT NULL,
		value STRING NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := sourceSession.Exec(`INSERT INTO orders (id, tenant_id, value) VALUES
		('a1', 'tenant-a', 'alpha'), ('b1', 'tenant-b', 'beta')`); err != nil {
		t.Fatal(err)
	}
	if err := sourceDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := sourceEnvelope.Close(); err != nil {
		t.Fatal(err)
	}
	sourceRoot.Zero()

	destDir := filepath.Join(t.TempDir(), "isolated")
	destKey := filepath.Join(secrets, "dest.key")
	instanceKey := filepath.Join(secrets, "instance.key")
	args := []string{
		"--source-data-dir", sourceDir,
		"--source-key-file", sourceKey,
		"--tenant", "tenant-a",
		"--data-dir", destDir,
		"--key-file", destKey,
		"--instance-key-file", instanceKey,
		"--realm", "Customer-A",
		"--database", "Production",
		"--buffer-pages", "8",
		"--batch-rows", "1",
	}
	if err := migrateLegacyTenant(args); !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("missing confirmation: %v", err)
	}
	if _, err := os.Stat(destDir); !os.IsNotExist(err) {
		t.Fatalf("unconfirmed migration mutated destination: %v", err)
	}
	args = append(args, "--confirm")
	if err := migrateLegacyTenant(args); err != nil {
		t.Fatal(err)
	}

	instanceRoot, err := crypto.ReadKeyFile(instanceKey)
	if err != nil {
		t.Fatal(err)
	}
	registry, err := hosting.Open(hosting.Path(destDir), instanceRoot)
	if err != nil {
		t.Fatal(err)
	}
	realm, database, err := registry.Default()
	if err != nil {
		t.Fatal(err)
	}
	if realm.Name != "customer-a" || database.Name != "production" || database.State != hosting.StateActive {
		t.Fatalf("destination published incorrectly: realm=%+v database=%+v", realm, database)
	}
	_ = registry.Close()
	instanceRoot.Zero()

	destRoot, err := crypto.ReadKeyFile(destKey)
	if err != nil {
		t.Fatal(err)
	}
	destPath := filepath.Join(destDir, config.DataFileName)
	destEnvelope, err := crypto.OpenEnvelope(crypto.KeystorePath(destPath), destRoot)
	if err != nil {
		t.Fatal(err)
	}
	intent, err := hosting.ReadTenantMigrationIntent(hosting.TenantMigrationPath(destDir), destEnvelope.Provider(crypto.DomainTemp))
	if err != nil {
		t.Fatal(err)
	}
	if intent.State != hosting.TenantMigrationComplete || intent.Tables != 1 || intent.Rows != 1 {
		t.Fatalf("migration intent %+v", intent)
	}
	destDB, err := executor.Open(destPath, destEnvelope, 8)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := destDB.Session().Exec(`SELECT id, legacy_tenant_id, value FROM orders`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0].Str != "a1" || rows.Rows[0][1].Str != "tenant-a" {
		t.Fatalf("isolated rows %+v", rows.Rows)
	}
	if err := destDB.Close(); err != nil {
		t.Fatal(err)
	}
	if err := destEnvelope.Close(); err != nil {
		t.Fatal(err)
	}
	destRoot.Zero()

	if err := migrateLegacyTenant(args); err != nil {
		t.Fatalf("exact retry: %v", err)
	}
	changed := append([]string(nil), args...)
	for i := 0; i+1 < len(changed); i++ {
		if changed[i] == "--tenant" {
			changed[i+1] = "tenant-b"
		}
	}
	if err := migrateLegacyTenant(changed); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("changed tenant retry: %v", err)
	}
}

func TestExecSQLTextPositional(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	c := fs.String("c", "", "")
	if err := fs.Parse([]string{"SELECT 1"}); err != nil {
		t.Fatal(err)
	}
	got, err := execSQLText(fs, *c)
	if err != nil || got != "SELECT 1" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestExecSQLTextDashCWins(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	c := fs.String("c", "", "")
	if err := fs.Parse([]string{"-c", "SELECT 2"}); err != nil {
		t.Fatal(err)
	}
	got, err := execSQLText(fs, *c)
	if err != nil || got != "SELECT 2" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestExecSQLTextDashCWinsOverPositional(t *testing.T) {
	fs := flag.NewFlagSet("exec", flag.ContinueOnError)
	c := fs.String("c", "", "")
	if err := fs.Parse([]string{"-c", "SELECT 1", "SELECT 2"}); err != nil {
		t.Fatal(err)
	}
	got, err := execSQLText(fs, *c)
	if err != nil || got != "SELECT 1" {
		t.Fatalf("%q %v", got, err)
	}
}

func TestExecSQLRequiresSQL(t *testing.T) {
	t.Setenv("NEXTSQL_DATABASE_USER", "app")
	t.Setenv("NEXTSQL_DATABASE_PASS", "secret")
	t.Setenv("NEXTSQL_INSECURE", "true")
	err := execSQL([]string{"--no-env"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "SQL is required") {
		t.Fatalf("%v", err)
	}
}

func TestExecSQLRejectsDataDirFlag(t *testing.T) {
	err := execSQL([]string{"--no-env", "--data-dir", "/tmp"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "local command") {
		t.Fatalf("%v", err)
	}
	if strings.Contains(err.Error(), "SQL is required") {
		t.Fatalf("mode error should precede SQL-required: %v", err)
	}
}

func TestExecSQLRejectsURLAddress(t *testing.T) {
	err := execSQL([]string{
		"--no-env",
		"--addr", "nextsql://127.0.0.1:7210",
		"--user", "app",
		"--insecure",
		"-c", "SELECT 1",
	})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "host:port") {
		t.Fatalf("%v", err)
	}
}

func TestStatusRejectsAddrAndLocal(t *testing.T) {
	err := statusDB([]string{"--no-env", "--local", "--addr", "127.0.0.1:7210"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "--local or --addr") {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitUsage {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestStatusLocalMissingDataDir(t *testing.T) {
	t.Setenv("NEXTSQL_DATA_DIR", "")
	t.Setenv("NEXTSQL_KEY_FILE", "")
	err := statusDB([]string{"--no-env", "--local"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "--data-dir and --key-file") {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitLocal {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestStatusServerRejectsDataDir(t *testing.T) {
	err := statusDB([]string{"--no-env", "--data-dir", "/tmp"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if err == nil || !strings.Contains(err.Error(), "status --local") {
		t.Fatalf("%v", err)
	}
}

func TestStatusServerRequiresUser(t *testing.T) {
	t.Setenv("NEXTSQL_DATABASE_USER", "")
	t.Setenv("NEXTSQL_DATABASE_PASS", "")
	t.Setenv("NEXTSQL_DATABASE_PASSWORD_FILE", "")
	err := statusDB([]string{"--no-env", "--insecure"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitUsage {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestLocalCommandsMissingDataDirExit7(t *testing.T) {
	cases := []struct {
		name string
		fn   func() error
	}{
		{"init", func() error { return initDB(nil) }},
		{"diagnose", func() error { return diagnoseDB(nil) }},
		{"cluster", func() error { return clusterCmd([]string{"status"}) }},
		{"backup", func() error { return backupDB([]string{"--out", "/tmp/out"}) }},
		{"verify", func() error { return verifyBackup([]string{"--from", "/tmp/from"}) }},
	}
	for _, tc := range cases {
		err := tc.fn()
		if cli.Code(err) != cli.ExitLocal {
			t.Errorf("%s: Code(%v)=%d want %d", tc.name, err, cli.Code(err), cli.ExitLocal)
		}
	}
}

func TestUnknownCommandExitUsage(t *testing.T) {
	err := run([]string{"not-a-command"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
	if cli.Code(err) != cli.ExitUsage {
		t.Fatalf("code %d", cli.Code(err))
	}
}

func TestExecSQLRequiresPassword(t *testing.T) {
	t.Setenv("NEXTSQL_DATABASE_PASS", "")
	t.Setenv("NEXTSQL_DATABASE_PASSWORD_FILE", "")
	err := execSQL([]string{"--no-env", "--user", "app", "--insecure", "-c", "SELECT 1"})
	if !nerr.HasCode(err, nerr.InvalidArgument) {
		t.Fatalf("%v", err)
	}
}

func TestMigrateRequiresSubcommand(t *testing.T) {
	err := migrateCmd(nil)
	if !nerr.HasCode(err, nerr.InvalidArgument) || !strings.Contains(err.Error(), "expected status") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateRejectsUnknown(t *testing.T) {
	err := migrateCmd([]string{"bogus", "--no-env"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || !strings.Contains(err.Error(), "unknown migrate command") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateDownRequiresUser(t *testing.T) {
	err := migrateDown([]string{"--no-env"})
	if err == nil || !nerr.HasCode(err, nerr.InvalidArgument) || !strings.Contains(err.Error(), "user is required") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateForceRequiresConfirm(t *testing.T) {
	err := migrateForce([]string{"--no-env", "20260818120000"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateRepairRequiresConfirm(t *testing.T) {
	err := migrateRepair([]string{"--no-env"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "--confirm") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateValidateOK(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "migrate", "testdata", "ok")
	if err := migrateValidate([]string{"--no-env", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateValidateRejects(t *testing.T) {
	dir := filepath.Join("..", "..", "internal", "migrate", "testdata", "invalid", "begin")
	err := migrateValidate([]string{"--no-env", "--dir", dir})
	if err == nil || !strings.Contains(err.Error(), "BEGIN/COMMIT/ROLLBACK") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateValidateRejectsDataDir(t *testing.T) {
	err := migrateValidate([]string{"--no-env", "--data-dir", "/tmp"})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "local command") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateCreateAndValidate(t *testing.T) {
	dir := t.TempDir()
	if err := migrateCreate([]string{"--no-env", "--dir", dir, "add_orders"}); err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) != 2 {
		t.Fatalf("files %d", len(ents))
	}
	if err := migrateValidate([]string{"--no-env", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCreate200Rapidly(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 200; i++ {
		name := fmt.Sprintf("test_%03d", i)
		if err := migrateCreate([]string{"--no-env", "--dir", dir, name}); err != nil {
			t.Fatalf("migration %d failed: %v", i, err)
		}
	}
	if err := migrateValidate([]string{"--no-env", "--dir", dir}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 400 {
		t.Fatalf("got %d files; want 400", len(entries))
	}
}

func TestMigrateCreateRequiresName(t *testing.T) {
	err := migrateCreate([]string{"--no-env", "--dir", t.TempDir()})
	if !nerr.HasCode(err, nerr.InvalidArgument) || err == nil || !strings.Contains(err.Error(), "NAME") {
		t.Fatalf("%v", err)
	}
}

func TestMigrateDirFromEnv(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "20260818120000_t.up.sql"), []byte("ANALYZE;\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NEXTSQL_MIGRATION_DIR", dir)
	if err := migrateValidate([]string{"--no-env"}); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateCreateUsesDotenvMigrationDir(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "schema", "migrations")
	envFile := filepath.Join(root, "project.env")
	body := "NEXTSQL_MIGRATION_DIR=" + dir + "\n" +
		"NEXTSQL_ADDR=127.0.0.1:7210\n" +
		"NEXTSQL_DATABASE=app_db\n" +
		"NEXTSQL_DATABASE_USER=db_user\n" +
		"NEXTSQL_DATABASE_PASS=db_password\n" +
		"NEXTSQL_INSECURE=true\n" +
		"NEXTSQL_TLS_SERVER_NAME=db.internal\n"
	if err := os.WriteFile(envFile, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := migrateCreate([]string{"--env-file", envFile, "from_dotenv"}); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d files; want 2", len(entries))
	}
}

func TestTokenCLIRoundTrip(t *testing.T) {
	dir := t.TempDir()
	keyset := filepath.Join(dir, "token.keyset")
	pub := filepath.Join(dir, "token.keyset.pub")
	rev := filepath.Join(dir, "token.revocations")

	if err := run([]string{"token", "keygen", "--keyset", keyset}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"token", "rotate", "--keyset", keyset}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"token", "export-public", "--keyset", keyset, "--out", pub}); err != nil {
		t.Fatal(err)
	}

	// Mint against the issuer keyset, then verify against the verify-only copy.
	ks, err := auth.OpenTokenKeyset(keyset)
	if err != nil {
		t.Fatal(err)
	}
	tok, id, _, err := ks.Mint(auth.TokenMintRequest{Principal: "app", Audience: "prod", TTL: 10 * time.Minute}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"token", "verify", "--keyset", pub, "--audience", "prod", tok}); err != nil {
		t.Fatalf("verify accepted keyset rejected a good credential: %v", err)
	}
	if err := run([]string{"token", "verify", "--keyset", pub, "--audience", "staging", tok}); err == nil {
		t.Fatal("verify accepted a credential with the wrong audience")
	}

	hexID := fmt.Sprintf("%x", id[:])
	if err := run([]string{"token", "revoke", "--revocations", rev, "--token-id", hexID}); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"token", "verify", "--keyset", pub, "--revocations", rev, tok}); err == nil {
		t.Fatal("verify accepted a revoked credential")
	}
}
