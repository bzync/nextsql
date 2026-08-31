package xport

import (
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
)

func TestMigrateLegacyTenantPartitionIsBoundedVerifiedAndIdempotent(t *testing.T) {
	dir := t.TempDir()
	sourceKeys := migrationKeys(t)
	source, err := executor.Create(filepath.Join(dir, "source.db"), sourceKeys, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := source.Session()
	if _, err := s.Exec(`CREATE TABLE orders (
		tenant_id STRING NOT NULL,
		id STRING NOT NULL,
		amount DECIMAL(12,0) NOT NULL,
		PRIMARY KEY (tenant_id, id)
	) PARTITION BY LIST (tenant_id) (
		PARTITION tenant_a VALUES IN ('a'),
		PARTITION tenant_b VALUES IN ('b')
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE INDEX by_amount ON orders (amount) INCLUDE (tenant_id)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`ALTER TABLE orders SET CDC IMAGES FULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO orders (tenant_id, id, amount) VALUES
		('a', '1', 10), ('a', '2', 20), ('b', '1', 99)`); err != nil {
		t.Fatal(err)
	}
	makeLegacyTenantDescriptor(t, source, "orders")

	destKeys := migrationKeys(t)
	dest, err := executor.Create(filepath.Join(dir, "dest.db"), destKeys, 64)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MigrateLegacyTenant(source, dest, "a", LegacyTenantOptions{BatchRows: 1})
	if err != nil {
		t.Fatal(err)
	}
	if result.Tables != 1 || result.Rows != 2 {
		t.Fatalf("migration result %+v", result)
	}

	table, ok := dest.Cat.Get("orders")
	if !ok {
		t.Fatal("migrated table missing")
	}
	if table.Partitioning != nil {
		t.Fatalf("destination retained physical tenant partitioning: %+v", table.Partitioning)
	}
	if _, legacy := table.LegacyTenantCol(); legacy {
		t.Fatal("destination retained legacy shared-tenancy marker")
	}
	if ord, ok := table.ColIndex(LegacyTenantDestinationColumn); !ok || ord != 0 {
		t.Fatalf("renamed tenant column missing: %+v", table.Columns)
	}
	if table.CDCImages != catalog.CDCImagesFull || len(table.Indexes) != 1 {
		t.Fatalf("logical metadata was not preserved: %+v", table)
	}
	rows, err := dest.Session().Exec(`SELECT legacy_tenant_id, id, amount FROM orders ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 2 || rows.Rows[0][0].Str != "a" || rows.Rows[1][0].Str != "a" {
		t.Fatalf("migrated rows %+v", rows.Rows)
	}
	indexed, err := dest.Session().Exec(`SELECT id FROM orders WHERE amount = 20`)
	if err != nil || len(indexed.Rows) != 1 || indexed.Rows[0][0].Str != "2" {
		t.Fatalf("migrated index result=%+v err=%v", indexed, err)
	}

	// A retry replays bounded UPSERT batches and proves the same final state.
	again, err := MigrateLegacyTenant(source, dest, "a", LegacyTenantOptions{BatchRows: 2})
	if err != nil {
		t.Fatal(err)
	}
	if again.Rows != 2 {
		t.Fatalf("retry result %+v", again)
	}
	rows, err = dest.Session().Exec(`SELECT id FROM orders ORDER BY id`)
	if err != nil || len(rows.Rows) != 2 {
		t.Fatalf("retry duplicated rows: %+v err=%v", rows, err)
	}
	if _, err := VerifyLegacyTenantMigration(source, dest, "a"); err != nil {
		t.Fatalf("read-only verification: %v", err)
	}
	if _, err := dest.Session().Exec(`UPDATE orders SET amount = 777 WHERE id = '1'`); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyLegacyTenantMigration(source, dest, "a"); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("changed destination verified: %v", err)
	}
	changed, err := dest.Session().Exec(`SELECT amount FROM orders WHERE id = '1'`)
	if err != nil || len(changed.Rows) != 1 || changed.Rows[0][0].Dec.String() != "777" {
		t.Fatalf("verification overwrote destination: %+v err=%v", changed, err)
	}

	sourceRows, err := source.Session().Exec(`SELECT tenant_id, id FROM orders ORDER BY tenant_id, id`)
	if err != nil || len(sourceRows.Rows) != 3 {
		t.Fatalf("source changed: %+v err=%v", sourceRows, err)
	}
	if err := dest.Close(); err != nil {
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMigrateLegacyTenantRejectsUnexpectedDestinationState(t *testing.T) {
	dir := t.TempDir()
	source, err := executor.Create(filepath.Join(dir, "source.db"), migrationKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Session().Exec(`CREATE TABLE rows (id STRING PRIMARY KEY, tenant_id STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Session().Exec(`INSERT INTO rows (id, tenant_id) VALUES ('1', 'a')`); err != nil {
		t.Fatal(err)
	}
	dest, err := executor.Create(filepath.Join(dir, "dest.db"), migrationKeys(t), 32)
	if err != nil {
		t.Fatal(err)
	}
	defer dest.Close()
	if _, err := dest.Session().Exec(`CREATE TABLE unrelated (id STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := MigrateLegacyTenant(source, dest, "a", LegacyTenantOptions{}); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("unexpected destination accepted: %v", err)
	}
}

func migrationKeys(t *testing.T) *crypto.MemoryKeyProvider {
	t.Helper()
	dek, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	keys, err := crypto.NewMemoryKeyProvider(dek)
	if err != nil {
		t.Fatal(err)
	}
	return keys
}

func makeLegacyTenantDescriptor(t *testing.T, db *executor.DB, name string) {
	t.Helper()
	table, ok := db.Cat.Get(name)
	if !ok || table.Partitioning == nil {
		t.Fatal("partitioned table missing")
	}
	table.Partitioning.Kind = catalog.PartitionLegacyTenant
	raw, err := catalog.EncodeTable(table)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := db.CatTree.Begin()
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Update(catalog.TableKey(name), raw); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	db.Cat.Put(table)
}
