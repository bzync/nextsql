package vector

import (
	"bytes"
	"container/heap"
	"hash/fnv"
	"math"
	"sort"

	"github.com/bzync/nextsql/internal/nerr"
)

// Graph is a durable or in-memory HNSW. The executor implements this
// over encrypted B+Trees; tests use Mem.
type Graph interface {
	LoadMeta() (Meta, error)
	SaveMeta(Meta) error
	LoadNode(pk []byte) (Node, bool, error)
	SaveNode(pk []byte, n Node) error
	LoadVec(pk []byte) ([]float32, error)
}

// Hit is one ranked neighbor. Dist is lower-is-closer.
type Hit struct {
	PK   []byte
	Dist float64
}

// LessHit orders closer first, then primary key.
func LessHit(a, b Hit) bool {
	if a.Dist != b.Dist {
		return a.Dist < b.Dist
	}
	return bytes.Compare(a.PK, b.PK) < 0
}

// Mem is an in-memory Graph used by tests and benches.
type Mem struct {
	Meta  Meta
	Nodes map[string]Node
	Vecs  map[string][]float32
}

func NewMem(dim uint16, metric Metric) *Mem {
	return &Mem{
		Meta:  DefaultMeta(dim, metric),
		Nodes: make(map[string]Node),
		Vecs:  make(map[string][]float32),
	}
}

func (m *Mem) LoadMeta() (Meta, error) { return m.Meta, nil }

func (m *Mem) SaveMeta(meta Meta) error {
	m.Meta = meta
	return nil
}

func (m *Mem) LoadNode(pk []byte) (Node, bool, error) {
	n, ok := m.Nodes[string(pk)]
	if !ok {
		return Node{}, false, nil
	}
	return cloneNode(n), true, nil
}

func (m *Mem) SaveNode(pk []byte, n Node) error {
	if m.Nodes == nil {
		m.Nodes = make(map[string]Node)
	}
	m.Nodes[string(pk)] = cloneNode(n)
	return nil
}

func (m *Mem) LoadVec(pk []byte) ([]float32, error) {
	v, ok := m.Vecs[string(pk)]
	if !ok {
		return nil, nerr.New(nerr.NotFound, "vector.Mem.LoadVec", "vector not found")
	}
	return append([]float32(nil), v...), nil
}

func (m *Mem) PutVec(pk []byte, v []float32) {
	if m.Vecs == nil {
		m.Vecs = make(map[string][]float32)
	}
	m.Vecs[string(pk)] = append([]float32(nil), v...)
}

func cloneNode(n Node) Node {
	out := Node{Level: n.Level, Deleted: n.Deleted, Neighbors: make([][][]byte, len(n.Neighbors))}
	for i, layer := range n.Neighbors {
		out.Neighbors[i] = make([][]byte, len(layer))
		for j, pk := range layer {
			out.Neighbors[i][j] = append([]byte(nil), pk...)
		}
	}
	return out
}

// NodeRanger walks HNSW vertices. Durable graphs implement this over the index tree.
type NodeRanger interface {
	RangeNodes(fn func(pk []byte, n Node) error) error
}

const (
	// RebuildMinTombstones avoids rebuilding small graphs for a handful of
	// deletes. RebuildTombstonePercent bounds graph bloat and dead-edge walks.
	RebuildMinTombstones    uint64 = 1024
	RebuildTombstonePercent uint64 = 20
)

// TombstoneStats describes physical HNSW vertices. Live is validated against
// the durable metadata count so corrupt/incomplete graphs fail closed.
type TombstoneStats struct {
	Total, Live, Deleted uint64
}

func InspectTombstones(g Graph) (TombstoneStats, error) {
	if g == nil {
		return TombstoneStats{}, nerr.New(nerr.InvalidArgument, "vector.InspectTombstones", "nil graph")
	}
	r, ok := g.(NodeRanger)
	if !ok {
		return TombstoneStats{}, nerr.New(nerr.InvalidArgument, "vector.InspectTombstones", "graph does not support node range")
	}
	meta, err := g.LoadMeta()
	if err != nil {
		return TombstoneStats{}, err
	}
	var st TombstoneStats
	if err := r.RangeNodes(func(_ []byte, n Node) error {
		st.Total++
		if n.Deleted {
			st.Deleted++
		} else {
			st.Live++
		}
		return nil
	}); err != nil {
		return TombstoneStats{}, err
	}
	if st.Live != meta.Count {
		return TombstoneStats{}, nerr.New(nerr.Corruption, "vector.InspectTombstones", "live node count does not match metadata")
	}
	return st, nil
}

