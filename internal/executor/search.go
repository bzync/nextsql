package executor

import (
	"sort"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
)

func (s *Session) searchFulltext(n planner.Search) ([][]types.Value, error) {
	q, err := s.searchQuery(n)
	if err != nil {
		return nil, err
	}
	if len(q.Terms) == 0 {
		return nil, nil
	}
	var rows [][]types.Value
	if n.IndexName != "" {
		rows, err = s.searchIndex(n, q)
	} else {
		rows, err = s.searchScan(n, q)
	}
	if err != nil {
		return nil, err
	}
	if s.trace != nil {
		if node := optimizer.Find(s.trace, "Search"); node != nil {
			node.ActRows = int64(len(rows))
			node.Workers = 1
		}
	}
	return rows, nil
}

func (s *Session) searchQuery(n planner.Search) (fulltext.Query, error) {
	v, err := s.eval(n.Query, n.Table, nil)
	if err != nil {
		return fulltext.Query{}, err
	}
	if v.Null {
		return fulltext.Query{}, nil
	}
	if v.Typ.Kind != types.KindString && v.Typ.Kind != types.KindText {
		return fulltext.Query{}, nerr.New(nerr.InvalidArgument, "executor.searchQuery", "SEARCH query must be a string")
	}
	return fulltext.ParseQuery(v.Str)
}

func (s *Session) searchIndex(n planner.Search, q fulltext.Query) ([][]types.Value, error) {
	tab := n.Table
	idx := indexByName(tab, n.IndexName)
	ix, err := s.indexOf(tab, idx)
	if err != nil {
		return nil, err
	}
	itx := s.x.use(ix)
	raw, err := itx.Lookup(fulltext.StatsKey())
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	st, err := fulltext.DecodeStats(raw)
	if err != nil || st.Docs == 0 {
		return nil, err
	}
	type posting struct {
		tf  uint32
		pos []uint32
	}
	docs := map[string]map[string]posting{}
	df := make(map[string]uint64, len(q.Terms))
	for _, term := range q.Terms {
		start, end := fulltext.PostingBounds(term)
		err := itx.Range(start, end, func(key, val []byte) error {
			_, pk, err := fulltext.SplitPostingKey(key)
			if err != nil {
				return err
			}
			tf, pos, err := fulltext.DecodePosting(val)
			if err != nil {
				return err
			}
			id := string(pk)
			m := docs[id]
			if m == nil {
				m = make(map[string]posting, len(q.Terms))
				docs[id] = m
			}
			m[term] = posting{tf: tf, pos: pos}
			df[term]++
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	avg := fulltext.AvgDL(st)
	var hits []fulltext.Hit
	for id, terms := range docs {
		if len(terms) != len(q.Terms) {
			continue
		}
		pos := make(map[string][]uint32, len(terms))
		for t, p := range terms {
			pos[t] = p.pos
		}
		ok := true
		for _, ph := range q.Phrases {
			if !fulltext.PhraseMatch(ph, pos) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		pk := []byte(id)
		dlRaw, err := itx.Lookup(fulltext.DocLenKey(pk))
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				continue
			}
			return nil, err
		}
		dl, err := fulltext.DecodeDocLen(dlRaw)
		if err != nil {
			return nil, err
		}
		var score float64
		for _, term := range q.Terms {
			score += fulltext.Score(terms[term].tf, dl, avg, fulltext.IDF(st.Docs, df[term]))
		}
		hits = append(hits, fulltext.Hit{PK: pk, Score: score})
	}
	sort.Slice(hits, func(i, j int) bool { return fulltext.LessHit(hits[i], hits[j]) })
	heap, err := s.heapOf(tab)
	if err != nil {
		return nil, err
	}
	htx := s.x.use(heap)
	var out [][]types.Value
	for _, h := range hits {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		row, err := s.fetchPKRow(htx, tab, h.PK)
		if err != nil || row == nil {
			if err != nil {
				return nil, err
			}
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

func (s *Session) searchScan(n planner.Search, q fulltext.Query) ([][]types.Value, error) {
	rows, err := s.collectPlan(n.Input)
	if err != nil {
		return nil, err
	}
	tab := n.Table
	if tab == nil {
		tab = tableOf(n.Input)
	}
	if tab == nil || n.Column < 0 || n.Column >= len(tab.Columns) {
		return nil, nerr.New(nerr.Internal, "executor.searchScan", "missing SEARCH column")
	}
	type scored struct {
		row []types.Value
		hit fulltext.Hit
		pos map[string][]uint32
		tf  map[string]uint32
		dl  uint32
	}
	var cand []scored
	df := make(map[string]uint64, len(q.Terms))
	var docs, tokens uint64
	for _, row := range rows {
		v := row[n.Column]
		if v.Null {
			continue
		}
		doc, err := fulltext.Analyze(v.Str)
		if err != nil {
			return nil, err
		}
		if doc.Len == 0 {
			continue
		}
		docs++
		tokens += uint64(doc.Len)
		tf := make(map[string]uint32, len(doc.Terms))
		pos := make(map[string][]uint32, len(doc.Terms))
		for _, t := range doc.Terms {
			tf[t.Term] = t.TF
			pos[t.Term] = t.Pos
		}
		ok := true
		for _, term := range q.Terms {
			if tf[term] == 0 {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for _, ph := range q.Phrases {
			if !fulltext.PhraseMatch(ph, pos) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		for _, term := range q.Terms {
			df[term]++
		}
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return nil, err
		}
		cand = append(cand, scored{row: row, hit: fulltext.Hit{PK: pk}, pos: pos, tf: tf, dl: doc.Len})
	}
	avg := fulltext.AvgDL(fulltext.Stats{Docs: docs, Tokens: tokens})
	var hits []scored
	for _, c := range cand {
		var score float64
		for _, term := range q.Terms {
			score += fulltext.Score(c.tf[term], c.dl, avg, fulltext.IDF(docs, df[term]))
		}
		c.hit.Score = score
		hits = append(hits, c)
	}
	sort.Slice(hits, func(i, j int) bool { return fulltext.LessHit(hits[i].hit, hits[j].hit) })
	out := make([][]types.Value, len(hits))
	for i, h := range hits {
		out[i] = h.row
	}
	return out, nil
}

func (s *Session) fetchPKRow(htx *btree.Txn, tab *catalog.Table, pk []byte) ([]types.Value, error) {
	payload, err := htx.Lookup(pk)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	return s.decodeHeapRow(tab, payload)
}
