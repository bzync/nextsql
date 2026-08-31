package executor

import (
	"sort"
	"strings"

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
	if q.Empty() {
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
	return fulltext.ParseQueryWith(v.Str, fulltextAnalyzer(n.Table, n.IndexName, n.Columns))
}

func fulltextAnalyzer(tab *catalog.Table, indexName string, cols []int) fulltext.Analyzer {
	if tab == nil {
		return fulltext.Simple
	}
	if indexName != "" {
		idx := indexByName(tab, indexName)
		if idx.Fulltext {
			return fulltext.Analyzer{ID: idx.FTAnalyzer, Version: idx.FTVersion}
		}
	}
	var found *catalog.Index
	for i := range tab.Indexes {
		idx := &tab.Indexes[i]
		if !idx.Fulltext || !catalog.IntsEqual(idx.Columns, cols) {
			continue
		}
		if found == nil || idx.Name < found.Name {
			found = idx
		}
	}
	if found != nil {
		return fulltext.Analyzer{ID: found.FTAnalyzer, Version: found.FTVersion}
	}
	return fulltext.Simple
}

// ftSegment is one inverted-index tree paired with the heap that resolves its
// primary keys. A non-partitioned table has one segment; a partitioned table
// has one per partition-local FULLTEXT root.
type ftSegment struct {
	itx *btree.Txn
	htx *btree.Txn
}

func (s *Session) searchIndex(n planner.Search, q fulltext.Query) ([][]types.Value, error) {
	tab := n.Table
	idx := indexByName(tab, n.IndexName)
	var segs []ftSegment
	if tab.Partitioning != nil {
		for _, part := range partitionSelection(tab, nil) {
			local, err := s.partitionIndex(tab, part.ID, idx)
			if err != nil {
				return nil, err
			}
			heap, err := s.partitionHeap(tab, part.ID)
			if err != nil {
				return nil, err
			}
			segs = append(segs, ftSegment{itx: s.x.use(local), htx: s.x.use(heap)})
		}
	} else {
		ix, err := s.indexOf(tab, idx)
		if err != nil {
			return nil, err
		}
		heap, err := s.heapOf(tab)
		if err != nil {
			return nil, err
		}
		segs = append(segs, ftSegment{itx: s.x.use(ix), htx: s.x.use(heap)})
	}
	return s.searchSegments(n, q, tab, segs)
}

// searchSegments scores BM25 over one or more inverted-index segments as a
// single logical corpus: document frequency and corpus size are summed across
// segments so partitioning never changes ranking.
func (s *Session) searchSegments(n planner.Search, q fulltext.Query, tab *catalog.Table, segs []ftSegment) ([][]types.Value, error) {
	type posting struct {
		tf  uint32
		pos []uint32
	}
	type docKey struct {
		seg int
		id  string
	}
	docs := map[docKey]map[string]posting{}
	df := make(map[string]uint64, len(q.Terms)+len(q.Prefixes)+len(q.Fuzzies))
	var corpus fulltext.Stats
	addPosting := func(si int, term string, pk, val []byte) error {
		tf, pos, err := fulltext.DecodePosting(val)
		if err != nil {
			return err
		}
		dk := docKey{seg: si, id: string(pk)}
		m := docs[dk]
		if m == nil {
			m = make(map[string]posting, len(q.Terms)+len(q.Prefixes)+len(q.Fuzzies))
			docs[dk] = m
		}
		if _, exists := m[term]; !exists {
			df[term]++
		}
		m[term] = posting{tf: tf, pos: pos}
		return nil
	}
	for si := range segs {
		itx := segs[si].itx
		raw, err := itx.Lookup(fulltext.StatsKey())
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				continue
			}
			return nil, err
		}
		st, err := fulltext.DecodeStats(raw)
		if err != nil {
			return nil, err
		}
		if st.Docs == 0 {
			continue
		}
		corpus.Docs += st.Docs
		corpus.Tokens += st.Tokens
		for _, term := range q.Terms {
			start, end := fulltext.PostingBounds(term)
			err := itx.Range(start, end, func(key, val []byte) error {
				_, pk, err := fulltext.SplitPostingKey(key)
				if err != nil {
					return err
				}
				return addPosting(si, term, pk, val)
			})
			if err != nil {
				return nil, err
			}
		}
	}
	if corpus.Docs == 0 {
		return nil, nil
	}
	q = fulltext.ApplyTypoTolerance(q, func(term string) bool {
		return df[term] > 0
	})
	exp := fulltext.NewPrefixExpander(q.Terms)
	fuzzyBudget := fulltext.NewFuzzyVocabularyBudget()
	for si := range segs {
		itx := segs[si].itx
		for _, pfx := range q.Prefixes {
			start, end := fulltext.PostingPrefixBounds(pfx)
			err := itx.Range(start, end, func(key, val []byte) error {
				term, pk, err := fulltext.SplitPostingKey(key)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(term, pfx) {
					return nil
				}
				if err := exp.Observe(term); err != nil {
					return err
				}
				return addPosting(si, term, pk, val)
			})
			if err != nil {
				return nil, err
			}
		}
		if len(q.Fuzzies) > 0 {
			start, end := fulltext.PostingPrefixBounds("")
			var last string
			var lastMatch bool
			err := itx.Range(start, end, func(key, val []byte) error {
				term, pk, err := fulltext.SplitPostingKey(key)
				if err != nil {
					return err
				}
				if term != last {
					last = term
					if err := fuzzyBudget.Observe(term); err != nil {
						return err
					}
					lastMatch = q.FuzzyMatchesTerm(term)
					if lastMatch {
						if err := exp.Observe(term); err != nil {
							return err
						}
					}
				}
				if !lastMatch {
					return nil
				}
				return addPosting(si, term, pk, val)
			})
			if err != nil {
				return nil, err
			}
		}
	}
	avg := fulltext.AvgDL(corpus)
	type segHit struct {
		seg int
		hit fulltext.Hit
	}
	var hits []segHit
	for dk, terms := range docs {
		tf := make(map[string]uint32, len(terms))
		pos := make(map[string][]uint32, len(terms))
		for t, p := range terms {
			tf[t] = p.tf
			pos[t] = p.pos
		}
		if !fulltext.QueryMatches(q, tf, pos) {
			continue
		}
		pk := []byte(dk.id)
		dlRaw, err := segs[dk.seg].itx.Lookup(fulltext.DocLenKey(pk))
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
		score := fulltext.QueryScoreWeighted(q, tf, pos, n.Weights, df, dl, avg, corpus.Docs)
		hits = append(hits, segHit{seg: dk.seg, hit: fulltext.Hit{PK: pk, Score: score}})
	}
	sort.Slice(hits, func(i, j int) bool { return fulltext.LessHit(hits[i].hit, hits[j].hit) })
	var out [][]types.Value
	for _, h := range hits {
		if err := s.budget().Check(); err != nil {
			return nil, err
		}
		row, err := s.fetchPKRow(segs[h.seg].htx, tab, h.hit.PK)
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
	if tab == nil || len(n.Columns) == 0 {
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
	vocab := make(map[string]struct{})
	var docs, tokens uint64
	for _, row := range rows {
		doc, err := analyzeSearchRow(row, n.Columns, fulltextAnalyzer(tab, n.IndexName, n.Columns))
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
			vocab[t.Term] = struct{}{}
		}
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return nil, err
		}
		cand = append(cand, scored{row: row, hit: fulltext.Hit{PK: pk}, pos: pos, tf: tf, dl: doc.Len})
	}
	q = fulltext.ApplyTypoTolerance(q, func(term string) bool {
		_, ok := vocab[term]
		return ok
	})
	if len(q.Fuzzies) > 0 {
		fuzzyBudget := fulltext.NewFuzzyVocabularyBudget()
		for term := range vocab {
			if err := fuzzyBudget.Observe(term); err != nil {
				return nil, err
			}
		}
	}
	df := make(map[string]uint64, len(q.Terms)+len(q.Prefixes)+len(q.Fuzzies))
	exp := fulltext.NewPrefixExpander(q.Terms)
	var matched []scored
	for _, c := range cand {
		for term, freq := range c.tf {
			if freq == 0 {
				continue
			}
			if q.PrefixMatchesTerm(term) || q.FuzzyMatchesTerm(term) {
				if err := exp.Observe(term); err != nil {
					return nil, err
				}
			}
		}
		if !fulltext.QueryMatches(q, c.tf, c.pos) {
			continue
		}
		counted := make(map[string]struct{}, len(c.tf))
		for _, term := range q.Terms {
			if c.tf[term] > 0 {
				df[term]++
				counted[term] = struct{}{}
			}
		}
		for term, freq := range c.tf {
			if freq == 0 {
				continue
			}
			if _, ok := counted[term]; ok {
				continue
			}
			if q.PrefixMatchesTerm(term) || q.FuzzyMatchesTerm(term) {
				df[term]++
			}
		}
		matched = append(matched, c)
	}
	cand = matched
	avg := fulltext.AvgDL(fulltext.Stats{Docs: docs, Tokens: tokens})
	var hits []scored
	for _, c := range cand {
		c.hit.Score = fulltext.QueryScoreWeighted(q, c.tf, c.pos, n.Weights, df, c.dl, avg, docs)
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
