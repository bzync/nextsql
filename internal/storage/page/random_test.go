package page

import (
	"bytes"
	"fmt"
	"math/rand"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

func TestRandomizedSlottedOps(t *testing.T) {
	const ops = 2000
	seed := int64(0x5EED)
	rng := rand.New(rand.NewSource(seed))

	p := New(11, format.PageTypeSlotted)
	oracle := map[uint16][]byte{}

	for i := 0; i < ops; i++ {
		switch rng.Intn(6) {
		case 0, 1:
			n := rng.Intn(64)
			rec := make([]byte, n)
			rng.Read(rec)
			slot, err := p.Insert(rec)
			if err != nil {
				if !nerr.HasCode(err, nerr.PageFull) {
					t.Fatalf("seed %d op %d insert: %v", seed, i, err)
				}
				continue
			}
			oracle[slot] = append([]byte(nil), rec...)
		case 2:
			if len(oracle) == 0 {
				continue
			}
			slot := pickSlot(rng, oracle)
			if err := p.Delete(slot); err != nil {
				t.Fatalf("seed %d op %d delete slot %d: %v", seed, i, slot, err)
			}
			delete(oracle, slot)
		case 3:
			if len(oracle) == 0 {
				continue
			}
			slot := pickSlot(rng, oracle)
			n := rng.Intn(80)
			rec := make([]byte, n)
			rng.Read(rec)
			if err := p.Update(slot, rec); err != nil {
				if !nerr.HasCode(err, nerr.PageFull) {
					t.Fatalf("seed %d op %d update: %v", seed, i, err)
				}
				continue
			}
			oracle[slot] = append([]byte(nil), rec...)
		case 4:
			p.Compact()
		default:
			p.Finalize()
			got, err := Parse(p.Bytes())
			if err != nil {
				t.Fatalf("seed %d op %d parse: %v", seed, i, err)
			}
			p = got
		}
		if err := p.Validate(); err != nil {
			t.Fatalf("seed %d op %d validate: %v", seed, i, err)
		}
		if err := compareOracle(p, oracle); err != nil {
			t.Fatalf("seed %d op %d: %v", seed, i, err)
		}
	}
}

func pickSlot(rng *rand.Rand, oracle map[uint16][]byte) uint16 {
	n := rng.Intn(len(oracle))
	i := 0
	for slot := range oracle {
		if i == n {
			return slot
		}
		i++
	}
	panic("empty oracle")
}

func compareOracle(p *Page, oracle map[uint16][]byte) error {
	if p.LiveSlots() != len(oracle) {
		return fmt.Errorf("live slots %d != oracle %d", p.LiveSlots(), len(oracle))
	}
	for slot, want := range oracle {
		got, err := p.Get(slot)
		if err != nil {
			return fmt.Errorf("get %d: %w", slot, err)
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("slot %d: got %x want %x", slot, got, want)
		}
	}
	return nil
}
