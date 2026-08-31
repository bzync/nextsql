package executor

import (
	"sync"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

// graphMetric is the HNSW graph distance for a vector column: HAMMING for a
// BITVECTOR column, COSINE otherwise. (SQL has no per-index metric clause yet;
// search-time USING falls back to exact flat when it differs from the graph.)
func graphMetric(colType types.Type) nsvec.Metric {
	if colType.VecElem == types.VecBit {
		return nsvec.MetricHamming
	}
	return nsvec.MetricCosine
}

func (s *Session) vecOf(t *catalog.Table) (*btree.Tree, error) {
	if t == nil || t.VecMeta == 0 {
		return nil, nerr.New(nerr.NotFound, "executor.vecOf", "vector store not open")
	}
	if s.pending != nil {
		if tr, ok := s.pending.vecs[t.Name]; ok {
			return tr, nil
		}
	}
	return s.db.vecStore(t.Name)
}

func (s *Session) ensureVec(tab *catalog.Table) (*btree.Tree, error) {
	if tab.VecMeta != 0 {
		if tr, err := s.vecOf(tab); err == nil {
			return tr, nil
		}
	}
	s.db.Eng.Enter(s.x.owner.Storage())
	vs, err := btree.CreateDetached(s.db.Eng)
	s.db.Eng.Leave(s.x.owner.Storage())
	if err != nil {
		return nil, err
	}
	tab.VecMeta = vs.Meta()
	raw, err := catalog.EncodeTable(tab)
	if err != nil {
		return nil, err
	}
	if err := s.x.use(s.db.CatTree).Update(catalog.TableKey(tab.Name), raw); err != nil {
		return nil, err
	}
	if s.overlay != nil {
		s.overlay[tab.Name] = tab.Clone()
	}
	if s.pending != nil {
		if s.pending.vecs == nil {
			s.pending.vecs = make(map[string]*btree.Tree)
		}
		s.pending.vecs[tab.Name] = vs
	}
	return vs, nil
}

func detachVectors(tab *catalog.Table, row []types.Value) []types.Value {
	if tab == nil || !tab.HasVector() {
		return row
	}
	out := append([]types.Value(nil), row...)
	for i, c := range tab.Columns {
		if c.Type.Kind != types.KindVector || out[i].Null {
			continue
		}
		out[i] = types.VectorRef(c.Type)
	}
	return out
}

func (s *Session) putVectors(tab *catalog.Table, pk []byte, row []types.Value) error {
	if tab == nil || !tab.HasVector() {
		return nil
	}
	var vtx *btree.Txn
	if tab.Partitioning != nil {
		vt, err := s.partitionVecFor(tab, row)
		if err != nil {
			return err
		}
		vtx = s.x.use(vt)
	} else {
		vs, err := s.ensureVec(tab)
		if err != nil {
			return err
		}
		vtx = s.x.use(vs)
	}
	for i, c := range tab.Columns {
		if c.Type.Kind != types.KindVector {
			continue
		}
		key := nsvec.PayloadKey(uint16(i), pk)
		if i >= len(row) || row[i].Null {
			if err := s.treeDelete(vtx, key); err != nil && !nerr.HasCode(err, nerr.NotFound) {
				return err
			}
			continue
		}
		var raw []byte
		if c.Type.VecElem == types.VecSparse {
			sv, err := valueSparse(row[i], uint32(c.Type.Precision))
			if err != nil {
				return err
			}
			raw, err = nsvec.EncodeSparse(sv)
			if err != nil {
				return err
			}
		} else {
			if err := nsvec.Check(row[i].Vec, int(c.Type.Precision)); err != nil {
				return err
			}
			var err error
			raw, err = nsvec.EncodePayloadElem(row[i].Vec, c.Type.VecElem)
			if err != nil {
				return err
			}
		}
		if err := s.upsert(vtx, key, raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) deleteVectors(tab *catalog.Table, pk []byte, row []types.Value) error {
	if tab == nil || !tab.HasVector() {
		return nil
	}
	var vtx *btree.Txn
	if tab.Partitioning != nil {
		vt, err := s.partitionVecFor(tab, row)
		if err != nil {
			return err
		}
		vtx = s.x.use(vt)
	} else {
		if tab.VecMeta == 0 {
			return nil
		}
		vs, err := s.vecOf(tab)
		if err != nil {
			return err
		}
		vtx = s.x.use(vs)
	}
	for i, c := range tab.Columns {
		if c.Type.Kind != types.KindVector {
			continue
		}
		if err := s.treeDelete(vtx, nsvec.PayloadKey(uint16(i), pk)); err != nil && !nerr.HasCode(err, nerr.NotFound) {
			return err
		}
	}
	return nil
}

func (s *Session) hydrate(tab *catalog.Table, row []types.Value) error {
	if tab == nil || !tab.HasVector() {
		return nil
	}
	if tab.Partitioning == nil && tab.VecMeta == 0 {
		return nil
	}
	need := false
	for i, c := range tab.Columns {
		if c.Type.Kind != types.KindVector || i >= len(row) || row[i].Null {
			continue
		}
		if row[i].VecRef {
			need = true
			break
		}
		if c.Type.VecElem != types.VecSparse && len(row[i].Vec) == 0 {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	var vtx *btree.Txn
	if tab.Partitioning != nil {
		vt, err := s.partitionVecFor(tab, row)
		if err != nil {
			return err
		}
		vtx = s.x.use(vt)
	} else {
		vs, err := s.vecOf(tab)
		if err != nil {
			return err
		}
		vtx = s.x.use(vs)
	}
	pk, err := types.EncodeKey(tab.PKValues(row))
	if err != nil {
		return err
	}
	for i, c := range tab.Columns {
		if c.Type.Kind != types.KindVector || i >= len(row) || row[i].Null {
			continue
		}
		if c.Type.VecElem == types.VecSparse {
			if !row[i].VecRef {
				continue
			}
		} else if !row[i].VecRef && len(row[i].Vec) > 0 {
			continue
		}
		raw, err := vtx.Lookup(nsvec.PayloadKey(uint16(i), pk))
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nerr.New(nerr.Corruption, "executor.hydrate", "missing vector payload")
			}
			return err
		}
		if c.Type.VecElem == types.VecSparse {
			sv, err := nsvec.DecodeSparse(raw)
			if err != nil {
				return err
			}
			row[i] = types.SparseValue(sv.Indices, sv.Values, c.Type)
			continue
		}
		vec, err := nsvec.DecodePayload(raw)
		if err != nil {
			return err
		}
		row[i] = types.VectorValue(vec, c.Type)
	}
	return nil
}

func (s *Session) hydrateRows(tab *catalog.Table, rows [][]types.Value) error {
	for _, row := range rows {
		if err := s.hydrate(tab, row); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) decodeHeapRow(tab *catalog.Table, payload []byte) ([]types.Value, error) {
	row, err := types.DecodeRow(payload, tab.Types())
	if err != nil {
		return nil, err
	}
	if err := s.hydrate(tab, row); err != nil {
		return nil, err
	}
	return row, nil
}

func upsert(tx *btree.Txn, k, v []byte) error {
	_, err := tx.Lookup(k)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return tx.Insert(k, v)
		}
		return err
	}
	return tx.Update(k, v)
}

func (s *Session) upsert(tx *btree.Txn, k, v []byte) error {
	_, err := s.treeLookup(tx, k)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return s.treeInsert(tx, k, v)
		}
		return err
	}
	return s.treeUpdate(tx, k, v)
}

