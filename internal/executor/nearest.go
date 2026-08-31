package executor

import (
	"sort"

	"github.com/bzync/nextsql/internal/bitvec"
	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

// nearestMetric resolves the distance metric for a NEAREST / hybrid query. An
// explicit USING clause wins; otherwise a BITVECTOR column defaults to HAMMING
// and every other vector column to COSINE.
func nearestMetric(explicit string, colType types.Type) (nsvec.Metric, error) {
	if explicit == "" && colType.Kind == types.KindVector && colType.VecElem == types.VecBit {
		return nsvec.MetricHamming, nil
	}
	return nsvec.ParseMetric(explicit)
}

func (s *Session) searchNearest(n planner.Nearest) ([][]types.Value, error) {
	var colType types.Type
	if n.Table != nil && n.Column >= 0 && n.Column < len(n.Table.Columns) {
		colType = n.Table.Columns[n.Column].Type
	}
	if colType.VecElem == types.VecSparse {
		return s.searchNearestSparse(n, colType)
	}
	q, err := s.nearestQuery(n)
	if err != nil {
		return nil, err
	}
	if q == nil {
		return nil, nil
	}
	metric, err := nearestMetric(n.Metric, colType)
	if err != nil {
		return nil, err
	}
	var rows [][]types.Value
	if n.IndexName != "" {
		rows, err = s.nearestIndex(n, q, metric)
	} else {
		rows, err = s.nearestFlat(n, q, metric)
	}
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, "Nearest"); node != nil {
			node.ActRows = int64(len(rows))
			node.Workers = s.workers()
		}
	}
	return rows, nil
}

func (s *Session) searchNearestSparse(n planner.Nearest, colType types.Type) ([][]types.Value, error) {
	q, err := s.nearestSparseQuery(n)
	if err != nil {
		return nil, err
	}
	if q.Dim == 0 {
		return nil, nil
	}
	metric, err := nearestMetric(n.Metric, colType)
	if err != nil {
		return nil, err
	}
	if metric != nsvec.MetricCosine && metric != nsvec.MetricIP {
		return nil, nerr.New(nerr.InvalidArgument, "executor.searchNearestSparse", "SPARSEVECTOR NEAREST requires COSINE or INNER_PRODUCT")
	}
	var rows [][]types.Value
	if n.IndexName != "" {
		idx := indexByName(n.Table, n.IndexName)
		if !idx.Vector {
			return nil, nerr.New(nerr.NotFound, "executor.searchNearestSparse", "unknown vector index")
		}
		rows, err = s.nearestSparseIndex(n, q, metric, n.Table, idx)
	} else {
		rows, err = s.nearestSparseFlat(n, q, metric)
	}
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, "Nearest"); node != nil {
			node.ActRows = int64(len(rows))
			node.Workers = s.workers()
		}
	}
	return rows, nil
}

func (s *Session) nearestQuery(n planner.Nearest) ([]float32, error) {
	v, err := s.eval(n.Query, n.Table, nil)
	if err != nil {
		return nil, err
	}
	if v.Null {
		return nil, nil
	}
	tab := n.Table
	want := 0
	bit := false
	if tab != nil && n.Column >= 0 && n.Column < len(tab.Columns) {
		want = int(tab.Columns[n.Column].Type.Precision)
		bit = tab.Columns[n.Column].Type.VecElem == types.VecBit
	}
	if v.Typ.Kind != types.KindVector {
		return nil, nerr.New(nerr.InvalidArgument, "executor.nearestQuery", "NEAREST query must be a VECTOR")
	}
	if bit {
		if err := bitvec.Validate(v.Vec); err != nil {
			return nil, err
		}
	}
	if err := nsvec.Check(v.Vec, want); err != nil {
		return nil, err
	}
	return v.Vec, nil
}

