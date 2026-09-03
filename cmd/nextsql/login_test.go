package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bzync/nextsql/internal/cli"
)

func TestLoginProfileClientSecretFlagOverridesConfigPath(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.toml")
	validSecret := filepath.Join(dir, "override.secret")
	if err := os.WriteFile(validSecret, []byte("override-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config := strings.Join([]string{
		"[idp.corp]",
		`issuer = "https://idp.example"`,
		`client_id = "workload"`,
		`client_secret_file = "` + filepath.Join(dir, "missing.secret") + `"`,
		`broker_url = "https://broker.example"`,
	}, "\n")
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatal(err)
	}
	p, _, err := loginProfile(cli.Settings{IdP: "corp", IdPConfig: configPath}, validSecret)
	if err != nil {
		t.Fatalf("resolve override: %v", err)
	}
	if p.ClientSecretFile != validSecret || p.ClientSecret != "override-value" {
		t.Fatalf("resolved profile = %+v", p)
	}
}
