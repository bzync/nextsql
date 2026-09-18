package executor

import (
	"strconv"
	"testing"

	"github.com/bzync/nextsql/internal/metrics"
	"github.com/bzync/nextsql/internal/security"
)

// The WAL footprint gauges must be populated whether or not a size cap is
// configured: on a deployment with no cap, that number is the only thing
// that shows the WAL growing without bound. wal_bytes_written cannot — it
// counts bytes ever appended and never falls.
func TestSystemMetricsReportWALFootprint(t *testing.T) {
	db := testDB(t)
	reg := metrics.New()
	db.SetMetricsSource(func() *metrics.Registry { return reg })
	db.SetMetrics(reg)

	if err := db.ObserveWALFootprint(); err != nil {
		t.Fatal(err)
	}

	aclPath := t.TempDir() + "/acl.db"
	acl, err := security.CreateACL(aclPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("root", security.PrivConnect, security.ScopeDatabase, ""); err != nil {
		t.Fatal(err)
	}
	if err := acl.Grant("root", security.PrivAdmin, security.ScopeCluster, ""); err != nil {
		t.Fatal(err)
	}
	root := db.Session()
	root.SetACL(acl)
	root.SetIdentity("root")

	seen := map[string]string{}
	for _, r := range execOK(t, root, "SELECT * FROM system.metrics").Rows {
		seen[r[1].Str] = r[2].Str
	}
	for _, name := range []string{"wal_on_disk_bytes", "wal_segments", "wal_trimmed_segments"} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("system.metrics is missing %s", name)
		}
	}
	bytes, err := strconv.ParseInt(seen["wal_on_disk_bytes"], 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	segs, err := strconv.Atoi(seen["wal_segments"])
	if err != nil {
		t.Fatal(err)
	}
	if segs < 1 || bytes < 1 {
		t.Fatalf("an open database must report at least one WAL segment: %d segments, %d bytes", segs, bytes)
	}
}

// The default is unchanged behaviour: no cap, nothing trimmed.
func TestTrimWALIsInertWithoutACap(t *testing.T) {
	db := testDB(t)
	n, err := db.TrimWAL()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("trimmed %d segments with no cap configured", n)
	}

	// A cap far above anything a fresh database holds is equally inert, but
	// must run the pass rather than refuse it.
	db.SetWALSizeCap(1 << 40)
	n, err = db.TrimWAL()
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("trimmed %d segments under a 1 TiB cap", n)
	}
}
