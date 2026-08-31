package catalog

import (
	"bytes"
	"testing"
)

func TestIdempotencyRecordRoundTrip(t *testing.T) {
	var request [32]byte
	request[0] = 7
	record := IdempotencyRecord{RequestHash: request, CreatedNS: 10, ExpiresNS: 20, Response: []byte("response")}
	raw, err := EncodeIdempotency(record)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeIdempotency(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.RequestHash != request || got.CreatedNS != 10 || got.ExpiresNS != 20 || !bytes.Equal(got.Response, record.Response) {
		t.Fatalf("round trip: %+v", got)
	}
	key := IdempotencyKey(request)
	if len(key) != 33 || key[0] != KeyIdempotency || !bytes.Equal(key[1:], request[:]) {
		t.Fatalf("key=%x", key)
	}
}

func FuzzDecodeIdempotency(f *testing.F) {
	var request [32]byte
	record, err := EncodeIdempotency(IdempotencyRecord{RequestHash: request, CreatedNS: 1, ExpiresNS: 2, Response: []byte("ok")})
	if err != nil {
		f.Fatal(err)
	}
	f.Add(record)
	f.Add([]byte("NSID"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		_, _ = DecodeIdempotency(raw)
	})
}
