package executor

import (
	"bytes"
	"context"
	"encoding/hex"
	"strconv"
	"sync"

	"github.com/bzync/nextsql/internal/catalog"
	cdcstream "github.com/bzync/nextsql/internal/cdc"
	"github.com/bzync/nextsql/internal/executor/vector"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/security"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
	"github.com/bzync/nextsql/internal/storage/format"
	"github.com/bzync/nextsql/internal/wal"
)

func (s *Session) stageRowChange(tab *catalog.Table, op wal.ChangeOperation, old, neu []types.Value) error {
	if s == nil || s.x == nil || s.x.owner == nil || s.x.owner.Storage() == nil || tab == nil {
		return nerr.New(nerr.Internal, "executor.stageRowChange", "active transaction and table are required")
	}
	var oldKey, key []byte
	var err error
	if old != nil {
		oldKey, err = types.EncodeKey(tab.PKValues(old))
		if err != nil {
			return err
		}
	}
	if neu != nil {
		key, err = types.EncodeKey(tab.PKValues(neu))
		if err != nil {
			return err
		}
	}
	change := wal.Change{Operation: op, TableID: tab.ID, Table: tab.Name}
	switch op {
	case wal.ChangeInsert:
		change.Key = key
	case wal.ChangeDelete:
		change.Key = oldKey
	case wal.ChangeUpdate:
		change.Key = key
		if !bytes.Equal(oldKey, key) {
			change.OldKey = oldKey
		}
	default:
		return nerr.New(nerr.InvalidArgument, "executor.stageRowChange", "invalid operation")
	}
	if tab.CDCImages == catalog.CDCImagesFull {
		if old != nil {
			change.Before, err = types.EncodeRow(old)
			if err != nil {
				return err
			}
		}
		if neu != nil {
			change.After, err = types.EncodeRow(neu)
			if err != nil {
				return err
			}
		}
	}
	return s.x.owner.Storage().StageChange(change)
}

func (s *Session) stageEncodedChange(tab *catalog.Table, op wal.ChangeOperation, oldKey, key, before, after []byte, _, _ string) error {
	if s == nil || s.x == nil || s.x.owner == nil || s.x.owner.Storage() == nil || tab == nil {
		return nerr.New(nerr.Internal, "executor.stageEncodedChange", "active transaction and table are required")
	}
	change := wal.Change{Operation: op, TableID: tab.ID, Table: tab.Name}
	switch op {
	case wal.ChangeInsert:
		change.Key = key
	case wal.ChangeDelete:
		change.Key = oldKey
	case wal.ChangeUpdate:
		change.Key = key
		if !bytes.Equal(oldKey, key) {
			change.OldKey = oldKey
		}
	default:
		return nerr.New(nerr.InvalidArgument, "executor.stageEncodedChange", "invalid operation")
	}
	if tab.CDCImages == catalog.CDCImagesFull {
		change.Before = append([]byte(nil), before...)
		change.After = append([]byte(nil), after...)
	}
	return s.x.owner.Storage().StageChange(change)
}

var subscribeColumns = []string{
	"operation", "database_id", "table_id", "table", "tenant", "old_tenant",
	"primary_key", "old_primary_key", "transaction_id", "change_lsn",
	"commit_lsn", "resume_token", "lag_lsn",
	"before_image", "after_image",
}

