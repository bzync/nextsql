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
	vs, err := s.ensureVec(tab)
	if err != nil {
		return err
	}
	vtx := s.x.use(vs)
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
		if err := nsvec.Check(row[i].Vec, int(c.Type.Precision)); err != nil {
			return err
		}
		raw, err := nsvec.EncodePayload(row[i].Vec)
		if err != nil {
			return err
		}
		if err := s.upsert(vtx, key, raw); err != nil {
			return err
		}
	}
	return nil
}

func (s *Session) deleteVectors(tab *catalog.Table, pk []byte) error {
	if tab == nil || !tab.HasVector() || tab.VecMeta == 0 {
		return nil
	}
	vs, err := s.vecOf(tab)
	if err != nil {
		return err
	}
	vtx := s.x.use(vs)
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
	if tab == nil || !tab.HasVector() || tab.VecMeta == 0 {
		return nil
	}
	need := false
	for i, c := range tab.Columns {
		if c.Type.Kind == types.KindVector && i < len(row) && !row[i].Null && (row[i].VecRef || len(row[i].Vec) == 0) {
			need = true
			break
		}
	}
	if !need {
		return nil
	}
	vs, err := s.vecOf(tab)
	if err != nil {
		return err
	}
	pk, err := types.EncodeKey(tab.PKValues(row))
	if err != nil {
		return err
	}
	vtx := s.x.use(vs)
	for i, c := range tab.Columns {
		if c.Type.Kind != types.KindVector || i >= len(row) || row[i].Null {
			continue
		}
		if !row[i].VecRef && len(row[i].Vec) > 0 {
			continue
		}
		raw, err := vtx.Lookup(nsvec.PayloadKey(uint16(i), pk))
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return nerr.New(nerr.Corruption, "executor.hydrate", "missing vector payload")
			}
			return err
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
	raw, err := g.lookup(g.vtx, nsvec.PayloadKey(g.col, pk))
	if err != nil {
		return nil, err
	}
	return nsvec.DecodePayload(raw)
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

func (db *DB) dropHNSW(key string) {
	if db == nil {
		return
	}
	db.hnswMu.Lock()
	delete(db.hnsw, key)
	db.hnswMu.Unlock()
}

func (db *DB) dropAllHNSW() {
	if db == nil {
		return
	}
	db.hnswMu.Lock()
	db.hnswGen++
	db.hnsw = make(map[string]*lockedMem)
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
	if s.dirtyHNSW {
		s.db.dropAllHNSW()
	}
	gen := s.db.hnswGeneration()
	for k, m := range s.pendingHNSW {
		if m != nil {
			m.gen = gen
			s.db.setHNSW(k, m)
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
	mem := nsvec.NewMem(dim, nsvec.MetricCosine)
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
	g := &sqlGraph{itx: s.x.use(ix), vtx: s.x.use(vs), col: uint16(idx.Columns[0])}
	if snap, ok, err := s.fkWriteSnap(); err != nil {
		return nil, err
	} else if ok {
		g.snap = snap
		g.useSnap = true
	}
	return g, nil
}
