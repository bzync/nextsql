package executor

import (
	"bytes"
	"encoding/binary"
	"sync"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	nsvec "github.com/bzync/nextsql/internal/vector"
)

// sqlIVF binds one IVF (inverted-file) vector index to its encrypted storage:
// the detached index tree holds the coarse-quantiser header, centroids, and
// front-coded posting lists; the shared vector store holds the full-precision
// column payloads. It implements nsvec.IVFStore so training-time persistence,
// row maintenance (AddIVF / RemoveIVF), and search (SearchIVF) all run against
// the same WAL/backup/PITR/Raft-durable trees as every other index.
type sqlIVF struct {
	itx     *btree.Txn // detached IVF index tree
	vtx     *btree.Txn // shared vector payload store
	col     uint16     // catalog ordinal of the vector column
	snap    txn.Snapshot
	useSnap bool
}

func (v *sqlIVF) lookup(tx *btree.Txn, key []byte) ([]byte, error) {
	if v != nil && v.useSnap {
		return tx.LookupAt(key, v.snap)
	}
	return tx.Lookup(key)
}

func (v *sqlIVF) store(tx *btree.Txn, k, val []byte) error {
	if v != nil && v.useSnap {
		_, err := tx.LookupAt(k, v.snap)
		if err != nil {
			if nerr.HasCode(err, nerr.NotFound) {
				return tx.InsertAt(k, val, v.snap)
			}
			return err
		}
		return tx.UpdateAt(k, val, v.snap)
	}
	return upsert(tx, k, val)
}

func (v *sqlIVF) delete(tx *btree.Txn, k []byte) error {
	err := tx.Delete(k)
	if err != nil && nerr.HasCode(err, nerr.NotFound) {
		return nil
	}
	return err
}

func (v *sqlIVF) LoadIVFMeta() (nsvec.IVFMeta, error) {
	raw, err := v.lookup(v.itx, nsvec.IVFMetaKey())
	if err != nil {
		return nsvec.IVFMeta{}, err
	}
	return nsvec.DecodeIVFMeta(raw)
}

func (v *sqlIVF) SaveIVFMeta(m nsvec.IVFMeta) error {
	raw, err := nsvec.EncodeIVFMeta(m)
	if err != nil {
		return err
	}
	return v.store(v.itx, nsvec.IVFMetaKey(), raw)
}

// ivfCentGroupMagic marks the bare centroids record as a group index rather than
// a centroid block: "IVFCG" + version(1) + u32(groupCount). A wide coarse
// quantiser (many LISTS, high dimension) does not fit in one 16 KiB B+Tree
// record, so SaveCentroids splits the centroid set into groups that each encode
// under the record ceiling and writes them at IVFCentroidChunkKey(i).
const (
	ivfCentGroupMagic   = "IVFCG"
	ivfCentGroupVersion = 1
	// ivfCentGroupBudget is the per-group encoded-byte target. A B+Tree leaf must
	// hold at least two records, so the real ceiling is roughly half a logical
	// page; this stays comfortably under it with room for the group header.
	ivfCentGroupBudget = 7000
)

func (v *sqlIVF) LoadCentroids() ([][]float32, error) {
	raw, err := v.lookup(v.itx, nsvec.IVFCentroidsKey())
	if err != nil {
		return nil, err
	}
	if !bytes.HasPrefix(raw, []byte(ivfCentGroupMagic)) {
		// Legacy single-record layout (index built before centroid grouping).
		return nsvec.DecodeCentroids(raw)
	}
	if len(raw) != len(ivfCentGroupMagic)+1+4 || raw[len(ivfCentGroupMagic)] != ivfCentGroupVersion {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVF.LoadCentroids", "bad IVF centroid group header")
	}
	n := int(binary.BigEndian.Uint32(raw[len(ivfCentGroupMagic)+1:]))
	if n <= 0 || n > nsvec.MaxIVFLists {
		return nil, nerr.New(nerr.InvalidFormat, "executor.sqlIVF.LoadCentroids", "bad IVF centroid group count")
	}
	var out [][]float32
	for i := 0; i < n; i++ {
		craw, err := v.lookup(v.itx, nsvec.IVFCentroidChunkKey(uint32(i)))
		if err != nil {
			return nil, err
		}
		group, err := nsvec.DecodeCentroids(craw)
		if err != nil {
			return nil, err
		}
		out = append(out, group...)
	}
	return out, nil
}

