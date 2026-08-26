package executor

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/bzync/nextsql/internal/scheduler"
)

func BenchmarkHybrid(b *testing.B) {
	db := testDB(b)
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: 1, Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, BatchSize: 1024})
	if _, err := s.Exec(`CREATE TABLE products (
		id UUID PRIMARY KEY DEFAULT UUID(),
		name STRING NOT NULL,
		price DECIMAL(12,2),
		description TEXT,
		metadata JSON,
		embedding VECTOR<F32,8>
	)`); err != nil {
		b.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		cat := "headphones"
		if i%5 == 0 {
			cat = "home"
		}
		sql := fmt.Sprintf(`INSERT INTO products (name, price, description, metadata, embedding) VALUES ('p%d', %d, 'wireless noise cancelling item %d', '{"category":"%s"}', (%d, %d, 0, 0, 0, 0, 0, 0))`,
			i, 1000+i*10, i, cat, i%3, 3-i%3)
		if _, err := s.Exec(sql); err != nil {
			b.Fatal(err)
		}
	}
	if _, err := s.Exec(`CREATE INDEX ix_cat ON products (metadata.category)`); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Exec(`CREATE FULLTEXT INDEX ix_desc ON products (description)`); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Exec(`CREATE VECTOR INDEX ix_emb ON products (embedding) USING HNSW`); err != nil {
		b.Fatal(err)
	}
	s.SetLimits(scheduler.Limits{Workers: runtime.GOMAXPROCS(0), Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, BatchSize: 1024})
	q := `SELECT name, price FROM products
		WHERE metadata.category = 'headphones' AND price <= 15000
		SEARCH description FOR 'wireless noise cancelling'
		NEAREST embedding TO (1, 0, 0, 0, 0, 0, 0, 0)
		LIMIT 10`
	if _, err := s.Exec(q); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Exec(q); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkScanAndAgg(b *testing.B) {
	db := testDB(b)
	s := db.Session()
	s.SetLimits(scheduler.Limits{Workers: runtime.GOMAXPROCS(0), Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, BatchSize: 1024})
	if _, err := s.Exec(`CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), k STRING NOT NULL, n DECIMAL(10,0))`); err != nil {
		b.Fatal(err)
	}
	const n = 2000
	for i := 0; i < n; i++ {
		sql := fmt.Sprintf(`INSERT INTO t (k, n) VALUES ('%c', %d)`, 'a'+rune(i%10), i)
		if _, err := s.Exec(sql); err != nil {
			b.Fatal(err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Exec(`SELECT k, COUNT(*) FROM t GROUP BY k`); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkBatchSizes(b *testing.B) {
	for _, sz := range []int{1024, 2048, 4096} {
		b.Run(fmt.Sprintf("batch%d", sz), func(b *testing.B) {
			db := testDB(b)
			s := db.Session()
			s.SetLimits(scheduler.Limits{Workers: 2, Memory: 64 << 20, Disk: 64 << 20, IO: 1 << 30, BatchSize: sz})
			if _, err := s.Exec(`CREATE TABLE t (id UUID PRIMARY KEY DEFAULT UUID(), n STRING NOT NULL)`); err != nil {
				b.Fatal(err)
			}
			for i := 0; i < 512; i++ {
				if _, err := s.Exec(`INSERT INTO t (n) VALUES ('x')`); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if _, err := s.Exec(`SELECT n FROM t`); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
