package sort

import (
	"container/heap"
	"sort"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// Key is one ORDER BY column of a projected row.
type Key struct {
	Col  int
	Desc bool
}

type topItem struct {
	row []types.Value
	seq int
}

type topHeap struct {
	items []topItem
	keys  []Key
	err   error
}

func (h *topHeap) Len() int { return len(h.items) }
func (h *topHeap) Less(i, j int) bool {
	if h.err != nil {
		return false
	}
	cmp, err := Compare(h.items[i].row, h.items[j].row, h.keys)
	if err != nil {
		h.err = err
		return false
	}
	if cmp == 0 {
		return h.items[i].seq > h.items[j].seq
	}
	return cmp > 0 // max-heap: worst retained row is the root
}
func (h *topHeap) Swap(i, j int) { h.items[i], h.items[j] = h.items[j], h.items[i] }
func (h *topHeap) Push(x any)    { h.items = append(h.items, x.(topItem)) }
func (h *topHeap) Pop() any {
	n := len(h.items)
	x := h.items[n-1]
	h.items = h.items[:n-1]
	return x
}

// TopRows returns the first n rows in key order using O(n) retained entries.
func TopRows(rows [][]types.Value, keys []Key, n int64) ([][]types.Value, error) {
	if n <= 0 || len(rows) == 0 {
		return nil, nil
	}
	if n >= int64(len(rows)) {
		if err := Rows(rows, keys); err != nil {
			return nil, err
		}
		return rows, nil
	}
	h := &topHeap{keys: keys, items: make([]topItem, 0, int(n))}
	heap.Init(h)
	for seq, row := range rows {
		item := topItem{row: row, seq: seq}
		if int64(h.Len()) < n {
			heap.Push(h, item)
		} else {
			cmp, err := Compare(row, h.items[0].row, keys)
			if err != nil {
				return nil, err
			}
			if cmp < 0 {
				h.items[0] = item
				heap.Fix(h, 0)
			}
		}
		if h.err != nil {
			return nil, h.err
		}
	}
	out := make([][]types.Value, len(h.items))
	for i, item := range h.items {
		out[i] = item.row
	}
	if err := Rows(out, keys); err != nil {
		return nil, err
	}
	return out, nil
}

// Rows sorts projected rows in place. NULLS LAST for ASC, NULLS FIRST for DESC.
func Rows(rows [][]types.Value, keys []Key) error {
	if len(keys) == 0 || len(rows) < 2 {
		return nil
	}
	var sortErr error
	sort.SliceStable(rows, func(i, j int) bool {
		if sortErr != nil {
			return false
		}
		cmp, err := Compare(rows[i], rows[j], keys)
		if err != nil {
			sortErr = err
			return false
		}
		return cmp < 0
	})
	return sortErr
}

// Compare returns -1 if a < b, 0 if equal, 1 if a > b under keys.
func Compare(a, b []types.Value, keys []Key) (int, error) {
	for _, k := range keys {
		if k.Col < 0 || k.Col >= len(a) || k.Col >= len(b) {
			return 0, nerr.New(nerr.Internal, "executor.sort", "ORDER BY column out of range")
		}
		av, bv := a[k.Col], b[k.Col]
		if av.Null && bv.Null {
			continue
		}
		if av.Null {
			if k.Desc {
				return -1, nil
			}
			return 1, nil
		}
		if bv.Null {
			if k.Desc {
				return 1, nil
			}
			return -1, nil
		}
		c, err := av.Cmp(bv)
		if err != nil {
			return 0, err
		}
		if c == 0 {
			continue
		}
		if k.Desc {
			return -c, nil
		}
		return c, nil
	}
	return 0, nil
}