func (v *sqlIVF) SaveCentroids(c [][]float32) error {
	if len(c) == 0 || len(c[0]) == 0 {
		return nerr.New(nerr.InvalidArgument, "executor.sqlIVF.SaveCentroids", "empty IVF centroids")
	}
	dim := len(c[0])
	perGroup := ivfCentGroupBudget / (4 * dim)
	if perGroup < 1 {
		perGroup = 1
	}
	var groups int
	for start := 0; start < len(c); start += perGroup {
		end := start + perGroup
		if end > len(c) {
			end = len(c)
		}
		raw, err := nsvec.EncodeCentroids(c[start:end], dim)
		if err != nil {
			return err
		}
		if err := v.store(v.itx, nsvec.IVFCentroidChunkKey(uint32(groups)), raw); err != nil {
			return err
		}
		groups++
	}
	hdr := make([]byte, len(ivfCentGroupMagic)+1+4)
	copy(hdr, ivfCentGroupMagic)
	hdr[len(ivfCentGroupMagic)] = ivfCentGroupVersion
	binary.BigEndian.PutUint32(hdr[len(ivfCentGroupMagic)+1:], uint32(groups))
	return v.store(v.itx, nsvec.IVFCentroidsKey(), hdr)
}

func (v *sqlIVF) ListPKs(list int) ([][]byte, error) {
	if list < 0 || list > int(^uint32(0)) {
		return nil, nerr.New(nerr.InvalidArgument, "executor.sqlIVF.ListPKs", "IVF list out of range")
	}
	raw, err := v.lookup(v.itx, nsvec.IVFPostingKey(uint32(list)))
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	return nsvec.DecodeIVFList(raw)
}

func (v *sqlIVF) AddToList(list int, pk []byte) error {
	pks, err := v.ListPKs(list)
	if err != nil {
		return err
	}
	for _, ex := range pks {
		if string(ex) == string(pk) {
			return nil
		}
	}
	pks = append(pks, append([]byte(nil), pk...))
	return v.writeList(list, pks)
}

func (v *sqlIVF) RemoveFromList(list int, pk []byte) error {
	pks, err := v.ListPKs(list)
	if err != nil {
		return err
	}
	out := pks[:0]
	removed := false
	for _, ex := range pks {
		if string(ex) == string(pk) {
			removed = true
			continue
		}
		out = append(out, ex)
	}
	if !removed {
		return nil
	}
	return v.writeList(list, out)
}

func (v *sqlIVF) writeList(list int, pks [][]byte) error {
	if list < 0 || list > int(^uint32(0)) {
		return nerr.New(nerr.InvalidArgument, "executor.sqlIVF.writeList", "IVF list out of range")
	}
	key := nsvec.IVFPostingKey(uint32(list))
	if len(pks) == 0 {
		return v.delete(v.itx, key)
	}
	raw, err := nsvec.EncodeIVFList(pks)
	if err != nil {
		return err
	}
	return v.store(v.itx, key, raw)
}

func (v *sqlIVF) LoadVec(pk []byte) ([]float32, error) {
	raw, err := v.lookup(v.vtx, nsvec.PayloadKey(v.col, pk))
	if err != nil {
		return nil, err
	}
	return nsvec.DecodePayload(raw)
}

// lockedIVF is a process-local committed IVF index used for search: it holds the
// centroids, posting lists, and full-precision vectors in memory so a NEAREST
// query does not reload and decrypt the coarse quantiser from the index tree
// every time. It is installed at commit (fresh build) or lazily on first search,
// and evicted whenever the index is mutated, rebuilt, dropped, or replaced by a
// replicated apply — the same generation/lock the HNSW lockedMem uses.
type lockedIVF struct {
	mu  sync.RWMutex
	mem *nsvec.IVFMem
	gen uint64
}

func (m *lockedIVF) LoadIVFMeta() (nsvec.IVFMeta, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadIVFMeta()
}

func (m *lockedIVF) SaveIVFMeta(meta nsvec.IVFMeta) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.SaveIVFMeta(meta)
}

func (m *lockedIVF) LoadCentroids() ([][]float32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadCentroids()
}

