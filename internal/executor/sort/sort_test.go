package sort

import (
	"testing"

	"github.com/bzync/nextsql/internal/sql/types"
)

func TestRowsAscDescNulls(t *testing.T) {
	rows := [][]types.Value{
		{types.StringValue("b"), types.Null(types.String())},
		{types.StringValue("a"), types.StringValue("z")},
		{types.StringValue("a"), types.StringValue("m")},
		{types.StringValue("c"), types.Null(types.String())},
	}
	if err := Rows(rows, []Key{{Col: 0, Desc: false}, {Col: 1, Desc: true}}); err != nil {
		t.Fatal(err)
	}
	if rows[0][0].Str != "a" || rows[0][1].Str != "z" {
		t.Fatalf("0 %+v", rows[0])
	}
	if rows[1][0].Str != "a" || rows[1][1].Str != "m" {
		t.Fatalf("1 %+v", rows[1])
	}
	if rows[2][0].Str != "b" || !rows[2][1].Null {
		t.Fatalf("2 %+v", rows[2])
	}
}

func TestNullsLastAscFirstDesc(t *testing.T) {
	rows := [][]types.Value{
		{types.Null(types.String())},
		{types.StringValue("b")},
		{types.StringValue("a")},
	}
	if err := Rows(rows, []Key{{Col: 0}}); err != nil {
		t.Fatal(err)
	}
	if rows[0][0].Str != "a" || rows[2][0].Null == false {
		t.Fatalf("asc %+v", rows)
	}
	if err := Rows(rows, []Key{{Col: 0, Desc: true}}); err != nil {
		t.Fatal(err)
	}
	if !rows[0][0].Null || rows[1][0].Str != "b" {
		t.Fatalf("desc %+v", rows)
	}
}

func TestTopRows(t *testing.T) {
	rows := [][]types.Value{
		{types.StringValue("f")},
		{types.StringValue("b")},
		{types.StringValue("e")},
		{types.StringValue("a")},
		{types.StringValue("d")},
		{types.StringValue("c")},
	}
	got, err := TopRows(rows, []Key{{Col: 0}}, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0][0].Str != "a" || got[1][0].Str != "b" || got[2][0].Str != "c" {
		t.Fatalf("ascending top rows: %+v", got)
	}
	got, err = TopRows(rows, []Key{{Col: 0, Desc: true}}, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0][0].Str != "f" || got[1][0].Str != "e" {
		t.Fatalf("descending top rows: %+v", got)
	}
}
