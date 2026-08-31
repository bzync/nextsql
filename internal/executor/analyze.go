package executor

import (
	"crypto/sha256"
	"math"
	"math/rand"
	"sort"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

const (
	maxAnalyzeSample      = 100_000
	autoAnalyzeMinChanges = 1_000
	histBuckets           = 32
	mcvLimit              = 10
	segmentCount          = 8
	maxPartitionSample    = 4_096
)

func (s *Session) maybeAutoAnalyze(tab *catalog.Table, changed int64) error {
	if tab == nil || changed <= 0 || s.pending == nil {
		return nil
	}
	s.pending.statsChanges[tab.Name] += uint64(changed)
	threshold := uint64(autoAnalyzeMinChanges)
	if st, ok := s.lookupStats(tab.Name); ok && st.Rows/5 > threshold {
		threshold = st.Rows / 5
	}
	if s.pending.statsChanges[tab.Name] < threshold {
		return nil
	}
	st, err := s.collectStats(tab)
	if err != nil {
		return err
	}
	// Automatic refresh favors a bounded catalog record. Detailed segment,
	// histogram, and MCV data remains available through explicit ANALYZE.
	st.Segments = nil
	for i := range st.Columns {
		st.Columns[i].Histogram = nil
		st.Columns[i].MCV = nil
	}
	if err := s.persistStats(st); err != nil {
		return err
	}
	s.pending.statsChanges[tab.Name] = 0
	return nil
}

const (
	autoMaintenanceMinChanges = 1_000
	autoMaintenanceLimit      = 10_000
)

func (s *Session) recordAutomaticMaintenance(tab *catalog.Table, changed int64) {
	if tab == nil || changed <= 0 || s.pending == nil {
		return
	}
	s.pending.maintenanceChanges[tab.Name] += uint64(changed)
}

func (s *Session) runAutomaticMaintenance(changes map[string]uint64) {
	if s == nil || s.db == nil || len(changes) == 0 {
		return
	}
	names := make([]string, 0, len(changes))
	for name, changed := range changes {
		if changed >= autoMaintenanceMinChanges {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		limit := int(changes[name])
		if limit > autoMaintenanceLimit {
			limit = autoMaintenanceLimit
		}
		// The transaction is already durable. A paused/busy coordinator or a
		// live snapshot defers cleanup without changing the commit outcome.
		_, _ = s.db.CleanupTableDeadVersions(name, limit)
	}
}

func (s *Session) execAnalyze(p planner.Analyze) (*Result, error) {
	var tabs []*catalog.Table
	if p.Table != nil {
		t, ok := s.lookup(p.Table.Name)
		if !ok {
			return nil, nerr.New(nerr.NotFound, "executor.Analyze", "unknown table")
		}
		tabs = []*catalog.Table{t}
	} else {
		tabs = s.db.Cat.List()
		if s.overlay != nil {
			seen := map[string]struct{}{}
			var all []*catalog.Table
			for name, t := range s.overlay {
				if t == nil {
					seen[name] = struct{}{}
					continue
				}
				all = append(all, t.Clone())
				seen[t.Name] = struct{}{}
			}
			for _, t := range tabs {
				if _, ok := seen[t.Name]; !ok {
					all = append(all, t)
				}
			}
			tabs = all
		}
	}
	sort.Slice(tabs, func(i, j int) bool { return tabs[i].Name < tabs[j].Name })
	var n int64
	for _, tab := range tabs {
		st, err := s.collectStats(tab)
		if err != nil {
			return nil, err
		}
		if err := s.persistStats(st); err != nil {
			return nil, err
		}
		n++
	}
	return &Result{Affected: n}, nil
}

func (s *Session) persistStats(st *catalog.TableStats) error {
	raw, err := catalog.EncodeStats(st)
	if err != nil {
		return err
	}
	partitionRecords := make(map[string][]byte, len(st.Partitions))
	snapshot := sha256.Sum256(raw)
	for _, part := range st.Partitions {
		body, err := catalog.EncodePartitionStats(st.TableID, snapshot, part)
		if err != nil {
			return err
		}
		partitionRecords[string(catalog.PartitionStatsKey(st.TableID, part.ID))] = body
	}
	key := catalog.StatsKey(st.Table)
	ctx := s.x.use(s.db.CatTree)
	start, end := catalog.PartitionStatsRange(st.TableID)
	var stale [][]byte
	if err := ctx.Range(start, end, func(key, _ []byte) error {
		if _, keep := partitionRecords[string(key)]; !keep {
			stale = append(stale, append([]byte(nil), key...))
		}
		return nil
	}); err != nil {
		return err
	}
	for _, key := range stale {
		if err := ctx.Delete(key); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
	}
	_, err = ctx.Lookup(key)
	if err != nil {
		if !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
		if err := ctx.Insert(key, raw); err != nil {
			return err
		}
	} else if err := ctx.Update(key, raw); err != nil {
		return err
	}
	partitionKeys := make([]string, 0, len(partitionRecords))
	for encodedKey := range partitionRecords {
		partitionKeys = append(partitionKeys, encodedKey)
	}
	sort.Strings(partitionKeys)
	for _, encodedKey := range partitionKeys {
		body := partitionRecords[encodedKey]
		key := []byte(encodedKey)
		if _, err := ctx.Lookup(key); err != nil {
			if !nerr.HasCode(err, nerr.NotFound) {
				return err
			}
			if err := ctx.Insert(key, body); err != nil {
				return err
			}
		} else if err := ctx.Update(key, body); err != nil {
			return err
		}
	}
	if s.pending != nil {
		if s.pending.stats == nil {
			s.pending.stats = make(map[string]*catalog.TableStats)
		}
		s.pending.stats[st.Table] = st
	}
	return nil
}

func (s *Session) deletePartitionStats(tableID uint32) error {
	if s == nil || s.x == nil || tableID == 0 {
		return nil
	}
	ctx := s.x.use(s.db.CatTree)
	start, end := catalog.PartitionStatsRange(tableID)
	var keys [][]byte
	if err := ctx.Range(start, end, func(key, _ []byte) error {
		keys = append(keys, append([]byte(nil), key...))
		return nil
	}); err != nil {
		return err
	}
	for _, key := range keys {
		if err := ctx.Delete(key); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
	}
	return nil
}

func (s *Session) deleteOnePartitionStats(tableID, partitionID uint32) error {
	if s == nil || s.x == nil || tableID == 0 || partitionID == 0 {
		return nil
	}
	err := s.x.use(s.db.CatTree).Delete(catalog.PartitionStatsKey(tableID, partitionID))
	if err != nil && !nerr.HasCode(err, nerr.NotFound) {
		return err
	}
	return nil
}

func (s *Session) collectStats(tab *catalog.Table) (*catalog.TableStats, error) {
	ncols := len(tab.Columns)
	nulls := make([]uint64, ncols)
	mins := make([]types.Value, ncols)
	maxs := make([]types.Value, ncols)
	hasMM := make([]bool, ncols)
	var (
		sample [][]types.Value
		rows   uint64
	)
	rng := rand.New(rand.NewSource(1))
	consume := func(row []types.Value) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		rows++
		for i, v := range row {
			if v.Null {
				nulls[i]++
				continue
			}
			if !tab.Columns[i].Type.Comparable() {
				continue
			}
			if !hasMM[i] {
				mins[i], maxs[i] = v.Clone(), v.Clone()
				hasMM[i] = true
				continue
			}
			if c, err := v.Cmp(mins[i]); err == nil && c < 0 {
				mins[i] = v.Clone()
			}
			if c, err := v.Cmp(maxs[i]); err == nil && c > 0 {
				maxs[i] = v.Clone()
			}
		}
		if uint64(len(sample)) < maxAnalyzeSample {
			sample = append(sample, cloneRow(row))
			return nil
		}
		j := rng.Int63n(int64(rows))
		if j < int64(maxAnalyzeSample) {
			sample[j] = cloneRow(row)
		}
		return nil
	}
	var partitionStats []catalog.PartitionStats
	if tab.Partitioning != nil {
		partitionStats = make([]catalog.PartitionStats, 0, len(tab.Partitioning.Partitions))
		for _, part := range tab.Partitioning.Partitions {
			local := newAnalyzeAccumulator(tab, maxPartitionSample, int64(part.ID)+1)
			if err := s.scanHeapPartitions(tab, []uint32{part.ID}, nil, nil, true, true, func(row []types.Value) error {
				if err := consume(row); err != nil {
					return err
				}
				local.consume(row)
				return nil
			}); err != nil {
				return nil, err
			}
			partitionStats = append(partitionStats, s.finishPartitionStats(tab, part.ID, local))
		}
	} else if err := s.scanHeap(tab, nil, nil, true, true, consume); err != nil {
		return nil, err
	}
	st := &catalog.TableStats{Table: tab.Name, TableID: tab.ID, Rows: rows}
	st.Partitions = partitionStats
	scale := 1.0
	if len(sample) > 0 && rows > uint64(len(sample)) {
		scale = float64(rows) / float64(len(sample))
	}
	for i := 0; i < ncols; i++ {
		cs := catalog.ColumnStats{Ord: i, Nulls: nulls[i], HasMinMax: hasMM[i]}
		if hasMM[i] {
			cs.Min, cs.Max = mins[i], maxs[i]
		}
		if tab.Columns[i].Type.Comparable() {
			cs.NDV, cs.Histogram, cs.MCV = colSketch(sample, i, scale)
			cs.Correlation = spearman(sample, i, tab)
		}
		st.Columns = append(st.Columns, cs)
	}
	for _, idx := range tab.Indexes {
		is := catalog.IndexStats{Name: idx.Name, Unique: idx.Unique}
		if len(idx.Columns) > 0 {
			if cs, ok := st.Column(idx.Columns[0]); ok {
				is.NDV = cs.NDV
			}
		}
		if idx.Unique && rows > 0 {
			is.Selectivity = 1 / float64(rows)
			is.NDV = rows
		} else if is.NDV > 0 {
			is.Selectivity = 1 / float64(is.NDV)
		} else {
			is.Selectivity = 0.1
		}
		st.Indexes = append(st.Indexes, is)
	}
	st.Vectors = s.collectVectorStats(tab, nulls, rows)
	st.Segments = buildSegments(sample, tab, scale)
	return st, nil
}

type analyzeAccumulator struct {
	rows   uint64
	nulls  []uint64
	mins   []types.Value
	maxs   []types.Value
	hasMM  []bool
	sample [][]types.Value
	limit  int
	rng    *rand.Rand
}

func newAnalyzeAccumulator(tab *catalog.Table, limit int, seed int64) *analyzeAccumulator {
	ncols := 0
	if tab != nil {
		ncols = len(tab.Columns)
	}
	return &analyzeAccumulator{
		nulls: make([]uint64, ncols),
		mins:  make([]types.Value, ncols),
		maxs:  make([]types.Value, ncols),
		hasMM: make([]bool, ncols),
		limit: limit,
		rng:   rand.New(rand.NewSource(seed)),
	}
}

func (a *analyzeAccumulator) consume(row []types.Value) {
	if a == nil {
		return
	}
	a.rows++
	for i, value := range row {
		if i >= len(a.nulls) {
			break
		}
		if value.Null {
			a.nulls[i]++
			continue
		}
		if !value.Typ.Comparable() {
			continue
		}
		if !a.hasMM[i] {
			a.mins[i], a.maxs[i], a.hasMM[i] = value.Clone(), value.Clone(), true
			continue
		}
		if cmp, err := value.Cmp(a.mins[i]); err == nil && cmp < 0 {
			a.mins[i] = value.Clone()
		}
		if cmp, err := value.Cmp(a.maxs[i]); err == nil && cmp > 0 {
			a.maxs[i] = value.Clone()
		}
	}
	if a.limit <= 0 {
		return
	}
	if len(a.sample) < a.limit {
		a.sample = append(a.sample, cloneRow(row))
		return
	}
	j := a.rng.Int63n(int64(a.rows))
	if j < int64(a.limit) {
		a.sample[j] = cloneRow(row)
	}
}

func (s *Session) finishPartitionStats(tab *catalog.Table, id uint32, a *analyzeAccumulator) catalog.PartitionStats {
	out := catalog.PartitionStats{ID: id}
	if tab == nil || a == nil {
		return out
	}
	out.Rows = a.rows
	scale := 1.0
	if len(a.sample) > 0 && a.rows > uint64(len(a.sample)) {
		scale = float64(a.rows) / float64(len(a.sample))
	}
	for _, ord := range partitionSketchOrdinals(tab) {
		col := catalog.ColumnStats{Ord: ord, Nulls: a.nulls[ord], HasMinMax: a.hasMM[ord]}
		if col.HasMinMax {
			col.Min, col.Max = a.mins[ord].Clone(), a.maxs[ord].Clone()
		}
		if tab.Columns[ord].Type.Comparable() {
			col.NDV, _, _ = colSketch(a.sample, ord, scale)
			col.Correlation = spearman(a.sample, ord, tab)
		}
		out.Columns = append(out.Columns, col)
	}
	for _, idx := range tab.Indexes {
		if len(out.Indexes) >= catalog.MaxPartitionSketchIndexes {
			break
		}
		is := catalog.IndexStats{Name: idx.Name, Unique: idx.Unique}
		if len(idx.Columns) > 0 {
			for _, col := range out.Columns {
				if col.Ord == idx.Columns[0] {
					is.NDV = col.NDV
					break
				}
			}
		}
		if idx.Unique && a.rows > 0 {
			is.Selectivity, is.NDV = 1/float64(a.rows), a.rows
		} else if is.NDV > 0 {
			is.Selectivity = 1 / float64(is.NDV)
		} else {
			is.Selectivity = 0.1
		}
		out.Indexes = append(out.Indexes, is)
	}
	for ord, col := range tab.Columns {
		if len(out.Vectors) >= catalog.MaxPartitionSketchVectors {
			break
		}
		if col.Type.Kind != types.KindVector {
			continue
		}
		out.Vectors = append(out.Vectors, catalog.VectorStats{Ord: ord, Count: a.rows - a.nulls[ord], Dim: col.Type.Precision})
	}
	return boundPartitionStats(tab.ID, out)
}

// boundPartitionStats keeps validate-all-before-mutation memory below the
// per-record catalog limit. It preserves the deterministic priority prefix and
// converges in logarithmic steps for adversarial wide/value-heavy schemas.
func boundPartitionStats(tableID uint32, out catalog.PartitionStats) catalog.PartitionStats {
	for attempts := 0; attempts < 24; attempts++ {
		if _, err := catalog.EncodePartitionStats(tableID, [32]byte{}, out); err == nil {
			return out
		}
		switch {
		case len(out.Columns) > 0:
			out.Columns = out.Columns[:len(out.Columns)/2]
		case len(out.Indexes) > 0:
			out.Indexes = out.Indexes[:len(out.Indexes)/2]
		case len(out.Vectors) > 0:
			out.Vectors = out.Vectors[:len(out.Vectors)/2]
		default:
			return out
		}
	}
	return out
}

// partitionSketchOrdinals prioritizes routing, indexed, and vector columns,
// then fills the remaining bounded record in catalog order. Missing local
// sketches always fall back to the global NSST distribution.
func partitionSketchOrdinals(tab *catalog.Table) []int {
	if tab == nil {
		return nil
	}
	out := make([]int, 0, min(len(tab.Columns), catalog.MaxPartitionSketchColumns))
	seen := make(map[int]struct{}, cap(out))
	add := func(ord int) {
		if len(out) >= catalog.MaxPartitionSketchColumns || ord < 0 || ord >= len(tab.Columns) {
			return
		}
		if _, exists := seen[ord]; exists {
			return
		}
		seen[ord] = struct{}{}
		out = append(out, ord)
	}
	if tab.Partitioning != nil {
		for _, ord := range tab.Partitioning.Columns {
			add(ord)
		}
	}
	for _, idx := range tab.Indexes {
		for _, ord := range idx.Columns {
			add(ord)
		}
	}
	for ord, col := range tab.Columns {
		if col.Type.Kind == types.KindVector {
			add(ord)
		}
	}
	for ord := range tab.Columns {
		add(ord)
	}
	return out
}

func (s *Session) collectVectorStats(tab *catalog.Table, nulls []uint64, rows uint64) []catalog.VectorStats {
	if tab == nil {
		return nil
	}
	var out []catalog.VectorStats
	for i, col := range tab.Columns {
		if col.Type.Kind != types.KindVector {
			continue
		}
		vs := catalog.VectorStats{
			Ord:   i,
			Count: rows - nulls[i],
			Dim:   col.Type.Precision,
		}
		for _, idx := range tab.Indexes {
			if !idx.Vector || len(idx.Columns) != 1 || idx.Columns[0] != i {
				continue
			}
			vs.IndexName = idx.Name
			if tab.Partitioning != nil {
				var total uint64
				var seen bool
				for _, part := range tab.Partitioning.Partitions {
					g, err := s.graphOfPartition(tab, part, idx)
					if err != nil {
						continue
					}
					meta, err := g.LoadMeta()
					if err != nil {
						continue
					}
					seen = true
					total += meta.Count
					if vs.M == 0 {
						vs.M = uint16(meta.M)
						vs.EfConstruct = meta.EfConstruct
					}
					if meta.Dim > 0 {
						vs.Dim = meta.Dim
					}
				}
				if seen {
					vs.Count = total
				}
			} else if g, err := s.graphOf(tab, idx); err == nil {
				if meta, err := g.LoadMeta(); err == nil {
					vs.M = uint16(meta.M)
					vs.EfConstruct = meta.EfConstruct
					if meta.Count > 0 {
						vs.Count = meta.Count
					}
					if meta.Dim > 0 {
						vs.Dim = meta.Dim
					}
				}
			}
			break
		}
		out = append(out, vs)
	}
	return out
}

func cloneRow(row []types.Value) []types.Value {
	out := make([]types.Value, len(row))
	for i := range row {
		out[i] = row[i].Clone()
	}
	return out
}

func colSketch(sample [][]types.Value, ord int, scale float64) (ndv uint64, hist []catalog.HistBucket, mcv []catalog.MCV) {
	type pair struct {
		v types.Value
		n int
	}
	freq := map[string]pair{}
	var vals []types.Value
	for _, row := range sample {
		if ord >= len(row) || row[ord].Null {
			continue
		}
		v := row[ord]
		k, err := types.EncodeKey([]types.Value{v})
		if err != nil {
			continue
		}
		p := freq[string(k)]
		p.v = v
		p.n++
		freq[string(k)] = p
		vals = append(vals, v)
	}
	ndv = uint64(len(freq))
	if len(vals) == 0 {
		return ndv, nil, nil
	}
	sort.SliceStable(vals, func(i, j int) bool {
		c, err := vals[i].Cmp(vals[j])
		return err == nil && c < 0
	})
	nb := histBuckets
	if len(vals) < nb {
		nb = len(vals)
	}
	if nb < 1 {
		nb = 1
	}
	chunk := (len(vals) + nb - 1) / nb
	for i := 0; i < len(vals); i += chunk {
		end := i + chunk
		if end > len(vals) {
			end = len(vals)
		}
		seen := map[string]struct{}{}
		for _, v := range vals[i:end] {
			if k, err := types.EncodeKey([]types.Value{v}); err == nil {
				seen[string(k)] = struct{}{}
			}
		}
		hist = append(hist, catalog.HistBucket{
			Lower: vals[i].Clone(),
			Upper: vals[end-1].Clone(),
			Count: uint64(math.Round(float64(end-i) * scale)),
			NDV:   uint64(len(seen)),
		})
	}
	type mf struct {
		v types.Value
		n int
	}
	var list []mf
	for _, p := range freq {
		list = append(list, mf{v: p.v, n: p.n})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].n != list[j].n {
			return list[i].n > list[j].n
		}
		c, err := list[i].v.Cmp(list[j].v)
		return err == nil && c < 0
	})
	thresh := len(sample) / 100
	if thresh < 2 {
		thresh = 2
	}
	for i := 0; i < len(list) && i < mcvLimit; i++ {
		if list[i].n < thresh {
			break
		}
		mcv = append(mcv, catalog.MCV{Value: list[i].v.Clone(), Freq: uint64(math.Round(float64(list[i].n) * scale))})
	}
	return ndv, hist, mcv
}

