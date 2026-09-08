package executor

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/storage/format"
)

// TestCreateDatabaseFailsClosedOnRegistryBackedDeployment pins the refusal that
// keeps a registry-backed deployment from accumulating unreachable databases.
//
// The SQL statement only creates a bare sibling file. That is meaningful for an
// embedded deployment with no registry, but on a registry-backed one the file
// gets no realm, no registry record, and no routing entry, so no client can
// ever connect to it (Hello resolves names through the registry) while it still
// consumes a full database's worth of disk. It must fail closed and leave
// nothing behind.
func TestCreateDatabaseFailsClosedOnRegistryBackedDeployment(t *testing.T) {
	dir := t.TempDir()
	db, err := Create(filepath.Join(dir, "nextsql.db"), testKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()

	// Without a registry this is an embedded deployment: the statement is
	// still supported, and creates its sibling file.
	if _, err := s.Exec(`CREATE DATABASE embedded_sibling`); err != nil {
		t.Fatalf("embedded CREATE DATABASE should still work: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "embedded_sibling")); err != nil {
		t.Fatalf("embedded CREATE DATABASE did not create its file: %v", err)
	}

	// Attach a registry: the same statement must now be refused.
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	ident, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), root, hosting.Bootstrap{
		RealmName:        "realm-a",
		DatabaseName:     "db-a",
		DatabaseIdentity: ident,
		DatabaseState:    hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()
	s.SetHostingRegistry(reg)

	_, err = s.Exec(`CREATE DATABASE managed_orphan`)
	if err == nil {
		t.Fatal("CREATE DATABASE must fail closed on a registry-backed deployment")
	}
	if !strings.Contains(err.Error(), "registry-backed") {
		t.Fatalf("error should explain the deployment shape, got: %v", err)
	}
	if !strings.Contains(err.Error(), "nextsql database create") {
		t.Fatalf("error should name the supported provisioning path, got: %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, "managed_orphan")); !os.IsNotExist(statErr) {
		t.Fatalf("refused CREATE DATABASE must leave no file behind (stat: %v)", statErr)
	}

	// IF NOT EXISTS must not turn the refusal into a silent success either.
	if _, err := s.Exec(`CREATE DATABASE IF NOT EXISTS managed_orphan`); err == nil {
		t.Fatal("IF NOT EXISTS must not bypass the registry-backed refusal")
	}
	if _, statErr := os.Stat(filepath.Join(dir, "managed_orphan")); !os.IsNotExist(statErr) {
		t.Fatal("IF NOT EXISTS variant must leave no file behind")
	}
}