func (m *lockedIVF) SaveCentroids(c [][]float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.SaveCentroids(c)
}

func (m *lockedIVF) ListPKs(list int) ([][]byte, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.ListPKs(list)
}

func (m *lockedIVF) AddToList(list int, pk []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.AddToList(list, pk)
}

func (m *lockedIVF) RemoveFromList(list int, pk []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mem.RemoveFromList(list, pk)
}

func (m *lockedIVF) LoadVec(pk []byte) ([]float32, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.mem.LoadVec(pk)
}

// ivfSearchStore returns the IVFStore a NEAREST query should read: the
// process-local committed copy when this session has not modified the index in
// the open transaction, otherwise the transaction-scoped disk-backed store so
// uncommitted changes are visible. A missing on-disk index (never built) yields
// the disk-backed store, which reports NotFound to the caller.
func (s *Session) ivfSearchStore(tab *catalog.Table, idx catalog.Index) (nsvec.IVFStore, error) {
	if s.dirtyIVF {
		return s.ivfStoreOf(tab, idx)
	}
	key := idxKey(tab.Name, idx.Name)
	gen := uint64(0)
	if s.db != nil {
		gen = s.db.hnswGeneration()
		if m := s.db.getIVF(key); m != nil && m.gen == gen {
			return m, nil
		}
	}
	st, err := s.ivfStoreOf(tab, idx)
	if err != nil {
		return nil, err
	}
	mem, err := nsvec.LoadIVFMem(st)
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return st, nil
		}
		return nil, err
	}
	locked := &lockedIVF{mem: mem, gen: gen}
	if s.db != nil {
		s.db.setIVF(key, locked)
	}
	return locked, nil
}

// ivfStoreOf binds the IVF store for a non-partitioned table's vector index.
func (s *Session) ivfStoreOf(tab *catalog.Table, idx catalog.Index) (*sqlIVF, error) {
	if len(idx.Columns) != 1 {
		return nil, nerr.New(nerr.Internal, "executor.ivfStoreOf", "VECTOR INDEX column count")
	}
	ix, err := s.indexOf(tab, idx)
	if err != nil {
		return nil, err
	}
	vs, err := s.vecOf(tab)
	if err != nil {
		return nil, err
	}
	st := &sqlIVF{itx: s.x.use(ix), vtx: s.x.use(vs), col: uint16(idx.Columns[0])}
	if snap, ok, err := s.fkWriteSnap(); err != nil {
		return nil, err
	} else if ok {
		st.snap = snap
		st.useSnap = true
	}
	return st, nil
}