func spearman(sample [][]types.Value, ord int, tab *catalog.Table) float64 {
	if len(tab.PK) == 0 {
		return 0
	}
	pk := tab.PK[0]
	var pts []rankPt
	for _, row := range sample {
		if ord >= len(row) || pk >= len(row) || row[ord].Null || row[pk].Null {
			continue
		}
		if !row[ord].Typ.Comparable() || !row[pk].Typ.Comparable() {
			continue
		}
		pts = append(pts, rankPt{c: row[ord], p: row[pk]})
	}
	if len(pts) < 2 {
		return 0
	}
	cr := ranks(pts, func(a, b rankPt) int {
		c, err := a.c.Cmp(b.c)
		if err != nil {
			return 0
		}
		return c
	})
	pr := ranks(pts, func(a, b rankPt) int {
		c, err := a.p.Cmp(b.p)
		if err != nil {
			return 0
		}
		return c
	})
	return pearson(cr, pr)
}

type rankPt struct{ c, p types.Value }

func ranks(pts []rankPt, cmp func(a, b rankPt) int) []float64 {
	idx := make([]int, len(pts))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(i, j int) bool { return cmp(pts[idx[i]], pts[idx[j]]) < 0 })
	out := make([]float64, len(pts))
	for r, i := range idx {
		out[i] = float64(r)
	}
	return out
}

