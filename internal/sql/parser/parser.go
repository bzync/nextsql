package parser

import (
	"math"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/lexer"
	"github.com/bzync/nextsql/internal/sql/types"
)

type Parser struct {
	lx         *lexer.Lexer
	tok        lexer.Token
	subqueryID uint64
}

func (p *Parser) nextSubqueryID() uint64 {
	p.subqueryID++
	return p.subqueryID
}

func Parse(src string) (ast.Stmt, error) {
	p := &Parser{lx: lexer.New(src)}
	p.next()
	if p.tok.Kind == lexer.EOF {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "empty statement")
	}
	stmt, err := p.stmt()
	if err != nil {
		return nil, err
	}
	if p.tok.Kind == lexer.Semi {
		p.next()
	}
	if p.tok.Kind != lexer.EOF {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "unexpected token after statement")
	}
	if err := p.lx.Err(); err != nil {
		return nil, err
	}
	return stmt, nil
}

func (p *Parser) next() {
	p.tok = p.lx.Next()
}

func (p *Parser) stmt() (ast.Stmt, error) {
	switch p.tok.Kind {
	case lexer.KwCreate:
		return p.create()
	case lexer.KwDrop:
		return p.drop()
	case lexer.KwAlter:
		return p.alter()
	case lexer.KwRebuild:
		return p.rebuild()
	case lexer.KwGrant:
		return p.grant()
	case lexer.KwRevoke:
		return p.revoke()
	case lexer.KwInsert:
		return p.insert()
	case lexer.KwUpsert:
		return p.upsert()
	case lexer.KwSelect:
		return p.query()
	case lexer.KwWith:
		return p.withQuery()
	case lexer.KwUpdate:
		return p.update()
	case lexer.KwDelete:
		return p.del()
	case lexer.KwBegin:
		return p.begin()
	case lexer.KwExplain:
		return p.explain()
	case lexer.KwAnalyze:
		return p.analyze()
	case lexer.KwMaintain:
		return p.maintain()
	case lexer.KwSet:
		return p.setStmt()
	case lexer.KwReset:
		return p.resetStmt()
	case lexer.KwRun:
		return p.runWorkflow()
	case lexer.KwShow:
		return p.show()
	case lexer.KwCancel:
		return p.cancelTask()
	case lexer.KwSubscribe:
		return p.subscribe()
	case lexer.KwCluster:
		return p.clusterStmt()
	case lexer.KwCommit:
		p.next()
		if p.tok.Kind == lexer.KwTransaction {
			p.next()
		}
		return ast.Commit{}, nil
	case lexer.KwRollback:
		p.next()
		if p.tok.Kind == lexer.KwTransaction {
			p.next()
		}
		return ast.Rollback{}, nil
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected a statement")
	}
}

// subscribe parses the native continuous surface:
//
//	SUBSCRIBE TO table [WHERE operation = 'INSERT|UPDATE|DELETE'] [AFTER commit_lsn]
//
// The tenant is never accepted from SQL; it is bound from the authenticated
// session by the executor.
func (p *Parser) subscribe() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwTo, "TO"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	out := ast.Subscribe{Table: table}
	if p.tok.Kind == lexer.KwWhere {
		p.next()
		if p.tok.Kind != lexer.Ident || p.tok.Lit != "operation" {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "SUBSCRIBE predicate supports only operation")
		}
		p.next()
		if err := p.expect(lexer.Eq, "="); err != nil {
			return nil, err
		}
		if p.tok.Kind != lexer.String {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "SUBSCRIBE operation requires a string")
		}
		out.Operation = strings.ToUpper(p.tok.Lit)
		switch out.Operation {
		case "INSERT", "UPDATE", "DELETE":
		default:
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "SUBSCRIBE operation must be INSERT, UPDATE, or DELETE")
		}
		p.next()
	}
	if p.tok.Kind == lexer.KwAfter {
		p.next()
		if p.tok.Kind != lexer.Number {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "SUBSCRIBE AFTER requires a commit LSN")
		}
		after, err := strconv.ParseUint(p.tok.Lit, 10, 64)
		if err != nil {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "invalid SUBSCRIBE commit LSN")
		}
		out.After = after
		p.next()
	}
	return out, nil
}

// show parses bounded convenience aliases over the canonical system schema.
// Returning an ordinary system-table SELECT keeps execution, RBAC, redaction,
// and column definitions on the same source of truth as direct system.*
// queries. SHOW TASKS retains its existing pagination-specific surface.
func (p *Parser) show() (ast.Stmt, error) {
	p.next()
	switch p.tok.Lit {
	case "tasks":
		return p.showTasks()
	case "databases":
		p.next()
		return ast.Select{
			Table: "system.storage",
			List:  []ast.SelectItem{{Expr: ast.Ident{Name: "database"}}},
		}, nil
	case "realms":
		return p.showSystemTable("system.realms")
	case "tables":
		return p.showSystemTable("system.tables")
	case "indexes":
		return p.showSystemTable("system.indexes")
	case "connections":
		return p.showSystemTable("system.sessions")
	case "queries":
		return p.showSystemTable("system.active_queries")
	case "transactions":
		return p.showSystemTable("system.transactions")
	case "locks":
		return p.showSystemTable("system.locks")
	case "cluster":
		return p.showSystemTable("system.replication")
	case "storage":
		return p.showSystemTable("system.storage")
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "unsupported SHOW object")
	}
}

func (p *Parser) showSystemTable(name string) (ast.Stmt, error) {
	p.next()
	return ast.Select{Table: name, Star: true}, nil
}

func (p *Parser) showTasks() (ast.Stmt, error) {
	if err := p.expect(lexer.KwTasks, "TASKS"); err != nil {
		return nil, err
	}
	out := ast.ShowTasks{Limit: 100}
	if p.tok.Kind == lexer.KwAfter {
		p.next()
		if p.tok.Kind != lexer.String {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "SHOW TASKS AFTER requires a task id string")
		}
		out.After = p.tok.Lit
		p.next()
	}
	if p.tok.Kind == lexer.KwLimit {
		p.next()
		if p.tok.Kind != lexer.Number {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "SHOW TASKS LIMIT requires an integer")
		}
		n, err := strconv.Atoi(p.tok.Lit)
		if err != nil || n < 1 || n > 256 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "SHOW TASKS LIMIT must be between 1 and 256")
		}
		out.Limit = n
		p.next()
	}
	return out, nil
}

// clusterStmt parses the cluster admin surface:
//
//	CLUSTER TRANSFER LEADER
//	CLUSTER DRAIN [WITH (TIMEOUT_MS = n)]
//	CLUSTER MAINTENANCE ENABLE|DISABLE
//
// setStmt parses SET RESOURCE GROUP <name>, the one surviving form of SET
// after SET TENANT (multi-tenancy) was removed; every other spelling is
// still rejected with the same removal message as before.
func (p *Parser) setStmt() (ast.Stmt, error) {
	p.next()
	if p.tok.Kind != lexer.KwResource {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "SET TENANT was removed; provision an isolated database with nextsql hosting")
	}
	p.next()
	if err := p.expect(lexer.KwGroup, "GROUP"); err != nil {
		return nil, err
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	return ast.SetResourceGroup{Name: name}, nil
}

// resetStmt parses RESET RESOURCE GROUP, the one surviving form of RESET
// after RESET TENANT was removed; every other spelling is still rejected
// with the same removal message as before.
func (p *Parser) resetStmt() (ast.Stmt, error) {
	p.next()
	if p.tok.Kind != lexer.KwResource {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "RESET TENANT was removed; provision an isolated database with nextsql hosting")
	}
	p.next()
	if err := p.expect(lexer.KwGroup, "GROUP"); err != nil {
		return nil, err
	}
	return ast.ResetResourceGroup{}, nil
}

func (p *Parser) clusterStmt() (ast.Stmt, error) {
	p.next()
	switch p.tok.Kind {
	case lexer.KwTransfer:
		p.next()
		if err := p.expect(lexer.KwLeader, "LEADER"); err != nil {
			return nil, err
		}
		return ast.TransferLeader{}, nil
	case lexer.KwDrain:
		p.next()
		var timeoutMS int64
		if p.tok.Kind == lexer.KwWith {
			p.next()
			if err := p.expect(lexer.LParen, "("); err != nil {
				return nil, err
			}
			if !p.identIs("timeout_ms") {
				return nil, nerr.New(nerr.Syntax, "sql.parser", "expected TIMEOUT_MS")
			}
			p.next()
			if err := p.expect(lexer.Eq, "="); err != nil {
				return nil, err
			}
			n, err := p.uint64Lit()
			if err != nil {
				return nil, err
			}
			timeoutMS = int64(n)
			if err := p.expect(lexer.RParen, ")"); err != nil {
				return nil, err
			}
		}
		return ast.ClusterDrain{TimeoutMS: timeoutMS}, nil
	case lexer.KwMaintenance:
		p.next()
		switch p.tok.Kind {
		case lexer.KwEnable:
			p.next()
			return ast.ClusterMaintenance{Enable: true}, nil
		case lexer.KwDisable:
			p.next()
			return ast.ClusterMaintenance{Enable: false}, nil
		default:
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected ENABLE or DISABLE after CLUSTER MAINTENANCE")
		}
	case lexer.KwReconcile:
		p.next()
		if err := p.expect(lexer.KwConfirm, "CONFIRM"); err != nil {
			return nil, err
		}
		return ast.ClusterReconcileConfirm{}, nil
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected TRANSFER, DRAIN, MAINTENANCE, or RECONCILE after CLUSTER")
	}
}

func (p *Parser) cancelTask() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwTask, "TASK"); err != nil {
		return nil, err
	}
	if p.tok.Kind != lexer.String || p.tok.Lit == "" {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "CANCEL TASK requires a task id string")
	}
	out := ast.CancelTask{ID: p.tok.Lit}
	p.next()
	return out, nil
}

func (p *Parser) queryOrWith() (ast.Stmt, error) {
	if p.tok.Kind == lexer.KwWith {
		return p.withQuery()
	}
	return p.query()
}

func (p *Parser) withQuery() (ast.Stmt, error) {
	p.next()
	recursive := false
	if p.tok.Kind == lexer.Ident && p.tok.Lit == "recursive" {
		recursive = true
		p.next()
	}
	var ctes []ast.CTEDef
	for {
		if len(ctes) >= 32 {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "WITH exceeds CTE limit")
		}
		name, err := p.ident()
		if err != nil {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected CTE name")
		}
		var cols []string
		if p.tok.Kind == lexer.LParen {
			cols, err = p.identList()
			if err != nil {
				return nil, err
			}
		}
		if err := p.expect(lexer.KwAs, "AS"); err != nil {
			return nil, err
		}
		mat := ast.CTEAuto
		if p.tok.Kind == lexer.KwNot {
			p.next()
			if p.tok.Kind != lexer.Ident || p.tok.Lit != "materialized" {
				return nil, nerr.New(nerr.Syntax, "sql.parser", "expected MATERIALIZED")
			}
			p.next()
			mat = ast.CTENever
		} else if p.tok.Kind == lexer.Ident && p.tok.Lit == "materialized" {
			p.next()
			mat = ast.CTEAlways
		}
		if err := p.expect(lexer.LParen, "("); err != nil {
			return nil, err
		}
		query, err := p.queryOrWith()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
		ctes = append(ctes, ast.CTEDef{Name: name, Columns: cols, Query: query, Materialize: mat})
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if len(ctes) == 0 {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "WITH requires a CTE")
	}
	if p.tok.Kind != lexer.KwSelect {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "WITH requires a SELECT query")
	}
	query, err := p.query()
	if err != nil {
		return nil, err
	}
	return ast.With{Recursive: recursive, CTEs: ctes, Query: query}, nil
}

func (p *Parser) query() (ast.Stmt, error) {
	left, err := p.intersectQuery()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == lexer.KwUnion || p.tok.Kind == lexer.KwExcept {
		op := "union"
		if p.tok.Kind == lexer.KwExcept {
			op = "except"
		}
		p.next()
		all := false
		if p.tok.Kind == lexer.KwAll {
			if op != "union" {
				return nil, nerr.New(nerr.Syntax, "sql.parser", "EXCEPT ALL is not implemented")
			}
			all = true
			p.next()
		}
		if p.tok.Kind != lexer.KwSelect {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "set operation requires SELECT")
		}
		right, err := p.intersectQuery()
		if err != nil {
			return nil, err
		}
		left = ast.SetOperation{Left: left, Right: right, Op: op, All: all}
	}
	return left, nil
}

func (p *Parser) intersectQuery() (ast.Stmt, error) {
	left, err := p.sel()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == lexer.KwIntersect {
		p.next()
		if p.tok.Kind == lexer.KwAll {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "INTERSECT ALL is not implemented")
		}
		if p.tok.Kind != lexer.KwSelect {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "INTERSECT requires SELECT")
		}
		right, err := p.sel()
		if err != nil {
			return nil, err
		}
		left = ast.SetOperation{Left: left, Right: right, Op: "intersect"}
	}
	return left, nil
}