type sqlGraph struct {
	itx, vtx *btree.Txn
	col      uint16
	quant    uint8 // traversal quantisation (0 / VecF16 / VecI8) from the index def
	snap     txn.Snapshot
	useSnap  bool
}

func (g *sqlGraph) lookup(tx *btree.Txn, key []byte) ([]byte, error) {
	if g != nil && g.useSnap {
		return tx.LookupAt(key, g.snap)
	}
	return tx.Lookup(key)
}

func (g *sqlGraph) store(tx *btree.Txn, k, v []byte) error {
	if g != nil && g.useSnap {
		_, err := tx.LookupAt(k, g.snap)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return tx.InsertAt(k, v, g.snap)
			}
			return err
		}
		return tx.UpdateAt(k, v, g.snap)
	}
	return upsert(tx, k, v)
}

func (g *sqlGraph) LoadMeta() (nsvec.Meta, error) {
	raw, err := g.lookup(g.itx, nsvec.MetaKey())
	if err != nil {
		return nsvec.Meta{}, err
	}
	return nsvec.DecodeMeta(raw)
}

func (g *sqlGraph) SaveMeta(m nsvec.Meta) error {
	raw, err := nsvec.EncodeMeta(m)
	if err != nil {
		return err
	}
	return g.store(g.itx, nsvec.MetaKey(), raw)
}

