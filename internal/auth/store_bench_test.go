package auth

import (
	"path/filepath"
	"testing"
)

// These benchmarks characterize the CPU/memory cost of one password-hash
// verification under each algorithm, and the throughput of concurrent login
// attempts against a live Store — the capacity-planning input for
// authentication-DoS resistance (TODO.md P25 "Authentication DoS benchmark").

func BenchmarkVerifyPBKDF2(b *testing.B) {
	rec, err := hashPasswordPBKDF2("s3cret", defaultIter)
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := checkPassword("s3cret", rec); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkVerifyArgon2id(b *testing.B) {
	rec, err := hashPasswordArgon2id("s3cret")
	if err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := checkPassword("s3cret", rec); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkConcurrentLoginAttempts drives Store.Verify from many goroutines
// at once — a mix of correct and incorrect passwords, matching what a
// credential-stuffing or brute-force burst looks like against a live
// server — and reports realized throughput/latency including lock
// contention and (once) the legacy-record rehash path.
func BenchmarkConcurrentLoginAttempts(b *testing.B) {
	s, err := Create(filepath.Join(b.TempDir(), "users"))
	if err != nil {
		b.Fatal(err)
	}
	if err := s.Upsert("app", "s3cret"); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			pw := "s3cret"
			if i%4 == 0 {
				pw = "wrong"
			}
			_ = s.Verify("app", pw)
			i++
		}
	})
}
