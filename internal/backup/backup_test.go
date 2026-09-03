package backup

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/bzync/nextsql/internal/clientenc"
	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

type backupFieldKeys struct{ key clientenc.Key }

func (p backupFieldKeys) CurrentFieldKey(context.Context, string, string, string) (clientenc.Key, error) {
	return p.key, nil
}
func (p backupFieldKeys) FieldKey(_ context.Context, _, _, _, id string) (clientenc.Key, error) {
	if id != p.key.ID {
		return clientenc.Key{}, nerr.New(nerr.NotFound, "test", "field key missing")
	}
	return p.key, nil
}

func setupSQL(t *testing.T, dir string) (dataDir, dbPath string, root *crypto.DEK, env *crypto.Envelope) {
	t.Helper()
	dataDir = filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	dbPath = filepath.Join(dataDir, config.DataFileName)
	root, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	env, err = crypto.CreateEnvelope(crypto.KeystorePath(dbPath), id, root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.CreateWithIdentity(dbPath, id, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if _, err := s.Exec(`CREATE TABLE items (id UUID PRIMARY KEY DEFAULT UUID(), sku STRING NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`INSERT INTO items (sku) VALUES ('alpha')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE WORKFLOW add_item(sku STRING) AS BEGIN INSERT INTO items (sku) VALUES ($sku); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE TABLE item_audit (sku STRING PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE WORKFLOW audit_item(sku STRING) AS BEGIN INSERT INTO item_audit (sku) VALUES ($sku); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Exec(`CREATE TRIGGER audit_item_insert AFTER INSERT ON items FOR EACH ROW RUN WORKFLOW audit_item(NEW.sku)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return dataDir, dbPath, root, env
}

func execSKUs(t *testing.T, dbPath string, keys crypto.KeyProvider) []string {
	t.Helper()
	db, err := executor.Open(dbPath, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	res, err := db.Session().Exec(`SELECT sku FROM items`)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]string, 0, len(res.Rows))
	for _, row := range res.Rows {
		out = append(out, row[0].Str)
	}
	return out
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

func TestBackupRestoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "full.nsbak")
	res, err := Create(dataDir, dest, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified || !res.RestoreTest || res.Members < 2 {
		t.Fatalf("unexpected result %+v", res)
	}
	_ = env.Close()

	restored := filepath.Join(dir, "restored")
	env2, err := crypto.OpenEnvelope(crypto.KeystorePath(filepath.Join(dataDir, config.DataFileName)), root)
	if err != nil {
		t.Fatal(err)
	}
	defer env2.Close()
	if _, err := Restore(dest, restored, env2, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	got := execSKUs(t, filepath.Join(restored, config.DataFileName), env2)
	if !contains(got, "alpha") {
		t.Fatalf("restored rows %v", got)
	}
	restoredDB, err := executor.Open(filepath.Join(restored, config.DataFileName), env2, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restoredDB.Session().Exec(`RUN WORKFLOW add_item('from-backup')`); err != nil {
		t.Fatal(err)
	}
	audit, err := restoredDB.Session().Exec(`SELECT sku FROM item_audit WHERE sku = 'from-backup'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Rows) != 1 {
		t.Fatalf("restored trigger did not fire: %v", audit.Rows)
	}
	if err := restoredDB.Close(); err != nil {
		t.Fatal(err)
	}
	got = execSKUs(t, filepath.Join(restored, config.DataFileName), env2)
	if !contains(got, "from-backup") {
		t.Fatalf("restored workflow did not execute: %v", got)
	}
}

func TestEncryptedClientBackupRestoreRemainsOpaque(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, root, env := setupSQL(t, dir)
	db, err := executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().Exec(`CREATE TABLE accounts (id STRING PRIMARY KEY, secret TEXT ENCRYPTED CLIENT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	fieldKey := clientenc.Key{ID: "backup-v1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 4
	}
	provider := backupFieldKeys{key: fieldKey}
	ciphertext, err := clientenc.Encrypt(context.Background(), provider, "app", "accounts", "secret", types.TextValue("backup-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().ExecContext(context.Background(), `INSERT INTO accounts (id, secret) VALUES ('1', $1)`, []executor.Param{{Value: types.StringValue(ciphertext)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(dir, "clientenc.nsbak")
	if _, err := Create(dataDir, backupDir, env, Options{}); err != nil {
		t.Fatal(err)
	}
	_ = env.Close()
	restoreKeys, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), root)
	if err != nil {
		t.Fatal(err)
	}
	defer restoreKeys.Close()
	restoredDir := filepath.Join(dir, "restored-clientenc")
	if _, err := Restore(backupDir, restoredDir, restoreKeys, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	restored, err := executor.Open(filepath.Join(restoredDir, config.DataFileName), restoreKeys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	res, err := restored.Session().Exec(`SELECT secret FROM accounts WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != ciphertext {
		t.Fatalf("restored ciphertext: %+v", res.Rows)
	}
	plain, err := clientenc.Decrypt(context.Background(), provider, "app", "accounts", "secret", res.Rows[0][0].Str)
	if err != nil || plain.Str != "backup-secret" {
		t.Fatalf("restored decrypt: %+v %v", plain, err)
	}
}

func TestEncryptedClientPITRRestoresExactCiphertextAtTarget(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, _, env := setupSQL(t, dir)
	db, err := executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().Exec(`CREATE TABLE accounts (id STRING PRIMARY KEY, secret TEXT ENCRYPTED CLIENT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(dir, "clientenc-base.nsbak")
	if _, err := Create(dataDir, base, env, Options{}); err != nil {
		t.Fatal(err)
	}
	archiveDir := filepath.Join(dir, "clientenc-walarch")
	archiver, err := NewDirArchiver(archiveDir, env)
	if err != nil {
		t.Fatal(err)
	}
	db, err = executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetArchiver(archiver)

	fieldKey := clientenc.Key{ID: "pitr-v1"}
	for i := range fieldKey.Material {
		fieldKey.Material[i] = 5
	}
	provider := backupFieldKeys{key: fieldKey}
	atTarget, err := clientenc.Encrypt(context.Background(), provider, "app", "accounts", "secret", types.TextValue("secret-at-target"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().ExecContext(context.Background(), `INSERT INTO accounts (id, secret) VALUES ('1', $1)`, []executor.Param{{Value: types.StringValue(atTarget)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	targetLSN := db.Eng.WAL.NextLSN() - 1
	if targetLSN == 0 {
		t.Fatal("client-encrypted insert did not allocate a recovery LSN")
	}

	afterTarget, err := clientenc.Encrypt(context.Background(), provider, "app", "accounts", "secret", types.TextValue("secret-after-target"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().ExecContext(context.Background(), `UPDATE accounts SET secret = $1 WHERE id = '1'`, []executor.Param{{Value: types.StringValue(afterTarget)}}); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	if db.Eng.WAL.NextLSN()-1 <= targetLSN {
		t.Fatal("post-target update did not allocate a later recovery LSN")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	restoredDir := filepath.Join(dir, "clientenc-pitr")
	if _, err := Restore(base, restoredDir, env, RestoreOptions{ArchiveDir: archiveDir, UntilLSN: targetLSN}); err != nil {
		t.Fatal(err)
	}
	restored, err := executor.Open(filepath.Join(restoredDir, config.DataFileName), env, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	meta, err := restored.Session().Exec(`SELECT type FROM system.columns WHERE table_name = 'accounts' AND column_name = 'secret'`)
	if err != nil || len(meta.Rows) != 1 || meta.Rows[0][0].Str != "TEXT ENCRYPTED CLIENT" {
		t.Fatalf("PITR client-encrypted catalog metadata: rows=%+v err=%v", meta.Rows, err)
	}
	res, err := restored.Session().Exec(`SELECT secret FROM accounts WHERE id = '1'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 || res.Rows[0][0].Str != atTarget || res.Rows[0][0].Str == afterTarget {
		t.Fatalf("PITR ciphertext at target: rows=%+v", res.Rows)
	}
	plain, err := clientenc.Decrypt(context.Background(), provider, "app", "accounts", "secret", res.Rows[0][0].Str)
	if err != nil || plain.Typ.Kind != types.KindText || plain.Str != "secret-at-target" {
		t.Fatalf("PITR decrypt: value=%+v err=%v", plain, err)
	}
}

func TestPartitionBackupRestoreAndPITR(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, _, env := setupSQL(t, dir)
	db, err := executor.Open(dbPath, env, 64)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	for _, sql := range []string{
		`CREATE TABLE partition_orders (
			region STRING NOT NULL,
			id STRING NOT NULL,
			sku STRING NOT NULL,
			PRIMARY KEY (region, id)
		) PARTITION BY LIST (region) (
			PARTITION americas VALUES IN ('us'),
			PARTITION europe VALUES IN ('eu')
		)`,
		`INSERT INTO partition_orders (region, id, sku) VALUES
			('us', '1', 'alpha'),
			('eu', '2', 'beta')`,
		`CREATE INDEX ix_partition_orders_sku ON partition_orders (sku)`,
		`ANALYZE partition_orders`,
	} {
		if _, err := s.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	base := filepath.Join(dir, "partition-base.nsbak")
	if _, err := Create(dataDir, base, env, Options{}); err != nil {
		t.Fatal(err)
	}
	baseOut := filepath.Join(dir, "partition-base-restore")
	if _, err := Restore(base, baseOut, env, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	assertPartitionBackupState(t, filepath.Join(baseOut, config.DataFileName), env, 2, 2, "beta", "europe")

	archDir := filepath.Join(dir, "partition-walarch")
	arch, err := NewDirArchiver(archDir, env)
	if err != nil {
		t.Fatal(err)
	}
	db, err = executor.Open(dbPath, env, 64)
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetArchiver(arch)
	s = db.Session()
	for _, sql := range []string{
		`ALTER TABLE partition_orders ADD PARTITION asia VALUES IN ('ap')`,
		`INSERT INTO partition_orders (region, id, sku) VALUES ('ap', '3', 'gamma')`,
		`ANALYZE partition_orders`,
	} {
		if _, err := s.Exec(sql); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	partitionLSN := db.Eng.WAL.NextLSN() - 1
	if partitionLSN == 0 {
		t.Fatal("partition lifecycle did not allocate a recovery LSN")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	pitrOut := filepath.Join(dir, "partition-pitr")
	if _, err := Restore(base, pitrOut, env, RestoreOptions{ArchiveDir: archDir, UntilLSN: partitionLSN}); err != nil {
		t.Fatal(err)
	}
	assertPartitionBackupState(t, filepath.Join(pitrOut, config.DataFileName), env, 3, 3, "gamma", "asia")
}

func assertPartitionBackupState(t *testing.T, dbPath string, keys crypto.KeyProvider, wantPartitions int, wantRows uint64, sku, partition string) {
	t.Helper()
	db, err := executor.Open(dbPath, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tab, ok := db.Cat.Get("partition_orders")
	if !ok || tab.Partitioning == nil || len(tab.Partitioning.Partitions) != wantPartitions || len(tab.Indexes) != 1 {
		t.Fatalf("restored partition catalog: ok=%v table=%+v", ok, tab)
	}
	for _, part := range tab.Partitioning.Partitions {
		if len(part.Indexes) != 1 || part.Indexes[0].Name != "ix_partition_orders_sku" || part.Indexes[0].Meta == 0 {
			t.Fatalf("restored partition-local index root: %+v", part)
		}
	}
	stats, ok := db.Cat.Stats("partition_orders")
	if !ok || stats.Rows != wantRows || len(stats.Partitions) != wantPartitions {
		t.Fatalf("restored partition statistics: ok=%v stats=%+v", ok, stats)
	}
	statsByID := make(map[uint32]uint64, len(stats.Partitions))
	var partitionRows uint64
	for _, part := range stats.Partitions {
		if len(part.Columns) != 3 || len(part.Indexes) != 1 || part.Indexes[0].Name != "ix_partition_orders_sku" {
			t.Fatalf("restored partition sketches: %+v", part)
		}
		statsByID[part.ID] = part.Rows
		partitionRows += part.Rows
	}
	for _, part := range tab.Partitioning.Partitions {
		if _, ok := statsByID[part.ID]; !ok {
			t.Fatalf("restored statistics lost stable partition ID %d: %+v", part.ID, stats.Partitions)
		}
	}
	if partitionRows != wantRows {
		t.Fatalf("restored partition row-count sum=%d want=%d", partitionRows, wantRows)
	}
	res, err := db.Session().Exec(`SELECT region, id FROM partition_orders WHERE sku = '` + sku + `'`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str == "" {
		t.Fatalf("restored partition-local index query: rows=%+v err=%v", res.Rows, err)
	}
	plan, err := db.Session().Exec(`EXPLAIN SELECT id FROM partition_orders WHERE region = '` + res.Rows[0][0].Str + `' AND sku = '` + sku + `'`)
	if err != nil || len(plan.Rows) == 0 {
		t.Fatalf("restored partition plan: rows=%+v err=%v", plan.Rows, err)
	}
	want := "partitions=[" + partition + "]"
	foundPartition := false
	foundIndex := false
	for _, row := range plan.Rows {
		if len(row) == 0 {
			continue
		}
		if bytes.Contains([]byte(row[0].Str), []byte(want)) {
			foundPartition = true
		}
		if bytes.Contains([]byte(row[0].Str), []byte("IndexScan")) && bytes.Contains([]byte(row[0].Str), []byte("ix_partition_orders_sku")) {
			foundIndex = true
		}
	}
	if !foundPartition {
		t.Fatalf("restored partition pruning missing %q: %+v", want, plan.Rows)
	}
	if !foundIndex {
		t.Fatalf("restored partition-local index plan missing: %+v", plan.Rows)
	}
}

func TestBackupRestoreWithPendingReclaimIntent(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, _, env := setupSQL(t, dir)
	db, err := executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	s := db.Session()
	if _, err := s.Exec(`CREATE INDEX ix_items_sku ON items (sku)`); err != nil {
		t.Fatal(err)
	}
	db.Eng.SetCrash(wal.PointDuringPageReclaim)
	if _, err := s.Exec(`DROP INDEX ix_items_sku`); err != nil {
		t.Fatal(err)
	}
	if !wal.IsCrash(db.LastReclaimError()) {
		t.Fatalf("reclaim crash not recorded: %v", db.LastReclaimError())
	}
	if _, err := os.Stat(dbPath + ".reclaim"); err != nil {
		t.Fatalf("pending reclaim intent missing: %v", err)
	}
	db.Eng.Kill()

	dest := filepath.Join(dir, "pending-reclaim.nsbak")
	if _, err := Create(dataDir, dest, env, Options{}); err != nil {
		t.Fatal(err)
	}
	restored := filepath.Join(dir, "restored")
	if _, err := Restore(dest, restored, env, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	restoredDB := filepath.Join(restored, config.DataFileName)
	if _, err := os.Stat(restoredDB + ".reclaim"); err != nil {
		t.Fatalf("restore did not preserve reclaim intent: %v", err)
	}
	reopened, err := executor.Open(restoredDB, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err := os.Stat(restoredDB + ".reclaim"); !os.IsNotExist(err) {
		t.Fatalf("restored reclaim intent was not replayed and cleared: %v", err)
	}
	res, err := reopened.Session().Exec(`SELECT sku FROM items`)
	if err != nil || len(res.Rows) != 1 || res.Rows[0][0].Str != "alpha" {
		t.Fatalf("restored database unusable after reclaim replay: rows=%v err=%v", res.Rows, err)
	}
}

func TestBackupWrongKeyFailsClosed(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, _, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "full.nsbak")
	if _, err := Create(dataDir, dest, env, Options{}); err != nil {
		t.Fatal(err)
	}
	other, err := crypto.GenerateDEK(1)
	if err != nil {
		t.Fatal(err)
	}
	id, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	bad, err := crypto.CreateEnvelope(filepath.Join(dir, "other.keys"), id, other)
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	if err := Verify(dest, bad, false); err == nil {
		t.Fatal("wrong key must not verify a backup")
	}
}

func TestBackupHasNoPlaintextRows(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, _, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "full.nsbak")
	if _, err := Create(dataDir, dest, env, Options{}); err != nil {
		t.Fatal(err)
	}
	marker := []byte("alpha")
	err := filepath.Walk(dest, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(raw, marker) {
			t.Errorf("plaintext %q found in %s", marker, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestTamperedMemberFails(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, _, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "full.nsbak")
	if _, err := Create(dataDir, dest, env, Options{SkipRestoreTest: true}); err != nil {
		t.Fatal(err)
	}
	// Create with skip still verifies hashes; flip a byte in a member.
	members := filepath.Join(dest, memberDirName)
	ents, err := os.ReadDir(members)
	if err != nil {
		t.Fatal(err)
	}
	if len(ents) == 0 {
		t.Fatal("no members")
	}
	p := filepath.Join(members, ents[0].Name())
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	raw[len(raw)/2] ^= 0xFF
	if err := os.WriteFile(p, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := Verify(dest, env, false); !nerr.HasCode(err, nerr.Corruption) && !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("expected corruption/crypto, got %v", err)
	}
}

func TestCrashDuringBackupNotPublished(t *testing.T) {
	for _, p := range []Point{PointBeforeCopy, PointDuringCopy, PointBeforeManifest, PointBeforeVerify} {
		p := p
		t.Run(p.String(), func(t *testing.T) {
			dir := t.TempDir()
			dataDir, dbPath, root, env := setupSQL(t, dir)
			dest := filepath.Join(dir, "full.nsbak")
			inj := NewInjector()
			inj.Arm(p)
			_, err := Create(dataDir, dest, env, Options{Crash: inj, SkipRestoreTest: true})
			if !IsCrash(err) {
				t.Fatalf("expected crash, got %v", err)
			}
			if _, err := os.Stat(dest); !os.IsNotExist(err) {
				t.Fatal("incomplete backup must not be published")
			}
			if _, err := Restore(dest, filepath.Join(dir, "out"), env, RestoreOptions{}); err == nil {
				t.Fatal("restore of missing backup must fail")
			}
			_ = env.Close()
			re, err := crypto.OpenEnvelope(crypto.KeystorePath(dbPath), root)
			if err != nil {
				t.Fatal(err)
			}
			defer re.Close()
			got := execSKUs(t, dbPath, re)
			if !contains(got, "alpha") {
				t.Fatalf("source must survive crash-during-backup, got %v", got)
			}
		})
	}
}

func (p Point) String() string {
	switch p {
	case PointBeforeCopy:
		return "before_copy"
	case PointDuringCopy:
		return "during_copy"
	case PointBeforeManifest:
		return "before_manifest"
	case PointBeforeVerify:
		return "before_verify"
	default:
		return "none"
	}
}

func TestUnverifiedBackupRefused(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, _, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "full.nsbak")
	if _, err := Create(dataDir, dest, env, Options{SkipRestoreTest: true}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(dest, verifiedName)); err != nil {
		t.Fatal(err)
	}
	if _, err := Restore(dest, filepath.Join(dir, "out"), env, RestoreOptions{}); !nerr.HasCode(err, nerr.Corruption) {
		t.Fatalf("expected unverified refusal, got %v", err)
	}
}

func TestPITRByLSN(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, root, env := setupSQL(t, dir)
	base := filepath.Join(dir, "base.nsbak")
	if _, err := Create(dataDir, base, env, Options{}); err != nil {
		t.Fatal(err)
	}

	archDir := filepath.Join(dir, "walarch")
	arch, err := NewDirArchiver(archDir, env)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetArchiver(arch)
	if _, err := db.Session().Exec(`INSERT INTO items (sku) VALUES ('beta')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().Exec(`CREATE WORKFLOW pitr_add(sku STRING) AS BEGIN INSERT INTO items (sku) VALUES ($sku); END`); err != nil {
		t.Fatal(err)
	}
	fireAt := time.Now().UTC().Add(50 * time.Millisecond)
	if _, err := db.Session().Exec(`CREATE SCHEDULE pitr_once AT '` + fireAt.Format(time.RFC3339Nano) + `' RUN WORKFLOW pitr_add('from-task')`); err != nil {
		t.Fatal(err)
	}
	time.Sleep(60 * time.Millisecond)
	if got, err := db.DispatchDueSchedules(context.Background(), time.Now().UTC(), 1); err != nil || got != 1 {
		t.Fatalf("dispatch task before PITR got=%d err=%v", got, err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	afterBeta := db.Eng.WAL.NextLSN() - 1
	if afterBeta == 0 {
		t.Fatal("expected a durable LSN after beta")
	}
	if _, err := db.Session().Exec(`INSERT INTO items (sku) VALUES ('gamma')`); err != nil {
		t.Fatal(err)
	}
	if db.Eng.WAL.NextLSN()-1 <= afterBeta {
		t.Fatal("gamma must allocate a later LSN than beta")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	onlyBase := filepath.Join(dir, "only-base")
	if _, err := Restore(base, onlyBase, env, RestoreOptions{}); err != nil {
		t.Fatal(err)
	}
	got := execSKUs(t, filepath.Join(onlyBase, config.DataFileName), env)
	if contains(got, "beta") || contains(got, "gamma") {
		t.Fatalf("base backup must not include later rows: %v", got)
	}

	pitr := filepath.Join(dir, "pitr")
	if _, err := Restore(base, pitr, env, RestoreOptions{ArchiveDir: archDir, UntilLSN: afterBeta}); err != nil {
		t.Fatal(err)
	}
	got = execSKUs(t, filepath.Join(pitr, config.DataFileName), env)
	if !contains(got, "alpha") || !contains(got, "beta") {
		t.Fatalf("PITR to beta LSN: %v", got)
	}
	if contains(got, "gamma") {
		t.Fatalf("PITR to beta must not include gamma: %v", got)
	}
	pitrDB, err := executor.Open(filepath.Join(pitr, config.DataFileName), env, 16)
	if err != nil {
		t.Fatal(err)
	}
	tasks, err := pitrDB.Session().Exec(`SHOW TASKS LIMIT 1`)
	if err != nil || len(tasks.Rows) != 1 || tasks.Rows[0][1].Str != "PENDING" || tasks.Rows[0][3].Str != "pitr_add" {
		t.Fatalf("PITR task state rows=%+v err=%v", tasks, err)
	}
	if _, err := pitrDB.Session().Exec(`RUN WORKFLOW pitr_add('from-pitr')`); err != nil {
		t.Fatal(err)
	}
	audit, err := pitrDB.Session().Exec(`SELECT sku FROM item_audit WHERE sku = 'from-pitr'`)
	if err != nil {
		t.Fatal(err)
	}
	if len(audit.Rows) != 1 {
		t.Fatalf("PITR trigger did not fire: %v", audit.Rows)
	}
	if err := pitrDB.Close(); err != nil {
		t.Fatal(err)
	}
	_ = root
}

func TestPITRAcrossRebuildAndDropIndex(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, _, env := setupSQL(t, dir)
	db, err := executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Session().Exec(`CREATE INDEX ix_items_sku ON items (sku)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	base := filepath.Join(dir, "ddl-base.nsbak")
	if _, err := Create(dataDir, base, env, Options{}); err != nil {
		t.Fatal(err)
	}

	archDir := filepath.Join(dir, "ddl-walarch")
	arch, err := NewDirArchiver(archDir, env)
	if err != nil {
		t.Fatal(err)
	}
	db, err = executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetArchiver(arch)
	if _, err := db.Session().Exec(`REBUILD INDEX ix_items_sku`); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	afterRebuild := db.Eng.WAL.NextLSN() - 1
	if _, err := db.Session().Exec(`DROP INDEX ix_items_sku`); err != nil {
		t.Fatal(err)
	}
	if err := db.Eng.Checkpoint(); err != nil {
		t.Fatal(err)
	}
	afterDrop := db.Eng.WAL.NextLSN() - 1
	if afterDrop <= afterRebuild {
		t.Fatal("drop must have a later recovery LSN than rebuild")
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	rebuilt := filepath.Join(dir, "at-rebuild")
	if _, err := Restore(base, rebuilt, env, RestoreOptions{ArchiveDir: archDir, UntilLSN: afterRebuild}); err != nil {
		t.Fatal(err)
	}
	if !restoredHasIndex(t, filepath.Join(rebuilt, config.DataFileName), env, "ix_items_sku") {
		t.Fatal("PITR at rebuild LSN lost the rebuilt index")
	}
	dropped := filepath.Join(dir, "after-drop")
	if _, err := Restore(base, dropped, env, RestoreOptions{ArchiveDir: archDir, UntilLSN: afterDrop}); err != nil {
		t.Fatal(err)
	}
	if restoredHasIndex(t, filepath.Join(dropped, config.DataFileName), env, "ix_items_sku") {
		t.Fatal("PITR after drop retained the index")
	}
}

func restoredHasIndex(t *testing.T, dbPath string, keys crypto.KeyProvider, name string) bool {
	t.Helper()
	db, err := executor.Open(dbPath, keys, 16)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	tab, ok := db.Cat.Get("items")
	if !ok {
		t.Fatal("restored items table missing")
	}
	for _, idx := range tab.Indexes {
		if idx.Name == name {
			return true
		}
	}
	return false
}

func TestPITRByTime(t *testing.T) {
	dir := t.TempDir()
	dataDir, dbPath, _, env := setupSQL(t, dir)
	base := filepath.Join(dir, "base.nsbak")
	res, err := Create(dataDir, base, env, Options{})
	if err != nil {
		t.Fatal(err)
	}
	mid := time.Unix(0, res.Header.CreatedNano).Add(time.Millisecond)

	archDir := filepath.Join(dir, "walarch")
	arch, err := NewDirArchiver(archDir, env)
	if err != nil {
		t.Fatal(err)
	}
	db, err := executor.Open(dbPath, env, 16)
	if err != nil {
		t.Fatal(err)
	}
	db.Eng.SetArchiver(arch)
	if _, err := db.Session().Exec(`INSERT INTO items (sku) VALUES ('later')`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	out := filepath.Join(dir, "by-time")
	if _, err := Restore(base, out, env, RestoreOptions{ArchiveDir: archDir, UntilTime: mid}); err != nil {
		t.Fatal(err)
	}
	got := execSKUs(t, filepath.Join(out, config.DataFileName), env)
	if contains(got, "later") {
		t.Fatalf("timestamp before later archive must not include later: %v", got)
	}
	if !contains(got, "alpha") {
		t.Fatalf("expected alpha, got %v", got)
	}
}

func TestOpenKeysUnlocksSidecar(t *testing.T) {
	dir := t.TempDir()
	dataDir, _, root, env := setupSQL(t, dir)
	dest := filepath.Join(dir, "full.nsbak")
	if _, err := Create(dataDir, dest, env, Options{SkipRestoreTest: true}); err != nil {
		t.Fatal(err)
	}
	keys, opened, err := OpenKeys(dest, root)
	if err != nil {
		t.Fatal(err)
	}
	if opened == nil || keys == nil {
		t.Fatal("expected envelope from keystore sidecar")
	}
	defer opened.Close()
	if err := Verify(dest, keys, true); err != nil {
		t.Fatal(err)
	}
}

func TestHeaderManifestRoundTrip(t *testing.T) {
	id, err := format.NewUUID()
	if err != nil {
		t.Fatal(err)
	}
	ident, err := format.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	h := Header{
		Version:     CurrentVersion,
		Suite:       format.CipherAES256GCM,
		KeyVersion:  1,
		Identity:    ident,
		Checkpoint:  3,
		RedoLSN:     4,
		DurableLSN:  5,
		CreatedNano: 6,
		BackupID:    id,
		NonceHigh:   7,
		WrappedDEK:  bytes.Repeat([]byte{1}, 64),
	}
	raw, err := encodeHeader(h)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeHeader(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.DurableLSN != 5 || got.Identity != ident {
		t.Fatalf("%+v", got)
	}
	mf := Manifest{Version: CurrentVersion, Members: []Member{{
		Kind: KindData, Name: "data", PlainSize: 10, SealedSize: 20,
	}}}
	mraw, err := encodeManifest(mf)
	if err != nil {
		t.Fatal(err)
	}
	mgot, err := decodeManifest(mraw)
	if err != nil {
		t.Fatal(err)
	}
	if len(mgot.Members) != 1 || mgot.Members[0].Name != "data" {
		t.Fatalf("%+v", mgot)
	}
}
