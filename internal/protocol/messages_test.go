package protocol

import (
	"bytes"
	"testing"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/executor"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

// TestHelloRealmRoundTrip proves the new M2-2 trailing field round-trips.
func TestHelloRealmRoundTrip(t *testing.T) {
	lim := DefaultLimits()
	h := Hello{Version: Version, Database: "production", User: "app", Realm: "tenant-a"}
	raw, err := EncodeHello(h, lim)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeHello(raw, lim)
	if err != nil {
		t.Fatal(err)
	}
	if got.Realm != "tenant-a" || got.Database != "production" || got.User != "app" {
		t.Fatalf("%+v", got)
	}
}

// TestHelloWithoutRealmIsByteIdenticalToOldShape is the concrete regression
// guard for the M2-2 compatibility promise: an unconfigured client's Hello
// must be byte-for-byte the same wire shape as before Realm existed, not
// merely "decodes to the same values."
func TestHelloWithoutRealmIsByteIdenticalToOldShape(t *testing.T) {
	lim := DefaultLimits()
	withRealmField, err := EncodeHello(Hello{Version: Version, Database: "production", User: "app"}, lim)
	if err != nil {
		t.Fatal(err)
	}
	noRealmField, err := EncodeHello(Hello{Version: Version, Database: "production", User: "app", Realm: ""}, lim)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(withRealmField, noRealmField) {
		t.Fatalf("Realm-unset encodings differ: %x vs %x", withRealmField, noRealmField)
	}
}

// TestDecodeHelloOldShapeNoTrailingBytes decodes a literal old-shape byte
// sequence (no bytes at all past User, not just "Realm encoded as empty")
// to prove backward compatibility at the byte level, not just the API level.
func TestDecodeHelloOldShapeNoTrailingBytes(t *testing.T) {
	lim := DefaultLimits()
	var hdr [12]byte
	encoding.PutU16(hdr[:], 0, Version)
	buf := append([]byte{}, hdr[:]...)
	buf, err := appendU16String(buf, "production", lim.MaxName)
	if err != nil {
		t.Fatal(err)
	}
	buf, err = appendU16String(buf, "app", lim.MaxName)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeHello(buf, lim)
	if err != nil {
		t.Fatal(err)
	}
	if got.Realm != "" || got.Database != "production" || got.User != "app" {
		t.Fatalf("%+v", got)
	}
}

// TestDecodeHelloRejectsCorruptTrailingBytes proves the tail-sniff is not a
// blanket "accept anything past User" hole: a well-formed trailing Realm
// field followed by extra garbage bytes must still be rejected.
func TestDecodeHelloRejectsCorruptTrailingBytes(t *testing.T) {
	lim := DefaultLimits()
	h := Hello{Version: Version, Database: "production", User: "app", Realm: "tenant-a"}
	raw, err := EncodeHello(h, lim)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, 0xDE, 0xAD, 0xBE, 0xEF)
	if _, err := DecodeHello(raw, lim); !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("want a protocol decode error, got %v", err)
	}
}

// TestDecodeHelloRejectsTruncatedRealmLength proves corruption *within* the
// optional field itself (a length prefix claiming more bytes than exist) is
// still caught, not silently truncated or accepted.
func TestDecodeHelloRejectsTruncatedRealmLength(t *testing.T) {
	lim := DefaultLimits()
	h := Hello{Version: Version, Database: "production", User: "app"}
	raw, err := EncodeHello(h, lim)
	if err != nil {
		t.Fatal(err)
	}
	// A realm length prefix claiming 10 bytes with none actually present.
	var n [2]byte
	encoding.PutU16(n[:], 0, 10)
	raw = append(raw, n[:]...)
	if _, err := DecodeHello(raw, lim); !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("want a protocol decode error, got %v", err)
	}
}

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
	iqraw, err := EncodeIdempotentQuery(IdempotentQuery{
		Key: "order-42", SQL: "INSERT INTO orders (id) VALUES ($1)",
		Params: []executor.Param{{Value: types.StringValue("42")}},
	}, lim)
	if err != nil {
		t.Fatal(err)
	}
	iq, err := DecodeIdempotentQuery(iqraw, lim)
	if err != nil || iq.Key != "order-42" || iq.SQL != "INSERT INTO orders (id) VALUES ($1)" || len(iq.Params) != 1 || iq.Params[0].Value.Str != "42" {
		t.Fatalf("%+v %v", iq, err)
	}
}

func TestSetReadConsistencyRoundTrip(t *testing.T) {
	for _, m := range []SetReadConsistency{
		{Mode: ReadModeStrong},
		{Mode: ReadModeBounded, MaxStalenessMS: 2500},
		{Mode: ReadModeStale},
	} {
		got, err := DecodeSetReadConsistency(EncodeSetReadConsistency(m))
		if err != nil || got != m {
			t.Fatalf("%+v -> %+v %v", m, got, err)
		}
	}
	if _, err := DecodeSetReadConsistency([]byte{9, 0, 0, 0, 0, 0, 0, 0, 0}); !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("unknown mode byte accepted")
	}
	if _, err := DecodeSetReadConsistency([]byte{0}); !nerr.HasCode(err, nerr.Protocol) {
		t.Fatalf("short payload accepted")
	}
}

func TestNodeStatusRoundTrip(t *testing.T) {
	lim := DefaultLimits()
	for _, n := range []NodeStatus{
		{Role: "leader", HasLeader: true, Healthy: true, AppliedLSN: 42},
		{Role: "follower", HasLeader: true, Healthy: true, AppliedLSN: 40, LastContactMS: 12, ApplyBacklog: 2},
		{Role: "follower", LastContactMS: -1},
		{Role: "standalone", HasLeader: true, Healthy: true},
	} {
		raw, err := EncodeNodeStatus(n, lim)
		if err != nil {
			t.Fatal(err)
		}
		got, err := DecodeNodeStatus(raw, lim)
		if err != nil || got != n {
			t.Fatalf("%+v -> %+v %v", n, got, err)
		}
	}
	if _, err := DecodeNodeStatus([]byte{0, 1, byte('x')}, lim); err == nil {
		t.Fatalf("truncated node status accepted")
	}
}

func FuzzDecodeNodeStatus(f *testing.F) {
	raw, _ := EncodeNodeStatus(NodeStatus{Role: "follower", HasLeader: true, LastContactMS: 5}, DefaultLimits())
	f.Add(raw)
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeNodeStatus(b, DefaultLimits())
	})
}

func FuzzDecodeSetReadConsistency(f *testing.F) {
	f.Add(EncodeSetReadConsistency(SetReadConsistency{Mode: ReadModeBounded, MaxStalenessMS: 1000}))
	f.Add([]byte{0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeSetReadConsistency(b)
	})
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

func FuzzDecodeIdempotentQuery(f *testing.F) {
	raw, _ := EncodeIdempotentQuery(IdempotentQuery{Key: "key", SQL: "INSERT INTO t (id) VALUES ('1')"}, DefaultLimits())
	f.Add(raw)
	f.Add([]byte{0xff, 0xff})
	f.Fuzz(func(t *testing.T, b []byte) {
		_, _ = DecodeIdempotentQuery(b, DefaultLimits())
	})
}
