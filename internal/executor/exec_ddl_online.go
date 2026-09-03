package executor

import (
	"context"
	"time"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/btree"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

// onlineBuild tracks one in-progress REBUILD INDEX ... ONLINE. While it is
// registered, every DML transaction that maintains the real index also
// mirrors the same change into shadow (see maintainIndexes / mirrorOnlineIndex),
// so a concurrent write is never lost from the index being rebuilt. The
// rebuild's own backfill walks the heap under per-row key locks and populates
// shadow for rows that existed before the mirror was armed. A short final
// transaction swaps the catalog to point at shadow and queues the old tree for
// reclamation on the normal post-commit drain.
type onlineBuild struct {
	table   string
	index   string
	shadow  *btree.Tree // nil until the shadow tree is created and armed
	swapped bool        // catalog now points at shadow; safe to disarm after drain
}

// onlineShadow returns the shadow tree for an armed online rebuild of the
// named index, or nil when none is in progress.
func (db *DB) onlineShadow(key string) *btree.Tree {
	if db == nil {
		return nil
	}
	db.onlineMu.RLock()
	defer db.onlineMu.RUnlock()
	if ob := db.onlineBuilds[key]; ob != nil {
		return ob.shadow
	}
	return nil
}

// onlineBuildActive reports whether an online rebuild is registered for the
// named index (armed or not). DROP/ALTER/blocking-REBUILD guards use it.
func (db *DB) onlineBuildActive(key string) bool {
	if db == nil {
		return false
	}
	db.onlineMu.RLock()
	defer db.onlineMu.RUnlock()
	return db.onlineBuilds[key] != nil
}

// armOnlineBuild registers a new online rebuild. It returns false when one is
// already in progress for the same index.
func (db *DB) armOnlineBuild(key, table, index string) bool {
	db.onlineMu.Lock()
	defer db.onlineMu.Unlock()
	if db.onlineBuilds[key] != nil {
		return false
	}
	db.onlineBuilds[key] = &onlineBuild{table: table, index: index}
	if db.Eng != nil && db.Eng.TM != nil {
		db.Eng.TM.BeginOnlineBuild()
	}
	return true
}

func (db *DB) getOnlineBuild(key string) *onlineBuild {
	db.onlineMu.RLock()
	defer db.onlineMu.RUnlock()
	return db.onlineBuilds[key]
}

// abortOnlineBuild removes a registration whose swap never completed and
// queues the shadow tree's pages for reclamation on the next drain.
func (db *DB) abortOnlineBuild(key string) {
	db.onlineMu.Lock()
	ob := db.onlineBuilds[key]
	delete(db.onlineBuilds, key)
	db.onlineMu.Unlock()
	if ob == nil {
		return
	}
	if db.Eng != nil && db.Eng.TM != nil {
		db.Eng.TM.EndOnlineBuild()
	}
	if ob.shadow != nil {
		if pages, err := ob.shadow.OwnedPages(); err == nil && len(pages) > 0 {
			db.queueCommittedReclaims(pages, nil, nil)
		}
	}
}

// markOnlineBuildSwapped records that the catalog now points at shadow. The
// registration is not removed yet: an in-flight transaction may still resolve
// the pre-swap tree and must keep mirroring until it drains. disarmSwapped is
// called under applyMu by drainCommittedReclaims once every such transaction
// has finished.
func (db *DB) markOnlineBuildSwapped(key string) {
	db.onlineMu.Lock()
	if ob := db.onlineBuilds[key]; ob != nil {
		ob.swapped = true
	}
	db.onlineMu.Unlock()
}

// disarmSwappedOnlineBuilds removes registrations whose swap has completed.
// The caller holds applyMu exclusively, so no transaction can still be between
// resolving the real tree and resolving the shadow.
func (db *DB) disarmSwappedOnlineBuilds() {
	db.onlineMu.Lock()
	var done int
	for key, ob := range db.onlineBuilds {
		if ob.swapped {
			delete(db.onlineBuilds, key)
			done++
		}
	}
	db.onlineMu.Unlock()
	for i := 0; i < done; i++ {
		if db.Eng != nil && db.Eng.TM != nil {
			db.Eng.TM.EndOnlineBuild()
		}
	}
}

// anyOnlineBuildForTable reports whether an online rebuild is registered for
// any index on the named table. Conflicting DDL (DROP TABLE / DROP INDEX /
// ALTER TABLE / blocking REBUILD) rejects while one is in progress.
func (db *DB) anyOnlineBuildForTable(table string) bool {
	if db == nil {
		return false
	}
	db.onlineMu.RLock()
	defer db.onlineMu.RUnlock()
	for _, ob := range db.onlineBuilds {
		if ob.table == table {
			return true
		}
	}
	return false
}

func (db *DB) hasSwappedOnlineBuilds() bool {
	db.onlineMu.RLock()
	defer db.onlineMu.RUnlock()
	for _, ob := range db.onlineBuilds {
		if ob.swapped {
			return true
		}
	}
	return false
}

// mirrorOnlineIndex applies the same (old -> neu) row change to shadow that
// maintainIndexes just applied to the real index. It uses a freshly captured
// snapshot so a backfill row that committed after this transaction began does
// not trip a spurious write-write conflict; the per-row heap key lock held by
// both this writer and the backfill keeps the two from racing on one row.
func (s *Session) mirrorOnlineIndex(tab *catalog.Table, idx catalog.Index, shadow *btree.Tree, old, neu []types.Value) error {
	h, tm, err := s.fkTM()
	if err != nil {
		return err
	}
	snap := tm.Capture(h.ID)
	shtx := s.x.use(shadow)
	if old != nil {
		if ok, err := s.indexRowMatches(tab, idx, old); err != nil {
			return err
		} else if ok {
			pairs, err := s.indexPairs(tab, idx, old)
			if err != nil {
				return err
			}
			for _, pkv := range pairs {
				if derr := shtx.DeleteAt(pkv.k, snap); derr != nil && !nerr.HasCode(derr, nerr.NotFound) {
					return derr
				}
			}
		}
	}
	if neu != nil {
		if ok, err := s.indexRowMatches(tab, idx, neu); err != nil {
			return err
		} else if ok {
			pairs, err := s.indexPairs(tab, idx, neu)
			if err != nil {
				return err
			}
			for _, pkv := range pairs {
				if ierr := shtx.InsertAt(pkv.k, pkv.v, snap); ierr != nil && !nerr.HasCode(ierr, nerr.AlreadyExists) {
					return ierr
				}
			}
		}
	}
	return nil
}

const (
	onlineBackfillChunk = 512
	onlineChunkRetries  = 8
)

// rebuildIndexOnline rebuilds one secondary index without blocking concurrent
// writes. It is intercepted before the normal plan/run path (like MAINTAIN)
// and manages its own short transactions. Scope: non-partitioned tables and
// b-tree / UNIQUE / JSON-path / spatial indexes. Vector, full-text and
// partitioned indexes fall back to the blocking REBUILD INDEX.
func (s *Session) rebuildIndexOnline(ctx context.Context, table, index string) (res *Result, err error) {
	// This routine drives its own transaction cycles; the outer autocommit
	// guard must not stay held across them or the post-commit reclaim drain
	// (which needs applyMu exclusively) would deadlock.
	s.releaseTxnGuard()

	tab, ok := s.db.Cat.Get(table)
	if !ok {
		return nil, nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown table")
	}
	pos := -1
	var idx catalog.Index
	for i, ix := range tab.Indexes {
		if ix.Name == index {
			pos, idx = i, ix
			break
		}
	}
	if pos < 0 {
		return nil, nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown index")
	}
	if tab.Partitioning != nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.RebuildIndex", "ONLINE rebuild is not supported for partitioned tables; use REBUILD INDEX without ONLINE")
	}
	if idx.Vector {
		return nil, nerr.New(nerr.InvalidArgument, "executor.RebuildIndex", "ONLINE rebuild is not supported for vector indexes; use REBUILD INDEX without ONLINE")
	}
	if idx.Fulltext {
		return nil, nerr.New(nerr.InvalidArgument, "executor.RebuildIndex", "ONLINE rebuild is not supported for full-text indexes; use REBUILD INDEX without ONLINE")
	}

	key := idxKey(table, index)
	if !s.db.armOnlineBuild(key, table, index) {
		return nil, nerr.New(nerr.Unavailable, "executor.RebuildIndex", "an online rebuild is already in progress for this index")
	}
	ob := s.db.getOnlineBuild(key)
	progress := s.db.beginIndexRebuild(table, index)
	started := time.Now()
	swapped := false
	defer func() {
		s.db.finishIndexRebuild(progress)
		if s.db.metrics != nil {
			s.db.metrics.ObserveIndexRebuild(progress.rows.Load(), progress.entries.Load(), time.Since(started), err)
		}
		if !swapped {
			s.db.abortOnlineBuild(key)
		}
	}()

	// 1. Create the shadow tree in its own committed transaction so its meta
	//    page is durably allocated, then arm the mirror by publishing it.
	progress.phase.Store("arming")
	shadow, err := s.createOnlineShadowTree()
	if err != nil {
		return nil, err
	}
	s.db.onlineMu.Lock()
	ob.shadow = shadow
	s.db.onlineMu.Unlock()

	// 2. Drain every write transaction that was in flight when the mirror was
	//    armed: a change it makes after this point mirrors, and the backfill
	//    snapshot (taken next) sees everything it committed.
	progress.phase.Store("draining")
	if s.db.Eng != nil && s.db.Eng.TM != nil {
		for _, id := range s.db.Eng.TM.ActiveWriterIDs() {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			s.db.Eng.TM.WaitDone(id)
		}
	}

	// 3. Backfill: walk the heap in key order, locking each row so it cannot
	//    change under us, and insert its current index entries into shadow.
	progress.phase.Store("backfilling")
	if err := s.backfillOnlineIndex(ctx, tab, idx, shadow, progress); err != nil {
		return nil, err
	}

	// 4. Swap the catalog to point at shadow and queue the old tree for
	//    reclamation. The mirror stays armed until the post-commit drain so an
	//    in-flight writer that still holds the old tree keeps shadow current.
	progress.phase.Store("committing")
	if err := s.swapOnlineIndex(table, index, pos, shadow); err != nil {
		return nil, err
	}
	s.db.markOnlineBuildSwapped(key)
	swapped = true

	// 5. Force the drain now so the old tree is gone and the mirror disarmed
	//    before we report success.
	s.db.drainCommittedReclaims()
	return &Result{}, nil
}

