package catalog

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
)

func TestTriggerDescriptorRoundTrip(t *testing.T) {
	stmt, err := parser.Parse(`CREATE TRIGGER audit AFTER UPDATE ON orders FOR EACH ROW RUN WORKFLOW record(OLD.status, NEW.status)`)
	if err != nil {
		t.Fatal(err)
	}
	create := stmt.(ast.CreateTrigger)
	want := &Trigger{ID: 7, Name: create.Name, Owner: "alice", Timing: create.Timing, Event: create.Event, TableID: 2, Table: create.Table, WorkflowID: 5, Workflow: create.Workflow, Args: create.Args}
	raw, err := EncodeTrigger(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeTrigger(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v want %#v", got, want)
	}
	again, err := EncodeTrigger(got)
	if err != nil || !bytes.Equal(again, raw) {
		t.Fatalf("non-canonical descriptor: %v", err)
	}
	if string(TriggerKey("audit")) != "Gaudit" {
		t.Fatalf("key=%q", TriggerKey("audit"))
	}
}

func TestTriggerDescriptorRejectsInvalid(t *testing.T) {
	base := &Trigger{ID: 1, Name: "t", Owner: "alice", Timing: ast.TriggerAfter, Event: ast.TriggerInsert, TableID: 2, Table: "orders", WorkflowID: 3, Workflow: "record", Args: []ast.Expr{ast.Path{Parts: []string{"new", "id"}}}}
	raw, err := EncodeTrigger(base)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{nil, []byte("bad"), raw[:len(raw)-1], append(append([]byte(nil), raw...), 0)} {
		if _, err := DecodeTrigger(bad); err == nil {
			t.Fatalf("accepted %x", bad)
		}
	}
	bad := *base
	bad.Args = []ast.Expr{ast.Path{Parts: []string{"old", "id"}}}
	if _, err := EncodeTrigger(&bad); err == nil {
		t.Fatal("accepted OLD for INSERT")
	}
}

func FuzzDecodeTrigger(f *testing.F) {
	seed, err := EncodeTrigger(&Trigger{ID: 1, Name: "t", Owner: "alice", Timing: ast.TriggerBefore, Event: ast.TriggerDelete, TableID: 2, Table: "orders", WorkflowID: 3, Workflow: "record", Args: []ast.Expr{ast.Path{Parts: []string{"old", "id"}}}})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NSTG"))
	f.Fuzz(func(t *testing.T, raw []byte) { _, _ = DecodeTrigger(raw) })
}
