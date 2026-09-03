package diskspace

import "testing"

func TestStatRealFilesystem(t *testing.T) {
	u, err := Stat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if u.TotalBytes == 0 {
		t.Fatal("expected a nonzero total on a real filesystem")
	}
	if u.FreeBytes > u.TotalBytes {
		t.Fatalf("free (%d) must not exceed total (%d)", u.FreeBytes, u.TotalBytes)
	}
}

func TestStatRejectsMissingPath(t *testing.T) {
	if _, err := Stat("/this/path/almost-certainly-does-not-exist-nextsql-test"); err == nil {
		t.Fatal("expected an error for a nonexistent path")
	}
}

func TestUsedFraction(t *testing.T) {
	cases := []struct {
		u    Usage
		want float64
	}{
		{Usage{TotalBytes: 100, FreeBytes: 100}, 0},
		{Usage{TotalBytes: 100, FreeBytes: 0}, 1},
		{Usage{TotalBytes: 100, FreeBytes: 25}, 0.75},
		{Usage{TotalBytes: 0, FreeBytes: 0}, 0}, // no divide-by-zero
	}
	for _, tc := range cases {
		if got := tc.u.UsedFraction(); got != tc.want {
			t.Errorf("%+v.UsedFraction() = %v, want %v", tc.u, got, tc.want)
		}
	}
}
