package auth

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
)

func TestUpsertVerifyRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nextsql.users")
	s, err := Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("App", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "wrong"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("wrong password: %v", err)
	}
	if err := s.Verify("missing", "s3cret"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("missing user: %v", err)
	}

	re, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := re.Verify("APP", "s3cret"); err != nil {
		t.Fatal(err)
	}
}

func TestDeleteUser(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("app", "s3cret"); err != nil {
		t.Fatal(err)
	}
	if err := s.Delete("app"); err != nil {
		t.Fatal(err)
	}
	if err := s.Verify("app", "s3cret"); !nerr.HasCode(err, nerr.Unauthorized) {
		t.Fatalf("deleted user still verifies: %v", err)
	}
}

func TestRejectEmptyPassword(t *testing.T) {
	s, err := Create(filepath.Join(t.TempDir(), "users"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Upsert("app", ""); err == nil {
		t.Fatal("expected error")
	}
}

func TestDecodeRejectsGarbage(t *testing.T) {
	cases := [][]byte{
		nil,
		[]byte("xxxx"),
		[]byte("NSAU"),
		append([]byte("NSAU"), 0, 99, 0, 0, 0, 1),
		append([]byte("BAD!"), 1, 0, 0, 0, 0, 0),
	}
	for _, c := range cases {
		if _, err := Decode(c); err == nil {
			t.Fatalf("accepted %q", c)
		}
	}
}

func FuzzDecode(f *testing.F) {
	s, err := Create(filepath.Join(f.TempDir(), "users"))
	if err != nil {
		f.Fatal(err)
	}
	if err := s.Upsert("app", "s3cret"); err != nil {
		f.Fatal(err)
	}
	good, err := os.ReadFile(s.Path())
	if err != nil {
		f.Fatal(err)
	}
	f.Add(good)
	f.Add([]byte("NSAU"))
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, raw []byte) {
		users, err := Decode(raw)
		if err != nil {
			if users != nil {
				t.Fatalf("error with non-nil map")
			}
			return
		}
		if users == nil {
			t.Fatalf("nil map without error")
		}
	})
}
