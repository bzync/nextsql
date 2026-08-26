package optimizer

import (
	"sync"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/sql/planner"
)

// StatsFunc returns a table's statistics snapshot, if any.
type StatsFunc func(name string) (*catalog.TableStats, bool)

// Request is one optimize call.
type Request struct {
	Plan     planner.Logical
	SQL      string
	CacheKey string // if set, used instead of SQL for the plan cache
	Stats    StatsFunc
	Gen      uint64
	Cache    *Cache
}

// Outcome is the chosen physical plan plus the EXPLAIN tree.
type Outcome struct {
	Plan   planner.Logical
	Trace  *Node
	Cached bool
}

// Node is one EXPLAIN operator. Estimates are filled by the optimizer;
// actuals / timings are filled by the executor.
type Node struct {
	Op        string
	Detail    string
	EstRows   int64
	EstCost   int64
	ActRows   int64
	TimeNS    int64
	CPUTimeNS int64
	Memory    int64
	Disk      int64
	Cache     int64
	Spill     int64
	Workers   int
	Index     string
	Kids      []*Node
}

// Cache is a bounded, generation-keyed plan cache.
type Cache struct {
	mu    sync.Mutex
	items map[string]cacheEnt
	order []string
}

type cacheEnt struct {
	gen   uint64
	plan  planner.Logical
	trace *Node
}

const maxCache = 256

func NewCache() *Cache {
	return &Cache{items: make(map[string]cacheEnt)}
}

func (c *Cache) get(sql string, gen uint64) (planner.Logical, *Node, bool) {
	if c == nil || sql == "" {
		return nil, nil, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.items[sql]
	if !ok || e.gen != gen {
		return nil, nil, false
	}
	return e.plan, cloneNode(e.trace), true
}

func (c *Cache) put(sql string, gen uint64, plan planner.Logical, trace *Node) {
	if c == nil || sql == "" || !cacheable(plan) {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.items[sql]; !ok {
		if len(c.order) >= maxCache {
			old := c.order[0]
			c.order = c.order[1:]
			delete(c.items, old)
		}
		c.order = append(c.order, sql)
	}
	c.items[sql] = cacheEnt{gen: gen, plan: plan, trace: cloneNode(trace)}
}

func cacheable(p planner.Logical) bool {
	switch p.(type) {
	case planner.Begin, planner.Commit, planner.Rollback, planner.Explain, planner.Analyze, planner.Subscribe, planner.CreateTable, planner.DropTable, planner.DropIndex, planner.RebuildIndex, planner.AlterTable, planner.CreateIndex:
		return false
	default:
		return true
	}
}

func cloneNode(n *Node) *Node {
	if n == nil {
		return nil
	}
	c := *n
	if n.Kids != nil {
		c.Kids = make([]*Node, len(n.Kids))
		for i := range n.Kids {
			c.Kids[i] = cloneNode(n.Kids[i])
		}
	}
	return &c
}

// Feedback records estimated vs actual rows from EXPLAIN ANALYZE.
// It is observational: it does not change plan choice (same catalog +
// stats must produce the same plan).
type Feedback struct {
	mu sync.Mutex
	m  map[string][]Sample
}

type Sample struct {
	Op      string
	EstRows int64
	ActRows int64
}

func NewFeedback() *Feedback {
	return &Feedback{m: make(map[string][]Sample)}
}

func (f *Feedback) Record(sql string, n *Node) {
	if f == nil || n == nil || sql == "" {
		return
	}
	var out []Sample
	var walk func(*Node)
	walk = func(x *Node) {
		if x == nil {
			return
		}
		out = append(out, Sample{Op: x.Op, EstRows: x.EstRows, ActRows: x.ActRows})
		for _, k := range x.Kids {
			walk(k)
		}
	}
	walk(n)
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = make(map[string][]Sample)
	}
	if len(f.m) >= maxCache {
		for k := range f.m {
			delete(f.m, k)
			break
		}
	}
	f.m[sql] = out
}

func (f *Feedback) Get(sql string) []Sample {
	if f == nil {
		return nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]Sample(nil), f.m[sql]...)
}

// Optimize rewrites a logical plan and picks a deterministic physical plan.
func Optimize(req Request) (Outcome, error) {
	if req.Plan == nil {
		return Outcome{}, nil
	}
	if ex, ok := req.Plan.(planner.Explain); ok {
		inner, err := Optimize(Request{Plan: ex.Input, SQL: req.SQL, CacheKey: req.CacheKey, Stats: req.Stats, Gen: req.Gen})
		if err != nil {
			return Outcome{}, err
		}
		return Outcome{Plan: planner.Explain{Input: inner.Plan, Analyze: ex.Analyze}, Trace: inner.Trace}, nil
	}
	if _, ok := req.Plan.(planner.Analyze); ok {
		return Outcome{Plan: req.Plan, Trace: &Node{Op: "Analyze"}}, nil
	}
	switch req.Plan.(type) {
	case planner.Begin, planner.Commit, planner.Rollback, planner.Subscribe, planner.CreateTable, planner.CreateDatabase, planner.DropTable, planner.DropIndex, planner.RebuildIndex, planner.AlterTable, planner.CreateIndex, planner.Insert, planner.Upsert:
		return Outcome{Plan: req.Plan, Trace: leafTrace(req.Plan)}, nil
	}
	key := req.SQL
	if req.CacheKey != "" {
		key = req.CacheKey
	}
	if p, tr, ok := req.Cache.get(key, req.Gen); ok {
		return Outcome{Plan: p, Trace: tr, Cached: true}, nil
	}
	rewritten := rewrite(req.Plan)
	rewritten = decideCTEs(rewritten, req.Stats)
	rewritten = rewrite(rewritten)
	phys, trace := choose(rewritten, req.Stats)
	req.Cache.put(key, req.Gen, phys, trace)
	return Outcome{Plan: phys, Trace: trace}, nil
}

func leafTrace(p planner.Logical) *Node {
	switch p.(type) {
	case planner.CreateTable:
		return &Node{Op: "CreateTable"}
	case planner.CreateDatabase:
		return &Node{Op: "CreateDatabase"}
	case planner.DropTable:
		return &Node{Op: "DropTable"}
	case planner.DropIndex:
		return &Node{Op: "DropIndex"}
	case planner.RebuildIndex:
		return &Node{Op: "RebuildIndex"}
	case planner.AlterTable:
		return &Node{Op: "AlterTable"}
	case planner.CreateIndex:
		return &Node{Op: "CreateIndex"}
	case planner.Insert:
		return &Node{Op: "Insert"}
	case planner.Upsert:
		return &Node{Op: "Upsert"}
	case planner.Begin:
		return &Node{Op: "Begin"}
	case planner.Commit:
		return &Node{Op: "Commit"}
	case planner.Rollback:
		return &Node{Op: "Rollback"}
	case planner.Subscribe:
		return &Node{Op: "Subscribe"}
	default:
		return &Node{Op: "Plan"}
	}
}