func (p *Parser) rebuild() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwIndex, "INDEX"); err != nil {
		return nil, err
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	online := false
	if p.tok.Kind == lexer.Ident && p.tok.Lit == "online" {
		online = true
		p.next()
	}
	return ast.RebuildIndex{Name: name, Online: online}, nil
}

func (p *Parser) create() (ast.Stmt, error) {
	p.next()
	switch p.tok.Kind {
	case lexer.KwTable:
		return p.createTable()
	case lexer.KwUnique:
		p.next()
		if p.tok.Kind != lexer.KwIndex {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected INDEX")
		}
		return p.createIndex(true, false, false, false)
	case lexer.KwIndex:
		return p.createIndex(false, false, false, false)
	case lexer.KwSpatial:
		p.next()
		if p.tok.Kind != lexer.KwIndex {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected INDEX")
		}
		return p.createIndex(false, true, false, false)
	case lexer.KwFulltext:
		p.next()
		if p.tok.Kind != lexer.KwIndex {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected INDEX")
		}
		return p.createIndex(false, false, true, false)
	case lexer.KwVector:
		p.next()
		if p.tok.Kind != lexer.KwIndex {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected INDEX")
		}
		return p.createIndex(false, false, false, true)
	case lexer.KwUser:
		return p.createUser()
	case lexer.KwRole:
		return p.createRole()
	case lexer.KwDatabase:
		return p.createDatabase()
	case lexer.KwWorkflow:
		return p.createWorkflow()
	case lexer.KwTrigger:
		return p.createTrigger()
	case lexer.KwSchedule:
		return p.createSchedule()
	case lexer.KwResource:
		return p.createResourceGroup()
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected TABLE, INDEX, DATABASE, WORKFLOW, TRIGGER, SCHEDULE, or RESOURCE GROUP")
	}
}

func (p *Parser) createSchedule() (ast.Stmt, error) {
	p.next()
	ifNot := false
	if p.tok.Kind == lexer.KwIf {
		p.next()
		if err := p.expect(lexer.KwNot, "NOT"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
			return nil, err
		}
		ifNot = true
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	var kind ast.ScheduleKind
	switch p.tok.Kind {
	case lexer.KwEvery:
		kind = ast.ScheduleEvery
	case lexer.KwAt:
		kind = ast.ScheduleAt
	case lexer.KwCron:
		kind = ast.ScheduleCron
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected EVERY, AT, or CRON")
	}
	p.next()
	if p.tok.Kind != lexer.String {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "schedule specification must be a string")
	}
	spec := p.tok.Lit
	p.next()
	if err := p.expect(lexer.KwRun, "RUN"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwWorkflow, "WORKFLOW"); err != nil {
		return nil, err
	}
	workflow, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var args []ast.Expr
	for p.tok.Kind != lexer.RParen {
		if len(args) >= maxWorkflowParams {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "schedule exceeds argument limit")
		}
		arg, err := p.or()
		if err != nil {
			return nil, err
		}
		if !scheduleLiteral(arg) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "schedule arguments must be literals")
		}
		args = append(args, arg)
		if p.tok.Kind != lexer.Comma {
			break
		}
		p.next()
		if p.tok.Kind == lexer.RParen {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "trailing schedule argument comma")
		}
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return ast.CreateSchedule{Name: name, Kind: kind, Spec: spec, Workflow: workflow, Args: args, IfNotExists: ifNot}, nil
}

func scheduleLiteral(expr ast.Expr) bool {
	switch x := expr.(type) {
	case ast.Literal:
		return true
	case ast.Unary:
		return scheduleLiteral(x.Right)
	default:
		return false
	}
}

// resourceGroupOptions holds the outcome of a RESOURCE GROUP WITH (...)
// clause: which options were given (Has*) and their parsed values. CREATE
// applies given options over zero defaults; ALTER applies given options
// over the group's current stored values, leaving the rest untouched.
type resourceGroupOptions struct {
	maxConcurrency    uint64
	hasMaxConcurrency bool
	memoryBytes       uint64
	hasMemoryBytes    bool
	workers           uint64
	hasWorkers        bool
	priority          uint64
	hasPriority       bool
}

// resourceGroupWith parses WITH (MAX_CONCURRENCY = n, MEMORY = n, WORKERS =
// n, PRIORITY = n), any subset in any order, each key at most once. Range
// checks against catalog.MaxResourceGroup* happen later, at the catalog
// layer, matching how CREATE VECTOR INDEX ... WITH (LISTS = n, ...) leaves
// range validation to the caller instead of the parser.
func (p *Parser) resourceGroupWith() (resourceGroupOptions, error) {
	var opt resourceGroupOptions
	if err := p.expect(lexer.LParen, "("); err != nil {
		return opt, err
	}
	for {
		switch {
		case p.identIs("max_concurrency"):
			if opt.hasMaxConcurrency {
				return opt, nerr.New(nerr.Syntax, "sql.parser", "duplicate MAX_CONCURRENCY option")
			}
			p.next()
			if err := p.expect(lexer.Eq, "="); err != nil {
				return opt, err
			}
			n, err := p.uintLit()
			if err != nil {
				return opt, err
			}
			opt.maxConcurrency, opt.hasMaxConcurrency = n, true
		case p.identIs("memory"):
			if opt.hasMemoryBytes {
				return opt, nerr.New(nerr.Syntax, "sql.parser", "duplicate MEMORY option")
			}
			p.next()
			if err := p.expect(lexer.Eq, "="); err != nil {
				return opt, err
			}
			n, err := p.uint64Lit()
			if err != nil {
				return opt, err
			}
			opt.memoryBytes, opt.hasMemoryBytes = n, true
		case p.identIs("workers"):
			if opt.hasWorkers {
				return opt, nerr.New(nerr.Syntax, "sql.parser", "duplicate WORKERS option")
			}
			p.next()
			if err := p.expect(lexer.Eq, "="); err != nil {
				return opt, err
			}
			n, err := p.uintLit()
			if err != nil {
				return opt, err
			}
			opt.workers, opt.hasWorkers = n, true
		case p.identIs("priority"):
			if opt.hasPriority {
				return opt, nerr.New(nerr.Syntax, "sql.parser", "duplicate PRIORITY option")
			}
			p.next()
			if err := p.expect(lexer.Eq, "="); err != nil {
				return opt, err
			}
			n, err := p.uintLit()
			if err != nil {
				return opt, err
			}
			opt.priority, opt.hasPriority = n, true
		default:
			return opt, nerr.New(nerr.Syntax, "sql.parser", "expected MAX_CONCURRENCY, MEMORY, WORKERS, or PRIORITY")
		}
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return opt, err
	}
	return opt, nil
}

func (p *Parser) createResourceGroup() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwGroup, "GROUP"); err != nil {
		return nil, err
	}
	ifNot := false
	if p.tok.Kind == lexer.KwIf {
		p.next()
		if err := p.expect(lexer.KwNot, "NOT"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
			return nil, err
		}
		ifNot = true
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	out := ast.CreateResourceGroup{Name: name, IfNotExists: ifNot}
	if p.tok.Kind == lexer.KwWith {
		p.next()
		opt, err := p.resourceGroupWith()
		if err != nil {
			return nil, err
		}
		out.MaxConcurrency = int(opt.maxConcurrency)
		out.MemoryBytes = int64(opt.memoryBytes)
		out.Workers = int(opt.workers)
		out.Priority = int(opt.priority)
	}
	return out, nil
}

func (p *Parser) alterResourceGroup() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwGroup, "GROUP"); err != nil {
		return nil, err
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwWith, "WITH"); err != nil {
		return nil, err
	}
	opt, err := p.resourceGroupWith()
	if err != nil {
		return nil, err
	}
	if !opt.hasMaxConcurrency && !opt.hasMemoryBytes && !opt.hasWorkers && !opt.hasPriority {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "ALTER RESOURCE GROUP WITH requires at least one option")
	}
	return ast.AlterResourceGroup{
		Name:              name,
		MaxConcurrency:    int(opt.maxConcurrency),
		HasMaxConcurrency: opt.hasMaxConcurrency,
		MemoryBytes:       int64(opt.memoryBytes),
		HasMemoryBytes:    opt.hasMemoryBytes,
		Workers:           int(opt.workers),
		HasWorkers:        opt.hasWorkers,
		Priority:          int(opt.priority),
		HasPriority:       opt.hasPriority,
	}, nil
}

func (p *Parser) dropResourceGroup() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwGroup, "GROUP"); err != nil {
		return nil, err
	}
	ifExists := false
	if p.tok.Kind == lexer.KwIf {
		p.next()
		if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
			return nil, err
		}
		ifExists = true
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	return ast.DropResourceGroup{Name: name, IfExists: ifExists}, nil
}

func (p *Parser) createTrigger() (ast.Stmt, error) {
	p.next()
	ifNot := false
	if p.tok.Kind == lexer.KwIf {
		p.next()
		if err := p.expect(lexer.KwNot, "NOT"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
			return nil, err
		}
		ifNot = true
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	var timing ast.TriggerTiming
	switch p.tok.Kind {
	case lexer.KwBefore:
		timing = ast.TriggerBefore
	case lexer.KwAfter:
		timing = ast.TriggerAfter
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected BEFORE or AFTER")
	}
	p.next()
	var event ast.TriggerEvent
	switch p.tok.Kind {
	case lexer.KwInsert:
		event = ast.TriggerInsert
	case lexer.KwUpdate:
		event = ast.TriggerUpdate
	case lexer.KwDelete:
		event = ast.TriggerDelete
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected INSERT, UPDATE, or DELETE")
	}
	p.next()
	if err := p.expect(lexer.KwOn, "ON"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwFor, "FOR"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwEach, "EACH"); err != nil {
		return nil, err
	}
	if !p.identIs("row") {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected ROW")
	}
	p.next()
	if err := p.expect(lexer.KwRun, "RUN"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwWorkflow, "WORKFLOW"); err != nil {
		return nil, err
	}
	workflow, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var args []ast.Expr
	for p.tok.Kind != lexer.RParen {
		if len(args) >= maxWorkflowParams {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "trigger exceeds argument limit")
		}
		arg, err := p.or()
		if err != nil {
			return nil, err
		}
		if err := validateTriggerExpr(arg, event); err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.tok.Kind != lexer.Comma {
			break
		}
		p.next()
		if p.tok.Kind == lexer.RParen {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "trailing trigger argument comma")
		}
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return ast.CreateTrigger{Name: name, Timing: timing, Event: event, Table: table, Workflow: workflow, Args: args, IfNotExists: ifNot}, nil
}

func validateTriggerExpr(expr ast.Expr, event ast.TriggerEvent) error {
	if expr == nil {
		return nil
	}
	switch x := expr.(type) {
	case ast.Literal:
		return nil
	case ast.Path:
		if len(x.Parts) != 2 || (x.Parts[0] != "old" && x.Parts[0] != "new") {
			return nerr.New(nerr.InvalidArgument, "sql.parser", "trigger row reference must be OLD.column or NEW.column")
		}
		if event == ast.TriggerInsert && x.Parts[0] == "old" {
			return nerr.New(nerr.InvalidArgument, "sql.parser", "OLD is unavailable for INSERT triggers")
		}
		if event == ast.TriggerDelete && x.Parts[0] == "new" {
			return nerr.New(nerr.InvalidArgument, "sql.parser", "NEW is unavailable for DELETE triggers")
		}
		return nil
	case ast.Unary:
		return validateTriggerExpr(x.Right, event)
	case ast.Binary:
		if err := validateTriggerExpr(x.Left, event); err != nil {
			return err
		}
		return validateTriggerExpr(x.Right, event)
	case ast.Between:
		for _, item := range []ast.Expr{x.Expr, x.Low, x.High} {
			if err := validateTriggerExpr(item, event); err != nil {
				return err
			}
		}
		return nil
	case ast.IsNull:
		return validateTriggerExpr(x.Expr, event)
	case ast.Case:
		if err := validateTriggerExpr(x.Operand, event); err != nil {
			return err
		}
		for _, arm := range x.Whens {
			if err := validateTriggerExpr(arm.When, event); err != nil {
				return err
			}
			if err := validateTriggerExpr(arm.Then, event); err != nil {
				return err
			}
		}
		return validateTriggerExpr(x.Else, event)
	default:
		return nerr.New(nerr.InvalidArgument, "sql.parser", "expression is not allowed in a trigger argument")
	}
}

const (
	maxWorkflowParams     = 64
	maxWorkflowStatements = 256
)

