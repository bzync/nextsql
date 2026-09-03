package catalog

import "testing"

func TestResourceGroupCodec(t *testing.T) {
	want := &ResourceGroup{ID: 7, Name: "reporting", Owner: "admin", MaxConcurrency: 16, MemoryBytes: 1 << 30, Workers: 4, Priority: 3}
	raw, err := EncodeResourceGroup(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeResourceGroup(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Owner != want.Owner || got.MaxConcurrency != want.MaxConcurrency || got.MemoryBytes != want.MemoryBytes || got.Workers != want.Workers || got.Priority != want.Priority {
		t.Fatalf("resource group=%#v", got)
	}
	clone := want.Clone()
	if clone == want || clone.Name != want.Name || clone.MemoryBytes != want.MemoryBytes {
		t.Fatalf("clone=%#v", clone)
	}
}

func TestResourceGroupCodecZeroOptionsMeanUnbounded(t *testing.T) {
	want := &ResourceGroup{ID: 1, Name: "default", Owner: "admin"}
	raw, err := EncodeResourceGroup(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeResourceGroup(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.MaxConcurrency != 0 || got.MemoryBytes != 0 || got.Workers != 0 || got.Priority != 0 {
		t.Fatalf("expected zero-valued options to round-trip as unbounded, got %#v", got)
	}
}

func TestResourceGroupValidateRejectsOutOfRange(t *testing.T) {
	cases := []*ResourceGroup{
		nil,
		{ID: 0, Name: "x", Owner: "admin"},
		{ID: 1, Name: "", Owner: "admin"},
		{ID: 1, Name: "x", Owner: ""},
		{ID: 1, Name: "x", Owner: "admin", MaxConcurrency: -1},
		{ID: 1, Name: "x", Owner: "admin", MaxConcurrency: MaxResourceGroupConcurrency + 1},
		{ID: 1, Name: "x", Owner: "admin", MemoryBytes: -1},
		{ID: 1, Name: "x", Owner: "admin", MemoryBytes: MaxResourceGroupMemoryBytes + 1},
		{ID: 1, Name: "x", Owner: "admin", Workers: -1},
		{ID: 1, Name: "x", Owner: "admin", Workers: MaxResourceGroupWorkers + 1},
		{ID: 1, Name: "x", Owner: "admin", Priority: -1},
		{ID: 1, Name: "x", Owner: "admin", Priority: MaxResourceGroupPriority + 1},
	}
	for i, g := range cases {
		if _, err := EncodeResourceGroup(g); err == nil {
			t.Fatalf("case %d: expected error for %#v", i, g)
		}
	}
}

func TestResourceGroupDecodeRejectsBadMagicAndTrailingBytes(t *testing.T) {
	if _, err := DecodeResourceGroup([]byte("not a resource group")); err == nil {
		t.Fatal("expected bad magic error")
	}
	raw, err := EncodeResourceGroup(&ResourceGroup{ID: 1, Name: "x", Owner: "admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeResourceGroup(append(raw, 0xFF)); err == nil {
		t.Fatal("expected trailing bytes error")
	}
}

func FuzzDecodeResourceGroup(f *testing.F) {
	seed, err := EncodeResourceGroup(&ResourceGroup{ID: 1, Name: "reporting", Owner: "admin", MaxConcurrency: 8, MemoryBytes: 1 << 20, Workers: 2, Priority: 1})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Fuzz(func(t *testing.T, raw []byte) {
		got, err := DecodeResourceGroup(raw)
		if err != nil {
			return
		}
		reencoded, err := EncodeResourceGroup(got)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := DecodeResourceGroup(reencoded); err != nil {
			t.Fatal(err)
		}
	})
}
