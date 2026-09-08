package dockerentry

import (
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultDataDir      = "/var/lib/nextsql"
	defaultKeyFile      = "/run/secrets/root.key"
	defaultListen       = "0.0.0.0:7210"
	defaultPasswordFile = "/run/bootstrap/password"
	dbFileName          = "nextsql.db"
	confFileName        = "nextsql.conf"
	verifiedMarker      = "verified"
)

// Env is the container-wrapper configuration, read from process environment.
// Unset variables keep the same defaults docker/entrypoint.sh used.
type Env struct {
	DataDir       string
	KeyFile       string
	Listen        string
	PasswordFile  string
	ServerUser    string
	ServerPass    string
	Profile       string
	Preset        string
	ConfigFile    string
	SeedFrom      string
	SeedTo        string
	AuthFile      string
	TLSCert       string
	TLSKey        string
	TLSClientCA   string
	TLSClientCRL  string
	BufferPages   string
	NodeID        string
	RaftBind      string
	RaftJoin      string
	RaftBootstrap string
	JoinWait      string
}

// LoadEnv reads wrapper settings from getenv. Empty values fall back to the
// image defaults so a Compose file only has to set what it differs.
func LoadEnv(getenv func(string) string) Env {
	get := func(key, fallback string) string {
		if v := getenv(key); v != "" {
			return v
		}
		return fallback
	}
	dataDir := get("NEXTSQL_DATA_DIR", defaultDataDir)
	return Env{
		DataDir:       dataDir,
		KeyFile:       get("NEXTSQL_KEY_FILE", defaultKeyFile),
		Listen:        get("NEXTSQL_LISTEN", defaultListen),
		PasswordFile:  get("NEXTSQL_SERVER_PASSWORD_FILE", defaultPasswordFile),
		ServerUser:    getenv("NEXTSQL_SERVER_USER"),
		ServerPass:    getenv("NEXTSQL_SERVER_PASS"),
		Profile:       getenv("NEXTSQL_PROFILE"),
		Preset:        getenv("NEXTSQL_PRESET"),
		ConfigFile:    get("NEXTSQL_CONFIG_FILE", filepath.Join(dataDir, confFileName)),
		SeedFrom:      getenv("NEXTSQL_SEED_FROM"),
		SeedTo:        getenv("NEXTSQL_SEED_TO"),
		AuthFile:      getenv("NEXTSQL_AUTH_FILE"),
		TLSCert:       getenv("NEXTSQL_TLS_CERT"),
		TLSKey:        getenv("NEXTSQL_TLS_KEY"),
		TLSClientCA:   getenv("NEXTSQL_TLS_CLIENT_CA"),
		TLSClientCRL:  getenv("NEXTSQL_TLS_CLIENT_CRL"),
		BufferPages:   getenv("NEXTSQL_BUFFER_PAGES"),
		NodeID:        getenv("NEXTSQL_NODE_ID"),
		RaftBind:      getenv("NEXTSQL_RAFT_BIND"),
		RaftJoin:      getenv("NEXTSQL_RAFT_JOIN"),
		RaftBootstrap: getenv("NEXTSQL_RAFT_BOOTSTRAP"),
		JoinWait:      getenv("NEXTSQL_JOIN_WAIT"),
	}
}

func (e Env) dbPath() string {
	return filepath.Join(e.DataDir, dbFileName)
}

// configPath is the nextsql.conf the entrypoint generates on first start and
// passes to nextsqld on every start. NEXTSQL_CONFIG_FILE overrides it.
func (e Env) configPath() string {
	if e.ConfigFile != "" {
		return e.ConfigFile
	}
	return filepath.Join(e.DataDir, confFileName)
}

func (e Env) seedVerifiedPath() string {
	return filepath.Join(e.SeedFrom, verifiedMarker)
}

func (e Env) seedToVerifiedPath() string {
	return filepath.Join(e.SeedTo, verifiedMarker)
}

// serverArgs is the nextsqld flag list after argv0. Flag order matches the
// historical shell wrapper so debug logs stay comparable.
func (e Env) serverArgs() ([]string, error) {
	if (e.TLSCert == "") != (e.TLSKey == "") {
		return nil, usage("NEXTSQL_TLS_CERT and NEXTSQL_TLS_KEY must be set together")
	}
	if e.TLSClientCA != "" && (e.TLSCert == "" || e.TLSKey == "") {
		return nil, tlsFail("NEXTSQL_TLS_CLIENT_CA requires NEXTSQL_TLS_CERT and NEXTSQL_TLS_KEY")
	}
	if e.TLSClientCRL != "" && e.TLSClientCA == "" {
		return nil, tlsFail("NEXTSQL_TLS_CLIENT_CRL requires NEXTSQL_TLS_CLIENT_CA")
	}

	args := []string{
		"--data-dir", e.DataDir,
		"--key-file", e.KeyFile,
		"--listen", e.Listen,
	}
	if e.AuthFile != "" {
		args = append(args, "--auth-file", e.AuthFile)
	}
	if e.TLSCert != "" {
		args = append(args, "--tls-cert", e.TLSCert, "--tls-key", e.TLSKey)
	}
	if e.TLSClientCA != "" {
		args = append(args, "--tls-client-ca", e.TLSClientCA)
	}
	if e.TLSClientCRL != "" {
		args = append(args, "--tls-client-crl", e.TLSClientCRL)
	}
	if e.BufferPages != "" {
		args = append(args, "--buffer-pages", e.BufferPages)
	}
	if e.NodeID != "" {
		args = append(args, "--node-id", e.NodeID)
	}
	if e.RaftBind != "" {
		args = append(args, "--raft-bind", e.RaftBind)
	}
	if e.RaftJoin != "" {
		args = append(args, "--raft-join", e.RaftJoin)
	}
	if e.RaftBootstrap != "" {
		args = append(args, "--raft-bootstrap")
	}
	return args, nil
}

func (e Env) joinPeers() []string {
	if e.JoinWait == "" {
		return nil
	}
	parts := strings.Split(e.JoinWait, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func passwordFileReadable(path string) bool {
	info, err := os.Stat(path)
	if err != nil || info.IsDir() {
		return false
	}
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	_ = f.Close()
	return true
}
