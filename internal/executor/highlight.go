package executor

import (
	"unicode/utf8"

	"github.com/bzync/nextsql/internal/catalog"
	"github.com/bzync/nextsql/internal/fulltext"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/planner"
	"github.com/bzync/nextsql/internal/sql/types"
)

type ftHighlightState struct {
	query     ast.Expr
	table     *catalog.Table
	indexName string
	columns   []int
	analyzer  fulltext.Analyzer
	parsed    fulltext.Query
	haveQ     bool
	prev      *ftHighlightState
}

func (s *Session) pushHighlight(st *ftHighlightState) {
	if st == nil {
		return
	}
	st.prev = s.ftHL
	s.ftHL = st
}

func (s *Session) popHighlight() {
	if s.ftHL != nil {
		s.ftHL = s.ftHL.prev
	}
}

func exprsHaveHighlight(exprs []ast.Expr) bool {
	for _, e := range exprs {
		if callHasHighlight(e) {
			return true
		}
	}
	return false
}

func callHasHighlight(e ast.Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case ast.Call:
		if x.Name == "highlight" || x.Name == "snippet" {
			return true
		}
		for _, a := range x.Args {
			if callHasHighlight(a) {
				return true
			}
		}
	case ast.Unary:
		return callHasHighlight(x.Right)
	case ast.Binary:
		return callHasHighlight(x.Left) || callHasHighlight(x.Right)
	case ast.Between:
		return callHasHighlight(x.Expr) || callHasHighlight(x.Low) || callHasHighlight(x.High)
	case ast.IsNull:
		return callHasHighlight(x.Expr)
	case ast.Case:
		if callHasHighlight(x.Operand) || callHasHighlight(x.Else) {
			return true
		}
		for _, arm := range x.Whens {
			if callHasHighlight(arm.When) || callHasHighlight(arm.Then) {
				return true
			}
		}
	}
	return false
}

func (s *Session) maybePushHighlight(plan planner.Logical, exprs []ast.Expr) (func(), error) {
	if !exprsHaveHighlight(exprs) {
		return func() {}, nil
	}
	st, ok := highlightStateFromPlan(plan)
	if !ok {
		return nil, nerr.New(nerr.InvalidArgument, "executor.highlight", "HIGHLIGHT/SNIPPET requires SEARCH")
	}
	st.analyzer = fulltextAnalyzer(st.table, st.indexName, st.columns)
	s.pushHighlight(st)
	return s.popHighlight, nil
}

func highlightStateFromPlan(p planner.Logical) (*ftHighlightState, bool) {
	if p == nil {
		return nil, false
	}
	switch n := p.(type) {
	case planner.Search:
		return &ftHighlightState{query: n.Query, table: n.Table, indexName: n.IndexName, columns: n.Columns}, true
	case planner.Rerank:
		if n.SearchQuery != nil && len(n.SearchCols) > 0 {
			return &ftHighlightState{query: n.SearchQuery, table: n.Table, columns: n.SearchCols}, true
		}
		if st, ok := highlightStateFromPlan(n.Input); ok {
			return st, true
		}
		for _, extra := range n.Extra {
			if st, ok := highlightStateFromPlan(extra); ok {
				return st, true
			}
		}
		return nil, false
	case planner.Candidates:
		if n.Kind == "fulltext" {
			return &ftHighlightState{query: n.Query, table: n.Table, indexName: n.IndexName, columns: []int{n.Column}}, true
		}
		return highlightStateFromPlan(n.Input)
	case planner.With:
		return highlightStateFromPlan(n.Query)
	case planner.SetOperation:
		if st, ok := highlightStateFromPlan(n.Left); ok {
			return st, true
		}
		return highlightStateFromPlan(n.Right)
	case planner.Filter:
		return highlightStateFromPlan(n.Input)
	case planner.Project:
		return highlightStateFromPlan(n.Input)
	case planner.Limit:
		return highlightStateFromPlan(n.Input)
	case planner.Sort:
		return highlightStateFromPlan(n.Input)
	case planner.Window:
		return highlightStateFromPlan(n.Input)
	case planner.Aggregate:
		return highlightStateFromPlan(n.Input)
	case planner.Facet:
		return highlightStateFromPlan(n.Input)
	case planner.Join:
		if st, ok := highlightStateFromPlan(n.Left); ok {
			return st, true
		}
		return highlightStateFromPlan(n.Right)
	case planner.Nearest:
		return highlightStateFromPlan(n.Input)
	default:
		return nil, false
	}
}

