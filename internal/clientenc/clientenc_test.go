package clientenc

import (
	"context"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/sql/types"
)

type testProvider struct {
	current string
	keys    map[string]Key
}

func (p *testProvider) CurrentFieldKey(context.Context, string, string, string) (Key, error) {
	return p.FieldKey(context.Background(), "", "", "", p.current)
}
func (p *testProvider) FieldKey(_ context.Context, _, _, _, id string) (Key, error) {
	k, ok := p.keys[id]
	if !ok {
		return Key{}, nerr.New(nerr.NotFound, "test", "missing")
	}
	return k, nil
}

func key(id string, fill byte) Key {
	k := Key{ID: id}
	for i := range k.Material {
		k.Material[i] = fill
	}
	return k
}

func TestEncryptDecryptRotationRevocationAndContext(t *testing.T) {
	p := &testProvider{current: "field-v1", keys: map[string]Key{"field-v1": key("field-v1", 1)}}
	ctx := context.Background()
	c1, err := Encrypt(ctx, p, "app", "accounts", "ssn", types.StringValue("123-45-6789"))
	if err != nil {
		t.Fatal(err)
	}
	c2, err := Encrypt(ctx, p, "app", "accounts", "ssn", types.StringValue("123-45-6789"))
	if err != nil {
		t.Fatal(err)
	}
	if c1 == c2 {
		t.Fatal("randomized encryption produced equal ciphertext")
	}
	v, err := Decrypt(ctx, p, "app", "accounts", "ssn", c1)
	if err != nil || v.Str != "123-45-6789" {
		t.Fatalf("decrypt: value=%q err=%v", v.Str, err)
	}
	if _, err := Decrypt(ctx, p, "app", "accounts", "other", c1); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("wrong context should fail authentication: %v", err)
	}

	p.keys["field-v2"] = key("field-v2", 2)
	p.current = "field-v2"
	c3, err := Encrypt(ctx, p, "app", "accounts", "ssn", types.StringValue("new"))
	if err != nil {
		t.Fatal(err)
	}
	if h, err := Inspect(c3); err != nil || h.KeyID != "field-v2" {
		t.Fatalf("rotation header: %+v %v", h, err)
	}
	if _, err := Decrypt(ctx, p, "app", "accounts", "ssn", c1); err != nil {
		t.Fatalf("overlap key should decrypt: %v", err)
	}
	delete(p.keys, "field-v1")
	if _, err := Decrypt(ctx, p, "app", "accounts", "ssn", c1); !nerr.HasCode(err, nerr.Crypto) {
		t.Fatalf("revoked key should fail closed: %v", err)
	}
}

func TestTamperAndTypeMismatchFailClosed(t *testing.T) {
	p := &testProvider{current: "v1", keys: map[string]Key{"v1": key("v1", 7)}}
	ciphertext, err := Encrypt(context.Background(), p, "db", "t", "secret", types.TextValue("sensitive"))
	if err != nil {
		t.Fatal(err)
	}
	tampered := []byte(ciphertext)
	tampered[len(tampered)-1] ^= 1
	if _, err := Decrypt(context.Background(), p, "db", "t", "secret", string(tampered)); err == nil {
		t.Fatal("tampered ciphertext decrypted")
	}
	if err := ValidateForColumn(ciphertext, types.String()); err == nil {
		t.Fatal("logical type mismatch accepted")
	}
	if err := ValidateForColumn(ciphertext, types.Text()); err != nil {
		t.Fatal(err)
	}
}

func TestSupportedTypeRejectsNonCanonicalMetadata(t *testing.T) {
	for _, typ := range []types.Type{
		{Kind: types.KindString, Scale: 1},
		{Kind: types.KindText, Precision: 1},
		{Kind: types.KindDecimal},
		{Kind: types.KindDecimal, Precision: 39},
		{Kind: types.KindDecimal, Precision: 2, Scale: 3},
	} {
		if SupportedType(typ) {
			t.Fatalf("accepted non-canonical type %+v", typ)
		}
	}
	decimal, err := types.DecimalType(12, 2)
	if err != nil || !SupportedType(decimal) {
		t.Fatalf("canonical decimal rejected: %+v %v", decimal, err)
	}
}

func TestEncryptDecimalEnforcesLogicalPrecisionAndScale(t *testing.T) {
	p := &testProvider{current: "v1", keys: map[string]Key{"v1": key("v1", 1)}}
	typ, err := types.DecimalType(4, 2)
	if err != nil {
		t.Fatal(err)
	}
	tooWide, err := types.ParseDecimal("123.45")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Encrypt(context.Background(), p, "app", "t", "amount", types.DecimalValue(tooWide, typ)); err == nil {
		t.Fatal("accepted DECIMAL precision overflow")
	}
	scaleLoss, err := types.ParseDecimal("1.234")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Encrypt(context.Background(), p, "app", "t", "amount", types.DecimalValue(scaleLoss, typ)); err == nil {
		t.Fatal("accepted DECIMAL scale loss")
	}
}

func TestCiphertextResourceBoundsFailClosed(t *testing.T) {
	p := &testProvider{current: "v1", keys: map[string]Key{"v1": key("v1", 7)}}
	tooLarge := types.TextValue(strings.Repeat("x", MaxPlaintext+1))
	if _, err := Encrypt(context.Background(), p, "db", "t", "secret", tooLarge); !nerr.HasCode(err, nerr.Exhausted) {
		t.Fatalf("oversized plaintext: %v", err)
	}
	oversizedEnvelope := Prefix + strings.Repeat("A", MaxPlaintext*2)
	if _, err := Inspect(oversizedEnvelope); !nerr.HasCode(err, nerr.InvalidFormat) {
		t.Fatalf("oversized envelope: %v", err)
	}
}

func TestNodeDriverCiphertextIsPortable(t *testing.T) {
	p := &testProvider{current: "v1", keys: map[string]Key{"v1": key("v1", 1)}}
	const nodeCiphertext = "NSCE1.AQECdjEDAAAAAABR9cxMnZfNCG7VFNXps3YzfALHK-_GEqEZfqFLjjGGGrvnwH9DLGPj"
	v, err := Decrypt(context.Background(), p, "app", "accounts", "secret", nodeCiphertext)
	if err != nil || v.Typ.Kind != types.KindText || v.Str != "portable" {
		t.Fatalf("Node ciphertext: value=%+v err=%v", v, err)
	}
}

func FuzzInspect(f *testing.F) {
	f.Add("NSCE1.")
	f.Add("plaintext")
	f.Fuzz(func(t *testing.T, s string) {
		h, err := Inspect(s)
		if err != nil {
			return
		}
		if h.KeyID == "" || !SupportedType(h.LogicalType) {
			t.Fatalf("accepted invalid header: %+v", h)
		}
	})
}