func (s *Session) execSubscribe(parent context.Context, p planner.Subscribe) (*Result, error) {
	if s == nil || s.db == nil || s.db.Eng == nil || s.db.Eng.WAL == nil || p.Table == nil {
		return nil, nerr.New(nerr.Internal, "executor.Subscribe", "database WAL and table are required")
	}
	if parent == nil {
		parent = context.Background()
	}
	filter := cdcstream.Filter{TableIDs: map[uint32]struct{}{p.Table.ID: {}}}
	if p.Operation != "" {
		filter.Operations = make(map[wal.ChangeOperation]struct{}, 1)
		switch p.Operation {
		case "INSERT":
			filter.Operations[wal.ChangeInsert] = struct{}{}
		case "UPDATE":
			filter.Operations[wal.ChangeUpdate] = struct{}{}
		case "DELETE":
			filter.Operations[wal.ChangeDelete] = struct{}{}
		default:
			return nil, nerr.New(nerr.Internal, "executor.Subscribe", "invalid bound operation filter")
		}
	}
	ctx := parent
	cancel := func() {}
	if timeout := s.limitsOrDefault().Time; timeout > 0 {
		ctx, cancel = context.WithTimeout(parent, timeout)
	} else {
		ctx, cancel = context.WithCancel(parent)
	}
	sub, err := cdcstream.Subscribe(s.db.Eng.WAL, format.LSN(p.After), filter, cdcstream.Limits{})
	if err != nil {
		cancel()
		return nil, err
	}
	s.db.metrics.AddCDCSubscription()
	subID := s.db.registerCDCSubscription(p.Table.Name, uint64(p.After))
	var cleanupOnce sync.Once
	cleanup := func() {
		cleanupOnce.Do(func() {
			cancel()
			sub.Close()
			s.db.metrics.CloseCDCSubscription()
			s.db.unregisterCDCSubscription(subID)
		})
	}
	typesOut := make([]types.Type, len(subscribeColumns))
	for i := range typesOut {
		typesOut[i] = types.String()
	}
	batchSize := s.limitsOrDefault().BatchSize
	if batchSize < 1 {
		batchSize = 1024
	}
	var current *cdcstream.Transaction
	currentEvent := 0
	result := &Result{Columns: append([]string(nil), subscribeColumns...)}
	result.close = cleanup
	result.next = func() (*vector.Batch, error) {
		// Authorization is checked for every pull as well as at statement
		// admission so revoking CDC stops an already-open stream.
		if err := s.require(security.PrivCDC, security.ScopeTable, p.Table.Name); err != nil {
			s.db.metrics.AddCDCError()
			cleanup()
			return nil, err
		}
		batch := vector.New(typesOut, batchSize)
		transactions := int64(0)
		for batch.Count < batch.Cap() {
			if current == nil || currentEvent >= len(current.Events) {
				var err error
				current, err = sub.Next(ctx)
				if err != nil {
					s.db.metrics.AddCDCError()
					cleanup()
					return nil, err
				}
				currentEvent = 0
				transactions++
				s.db.updateCDCSubscriptionLSN(subID, uint64(current.Token))
			}
			e := current.Events[currentEvent]
			currentEvent++
			row := []types.Value{
				types.StringValue(e.Operation.String()),
				types.StringValue(current.Database),
				types.StringValue(strconv.FormatUint(uint64(e.TableID), 10)),
				types.StringValue(e.Table),
				types.StringValue(e.Tenant),
				types.StringValue(e.OldTenant),
				types.StringValue(hex.EncodeToString(e.Key)),
				types.StringValue(hex.EncodeToString(e.OldKey)),
				types.StringValue(strconv.FormatUint(uint64(e.TxnID), 10)),
				types.StringValue(strconv.FormatUint(uint64(e.ChangeLSN), 10)),
				types.StringValue(strconv.FormatUint(uint64(e.CommitLSN), 10)),
				types.StringValue(strconv.FormatUint(uint64(current.Token), 10)),
				types.StringValue(strconv.FormatUint(sub.Lag(), 10)),
				types.StringValue(hex.EncodeToString(e.Before)),
				types.StringValue(hex.EncodeToString(e.After)),
			}
			if !batch.AppendRow(row) {
				s.db.metrics.AddCDCError()
				cleanup()
				return nil, nerr.New(nerr.Internal, "executor.Subscribe", "CDC batch append failed")
			}
			// Do not block waiting for another transaction after producing rows.
			// The protocol can send this bounded batch and wait for FlowAck first.
			if currentEvent >= len(current.Events) {
				current = nil
				break
			}
		}
		s.db.metrics.AddCDCDelivery(transactions, int64(batch.Count), sub.Lag())
		return batch, nil
	}
	return result, nil
}
