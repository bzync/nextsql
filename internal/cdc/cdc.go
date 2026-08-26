// Package cdc decodes committed logical row changes from the encrypted WAL.
// It is deliberately pull-based: consumers request the next transaction, so
// slow consumers cannot create an unbounded server-side queue.
package cdc

import (
	"context"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

const (
	defaultMaxPendingTransactions = 1024
	defaultMaxChangesPerTxn       = 131072
	defaultMaxPendingBytes        = 16 << 20
	defaultScanRecords            = 256
	defaultScanBytes              = 4 << 20
	defaultPollInterval           = 25 * time.Millisecond
)

// Limits bound decoder state and each incremental WAL scan.
type Limits struct {
	MaxPendingTransactions int
	MaxChangesPerTxn       int
	MaxPendingBytes        int
	ScanRecords            int
	ScanBytes              int
	PollInterval           time.Duration
}

func (l Limits) defaults() Limits {
	if l.MaxPendingTransactions <= 0 {
		l.MaxPendingTransactions = defaultMaxPendingTransactions
	}
	if l.MaxChangesPerTxn <= 0 {
		l.MaxChangesPerTxn = defaultMaxChangesPerTxn
	}
	if l.MaxPendingBytes <= 0 {
		l.MaxPendingBytes = defaultMaxPendingBytes
	}
	if l.ScanRecords <= 0 {
		l.ScanRecords = defaultScanRecords
	}
	if l.ScanBytes <= 0 {
		l.ScanBytes = defaultScanBytes
	}
	if l.PollInterval <= 0 {
		l.PollInterval = defaultPollInterval
	}
	return l
}

// Event is one key-only v1 row mutation. ChangeLSN orders events within a
// committed transaction; CommitLSN is the resumable transaction boundary.
type Event struct {
	Operation wal.ChangeOperation
	TableID   uint32
	Table     string
	Tenant    string
	OldTenant string
	Key       []byte
	OldKey    []byte
	Before    []byte
	After     []byte
	ChangeLSN format.LSN
	CommitLSN format.LSN
	TxnID     format.TxnID
}

// Transaction is the atomic CDC delivery unit. Token equals CommitLSN; resume
// starts strictly after it, providing ordered at-least-once delivery when a
// consumer persists the token only after processing the transaction.
type Transaction struct {
	Database  string
	TxnID     format.TxnID
	CommitLSN format.LSN
	Token     format.LSN
	Events    []Event
}

type pendingTxn struct {
	events []Event
	bytes  int
}

// Decoder is a bounded state machine over WAL records.
type Decoder struct {
	limits       Limits
	pending      map[format.TxnID]*pendingTxn
	pendingBytes int
	openTxn      format.TxnID
}

func NewDecoder(limits Limits) *Decoder {
	return &Decoder{limits: limits.defaults(), pending: make(map[format.TxnID]*pendingTxn)}
}

// Feed consumes one record. committed is true for every COMMIT, including a
// transaction with no logical row changes. A non-nil Transaction is returned
// only when its matching COMMIT record has been observed.
func (d *Decoder) Feed(rec wal.Record) (tx *Transaction, committed bool, err error) {
	if d == nil {
		return nil, false, nerr.New(nerr.InvalidArgument, "cdc.Decoder.Feed", "nil decoder")
	}
	if d.openTxn != 0 && rec.TxnID != d.openTxn {
		// A process may crash after writing change records but before COMMIT.
		// Once a later transaction appears, the stranded batch can never
		// commit and must be discarded rather than exposed or retained.
		if p := d.pending[d.openTxn]; p != nil {
			d.pendingBytes -= p.bytes
			delete(d.pending, d.openTxn)
		}
		d.openTxn = 0
	}
	switch rec.Type {
	case wal.RecChange:
		if rec.TxnID == 0 || rec.LSN == 0 {
			return nil, false, nerr.New(nerr.InvalidFormat, "cdc.Decoder.Feed", "change has invalid transaction identity")
		}
		change, err := wal.DecodeChange(rec.Body)
		if err != nil {
			return nil, false, err
		}
		p := d.pending[rec.TxnID]
		if p == nil {
			if len(d.pending) >= d.limits.MaxPendingTransactions {
				return nil, false, nerr.New(nerr.Exhausted, "cdc.Decoder.Feed", "pending transaction limit exceeded")
			}
			p = &pendingTxn{}
			d.pending[rec.TxnID] = p
		}
		if len(p.events) >= d.limits.MaxChangesPerTxn || d.pendingBytes+len(rec.Body) > d.limits.MaxPendingBytes {
			return nil, false, nerr.New(nerr.Exhausted, "cdc.Decoder.Feed", "pending change limit exceeded")
		}
		p.events = append(p.events, Event{
			Operation: change.Operation,
			TableID:   change.TableID,
			Table:     change.Table,
			Tenant:    change.Tenant,
			OldTenant: change.OldTenant,
			Key:       append([]byte(nil), change.Key...),
			OldKey:    append([]byte(nil), change.OldKey...),
			Before:    append([]byte(nil), change.Before...),
			After:     append([]byte(nil), change.After...),
			ChangeLSN: rec.LSN,
			TxnID:     rec.TxnID,
		})
		p.bytes += len(rec.Body)
		d.pendingBytes += len(rec.Body)
		d.openTxn = rec.TxnID
		return nil, false, nil
	case wal.RecCommit:
		committed = true
		p := d.pending[rec.TxnID]
		if p == nil {
			return nil, true, nil
		}
		if len(p.events) == 0 || rec.PrevLSN != p.events[len(p.events)-1].ChangeLSN {
			return nil, false, nerr.New(nerr.InvalidFormat, "cdc.Decoder.Feed", "commit does not close logical change batch")
		}
		delete(d.pending, rec.TxnID)
		d.pendingBytes -= p.bytes
		d.openTxn = 0
		for i := range p.events {
			p.events[i].CommitLSN = rec.LSN
		}
		return &Transaction{TxnID: rec.TxnID, CommitLSN: rec.LSN, Token: rec.LSN, Events: p.events}, true, nil
	case wal.RecAbort:
		if p := d.pending[rec.TxnID]; p != nil {
			d.pendingBytes -= p.bytes
			delete(d.pending, rec.TxnID)
		}
		if d.openTxn == rec.TxnID {
			d.openTxn = 0
		}
		return nil, false, nil
	default:
		return nil, false, nil
	}
}

// Filter narrows a subscription. Empty table filters mean all tables. Tenant
// is fail-closed for cross-tenant UPDATEs: both old and new tenant identities
// must be empty or equal to the requested tenant.
type Filter struct {
	TableIDs   map[uint32]struct{}
	Tables     map[string]struct{}
	Operations map[wal.ChangeOperation]struct{}
	Tenant     string
}

func (f Filter) match(e Event) bool {
	if len(f.Operations) != 0 {
		if _, ok := f.Operations[e.Operation]; !ok {
			return false
		}
	}
	if len(f.TableIDs) != 0 {
		if _, ok := f.TableIDs[e.TableID]; !ok {
			return false
		}
	}
	if len(f.Tables) != 0 {
		if _, ok := f.Tables[e.Table]; !ok {
			return false
		}
	}
	if f.Tenant == "" {
		return true
	}
	if e.Tenant != f.Tenant {
		return false
	}
	return e.OldTenant == "" || e.OldTenant == f.Tenant
}

func filterTransaction(tx *Transaction, f Filter) *Transaction {
	if tx == nil {
		return nil
	}
	out := *tx
	out.Events = make([]Event, 0, len(tx.Events))
	for _, e := range tx.Events {
		if f.match(e) {
			out.Events = append(out.Events, e)
		}
	}
	if len(out.Events) == 0 {
		return nil
	}
	return &out
}

// Subscription is a pull-based, bounded WAL subscription. It owns no
// goroutine and retains at most one bounded scan chunk plus Decoder state.
type Subscription struct {
	log      *wal.Log
	database string
	filter   Filter
	limits   Limits
	decoder  *Decoder
	scanLSN  format.LSN
	token    format.LSN
	records  []wal.Record
	next     int
	release  func()
	closed   bool
}

// Subscribe starts after the supplied committed token. after=0 starts at the
// oldest retained WAL record. A token older than retained history fails
// explicitly instead of silently skipping changes.
func Subscribe(log *wal.Log, after format.LSN, filter Filter, limits Limits) (*Subscription, error) {
	if log == nil {
		return nil, nerr.New(nerr.InvalidArgument, "cdc.Subscribe", "nil WAL")
	}
	oldest, err := log.OldestLSN()
	if err != nil {
		return nil, err
	}
	if after != 0 && (after == ^format.LSN(0) || after+1 < oldest) {
		return nil, nerr.New(nerr.NotFound, "cdc.Subscribe", "change history expired")
	}
	retained := oldest
	if after != 0 {
		retained = after + 1
	}
	release, err := log.PinRetention(retained)
	if err != nil {
		return nil, err
	}
	limits = limits.defaults()
	return &Subscription{
		log: log, database: log.Identity().DatabaseString(), filter: filter,
		limits: limits, decoder: NewDecoder(limits), scanLSN: after, token: after, release: release,
	}, nil
}

// Close releases the subscription's WAL-retention pin. It is idempotent.
func (s *Subscription) Close() {
	if s == nil || s.closed {
		return
	}
	s.closed = true
	if s.release != nil {
		s.release()
		s.release = nil
	}
}

// Next blocks until the caller pulls one matching committed transaction or
// ctx is canceled. Backpressure is therefore exactly one transaction.
func (s *Subscription) Next(ctx context.Context) (*Transaction, error) {
	if s == nil || s.log == nil {
		return nil, nerr.New(nerr.InvalidArgument, "cdc.Subscription.Next", "nil subscription")
	}
	if s.closed {
		return nil, nerr.New(nerr.Canceled, "cdc.Subscription.Next", "subscription closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		for s.next < len(s.records) {
			rec := s.records[s.next]
			s.next++
			s.scanLSN = rec.LSN
			tx, committed, err := s.decoder.Feed(rec)
			if err != nil {
				s.Close()
				return nil, err
			}
			if committed {
				s.token = rec.LSN
			}
			if tx = filterTransaction(tx, s.filter); tx != nil {
				tx.Database = s.database
				return tx, nil
			}
		}
		s.records = nil
		s.next = 0
		recs, _, err := s.log.ScanFromLimit(s.scanLSN+1, s.limits.ScanRecords, s.limits.ScanBytes)
		if err != nil {
			s.Close()
			return nil, err
		}
		if len(recs) != 0 {
			s.records = recs
			continue
		}
		timer := time.NewTimer(s.limits.PollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			s.Close()
			return nil, nerr.Wrap(nerr.Canceled, "cdc.Subscription.Next", "subscription canceled", ctx.Err())
		case <-timer.C:
		}
	}
}

// Token is the latest fully observed COMMIT boundary, including commits that
// contained no event matching the subscription filter.
func (s *Subscription) Token() format.LSN {
	if s == nil {
		return 0
	}
	return s.token
}

// Lag reports durable WAL records after the last observed commit token.
func (s *Subscription) Lag() uint64 {
	if s == nil || s.log == nil {
		return 0
	}
	durable := s.log.DurableLSN()
	if durable <= s.token {
		return 0
	}
	return uint64(durable - s.token)
}