// ShouldRebuildTombstones applies the default blocking-rebuild policy.
func ShouldRebuildTombstones(st TombstoneStats) bool {
	return st.Total != 0 && st.Deleted >= RebuildMinTombstones &&
		st.Deleted*100 >= st.Total*RebuildTombstonePercent
}

// RangeNodes visits every in-memory vertex.
func (m *Mem) RangeNodes(fn func(pk []byte, n Node) error) error {
	if m == nil || fn == nil {
		return nerr.New(nerr.InvalidArgument, "vector.Mem.RangeNodes", "nil graph or callback")
	}
	for k, n := range m.Nodes {
		if err := fn([]byte(k), n); err != nil {
			return err
		}
	}
	return nil
}

// Persist writes src meta and nodes to dst. Vector payloads stay in the vector store.
func Persist(dst Graph, src *Mem) error {
	if dst == nil || src == nil {
		return nerr.New(nerr.InvalidArgument, "vector.Persist", "nil graph")
	}
	if err := dst.SaveMeta(src.Meta); err != nil {
		return err
	}
	for k, n := range src.Nodes {
		if err := dst.SaveNode([]byte(k), n); err != nil {
			return err
		}
	}
	return nil
}

// LoadMem copies a durable graph into memory for search and further inserts.
func LoadMem(g Graph) (*Mem, error) {
	if g == nil {
		return nil, nerr.New(nerr.InvalidArgument, "vector.LoadMem", "nil graph")
	}
	meta, err := g.LoadMeta()
	if err != nil {
		return nil, err
	}
	ranger, ok := g.(NodeRanger)
	if !ok {
		return nil, nerr.New(nerr.InvalidArgument, "vector.LoadMem", "graph does not support node range")
	}
	mem := NewMem(meta.Dim, meta.Metric)
	mem.Meta = meta
	if mem.Nodes == nil {
		mem.Nodes = make(map[string]Node)
	}
	if err := ranger.RangeNodes(func(pk []byte, n Node) error {
		mem.Nodes[string(pk)] = cloneNode(n)
		vec, err := g.LoadVec(pk)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil
			}
			return err
		}
		mem.PutVec(pk, vec)
		return nil
	}); err != nil {
		return nil, err
	}
	return mem, nil
}

// Insert adds pk to the graph. vec must already be loadable via LoadVec.
func Insert(g Graph, pk []byte, vec []float32) error {
	if len(pk) == 0 {
		return nerr.New(nerr.InvalidArgument, "vector.Insert", "empty primary key")
	}
	meta, err := g.LoadMeta()
	if err != nil {
		return err
	}
	if err := Check(vec, int(meta.Dim)); err != nil {
		return err
	}
	if _, ok, err := g.LoadNode(pk); err != nil {
		return err
	} else if ok {
		if err := Delete(g, pk); err != nil {
			return err
		}
		meta, err = g.LoadMeta()
		if err != nil {
			return err
		}
	}
	level := assignLevel(pk, meta.M)
	node := Node{Level: uint8(level), Neighbors: make([][][]byte, level+1)}
	if meta.Count == 0 || len(meta.Entry) == 0 {
		if err := g.SaveNode(pk, node); err != nil {
			return err
		}
		meta.Entry = append([]byte(nil), pk...)
		meta.MaxLevel = uint8(level)
		meta.Count = 1
		return g.SaveMeta(meta)
	}
	ep := append([]byte(nil), meta.Entry...)
	for lc := int(meta.MaxLevel); lc > level; lc-- {
		hits, err := searchLayer(g, meta, vec, [][]byte{ep}, 1, lc)
		if err != nil {
			return err
		}
		if len(hits) == 0 {
			break
		}
		ep = hits[0].PK
	}
	ef := int(meta.EfConstruct)
	if ef < 1 {
		ef = 1
	}
	top := int(level)
	if int(meta.MaxLevel) < top {
		top = int(meta.MaxLevel)
	}
	for lc := top; lc >= 0; lc-- {
		W, err := searchLayer(g, meta, vec, [][]byte{ep}, ef, lc)
		if err != nil {
			return err
		}
		mmax := int(meta.M)
		if lc == 0 {
			mmax = 2 * int(meta.M)
		}
		sel := selectNeighbors(W, int(meta.M))
		node.Neighbors[lc] = copyPKs(sel)
		for _, nb := range sel {
			if err := link(g, meta, nb, pk, lc, mmax); err != nil {
				return err
			}
		}
		if len(W) > 0 {
			ep = W[0].PK
		}
	}
	if err := g.SaveNode(pk, node); err != nil {
		return err
	}
	meta.Count++
	if uint8(level) > meta.MaxLevel {
		meta.MaxLevel = uint8(level)
		meta.Entry = append([]byte(nil), pk...)
	}
	return g.SaveMeta(meta)
}

