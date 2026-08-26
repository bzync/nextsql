package btree

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/txn"
	"github.com/bzync/nextsql/internal/wal"
)

// Update replaces the clustered row stored under an existing key.
func (t *Tree) Update(key, value []byte) error {
	if t == nil {
		return nerr.New(nerr.InvalidArgument, "btree.Update", "nil tree")
	}
	if _, err := encodeLeaf(key, value); err != nil {
		return err
	}
	key = copyBytes(key)
	value = copyBytes(value)

	tx, err := t.BeginTxn(txn.SnapshotIsolation)
	if err != nil {
		return err
	}
	if err := tx.Update(key, value); err != nil {
		if !wal.IsCrash(err) {
			_ = tx.Rollback()
		}
		return err
	}
	return tx.Commit()
}

func (t *Tree) updateLocked(key, value []byte) error {
	path, err := t.descend(key)
	if err != nil {
		return err
	}
	leafID := path[len(path)-1]
	h, err := t.pin(leafID)
	if err != nil {
		return err
	}
	if err := expectType(h.Page(), format.PageTypeBTreeLeaf); err != nil {
		_ = release(h, false)
		return err
	}
	slot, _, found, err := findLeafSlot(h.Page(), key)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if !found {
		_ = release(h, false)
		return nerr.New(nerr.NotFound, "btree.Update", "key not found")
	}
	rec, err := encodeLeaf(key, value)
	if err != nil {
		_ = release(h, false)
		return err
	}
	if err := h.Page().Update(slot, rec); err == nil {
		return release(h, true)
	} else if !nerr.HasCode(err, nerr.PageFull) {
		_ = release(h, false)
		return err
	}
	hdr, err := readHeader(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	ents, err := collectLeaves(h.Page())
	if err != nil {
		_ = release(h, false)
		return err
	}
	i, ok := findLeaf(ents, key)
	if !ok {
		_ = release(h, false)
		return nerr.New(nerr.NotFound, "btree.Update", "key not found")
	}
	ents = append(ents[:i], ents[i+1:]...)
	if err := release(h, false); err != nil {
		return err
	}
	return t.splitLeafAndInsert(path, hdr, ents, leafEntry{key: key, value: value})
}
