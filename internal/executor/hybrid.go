package executor

import (
	"bytes"
	"sort"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

const rrfK = 60

type hybridHit struct {
	row        []types.Value
	pk         []byte
	bm25       float64
	dist       float64
	sparseDist float64
	hasVec     bool
	hasText    bool
	hasSparse  bool
}

func (s *Session) execCandidates(n planner.Candidates) ([][]types.Value, error) {
	var (
		rows [][]types.Value
		err  error
	)
	switch n.Kind {
	case "hnsw", "flat", "sparse":
		nr := planner.Nearest{
			Input:      n.Input,
			Table:      n.Table,
			IndexName:  n.IndexName,
			Column:     n.Column,
			Query:      n.Query,
			Metric:     n.Metric,
			Residual:   n.Residual,
			Needed:     n.Needed,
			K:          n.K,
			Partitions: n.Partitions,
		}
		if n.Kind == "flat" {
			nr.IndexName = ""
		}
		rows, err = s.searchNearest(nr)
	case "fulltext":
		rows, err = s.searchFulltext(planner.Search{
			Input:     n.Input,
			Table:     n.Table,
			IndexName: n.IndexName,
			Columns:   []int{n.Column},
			Query:     n.Query,
			Residual:  n.Residual,
			Needed:    n.Needed,
		})
	default:
		return nil, nerr.New(nerr.Internal, "executor.execCandidates", "unknown candidate kind")
	}
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, "Candidates"); node != nil {
			node.ActRows = int64(len(rows))
			node.Workers = s.workers()
		}
	}
	return rows, nil
}

