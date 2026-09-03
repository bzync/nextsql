package optimizer

import (
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
)

// The partition-pruning soundness contract: for any predicate, every row that
// satisfies the predicate must live in a partition that survives pruning.
// Pruning may keep extra partitions (conservative), but it must never drop a
// partition that could hold a matching row — that would silently lose rows from
// a scan. This property test generates random RANGE/HASH/LIST schemes (single
// and multi-column) plus random predicates, enumerates the whole key space, and
// checks the invariant against the authoritative pruner used by real scans
// (prunePartitionsForExplain, reached from partitionAccessDetail).

const prunePropAlphabet = "abcdefgh"

func prunePropLetters() []string {
	out := make([]string, len(prunePropAlphabet))
	for i := range prunePropAlphabet {
		out[i] = string(prunePropAlphabet[i])
	}
	return out
}

// prunePred is a generated predicate: its AST and a reference evaluator over the
// partition-key tuple plus the unrelated "noise" column value.
type prunePred struct {
	expr ast.Expr
	eval func(key []string, noise string) bool
}

func lit(s string) ast.Expr { return ast.Literal{Value: types.StringValue(s)} }
func bin(op string, l, r ast.Expr) ast.Expr {
	return ast.Binary{Op: op, Left: l, Right: r}
}

func genSingleColPred(r *rand.Rand, col string) prunePred {
	id := ast.Ident{Name: col}
	pick := func() string { return string(prunePropAlphabet[r.Intn(len(prunePropAlphabet))]) }
	var p prunePred
	switch r.Intn(8) {
	case 0:
		v := pick()
		p.expr = bin("=", id, lit(v))
		p.eval = func(k []string, _ string) bool { return k[0] == v }
	case 1:
		v := pick()
		p.expr = bin("<", id, lit(v))
		p.eval = func(k []string, _ string) bool { return k[0] < v }
	case 2:
		v := pick()
		p.expr = bin("<=", id, lit(v))
		p.eval = func(k []string, _ string) bool { return k[0] <= v }
	case 3:
		v := pick()
		p.expr = bin(">", id, lit(v))
		p.eval = func(k []string, _ string) bool { return k[0] > v }
	case 4:
		v := pick()
		p.expr = bin(">=", id, lit(v))
		p.eval = func(k []string, _ string) bool { return k[0] >= v }
	case 5:
		a, b := pick(), pick()
		if a > b {
			a, b = b, a
		}
		p.expr = ast.Between{Expr: id, Low: lit(a), High: lit(b)}
		p.eval = func(k []string, _ string) bool { return k[0] >= a && k[0] <= b }
	case 6:
		a, b := pick(), pick()
		p.expr = bin("or", bin("=", id, lit(a)), bin("=", id, lit(b)))
		p.eval = func(k []string, _ string) bool { return k[0] == a || k[0] == b }
	case 7:
		a, b := pick(), pick()
		if a > b {
			a, b = b, a
		}
		p.expr = bin("and", bin(">=", id, lit(a)), bin("<", id, lit(b)))
		p.eval = func(k []string, _ string) bool { return k[0] >= a && k[0] < b }
	}
	if r.Intn(2) == 0 {
		inner, innerEval := p.expr, p.eval
		p.expr = bin("and", inner, bin("=", ast.Ident{Name: "noise"}, lit("q")))
		p.eval = func(k []string, noise string) bool { return innerEval(k, noise) && noise == "q" }
	}
	return p
}

func genMultiColPred(r *rand.Rand, c0, c1 string) prunePred {
	id0, id1 := ast.Ident{Name: c0}, ast.Ident{Name: c1}
	pick := func() string { return string(prunePropAlphabet[r.Intn(len(prunePropAlphabet))]) }
	var p prunePred
	switch r.Intn(6) {
	case 0:
		a := pick()
		p.expr = bin("=", id0, lit(a))
		p.eval = func(k []string, _ string) bool { return k[0] == a }
	case 1:
		a, b := pick(), pick()
		p.expr = bin("and", bin("=", id0, lit(a)), bin("=", id1, lit(b)))
		p.eval = func(k []string, _ string) bool { return k[0] == a && k[1] == b }
	case 2:
		a, b := pick(), pick()
		p.expr = bin("and", bin("=", id0, lit(a)), bin("<", id1, lit(b)))
		p.eval = func(k []string, _ string) bool { return k[0] == a && k[1] < b }
	case 3:
		a := pick()
		p.expr = bin("<", id0, lit(a))
		p.eval = func(k []string, _ string) bool { return k[0] < a }
	case 4:
		a, b := pick(), pick()
		p.expr = bin("and", bin("=", id0, lit(a)), bin(">=", id1, lit(b)))
		p.eval = func(k []string, _ string) bool { return k[0] == a && k[1] >= b }
	case 5:
		a, b := pick(), pick()
		if a > b {
			a, b = b, a
		}
		p.expr = bin("and", bin(">=", id0, lit(a)), bin("<", id0, lit(b)))
		p.eval = func(k []string, _ string) bool { return k[0] >= a && k[0] < b }
	}
	return p
}

