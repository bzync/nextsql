package setup

import (
	"reflect"
	"testing"
)

func TestInstallRollbackSkipsPreexisting(t *testing.T) {
	r := NewInstallRollback()
	r.Observe("/data", true)  // operator's existing dir
	r.Observe("/data/db", false)
	r.Observe("/key", true) // operator supplied their own key

	r.Track("/data")
	r.Track("/data/db")
	r.Track("/key")

	got := r.Plan()
	want := []string{"/data/db"} // /data and /key were preexisting
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan() = %v, want %v", got, want)
	}
}

func TestInstallRollbackReverseOrder(t *testing.T) {
	r := NewInstallRollback()
	for _, p := range []string{"/d", "/d/a", "/d/a/b"} {
		r.Observe(p, false)
		r.Track(p)
	}
	got := r.Plan()
	want := []string{"/d/a/b", "/d/a", "/d"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Plan() = %v, want %v", got, want)
	}
}

func TestInstallRollbackDedup(t *testing.T) {
	r := NewInstallRollback()
	r.Observe("/x", false)
	r.Track("/x")
	r.Track("/x")
	r.Track("/x")
	if got := r.Plan(); len(got) != 1 {
		t.Fatalf("Plan() = %v, want one entry", got)
	}
}

func TestInstallRollbackUnobservedIsNew(t *testing.T) {
	r := NewInstallRollback()
	r.Track("/never-observed")
	if r.Empty() {
		t.Fatal("an unobserved tracked path should be in the plan")
	}
	if got := r.Plan(); !reflect.DeepEqual(got, []string{"/never-observed"}) {
		t.Fatalf("Plan() = %v", got)
	}
}

func TestInstallRollbackObserveKeepsFirstReading(t *testing.T) {
	r := NewInstallRollback()
	r.Observe("/p", true)
	r.Observe("/p", false) // a later stat after we created it — must not flip
	r.Track("/p")
	if !r.Empty() {
		t.Fatalf("Plan() = %v, want empty (path was preexisting on first observation)", r.Plan())
	}
}

func TestInstallRollbackEmpty(t *testing.T) {
	r := NewInstallRollback()
	if !r.Empty() {
		t.Fatal("a fresh rollback is not empty")
	}
	r.Observe("/a", true)
	r.Track("/a")
	if !r.Empty() {
		t.Fatal("tracking only a preexisting path should leave the plan empty")
	}
}
