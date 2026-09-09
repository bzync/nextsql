package main

import (
	"os"
	"path/filepath"
	"strings"
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

	// A registry cap recorded by an earlier release still flows through to
	// the open database's data-file growth cap; only the ability to set one
	// through the CLI went away with hosting.
	if openedDB.StorageCapBytes() != 0 {
		t.Fatalf("unexpected default cap: %d", openedDB.StorageCapBytes())
	}
	capped := openedRegistry.Manifest().Realms[0]
	capped.StorageCapBytes = 200 << 20
	capped.Databases[0].StorageCapBytes = 64 << 20
	applyHostedStorageCap(openedDB, capped, capped.Databases[0])
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

func createTestKey(t *testing.T, path string) *crypto.DEK {
	t.Helper()
	root, err := crypto.CreateKeyFile(path, 1)
	if err != nil {
		t.Fatal(err)
	}
	return root
}

// TestRequireSingleDatabaseDeploymentFailsClosed proves nextsqld refuses to
// serve a registry written by a release that could hold more than one
// database, instead of starting up healthy with the operator's other
// databases silently unreachable. The registry format is unchanged, so such a
// deployment is still readable by the release that made it.
func TestRequireSingleDatabaseDeploymentFailsClosed(t *testing.T) {
	newRegistry := func(t *testing.T, dir string, layout hosting.Layout) *hosting.Registry {
		t.Helper()
		instanceRoot := createTestKey(t, filepath.Join(t.TempDir(), "instance.key"))
		identity, err := format.NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
			RealmName:        "default",
			DatabaseName:     "default",
			DatabaseIdentity: identity,
			DatabaseState:    hosting.StateActive,
		})
		if err != nil {
			t.Fatal(err)
		}
		return reg
	}

	t.Run("one database is accepted", func(t *testing.T) {
		reg := newRegistry(t, t.TempDir(), hosting.LayoutLegacyDefault)
		defer reg.Close()
		realm, database, err := reg.Default()
		if err != nil {
			t.Fatal(err)
		}
		if err := requireSingleDatabaseDeployment(reg, realm, database); err != nil {
			t.Fatalf("a single-database deployment must start: %v", err)
		}
	})

	// The manifest that produced these shapes is gone, so they are built by
	// hand here — which is exactly how they reach this build: on disk, from
	// an earlier release.
	t.Run("a second database is refused", func(t *testing.T) {
		reg := newRegistry(t, t.TempDir(), hosting.LayoutLegacyDefault)
		defer reg.Close()
		realm, database, err := reg.Default()
		if err != nil {
			t.Fatal(err)
		}
		second := database
		second.Name = "other"
		realm.Databases = append(append([]hosting.Database(nil), realm.Databases...), second)
		if err := requireSingleDatabaseDeploymentManifest(hosting.Manifest{Realms: []hosting.Realm{realm}}, realm, database); err == nil {
			t.Fatal("a registry with two databases must fail closed")
		} else if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("wrong code: %v", err)
		}
	})

	t.Run("a managed-layout database is refused", func(t *testing.T) {
		reg := newRegistry(t, t.TempDir(), hosting.LayoutLegacyDefault)
		defer reg.Close()
		realm, database, err := reg.Default()
		if err != nil {
			t.Fatal(err)
		}
		database.Layout = hosting.LayoutManaged
		realm.Databases = []hosting.Database{database}
		if err := requireSingleDatabaseDeploymentManifest(hosting.Manifest{Realms: []hosting.Realm{realm}}, realm, database); err == nil {
			t.Fatal("a managed-layout database must fail closed")
		} else if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("wrong code: %v", err)
		}
	})

	t.Run("a second realm is refused", func(t *testing.T) {
		reg := newRegistry(t, t.TempDir(), hosting.LayoutLegacyDefault)
		defer reg.Close()
		realm, database, err := reg.Default()
		if err != nil {
			t.Fatal(err)
		}
		m := hosting.Manifest{Realms: []hosting.Realm{realm, realm}}
		if err := requireSingleDatabaseDeploymentManifest(m, realm, database); err == nil {
			t.Fatal("a registry with two realms must fail closed")
		} else if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("wrong code: %v", err)
		}
	})
}

// A deployment initialized without `--database` has keys and an
// administrator but nothing to serve. nextsqld must say that, and say how to
// fix it, instead of surfacing a missing-file error from inside the storage
// layer — and it must still distinguish that from a registry whose database
// has gone missing, which is damage.
func TestRequireInitializedDatabase(t *testing.T) {
	t.Run("a database is served", func(t *testing.T) {
		dir := t.TempDir()
		dbPath := filepath.Join(dir, "nextsql.db")
		if err := os.WriteFile(dbPath, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
		cfg := config.Default()
		cfg.DataDir = dir
		if err := requireInitializedDatabase(cfg, dbPath, nil); err != nil {
			t.Fatalf("an existing database must be served: %v", err)
		}
	})

	t.Run("no database yet names the command that creates one", func(t *testing.T) {
		dir := t.TempDir()
		cfg := config.Default()
		cfg.DataDir = dir
		cfg.KeyFile = "/etc/nextsql/root.key"
		err := requireInitializedDatabase(cfg, filepath.Join(dir, "nextsql.db"), nil)
		if !nerr.HasCode(err, nerr.Unavailable) {
			t.Fatalf("want unavailable, got %v", err)
		}
		if !strings.Contains(err.Error(), "--database NAME") || !strings.Contains(err.Error(), cfg.KeyFile) {
			t.Fatalf("the error must name the command to run: %v", err)
		}
	})

	t.Run("a registry without its database is corruption", func(t *testing.T) {
		dir := t.TempDir()
		instanceKey := filepath.Join(t.TempDir(), "instance.key")
		instanceRoot := createTestKey(t, instanceKey)
		identity, err := format.NewIdentity()
		if err != nil {
			t.Fatal(err)
		}
		reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
			RealmName: "default", DatabaseName: "default",
			DatabaseIdentity: identity, DatabaseState: hosting.StateActive,
		})
		if err != nil {
			t.Fatal(err)
		}
		defer reg.Close()
		cfg := config.Default()
		cfg.DataDir = dir
		if err := requireInitializedDatabase(cfg, filepath.Join(dir, "nextsql.db"), reg); !nerr.HasCode(err, nerr.Corruption) {
			t.Fatalf("want corruption, got %v", err)
		}
	})
}
