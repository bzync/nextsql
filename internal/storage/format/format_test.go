package format

import "testing"

func TestConstants(t *testing.T) {
	if LogicalPageSize != 16384 {
		t.Fatalf("logical page size %d", LogicalPageSize)
	}
	if PhysicalPageSize != LogicalPageSize+EnvelopeHeaderSize+AuthTagSize+EnvelopePadSize {
		t.Fatalf("physical page size %d", PhysicalPageSize)
	}
	if PhysicalPageSize != 16448 {
		t.Fatalf("expected 16448 physical bytes, got %d", PhysicalPageSize)
	}
	if !HasMagic(append(MagicBytes[:], 0), 0) {
		t.Fatal("magic bytes")
	}
	if err := PageIDSuperblock.UserData(); err == nil {
		t.Fatal("page 0 must be reserved")
	}
	if err := PageID(1).UserData(); err != nil {
		t.Fatal(err)
	}
	if !PageTypeBTreeLeaf.Known() || !PageTypeBTreeInternal.Known() {
		t.Fatal("btree page types")
	}
	if PageTypeBTreeLeaf.String() != "btree_leaf" {
		t.Fatalf("leaf name %s", PageTypeBTreeLeaf)
	}
}

func TestNewIdentity(t *testing.T) {
	a, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	if a.DatabaseString() == b.DatabaseString() || a.FileString() == b.FileString() {
		t.Fatal("identities must be unique")
	}
	if len(a.DatabaseString()) != 32 {
		t.Fatalf("uuid hex %q", a.DatabaseString())
	}
}
