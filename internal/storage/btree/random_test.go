package btree

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage"
)

func TestRandomizedInsertDeleteScan(t *testing.T) {
	const (
		ops  = 2000
		seed = int64(0xB7EE)
	)
	rng := rand.New(rand.NewSource(seed))
	tr, _ := testTree(t, 64)
	oracle := map[string][]byte{}

	apply := func(i int, op string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("seed %d op %d %s: %v", seed, i, op, err)
		}
	}

	for i := 0; i < ops; i++ {
		switch rng.Intn(10) {
		case 0, 1, 2, 3:
			k := randomKey(rng)
			v := randomVal(rng)
			err := tr.Insert(k, v)
			if _, exists := oracle[string(k)]; exists {
				if !nerr.HasCode(err, nerr.AlreadyExists) {
					apply(i, "dup-insert", fmt.Errorf("want already exists, got %v", err))
				}
				break
			}
			apply(i, "insert", err)
			oracle[string(k)] = append([]byte(nil), v...)
		case 4, 5:
			if len(oracle) == 0 {
				continue
			}
			k := pickOracleKey(rng, oracle)
			err := tr.Delete([]byte(k))
			apply(i, "delete", err)
			delete(oracle, k)
		case 6:
			if len(oracle) == 0 {
				if _, err := tr.Lookup([]byte("nope")); !nerr.HasCode(err, nerr.NotFound) {
					apply(i, "lookup-empty", fmt.Errorf("%v", err))
				}
				break
			}
			k := pickOracleKey(rng, oracle)
			got, err := tr.Lookup([]byte(k))
			apply(i, "lookup", err)
			if !bytes.Equal(got, oracle[k]) {
				t.Fatalf("seed %d op %d lookup mismatch", seed, i)
			}
		default:
			if err := compareScan(tr, oracle); err != nil {
				t.Fatalf("seed %d op %d scan: %v", seed, i, err)
			}
		}
		if i%50 == 0 || i == ops-1 {
			if err := tr.Check(); err != nil {
				t.Fatalf("seed %d op %d check: %v", seed, i, err)
			}
			if err := compareOracle(tr, oracle); err != nil {
				t.Fatalf("seed %d op %d: %v", seed, i, err)
			}
		}
	}

	if err := compareScan(tr, oracle); err != nil {
		t.Fatal(err)
	}
}

func TestRandomizedRestart(t *testing.T) {
	const (
		n    = 400
		seed = int64(0x51A7)
	)
	rng := rand.New(rand.NewSource(seed))
	dir := t.TempDir()
	path := dir + "/nextsql.db"
	keys := testKeys(t)
	e, err := storage.Create(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	tr, err := Create(e)
	if err != nil {
		t.Fatal(err)
	}
	oracle := map[string][]byte{}
	for i := 0; i < n; i++ {
		k := []byte(fmt.Sprintf("rk%04d", rng.Intn(500)))
		v := []byte(fmt.Sprintf("rv%08d", i))
		err := tr.Insert(k, v)
		if _, ok := oracle[string(k)]; ok {
			if !nerr.HasCode(err, nerr.AlreadyExists) {
				t.Fatalf("insert: %v", err)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		oracle[string(k)] = v
	}
	for i := 0; i < 80; i++ {
		if len(oracle) == 0 {
			break
		}
		k := pickOracleKey(rng, oracle)
		if err := tr.Delete([]byte(k)); err != nil {
			t.Fatal(err)
		}
		delete(oracle, k)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	if err := e.Close(); err != nil {
		t.Fatal(err)
	}

	e, err = storage.Open(path, keys, 64)
	if err != nil {
		t.Fatal(err)
	}
	defer e.Close()
	tr, err = Open(e)
	if err != nil {
		t.Fatal(err)
	}
	if err := tr.Check(); err != nil {
		t.Fatal(err)
	}
	if err := compareOracle(tr, oracle); err != nil {
		t.Fatal(err)
	}
	if err := compareScan(tr, oracle); err != nil {
		t.Fatal(err)
	}
}

func randomKey(rng *rand.Rand) []byte {
	n := 1 + rng.Intn(24)
	b := make([]byte, n)
	rng.Read(b)
	for i := range b {
		if b[i] == 0 {
			b[i] = 'x'
		}
	}
	return b
}

func randomVal(rng *rand.Rand) []byte {
	n := rng.Intn(64)
	b := make([]byte, n)
	rng.Read(b)
	return b
}

func pickOracleKey(rng *rand.Rand, oracle map[string][]byte) string {
	n := rng.Intn(len(oracle))
	i := 0
	for k := range oracle {
		if i == n {
			return k
		}
		i++
	}
	panic("empty oracle")
}

func compareOracle(tr *Tree, oracle map[string][]byte) error {
	for k, v := range oracle {
		got, err := tr.Lookup([]byte(k))
		if err != nil {
			return fmt.Errorf("lookup %q: %w", k, err)
		}
		if !bytes.Equal(got, v) {
			return fmt.Errorf("lookup %q: got %q want %q", k, got, v)
		}
	}
	return nil
}

func compareScan(tr *Tree, oracle map[string][]byte) error {
	seen := map[string][]byte{}
	var prev []byte
	if err := tr.Range(nil, nil, func(k, v []byte) error {
		if prev != nil && bytes.Compare(prev, k) >= 0 {
			return fmt.Errorf("scan out of order")
		}
		prev = append([]byte(nil), k...)
		seen[string(k)] = append([]byte(nil), v...)
		return nil
	}); err != nil {
		return err
	}
	if len(seen) != len(oracle) {
		return fmt.Errorf("scan len %d oracle %d", len(seen), len(oracle))
	}
	for k, v := range oracle {
		if !bytes.Equal(seen[k], v) {
			return fmt.Errorf("scan mismatch for %q", k)
		}
	}
	return nil
}
