package profile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const validFile = `{
  "version": 1,
  "profiles": [
    {"id": "local", "name": "Local dev", "environment": "development",
     "address": "127.0.0.1:7211", "insecure": true, "user": "app"},
    {"id": "prod", "name": "Production", "environment": "production",
     "address": "db.example.com:7210", "tls_ca": "certs/ca.pem",
     "tls_server_name": "db.internal", "tls_client_cert": "/etc/nsql/client.pem",
     "tls_client_key": "/etc/nsql/client.key", "database": "main"}
  ]
}`

func TestParseValidFile(t *testing.T) {
	base := filepath.FromSlash("/srv/admin")
	got, err := Parse([]byte(validFile), base)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("got %d profiles, want 2", len(got))
	}
	local, prod := got[0], got[1]
	if local.ID != "local" || !local.Insecure || local.User != "app" || local.Environment != "development" {
		t.Fatalf("local profile = %+v", local)
	}
	if prod.TLSCA != filepath.Join(base, "certs", "ca.pem") {
		t.Fatalf("relative tls_ca resolved to %q, want it under the file's directory", prod.TLSCA)
	}
	if prod.ServerName() != "db.internal" || prod.Database != "main" {
		t.Fatalf("prod profile = %+v", prod)
	}
	if d := prod.Detail(); !d.TLS || !d.MTLS || d.Address != "db.example.com:7210" {
		t.Fatalf("prod detail = %+v", d)
	}
}

// The pre-auth view must never carry where a profile points or any path.
func TestPublicViewCarriesNoTargetOrPath(t *testing.T) {
	got, err := Parse([]byte(validFile), "/srv/admin")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range got {
		raw, _ := json.Marshal(p.Public())
		for _, leak := range []string{p.Address, "ca.pem", "client", "app", "main", "db.internal"} {
			if strings.Contains(string(raw), leak) {
				t.Fatalf("public view of %q leaks %q: %s", p.ID, leak, raw)
			}
		}
		// A detail view shows the address but still no local file path.
		raw, _ = json.Marshal(p.Detail())
		if strings.Contains(string(raw), "ca.pem") || strings.Contains(string(raw), ".key") {
			t.Fatalf("detail view of %q leaks a path: %s", p.ID, raw)
		}
	}
}

func TestParseRejects(t *testing.T) {
	one := func(fields string) string {
		return `{"version":1,"profiles":[{` + fields + `}]}`
	}
	cases := map[string]string{
		"not json":            `{`,
		"missing version":     `{"profiles":[{"id":"a","address":"127.0.0.1:1","insecure":true}]}`,
		"future version":      `{"version":2,"profiles":[]}`,
		"unknown top field":   `{"version":1,"profiles":[{"id":"a","address":"127.0.0.1:1","insecure":true}],"extra":1}`,
		"password field":      one(`"id":"a","address":"127.0.0.1:1","insecure":true,"password":"x"`),
		"misspelled tls_ca":   one(`"id":"a","address":"db:1","tlsca":"ca.pem"`),
		"trailing data":       `{"version":1,"profiles":[{"id":"a","address":"127.0.0.1:1","insecure":true}]} {}`,
		"no profiles":         `{"version":1,"profiles":[]}`,
		"reserved id":         one(`"id":"default","address":"127.0.0.1:1","insecure":true`),
		"bad id":              one(`"id":"Prod DB","address":"127.0.0.1:1","insecure":true`),
		"no port":             one(`"id":"a","address":"127.0.0.1","insecure":true`),
		"bad port":            one(`"id":"a","address":"127.0.0.1:99999","insecure":true`),
		"empty host":          one(`"id":"a","address":":7210","insecure":true`),
		"remote insecure":     one(`"id":"a","address":"db.example.com:7210","insecure":true`),
		"insecure with tls":   one(`"id":"a","address":"127.0.0.1:1","insecure":true,"tls_ca":"ca.pem"`),
		"tls without ca":      one(`"id":"a","address":"db:1"`),
		"cert without key":    one(`"id":"a","address":"db:1","tls_ca":"ca.pem","tls_client_cert":"c.pem"`),
		"unknown environment": one(`"id":"a","address":"127.0.0.1:1","insecure":true,"environment":"prod"`),
		"control char name":   one(`"id":"a","name":"x\u0007","address":"127.0.0.1:1","insecure":true`),
		"bad database":        one(`"id":"a","address":"127.0.0.1:1","insecure":true,"database":"a b"`),
		"nul in path":         one(`"id":"a","address":"db:1","tls_ca":"c\u0000a.pem"`),
		"duplicate id": `{"version":1,"profiles":[
			{"id":"a","address":"127.0.0.1:1","insecure":true},
			{"id":"a","address":"127.0.0.1:2","insecure":true}]}`,
	}
	for name, data := range cases {
		if _, err := Parse([]byte(data), "/srv"); err == nil {
			t.Errorf("%s: Parse accepted %s", name, data)
		}
	}
}

