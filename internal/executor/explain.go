package executor

import (
	"time"

	"github.com/bzync/nextsql/internal/sql/optimizer"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/txn"
)

func (s *Session) execExplain(p planner.Explain, trace *optimizer.Node, sql string) (*Result, error) {
	if !p.Analyze {
		return explainResult(trace, false), nil
	}
	auto := false
	if s.x == nil {
		if err := s.start(txn.SnapshotIsolation); err != nil {
			return nil, err
		}
		auto = true
	}
	start := time.Now()
	prev := s.trace
	s.trace = trace
	_, err := s.execPlan(p.Input)
	s.trace = prev
	if auto {
		if err != nil {
			_ = s.abort()
			return nil, err
		}
		if _, cerr := s.commit(); cerr != nil {
			return nil, cerr
		}
	} else if err != nil {
		return nil, err
	}
	if trace != nil {
		if trace.TimeNS == 0 {
			trace.TimeNS = time.Since(start).Nanoseconds()
			trace.CPUTimeNS = trace.TimeNS
		}
		optimizer.FillDefaults(trace)
		if s.db.feedback != nil && sql != "" {
			s.db.feedback.Record(sql, trace)
		}
	}
	return explainResult(trace, true), nil
}

func explainResult(trace *optimizer.Node, analyze bool) *Result {
	return &Result{
		Columns: append([]string(nil), optimizer.ExplainColumns...),
		Rows:    optimizer.Rows(trace, analyze),
	}
}