func (g *sqlGraph) LoadNode(pk []byte) (nsvec.Node, bool, error) {
	raw, err := g.lookup(g.itx, nsvec.NodeKey(pk))
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nsvec.Node{}, false, nil
		}
		return nsvec.Node{}, false, err
	}
	n, err := nsvec.DecodeNode(raw)
	if err != nil {
		return nsvec.Node{}, false, err
	}
	return n, true, nil
}

func (g *sqlGraph) SaveNode(pk []byte, n nsvec.Node) error {
	raw, err := nsvec.EncodeNode(n)
	if err != nil {
		return err
	}
	return g.store(g.itx, nsvec.NodeKey(pk), raw)
}

func (g *sqlGraph) LoadVec(pk []byte) ([]float32, error) {
	if g.quant != 0 {
		raw, err := g.lookup(g.itx, nsvec.QVecKey(pk))
		if err == nil {
			return nsvec.DecodePayload(raw)
		}
		if !nerr.HasCode(err, nerr.NotFound) {
			return nil, err
		}
		// No quantised copy yet (mid-rebuild / legacy row): fall back to the
		// column payload, quantised on the fly so traversal stays consistent.
		full, ferr := g.LoadVecFull(pk)
		if ferr != nil {
			return nil, ferr
		}
		return nsvec.QuantizeElem(full, g.quant), nil
	}
	return g.LoadVecFull(pk)
}

// LoadVecFull returns the full-precision column payload, independent of any
// index-level traversal quantisation.
func (g *sqlGraph) LoadVecFull(pk []byte) ([]float32, error) {
	raw, err := g.lookup(g.vtx, nsvec.PayloadKey(g.col, pk))
	if err != nil {
		return nil, err
	}
	return nsvec.DecodePayload(raw)
}

// SaveQVec writes the traversal-quantised copy of v into the graph tree.
func (g *sqlGraph) SaveQVec(pk []byte, v []float32) error {
	if g.quant == 0 {
		return nil
	}
	raw, err := nsvec.EncodePayloadElem(v, g.quant)
	if err != nil {
		return err
	}
	return g.store(g.itx, nsvec.QVecKey(pk), raw)
}

func (g *sqlGraph) RangeNodes(fn func(pk []byte, n nsvec.Node) error) error {
	if g == nil || fn == nil {
		return nerr.New(nerr.InvalidArgument, "executor.sqlGraph.RangeNodes", "nil graph or callback")
	}
	start, end := nsvec.NodeBounds()
	scan := g.itx.Range
	if g.useSnap {
		scan = func(start, end []byte, fn func(key, value []byte) error) error {
			return g.itx.RangeAt(start, end, g.snap, fn)
		}
	}
	return scan(start, end, func(k, v []byte) error {
		pk, err := nsvec.SplitNodeKey(k)
		if err != nil {
			return err
		}
		n, err := nsvec.DecodeNode(v)
		if err != nil {
			return err
		}
		return fn(pk, n)
	})
}

// lockedMem is a process-local committed HNSW graph used for search.
type lockedMem struct {
	mu  sync.RWMutex
	mem *nsvec.Mem
	gen uint64
}

func newLockedMem(m *nsvec.Mem, gen uint64) *lockedMem {
	if m == nil {
		m = nsvec.NewMem(1, nsvec.MetricCosine)
	}
	return &lockedMem{mem: m, gen: gen}
}

func (m *lockedMem) LoadMeta() (nsvec.Meta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadMeta()
}

func (m *lockedMem) SaveMeta(meta nsvec.Meta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.SaveMeta(meta)
}

func (m *lockedMem) LoadNode(pk []byte) (nsvec.Node, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadNode(pk)
}

func (m *lockedMem) SaveNode(pk []byte, n nsvec.Node) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.SaveNode(pk, n)
}

func (m *lockedMem) LoadVec(pk []byte) ([]float32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadVec(pk)
}

func (m *lockedMem) LoadVecFull(pk []byte) ([]float32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadVecFull(pk)
}

func (m *lockedMem) SaveQVec(pk []byte, v []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.SaveQVec(pk, v)
}

