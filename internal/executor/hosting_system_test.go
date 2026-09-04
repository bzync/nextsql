package executor

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/hosting"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

// TestSystemHostingViewsNilRegistry proves system.realms/system.databases
// degrade to empty rows (never an error) on a legacy, non-hosted deployment
// where no hosting.Registry was ever wired via SetHostingRegistry.
func TestSystemHostingViewsNilRegistry(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	sess := db.Session()

	for _, view := range []string{"system.realms", "system.databases"} {
		res := execOK(t, sess, "SELECT * FROM "+view)
		if len(res.Rows) != 0 {
			t.Fatalf("%s with no hosting registry wired must be empty, got %v", view, res.Rows)
		}
	}
}

// TestSystemHostingViewsRBACAndContent proves system.realms/system.databases
// reflect a real hosting.Registry's manifest for an admin caller and stay
// empty for a non-admin one, mirroring system.resource_groups.
func TestSystemHostingViewsRBACAndContent(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()

	instanceRoot, err := crypto.CreateKeyFile(filepath.Join(dir, "instance.key"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ident1, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
		RealmName: "acme", DatabaseName: "db1", DatabaseIdentity: ident1, DatabaseState: hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	realm, db1, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}

	ident2, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	key2Path := filepath.Join(dir, "db2.key")
	if _, err := crypto.CreateKeyFile(key2Path, 1); err != nil {
		t.Fatal(err)
	}
	db2, _, err := reg.CreateDatabase(realm.ID, "db2", ident2, key2Path)
	if err != nil {
		t.Fatal(err)
	}

	acl, err := security.CreateACL(filepath.Join(dir, "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetHostingRegistry(reg)

	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetHostingRegistry(reg)

	for _, view := range []string{"system.realms", "system.databases"} {
		res := execOK(t, app, "SELECT * FROM "+view)
		if len(res.Rows) != 0 {
			t.Fatalf("non-admin must see zero rows from %s, got %v", view, res.Rows)
		}
	}

	realmsRes := execOK(t, admin, "SELECT * FROM system.realms")
	if len(realmsRes.Rows) != 1 {
		t.Fatalf("system.realms = %v", realmsRes.Rows)
	}
	rrow := realmsRes.Rows[0]
	if rrow[0].Str != realm.ID.String() || rrow[1].Str != "acme" || rrow[2].Str != "active" {
		t.Fatalf("system.realms row = %+v", rrow)
	}
	if rrow[3].Dec.String() != "2" {
		t.Fatalf("system.realms database_count = %v, want 2", rrow[3])
	}

	dbsRes := execOK(t, admin, "SELECT * FROM system.databases")
	if len(dbsRes.Rows) != 2 {
		t.Fatalf("system.databases = %v", dbsRes.Rows)
	}
	byName := map[string][]types.Value{}
	for _, row := range dbsRes.Rows {
		byName[row[3].Str] = row
	}
	d1row, ok := byName["db1"]
	if !ok {
		t.Fatalf("system.databases missing db1: %v", dbsRes.Rows)
	}
	if d1row[0].Str != realm.ID.String() || d1row[1].Str != "acme" || d1row[2].Str != db1.ID.String() {
		t.Fatalf("db1 row = %+v", d1row)
	}
	if d1row[4].Str != "active" || d1row[5].Str != "legacy_default" {
		t.Fatalf("db1 row state/layout = %+v", d1row)
	}
	d2row, ok := byName["db2"]
	if !ok {
		t.Fatalf("system.databases missing db2: %v", dbsRes.Rows)
	}
	if d2row[2].Str != db2.ID.String() || d2row[4].Str != "provisioning" || d2row[5].Str != "managed" {
		t.Fatalf("db2 row = %+v", d2row)
	}
}

// TestSystemQuotasViewNilRegistry proves system.quotas (M3) degrades to empty
// rows on a legacy, non-hosted deployment, exactly like system.realms.
func TestSystemQuotasViewNilRegistry(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()
	res := execOK(t, db.Session(), "SELECT * FROM system.quotas")
	if len(res.Rows) != 0 {
		t.Fatalf("system.quotas with no hosting registry must be empty, got %v", res.Rows)
	}
}

// TestSystemQuotasViewRBACAndContent proves system.quotas surfaces the hosting
// storage caps (realm + effective per-database) for an admin, stays empty for a
// non-admin, and populates the usage columns only for the session's own
// connected realm+database.
func TestSystemQuotasViewRBACAndContent(t *testing.T) {
	dir := t.TempDir()
	dek, _ := crypto.GenerateDEK(1)
	keys, _ := crypto.NewMemoryKeyProvider(dek)
	db, err := Create(dir+"/db", keys, 16)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer db.Close()

	instanceRoot, err := crypto.CreateKeyFile(filepath.Join(dir, "instance.key"), 1)
	if err != nil {
		t.Fatal(err)
	}
	ident1, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	reg, _, err := hosting.EnsureBootstrap(hosting.Path(dir), instanceRoot, hosting.Bootstrap{
		RealmName: "acme", DatabaseName: "db1", DatabaseIdentity: ident1, DatabaseState: hosting.StateActive,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reg.Close()

	realm, _, err := reg.Default()
	if err != nil {
		t.Fatal(err)
	}
	ident2, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	key2Path := filepath.Join(dir, "db2.key")
	if _, err := crypto.CreateKeyFile(key2Path, 1); err != nil {
		t.Fatal(err)
	}
	db2, _, err := reg.CreateDatabase(realm.ID, "db2", ident2, key2Path)
	if err != nil {
		t.Fatal(err)
	}

	const realmCap = uint64(100 << 20)
	const db1Cap = uint64(40 << 20)
	if err := reg.SetRealmStorageCap(realm.ID, realmCap); err != nil {
		t.Fatal(err)
	}
	db1ID := reg.Manifest().Realms[0].Databases[0].ID
	if err := reg.SetDatabaseStorageCap(realm.ID, db1ID, db1Cap); err != nil {
		t.Fatal(err)
	}

	acl, err := security.CreateACL(filepath.Join(dir, "acl.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("app", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("dba", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}

	app := db.Session()
	app.SetIdentity("app")
	app.SetACL(acl)
	app.SetHostingRegistry(reg)
	if res := execOK(t, app, "SELECT * FROM system.quotas"); len(res.Rows) != 0 {
		t.Fatalf("non-admin must see zero rows from system.quotas, got %v", res.Rows)
	}

	// The admin session is "connected to" acme/db1.
	db.SetDatabaseName("db1")
	admin := db.Session()
	admin.SetIdentity("dba")
	admin.SetACL(acl)
	admin.SetHostingRegistry(reg)
	admin.SetRealmID(realm.ID)

	res := execOK(t, admin, "SELECT scope, realm_name, database_name, cap_bytes, effective_cap_bytes, usage_known, used_bytes, over_cap FROM system.quotas")
	// 1 realm row + 2 database rows.
	if len(res.Rows) != 3 {
		t.Fatalf("system.quotas rows = %v", res.Rows)
	}
	if res.Rows[0][0].Str != "realm" || res.Rows[0][1].Str != "acme" || res.Rows[0][2].Str != "" {
		t.Fatalf("first row must be the realm: %+v", res.Rows[0])
	}
	if res.Rows[0][3].Dec.String() != "104857600" || res.Rows[0][4].Dec.String() != "104857600" {
		t.Fatalf("realm cap row = %+v", res.Rows[0])
	}
	byDB := map[string][]types.Value{}
	for _, r := range res.Rows[1:] {
		if r[0].Str != "database" {
			t.Fatalf("expected database row, got %+v", r)
		}
		byDB[r[2].Str] = r
	}
	d1 := byDB["db1"]
	if d1[3].Dec.String() != "41943040" || d1[4].Dec.String() != "41943040" {
		t.Fatalf("db1 caps = %+v", d1)
	}
	if !d1[5].Bool {
		t.Fatalf("db1 usage_known must be true for the connected session: %+v", d1)
	}
	if d1[6].Dec.IsZero() {
		t.Fatalf("db1 used_bytes must be positive: %+v", d1)
	}
	if d1[7].Bool {
		t.Fatalf("db1 must not be over cap for a fresh database: %+v", d1)
	}
	d2 := byDB["db2"]
	// db2 has no per-database cap, so its effective cap is the realm cap.
	if d2[3].Dec.String() != "0" || d2[4].Dec.String() != "104857600" {
		t.Fatalf("db2 caps = %+v", d2)
	}
	if d2[5].Bool {
		t.Fatalf("db2 usage_known must be false (not the connected database): %+v", d2)
	}
	_ = db2
}
