package hosting

import (
	"testing"

	"github.com/bzync/nextsql/internal/storage/format"
)

func FuzzDecodeTenantMigrationIntent(f *testing.F) {
	source, err := format.NewIdentity()
	if err != nil {
		f.Fatal(err)
	}
	dest, err := format.NewIdentity()
	if err != nil {
		f.Fatal(err)
	}
	provisioning := TenantMigrationIntent{
		Source:      source,
		Destination: dest,
		Tenant:      "tenant-secret-value",
		Realm:       "customer_a",
		Database:    "production",
		State:       TenantMigrationProvisioning,
	}
	complete := provisioning
	complete.State = TenantMigrationComplete
	complete.Tables = 3
	complete.Rows = 4096
	f.Add(encodeTenantMigrationIntent(provisioning))
	f.Add(encodeTenantMigrationIntent(complete))
	f.Add([]byte{})
	f.Add(make([]byte, 64+1+4+8))

	f.Fuzz(func(t *testing.T, raw []byte) {
		intent, err := decodeTenantMigrationIntent(raw)
		if err != nil {
			return
		}
		// A decoded intent that also passes the durable validator must
		// round-trip byte-for-byte, otherwise a rewrite could silently
		// alter migration state.
		if err := validateTenantMigrationIntent(intent, true); err != nil {
			return
		}
		again, err := decodeTenantMigrationIntent(encodeTenantMigrationIntent(intent))
		if err != nil {
			t.Fatalf("re-encoded intent did not decode: %v", err)
		}
		if !sameTenantMigration(again, intent) || again.State != intent.State ||
			again.Tables != intent.Tables || again.Rows != intent.Rows {
			t.Fatalf("round-trip mismatch: %+v vs %+v", again, intent)
		}
	})
}
