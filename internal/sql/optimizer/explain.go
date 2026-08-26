package optimizer

import (
	"fmt"
	"strings"

	"github.com/bzync/nextsql/internal/sql/types"
)

// ExplainColumns is the structured EXPLAIN / EXPLAIN ANALYZE schema.
var ExplainColumns = []string{
	"operator", "estimates", "actuals", "time", "cpu", "memory", "disk", "cache", "spill", "workers", "index",
}

func FormatText(n *Node, analyze bool) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	writeText(&b, n, "", analyze)
	return b.String()
}

func writeText(b *strings.Builder, n *Node, pad string, analyze bool) {
	if n == nil {
		return
	}
	b.WriteString(pad)
	b.WriteString(n.Op)
	if n.Detail != "" {
		b.WriteByte(' ')
		b.WriteString(n.Detail)
	}
	fmt.Fprintf(b, " est=%d cost=%d", n.EstRows, n.EstCost)
	if analyze {
		fmt.Fprintf(b, " actual=%d time=%s cpu=%s mem=%d disk=%d cache=%d spill=%d workers=%d",
			n.ActRows, formatNS(n.TimeNS), formatNS(n.CPUTimeNS), n.Memory, n.Disk, n.Cache, n.Spill, n.Workers)
		if n.Index != "" {
			b.WriteString(" index=")
			b.WriteString(n.Index)
		}
	}
	b.WriteByte('\n')
	for _, k := range n.Kids {
		writeText(b, k, pad+"  ", analyze)
	}
}

func formatNS(ns int64) string {
	if ns <= 0 {
		return "0ns"
	}
	if ns < 1000 {
		return itoa64(ns) + "ns"
	}
	if ns < 1_000_000 {
		return itoa64(ns/1000) + "µs"
	}
	if ns < 1_000_000_000 {
		return itoa64(ns/1_000_000) + "ms"
	}
	return itoa64(ns/1_000_000_000) + "s"
}

// Rows returns one result row per operator (preorder).
func Rows(n *Node, analyze bool) [][]types.Value {
	var out [][]types.Value
	collectRows(&out, n, "", analyze)
	return out
}

func collectRows(out *[][]types.Value, n *Node, pad string, analyze bool) {
	if n == nil {
		return
	}
	op := pad + n.Op
	if n.Detail != "" {
		op += " " + n.Detail
	}
	est := "rows=" + itoa64(n.EstRows) + " cost=" + itoa64(n.EstCost)
	act := ""
	tm, cpu := "", ""
	if analyze {
		act = "rows=" + itoa64(n.ActRows)
		tm = formatNS(n.TimeNS)
		cpu = formatNS(n.CPUTimeNS)
	}
	workers := int64(n.Workers)
	if analyze && workers == 0 {
		workers = 1
	}
	*out = append(*out, []types.Value{
		types.StringValue(op),
		types.StringValue(est),
		types.StringValue(act),
		types.StringValue(tm),
		types.StringValue(cpu),
		types.StringValue(itoa64(n.Memory)),
		types.StringValue(itoa64(n.Disk)),
		types.StringValue(itoa64(n.Cache)),
		types.StringValue(itoa64(n.Spill)),
		types.StringValue(itoa64(workers)),
		types.StringValue(n.Index),
	})
	for _, k := range n.Kids {
		collectRows(out, k, pad+"  ", analyze)
	}
}

// FillDefaults sets workers=1 on executed operators that produced rows or were visited.
func FillDefaults(n *Node) {
	if n == nil {
		return
	}
	if n.Workers == 0 {
		n.Workers = 1
	}
	for _, k := range n.Kids {
		FillDefaults(k)
	}
}

// Walk applies fn preorder.
func Walk(n *Node, fn func(*Node)) {
	if n == nil {
		return
	}
	fn(n)
	for _, k := range n.Kids {
		Walk(k, fn)
	}
}

// AddActual increments ActRows on the first node with the given op (depth-first).
func AddActual(n *Node, op string, delta int64) {
	if n == nil || delta == 0 {
		return
	}
	if n.Op == op {
		n.ActRows += delta
		return
	}
	for _, k := range n.Kids {
		if findOp(k, op) {
			AddActual(k, op, delta)
			return
		}
	}
}

func findOp(n *Node, op string) bool {
	if n == nil {
		return false
	}
	if n.Op == op {
		return true
	}
	for _, k := range n.Kids {
		if findOp(k, op) {
			return true
		}
	}
	return false
}

func Find(n *Node, op string) *Node {
	if n == nil {
		return nil
	}
	if n.Op == op {
		return n
	}
	for _, k := range n.Kids {
		if got := Find(k, op); got != nil {
			return got
		}
	}
	return nil
}