func (s *Session) evalHighlight(x ast.Call, tab *catalog.Table, row []types.Value) (types.Value, error) {
	if s.ftHL == nil {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "HIGHLIGHT/SNIPPET requires SEARCH")
	}
	if len(x.Args) == 0 {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", x.Name+" requires arguments")
	}
	src, err := s.eval(x.Args[0], tab, row)
	if err != nil {
		return types.Value{}, err
	}
	if src.Null {
		return types.Null(src.Typ), nil
	}
	if src.Typ.Kind != types.KindString && src.Typ.Kind != types.KindText {
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", x.Name+" requires STRING or TEXT")
	}
	pre, post := fulltext.DefaultHighlightPre, fulltext.DefaultHighlightPost
	width := fulltext.DefaultSnippetRunes
	switch x.Name {
	case "highlight":
		if len(x.Args) != 1 && len(x.Args) != 3 {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "highlight takes one or three arguments")
		}
		if len(x.Args) == 3 {
			pre, post, err = s.evalMarkers(x.Args[1], x.Args[2], tab, row)
			if err != nil {
				return types.Value{}, err
			}
		}
	case "snippet":
		if len(x.Args) != 1 && len(x.Args) != 2 && len(x.Args) != 4 {
			return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "snippet takes one, two, or four arguments")
		}
		if len(x.Args) >= 2 {
			w, err := s.eval(x.Args[1], tab, row)
			if err != nil {
				return types.Value{}, err
			}
			if w.Null {
				return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "snippet width must be an integer DECIMAL")
			}
			n, err := decimalIndex(w, "snippet width")
			if err != nil {
				return types.Value{}, err
			}
			width = n
		}
		if len(x.Args) == 4 {
			pre, post, err = s.evalMarkers(x.Args[2], x.Args[3], tab, row)
			if err != nil {
				return types.Value{}, err
			}
		}
	default:
		return types.Value{}, nerr.New(nerr.InvalidArgument, "executor.eval", "unknown function")
	}
	q, err := s.highlightQuery(src.Str)
	if err != nil {
		return types.Value{}, err
	}
	var out string
	switch x.Name {
	case "snippet":
		out, err = fulltext.Snippet(src.Str, q, s.ftHL.analyzer, width, pre, post)
	default:
		out, err = fulltext.Highlight(src.Str, q, s.ftHL.analyzer, pre, post)
	}
	if err != nil {
		return types.Value{}, err
	}
	src.Str = out
	return src, nil
}

func (s *Session) evalMarkers(preE, postE ast.Expr, tab *catalog.Table, row []types.Value) (string, string, error) {
	pre, err := s.eval(preE, tab, row)
	if err != nil {
		return "", "", err
	}
	post, err := s.eval(postE, tab, row)
	if err != nil {
		return "", "", err
	}
	if pre.Null || post.Null {
		return "", "", nerr.New(nerr.InvalidArgument, "executor.eval", "highlight markers must be STRING or TEXT")
	}
	if (pre.Typ.Kind != types.KindString && pre.Typ.Kind != types.KindText) ||
		(post.Typ.Kind != types.KindString && post.Typ.Kind != types.KindText) {
		return "", "", nerr.New(nerr.InvalidArgument, "executor.eval", "highlight markers must be STRING or TEXT")
	}
	if utf8.RuneCountInString(pre.Str) > fulltext.MaxHighlightMarkerRunes || utf8.RuneCountInString(post.Str) > fulltext.MaxHighlightMarkerRunes {
		return "", "", nerr.New(nerr.InvalidArgument, "executor.eval", "highlight marker too long")
	}
	return pre.Str, post.Str, nil
}

func (s *Session) highlightQuery(doc string) (fulltext.Query, error) {
	st := s.ftHL
	if !st.haveQ {
		q, err := s.searchQuery(planner.Search{
			Table:     st.table,
			IndexName: st.indexName,
			Columns:   st.columns,
			Query:     st.query,
		})
		if err != nil {
			return fulltext.Query{}, err
		}
		st.parsed = q
		st.haveQ = true
	}
	q := st.parsed
	if q.Empty() {
		return q, nil
	}
	docQ, err := fulltext.AnalyzeWith(doc, st.analyzer)
	if err != nil {
		return fulltext.Query{}, err
	}
	present := make(map[string]struct{}, len(docQ.Terms))
	for _, t := range docQ.Terms {
		present[t.Term] = struct{}{}
	}
	return fulltext.ApplyTypoTolerance(q, func(term string) bool {
		_, ok := present[term]
		return ok
	}), nil
}
