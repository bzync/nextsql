package executor

import (
	"bytes"
	"sort"

	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

const rrfK = 60

type hybridHit struct {
	row     []types.Value
	pk      []byte
	bm25    float64
	dist    float64
	hasVec  bool
	hasText bool
}

func (s *Session) execCandidates(n planner.Candidates) ([][]types.Value, error) {
	var (
		rows [][]types.Value
		err  error
	)
	switch n.Kind {
	case "hnsw", "flat":
		nr := planner.Nearest{
			Input:     n.Input,
			Table:     n.Table,
			IndexName: n.IndexName,
			Column:    n.Column,
			Query:     n.Query,
			Metric:    n.Metric,
			Residual:  n.Residual,
			Needed:    n.Needed,
			K:         n.K,
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
			Column:    n.Column,
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

	q, err := s.searchQuery(planner.Search{Table: tab, Query: n.SearchQuery})
	if err != nil {
		return nil, err
	}
	var qvec []float32
	if n.NearestQuery != nil {
		qvec, err = s.nearestQuery(planner.Nearest{Table: tab, Column: n.NearestCol, Query: n.NearestQuery})
		if err != nil {
			return nil, err
		}
	}
	metric, err := nsvec.ParseMetric(n.Metric)
	if err != nil {
		return nil, err
	}

	type analyzed struct {
		tf  map[string]uint32
		dl  uint32
		hit bool
	}
	ana := make([]analyzed, len(rows))
	df := make(map[string]uint64, len(q.Terms))
	var docs, tokens uint64
	for i, row := range rows {
		if n.SearchCol < 0 || n.SearchCol >= len(row) || row[n.SearchCol].Null {
			continue
		}
		doc, err := fulltext.Analyze(row[n.SearchCol].Str)
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
		ok := len(q.Terms) > 0
		for _, term := range q.Terms {
			if tf[term] == 0 {
				ok = false
				break
			}
		}
		if ok {
			for _, ph := range q.Phrases {
				if !fulltext.PhraseMatch(ph, pos) {
					ok = false
					break
				}
			}
		}
		if ok {
			docs++
			tokens += uint64(doc.Len)
			for _, term := range q.Terms {
				df[term]++
			}
		}
		ana[i] = analyzed{tf: tf, dl: doc.Len, hit: ok}
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
			var score float64
			for _, term := range q.Terms {
				score += fulltext.Score(ana[i].tf[term], ana[i].dl, avg, fulltext.IDF(docs, df[term]))
			}
			it.bm25 = score
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
		items = append(items, it)
	}
	if len(q.Terms) > 0 {
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

	bm25Ord := orderHits(items, func(a, b hybridHit) bool {
		if a.hasText != b.hasText {
			return a.hasText
		}
		if a.bm25 != b.bm25 {
			return a.bm25 > b.bm25
		}
		return bytes.Compare(a.pk, b.pk) < 0
	})
	vecOrd := orderHits(items, func(a, b hybridHit) bool {
		if a.hasVec != b.hasVec {
			return a.hasVec
		}
		if a.dist != b.dist {
			return a.dist < b.dist
		}
		return bytes.Compare(a.pk, b.pk) < 0
	})

	type fused struct {
		row   []types.Value
		pk    []byte
		score float64
	}
	fusedRows := make([]fused, len(items))
	for i, it := range items {
		score := 1.0/float64(rrfK+bm25Ord[i]) + 1.0/float64(rrfK+vecOrd[i])
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