func (s *Session) nearestIndex(n planner.Nearest, q []float32, metric nsvec.Metric) ([][]types.Value, error) {
	tab := n.Table
	idx := indexByName(tab, n.IndexName)
	if !idx.Vector {
		return nil, nerr.New(nerr.NotFound, "executor.nearestIndex", "unknown vector index")
	}
	if idx.VecMethod == catalog.VecMethodIVF {
		return s.nearestIVFIndex(n, q, metric, tab, idx)
	}
	if idx.VecMethod == catalog.VecMethodIVFPQ {
		return s.nearestIVFPQIndex(n, q, metric, tab, idx)
	}
	if idx.VecMethod == catalog.VecMethodSPARSE {
		sv, err := valueSparse(types.VectorValue(q, types.Type{Kind: types.KindVector, Precision: uint16(len(q)), VecElem: types.VecF32}), uint32(len(q)))
		if err != nil {
			return nil, err
		}
		return s.nearestSparseIndex(n, sv, metric, tab, idx)
	}
	if tab.Partitioning != nil {
		return s.nearestIndexPartitioned(n, q, metric, tab, idx)
	}
	g, err := s.hnswGraph(tab, idx)
	if err != nil {
		return nil, err
	}
	meta, err := g.LoadMeta()
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	if meta.Metric != metric && n.Metric != "" && meta.Metric != 0 {
		// Query asked for a different metric than the graph; exact flat.
		return s.nearestFlat(n, q, metric)
	}
	k := int(n.K)
	if k < 1 {
		k = int(meta.Count)
	}
	if k < 1 {
		return nil, nil
	}
	ef := int(meta.EfConstruct)
	if n.Residual != nil && k < int(meta.Count) {
		over := k * 4
		if over < k {
			over = k
		}
		if uint64(over) > meta.Count {
			over = int(meta.Count)
		}
		k = over
	}
	if ef < k {
		ef = k
	}
	hits, err := nsvec.Search(g, q, k, ef)
	if err != nil {
		return nil, err
	}
	var htx *btree.Txn
	var out [][]types.Value
	for _, h := range hits {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		row, covered, err := coveringNearestRow(g, tab, n.Needed, n.Column, h.PK)
		if err != nil {
			return nil, err
		}
		if !covered {
			if htx == nil {
				heap, err := s.heapOf(tab)
				if err != nil {
					return nil, err
				}
				htx = s.x.use(heap)
			}
			row, err = s.fetchPKRow(htx, tab, h.PK)
			if err != nil || row == nil {
				if err != nil {
					return nil, err
				}
				continue
			}
		}
		ok, err := s.match(n.Residual, tab, row)
		if err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, row)
		if n.K > 0 && int64(len(out)) >= n.K {
			break
		}
	}
	return out, nil
}

// nearestIndexPartitioned searches every surviving partition-local HNSW graph and
// merges the results by distance, so partitioning never changes recall or ranking
// among the partitions that a predicate cannot rule out. When the residual
// predicate constrains the partition key, `n.Partitions` carries the pruned
// stable-ID set and only those partition graphs are opened and searched.
func (s *Session) nearestIndexPartitioned(n planner.Nearest, q []float32, metric nsvec.Metric, tab *catalog.Table, idx catalog.Index) ([][]types.Value, error) {
	type pgraph struct {
		part catalog.Partition
		g    nsvec.Graph
	}
	var graphs []pgraph
	var total uint64
	var ef int
	for _, part := range partitionSelection(tab, n.Partitions) {
		g, err := s.hnswGraphPartition(tab, part, idx)
		if err != nil {
			return nil, err
		}
		meta, err := g.LoadMeta()
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				continue
			}
			return nil, err
		}
		if meta.Metric != metric && n.Metric != "" && meta.Metric != 0 {
			// A different metric than the graph was built for: exact flat.
			return s.nearestFlat(n, q, metric)
		}
		total += meta.Count
		if int(meta.EfConstruct) > ef {
			ef = int(meta.EfConstruct)
		}
		graphs = append(graphs, pgraph{part: part, g: g})
	}
	if len(graphs) == 0 || total == 0 {
		return nil, nil
	}
	k := int(n.K)
	if k < 1 {
		k = int(total)
	}
	if k < 1 {
		return nil, nil
	}
	if n.Residual != nil && uint64(k) < total {
		over := k * 4
		if over < k {
			over = k
		}
		if uint64(over) > total {
			over = int(total)
		}
		k = over
	}
	if ef < k {
		ef = k
	}
	type mergedHit struct {
		hit nsvec.Hit
		gi  int
	}
	var all []mergedHit
	for gi := range graphs {
		hits, err := nsvec.Search(graphs[gi].g, q, k, ef)
		if err != nil {
			return nil, err
		}
		for _, h := range hits {
			all = append(all, mergedHit{hit: h, gi: gi})
		}
	}
	sort.Slice(all, func(i, j int) bool { return nsvec.LessHit(all[i].hit, all[j].hit) })
	htxs := make([]*btree.Txn, len(graphs))
	var out [][]types.Value
	for _, m := range all {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		pg := graphs[m.gi]
		row, covered, err := coveringNearestRow(pg.g, tab, n.Needed, n.Column, m.hit.PK)
		if err != nil {
			return nil, err
		}
		if !covered {
			if htxs[m.gi] == nil {
				heap, err := s.partitionHeap(tab, pg.part.ID)
				if err != nil {
					return nil, err
				}
				htxs[m.gi] = s.x.use(heap)
			}
			row, err = s.fetchPKRow(htxs[m.gi], tab, m.hit.PK)
			if err != nil {
				return nil, err
			}
			if row == nil {
				continue
			}
		}
		ok, err := s.match(n.Residual, tab, row)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		out = append(out, row)
		if n.K > 0 && int64(len(out)) >= n.K {
			break
		}
	}
	return out, nil
}