// createOnlineShadowTree allocates a detached b-tree in a committed
// transaction and returns it.
func (s *Session) createOnlineShadowTree() (*btree.Tree, error) {
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return nil, err
	}
	s.db.Eng.Enter(s.x.owner.Storage())
	shadow, cerr := btree.CreateDetached(s.db.Eng)
	s.db.Eng.Leave(s.x.owner.Storage())
	if cerr != nil {
		_ = s.abort()
		return nil, cerr
	}
	// Touch the tree inside this transaction so its meta page is flushed on
	// commit even though it holds no rows yet.
	s.x.use(shadow)
	if _, err := s.commit(); err != nil {
		return nil, err
	}
	return shadow, nil
}

// backfillOnlineIndex populates shadow with the index entries for every heap
// row, processing the heap in bounded key-order chunks. Each row is locked for
// the duration of its probe+insert so a concurrent writer's mirror cannot race
// it; a deleted row is skipped (its mirror removes any stale entry).
func (s *Session) backfillOnlineIndex(ctx context.Context, tab *catalog.Table, idx catalog.Index, shadow *btree.Tree, progress *rebuildProgress) error {
	heap, err := s.db.heap(tab.Name)
	if err != nil {
		return err
	}
	var resume []byte
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, next, cerr := s.backfillOnlineChunk(tab, idx, heap, shadow, resume, progress)
		if cerr != nil {
			return cerr
		}
		if done {
			return nil
		}
		resume = next
	}
}