func (s *Session) execRerank(n planner.Rerank) ([][]types.Value, error) {
	rows, err := s.collectPlan(n.Input)
	if err != nil {
		return nil, err
	}
	tab := n.Table
	if tab == nil {
		tab = tableOf(n.Input)
	}
	for _, extra := range n.Extra {
		more, err := s.collectPlan(extra)
		if err != nil {
			return nil, err
		}
		rows, err = unionHybridRows(tab, rows, more)
		if err != nil {
			return nil, err
		}
	}
	if err := s.hydrateRows(tab, rows); err != nil {
		return nil, err
	}
	if s.trace != nil {
		defer func() {
			if node := optimizer.Find(s.trace, "Rerank"); node != nil {
				node.ActRows = int64(len(rows))
				node.Workers = 1
			}
		}()
	}
	if len(rows) == 0 {
		return rows, nil
	}

	var q fulltext.Query
	if n.SearchQuery != nil && len(n.SearchCols) > 0 {
		q, err = s.searchQuery(planner.Search{Table: tab, Query: n.SearchQuery, Columns: n.SearchCols})
		if err != nil {
			return nil, err
		}
	}
	nearestSparse := n.NearestCol >= 0 && n.NearestCol < len(tab.Columns) && tab.Columns[n.NearestCol].Type.VecElem == types.VecSparse
	var qvec []float32
	var nearestColType types.Type
	var metric nsvec.Metric
	if n.NearestQuery != nil && n.NearestCol >= 0 && !nearestSparse {
		qvec, err = s.nearestQuery(planner.Nearest{Table: tab, Column: n.NearestCol, Query: n.NearestQuery})
		if err != nil {
			return nil, err
		}
		if n.NearestCol < len(tab.Columns) {
			nearestColType = tab.Columns[n.NearestCol].Type
		}
		metric, err = nearestMetric(n.Metric, nearestColType)
		if err != nil {
			return nil, err
		}
	}
	sparseCol, sparseQuery, sparseMetricName := n.SparseCol, n.SparseQuery, n.SparseMetric
	if nearestSparse {
		sparseCol, sparseQuery, sparseMetricName = n.NearestCol, n.NearestQuery, n.Metric
	}
	var qsparse nsvec.SparseVec
	var sparseMetric nsvec.Metric
	if sparseQuery != nil && sparseCol >= 0 {
		qsparse, err = s.nearestSparseQuery(planner.Nearest{Table: tab, Column: sparseCol, Query: sparseQuery})
		if err != nil {
			return nil, err
		}
		var sparseType types.Type
		if sparseCol < len(tab.Columns) {
			sparseType = tab.Columns[sparseCol].Type
		}
		sparseMetric, err = nearestMetric(sparseMetricName, sparseType)
		if err != nil {
			return nil, err
		}
	}

	type analyzed struct {
		tf  map[string]uint32
		pos map[string][]uint32
		dl  uint32
		hit bool
	}
	ana := make([]analyzed, len(rows))
	df := make(map[string]uint64, len(q.Terms))
	var docs, tokens uint64
	for i, row := range rows {
		if len(n.SearchCols) == 0 {
			continue
		}
		doc, err := analyzeSearchRow(row, n.SearchCols, fulltextAnalyzer(tab, "", n.SearchCols))
		if err != nil {
			return nil, err
		}
		if doc.Len == 0 {
			continue
		}
		tf := make(map[string]uint32, len(doc.Terms))
		pos := make(map[string][]uint32, len(doc.Terms))
		for _, t := range doc.Terms {
			tf[t.Term] = t.TF
			pos[t.Term] = t.Pos
		}
		ok := fulltext.QueryMatches(q, tf, pos)
		if ok {
			docs++
			tokens += uint64(doc.Len)
			for _, term := range q.Terms {
				if tf[term] > 0 {
					df[term]++
				}
			}
		}
		ana[i] = analyzed{tf: tf, pos: pos, dl: doc.Len, hit: ok}
	}
	avg := fulltext.AvgDL(fulltext.Stats{Docs: docs, Tokens: tokens})

	items := make([]hybridHit, 0, len(rows))
	for i, row := range rows {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		it := hybridHit{row: row}
		if pk, err := types.EncodeKey(tab.PKValues(row)); err == nil {
			it.pk = pk
		}
		if ana[i].hit {
			it.bm25 = fulltext.QueryScoreWeighted(q, ana[i].tf, ana[i].pos, n.SearchWeights, df, ana[i].dl, avg, docs)
			it.hasText = true
		}
		if qvec != nil && n.NearestCol >= 0 && n.NearestCol < len(row) && !row[n.NearestCol].Null {
			vec := row[n.NearestCol].Vec
			if err := nsvec.Check(vec, len(qvec)); err != nil {
				return nil, err
			}
			it.dist = nsvec.Distance(metric, qvec, vec)
			it.hasVec = true
		}
		if qsparse.Dim > 0 && sparseCol >= 0 && sparseCol < len(row) && !row[sparseCol].Null {
			sv, err := valueSparse(row[sparseCol], qsparse.Dim)
			if err != nil {
				return nil, err
			}
			it.sparseDist = nsvec.SparseDistance(sparseMetric, qsparse, sv)
			it.hasSparse = true
		}
		items = append(items, it)
	}
	if n.Strategy != "fusion" && len(q.Terms) > 0 {
		kept := items[:0]
		for _, it := range items {
			if it.hasText {
				kept = append(kept, it)
			}
		}
		items = kept
	}
	if len(items) == 0 {
		rows = nil
		return rows, nil
	}

	wantText := n.SearchQuery != nil && len(n.SearchCols) > 0
	wantVec := qvec != nil
	wantSparse := qsparse.Dim > 0
	var bm25Ord, vecOrd, sparseOrd []int
	if wantText {
		bm25Ord = orderHits(items, func(a, b hybridHit) bool {
			if a.hasText != b.hasText {
				return a.hasText
			}
			if a.bm25 != b.bm25 {
				return a.bm25 > b.bm25
			}
			return bytes.Compare(a.pk, b.pk) < 0
		})
	}
	if wantVec {
		vecOrd = orderHits(items, func(a, b hybridHit) bool {
			if a.hasVec != b.hasVec {
				return a.hasVec
			}
			if a.dist != b.dist {
				return a.dist < b.dist
			}
			return bytes.Compare(a.pk, b.pk) < 0
		})
	}
	if wantSparse {
		sparseOrd = orderHits(items, func(a, b hybridHit) bool {
			if a.hasSparse != b.hasSparse {
				return a.hasSparse
			}
			if a.sparseDist != b.sparseDist {
				return a.sparseDist < b.sparseDist
			}
			return bytes.Compare(a.pk, b.pk) < 0
		})
	}

	type fused struct {
		row   []types.Value
		pk    []byte
		score float64
	}
	fusedRows := make([]fused, len(items))
	for i, it := range items {
		var score float64
		if wantText && it.hasText {
			score += 1.0 / float64(rrfK+bm25Ord[i])
		}
		if wantVec && it.hasVec {
			score += 1.0 / float64(rrfK+vecOrd[i])
		}
		if wantSparse && it.hasSparse {
			score += 1.0 / float64(rrfK+sparseOrd[i])
		}
		fusedRows[i] = fused{row: it.row, pk: it.pk, score: score}
	}
	sort.SliceStable(fusedRows, func(i, j int) bool {
		if fusedRows[i].score != fusedRows[j].score {
			return fusedRows[i].score > fusedRows[j].score
		}
		return bytes.Compare(fusedRows[i].pk, fusedRows[j].pk) < 0
	})
	k := int(n.K)
	if k <= 0 || k > len(fusedRows) {
		k = len(fusedRows)
	}
	rows = make([][]types.Value, k)
	for i := 0; i < k; i++ {
		rows[i] = fusedRows[i].row
	}
	return rows, nil
}

func unionHybridRows(tab *catalog.Table, a, b [][]types.Value) ([][]types.Value, error) {
	if len(b) == 0 {
		return a, nil
	}
	seen := make(map[string]struct{}, len(a)+len(b))
	for _, row := range a {
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return nil, err
		}
		seen[string(pk)] = struct{}{}
	}
	out := a
	for _, row := range b {
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return nil, err
		}
		key := string(pk)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, row)
	}
	return out, nil
}

func orderHits(items []hybridHit, less func(a, b hybridHit) bool) []int {
	idx := make([]int, len(items))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return less(items[idx[i]], items[idx[j]]) })
	rank := make([]int, len(items))
	for r, i := range idx {
		rank[i] = r + 1
	}
	return rank
}