// Delete tombstones pk. The vector payload is left to the caller.
func Delete(g Graph, pk []byte) error {
	meta, err := g.LoadMeta()
	if err != nil {
		return err
	}
	n, ok, err := g.LoadNode(pk)
	if err != nil || !ok {
		return err
	}
	if n.Deleted {
		return nil
	}
	n.Deleted = true
	if err := g.SaveNode(pk, n); err != nil {
		return err
	}
	if meta.Count > 0 {
		meta.Count--
	}
	if bytes.Equal(meta.Entry, pk) {
		next, err := replacementEntry(g, meta, n)
		if err != nil {
			return err
		}
		meta.Entry = next
		if len(next) == 0 {
			meta.MaxLevel = 0
		}
	}
	return g.SaveMeta(meta)
}

func replacementEntry(g Graph, meta Meta, n Node) ([]byte, error) {
	for i := len(n.Neighbors) - 1; i >= 0; i-- {
		for _, pk := range n.Neighbors[i] {
			nb, ok, err := g.LoadNode(pk)
			if err != nil {
				return nil, err
			}
			if ok && !nb.Deleted {
				return append([]byte(nil), pk...), nil
			}
		}
	}
	return nil, nil
}

// Search returns the k closest live vertices. ef is never reduced below k.
func Search(g Graph, query []float32, k, ef int) ([]Hit, error) {
	meta, err := g.LoadMeta()
	if err != nil {
		return nil, err
	}
	if err := Check(query, int(meta.Dim)); err != nil {
		return nil, err
	}
	if k < 1 || meta.Count == 0 || len(meta.Entry) == 0 {
		return nil, nil
	}
	if ef < k {
		ef = k
	}
	ep := append([]byte(nil), meta.Entry...)
	for lc := int(meta.MaxLevel); lc > 0; lc-- {
		hits, err := searchLayer(g, meta, query, [][]byte{ep}, 1, lc)
		if err != nil {
			return nil, err
		}
		if len(hits) == 0 {
			break
		}
		ep = hits[0].PK
	}
	hits, err := searchLayer(g, meta, query, [][]byte{ep}, ef, 0)
	if err != nil {
		return nil, err
	}
	if len(hits) > k {
		hits = hits[:k]
	}
	return hits, nil
}

type hnswItem struct {
	pk   string
	dist float64
}

// minCand is closest-first (HNSW exploration queue).
type minCand []hnswItem

func (h minCand) Len() int { return len(h) }
func (h minCand) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist < h[j].dist
	}
	return h[i].pk < h[j].pk
}
func (h minCand) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *minCand) Push(x any)   { *h = append(*h, x.(hnswItem)) }
func (h *minCand) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

// maxW is furthest-first among the ef-best (HNSW result set).
type maxW []hnswItem

func (h maxW) Len() int { return len(h) }
func (h maxW) Less(i, j int) bool {
	if h[i].dist != h[j].dist {
		return h[i].dist > h[j].dist
	}
	return h[i].pk > h[j].pk
}
func (h maxW) Swap(i, j int) { h[i], h[j] = h[j], h[i] }
func (h *maxW) Push(x any)   { *h = append(*h, x.(hnswItem)) }
func (h *maxW) Pop() any {
	old := *h
	n := len(old)
	it := old[n-1]
	*h = old[:n-1]
	return it
}

