package btree

import (
	"fmt"
	"testing"
)

func benchTree(b *testing.B, n int) *Tree {
	b.Helper()
	tr, _ := testTree(b, 128)
	for i := 0; i < n; i++ {
		if err := tr.Insert(keyOf(i), valOf(i)); err != nil {
			b.Fatal(err)
		}
	}
	return tr
}

func BenchmarkInsert(b *testing.B) {
	tr, _ := testTree(b, 128)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := tr.Insert([]byte(fmt.Sprintf("bk%08d", i)), []byte("row")); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkLookup(b *testing.B) {
	const n = 2000
	tr := benchTree(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := tr.Lookup(keyOf(i % n)); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRangeScan(b *testing.B) {
	const n = 2000
	tr := benchTree(b, n)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		count := 0
		if err := tr.Range(keyOf(100), keyOf(200), func(k, v []byte) error {
			count++
			return nil
		}); err != nil {
			b.Fatal(err)
		}
		if count != 100 {
			b.Fatalf("scan %d", count)
		}
	}
}
