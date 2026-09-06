package setup

import (
	"strings"
	"testing"
)

func TestParamsValidate(t *testing.T) {
	cases := []struct {
		name    string
		p       Params
		wantErr bool
	}{
		{"minimal ok", Params{DataDir: "/d", KeyFile: "/k"}, false},
		{"missing data dir", Params{KeyFile: "/k"}, true},
		{"missing key file", Params{DataDir: "/d"}, true},
		{"bad preset", Params{DataDir: "/d", KeyFile: "/k", Preset: "turbo"}, true},
		{"bad profile", Params{DataDir: "/d", KeyFile: "/k", Profile: "staging"}, true},
		{"production skip-init rejected", Params{DataDir: "/d", KeyFile: "/k", Profile: "production", SkipInit: true}, true},
		{"production ok", Params{DataDir: "/d", KeyFile: "/k", Profile: "production"}, false},
		{"developer skip-init ok", Params{DataDir: "/d", KeyFile: "/k", Profile: "developer", SkipInit: true}, false},
		{"custom without buffer pages", Params{DataDir: "/d", KeyFile: "/k", Preset: "custom"}, true},
		{"custom with buffer pages ok", Params{DataDir: "/d", KeyFile: "/k", Preset: "custom", BufferPages: 64}, false},
		{"user without password", Params{DataDir: "/d", KeyFile: "/k", AdminUser: "app"}, true},
		{"password without user", Params{DataDir: "/d", KeyFile: "/k", AdminPassword: "secret123"}, true},
		{"user and password ok", Params{DataDir: "/d", KeyFile: "/k", AdminUser: "app", AdminPassword: "secret123"}, false},
		{"skip-init ok", Params{DataDir: "/d", KeyFile: "/k", SkipInit: true}, false},
		{"skip-init with admin user rejected", Params{DataDir: "/d", KeyFile: "/k", SkipInit: true, AdminUser: "app", AdminPassword: "secret123"}, true},
		{"listen without tls ok (nextsql setup is the authoritative check)", Params{DataDir: "/d", KeyFile: "/k", ListenAddr: "0.0.0.0:7210"}, false},
		{"tls cert without key rejected", Params{DataDir: "/d", KeyFile: "/k", TLSCert: "/c.pem"}, true},
		{"tls key without cert rejected", Params{DataDir: "/d", KeyFile: "/k", TLSKey: "/k.pem"}, true},
		{"tls cert and key ok", Params{DataDir: "/d", KeyFile: "/k", ListenAddr: "0.0.0.0:7210", TLSCert: "/c.pem", TLSKey: "/k.pem"}, false},
		{"enableService ok", Params{DataDir: "/d", KeyFile: "/k", EnableService: true}, false},
		{"enableService with skipInit rejected", Params{DataDir: "/d", KeyFile: "/k", SkipInit: true, EnableService: true}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.p.Validate()
			if (err != nil) != c.wantErr {
				t.Fatalf("Validate() error = %v, wantErr %v", err, c.wantErr)
			}
		})
	}
}

func TestParamsToArgsNeverContainsPassword(t *testing.T) {
	p := Params{
		DataDir: "/data", KeyFile: "/key", Preset: "custom", BufferPages: 128,
		Profile:   "production",
		AdminUser: "app", AdminPassword: "super-secret-password",
		Realm: "r1", Database: "db1",
	}
	args := p.toArgs(true, "/tmp/pwfile")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "super-secret-password") {
		t.Fatalf("argv leaked the password: %v", args)
	}
	if !strings.Contains(joined, "/tmp/pwfile") {
		t.Fatalf("argv missing password-file path: %v", args)
	}
	want := []string{"--dry-run", "--data-dir", "/data", "--key-file", "/key", "--preset", "custom", "--profile", "production", "--buffer-pages", "128", "--user", "app", "--realm", "r1", "--database", "db1"}
	for _, w := range want {
		if !strings.Contains(joined, w) {
			t.Errorf("args missing %q: %v", w, args)
		}
	}
}

func TestParamsToArgsNoAdminUser(t *testing.T) {
	p := Params{DataDir: "/data", KeyFile: "/key"}
	args := p.toArgs(false, "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--user") || strings.Contains(joined, "--password-file") {
		t.Errorf("no admin user requested but args contain user/password flags: %v", args)
	}
	if strings.Contains(joined, "--dry-run") {
		t.Errorf("dryRun=false but --dry-run present: %v", args)
	}
	if !strings.Contains(joined, "--realm default") || !strings.Contains(joined, "--database default") {
		t.Errorf("expected default realm/database, got: %v", args)
	}
}

func TestParamsToArgsNoNetworkFieldsByDefault(t *testing.T) {
	p := Params{DataDir: "/data", KeyFile: "/key"}
	args := p.toArgs(false, "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "--listen") || strings.Contains(joined, "--tls-cert") || strings.Contains(joined, "--tls-key") {
		t.Errorf("expected no network flags when unset, got: %v", args)
	}
}

func TestParamsToArgsListenAndTLS(t *testing.T) {
	p := Params{
		DataDir: "/data", KeyFile: "/key",
		ListenAddr: "0.0.0.0:7210", TLSCert: "/certs/server.pem", TLSKey: "/certs/server.key",
	}
	args := p.toArgs(false, "")
	joined := strings.Join(args, " ")
	for _, want := range []string{"--listen 0.0.0.0:7210", "--tls-cert /certs/server.pem", "--tls-key /certs/server.key"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args missing %q: %v", want, args)
		}
	}
}

func TestParamsToArgsNeverContainsEnableService(t *testing.T) {
	p := Params{DataDir: "/data", KeyFile: "/key", EnableService: true}
	args := p.toArgs(false, "")
	joined := strings.Join(args, " ")
	if strings.Contains(joined, "enableService") || strings.Contains(joined, "--service") || strings.Contains(joined, "systemctl") {
		t.Errorf("EnableService must never reach nextsql setup's argv (it is a separate setup-side action): %v", args)
	}
}

func TestParamsToArgsConfigOut(t *testing.T) {
	p := Params{DataDir: "/data", KeyFile: "/key"}
	args := p.toArgs(false, "")
	if strings.Contains(strings.Join(args, " "), "--config-out") {
		t.Errorf("empty ConfigOut must not emit --config-out (let nextsql setup default): %v", args)
	}

	p.ConfigOut = "/etc/nextsql/nextsql.conf"
	args = p.toArgs(false, "")
	if !strings.Contains(strings.Join(args, " "), "--config-out /etc/nextsql/nextsql.conf") {
		t.Errorf("expected --config-out /etc/nextsql/nextsql.conf in args: %v", args)
	}
}

func TestParamsToArgsSkipInit(t *testing.T) {
	p := Params{DataDir: "/d", KeyFile: "/k", SkipInit: true}
	args := p.toArgs(false, "")
	if !strings.Contains(strings.Join(args, " "), "--skip-init") {
		t.Errorf("expected --skip-init in args: %v", args)
	}

	p.SkipInit = false
	args = p.toArgs(false, "")
	if strings.Contains(strings.Join(args, " "), "--skip-init") {
		t.Errorf("did not expect --skip-init in args: %v", args)
	}
}