func searchLayer(g Graph, meta Meta, q []float32, eps [][]byte, ef, layer int) ([]Hit, error) {
	if ef < 1 {
		ef = 1
	}
	visited := make(map[string]struct{}, ef*2)
	nodes := make(map[string]Node, ef*2)
	cand := minCand{}
	w := maxW{}
	heap.Init(&cand)
	heap.Init(&w)

	loadNode := func(pk []byte) (Node, bool, error) {
		id := string(pk)
		if n, ok := nodes[id]; ok {
			return n, true, nil
		}
		n, ok, err := g.LoadNode(pk)
		if err != nil || !ok {
			return Node{}, ok, err
		}
		nodes[id] = n
		return n, true, nil
	}
	consider := func(pk []byte) error {
		id := string(pk)
		if _, ok := visited[id]; ok {
			return nil
		}
		n, ok, err := loadNode(pk)
		if err != nil {
			return err
		}
		if !ok || n.Deleted {
			visited[id] = struct{}{}
			return nil
		}
		vec, err := g.LoadVec(pk)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				visited[id] = struct{}{}
				return nil
			}
			return err
		}
		visited[id] = struct{}{}
		d := Distance(meta.Metric, q, vec)
		if w.Len() >= ef && d >= w[0].dist {
			return nil
		}
		it := hnswItem{pk: id, dist: d}
		heap.Push(&cand, it)
		heap.Push(&w, it)
		if w.Len() > ef {
			heap.Pop(&w)
		}
		return nil
	}
	for _, pk := range eps {
		if err := consider(pk); err != nil {
			return nil, err
		}
	}
	if cand.Len() == 0 {
		return nil, nil
	}
	for cand.Len() > 0 {
		c := heap.Pop(&cand).(hnswItem)
		if w.Len() > 0 && c.dist > w[0].dist {
			break
		}
		n, ok, err := loadNode([]byte(c.pk))
		if err != nil {
			return nil, err
		}
		if !ok || layer >= len(n.Neighbors) {
			continue
		}
		for _, nb := range n.Neighbors[layer] {
			if err := consider(nb); err != nil {
				return nil, err
			}
		}
	}
	out := make([]Hit, w.Len())
	for i := len(out) - 1; i >= 0; i-- {
		it := heap.Pop(&w).(hnswItem)
		out[i] = Hit{PK: []byte(it.pk), Dist: it.dist}
	}
	return out, nil
}

func selectNeighbors(W []Hit, m int) [][]byte {
	if m < 1 {
		m = 1
	}
	if len(W) < m {
		m = len(W)
	}
	out := make([][]byte, m)
	for i := 0; i < m; i++ {
		out[i] = append([]byte(nil), W[i].PK...)
	}
	return out
}

func link(g Graph, meta Meta, src, dst []byte, layer, mmax int) error {
	n, ok, err := g.LoadNode(src)
	if err != nil || !ok || n.Deleted {
		return err
	}
	for len(n.Neighbors) <= layer {
		n.Neighbors = append(n.Neighbors, nil)
		n.Level = uint8(len(n.Neighbors) - 1)
	}
	for _, existing := range n.Neighbors[layer] {
		if bytes.Equal(existing, dst) {
			return nil
		}
	}
	n.Neighbors[layer] = append(n.Neighbors[layer], append([]byte(nil), dst...))
	if len(n.Neighbors[layer]) > mmax {
		srcVec, err := g.LoadVec(src)
		if err != nil {
			return err
		}
		type nd struct {
			pk   []byte
			dist float64
		}
		var all []nd
		for _, pk := range n.Neighbors[layer] {
			v, err := g.LoadVec(pk)
			if err != nil {
				if nerr.HasCode(err, nerr.NotFound) {
					continue
				}
				return err
			}
			all = append(all, nd{pk: pk, dist: Distance(meta.Metric, srcVec, v)})
		}
		sort.Slice(all, func(i, j int) bool {
			if all[i].dist != all[j].dist {
				return all[i].dist < all[j].dist
			}
			return bytes.Compare(all[i].pk, all[j].pk) < 0
		})
		if len(all) > mmax {
			all = all[:mmax]
		}
		n.Neighbors[layer] = n.Neighbors[layer][:0]
		for _, it := range all {
			n.Neighbors[layer] = append(n.Neighbors[layer], it.pk)
		}
	}
	return g.SaveNode(src, n)
}

func assignLevel(pk []byte, m uint8) int {
	if m < 2 {
		m = 2
	}
	h := fnv.New64a()
	_, _ = h.Write(pk)
	u := float64(h.Sum64()%1_000_000) + 0.5
	u /= 1_000_000
	ml := 1 / math.Log(float64(m))
	lvl := int(math.Floor(-math.Log(u) * ml))
	if lvl < 0 {
		lvl = 0
	}
	if lvl > maxLvl {
		lvl = maxLvl
	}
	return lvl
}

func copyPKs(in [][]byte) [][]byte {
	out := make([][]byte, len(in))
	for i, pk := range in {
		out[i] = append([]byte(nil), pk...)
	}
	return out
}

// RecallAt is |approx ∩ truth| / k for the first k hits of each list.
func RecallAt(truth, approx []Hit, k int) float64 {
	if k <= 0 {
		return 0
	}
	if len(truth) < k {
		k = len(truth)
	}
	if k == 0 {
		return 0
	}
	want := make(map[string]struct{}, k)
	for i := 0; i < k && i < len(truth); i++ {
		want[string(truth[i].PK)] = struct{}{}
	}
	var hit int
	limit := k
	if len(approx) < limit {
		limit = len(approx)
	}
	for i := 0; i < limit; i++ {
		if _, ok := want[string(approx[i].PK)]; ok {
			hit++
		}
	}
	return float64(hit) / float64(k)
}
