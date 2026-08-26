package replication

import (
	"io"
	"sync"

	"github.com/hashicorp/raft"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

// Applier installs a committed WAL batch on a replica.
type Applier interface {
	ApplyRecords(recs []wal.Record) error
}

type fsm struct {
	mu       sync.Mutex
	keys     crypto.KeyProvider
	applier  Applier
	lastLSN  format.LSN
	localLSN format.LSN
}

func newFSM(keys crypto.KeyProvider, applier Applier) *fsm {
	return &fsm{keys: keys, applier: applier}
}

func (f *fsm) markLocal(lsn format.LSN) {
	f.mu.Lock()
	if lsn > f.localLSN {
		f.localLSN = lsn
	}
	if lsn > f.lastLSN {
		f.lastLSN = lsn
	}
	f.mu.Unlock()
}

func (f *fsm) LastLSN() format.LSN {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastLSN
}

func (f *fsm) Apply(l *raft.Log) interface{} {
	if l == nil || l.Type != raft.LogCommand {
		return nil
	}
	recs, err := DecodeCommand(f.keys, l.Data)
	if err != nil {
		return err
	}
	if len(recs) == 0 {
		return nerr.New(nerr.InvalidFormat, "replication.FSM.Apply", "empty batch")
	}
	last := recs[len(recs)-1].LSN
	f.mu.Lock()
	skip := last <= f.localLSN || last <= f.lastLSN
	f.mu.Unlock()
	if skip {
		return nil
	}
	if f.applier != nil {
		if err := f.applier.ApplyRecords(recs); err != nil {
			return err
		}
	}
	f.mu.Lock()
	if last > f.lastLSN {
		f.lastLSN = last
	}
	f.mu.Unlock()
	return nil
}

func (f *fsm) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fsmSnapshot{lsn: uint64(f.lastLSN)}, nil
}

func (f *fsm) Restore(rc io.ReadCloser) error {
	defer rc.Close()
	buf, err := io.ReadAll(rc)
	if err != nil {
		return nerr.Wrap(nerr.IO, "replication.FSM.Restore", "read", err)
	}
	if len(buf) < 10 {
		return nerr.New(nerr.InvalidFormat, "replication.FSM.Restore", "truncated snapshot")
	}
	if encoding.U32(buf, 0) != Magic {
		return nerr.New(nerr.InvalidFormat, "replication.FSM.Restore", "bad snapshot magic")
	}
	if encoding.U16(buf, 4) != CurrentVersion {
		return nerr.New(nerr.InvalidFormat, "replication.FSM.Restore", "unsupported snapshot version")
	}
	lsn := format.LSN(encoding.U64(buf, 6))
	f.mu.Lock()
	f.lastLSN = lsn
	f.mu.Unlock()
	return nil
}

type fsmSnapshot struct {
	lsn uint64
}

func (s *fsmSnapshot) Persist(sink raft.SnapshotSink) error {
	buf := make([]byte, 14)
	encoding.PutU32(buf, 0, Magic)
	encoding.PutU16(buf, 4, CurrentVersion)
	encoding.PutU64(buf, 6, s.lsn)
	if _, err := sink.Write(buf); err != nil {
		_ = sink.Cancel()
		return nerr.Wrap(nerr.IO, "replication.snapshot", "write", err)
	}
	return sink.Close()
}

func (s *fsmSnapshot) Release() {}