func (m *lockedMem) PutVec(pk []byte, v []float32) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.mem.PutVec(pk, v)
}

func (db *DB) getHNSW(key string) *lockedMem {
	if db == nil {
		return nil
	}
	db.hnswMu.RLock()
	defer db.hnswMu.RUnlock()
	return db.hnsw[key]
}

func (db *DB) setHNSW(key string, m *lockedMem) {
	if db == nil || m == nil {
		return
	}
	db.hnswMu.Lock()
	if db.hnsw == nil {
		db.hnsw = make(map[string]*lockedMem)
	}
	db.hnsw[key] = m
	db.hnswMu.Unlock()
}

// dropHNSW evicts the process-local committed copy of one vector index — both
// the HNSW graph and the IVF quantiser share this keyspace and generation.
func (db *DB) dropHNSW(key string) {
	if db == nil {
		return
	}
	db.hnswMu.Lock()
	delete(db.hnsw, key)
	delete(db.ivf, key)
	db.hnswMu.Unlock()
}

// dropAllHNSW invalidates every process-local committed vector-index copy (HNSW
// and IVF) by bumping the shared generation and clearing both maps.
func (db *DB) dropAllHNSW() {
	if db == nil {
		return
	}
	db.hnswMu.Lock()
	db.hnswGen++
	db.hnsw = make(map[string]*lockedMem)
	db.ivf = make(map[string]*lockedIVF)
	db.hnswMu.Unlock()
}

func (db *DB) getIVF(key string) *lockedIVF {
	if db == nil {
		return nil
	}
	db.hnswMu.RLock()
	defer db.hnswMu.RUnlock()
	return db.ivf[key]
}

func (db *DB) setIVF(key string, m *lockedIVF) {
	if db == nil || m == nil {
		return
	}
	db.hnswMu.Lock()
	if db.ivf == nil {
		db.ivf = make(map[string]*lockedIVF)
	}
	db.ivf[key] = m
	db.hnswMu.Unlock()
}

func (db *DB) hnswGeneration() uint64 {
	if db == nil {
		return 0
	}
	db.hnswMu.RLock()
	defer db.hnswMu.RUnlock()
	return db.hnswGen
}

func (s *Session) installPendingHNSW() {
	if s == nil || s.db == nil {
		return
	}
	if s.dirtyHNSW || s.dirtyIVF {
		s.db.dropAllHNSW()
	}
	gen := s.db.hnswGeneration()
	for k, m := range s.pendingHNSW {
		if m != nil {
			m.gen = gen
			s.db.setHNSW(k, m)
		}
	}
	for k, m := range s.pendingIVF {
		if m != nil {
			m.gen = gen
			s.db.setIVF(k, m)
		}
	}
}

func (s *Session) hnswGraph(tab *catalog.Table, idx catalog.Index) (nsvec.Graph, error) {
	if s.dirtyHNSW {
		return s.graphOf(tab, idx)
	}
	key := idxKey(tab.Name, idx.Name)
	gen := uint64(0)
	if s.db != nil {
		gen = s.db.hnswGeneration()
		if m := s.db.getHNSW(key); m != nil && m.gen == gen {
			return m, nil
		}
	}
	g, err := s.graphOf(tab, idx)
	if err != nil {
		return nil, err
	}
	mem, err := nsvec.LoadMem(g)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return g, nil
		}
		return nil, err
	}
	locked := newLockedMem(mem, gen)
	if s.db != nil {
		s.db.setHNSW(key, locked)
	}
	return locked, nil
}

func (s *Session) buildVectorIndex(tab *catalog.Table, idx catalog.Index, htx *btree.Txn, progress *rebuildProgress) error {
	if len(idx.Columns) != 1 {
		return nerr.New(nerr.InvalidArgument, "executor.buildVectorIndex", "VECTOR INDEX column count")
	}
	col := idx.Columns[0]
	dim := tab.Columns[col].Type.Precision
	mem := nsvec.NewMem(dim, graphMetric(tab.Columns[col].Type))
	mem.Meta.Quant = idx.VecQuant
	if err := htx.Range(nil, nil, func(_, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		v := row[col]
		if v.Null {
			progress.add(1, 0)
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return err
		}
		mem.PutVec(pk, v.Vec)
		progress.add(1, 1)
		return nsvec.Insert(mem, pk, v.Vec)
	}); err != nil {
		return err
	}
	g, err := s.graphOf(tab, idx)
	if err != nil {
		return err
	}
	if err := nsvec.Persist(g, mem); err != nil {
		return err
	}
	if s.pendingHNSW == nil {
		s.pendingHNSW = make(map[string]*lockedMem)
	}
	s.pendingHNSW[idxKey(tab.Name, idx.Name)] = newLockedMem(mem, 0)
	return nil
}