func strTuple(ss ...string) []types.Value {
	out := make([]types.Value, len(ss))
	for i, s := range ss {
		out[i] = types.StringValue(s)
	}
	return out
}

// randomPartitionedTable returns a valid partitioned table and its key arity.
func randomPartitionedTable(r *rand.Rand) (*catalog.Table, int) {
	switch r.Intn(4) {
	case 0:
		return buildRange1(r), 1
	case 1:
		return buildList1(r), 1
	case 2:
		return buildHash1(r), 1
	default:
		return buildRange2(r), 2
	}
}

func singleColTable(parts []catalog.Partition, kind catalog.PartitionKind) *catalog.Table {
	return &catalog.Table{
		ID: 1, Name: "t", HeapMeta: 7,
		Columns: []catalog.Column{
			{Name: "pk", Type: types.String(), NotNull: true, Primary: true},
			{Name: "noise", Type: types.String()},
		},
		PK: []int{0},
		Partitioning: &catalog.Partitioning{
			Kind: kind, NextID: uint32(len(parts)) + 1, Columns: []int{0}, Partitions: parts,
		},
	}
}

func buildRange1(r *rand.Rand) *catalog.Table {
	set := map[string]struct{}{}
	for len(set) < 1+r.Intn(4) {
		set[string(prunePropAlphabet[r.Intn(len(prunePropAlphabet))])] = struct{}{}
	}
	splits := make([]string, 0, len(set))
	for s := range set {
		splits = append(splits, s)
	}
	sort.Strings(splits)
	parts := make([]catalog.Partition, 0, len(splits)+1)
	for i := 0; i <= len(splits); i++ {
		var lo, hi []types.Value
		lowerInc := false
		if i > 0 {
			lo = strTuple(splits[i-1])
			lowerInc = true
		}
		if i < len(splits) {
			hi = strTuple(splits[i])
		}
		parts = append(parts, catalog.Partition{
			ID: uint32(i + 1), Name: fmt.Sprintf("r%d", i), HeapMeta: format.PageID(100 + i),
			LowerInclusive: lowerInc, Values: [][]types.Value{lo, hi},
		})
	}
	return singleColTable(parts, catalog.PartitionRange)
}

func buildList1(r *rand.Rand) *catalog.Table {
	letters := prunePropLetters()
	r.Shuffle(len(letters), func(i, j int) { letters[i], letters[j] = letters[j], letters[i] })
	n := 2 + r.Intn(3)
	if n > len(letters) {
		n = len(letters)
	}
	buckets := make([][]string, n)
	for i := 0; i < n; i++ {
		buckets[i] = append(buckets[i], letters[i]) // one per partition
	}
	for _, l := range letters[n:] {
		if r.Intn(3) == 0 {
			continue // leave some letters unrouted (LIST gap)
		}
		b := r.Intn(n)
		buckets[b] = append(buckets[b], l)
	}
	parts := make([]catalog.Partition, 0, n)
	for i, b := range buckets {
		vals := make([][]types.Value, 0, len(b))
		for _, l := range b {
			vals = append(vals, strTuple(l))
		}
		parts = append(parts, catalog.Partition{
			ID: uint32(i + 1), Name: fmt.Sprintf("l%d", i), HeapMeta: format.PageID(200 + i), Values: vals,
		})
	}
	return singleColTable(parts, catalog.PartitionList)
}

func buildHash1(r *rand.Rand) *catalog.Table {
	n := uint32(2 + r.Intn(5))
	parts := make([]catalog.Partition, 0, n)
	for i := uint32(0); i < n; i++ {
		parts = append(parts, catalog.Partition{
			ID: i + 1, Name: fmt.Sprintf("h%d", i), HeapMeta: format.PageID(300 + int(i)),
			Modulus: n, Remainder: i,
		})
	}
	return singleColTable(parts, catalog.PartitionHash)
}