func (p *Parser) createWorkflow() (ast.Stmt, error) {
	p.next()
	ifNot := false
	if p.tok.Kind == lexer.KwIf {
		p.next()
		if err := p.expect(lexer.KwNot, "NOT"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
			return nil, err
		}
		ifNot = true
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var params []ast.WorkflowParam
	seen := make(map[string]struct{})
	for p.tok.Kind != lexer.RParen {
		if len(params) >= maxWorkflowParams {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "workflow exceeds parameter limit")
		}
		param, err := p.ident()
		if err != nil {
			return nil, err
		}
		if _, ok := seen[param]; ok {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "duplicate workflow parameter")
		}
		typ, err := p.colType()
		if err != nil {
			return nil, err
		}
		seen[param] = struct{}{}
		params = append(params, ast.WorkflowParam{Name: param, Type: typ})
		if p.tok.Kind != lexer.Comma {
			break
		}
		p.next()
		if p.tok.Kind == lexer.RParen {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "trailing workflow parameter comma")
		}
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwAs, "AS"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwBegin, "BEGIN"); err != nil {
		return nil, err
	}
	var body []ast.Stmt
	for p.tok.Kind != lexer.KwEnd {
		if p.tok.Kind == lexer.EOF {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "unterminated workflow body")
		}
		if len(body) >= maxWorkflowStatements {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "workflow exceeds statement limit")
		}
		stmt, err := p.stmt()
		if err != nil {
			return nil, err
		}
		if !workflowBodyStmtAllowed(stmt) {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "statement is not allowed in a workflow body")
		}
		body = append(body, stmt)
		if p.tok.Kind == lexer.Semi {
			p.next()
			continue
		}
		if p.tok.Kind != lexer.KwEnd {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected ; or END in workflow body")
		}
	}
	if len(body) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "workflow body is empty")
	}
	p.next()
	if err := validateWorkflowBodyParams(body, seen); err != nil {
		return nil, err
	}
	return ast.CreateWorkflow{Name: name, Params: params, Body: body, IfNotExists: ifNot}, nil
}

func validateWorkflowBodyParams(body []ast.Stmt, declared map[string]struct{}) error {
	check := func(expr ast.Expr) error {
		return walkWorkflowExpr(expr, func(param string) error {
			if _, ok := declared[param]; !ok {
				return nerr.New(nerr.InvalidArgument, "sql.parser", "undeclared workflow parameter")
			}
			return nil
		})
	}
	for _, stmt := range body {
		switch s := stmt.(type) {
		case ast.Insert:
			for _, row := range s.Rows {
				for _, expr := range row {
					if err := check(expr); err != nil {
						return err
					}
				}
			}
		case ast.Upsert:
			for _, row := range s.Rows {
				for _, expr := range row {
					if err := check(expr); err != nil {
						return err
					}
				}
			}
			for _, set := range s.Sets {
				if err := check(set.Expr); err != nil {
					return err
				}
			}
		case ast.Update:
			for _, set := range s.Sets {
				if err := check(set.Expr); err != nil {
					return err
				}
			}
			if err := check(s.Where); err != nil {
				return err
			}
		case ast.Delete:
			if err := check(s.Where); err != nil {
				return err
			}
		case ast.RunWorkflow:
			for _, arg := range s.Args {
				if err := check(arg); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func walkWorkflowExpr(expr ast.Expr, param func(string) error) error {
	if expr == nil {
		return nil
	}
	switch x := expr.(type) {
	case ast.Literal, ast.Ident, ast.Path:
		return nil
	case ast.Param:
		return param(x.Name)
	case ast.Unary:
		return walkWorkflowExpr(x.Right, param)
	case ast.Binary:
		if err := walkWorkflowExpr(x.Left, param); err != nil {
			return err
		}
		return walkWorkflowExpr(x.Right, param)
	case ast.Between:
		for _, item := range []ast.Expr{x.Expr, x.Low, x.High} {
			if err := walkWorkflowExpr(item, param); err != nil {
				return err
			}
		}
		return nil
	case ast.IsNull:
		return walkWorkflowExpr(x.Expr, param)
	case ast.Call:
		for _, arg := range x.Args {
			if err := walkWorkflowExpr(arg, param); err != nil {
				return err
			}
		}
		return nil
	case ast.Case:
		if err := walkWorkflowExpr(x.Operand, param); err != nil {
			return err
		}
		for _, arm := range x.Whens {
			if err := walkWorkflowExpr(arm.When, param); err != nil {
				return err
			}
			if err := walkWorkflowExpr(arm.Then, param); err != nil {
				return err
			}
		}
		return walkWorkflowExpr(x.Else, param)
	default:
		return nerr.New(nerr.InvalidArgument, "sql.parser", "expression is not allowed in a workflow body")
	}
}

func workflowBodyStmtAllowed(stmt ast.Stmt) bool {
	switch s := stmt.(type) {
	case ast.Insert:
		return !s.ReturningStar && len(s.Returning) == 0
	case ast.Upsert:
		return !s.ReturningStar && len(s.Returning) == 0
	case ast.Update:
		return !s.ReturningStar && len(s.Returning) == 0
	case ast.Delete:
		return !s.ReturningStar && len(s.Returning) == 0
	case ast.RunWorkflow:
		return true
	default:
		return false
	}
}

func (p *Parser) runWorkflow() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwWorkflow, "WORKFLOW"); err != nil {
		return nil, err
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var args []ast.Expr
	for p.tok.Kind != lexer.RParen {
		arg, err := p.or()
		if err != nil {
			return nil, err
		}
		args = append(args, arg)
		if p.tok.Kind != lexer.Comma {
			break
		}
		p.next()
		if p.tok.Kind == lexer.RParen {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "trailing workflow argument comma")
		}
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return ast.RunWorkflow{Name: name, Args: args}, nil
}

func (p *Parser) createDatabase() (ast.Stmt, error) {
	p.next()
	ifNot := false
	if p.tok.Kind == lexer.KwIf {
		p.next()
		if err := p.expect(lexer.KwNot, "NOT"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
			return nil, err
		}
		ifNot = true
	}
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	return ast.CreateDatabase{Name: name, IfNotExists: ifNot}, nil
}

func (p *Parser) createUser() (ast.Stmt, error) {
	p.next()
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwIdentified, "IDENTIFIED"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwBy, "BY"); err != nil {
		return nil, err
	}
	if p.tok.Kind != lexer.String {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected password string")
	}
	pw := p.tok.Lit
	p.next()
	return ast.CreateUser{Name: name, Password: pw}, nil
}

func (p *Parser) createRole() (ast.Stmt, error) {
	p.next()
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	return ast.CreateRole{Name: name}, nil
}

func (p *Parser) drop() (ast.Stmt, error) {
	p.next()
	switch p.tok.Kind {
	case lexer.KwTable:
		p.next()
		ifExists := false
		if p.tok.Kind == lexer.KwIf {
			p.next()
			if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
				return nil, err
			}
			ifExists = true
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropTable{Name: name, IfExists: ifExists}, nil
	case lexer.KwIndex:
		p.next()
		ifExists := false
		if p.tok.Kind == lexer.KwIf {
			p.next()
			if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
				return nil, err
			}
			ifExists = true
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropIndex{Name: name, IfExists: ifExists}, nil
	case lexer.KwUser:
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropUser{Name: name}, nil
	case lexer.KwRole:
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropRole{Name: name}, nil
	case lexer.KwWorkflow:
		p.next()
		ifExists := false
		if p.tok.Kind == lexer.KwIf {
			p.next()
			if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
				return nil, err
			}
			ifExists = true
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropWorkflow{Name: name, IfExists: ifExists}, nil
	case lexer.KwTrigger:
		p.next()
		ifExists := false
		if p.tok.Kind == lexer.KwIf {
			p.next()
			if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
				return nil, err
			}
			ifExists = true
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropTrigger{Name: name, IfExists: ifExists}, nil
	case lexer.KwSchedule:
		p.next()
		ifExists := false
		if p.tok.Kind == lexer.KwIf {
			p.next()
			if err := p.expect(lexer.KwExists, "EXISTS"); err != nil {
				return nil, err
			}
			ifExists = true
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.DropSchedule{Name: name, IfExists: ifExists}, nil
	case lexer.KwResource:
		return p.dropResourceGroup()
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected TABLE, INDEX, USER, ROLE, WORKFLOW, TRIGGER, SCHEDULE, or RESOURCE GROUP")
	}
}

func (p *Parser) alter() (ast.Stmt, error) {
	p.next()
	if p.tok.Kind == lexer.KwWorkflow {
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwRename, "RENAME"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwTo, "TO"); err != nil {
			return nil, err
		}
		newName, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.AlterWorkflow{Name: name, NewName: newName}, nil
	}
	if p.tok.Kind == lexer.KwTrigger {
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwRename, "RENAME"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwTo, "TO"); err != nil {
			return nil, err
		}
		newName, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.AlterTrigger{Name: name, NewName: newName}, nil
	}
	if p.tok.Kind == lexer.KwSchedule {
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwRename, "RENAME"); err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwTo, "TO"); err != nil {
			return nil, err
		}
		newName, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.AlterSchedule{Name: name, NewName: newName}, nil
	}
	if p.tok.Kind == lexer.KwResource {
		return p.alterResourceGroup()
	}
	if err := p.expect(lexer.KwTable, "TABLE"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	cmd, err := p.alterCmd()
	if err != nil {
		return nil, err
	}
	return ast.AlterTable{Table: table, Cmd: cmd}, nil
}

func (p *Parser) alterCmd() (ast.AlterCmd, error) {
	if p.identIs("attach") {
		p.next()
		part, err := p.partitionDef("")
		if err != nil {
			return nil, err
		}
		return ast.AlterAttachPartition{Partition: part}, nil
	}
	if p.identIs("detach") {
		p.next()
		if !p.identIs("partition") {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected PARTITION after DETACH")
		}
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.AlterDetachPartition{Name: name}, nil
	}
	switch p.tok.Kind {
	case lexer.KwAdd:
		p.next()
		if p.identIs("partition") {
			part, err := p.partitionDef("")
			if err != nil {
				return nil, err
			}
			return ast.AlterAddPartition{Partition: part}, nil
		}
		if p.tok.Kind == lexer.KwConstraint || p.tok.Kind == lexer.KwForeign {
			fk, err := p.tableFK()
			if err != nil {
				return nil, err
			}
			return ast.AlterAddConstraint{FK: fk}, nil
		}
		if p.tok.Kind == lexer.KwColumn {
			p.next()
		}
		col, err := p.columnDef()
		if err != nil {
			return nil, err
		}
		if col.Primary {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "ALTER TABLE ADD COLUMN cannot add a PRIMARY KEY")
		}
		return ast.AlterAddColumn{Column: col}, nil
	case lexer.KwDrop:
		p.next()
		if p.identIs("partition") {
			p.next()
			name, err := p.ident()
			if err != nil {
				return nil, err
			}
			return ast.AlterDropPartition{Name: name}, nil
		}
		if p.tok.Kind == lexer.KwConstraint {
			p.next()
			name, err := p.ident()
			if err != nil {
				return nil, err
			}
			return ast.AlterDropConstraint{Name: name}, nil
		}
		if p.tok.Kind == lexer.KwColumn {
			p.next()
		}
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.AlterDropColumn{Name: name}, nil
	case lexer.KwRename:
		p.next()
		if p.tok.Kind == lexer.KwTo {
			p.next()
			neu, err := p.ident()
			if err != nil {
				return nil, err
			}
			return ast.AlterRenameTable{New: neu}, nil
		}
		if p.tok.Kind == lexer.KwColumn {
			p.next()
		}
		old, err := p.ident()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwTo, "TO"); err != nil {
			return nil, err
		}
		neu, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.AlterRenameColumn{Old: old, New: neu}, nil
	case lexer.KwSet:
		p.next()
		if p.tok.Kind != lexer.Ident || p.tok.Lit != "cdc" {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "ALTER TABLE SET supports CDC IMAGES")
		}
		p.next()
		if p.tok.Kind != lexer.Ident || p.tok.Lit != "images" {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected CDC IMAGES")
		}
		p.next()
		mode := strings.ToUpper(p.tok.Lit)
		if (p.tok.Kind != lexer.Ident && p.tok.Kind != lexer.KwFull) || (mode != "KEYS" && mode != "FULL") {
			return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "CDC IMAGES must be KEYS or FULL")
		}
		p.next()
		return ast.AlterSetCDCImages{Mode: mode}, nil
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected ADD, DROP, RENAME, or SET CDC IMAGES")
	}
}

func (p *Parser) grant() (ast.Stmt, error) {
	p.next()
	g, err := p.grantRevoke()
	if err != nil {
		return nil, err
	}
	return ast.Grant(g), nil
}