func TestParseBounds(t *testing.T) {
	var b strings.Builder
	b.WriteString(`{"version":1,"profiles":[`)
	for i := 0; i <= MaxProfiles; i++ {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(`{"id":"p` + string(rune('a'+i%26)) + strings.Repeat("x", i/26) + `","address":"127.0.0.1:1","insecure":true}`)
	}
	b.WriteString(`]}`)
	if _, err := Parse([]byte(b.String()), "/srv"); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("%d profiles: want a count error, got %v", MaxProfiles+1, err)
	}
	if _, err := Parse(make([]byte, MaxFileBytes+1), "/srv"); err == nil {
		t.Fatal("an oversized file was accepted")
	}
}

func TestLoadChecksPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "profiles.json")
	if err := os.WriteFile(path, []byte(validFile), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load 0600: %v", err)
	}
	if got[1].TLSCA != filepath.Join(dir, "certs", "ca.pem") {
		t.Fatalf("tls_ca resolved to %q, want it beside the file", got[1].TLSCA)
	}
	if err := os.Chmod(path, 0o620); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil || !strings.Contains(err.Error(), "writable") {
		t.Fatalf("group-writable file: want a permission error, got %v", err)
	}
	if err := os.Chmod(path, 0o602); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("world-writable file was accepted")
	}
	if _, err := Load(dir); err == nil {
		t.Fatal("a directory was accepted as a profile file")
	}
}

func TestValidateTargetForDefaultProfile(t *testing.T) {
	ok := Profile{ID: DefaultID, Address: "127.0.0.1:7210", Insecure: true}
	if err := ok.ValidateTarget(); err != nil {
		t.Fatalf("loopback insecure default: %v", err)
	}
	remote := Profile{ID: DefaultID, Address: "10.0.0.5:7210", Insecure: true}
	if err := remote.ValidateTarget(); err == nil {
		t.Fatal("remote insecure default accepted")
	}
}

func FuzzParse(f *testing.F) {
	f.Add([]byte(validFile))
	f.Add([]byte(`{"version":1,"profiles":[{"id":"a","address":"127.0.0.1:1","insecure":true}]}`))
	f.Add([]byte(`{"version":2}`))
	f.Add([]byte(`[]`))
	f.Fuzz(func(t *testing.T, data []byte) {
		got, err := Parse(data, "/srv")
		if err != nil {
			return
		}
		// Anything accepted must be a self-consistent, bounded profile set.
		if len(got) == 0 || len(got) > MaxProfiles {
			t.Fatalf("accepted %d profiles", len(got))
		}
		seen := map[string]bool{}
		for _, p := range got {
			if err := p.Validate(); err != nil {
				t.Fatalf("accepted profile fails Validate: %v", err)
			}
			if seen[p.ID] || p.ID == DefaultID {
				t.Fatalf("accepted duplicate or reserved id %q", p.ID)
			}
			seen[p.ID] = true
			if p.Insecure && !isLoopbackAddr(p.Address) {
				t.Fatalf("accepted remote plaintext profile %q", p.Address)
			}
		}
	})
}

func isLoopbackAddr(addr string) bool {
	return (Profile{Address: addr, Insecure: true}).ValidateTarget() == nil
}
