package storage

import (
	"container/list"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

// Page delta bases.
//
// A commit logs the state of every page it dirtied. With page deltas enabled
// it logs a RecPageDelta against the page's previous logged state instead of
// a full image whenever that is provably safe, which is what this cache is for:
// it holds, per page, the most recent logged state -- the base a delta would be
// encoded against.
//
// A base may be used only when all of the following hold, checked under e.mu
// at append time:
//
//   - the record that logged it is the page's most recent record. The cache
//     entry is replaced on every append for the page, and the copied page's
//     own stamped LSN must equal the entry's LSN (a page copied before the
//     previous record was stamped onto its frame logs a full image);
//   - that record will be replayed by recovery: its transaction's commit
//     record is appended (and, for a replicated commit, released), or it is a
//     committed system transaction. Until then the entry is unsettled;
//   - it is at or after the redo boundary of the last checkpoint (the floor),
//     so recovery scans it;
//   - the page has not been changed without logging since. A rollback
//     changes pages it does not log, so it discards the entries for every
//     page it touched.
//
// Recovery independently refuses a delta whose page is not at its base LSN
// with its base digest, so a violated rule fails closed rather than silently
// producing a wrong page. The cache is bounded (LRU); an evicted page simply
// logs a full image next time.

// maxPageDeltaBody is the largest delta worth logging; beyond it a full image
// costs little more and needs no base.
const maxPageDeltaBody = format.LogicalPageSize / 2

type loggedPage struct {
	id      format.PageID
	lsn     format.LSN
	txn     format.TxnID
	settled bool
	content []byte // the logical page as logged, LSN stamped
	elem    *list.Element
}

type pageLogCache struct {
	byID  map[format.PageID]*loggedPage
	order *list.List // front = most recently logged
	cap   int
	floor format.LSN
}

func newPageLogCache(capacity int) *pageLogCache {
	if capacity < 16 {
		capacity = 16
	}
	return &pageLogCache{byID: make(map[format.PageID]*loggedPage), order: list.New(), cap: capacity}
}

// base returns the logged content to encode cur against, or nil.
func (c *pageLogCache) base(id format.PageID, cur []byte) *loggedPage {
	if c == nil {
		return nil
	}
	lp := c.byID[id]
	if lp == nil || !lp.settled || lp.lsn < c.floor {
		return nil
	}
	if format.LSN(encoding.U64(cur, wal.PageLSNOffset)) != lp.lsn {
		return nil
	}
	return lp
}

func (c *pageLogCache) put(id format.PageID, lsn format.LSN, txn format.TxnID, settled bool, content []byte) {
	if c == nil {
		return
	}
	if lp := c.byID[id]; lp != nil {
		lp.lsn, lp.txn, lp.settled, lp.content = lsn, txn, settled, content
		c.order.MoveToFront(lp.elem)
		return
	}
	lp := &loggedPage{id: id, lsn: lsn, txn: txn, settled: settled, content: content}
	lp.elem = c.order.PushFront(lp)
	c.byID[id] = lp
	for c.order.Len() > c.cap {
		old := c.order.Back().Value.(*loggedPage)
		c.order.Remove(old.elem)
		delete(c.byID, old.id)
	}
}

// settle marks txn's entries replayable once its commit is appended.
func (c *pageLogCache) settle(txn format.TxnID, ids []format.PageID) {
	if c == nil {
		return
	}
	for _, id := range ids {
		if lp := c.byID[id]; lp != nil && lp.txn == txn {
			lp.settled = true
		}
	}
}

func (c *pageLogCache) forget(id format.PageID) {
	if c == nil {
		return
	}
	if lp := c.byID[id]; lp != nil {
		c.order.Remove(lp.elem)
		delete(c.byID, id)
	}
}

// raiseFloor discards every base below a new redo boundary.
func (c *pageLogCache) raiseFloor(redo format.LSN) {
	if c == nil || redo <= c.floor {
		return
	}
	c.floor = redo
	for id, lp := range c.byID {
		if lp.lsn < redo {
			c.order.Remove(lp.elem)
			delete(c.byID, id)
		}
	}
}

func (c *pageLogCache) clear() {
	if c == nil {
		return
	}
	c.byID = make(map[format.PageID]*loggedPage)
	c.order.Init()
}
