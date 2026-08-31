package hosting

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestTenantMigrationIntentEncryptedExactAndComplete(t *testing.T) {
	dir := t.TempDir()
	keys, err := crypto.NewMemoryKeyProvider(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	intent := TenantMigrationIntent{
		Source:      testIdentity(t),
		Destination: testIdentity(t),
		Tenant:      "tenant-secret-value",
		Realm:       "customer_a",
		Database:    "production",
	}
	path := TenantMigrationPath(dir)
	got, created, err := EnsureTenantMigrationIntent(path, keys, intent)
	if err != nil {
		t.Fatal(err)
	}
	if !created || got.State != TenantMigrationProvisioning {
		t.Fatalf("created=%v intent=%+v", created, got)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) == "" || containsBytes(raw, []byte(intent.Tenant)) {
		t.Fatal("tenant identity leaked in plaintext intent")
	}
	got, created, err = EnsureTenantMigrationIntent(path, keys, intent)
	if err != nil || created || got.State != TenantMigrationProvisioning {
		t.Fatalf("exact retry created=%v intent=%+v err=%v", created, got, err)
	}

	changed := intent
	changed.Tenant = "other"
	if _, _, err := EnsureTenantMigrationIntent(path, keys, changed); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("changed tenant accepted: %v", err)
	}
	if err := CompleteTenantMigrationIntent(path, keys, intent, 2, 17); err != nil {
		t.Fatal(err)
	}
	complete, err := ReadTenantMigrationIntent(path, keys)
	if err != nil {
		t.Fatal(err)
	}
	if complete.State != TenantMigrationComplete || complete.Tables != 2 || complete.Rows != 17 {
		t.Fatalf("complete intent %+v", complete)
	}
	if err := CompleteTenantMigrationIntent(path, keys, intent, 2, 17); err != nil {
		t.Fatalf("idempotent complete: %v", err)
	}
	if err := CompleteTenantMigrationIntent(path, keys, intent, 2, 18); !nerr.HasCode(err, nerr.Conflict) {
		t.Fatalf("changed completion accepted: %v", err)
	}
}

func TestTenantMigrationIntentTamperTruncateAndWrongKeyFailClosed(t *testing.T) {
	dir := t.TempDir()
	keys, err := crypto.NewMemoryKeyProvider(testRoot(t))
	if err != nil {
		t.Fatal(err)
	}
	intent := TenantMigrationIntent{
		Source: testIdentity(t), Destination: testIdentity(t), Tenant: "a",
		Realm: "default", Database: "tenant_a",
	}
	path := filepath.Join(dir, "intent")
	if _, _, err := EnsureTenantMigrationIntent(path, keys, intent); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[len(tampered)-1] ^= 0xff
	if err := os.WriteFile(path, tampered, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTenantMigrationIntent(path, keys); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("tamper error %v", err)
	}
	if err := os.WriteFile(path, raw[:12], 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTenantMigrationIntent(path, keys); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("truncate error %v", err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	wrong, err := crypto.GenerateDEK(format.KeyVersion(1))
	if err != nil {
		t.Fatal(err)
	}
	wrongKeys, err := crypto.NewMemoryKeyProvider(wrong)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ReadTenantMigrationIntent(path, wrongKeys); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong-key error %v", err)
	}
}

func containsBytes(haystack, needle []byte) bool {
	if len(needle) == 0 || len(needle) > len(haystack) {
		return false
	}
	for i := 0; i+len(needle) <= len(haystack); i++ {
		match := true
		for j := range needle {
			if haystack[i+j] != needle[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}
