package catalog

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/bzync/nextsql/internal/sql/ast"
	"github.com/bzync/nextsql/internal/sql/parser"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestWorkflowDescriptorRoundTrip(t *testing.T) {
	stmt, err := parser.Parse(`CREATE WORKFLOW fulfill_order(order_id UUID, note TEXT) AS BEGIN
		UPDATE orders SET status = 'processing' WHERE id = $order_id;
		INSERT INTO events (id, order_id, note) VALUES (UUID(), $order_id, $note);
		UPSERT INTO counters (id, n) VALUES ($order_id, 1) ON UNIQUE (id) SET n = n + 1;
		DELETE FROM queue WHERE id = $order_id LIMIT 1;
		RUN WORKFLOW audit_order($order_id, $note);
	END`)
	if err != nil {
		t.Fatal(err)
	}
	create := stmt.(ast.CreateWorkflow)
	want := &Workflow{
		ID: 17, Name: create.Name, Owner: "alice", Params: create.Params, Body: create.Body,
		Dependencies: []WorkflowDependency{
			{Kind: WorkflowDependencyTable, ID: 2, Name: "orders"},
			{Kind: WorkflowDependencyWorkflow, ID: 3, Name: "audit_order"},
		},
	}
	raw, err := EncodeWorkflow(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeWorkflow(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != want.ID || got.Name != want.Name || got.Owner != want.Owner || !reflect.DeepEqual(got.Params, want.Params) || !reflect.DeepEqual(got.Dependencies, want.Dependencies) || len(got.Body) != len(want.Body) {
		t.Fatalf("round trip identity\n got: %#v\nwant: %#v", got, want)
	}
	rawAgain, err := EncodeWorkflow(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(rawAgain, raw) {
		t.Fatalf("descriptor is not canonical\n got: %x\nwant: %x", rawAgain, raw)
	}
	if string(WorkflowKey("fulfill_order")) != "Wfulfill_order" {
		t.Fatalf("key %q", WorkflowKey("fulfill_order"))
	}
}

func TestWorkflowDescriptorRejectsInvalid(t *testing.T) {
	base := &Workflow{
		ID:     1,
		Name:   "w",
		Owner:  "alice",
		Params: []ast.WorkflowParam{{Name: "id", Type: types.UUID()}},
		Body:   []ast.Stmt{ast.Delete{Table: "t"}},
	}
	raw, err := EncodeWorkflow(base)
	if err != nil {
		t.Fatal(err)
	}
	cases := [][]byte{
		nil,
		[]byte("bad"),
		raw[:len(raw)-1],
		append(append([]byte(nil), raw...), 0),
	}
	badVersion := append([]byte(nil), raw...)
	badVersion[4], badVersion[5] = 0, 2
	cases = append(cases, badVersion)
	for _, tc := range cases {
		if _, err := DecodeWorkflow(tc); err == nil {
			t.Fatalf("accepted invalid descriptor %x", tc)
		}
	}

	dup := *base
	dup.Params = []ast.WorkflowParam{{Name: "id", Type: types.UUID()}, {Name: "id", Type: types.Text()}}
	if _, err := EncodeWorkflow(&dup); err == nil {
		t.Fatal("accepted duplicate parameters")
	}
	empty := *base
	empty.Body = nil
	if _, err := EncodeWorkflow(&empty); err == nil {
		t.Fatal("accepted empty body")
	}
	query := *base
	query.Body = []ast.Stmt{ast.Select{Table: "t", Star: true}}
	if _, err := EncodeWorkflow(&query); err == nil {
		t.Fatal("accepted query body")
	}
	returning := *base
	returning.Body = []ast.Stmt{ast.Delete{Table: "t", ReturningStar: true}}
	if _, err := EncodeWorkflow(&returning); err == nil {
		t.Fatal("accepted RETURNING body")
	}
}

func FuzzDecodeWorkflow(f *testing.F) {
	seed, err := EncodeWorkflow(&Workflow{
		ID:    1,
		Name:  "w",
		Owner: "alice",
		Body:  []ast.Stmt{ast.Delete{Table: "t"}},
	})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(seed)
	f.Add([]byte("NSWK"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeWorkflow(raw)
	})
}