// coveringNearestRow rebuilds a hit from the HNSW primary key and vector store
// when the output only needs those columns. `SELECT id … NEAREST` is this shape.
func coveringNearestRow(g nsvec.Graph, tab *catalog.Table, needed []int, vecCol int, pk []byte) ([]types.Value, bool, error) {
	if g == nil || tab == nil || len(needed) == 0 || len(tab.PK) == 0 {
		return nil, false, nil
	}
	allow := make(map[int]struct{}, len(tab.PK)+1)
	for _, ord := range tab.PK {
		allow[ord] = struct{}{}
	}
	if vecCol >= 0 && vecCol < len(tab.Columns) {
		allow[vecCol] = struct{}{}
	}
	needVec := false
	for _, ord := range needed {
		if _, ok := allow[ord]; !ok {
			return nil, false, nil
		}
		if ord == vecCol {
			needVec = true
		}
	}
	pkTypes := make([]types.Type, len(tab.PK))
	for i, ord := range tab.PK {
		pkTypes[i] = tab.Columns[ord].Type
	}
	vals, err := types.DecodeKey(pk, pkTypes)
	if err != nil {
		return nil, false, err
	}
	row := make([]types.Value, len(tab.Columns))
	for i, ord := range tab.PK {
		if i < len(vals) {
			row[ord] = vals[i]
		}
	}
	if needVec {
		load := g.LoadVec
		if fl, ok := g.(nsvec.FullVecLoader); ok {
			load = fl.LoadVecFull
		}
		vec, err := load(pk)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nil, false, nil
			}
			return nil, false, err
		}
		row[vecCol] = types.VectorValue(vec, tab.Columns[vecCol].Type)
	}
	return row, true, nil
}

func (s *Session) nearestFlat(n planner.Nearest, q []float32, metric nsvec.Metric) ([][]types.Value, error) {
	tab := n.Table
	if tab == nil || n.Column < 0 || n.Column >= len(tab.Columns) {
		return nil, nerr.New(nerr.Internal, "executor.nearestFlat", "missing NEAREST column")
	}
	var (
		cands []nsvec.Candidate
		rows  [][]types.Value
	)
	if n.Input != nil {
		got, err := s.collectPlan(n.Input)
		if err != nil {
			return nil, err
		}
		for _, row := range got {
			v := row[n.Column]
			if v.Null {
				continue
			}
			if err := nsvec.Check(v.Vec, len(q)); err != nil {
				return nil, err
			}
			pk, err := types.EncodeKey(tab.PKValues(row))
			if err != nil {
				return nil, err
			}
			cands = append(cands, nsvec.Candidate{PK: pk, Vec: v.Vec})
			rows = append(rows, row)
		}
	} else {
		start, end := nsvec.PayloadBounds(uint16(n.Column))
		scan := func(vs *btree.Tree) error {
			return s.x.use(vs).Range(start, end, func(key, val []byte) error {
				if err := s.budget().Check(); err != nil {
					return err
				}
				_, pk, err := nsvec.SplitPayloadKey(key)
				if err != nil {
					return err
				}
				vec, err := nsvec.DecodePayload(val)
				if err != nil {
					return err
				}
				if err := nsvec.Check(vec, len(q)); err != nil {
					return err
				}
				cands = append(cands, nsvec.Candidate{PK: pk, Vec: vec})
				return nil
			})
		}
		if tab.Partitioning != nil {
			for _, part := range partitionSelection(tab, n.Partitions) {
				vs, err := s.partitionVec(tab, part.ID)
				if err != nil {
					return nil, err
				}
				if err := scan(vs); err != nil {
					return nil, err
				}
			}
		} else {
			if tab.VecMeta == 0 {
				return nil, nil
			}
			vs, err := s.vecOf(tab)
			if err != nil {
				return nil, err
			}
			if err := scan(vs); err != nil {
				return nil, err
			}
		}
	}
	k := int(n.K)
	if k < 1 {
		k = len(cands)
	}
	hits, err := nsvec.FlatSearch(q, metric, cands, k, s.workers())
	if err != nil {
		return nil, err
	}
	if n.Input != nil {
		byPK := make(map[string][]types.Value, len(rows))
		for i, row := range rows {
			byPK[string(cands[i].PK)] = row
		}
		out := make([][]types.Value, 0, len(hits))
		for _, h := range hits {
			row := byPK[string(h.PK)]
			if row == nil {
				continue
			}
			out = append(out, row)
		}
		return out, nil
	}
	var htxs []*btree.Txn
	if tab.Partitioning != nil {
		for _, part := range partitionSelection(tab, n.Partitions) {
			heap, err := s.partitionHeap(tab, part.ID)
			if err != nil {
				return nil, err
			}
			htxs = append(htxs, s.x.use(heap))
		}
	} else {
		heap, err := s.heapOf(tab)
		if err != nil {
			return nil, err
		}
		htxs = append(htxs, s.x.use(heap))
	}
	var out [][]types.Value
	for _, h := range hits {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		var row []types.Value
		var err error
		for _, htx := range htxs {
			row, err = s.fetchPKRow(htx, tab, h.PK)
			if err != nil {
				return nil, err
			}
			if row != nil {
				break
			}
		}
		if row == nil {
			continue
		}
		ok, err := s.match(n.Residual, tab, row)
		if err != nil || !ok {
			if err != nil {
				return nil, err
			}
			continue
		}
		out = append(out, row)
	}
	return out, nil
}
