package config

import (
	"bufio"
	"os"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
)

const (
	DefaultListenAddr  = "127.0.0.1:7210"
	DefaultBufferPages = 1024
	DefaultLogLevel    = "info"
	DefaultMaxInflight = 32
	DefaultMaxQueue    = 128
	DefaultQueueWaitMS = 5000
	DataFileName       = "nextsql.db"
	AuthFileName       = "nextsql.users"
	ACLFileName        = "nextsql.acl"
	AuditFileName      = "nextsql.audit"
)

// Config holds process settings. Encryption keys are never represented here.
type Config struct {
	DataDir          string
	KeyFile          string
	AuthFile         string
	ListenAddr       string
	LogLevel         string
	TLSCert          string
	TLSKey           string
	BufferPages      int
	RequireClientKey bool
	AuditFile        string
	WalArchive       string
	MaxInflight      int
	MaxQueryQueue    int
	QueueWaitMS      int
	MaxResultRows    int
	NodeID           string
	RaftBind         string
	RaftJoin         string
	RaftBootstrap    bool
}

func Default() Config {
	return Config{
		ListenAddr:    DefaultListenAddr,
		LogLevel:      DefaultLogLevel,
		BufferPages:   DefaultBufferPages,
		MaxInflight:   DefaultMaxInflight,
		MaxQueryQueue: DefaultMaxQueue,
		QueueWaitMS:   DefaultQueueWaitMS,
	}
}

func (c Config) DataFile() string {
	if c.DataDir == "" {
		return DataFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + DataFileName
}

func (c Config) UsersFile() string {
	if c.AuthFile != "" {
		return c.AuthFile
	}
	if c.DataDir == "" {
		return AuthFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + AuthFileName
}

func (c Config) ACLFile() string {
	if c.DataDir == "" {
		return ACLFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + ACLFileName
}

func (c Config) AuditPath() string {
	if c.AuditFile != "" {
		return c.AuditFile
	}
	if c.DataDir == "" {
		return AuditFileName
	}
	return strings.TrimRight(c.DataDir, "/") + "/" + AuditFileName
}

// Load reads a simple key=value file. Unknown keys are rejected.
func Load(path string) (Config, error) {
	cfg := Default()
	f, err := os.Open(path)
	if err != nil {
		return Config{}, nerr.Wrap(nerr.IO, "config.Load", "open", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "expected key=value")
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		switch k {
		case "data_dir":
			cfg.DataDir = v
		case "key_file":
			cfg.KeyFile = v
		case "listen_addr":
			cfg.ListenAddr = v
		case "log_level":
			cfg.LogLevel = v
		case "buffer_pages":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "buffer_pages must be a positive integer")
			}
			cfg.BufferPages = n
		case "auth_file":
			cfg.AuthFile = v
		case "tls_cert":
			cfg.TLSCert = v
		case "tls_key":
			cfg.TLSKey = v
		case "require_client_key":
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				cfg.RequireClientKey = true
			case "false", "0", "no":
				cfg.RequireClientKey = false
			default:
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "require_client_key must be true or false")
			}
		case "audit_file":
			cfg.AuditFile = v
		case "wal_archive":
			cfg.WalArchive = v
		case "max_inflight_queries":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_inflight_queries must be a positive integer")
			}
			cfg.MaxInflight = n
		case "max_query_queue":
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_query_queue must be >= 0")
			}
			cfg.MaxQueryQueue = n
		case "query_queue_wait_ms":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "query_queue_wait_ms must be a positive integer")
			}
			cfg.QueueWaitMS = n
		case "max_result_rows":
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 {
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "max_result_rows must be a positive integer")
			}
			cfg.MaxResultRows = n
		case "node_id":
			cfg.NodeID = v
		case "raft_bind":
			cfg.RaftBind = v
		case "raft_join":
			cfg.RaftJoin = v
		case "raft_bootstrap":
			switch strings.ToLower(v) {
			case "true", "1", "yes":
				cfg.RaftBootstrap = true
			case "false", "0", "no":
				cfg.RaftBootstrap = false
			default:
				return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "raft_bootstrap must be true or false")
			}
		default:
			return Config{}, nerr.New(nerr.InvalidArgument, "config.Load", "unknown key")
		}
	}
	if err := sc.Err(); err != nil {
		return Config{}, nerr.Wrap(nerr.IO, "config.Load", "read", err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	if c.BufferPages < 1 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "buffer_pages must be >= 1")
	}
	if c.MaxInflight < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_inflight_queries must be >= 0")
	}
	if c.MaxQueryQueue < 0 {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "max_query_queue must be >= 0")
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return nerr.New(nerr.InvalidArgument, "config.Validate", "log_level must be debug, info, warn, or error")
	}
	if (c.RaftBind != "" || c.NodeID != "") && (c.RaftBind == "" || c.NodeID == "") {
		return nerr.New(nerr.InvalidArgument, "config.Validate", "node_id and raft_bind must be set together")
	}
	return nil
}