func buildRange2(r *rand.Rand) *catalog.Table {
	type pair struct{ a, b string }
	set := map[pair]struct{}{}
	for len(set) < 1+r.Intn(4) {
		p := pair{
			string(prunePropAlphabet[r.Intn(len(prunePropAlphabet))]),
			string(prunePropAlphabet[r.Intn(len(prunePropAlphabet))]),
		}
		set[p] = struct{}{}
	}
	bounds := make([]pair, 0, len(set))
	for p := range set {
		bounds = append(bounds, p)
	}
	sort.Slice(bounds, func(i, j int) bool {
		if bounds[i].a != bounds[j].a {
			return bounds[i].a < bounds[j].a
		}
		return bounds[i].b < bounds[j].b
	})
	parts := make([]catalog.Partition, 0, len(bounds)+1)
	for i := 0; i <= len(bounds); i++ {
		var lo, hi []types.Value
		lowerInc := false
		if i > 0 {
			lo = strTuple(bounds[i-1].a, bounds[i-1].b)
			lowerInc = true
		}
		if i < len(bounds) {
			hi = strTuple(bounds[i].a, bounds[i].b)
		}
		parts = append(parts, catalog.Partition{
			ID: uint32(i + 1), Name: fmt.Sprintf("r%d", i), HeapMeta: format.PageID(400 + i),
			LowerInclusive: lowerInc, Values: [][]types.Value{lo, hi},
		})
	}
	return &catalog.Table{
		ID: 1, Name: "t", HeapMeta: 7,
		Columns: []catalog.Column{
			{Name: "c0", Type: types.String(), NotNull: true, Primary: true},
			{Name: "c1", Type: types.String(), NotNull: true, Primary: true},
			{Name: "noise", Type: types.String()},
		},
		PK: []int{0, 1},
		Partitioning: &catalog.Partitioning{
			Kind: catalog.PartitionRange, NextID: uint32(len(parts)) + 1, Columns: []int{0, 1}, Partitions: parts,
		},
	}
}

func allKeyTuples(ncols int) [][]string {
	letters := prunePropLetters()
	if ncols == 1 {
		out := make([][]string, 0, len(letters))
		for _, l := range letters {
			out = append(out, []string{l})
		}
		return out
	}
	out := make([][]string, 0, len(letters)*len(letters))
	for _, a := range letters {
		for _, b := range letters {
			out = append(out, []string{a, b})
		}
	}
	return out
}

func buildRow(ncols int, key []string, noise string) []types.Value {
	row := make([]types.Value, 0, ncols+1)
	for _, k := range key {
		row = append(row, types.StringValue(k))
	}
	row = append(row, types.StringValue(noise))
	return row
}

func partNames(ps []catalog.Partition) []string {
	out := make([]string, len(ps))
	for i, p := range ps {
		out[i] = p.Name
	}
	return out
}

func describeScheme(tab *catalog.Table) string {
	p := tab.Partitioning
	var sb strings.Builder
	fmt.Fprintf(&sb, "kind=%d cols=%v", p.Kind, p.Columns)
	for _, part := range p.Partitions {
		fmt.Fprintf(&sb, " {%s id=%d mod=%d rem=%d lowInc=%v vals=%v}",
			part.Name, part.ID, part.Modulus, part.Remainder, part.LowerInclusive, part.Values)
	}
	return sb.String()
}

func TestPartitionPruningSoundness(t *testing.T) {
	r := rand.New(rand.NewSource(0x9021))
	const iters = 4000
	prunedSubset := 0
	for iter := 0; iter < iters; iter++ {
		tab, ncols := randomPartitionedTable(r)
		if err := catalog.ValidatePartitioning(tab); err != nil {
			t.Fatalf("iter %d: generator produced an invalid scheme: %v\n%s", iter, err, describeScheme(tab))
		}
		var pred prunePred
		if ncols == 1 {
			pred = genSingleColPred(r, "pk")
		} else {
			pred = genMultiColPred(r, "c0", "c1")
		}
		pruned := prunePartitionsForExplain(tab, pred.expr)
		if len(pruned) < len(tab.Partitioning.Partitions) {
			prunedSubset++
		}
		survive := make(map[uint32]bool, len(pruned))
		for _, p := range pruned {
			survive[p.ID] = true
			// every surviving partition must be a real member
			found := false
			for _, m := range tab.Partitioning.Partitions {
				if m.ID == p.ID {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("iter %d: pruner returned phantom partition id %d\n%s", iter, p.ID, describeScheme(tab))
			}
		}
		for _, key := range allKeyTuples(ncols) {
			for _, noise := range []string{"q", "z"} {
				row := buildRow(ncols, key, noise)
				owner, err := tab.PartitionForRow(row)
				if err != nil || owner == nil {
					continue // unrouted row (LIST gap) — nothing to prune for
				}
				if pred.eval(key, noise) && !survive[owner.ID] {
					t.Fatalf("iter %d: row key=%v noise=%q matches the predicate but its owner %q (id %d) was pruned\nscheme: %s\npruned:  %v",
						iter, key, noise, owner.Name, owner.ID, describeScheme(tab), partNames(pruned))
				}
			}
		}
	}
	// Guard against the property becoming vacuous: a healthy fraction of the
	// generated (scheme, predicate) pairs must actually prune something.
	t.Logf("pruning fired in %d/%d iterations", prunedSubset, iters)
	if prunedSubset < iters/10 {
		t.Fatalf("pruning almost never fired (%d/%d iterations) — the soundness property is not being exercised", prunedSubset, iters)
	}
}