func (s *Session) maintainVectorIndex(tab *catalog.Table, idx catalog.Index, old, neu []types.Value) error {
	s.dirtyHNSW = true
	g, err := s.graphOf(tab, idx)
	if err != nil {
		return err
	}
	if old != nil {
		pk, err := types.EncodeKey(tab.PKValues(old))
		if err != nil {
			return err
		}
		if err := nsvec.Delete(g, pk); err != nil {
			return err
		}
	}
	if neu != nil {
		col := idx.Columns[0]
		if col >= len(neu) || neu[col].Null {
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(neu))
		if err != nil {
			return err
		}
		return nsvec.Insert(g, pk, neu[col].Vec)
	}
	return nil
}

// graphOfPartition binds one partition-local HNSW graph: the partition's index
// root holds the graph nodes, the partition's vector store holds the payloads.
func (s *Session) graphOfPartition(tab *catalog.Table, part catalog.Partition, idx catalog.Index) (*sqlGraph, error) {
	if len(idx.Columns) != 1 {
		return nil, nerr.New(nerr.Internal, "executor.graphOfPartition", "VECTOR INDEX column count")
	}
	ix, err := s.partitionIndex(tab, part.ID, idx)
	if err != nil {
		return nil, err
	}
	vs, err := s.partitionVec(tab, part.ID)
	if err != nil {
		return nil, err
	}
	g := &sqlGraph{itx: s.x.use(ix), vtx: s.x.use(vs), col: uint16(idx.Columns[0]), quant: idx.VecQuant}
	if snap, ok, err := s.fkWriteSnap(); err != nil {
		return nil, err
	} else if ok {
		g.snap = snap
		g.useSnap = true
	}
	return g, nil
}

// hnswGraphPartition returns a searchable partition-local HNSW graph, preferring
// the process-local committed mem copy keyed by partitionIndexKey.
func (s *Session) hnswGraphPartition(tab *catalog.Table, part catalog.Partition, idx catalog.Index) (nsvec.Graph, error) {
	if s.dirtyHNSW {
		return s.graphOfPartition(tab, part, idx)
	}
	key := partitionIndexKey(tab.Name, part.ID, idx.Name)
	gen := uint64(0)
	if s.db != nil {
		gen = s.db.hnswGeneration()
		if m := s.db.getHNSW(key); m != nil && m.gen == gen {
			return m, nil
		}
	}
	g, err := s.graphOfPartition(tab, part, idx)
	if err != nil {
		return nil, err
	}
	mem, err := nsvec.LoadMem(g)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return g, nil
		}
		return nil, err
	}
	locked := newLockedMem(mem, gen)
	if s.db != nil {
		s.db.setHNSW(key, locked)
	}
	return locked, nil
}

// buildPartitionVectorIndex streams one partition heap into one partition-local
// HNSW graph. Vector payloads are already in the partition's vector store.
func (s *Session) buildPartitionVectorIndex(tab *catalog.Table, idx catalog.Index, part catalog.Partition, htx *btree.Txn, progress *rebuildProgress) error {
	if len(idx.Columns) != 1 {
		return nerr.New(nerr.InvalidArgument, "executor.buildPartitionVectorIndex", "VECTOR INDEX column count")
	}
	col := idx.Columns[0]
	dim := tab.Columns[col].Type.Precision
	mem := nsvec.NewMem(dim, graphMetric(tab.Columns[col].Type))
	mem.Meta.Quant = idx.VecQuant
	if err := htx.Range(nil, nil, func(_, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		row, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		v := row[col]
		if v.Null {
			progress.add(1, 0)
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(row))
		if err != nil {
			return err
		}
		mem.PutVec(pk, v.Vec)
		progress.add(1, 1)
		return nsvec.Insert(mem, pk, v.Vec)
	}); err != nil {
		return err
	}
	g, err := s.graphOfPartition(tab, part, idx)
	if err != nil {
		return err
	}
	if err := nsvec.Persist(g, mem); err != nil {
		return err
	}
	if s.pendingHNSW == nil {
		s.pendingHNSW = make(map[string]*lockedMem)
	}
	s.pendingHNSW[partitionIndexKey(tab.Name, part.ID, idx.Name)] = newLockedMem(mem, 0)
	return nil
}