func (p *Parser) revoke() (ast.Stmt, error) {
	p.next()
	g, err := p.grantRevoke()
	if err != nil {
		return nil, err
	}
	// grantRevoke uses TO; REVOKE uses FROM
	return ast.Revoke(g), nil
}

type grantBits struct {
	Role       string
	Privileges []string
	All        bool
	Scope      string
	Object     string
	Grantee    string
}

func (p *Parser) grantRevoke() (grantBits, error) {
	// REVOKE uses FROM instead of TO. Accept either after the target.
	if p.tok.Kind == lexer.KwAll {
		p.next()
		if p.tok.Kind == lexer.KwPrivileges {
			p.next()
		}
		return p.finishPrivGrant(grantBits{All: true, Privileges: []string{"admin"}})
	}
	first, err := p.privOrIdent()
	if err != nil {
		return grantBits{}, err
	}
	if p.tok.Kind == lexer.Comma || p.tok.Kind == lexer.KwOn {
		privs := []string{first}
		for p.tok.Kind == lexer.Comma {
			p.next()
			n, err := p.privOrIdent()
			if err != nil {
				return grantBits{}, err
			}
			privs = append(privs, n)
		}
		return p.finishPrivGrant(grantBits{Privileges: privs})
	}
	if err := p.expectToOrFrom(); err != nil {
		return grantBits{}, err
	}
	grantee, err := p.ident()
	if err != nil {
		return grantBits{}, err
	}
	return grantBits{Role: first, Grantee: grantee}, nil
}

func (p *Parser) finishPrivGrant(g grantBits) (grantBits, error) {
	if err := p.expect(lexer.KwOn, "ON"); err != nil {
		return grantBits{}, err
	}
	kind, obj, err := p.scope()
	if err != nil {
		return grantBits{}, err
	}
	g.Scope = kind
	g.Object = obj
	if err := p.expectToOrFrom(); err != nil {
		return grantBits{}, err
	}
	grantee, err := p.ident()
	if err != nil {
		return grantBits{}, err
	}
	g.Grantee = grantee
	return g, nil
}

func (p *Parser) expectToOrFrom() error {
	if p.tok.Kind == lexer.KwTo || p.tok.Kind == lexer.KwFrom {
		p.next()
		return nil
	}
	return nerr.New(nerr.Syntax, "sql.parser", "expected TO or FROM")
}

func (p *Parser) scope() (string, string, error) {
	switch p.tok.Kind {
	case lexer.KwCluster:
		p.next()
		return "cluster", "", nil
	case lexer.KwDatabase:
		p.next()
		if p.tok.Kind == lexer.Ident {
			n, err := p.ident()
			return "database", n, err
		}
		return "database", "", nil
	case lexer.KwSchema:
		p.next()
		n, err := p.ident()
		return "schema", n, err
	case lexer.KwTable:
		p.next()
		n, err := p.ident()
		return "table", n, err
	case lexer.KwColumn:
		p.next()
		a, err := p.ident()
		if err != nil {
			return "", "", err
		}
		if p.tok.Kind == lexer.Dot {
			p.next()
			b, err := p.ident()
			if err != nil {
				return "", "", err
			}
			return "column", a + "." + b, nil
		}
		return "column", a, nil
	case lexer.KwFunction:
		p.next()
		n, err := p.ident()
		return "function", n, err
	case lexer.KwResource:
		p.next()
		if err := p.expect(lexer.KwGroup, "GROUP"); err != nil {
			return "", "", err
		}
		n, err := p.ident()
		return "resourcegroup", n, err
	case lexer.KwBackup:
		p.next()
		return "backup", "", nil
	case lexer.KwReplication:
		p.next()
		return "replication", "", nil
	case lexer.KwAdministration:
		p.next()
		return "administration", "", nil
	case lexer.Ident:
		n, err := p.ident()
		return "table", n, err
	default:
		return "", "", nerr.New(nerr.Syntax, "sql.parser", "expected a grant scope")
	}
}

func (p *Parser) privOrIdent() (string, error) {
	switch p.tok.Kind {
	case lexer.KwSelect, lexer.KwInsert, lexer.KwUpdate, lexer.KwDelete,
		lexer.KwCreate, lexer.KwDrop, lexer.KwIndex, lexer.KwConnect,
		lexer.KwExecute, lexer.KwAdmin, lexer.KwBackup, lexer.KwReplication,
		lexer.KwGrant, lexer.KwSubscribe:
		s := p.tok.Lit
		p.next()
		return s, nil
	case lexer.Ident:
		return p.ident()
	default:
		return "", nerr.New(nerr.Syntax, "sql.parser", "expected privilege or role name")
	}
}