// backfillOnlineChunk processes up to onlineBackfillChunk heap keys after
// resume (exclusive), retrying only a lock-ordering deadlock (rare, since the
// chunk takes its heap key locks in ascending order like every DML writer).
func (s *Session) backfillOnlineChunk(tab *catalog.Table, idx catalog.Index, heap, shadow *btree.Tree, resume []byte, progress *rebuildProgress) (done bool, next []byte, err error) {
	for attempt := 0; ; attempt++ {
		done, next, err = s.backfillOnlineChunkOnce(tab, idx, heap, shadow, resume, progress)
		if err == nil {
			return done, next, nil
		}
		if attempt >= onlineChunkRetries || !nerr.HasCode(err, nerr.Deadlock) {
			return false, nil, err
		}
	}
}

func (s *Session) backfillOnlineChunkOnce(tab *catalog.Table, idx catalog.Index, heap, shadow *btree.Tree, resume []byte, progress *rebuildProgress) (done bool, next []byte, err error) {
	if serr := s.start(txn.SnapshotIsolation); serr != nil {
		return false, nil, serr
	}
	defer func() {
		if err != nil {
			_ = s.abort()
		}
	}()
	if cerr := s.db.Eng.CrashAt(wal.PointDuringIndexBuild); cerr != nil {
		return false, nil, cerr
	}

	htx := s.x.use(heap)
	shtx := s.x.use(shadow)

	// Collect this chunk's candidate keys without doing per-key work inside
	// the range iteration (which holds the tree read lock). RangeVisible
	// yields them in ascending key order.
	keys := make([][]byte, 0, onlineBackfillChunk)
	rerr := htx.RangeVisible(resume, nil, func(k, _ []byte) error {
		if len(resume) > 0 && string(k) == string(resume) {
			return nil
		}
		keys = append(keys, append([]byte(nil), k...))
		if len(keys) >= onlineBackfillChunk {
			return errStopBackfillChunk
		}
		return nil
	})
	if rerr != nil && rerr != errStopBackfillChunk {
		return false, nil, rerr
	}
	if len(keys) == 0 {
		if _, cerr := s.commit(); cerr != nil {
			return false, nil, cerr
		}
		return true, nil, nil
	}

	h, tm, terr := s.fkTM()
	if terr != nil {
		return false, nil, terr
	}

	// Take every heap row lock for the chunk up front, in ascending key order.
	// DML writers lock the heap row before touching any index tree, so this
	// ordering means a concurrent writer of a row in this chunk simply waits
	// here rather than deadlocking against the shadow-key locks taken below.
	for _, k := range keys {
		if lerr := htx.LockExclusive(k); lerr != nil {
			return false, nil, lerr
		}
	}

	for _, k := range keys {
		snap := tm.Capture(h.ID)
		payload, lerr := htx.LookupAt(k, snap)
		if lerr != nil {
			if nerr.HasCode(lerr, nerr.NotFound) {
				continue // row deleted since the scan; its mirror handles removal
			}
			return false, nil, lerr
		}
		row, derr := s.decodeHeapRow(tab, payload)
		if derr != nil {
			return false, nil, derr
		}
		matches, merr := s.indexRowMatches(tab, idx, row)
		if merr != nil {
			return false, nil, merr
		}
		if !matches {
			progress.add(1, 0)
			continue
		}
		pairs, perr := s.indexPairs(tab, idx, row)
		if perr != nil {
			return false, nil, perr
		}
		for _, pkv := range pairs {
			if ierr := shtx.InsertAt(pkv.k, pkv.v, snap); ierr != nil && !nerr.HasCode(ierr, nerr.AlreadyExists) {
				return false, nil, ierr
			}
		}
		progress.add(1, int64(len(pairs)))
	}

	last := keys[len(keys)-1]
	if _, cerr := s.commit(); cerr != nil {
		return false, nil, cerr
	}
	return false, last, nil
}