// buildIVFIndex trains a coarse quantiser over the table heap and writes the
// centroids, posting lists, and header into the fresh detached index tree.
// Shared by CREATE VECTOR INDEX ... USING IVF and REBUILD INDEX.
func (s *Session) buildIVFIndex(tab *catalog.Table, idx catalog.Index, htx *btree.Txn, progress *rebuildProgress) error {
	if len(idx.Columns) != 1 {
		return nerr.New(nerr.InvalidArgument, "executor.buildIVFIndex", "VECTOR INDEX column count")
	}
	col := idx.Columns[0]
	dim := tab.Columns[col].Type.Precision
	metric := graphMetric(tab.Columns[col].Type)

	type row struct {
		pk  []byte
		vec []float32
	}
	var rows []row
	if err := htx.Range(nil, nil, func(_, val []byte) error {
		if err := s.budget().Check(); err != nil {
			return err
		}
		r, err := s.decodeHeapRow(tab, val)
		if err != nil {
			return err
		}
		v := r[col]
		if v.Null {
			progress.add(1, 0)
			return nil
		}
		pk, err := types.EncodeKey(tab.PKValues(r))
		if err != nil {
			return err
		}
		rows = append(rows, row{pk: pk, vec: append([]float32(nil), v.Vec...)})
		progress.add(1, 1)
		return nil
	}); err != nil {
		return err
	}

	st, err := s.ivfStoreOf(tab, idx)
	if err != nil {
		return err
	}

	nlist := idx.IVFLists
	meta := nsvec.DefaultIVFMeta(dim, metric, nlist)
	if idx.IVFProbes > 0 {
		meta.NProbe = idx.IVFProbes
		if meta.NProbe > meta.NList {
			meta.NProbe = meta.NList
		}
	}

	if len(rows) == 0 {
		// No vectors yet: persist a trained-but-empty quantiser over a single
		// zero centroid so search and later inserts have a valid header.
		meta.NList = 1
		meta.NProbe = 1
		meta.Trained = true
		meta.Count = 0
		if err := st.SaveCentroids([][]float32{make([]float32, int(dim))}); err != nil {
			return err
		}
		return st.SaveIVFMeta(meta)
	}

	// Train on a deterministic sample of the column so very large tables do not
	// hold every vector twice; TrainIVF clamps NList to the sample size.
	const maxTrainSample = 50000
	var sample [][]float32
	if len(rows) <= maxTrainSample {
		sample = make([][]float32, len(rows))
		for i, r := range rows {
			sample[i] = r.vec
		}
	} else {
		stride := len(rows) / maxTrainSample
		if stride < 1 {
			stride = 1
		}
		for i := 0; i < len(rows) && len(sample) < maxTrainSample; i += stride {
			sample = append(sample, rows[i].vec)
		}
	}
	mem, err := nsvec.TrainIVF(meta, sample)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err := s.budget().Check(); err != nil {
			return err
		}
		mem.PutVec(r.pk, r.vec)
		if err := nsvec.AddIVF(mem, r.pk, r.vec); err != nil {
			return err
		}
	}

	if err := st.SaveCentroids(mem.Centroids); err != nil {
		return err
	}
	for l, list := range mem.Lists {
		if len(list) == 0 {
			continue
		}
		if err := st.writeList(l, list); err != nil {
			return err
		}
	}
	if err := st.SaveIVFMeta(mem.Meta); err != nil {
		return err
	}
	// Hand the freshly trained in-memory index to the process-local cache so the
	// first NEAREST after commit does not reload it from the index tree. The
	// commit path keys it by index name and stamps the current generation.
	if s.pendingIVF == nil {
		s.pendingIVF = make(map[string]*lockedIVF)
	}
	s.pendingIVF[idxKey(tab.Name, idx.Name)] = &lockedIVF{mem: mem}
	return nil
}

// maintainIVFIndex applies one row change to a non-partitioned IVF index. The
// payload has already been written to the vector store by putVectors.
func (s *Session) maintainIVFIndex(tab *catalog.Table, idx catalog.Index, old, neu []types.Value) error {
	s.dirtyIVF = true
	st, err := s.ivfStoreOf(tab, idx)
	if err != nil {
		return err
	}
	if old != nil {
		pk, err := types.EncodeKey(tab.PKValues(old))
		if err != nil {
			return err
		}
		if _, err := nsvec.RemoveIVF(st, pk); err != nil {
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
		return nsvec.AddIVF(st, pk, neu[col].Vec)
	}
	return nil
}

// nearestIVFIndex answers a NEAREST query through an IVF index: rank the
// centroids, probe the nearest posting lists, and score their vectors exactly.
// A USING metric that differs from the trained one falls back to exact flat.
func (s *Session) nearestIVFIndex(n planner.Nearest, q []float32, metric nsvec.Metric, tab *catalog.Table, idx catalog.Index) ([][]types.Value, error) {
	st, err := s.ivfSearchStore(tab, idx)
	if err != nil {
		return nil, err
	}
	meta, err := st.LoadIVFMeta()
	if err != nil {
		if nerr.HasCode(err, nerr.NotFound) {
			return nil, nil
		}
		return nil, err
	}
	if n.Metric != "" && meta.Metric != metric {
		return s.nearestFlat(n, q, metric)
	}
	if !meta.Trained || meta.Count == 0 {
		return nil, nil
	}
	k := int(n.K)
	if k < 1 {
		k = int(meta.Count)
	}
	if k < 1 {
		return nil, nil
	}
	if n.Residual != nil && uint64(k) < meta.Count {
		over := k * 4
		if over < k {
			over = k
		}
		if uint64(over) > meta.Count {
			over = int(meta.Count)
		}
		k = over
	}
	hits, err := nsvec.SearchIVF(st, q, k, int(idx.IVFProbes), s.workers())
	if err != nil {
		return nil, err
	}
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
		if err != nil {
			return nil, err
		}
		if row == nil {
			continue
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