func (p *Parser) createTable() (ast.Stmt, error) {
	p.next()
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var (
		cols []ast.ColumnDef
		pk   []string
		fks  []ast.ForeignKeyDef
	)
	for {
		if p.tok.Kind == lexer.KwPrimary {
			names, err := p.tablePK()
			if err != nil {
				return nil, err
			}
			if len(pk) > 0 {
				return nil, nerr.New(nerr.Syntax, "sql.parser", "multiple PRIMARY KEY clauses")
			}
			pk = names
		} else if p.tok.Kind == lexer.KwConstraint || p.tok.Kind == lexer.KwForeign {
			fk, err := p.tableFK()
			if err != nil {
				return nil, err
			}
			fks = append(fks, fk)
		} else {
			col, err := p.columnDef()
			if err != nil {
				return nil, err
			}
			if col.Primary {
				if len(pk) > 0 {
					return nil, nerr.New(nerr.Syntax, "sql.parser", "multiple PRIMARY KEY clauses")
				}
				pk = []string{col.Name}
			}
			cols = append(cols, col)
		}
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	var part *ast.PartitionSpec
	if p.identIs("partition") {
		spec, err := p.partitionSpec()
		if err != nil {
			return nil, err
		}
		part = spec
	}
	return ast.CreateTable{Name: name, Columns: cols, PK: pk, FKs: fks, Partition: part}, nil
}

func (p *Parser) partitionSpec() (*ast.PartitionSpec, error) {
	// partition
	p.next()
	if !(p.tok.Kind == lexer.KwBy || p.identIs("by")) {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected BY after PARTITION")
	}
	p.next()
	var kind string
	switch {
	case p.identIs("range"):
		kind = "RANGE"
		p.next()
	case p.identIs("tenant"):
		return nil, nerr.New(nerr.Syntax, "sql.parser", "PARTITION BY TENANT was removed; use an isolated hosted database")
	case p.identIs("hash"):
		kind = "HASH"
		p.next()
	case p.identIs("list"):
		kind = "LIST"
		p.next()
	default:
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected RANGE, HASH, or LIST after PARTITION BY")
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var cols []string
	for {
		n, err := p.ident()
		if err != nil {
			return nil, err
		}
		cols = append(cols, n)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	if len(cols) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "PARTITION BY requires at least one column")
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var parts []ast.PartitionDef
	for {
		def, err := p.partitionDef(kind)
		if err != nil {
			return nil, err
		}
		parts = append(parts, def)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	if len(parts) == 0 {
		return nil, nerr.New(nerr.InvalidArgument, "sql.parser", "partition list is empty")
	}
	return &ast.PartitionSpec{Kind: kind, Columns: cols, Partitions: parts}, nil
}

// partitionDef parses one PARTITION clause. expected is empty for ALTER TABLE,
// where the rule syntax identifies the kind and the binder checks it against
// the table descriptor.
func (p *Parser) partitionDef(expected string) (ast.PartitionDef, error) {
	if !p.identIs("partition") {
		return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected PARTITION")
	}
	p.next()
	name, err := p.ident()
	if err != nil {
		return ast.PartitionDef{}, err
	}
	def := ast.PartitionDef{Name: name}
	kind := expected
	if kind == "" {
		switch {
		case p.tok.Kind == lexer.KwValues || p.identIs("values"):
			// LESS and IN are distinguished after VALUES below.
		case p.identIs("modulus"):
			kind = "HASH"
		default:
			return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected VALUES or MODULUS in partition definition")
		}
	}
	if kind == "RANGE" || kind == "LIST" || kind == "TENANT" || (kind == "" && (p.tok.Kind == lexer.KwValues || p.identIs("values"))) {
		if !(p.tok.Kind == lexer.KwValues || p.identIs("values")) {
			return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected VALUES")
		}
		p.next()
		if kind == "" {
			if p.identIs("less") {
				kind = "RANGE"
			} else if p.tok.Kind == lexer.KwIn || p.identIs("in") {
				kind = "VALUE"
			} else {
				return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected LESS THAN or IN")
			}
		}
		if kind == "RANGE" {
			def.Rule = "RANGE"
			if !p.identIs("less") {
				return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected LESS THAN for RANGE partition")
			}
			p.next()
			if !p.identIs("than") {
				return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected THAN")
			}
			p.next()
			if p.identIs("maxvalue") {
				p.next()
				return def, nil
			}
			if err := p.expect(lexer.LParen, "("); err != nil {
				return ast.PartitionDef{}, err
			}
			var bounds []ast.Expr
			for {
				ex, err := p.or()
				if err != nil {
					return ast.PartitionDef{}, err
				}
				bounds = append(bounds, ex)
				if p.tok.Kind == lexer.Comma {
					p.next()
					continue
				}
				break
			}
			if err := p.expect(lexer.RParen, ")"); err != nil {
				return ast.PartitionDef{}, err
			}
			if len(bounds) == 1 {
				def.LessThan = bounds[0]
			} else {
				def.LessThanTuple = bounds
			}
			return def, nil
		}
		// VALUE is valid for both LIST and TENANT; arity is a binder rule.
		def.Rule = "VALUE"
		if !(p.tok.Kind == lexer.KwIn || p.identIs("in")) {
			return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected IN for value partition")
		}
		p.next()
		if err := p.expect(lexer.LParen, "("); err != nil {
			return ast.PartitionDef{}, err
		}
		if p.tok.Kind == lexer.LParen {
			// Multi-column LIST: VALUES IN ((a, b), (c, d), ...).
			for {
				if err := p.expect(lexer.LParen, "("); err != nil {
					return ast.PartitionDef{}, err
				}
				var tuple []ast.Expr
				for {
					ex, err := p.or()
					if err != nil {
						return ast.PartitionDef{}, err
					}
					tuple = append(tuple, ex)
					if p.tok.Kind == lexer.Comma {
						p.next()
						continue
					}
					break
				}
				if err := p.expect(lexer.RParen, ")"); err != nil {
					return ast.PartitionDef{}, err
				}
				def.ValueTuples = append(def.ValueTuples, tuple)
				if p.tok.Kind == lexer.Comma {
					p.next()
					continue
				}
				break
			}
		} else {
			for {
				ex, err := p.or()
				if err != nil {
					return ast.PartitionDef{}, err
				}
				def.Values = append(def.Values, ex)
				if p.tok.Kind != lexer.Comma {
					break
				}
				p.next()
			}
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return ast.PartitionDef{}, err
		}
		if expected == "TENANT" && len(def.Values) != 1 {
			return ast.PartitionDef{}, nerr.New(nerr.InvalidArgument, "sql.parser", "TENANT partition requires one value")
		}
		return def, nil
	}
	if kind != "HASH" {
		return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "unknown partition rule")
	}
	def.Rule = "HASH"
	if !p.identIs("modulus") {
		return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected MODULUS for HASH partition")
	}
	p.next()
	modulus, err := p.uintLit()
	if err != nil {
		return ast.PartitionDef{}, err
	}
	if !p.identIs("remainder") {
		return ast.PartitionDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected REMAINDER for HASH partition")
	}
	p.next()
	remainder, err := p.uintLit()
	if err != nil {
		return ast.PartitionDef{}, err
	}
	def.Modulus = uint32(modulus)
	def.Remainder = uint32(remainder)
	return def, nil
}

func (p *Parser) tablePK() ([]string, error) {
	p.next()
	if err := p.expect(lexer.KwKey, "KEY"); err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var names []string
	for {
		n, err := p.ident()
		if err != nil {
			return nil, err
		}
		names = append(names, n)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return names, nil
}

func (p *Parser) columnDef() (ast.ColumnDef, error) {
	name, err := p.ident()
	if err != nil {
		return ast.ColumnDef{}, err
	}
	typ, err := p.colType()
	if err != nil {
		return ast.ColumnDef{}, err
	}
	col := ast.ColumnDef{Name: name, Type: typ}
	for {
		switch p.tok.Kind {
		case lexer.KwEncrypted:
			if col.EncryptedClient {
				return ast.ColumnDef{}, nerr.New(nerr.Syntax, "sql.parser", "multiple ENCRYPTED CLIENT clauses")
			}
			p.next()
			if err := p.expect(lexer.KwClient, "CLIENT"); err != nil {
				return ast.ColumnDef{}, err
			}
			col.EncryptedClient = true
		case lexer.KwNot:
			p.next()
			if err := p.expect(lexer.KwNull, "NULL"); err != nil {
				return ast.ColumnDef{}, err
			}
			col.NotNull = true
		case lexer.KwPrimary:
			p.next()
			if err := p.expect(lexer.KwKey, "KEY"); err != nil {
				return ast.ColumnDef{}, err
			}
			col.Primary = true
			col.NotNull = true
		case lexer.KwDefault:
			p.next()
			ex, err := p.primary()
			if err != nil {
				return ast.ColumnDef{}, err
			}
			col.Default = ex
		case lexer.KwConstraint:
			if col.References != nil {
				return ast.ColumnDef{}, nerr.New(nerr.Syntax, "sql.parser", "multiple REFERENCES clauses")
			}
			p.next()
			cname, err := p.ident()
			if err != nil {
				return ast.ColumnDef{}, err
			}
			fk, err := p.references(cname, []string{col.Name})
			if err != nil {
				return ast.ColumnDef{}, err
			}
			col.References = &fk
		case lexer.KwReferences:
			if col.References != nil {
				return ast.ColumnDef{}, nerr.New(nerr.Syntax, "sql.parser", "multiple REFERENCES clauses")
			}
			fk, err := p.references("", []string{col.Name})
			if err != nil {
				return ast.ColumnDef{}, err
			}
			col.References = &fk
		default:
			return col, nil
		}
	}
}

func (p *Parser) tableFK() (ast.ForeignKeyDef, error) {
	name := ""
	if p.tok.Kind == lexer.KwConstraint {
		p.next()
		n, err := p.ident()
		if err != nil {
			return ast.ForeignKeyDef{}, err
		}
		name = n
	}
	if err := p.expect(lexer.KwForeign, "FOREIGN"); err != nil {
		return ast.ForeignKeyDef{}, err
	}
	if err := p.expect(lexer.KwKey, "KEY"); err != nil {
		return ast.ForeignKeyDef{}, err
	}
	cols, err := p.identList()
	if err != nil {
		return ast.ForeignKeyDef{}, err
	}
	return p.references(name, cols)
}

func (p *Parser) identList() ([]string, error) {
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var names []string
	for {
		n, err := p.ident()
		if err != nil {
			return nil, err
		}
		names = append(names, n)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "empty column list")
	}
	return names, nil
}

func (p *Parser) references(name string, cols []string) (ast.ForeignKeyDef, error) {
	if err := p.expect(lexer.KwReferences, "REFERENCES"); err != nil {
		return ast.ForeignKeyDef{}, err
	}
	tab, err := p.ident()
	if err != nil {
		return ast.ForeignKeyDef{}, err
	}
	refCols, err := p.identList()
	if err != nil {
		return ast.ForeignKeyDef{}, err
	}
	fk := ast.ForeignKeyDef{Name: name, Columns: cols, RefTable: tab, RefCols: refCols}
	var haveDel, haveUp bool
	for {
		switch p.tok.Kind {
		case lexer.KwMatch:
			p.next()
			if p.tok.Kind != lexer.Ident {
				return ast.ForeignKeyDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected SIMPLE")
			}
			switch p.tok.Lit {
			case "simple":
				p.next()
			case "full":
				return ast.ForeignKeyDef{}, nerr.New(nerr.InvalidArgument, "sql.parser", "MATCH FULL is not supported")
			default:
				return ast.ForeignKeyDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected SIMPLE")
			}
		case lexer.KwOn:
			p.next()
			switch p.tok.Kind {
			case lexer.KwDelete:
				if haveDel {
					return ast.ForeignKeyDef{}, nerr.New(nerr.Syntax, "sql.parser", "duplicate ON DELETE")
				}
				p.next()
				act, err := p.refAction()
				if err != nil {
					return ast.ForeignKeyDef{}, err
				}
				fk.OnDelete = act
				haveDel = true
			case lexer.KwUpdate:
				if haveUp {
					return ast.ForeignKeyDef{}, nerr.New(nerr.Syntax, "sql.parser", "duplicate ON UPDATE")
				}
				p.next()
				act, err := p.refAction()
				if err != nil {
					return ast.ForeignKeyDef{}, err
				}
				fk.OnUpdate = act
				haveUp = true
			default:
				return ast.ForeignKeyDef{}, nerr.New(nerr.Syntax, "sql.parser", "expected DELETE or UPDATE")
			}
		default:
			return fk, nil
		}
	}
}

func (p *Parser) refAction() (ast.FKAction, error) {
	switch p.tok.Kind {
	case lexer.KwCascade:
		p.next()
		return ast.FKCascade, nil
	case lexer.KwRestrict:
		p.next()
		return ast.FKRestrict, nil
	case lexer.KwSet:
		p.next()
		switch p.tok.Kind {
		case lexer.KwNull:
			p.next()
			return ast.FKSetNull, nil
		case lexer.KwDefault:
			p.next()
			return ast.FKSetDefault, nil
		default:
			return 0, nerr.New(nerr.Syntax, "sql.parser", "expected NULL or DEFAULT")
		}
	case lexer.Ident:
		if p.tok.Lit == "no" {
			p.next()
			if err := p.expect(lexer.KwAction, "ACTION"); err != nil {
				return 0, err
			}
			return ast.FKRestrict, nil
		}
		return 0, nerr.New(nerr.Syntax, "sql.parser", "expected CASCADE, RESTRICT, NO ACTION, SET NULL, or SET DEFAULT")
	default:
		return 0, nerr.New(nerr.Syntax, "sql.parser", "expected CASCADE, RESTRICT, NO ACTION, SET NULL, or SET DEFAULT")
	}
}

func (p *Parser) colType() (types.Type, error) {
	switch p.tok.Kind {
	case lexer.KwUuid:
		p.next()
		return types.UUID(), nil
	case lexer.KwString:
		p.next()
		return types.String(), nil
	case lexer.KwText:
		p.next()
		return types.Text(), nil
	case lexer.KwBlob:
		p.next()
		return types.Blob(), nil
	case lexer.KwInt8:
		p.next()
		return types.Int8(), nil
	case lexer.KwInt16:
		p.next()
		return types.Int16(), nil
	case lexer.KwInt32:
		p.next()
		return types.Int32(), nil
	case lexer.KwInt64:
		p.next()
		return types.Int64(), nil
	case lexer.KwUint8:
		p.next()
		return types.Uint8(), nil
	case lexer.KwUint16:
		p.next()
		return types.Uint16(), nil
	case lexer.KwUint32:
		p.next()
		return types.Uint32(), nil
	case lexer.KwUint64:
		p.next()
		return types.Uint64(), nil
	case lexer.KwChar:
		p.next()
		n, err := p.charLen()
		if err != nil {
			return types.Type{}, err
		}
		return types.CharType(n)
	case lexer.KwVarchar:
		p.next()
		n, err := p.charLen()
		if err != nil {
			return types.Type{}, err
		}
		return types.VarcharType(n)
	case lexer.KwTimestamptz:
		p.next()
		return types.TimestampTZ(), nil
	case lexer.KwTimestamp:
		p.next()
		return types.Timestamp(), nil
	case lexer.KwFloat32:
		p.next()
		return types.Float32(), nil
	case lexer.KwFloat64:
		p.next()
		return types.Float64(), nil
	case lexer.KwEnum:
		p.next()
		if err := p.expect(lexer.LParen, "("); err != nil {
			return types.Type{}, err
		}
		var labels []string
		for {
			if p.tok.Kind != lexer.String {
				return types.Type{}, nerr.New(nerr.Syntax, "sql.parser", "expected a quoted ENUM label")
			}
			labels = append(labels, p.tok.Lit)
			p.next()
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return types.Type{}, err
		}
		return types.EnumType(labels)
	case lexer.KwDate:
		p.next()
		return types.Date(), nil
	case lexer.KwTime:
		p.next()
		return types.TimeOfDay(), nil
	case lexer.KwInterval:
		p.next()
		return types.Interval(), nil
	case lexer.KwJson:
		p.next()
		return types.JSON(), nil
	case lexer.KwDecimal:
		p.next()
		if err := p.expect(lexer.LParen, "("); err != nil {
			return types.Type{}, err
		}
		prec, err := p.uintLit()
		if err != nil {
			return types.Type{}, err
		}
		if err := p.expect(lexer.Comma, ","); err != nil {
			return types.Type{}, err
		}
		scale, err := p.uintLit()
		if err != nil {
			return types.Type{}, err
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return types.Type{}, err
		}
		return types.DecimalType(uint16(prec), uint16(scale))
	case lexer.KwPoint, lexer.KwLocation:
		p.next()
		return types.Point(), nil
	case lexer.KwBox:
		p.next()
		return types.Box(), nil
	case lexer.KwLineString:
		p.next()
		return types.Line(), nil
	case lexer.KwPolygon:
		p.next()
		return types.Polygon(), nil
	case lexer.KwBitvector:
		p.next()
		if err := p.expect(lexer.Lt, "<"); err != nil {
			return types.Type{}, err
		}
		n, err := p.uintLit()
		if err != nil {
			return types.Type{}, err
		}
		if err := p.expect(lexer.Gt, ">"); err != nil {
			return types.Type{}, err
		}
		return types.VectorBit(uint16(n))
	case lexer.KwSparsevector:
		p.next()
		if err := p.expect(lexer.Lt, "<"); err != nil {
			return types.Type{}, err
		}
		n, err := p.uintLit()
		if err != nil {
			return types.Type{}, err
		}
		if n < 1 || n > uint64(types.MaxSparseSQLDim) {
			return types.Type{}, nerr.New(nerr.InvalidArgument, "sql.parser", "SPARSEVECTOR dimension out of range")
		}
		if err := p.expect(lexer.Gt, ">"); err != nil {
			return types.Type{}, err
		}
		return types.VectorSparse(uint16(n))
	case lexer.KwVector:
		p.next()
		if err := p.expect(lexer.Lt, "<"); err != nil {
			return types.Type{}, err
		}
		elem := p.tok.Kind
		if elem != lexer.KwF32 && elem != lexer.KwF16 && elem != lexer.KwI8 {
			return types.Type{}, nerr.New(nerr.Syntax, "sql.parser", "expected F32, F16, or I8")
		}
		p.next()
		if err := p.expect(lexer.Comma, ","); err != nil {
			return types.Type{}, err
		}
		n, err := p.uintLit()
		if err != nil {
			return types.Type{}, err
		}
		if err := p.expect(lexer.Gt, ">"); err != nil {
			return types.Type{}, err
		}
		switch elem {
		case lexer.KwF16:
			return types.VectorF16(uint16(n))
		case lexer.KwI8:
			return types.VectorI8(uint16(n))
		}
		return types.VectorF32(uint16(n))
	default:
		return types.Type{}, nerr.New(nerr.Syntax, "sql.parser", "expected a type")
	}
}

// charLen parses the mandatory "(n)" length argument of CHAR(n) / VARCHAR(n).
func (p *Parser) charLen() (uint16, error) {
	if err := p.expect(lexer.LParen, "("); err != nil {
		return 0, err
	}
	n, err := p.uintLit()
	if err != nil {
		return 0, err
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return 0, err
	}
	if n < 1 || n > uint64(types.MaxCharLen) {
		return 0, nerr.New(nerr.InvalidArgument, "sql.parser", "CHAR/VARCHAR length out of range")
	}
	return uint16(n), nil
}

func (p *Parser) createIndex(unique, spatial, fulltext, vector bool) (ast.Stmt, error) {
	p.next()
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwOn, "ON"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var cols []string
	var keys [][]string
	var exprs []ast.Expr
	for {
		col, parts, expr, err := p.indexKey()
		if err != nil {
			return nil, err
		}
		cols = append(cols, col)
		keys = append(keys, parts)
		exprs = append(exprs, expr)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	var include []string
	if p.identIs("include") {
		p.next()
		if err := p.expect(lexer.LParen, "("); err != nil {
			return nil, err
		}
		for {
			c, err := p.ident()
			if err != nil {
				return nil, err
			}
			include = append(include, c)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
	}
	var where ast.Expr
	if p.tok.Kind == lexer.KwWhere {
		p.next()
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		where = ex
	}
	using := ""
	vecQuant := ""
	ivfLists := 0
	ivfProbes := 0
	ivfSubspaces := 0
	analyzer := ""
	if fulltext && p.tok.Kind == lexer.KwWith {
		p.next()
		if err := p.expect(lexer.LParen, "("); err != nil {
			return nil, err
		}
		if !p.identIs("analyzer") {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected ANALYZER")
		}
		p.next()
		if err := p.expect(lexer.Eq, "="); err != nil {
			return nil, err
		}
		if p.tok.Kind != lexer.String && p.tok.Kind != lexer.Ident {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected analyzer name")
		}
		analyzer = strings.ToLower(p.tok.Lit)
		p.next()
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
	}
	if vector {
		if err := p.expect(lexer.KwUsing, "USING"); err != nil {
			return nil, err
		}
		switch {
		case p.tok.Kind == lexer.KwHnsw:
			p.next()
			using = "hnsw"
			if p.tok.Kind == lexer.KwWith {
				p.next()
				if err := p.expect(lexer.LParen, "("); err != nil {
					return nil, err
				}
				if !p.identIs("quantization") {
					return nil, nerr.New(nerr.Syntax, "sql.parser", "expected QUANTIZATION")
				}
				p.next()
				if err := p.expect(lexer.Eq, "="); err != nil {
					return nil, err
				}
				if p.tok.Kind != lexer.String && p.tok.Kind != lexer.Ident {
					return nil, nerr.New(nerr.Syntax, "sql.parser", "expected quantisation name")
				}
				vecQuant = strings.ToLower(p.tok.Lit)
				p.next()
				if err := p.expect(lexer.RParen, ")"); err != nil {
					return nil, err
				}
			}
		case p.identIs("sparse"):
			p.next()
			using = "sparse"
			if p.tok.Kind == lexer.KwWith {
				return nil, nerr.New(nerr.Syntax, "sql.parser", "USING SPARSE does not take WITH options")
			}
		case p.identIs("ivf"), p.identIs("ivfpq"):
			if p.identIs("ivfpq") {
				using = "ivfpq"
			} else {
				using = "ivf"
			}
			p.next()
			if p.tok.Kind == lexer.KwWith {
				p.next()
				if err := p.expect(lexer.LParen, "("); err != nil {
					return nil, err
				}
				for {
					switch {
					case p.identIs("lists"):
						p.next()
						if err := p.expect(lexer.Eq, "="); err != nil {
							return nil, err
						}
						n, err := p.uintLit()
						if err != nil {
							return nil, err
						}
						ivfLists = int(n)
					case p.identIs("probes"):
						p.next()
						if err := p.expect(lexer.Eq, "="); err != nil {
							return nil, err
						}
						n, err := p.uintLit()
						if err != nil {
							return nil, err
						}
						ivfProbes = int(n)
					case p.identIs("subspaces"):
						p.next()
						if err := p.expect(lexer.Eq, "="); err != nil {
							return nil, err
						}
						n, err := p.uintLit()
						if err != nil {
							return nil, err
						}
						ivfSubspaces = int(n)
					default:
						return nil, nerr.New(nerr.Syntax, "sql.parser", "expected LISTS, PROBES, or SUBSPACES")
					}
					if p.tok.Kind == lexer.Comma {
						p.next()
						continue
					}
					break
				}
				if err := p.expect(lexer.RParen, ")"); err != nil {
					return nil, err
				}
			}
		default:
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected HNSW, IVF, IVFPQ, or SPARSE")
		}
	}
	hasExpr := false
	for _, e := range exprs {
		if e != nil {
			hasExpr = true
			break
		}
	}
	if !hasExpr {
		exprs = nil
	}
	return ast.CreateIndex{Name: name, Table: table, Unique: unique, Spatial: spatial, Fulltext: fulltext, Vector: vector, Using: using, VecQuant: vecQuant, IVFLists: ivfLists, IVFProbes: ivfProbes, IVFSubspaces: ivfSubspaces, Analyzer: analyzer, Cols: cols, Keys: keys, Exprs: exprs, Include: include, Where: where}, nil
}

func (p *Parser) indexKey() (string, []string, ast.Expr, error) {
	if p.tok.Kind == lexer.LParen {
		p.next()
		if p.tok.Kind == lexer.KwSelect || p.tok.Kind == lexer.KwWith {
			return "", nil, nil, nerr.New(nerr.Syntax, "sql.parser", "index key cannot be a subquery")
		}
		ex, err := p.or()
		if err != nil {
			return "", nil, nil, err
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return "", nil, nil, err
		}
		return flattenIndexExpr(ex)
	}
	if p.tok.Kind == lexer.Ident {
		c, err := p.ident()
		if err != nil {
			return "", nil, nil, err
		}
		if p.tok.Kind == lexer.LParen {
			p.next()
			var args []ast.Expr
			if p.tok.Kind != lexer.RParen {
				for {
					a, err := p.or()
					if err != nil {
						return "", nil, nil, err
					}
					args = append(args, a)
					if p.tok.Kind == lexer.Comma {
						p.next()
						continue
					}
					break
				}
			}
			if err := p.expect(lexer.RParen, ")"); err != nil {
				return "", nil, nil, err
			}
			return "", nil, ast.Call{Name: c, Args: args}, nil
		}
		parts := []string{c}
		for p.tok.Kind == lexer.Dot {
			p.next()
			part, err := p.ident()
			if err != nil {
				return "", nil, nil, err
			}
			parts = append(parts, part)
		}
		return parts[0], parts, nil, nil
	}
	switch p.tok.Kind {
	case lexer.KwUuid, lexer.KwPoint, lexer.KwBox, lexer.KwLocation, lexer.KwLineString, lexer.KwPolygon, lexer.KwCosine, lexer.KwL2, lexer.KwInnerProduct:
		ex, err := p.nameOrCall()
		if err != nil {
			return "", nil, nil, err
		}
		return flattenIndexExpr(ex)
	}
	if err := p.lx.Err(); err != nil {
		return "", nil, nil, err
	}
	return "", nil, nil, nerr.New(nerr.Syntax, "sql.parser", "expected an index key")
}

func flattenIndexExpr(ex ast.Expr) (string, []string, ast.Expr, error) {
	switch x := ex.(type) {
	case ast.Ident:
		return x.Name, []string{x.Name}, nil, nil
	case ast.Path:
		if len(x.Parts) == 0 {
			return "", nil, nil, nerr.New(nerr.Syntax, "sql.parser", "empty index key")
		}
		return x.Parts[0], x.Parts, nil, nil
	default:
		return "", nil, ex, nil
	}
}

func (p *Parser) insert() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwInto, "INTO"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	var cols []string
	if p.tok.Kind == lexer.LParen {
		p.next()
		for {
			c, err := p.ident()
			if err != nil {
				return nil, err
			}
			cols = append(cols, c)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
	}
	if err := p.expect(lexer.KwValues, "VALUES"); err != nil {
		return nil, err
	}
	var rows [][]ast.Expr
	for {
		row, err := p.tuple()
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	ret, star, err := p.returning()
	if err != nil {
		return nil, err
	}
	return ast.Insert{Table: table, Columns: cols, Rows: rows, Returning: ret, ReturningStar: star}, nil
}

func (p *Parser) upsert() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwInto, "INTO"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	var cols []string
	if p.tok.Kind == lexer.LParen {
		p.next()
		for {
			c, err := p.ident()
			if err != nil {
				return nil, err
			}
			cols = append(cols, c)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
	}
	if err := p.expect(lexer.KwValues, "VALUES"); err != nil {
		return nil, err
	}
	var rows [][]ast.Expr
	for {
		row, err := p.tuple()
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	var onUnique []string
	if p.tok.Kind == lexer.KwOn {
		p.next()
		if err := p.expect(lexer.KwUnique, "UNIQUE"); err != nil {
			return nil, err
		}
		onUnique, err = p.identList()
		if err != nil {
			return nil, err
		}
	}
	var sets []ast.Assignment
	if p.tok.Kind == lexer.KwSet {
		sets, err = p.assignments()
		if err != nil {
			return nil, err
		}
	}
	ret, star, err := p.returning()
	if err != nil {
		return nil, err
	}
	return ast.Upsert{Table: table, Columns: cols, Rows: rows, OnUnique: onUnique, Sets: sets, Returning: ret, ReturningStar: star}, nil
}

func (p *Parser) returning() ([]ast.SelectItem, bool, error) {
	if p.tok.Kind != lexer.KwReturning {
		return nil, false, nil
	}
	p.next()
	if p.tok.Kind == lexer.Star {
		p.next()
		return nil, true, nil
	}
	var list []ast.SelectItem
	for {
		item, err := p.selectItem()
		if err != nil {
			return nil, false, err
		}
		list = append(list, item)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if len(list) == 0 {
		return nil, false, nerr.New(nerr.Syntax, "sql.parser", "RETURNING requires a select list")
	}
	return list, false, nil
}

func (p *Parser) assignments() ([]ast.Assignment, error) {
	if err := p.expect(lexer.KwSet, "SET"); err != nil {
		return nil, err
	}
	var sets []ast.Assignment
	for {
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.Eq, "="); err != nil {
			return nil, err
		}
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		sets = append(sets, ast.Assignment{Name: name, Expr: ex})
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if len(sets) == 0 {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "SET has no assignments")
	}
	return sets, nil
}

func (p *Parser) tuple() ([]ast.Expr, error) {
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	var out []ast.Expr
	for {
		e, err := p.or()
		if err != nil {
			return nil, err
		}
		out = append(out, e)
		if p.tok.Kind == lexer.Comma {
			p.next()
			continue
		}
		break
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Parser) sel() (ast.Stmt, error) {
	p.next()
	s := ast.Select{}
	if p.tok.Kind == lexer.KwDistinct {
		s.Distinct = true
		p.next()
	}
	if p.tok.Kind == lexer.Star {
		s.Star = true
		p.next()
	} else {
		for {
			item, err := p.selectItem()
			if err != nil {
				return nil, err
			}
			s.List = append(s.List, item)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
	}
	if err := p.expect(lexer.KwFrom, "FROM"); err != nil {
		return nil, err
	}
	if p.tok.Kind == lexer.LParen {
		p.next()
		if p.tok.Kind != lexer.KwSelect && p.tok.Kind != lexer.KwWith {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "derived table requires a SELECT query")
		}
		query, err := p.queryOrWith()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
		if p.tok.Kind == lexer.KwAs {
			p.next()
		}
		alias, err := p.ident()
		if err != nil {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "derived table requires an alias")
		}
		s.FromQuery = query
		s.Alias = alias
	} else {
		name, alias, err := p.tableRef()
		if err != nil {
			return nil, err
		}
		s.Table = name
		s.Alias = alias
	}
	for {
		kind := ast.JoinInner
		switch p.tok.Kind {
		case lexer.KwJoin:
			p.next()
		case lexer.KwInner:
			p.next()
			if err := p.expect(lexer.KwJoin, "JOIN"); err != nil {
				return nil, err
			}
		case lexer.KwLeft:
			p.next()
			if p.tok.Kind == lexer.KwOuter {
				p.next()
			}
			if err := p.expect(lexer.KwJoin, "JOIN"); err != nil {
				return nil, err
			}
			kind = ast.JoinLeft
		case lexer.KwRight:
			p.next()
			if p.tok.Kind == lexer.KwOuter {
				p.next()
			}
			if err := p.expect(lexer.KwJoin, "JOIN"); err != nil {
				return nil, err
			}
			kind = ast.JoinRight
		case lexer.KwFull:
			p.next()
			if p.tok.Kind == lexer.KwOuter {
				p.next()
			}
			if err := p.expect(lexer.KwJoin, "JOIN"); err != nil {
				return nil, err
			}
			kind = ast.JoinFull
		case lexer.KwCross:
			p.next()
			if err := p.expect(lexer.KwJoin, "JOIN"); err != nil {
				return nil, err
			}
			kind = ast.JoinCross
		default:
			goto joinsDone
		}
		jn, ja, err := p.tableRef()
		if err != nil {
			return nil, err
		}
		js := ast.JoinSpec{Table: jn, Alias: ja, Kind: kind}
		if p.tok.Kind == lexer.KwOn {
			if kind == ast.JoinCross {
				return nil, nerr.New(nerr.Syntax, "sql.parser", "CROSS JOIN does not take ON")
			}
			p.next()
			on, err := p.or()
			if err != nil {
				return nil, err
			}
			js.On = on
		} else if kind == ast.JoinLeft || kind == ast.JoinRight || kind == ast.JoinFull {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "outer join requires ON")
		} else {
			js.Kind = ast.JoinCross
			js.Cross = true
		}
		if js.Kind == ast.JoinCross {
			js.Cross = true
		}
		s.Joins = append(s.Joins, js)
	}
joinsDone:
	if p.tok.Kind == lexer.KwWhere {
		p.next()
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		s.Where = ex
	}
	if p.tok.Kind == lexer.KwGroup {
		p.next()
		if err := p.expect(lexer.KwBy, "BY"); err != nil {
			return nil, err
		}
		for {
			g, err := p.or()
			if err != nil {
				return nil, err
			}
			s.Group = append(s.Group, g)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
	}
	if p.tok.Kind == lexer.KwHaving {
		p.next()
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		s.Having = ex
	}
	if p.tok.Kind == lexer.KwSearch {
		p.next()
		var cols []string
		var weights []float64
		weighted := false
		for {
			col, w, err := p.searchField()
			if err != nil {
				return nil, err
			}
			cols = append(cols, col)
			weights = append(weights, w)
			if w != 1 {
				weighted = true
			}
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
		if err := p.expect(lexer.KwFor, "FOR"); err != nil {
			return nil, err
		}
		q, err := p.or()
		if err != nil {
			return nil, err
		}
		s.SearchCols = cols
		if weighted {
			s.SearchWeights = weights
		}
		s.SearchQuery = q
	}
	for n := 0; p.tok.Kind == lexer.KwNearest; n++ {
		if n >= 2 {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "at most two NEAREST clauses")
		}
		col, q, metric, err := p.nearestClause()
		if err != nil {
			return nil, err
		}
		if n == 0 {
			s.NearestCol = col
			s.NearestQuery = q
			s.NearestMetric = metric
			continue
		}
		s.Nearest2Col = col
		s.Nearest2Query = q
		s.Nearest2Metric = metric
	}
	if p.identIs("facet") {
		p.next()
		for {
			col, err := p.ident()
			if err != nil {
				return nil, err
			}
			s.FacetCols = append(s.FacetCols, col)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
	}
	if p.tok.Kind == lexer.KwOrder {
		p.next()
		if err := p.expect(lexer.KwBy, "BY"); err != nil {
			return nil, err
		}
		for {
			item, err := p.orderItem()
			if err != nil {
				return nil, err
			}
			s.Order = append(s.Order, item)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
	}
	lim, off, err := p.limitOffset()
	if err != nil {
		return nil, err
	}
	s.Limit = lim
	s.Offset = off
	return s, nil
}

func (p *Parser) nearestClause() (col string, q ast.Expr, metric string, err error) {
	p.next()
	col, err = p.ident()
	if err != nil {
		return "", nil, "", err
	}
	if err := p.expect(lexer.KwTo, "TO"); err != nil {
		return "", nil, "", err
	}
	q, err = p.or()
	if err != nil {
		return "", nil, "", err
	}
	if p.tok.Kind == lexer.KwUsing {
		p.next()
		switch p.tok.Kind {
		case lexer.KwCosine:
			metric = "cosine"
		case lexer.KwL2:
			metric = "l2"
		case lexer.KwInnerProduct:
			metric = "inner_product"
		case lexer.KwHamming:
			metric = "hamming"
		default:
			return "", nil, "", nerr.New(nerr.Syntax, "sql.parser", "expected COSINE, L2, INNER_PRODUCT, or HAMMING")
		}
		p.next()
	}
	return col, q, metric, nil
}

func (p *Parser) limitOffset() (lim *int64, off *int64, err error) {
	for {
		switch p.tok.Kind {
		case lexer.KwLimit:
			if lim != nil {
				return nil, nil, nerr.New(nerr.Syntax, "sql.parser", "duplicate LIMIT")
			}
			p.next()
			n, err := p.uintLit()
			if err != nil {
				return nil, nil, err
			}
			v := int64(n)
			lim = &v
		case lexer.KwOffset:
			if off != nil {
				return nil, nil, nerr.New(nerr.Syntax, "sql.parser", "duplicate OFFSET")
			}
			p.next()
			n, err := p.uintLit()
			if err != nil {
				return nil, nil, err
			}
			v := int64(n)
			off = &v
		default:
			return lim, off, nil
		}
	}
}

func (p *Parser) tableRef() (name, alias string, err error) {
	name, err = p.identOrKeyword()
	if err != nil {
		return "", "", err
	}
	for p.tok.Kind == lexer.Dot {
		p.next()
		part, err := p.identOrKeyword()
		if err != nil {
			return "", "", err
		}
		name = name + "." + part
	}
	if p.tok.Kind == lexer.KwAs {
		p.next()
		alias, err = p.ident()
		if err != nil {
			return "", "", err
		}
		return name, alias, nil
	}
	if p.tok.Kind == lexer.Ident {
		alias = p.tok.Lit
		p.next()
	}
	return name, alias, nil
}

func (p *Parser) identOrKeyword() (string, error) {
	if p.tok.Kind == lexer.Ident {
		s := p.tok.Lit
		p.next()
		return s, nil
	}
	if p.tok.Kind >= lexer.KwCreate && p.tok.Kind <= lexer.KwSubscribe {
		s := p.tok.Lit
		p.next()
		return s, nil
	}
	if p.tok.Kind != lexer.Ident {
		if err := p.lx.Err(); err != nil {
			return "", err
		}
		return "", nerr.New(nerr.Syntax, "sql.parser", "expected identifier")
	}
	s := p.tok.Lit
	p.next()
	return s, nil
}

func (p *Parser) selectItem() (ast.SelectItem, error) {
	ex, err := p.or()
	if err != nil {
		return ast.SelectItem{}, err
	}
	alias := ""
	if p.tok.Kind == lexer.KwAs {
		p.next()
		alias, err = p.ident()
		if err != nil {
			return ast.SelectItem{}, err
		}
	} else if p.tok.Kind == lexer.Ident {
		alias = p.tok.Lit
		p.next()
	}
	return ast.SelectItem{Expr: ex, Alias: alias}, nil
}

func (p *Parser) update() (ast.Stmt, error) {
	p.next()
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	sets, err := p.assignments()
	if err != nil {
		return nil, err
	}
	var where ast.Expr
	if p.tok.Kind == lexer.KwWhere {
		p.next()
		where, err = p.or()
		if err != nil {
			return nil, err
		}
	}
	lim, err := p.optionalLimit()
	if err != nil {
		return nil, err
	}
	ret, star, err := p.returning()
	if err != nil {
		return nil, err
	}
	return ast.Update{Table: table, Sets: sets, Where: where, Limit: lim, Returning: ret, ReturningStar: star}, nil
}

func (p *Parser) del() (ast.Stmt, error) {
	p.next()
	if err := p.expect(lexer.KwFrom, "FROM"); err != nil {
		return nil, err
	}
	table, err := p.ident()
	if err != nil {
		return nil, err
	}
	var where ast.Expr
	if p.tok.Kind == lexer.KwWhere {
		p.next()
		where, err = p.or()
		if err != nil {
			return nil, err
		}
	}
	lim, err := p.optionalLimit()
	if err != nil {
		return nil, err
	}
	ret, star, err := p.returning()
	if err != nil {
		return nil, err
	}
	return ast.Delete{Table: table, Where: where, Limit: lim, Returning: ret, ReturningStar: star}, nil
}

func (p *Parser) optionalLimit() (int64, error) {
	if p.tok.Kind != lexer.KwLimit {
		return 0, nil
	}
	p.next()
	n, err := p.uintLit()
	if err != nil {
		return 0, err
	}
	if n < 1 {
		return 0, nerr.New(nerr.InvalidArgument, "sql.parser", "LIMIT must be >= 1")
	}
	return int64(n), nil
}

func (p *Parser) explain() (ast.Stmt, error) {
	p.next()
	analyze := false
	if p.tok.Kind == lexer.KwAnalyze {
		analyze = true
		p.next()
	}
	inner, err := p.stmt()
	if err != nil {
		return nil, err
	}
	if _, ok := inner.(ast.Explain); ok {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "nested EXPLAIN")
	}
	return ast.Explain{Analyze: analyze, Stmt: inner}, nil
}

func (p *Parser) analyze() (ast.Stmt, error) {
	p.next()
	name := ""
	if p.tok.Kind == lexer.Ident {
		name = p.tok.Lit
		p.next()
	}
	return ast.Analyze{Table: name}, nil
}

func (p *Parser) maintain() (ast.Stmt, error) {
	p.next()
	if p.tok.Kind == lexer.KwDatabase {
		p.next()
		return ast.Maintain{}, nil
	}
	if p.tok.Kind == lexer.KwIndex {
		p.next()
		name, err := p.ident()
		if err != nil {
			return nil, err
		}
		return ast.Maintain{Index: name}, nil
	}
	if p.tok.Kind != lexer.KwTable {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "MAINTAIN requires DATABASE, TABLE, or INDEX")
	}
	p.next()
	name, err := p.ident()
	if err != nil {
		return nil, err
	}
	return ast.Maintain{Table: name}, nil
}

func (p *Parser) begin() (ast.Stmt, error) {
	p.next()
	if p.tok.Kind == lexer.KwTransaction {
		p.next()
	}
	b := ast.Begin{}
	switch p.tok.Kind {
	case lexer.KwRead:
		p.next()
		if err := p.expect(lexer.KwCommitted, "COMMITTED"); err != nil {
			return nil, err
		}
		b.Isolation = "read committed"
	case lexer.KwSnapshot:
		p.next()
		b.Isolation = "snapshot"
	case lexer.KwSerializable:
		p.next()
		b.Isolation = "serializable"
	}
	return b, nil
}

func (p *Parser) or() (ast.Expr, error) {
	left, err := p.and()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == lexer.KwOr {
		p.next()
		right, err := p.and()
		if err != nil {
			return nil, err
		}
		left = ast.Binary{Op: "OR", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) and() (ast.Expr, error) {
	left, err := p.cmp()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == lexer.KwAnd {
		p.next()
		right, err := p.cmp()
		if err != nil {
			return nil, err
		}
		left = ast.Binary{Op: "AND", Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) cmp() (ast.Expr, error) {
	left, err := p.add()
	if err != nil {
		return nil, err
	}
	if p.tok.Kind == lexer.KwIs {
		p.next()
		not := false
		if p.tok.Kind == lexer.KwNot {
			not = true
			p.next()
		}
		if err := p.expect(lexer.KwNull, "NULL"); err != nil {
			return nil, err
		}
		return ast.IsNull{Expr: left, Not: not}, nil
	}
	if p.tok.Kind == lexer.KwNot {
		p.next()
		if p.tok.Kind == lexer.KwIn {
			return p.inSubquery(left, true)
		}
		if p.tok.Kind != lexer.KwBetween {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected BETWEEN or IN")
		}
		return p.between(left, true)
	}
	if p.tok.Kind == lexer.KwIn {
		return p.inSubquery(left, false)
	}
	if p.tok.Kind == lexer.KwBetween {
		return p.between(left, false)
	}
	op := ""
	switch p.tok.Kind {
	case lexer.Eq:
		op = "="
	case lexer.Neq:
		op = "<>"
	case lexer.Lt:
		op = "<"
	case lexer.Gt:
		op = ">"
	case lexer.Lte:
		op = "<="
	case lexer.Gte:
		op = ">="
	default:
		return left, nil
	}
	p.next()
	right, err := p.add()
	if err != nil {
		return nil, err
	}
	return ast.Binary{Op: op, Left: left, Right: right}, nil
}

func (p *Parser) inSubquery(left ast.Expr, not bool) (ast.Expr, error) {
	p.next()
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	if p.tok.Kind != lexer.KwSelect && p.tok.Kind != lexer.KwWith {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "IN currently requires a SELECT subquery")
	}
	query, err := p.queryOrWith()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return ast.InSubquery{Expr: left, Query: query, Not: not, ID: p.nextSubqueryID()}, nil
}

func (p *Parser) between(left ast.Expr, not bool) (ast.Expr, error) {
	p.next()
	low, err := p.add()
	if err != nil {
		return nil, err
	}
	if err := p.expect(lexer.KwAnd, "AND"); err != nil {
		return nil, err
	}
	high, err := p.add()
	if err != nil {
		return nil, err
	}
	return ast.Between{Expr: left, Low: low, High: high, Not: not}, nil
}

func (p *Parser) add() (ast.Expr, error) {
	left, err := p.mul()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == lexer.Plus || p.tok.Kind == lexer.Minus {
		op := p.tok.Lit
		p.next()
		right, err := p.mul()
		if err != nil {
			return nil, err
		}
		left = ast.Binary{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) mul() (ast.Expr, error) {
	left, err := p.unary()
	if err != nil {
		return nil, err
	}
	for p.tok.Kind == lexer.Star || p.tok.Kind == lexer.Slash {
		op := p.tok.Lit
		p.next()
		right, err := p.unary()
		if err != nil {
			return nil, err
		}
		left = ast.Binary{Op: op, Left: left, Right: right}
	}
	return left, nil
}

func (p *Parser) unary() (ast.Expr, error) {
	if p.tok.Kind == lexer.Minus {
		p.next()
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		return ast.Unary{Op: "-", Right: r}, nil
	}
	if p.tok.Kind == lexer.KwNot {
		p.next()
		r, err := p.unary()
		if err != nil {
			return nil, err
		}
		return ast.Unary{Op: "NOT", Right: r}, nil
	}
	return p.primary()
}

func (p *Parser) primary() (ast.Expr, error) {
	switch p.tok.Kind {
	case lexer.KwExists:
		p.next()
		if err := p.expect(lexer.LParen, "("); err != nil {
			return nil, err
		}
		if p.tok.Kind != lexer.KwSelect && p.tok.Kind != lexer.KwWith {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "EXISTS requires a SELECT subquery")
		}
		query, err := p.queryOrWith()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
		return ast.ExistsSubquery{Query: query, ID: p.nextSubqueryID()}, nil
	case lexer.KwCase:
		return p.caseExpr()
	case lexer.KwNull:
		p.next()
		return ast.Literal{Value: types.Null(types.NullType())}, nil
	case lexer.KwTrue:
		p.next()
		return ast.Literal{Value: types.BoolValue(true)}, nil
	case lexer.KwFalse:
		p.next()
		return ast.Literal{Value: types.BoolValue(false)}, nil
	case lexer.String:
		v := types.StringValue(p.tok.Lit)
		p.next()
		return ast.Literal{Value: v}, nil
	case lexer.HexLit:
		v := types.BlobValue([]byte(p.tok.Lit))
		p.next()
		return ast.Literal{Value: v}, nil
	case lexer.KwInterval:
		// INTERVAL 'text' typed-literal syntax (docs/design-datatypes.md
		// D6) — unlike DATE/TIME/TIMESTAMP (D5/D7), a bare quoted string
		// alone is not enough: arithmetic (WHERE/SELECT expressions
		// combining a temporal column with an interval) needs both
		// operands' Kind tagged before evaluation, which a plain STRING
		// literal cannot provide on its own the way column-context
		// coercion can for an INSERT/UPDATE target.
		p.next()
		if p.tok.Kind != lexer.String {
			return nil, nerr.New(nerr.Syntax, "sql.parser", "expected a quoted INTERVAL literal")
		}
		v, err := types.ParseInterval(p.tok.Lit)
		if err != nil {
			return nil, err
		}
		p.next()
		return ast.Literal{Value: v}, nil
	case lexer.Number:
		d, err := types.ParseDecimal(p.tok.Lit)
		if err != nil {
			return nil, err
		}
		p.next()
		return ast.Literal{Value: types.DecimalValue(d, types.Type{Kind: types.KindDecimal})}, nil
	case lexer.Param:
		name := p.tok.Lit
		p.next()
		return ast.Param{Name: name}, nil
	case lexer.Ident, lexer.KwUuid, lexer.KwPoint, lexer.KwBox, lexer.KwLocation, lexer.KwLineString, lexer.KwPolygon, lexer.KwCosine, lexer.KwL2, lexer.KwInnerProduct:
		return p.nameOrCall()
	case lexer.LParen:
		p.next()
		if p.tok.Kind == lexer.KwSelect || p.tok.Kind == lexer.KwWith {
			query, err := p.queryOrWith()
			if err != nil {
				return nil, err
			}
			if err := p.expect(lexer.RParen, ")"); err != nil {
				return nil, err
			}
			return ast.ScalarSubquery{Query: query, ID: p.nextSubqueryID()}, nil
		}
		// vector literal (1.0, 2.0) vs grouped expr
		// try to parse as expression; if comma after first number-like, treat as vector
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		if p.tok.Kind == lexer.Comma {
			elems, err := p.finishVector(ex)
			if err != nil {
				return nil, err
			}
			if err := p.expect(lexer.RParen, ")"); err != nil {
				return nil, err
			}
			return ast.VectorLit{Elems: elems}, nil
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
		return p.maybeWindow(ex)
	default:
		if err := p.lx.Err(); err != nil {
			return nil, err
		}
		return nil, nerr.New(nerr.Syntax, "sql.parser", "expected an expression")
	}
}

func (p *Parser) caseExpr() (ast.Expr, error) {
	p.next()
	var out ast.Case
	if p.tok.Kind != lexer.KwWhen {
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		out.Operand = ex
	}
	for p.tok.Kind == lexer.KwWhen {
		p.next()
		when, err := p.or()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwThen, "THEN"); err != nil {
			return nil, err
		}
		then, err := p.or()
		if err != nil {
			return nil, err
		}
		out.Whens = append(out.Whens, ast.CaseWhen{When: when, Then: then})
	}
	if len(out.Whens) == 0 {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "CASE requires at least one WHEN")
	}
	if p.tok.Kind == lexer.KwElse {
		p.next()
		ex, err := p.or()
		if err != nil {
			return nil, err
		}
		out.Else = ex
	}
	if err := p.expect(lexer.KwEnd, "END"); err != nil {
		return nil, err
	}
	return out, nil
}

func (p *Parser) nameOrCall() (ast.Expr, error) {
	name := p.tok.Lit
	p.next()
	if p.tok.Kind == lexer.LParen {
		p.next()
		if p.tok.Kind == lexer.Star {
			p.next()
			if err := p.expect(lexer.RParen, ")"); err != nil {
				return nil, err
			}
			return p.maybeWindow(ast.Call{Name: name, Star: true})
		}
		var args []ast.Expr
		if p.tok.Kind != lexer.RParen {
			for {
				a, err := p.or()
				if err != nil {
					return nil, err
				}
				args = append(args, a)
				if p.tok.Kind == lexer.Comma {
					p.next()
					continue
				}
				break
			}
		}
		if err := p.expect(lexer.RParen, ")"); err != nil {
			return nil, err
		}
		return p.maybeWindow(ast.Call{Name: name, Args: args})
	}
	if p.tok.Kind == lexer.Dot {
		parts := []string{name}
		for p.tok.Kind == lexer.Dot {
			p.next()
			part, err := p.ident()
			if err != nil {
				return nil, err
			}
			parts = append(parts, part)
		}
		return ast.Path{Parts: parts}, nil
	}
	return ast.Ident{Name: name}, nil
}

func (p *Parser) orderItem() (ast.OrderItem, error) {
	ex, err := p.or()
	if err != nil {
		return ast.OrderItem{}, err
	}
	item := ast.OrderItem{Expr: ex}
	switch p.tok.Kind {
	case lexer.KwAsc:
		p.next()
	case lexer.KwDesc:
		p.next()
		item.Desc = true
	}
	return item, nil
}

func (p *Parser) maybeWindow(ex ast.Expr) (ast.Expr, error) {
	if p.tok.Kind != lexer.KwOver {
		return ex, nil
	}
	call, ok := ex.(ast.Call)
	if !ok {
		return nil, nerr.New(nerr.Syntax, "sql.parser", "OVER requires a function call")
	}
	p.next()
	if err := p.expect(lexer.LParen, "("); err != nil {
		return nil, err
	}
	w := ast.Window{Fn: call}
	if p.identIs("partition") {
		p.next()
		if err := p.expect(lexer.KwBy, "BY"); err != nil {
			return nil, err
		}
		for {
			part, err := p.or()
			if err != nil {
				return nil, err
			}
			w.Partition = append(w.Partition, part)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
	}
	if p.tok.Kind == lexer.KwOrder {
		p.next()
		if err := p.expect(lexer.KwBy, "BY"); err != nil {
			return nil, err
		}
		for {
			item, err := p.orderItem()
			if err != nil {
				return nil, err
			}
			w.Order = append(w.Order, item)
			if p.tok.Kind == lexer.Comma {
				p.next()
				continue
			}
			break
		}
	}
	frame, err := p.windowFrame()
	if err != nil {
		return nil, err
	}
	w.Frame = frame
	if err := p.expect(lexer.RParen, ")"); err != nil {
		return nil, err
	}
	return w, nil
}

func (p *Parser) identIs(s string) bool {
	return p.tok.Kind == lexer.Ident && p.tok.Lit == s
}

func (p *Parser) windowFrame() (*ast.Frame, error) {
	var mode ast.FrameMode
	switch {
	case p.identIs("rows"):
		mode = ast.FrameRows
		p.next()
	case p.identIs("range"):
		mode = ast.FrameRange
		p.next()
	default:
		return nil, nil
	}
	fr := ast.Frame{Mode: mode}
	if p.tok.Kind == lexer.KwBetween {
		p.next()
		start, err := p.frameBound()
		if err != nil {
			return nil, err
		}
		if err := p.expect(lexer.KwAnd, "AND"); err != nil {
			return nil, err
		}
		end, err := p.frameBound()
		if err != nil {
			return nil, err
		}
		fr.Start, fr.End = start, end
		return &fr, nil
	}
	start, err := p.frameBound()
	if err != nil {
		return nil, err
	}
	fr.Start = start
	fr.End = ast.FrameBound{Kind: ast.BoundCurrentRow}
	return &fr, nil
}

func (p *Parser) frameBound() (ast.FrameBound, error) {
	if p.identIs("unbounded") {
		p.next()
		if p.identIs("preceding") {
			p.next()
			return ast.FrameBound{Kind: ast.BoundUnboundedPreceding}, nil
		}
		if p.identIs("following") {
			p.next()
			return ast.FrameBound{Kind: ast.BoundUnboundedFollowing}, nil
		}
		return ast.FrameBound{}, nerr.New(nerr.Syntax, "sql.parser", "expected PRECEDING or FOLLOWING")
	}
	if p.identIs("current") {
		p.next()
		if !p.identIs("row") {
			return ast.FrameBound{}, nerr.New(nerr.Syntax, "sql.parser", "expected ROW")
		}
		p.next()
		return ast.FrameBound{Kind: ast.BoundCurrentRow}, nil
	}
	off, err := p.or()
	if err != nil {
		return ast.FrameBound{}, err
	}
	if p.identIs("preceding") {
		p.next()
		return ast.FrameBound{Kind: ast.BoundPreceding, Offset: off}, nil
	}
	if p.identIs("following") {
		p.next()
		return ast.FrameBound{Kind: ast.BoundFollowing, Offset: off}, nil
	}
	return ast.FrameBound{}, nerr.New(nerr.Syntax, "sql.parser", "expected PRECEDING or FOLLOWING")
}

func (p *Parser) finishVector(first ast.Expr) ([]float32, error) {
	f, err := exprToFloat(first)
	if err != nil {
		return nil, err
	}
	elems := []float32{f}
	for p.tok.Kind == lexer.Comma {
		p.next()
		ex, err := p.add()
		if err != nil {
			return nil, err
		}
		f, err := exprToFloat(ex)
		if err != nil {
			return nil, err
		}
		elems = append(elems, f)
	}
	return elems, nil
}

func exprToFloat(e ast.Expr) (float32, error) {
	neg := false
	if u, ok := e.(ast.Unary); ok && u.Op == "-" {
		neg = true
		e = u.Right
	}
	lit, ok := e.(ast.Literal)
	if !ok || lit.Value.Typ.Kind != types.KindDecimal {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "vector elements must be numbers")
	}
	f, err := strconv.ParseFloat(lit.Value.Dec.String(), 32)
	if err != nil {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "invalid vector element")
	}
	if neg {
		f = -f
	}
	return float32(f), nil
}

func (p *Parser) searchField() (string, float64, error) {
	col, err := p.ident()
	if err != nil {
		return "", 0, err
	}
	if !p.identIs("weight") {
		return col, 1, nil
	}
	p.next()
	w, err := p.fieldWeight()
	if err != nil {
		return "", 0, err
	}
	return col, w, nil
}

func (p *Parser) fieldWeight() (float64, error) {
	if p.tok.Kind != lexer.Number {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "expected field weight")
	}
	w, err := strconv.ParseFloat(p.tok.Lit, 64)
	if err != nil || math.IsNaN(w) || math.IsInf(w, 0) {
		return 0, nerr.New(nerr.InvalidArgument, "sql.parser", "invalid field weight")
	}
	if err := fulltext.CheckFieldWeight(w); err != nil {
		return 0, err
	}
	p.next()
	return w, nil
}

func (p *Parser) ident() (string, error) {
	if p.tok.Kind != lexer.Ident {
		if err := p.lx.Err(); err != nil {
			return "", err
		}
		return "", nerr.New(nerr.Syntax, "sql.parser", "expected identifier")
	}
	s := p.tok.Lit
	p.next()
	return s, nil
}

func (p *Parser) uintLit() (uint64, error) {
	if p.tok.Kind != lexer.Number {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "expected number")
	}
	n, err := strconv.ParseUint(p.tok.Lit, 10, 32)
	if err != nil {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "invalid integer")
	}
	p.next()
	return n, nil
}

// uint64Lit is uintLit without the 32-bit ceiling, for fields such as byte
// counts that legitimately exceed it.
func (p *Parser) uint64Lit() (uint64, error) {
	if p.tok.Kind != lexer.Number {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "expected number")
	}
	n, err := strconv.ParseUint(p.tok.Lit, 10, 64)
	if err != nil {
		return 0, nerr.New(nerr.Syntax, "sql.parser", "invalid integer")
	}
	p.next()
	return n, nil
}

func (p *Parser) expect(k lexer.Kind, what string) error {
	if p.tok.Kind != k {
		if err := p.lx.Err(); err != nil {
			return err
		}
		return nerr.New(nerr.Syntax, "sql.parser", "expected "+what)
	}
	p.next()
	return nil
}