var errStopBackfillChunk = nerr.New(nerr.Internal, "executor.RebuildIndex", "chunk full")

// swapOnlineIndex points the catalog index entry at shadow and queues the old
// tree for reclamation, reusing the same pending machinery as the blocking
// rebuild.
func (s *Session) swapOnlineIndex(table, index string, pos int, shadow *btree.Tree) error {
	if err := s.start(txn.SnapshotIsolation); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			_ = s.abort()
		}
	}()
	tab, ok := s.lookup(table)
	if !ok {
		return nerr.New(nerr.NotFound, "executor.RebuildIndex", "unknown table")
	}
	if pos >= len(tab.Indexes) || tab.Indexes[pos].Name != index {
		return nerr.New(nerr.Corruption, "executor.RebuildIndex", "index moved during online rebuild")
	}
	idx := tab.Indexes[pos]
	old, err := s.indexOf(tab, idx)
	if err != nil {
		return err
	}
	if err := s.queueTreeReclaim(old); err != nil {
		return err
	}
	s.pending.indexDrops = append(s.pending.indexDrops, indexMapDrop{key: idxKey(table, index), tree: old})
	s.pending.idxs[idxKey(table, index)] = shadow
	neu := tab.Clone()
	neu.Indexes[pos].Meta = shadow.Meta()
	if err := s.putCatalog(neu, tab.Name); err != nil {
		return err
	}
	if _, err := s.commit(); err != nil {
		return err
	}
	committed = true
	return nil
}
