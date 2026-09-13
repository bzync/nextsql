package executor

import (
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/storage"
)

// MaxSavepoints bounds the savepoint stack of one transaction. Each entry is a
// small value, but the stack is client-controlled, so it is bounded like every
// other per-session structure.
const MaxSavepoints = 64

// savepoint is one named position inside the open transaction.
type savepoint struct {
	name string
	mark storage.Savepoint
	// ddlSeq is the session's DDL counter when the savepoint was set. A
	// partial rollback reverses row writes through the undo chain, but the
	// session's catalog overlay — created and dropped trees, renamed tables,
	// workflow and trigger definitions — is not part of that chain, so
	// rolling back across a DDL statement would leave the two disagreeing.
	// Rather than half-reverting, ROLLBACK TO refuses.
	ddlSeq uint64
}

func (s *Session) savepointStorageTxn() (*storage.Txn, error) {
	if s.x == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.savepoint", "no active transaction")
	}
	if s.x.readOnly || s.x.owner == nil || s.x.owner.Storage() == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.savepoint", "savepoints require a write transaction")
	}
	return s.x.owner.Storage(), nil
}

// setSavepoint implements SAVEPOINT name. Re-using a live name replaces it, as
// in the standard: the older savepoint of that name is destroyed, along with
// every savepoint established after it.
func (s *Session) setSavepoint(name string) (*Result, error) {
	stx, err := s.savepointStorageTxn()
	if err != nil {
		return nil, err
	}
	if i := s.findSavepoint(name); i >= 0 {
		s.savepoints = s.savepoints[:i]
	}
	if len(s.savepoints) >= MaxSavepoints {
		return nil, nerr.New(nerr.Exhausted, "executor.savepoint", "too many savepoints in one transaction")
	}
	s.savepoints = append(s.savepoints, savepoint{name: name, mark: stx.Savepoint(), ddlSeq: s.ddlSeq})
	return &Result{}, nil
}

// rollbackToSavepoint implements ROLLBACK TO [SAVEPOINT] name: the transaction
// stays open and keeps its locks, and every savepoint established after this
// one is destroyed. The savepoint itself remains, so it can be rolled back to
// again.
func (s *Session) rollbackToSavepoint(name string) (*Result, error) {
	stx, err := s.savepointStorageTxn()
	if err != nil {
		return nil, err
	}
	i := s.findSavepoint(name)
	if i < 0 {
		return nil, nerr.New(nerr.NotFound, "executor.savepoint", "no such savepoint: "+name)
	}
	sp := s.savepoints[i]
	if sp.ddlSeq != s.ddlSeq {
		return nil, nerr.New(nerr.InvalidArgument, "executor.savepoint", "cannot roll back to a savepoint set before a schema change in the same transaction")
	}
	if s.fkBroken {
		return nil, nerr.New(nerr.Exhausted, "executor.savepoint", "foreign key cascade exceeded limit")
	}
	if err := s.db.Eng.RollbackToSavepoint(stx, sp.mark); err != nil {
		return nil, err
	}
	// Caches derived from rows this transaction wrote are no longer valid.
	s.dirtyHNSW = false
	s.pendingHNSW = nil
	s.dirtyIVF = false
	s.pendingIVF = nil
	s.savepoints = s.savepoints[:i+1]
	return &Result{}, nil
}

// releaseSavepoint implements RELEASE [SAVEPOINT] name: the savepoint and every
// one established after it are destroyed, and the work they cover is kept.
func (s *Session) releaseSavepoint(name string) (*Result, error) {
	if _, err := s.savepointStorageTxn(); err != nil {
		return nil, err
	}
	i := s.findSavepoint(name)
	if i < 0 {
		return nil, nerr.New(nerr.NotFound, "executor.savepoint", "no such savepoint: "+name)
	}
	s.savepoints = s.savepoints[:i]
	return &Result{}, nil
}

func (s *Session) findSavepoint(name string) int {
	for i := len(s.savepoints) - 1; i >= 0; i-- {
		if s.savepoints[i].name == name {
			return i
		}
	}
	return -1
}

// clearSavepoints drops the stack when the transaction ends.
func (s *Session) clearSavepoints() { s.savepoints = nil }

// execCancelQuery stops a statement another session is running. A user may
// always cancel their own; cancelling anyone else's requires ADMIN, the same
// boundary system.active_queries applies to seeing the statement at all.
// Cancellation is advisory in the sense that it asks the target statement's
// context to stop — the target unwinds through its own cleanup, exactly as it
// does for a client-driven cancel.
func (s *Session) execCancelQuery(id string) (*Result, error) {
	if s == nil || s.db == nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CancelQuery", "no database")
	}
	qid, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return nil, nerr.New(nerr.InvalidArgument, "executor.CancelQuery", "invalid query id")
	}
	admin := s.acl == nil || s.isAdmin()
	for _, target := range s.db.LiveSessions() {
		if target == nil {
			continue
		}
		tid, _, _, running := target.CurrentQuery()
		if !running || tid != qid {
			continue
		}
		if !admin && !strings.EqualFold(target.User(), s.user) {
			return nil, security.Deny("executor.CancelQuery")
		}
		if target.CancelQuery(qid) {
			return &Result{Affected: 1}, nil
		}
		return &Result{}, nil
	}
	// A query that already finished is not an error: the statement asked for
	// it to stop, and it has.
	return &Result{}, nil
}
