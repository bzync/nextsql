package protocol

import (
	"testing"

	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

func TestHelloAuthQueryRoundTrip(t *testing.T) {
	lim := DefaultLimits()
	h := Hello{Version: Version, Database: "production", User: "app"}
	raw, err := EncodeHello(h, lim)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeHello(raw, lim)
	if err != nil || got.User != "app" || got.Database != "production" {
		t.Fatalf("%+v %v", got, err)
	}

	ok := EncodeHelloOK(HelloOK{Version: Version, AuthMethod: AuthPassword, Secret: 99})
	hok, err := DecodeHelloOK(ok)
	if err != nil || hok.Secret != 99 {
		t.Fatalf("%+v %v", hok, err)
	}

	araw, err := EncodeAuth(Auth{Password: "s3cret"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	ag, err := DecodeAuth(araw, lim)
	if err != nil || ag.Password != "s3cret" {
		t.Fatalf("%+v %v", ag, err)
	}

	qraw, err := EncodeQuery(Query{
		SQL:    "SELECT n FROM t WHERE n = $1",
		Params: []executor.Param{{Value: types.StringValue("beta")}},
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	qg, err := DecodeQuery(qraw, lim)
	if err != nil || qg.SQL != "SELECT n FROM t WHERE n = $1" || len(qg.Params) != 1 || qg.Params[0].Value.Str != "beta" {
		t.Fatalf("%+v %v", qg, err)
	}
}

func TestDecodeQueryRejectsHugeSQL(t *testing.T) {
	lim := DefaultLimits()
	lim.MaxSQL = 4
	raw, err := EncodeQuery(Query{SQL: "SELECT 1"}, DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeQuery(raw, lim); !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("got %v", err)
	}
}

func FuzzDecodeHello(f *testing.F) {
	raw, _ := EncodeHello(Hello{Version: 1, User: "app", Database: "db"}, DefaultLimits())
	f.Add(raw)
	f.Add([]byte{0, 1, 2, 255})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, err := DecodeHello(b, DefaultLimits())
		if err == nil && len(b) < 12 {
			t.Fatal("decoded short hello")
		}
	})
}

func FuzzDecodeAuth(f *testing.F) {
	raw, _ := EncodeAuth(Auth{Password: "x"}, DefaultLimits())
	f.Add(raw)
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeAuth(b, DefaultLimits())
	})
}

func FuzzDecodeQuery(f *testing.F) {
	raw, _ := EncodeQuery(Query{SQL: "SELECT 1"}, DefaultLimits())
	f.Add(raw)
	f.Add([]byte{0xff, 0xff, 0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeQuery(b, DefaultLimits())
	})
}
