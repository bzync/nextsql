package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestApplyDotenvSettingsToServerHostingConfig(t *testing.T) {
	cfg := config.Default()
	user := ""
	passwordFile := ""
	serverPass := ""
	settings := cli.Settings{
		Addr:            "127.0.0.1:9000",
		DataDir:         "/srv/nextsql",
		KeyFile:         "/run/keys/database.key",
		InstanceKeyFile: "/run/keys/instance.key",
		BufferPages:     64,
		ServerUser:      "admin",
		ServerPassFile:  "/run/secrets/admin.pw",
		ServerPass:      "inline-secret",
		Supplied: map[string]bool{
			"addr": true, "data-dir": true, "key-file": true,
			"instance-key-file": true, "buffer-pages": true,
			"server-user": true, "server-password-file": true, "server-pass": true,
		},
	}
	applyDotenvSettings(&cfg, settings, &user, &passwordFile, &serverPass)
	if cfg.ListenAddr != settings.Addr || cfg.DataDir != settings.DataDir || cfg.KeyFile != settings.KeyFile ||
		cfg.InstanceKeyFile != settings.InstanceKeyFile || cfg.BufferPages != settings.BufferPages {
		t.Fatalf("server hosting config: %+v", cfg)
	}
	if user != settings.ServerUser || passwordFile != settings.ServerPassFile || serverPass != settings.ServerPass {
		t.Fatalf("server bootstrap credentials: user=%q password_file=%q inline=%t", user, passwordFile, serverPass != "")
	}
}

func TestOpenHostedDefaultAndValidateDatabase(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	databaseKey := filepath.Join(secrets, "database.key")
	instanceKey := filepath.Join(secrets, "instance.key")
	databaseRoot := createTestKey(t, databaseKey)
	instanceRoot := createTestKey(t, instanceKey)
	identity, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	registry, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
		RealmName:        "customer-a",
		DatabaseName:     "production",
		DatabaseIdentity: identity,
		DatabaseState:    hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = registry.Close()
	dbPath := filepath.Join(dir, config.DataFileName)
	envelope, err := crypto.CreateEnvelope(crypto.KeystorePath(dbPath), identity, databaseRoot)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, identity, envelope, 8)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	_ = envelope.Close()

	cfg := config.Default()
	cfg.DataDir = dir
	cfg.KeyFile = databaseKey
	cfg.InstanceKeyFile = instanceKey
	openedRegistry, realm, hosted, err := openHostedDefault(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer openedRegistry.Close()
	if realm.Name != "customer-a" || hosted.Name != "production" {
		t.Fatalf("unexpected hosted default: realm=%+v database=%+v", realm, hosted)
	}
	keys, openedEnvelope, err := openKeys(databaseKey, crypto.KeystorePath(dbPath))
	if err != nil {
		t.Fatal(err)
	}
	defer openedEnvelope.Close()
	openedDB, err := executor.Open(dbPath, keys, 8)
	if err != nil {
		t.Fatal(err)
	}
	defer openedDB.Close()
	if err := validateHostedDatabase(openedRegistry, hosted, openedDB); err != nil {
		t.Fatal(err)
	}

	// A registry cap flows through to the open database's data-file growth cap.
	if openedDB.StorageCapBytes() != 0 {
		t.Fatalf("unexpected default cap: %d", openedDB.StorageCapBytes())
	}
	if err := openedRegistry.SetRealmStorageCap(realm.ID, 200<<20); err != nil {
		t.Fatal(err)
	}
	if err := openedRegistry.SetDatabaseStorageCap(realm.ID, hosted.ID, 64<<20); err != nil {
		t.Fatal(err)
	}
	updatedRealm := openedRegistry.Manifest().Realms[0]
	applyHostedStorageCap(openedDB, updatedRealm, updatedRealm.Databases[0])
	if got := openedDB.StorageCapBytes(); got == 0 || got > 64<<20 {
		t.Fatalf("effective cap not applied: %d", got)
	}
}

func TestEffectiveStorageCapBytes(t *testing.T) {
	cases := []struct{ realm, db, want uint64 }{
		{0, 0, 0},
		{100, 0, 100},
		{0, 100, 100},
		{100, 60, 60},
		{60, 100, 60},
		{100, 100, 100},
	}
	for _, c := range cases {
		if got := hosting.EffectiveStorageCapBytes(c.realm, c.db); got != c.want {
			t.Fatalf("EffectiveStorageCapBytes(%d,%d)=%d want %d", c.realm, c.db, got, c.want)
		}
	}
}

func TestOpenHostedDefaultRejectsProvisioning(t *testing.T) {
	dir := t.TempDir()
	instanceKey := filepath.Join(t.TempDir(), "instance.key")
	instanceRoot := createTestKey(t, instanceKey)
	identity, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	registry, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
		RealmName:        "default",
		DatabaseName:     "default",
		DatabaseIdentity: identity,
		DatabaseState:    hosting.StateProvisioning,
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = registry.Close()
	cfg := config.Default()
	cfg.DataDir = dir
	cfg.InstanceKeyFile = instanceKey
	if _, _, _, err := openHostedDefault(cfg); !nerr.HasCode(err, nerr.Unavailable) {
		t.Fatalf("provisioning registry: %v", err)
	}
}

// TestOpenHostedDefaultAcceptsManagedLayoutDefault covers a
// manifest-bootstrapped deployment: RegistryManifest marks every database
// LayoutManaged, including the default. openHostedDefault must accept it
// (nextsqld then serves it lazily through dbmanager, with no eager primary
// handle) instead of the pre-2026-09-03 "single-database runtime" refusal.
func TestOpenHostedDefaultAcceptsManagedLayoutDefault(t *testing.T) {
	dir := t.TempDir()
	secrets := t.TempDir()
	instanceKey := filepath.Join(secrets, "instance.key")
	instanceRoot := createTestKey(t, instanceKey)
	createTestKey(t, filepath.Join(secrets, "main.key")).Zero()
	instanceRoot.Zero()

	manifest := filepath.Join(t.TempDir(), "hosting.yaml")
	if err := os.WriteFile(manifest, []byte(`version: 1
default: {realm: only, database: main}
realms:
  - name: only
    databases:
      - {name: main, key_file: `+filepath.Join(secrets, "main.key")+`}
`), 0o600); err != nil {
		t.Fatal(err)
	}
	bootstrap, err := hosting.LoadDeploymentBootstrap(manifest)
	if err != nil {
		t.Fatal(err)
	}
	root, err := crypto.ReadKeyFile(instanceKey)
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureManifest(hosting.Path(dir), root, func(dep hosting.ID) (hosting.Manifest, error) {
		return bootstrap.RegistryManifest(dep, hosting.StateActive)
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = reg.Close()

	cfg := config.Default()
	cfg.DataDir = dir
	cfg.InstanceKeyFile = instanceKey
	openedReg, realm, db, err := openHostedDefault(cfg)
	if err != nil {
		t.Fatalf("openHostedDefault rejected a managed-layout default: %v", err)
	}
	defer openedReg.Close()
	if realm.Name != "only" || db.Name != "main" || db.Layout != hosting.LayoutManaged {
		t.Fatalf("realm=%+v db=%+v", realm, db)
	}
}

func createTestKey(t *testing.T, path string) *crypto.DEK {
	t.Helper()
	root, err := crypto.CreateKeyFile(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	return root
}