// initPartitionVectorIndex persists an empty HNSW graph for a freshly created
// partition-local vector root (ADD PARTITION on a table that already has a
// vector index).
func (s *Session) initPartitionVectorIndex(tab *catalog.Table, idx catalog.Index, part catalog.Partition) error {
	if len(idx.Columns) != 1 {
		return nerr.New(nerr.InvalidArgument, "executor.initPartitionVectorIndex", "VECTOR INDEX column count")
	}
	s.dirtyHNSW = true
	mem := nsvec.NewMem(tab.Columns[idx.Columns[0]].Type.Precision, graphMetric(tab.Columns[idx.Columns[0]].Type))
	mem.Meta.Quant = idx.VecQuant
	g, err := s.graphOfPartition(tab, part, idx)
	if err != nil {
		return err
	}
	if err := nsvec.Persist(g, mem); err != nil {
		return err
	}
	if s.pendingHNSW == nil {
		s.pendingHNSW = make(map[string]*lockedMem)
	}
	s.pendingHNSW[partitionIndexKey(tab.Name, part.ID, idx.Name)] = newLockedMem(mem, 0)
	return nil
}

// maintainPartitionVectorIndex applies one row change to a partition-local HNSW
// graph. It marks the process-local mem copies dirty so search reloads from disk.
func (s *Session) maintainPartitionVectorIndex(tab *catalog.Table, idx catalog.Index, part catalog.Partition, old, neu []types.Value) error {
	s.dirtyHNSW = true
	g, err := s.graphOfPartition(tab, part, idx)
	if err != nil {
		return err
	}
	if old != nil {
		pk, err := types.EncodeKey(tab.PKValues(old))
		if err != nil {
			return err
		}
		if err := nsvec.Delete(g, pk); err != nil {
			return err
		}
	}
	if neu != nil {
		col := idx.Columns[0]
		if col >= len(neu) || neu[col].Null {
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(neu))
		if err != nil {
			return err
		}
		return nsvec.Insert(g, pk, neu[col].Vec)
	}
	return nil
}

// maintainCrossPartitionVectorIndex moves one row's HNSW entry between two
// partition-local graphs. Payloads have already been re-homed by putVectors /
// deleteVectors.
func (s *Session) maintainCrossPartitionVectorIndex(tab *catalog.Table, idx catalog.Index, oldPart, newPart catalog.Partition, old, neu []types.Value) error {
	s.dirtyHNSW = true
	og, err := s.graphOfPartition(tab, oldPart, idx)
	if err != nil {
		return err
	}
	oldPK, err := types.EncodeKey(tab.PKValues(old))
	if err != nil {
		return err
	}
	if err := nsvec.Delete(og, oldPK); err != nil {
		return err
	}
	col := idx.Columns[0]
	if col >= len(neu) || neu[col].Null {
		return nil
	}
	ng, err := s.graphOfPartition(tab, newPart, idx)
	if err != nil {
		return err
	}
	newPK, err := types.EncodeKey(tab.PKValues(neu))
	if err != nil {
		return err
	}
	return nsvec.Insert(ng, newPK, neu[col].Vec)
}

func (s *Session) graphOf(tab *catalog.Table, idx catalog.Index) (*sqlGraph, error) {
	ix, err := s.indexOf(tab, idx)
	if err != nil {
		return nil, err
	}
	vs, err := s.vecOf(tab)
	if err != nil {
		return nil, err
	}
	if len(idx.Columns) != 1 {
		return nil, nerr.New(nerr.Internal, "executor.graphOf", "VECTOR INDEX column count")
	}
	g := &sqlGraph{itx: s.x.use(ix), vtx: s.x.use(vs), col: uint16(idx.Columns[0]), quant: idx.VecQuant}
	if snap, ok, err := s.fkWriteSnap(); err != nil {
		return nil, err
	} else if ok {
		g.snap = snap
		g.useSnap = true
	}
	return g, nil
}