func pearson(x, y []float64) float64 {
	n := float64(len(x))
	if n < 2 || len(x) != len(y) {
		return 0
	}
	var sx, sy, sxx, syy, sxy float64
	for i := range x {
		sx += x[i]
		sy += y[i]
		sxx += x[i] * x[i]
		syy += y[i] * y[i]
		sxy += x[i] * y[i]
	}
	num := n*sxy - sx*sy
	den := math.Sqrt((n*sxx - sx*sx) * (n*syy - sy*sy))
	if den == 0 {
		return 0
	}
	r := num / den
	if r > 1 {
		return 1
	}
	if r < -1 {
		return -1
	}
	return r
}

func buildSegments(sample [][]types.Value, tab *catalog.Table, scale float64) []catalog.SegmentStats {
	if len(sample) == 0 || len(tab.PK) == 0 {
		return nil
	}
	rows := append([][]types.Value(nil), sample...)
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := tab.PKValues(rows[i]), tab.PKValues(rows[j])
		for k := 0; k < len(a) && k < len(b); k++ {
			if a[k].Null || b[k].Null {
				continue
			}
			c, err := a[k].Cmp(b[k])
			if err != nil || c == 0 {
				continue
			}
			return c < 0
		}
		return false
	})
	n := segmentCount
	if len(rows) < n {
		n = len(rows)
	}
	if n < 1 {
		return nil
	}
	chunk := (len(rows) + n - 1) / n
	var out []catalog.SegmentStats
	for i, id := 0, 0; i < len(rows); i, id = i+chunk, id+1 {
		end := i + chunk
		if end > len(rows) {
			end = len(rows)
		}
		part := rows[i:end]
		seg := catalog.SegmentStats{
			ID:        id,
			Rows:      uint64(math.Round(float64(len(part)) * scale)),
			LowPK:     cloneRow(tab.PKValues(part[0])),
			HighPK:    cloneRow(tab.PKValues(part[len(part)-1])),
			ColMin:    make([]types.Value, len(tab.Columns)),
			ColMax:    make([]types.Value, len(tab.Columns)),
			HasBounds: true,
		}
		has := make([]bool, len(tab.Columns))
		for _, row := range part {
			for c, v := range row {
				if v.Null || !tab.Columns[c].Type.Comparable() {
					continue
				}
				if !has[c] {
					seg.ColMin[c], seg.ColMax[c] = v.Clone(), v.Clone()
					has[c] = true
					continue
				}
				if cmp, err := v.Cmp(seg.ColMin[c]); err == nil && cmp < 0 {
					seg.ColMin[c] = v.Clone()
				}
				if cmp, err := v.Cmp(seg.ColMax[c]); err == nil && cmp > 0 {
					seg.ColMax[c] = v.Clone()
				}
			}
		}
		for c := range seg.ColMin {
			if !has[c] {
				seg.ColMin[c] = types.Null(tab.Columns[c].Type)
				seg.ColMax[c] = types.Null(tab.Columns[c].Type)
			}
		}
		out = append(out, seg)
	}
	return out
}
