package cli

import (
	"flag"
	"os"
	"strconv"
	"strings"

	"github.com/bzync/nextsql/internal/config"
	"github.com/bzync/nextsql/internal/nerr"
)

const (
	envAddr             = "NEXTSQL_ADDR"
	envDatabaseUser     = "NEXTSQL_DATABASE_USER"
	envDatabasePass     = "NEXTSQL_DATABASE_PASS"
	envDatabasePassFile = "NEXTSQL_DATABASE_PASSWORD_FILE"
	envServerUser       = "NEXTSQL_SERVER_USER"
	envServerPass       = "NEXTSQL_SERVER_PASS"
	envServerPassFile   = "NEXTSQL_SERVER_PASSWORD_FILE"
	envDatabase         = "NEXTSQL_DATABASE"
	envTLSCA            = "NEXTSQL_TLS_CA"
	envTLSServerName    = "NEXTSQL_TLS_SERVER_NAME"
	envTLSClientCert    = "NEXTSQL_TLS_CLIENT_CERT"
	envTLSClientKey     = "NEXTSQL_TLS_CLIENT_KEY"
	envInsecure         = "NEXTSQL_INSECURE"
	envIdP              = "NEXTSQL_IDP"
	envIdPConfig        = "NEXTSQL_IDP_CONFIG"
	envMigrationDir     = "NEXTSQL_MIGRATION_DIR"
	envDataDir          = "NEXTSQL_DATA_DIR"
	envKeyFile          = "NEXTSQL_KEY_FILE"
	envInstanceKey      = "NEXTSQL_INSTANCE_KEY_FILE"
	envRealmName        = "NEXTSQL_REALM_NAME"
	envBufferPages      = "NEXTSQL_BUFFER_PAGES"
	envHostingConfirm   = "NEXTSQL_HOSTING_CONFIRM"
	envHostingManifest  = "NEXTSQL_HOSTING_MANIFEST_FILE"

	defaultMigrationsDir = "./migrations"
)

// Settings is the merged client configuration after flags, process env, and dotenv files.
type Settings struct {
	Addr            string
	User            string
	PasswordFile    string
	Password        string
	ServerUser      string
	ServerPass      string
	ServerPassFile  string
	Database        string
	TLSCA           string
	TLSServerName   string
	TLSClientCert   string
	TLSClientKey    string
	Insecure        bool
	IdP             string
	IdPConfig       string
	MigrationsDir   string
	DataDir         string
	KeyFile         string
	InstanceKeyFile string
	Realm           string
	BufferPages     int
	HostingConfirm  bool
	HostingManifest string

	NoEnv    bool
	EnvFile  string
	Explicit map[string]bool
	// Supplied reports fields provided by an explicit flag, process
	// environment, or dotenv file rather than a built-in default.
	Supplied map[string]bool

	inlinePasswordEnv string
	// oidcCredential, when set by an external-identity login, is the NSSC1.
	// credential presented in place of a password.
	oidcCredential string
}

// Defaults are built-in values used when no flag, env, or file sets a field.
func Defaults() Settings {
	return Settings{
		Addr:          config.DefaultListenAddr,
		MigrationsDir: defaultMigrationsDir,
		BufferPages:   config.DefaultBufferPages,
		Explicit:      map[string]bool{},
		Supplied:      map[string]bool{},
	}
}

// Resolve merges FlagSet values (after Parse), process environment, and dotenv files.
// Priority: explicit flags > non-empty process env > .env.local (cwd) > .env (walk-up) > defaults.
// args is accepted for callers that pass os.Args; flags come from fs.
func Resolve(fs *flag.FlagSet, args []string) (Settings, error) {
	_ = args
	s := Defaults()
	s.Explicit = visitedFlags(fs)

	if v, ok := flagString(fs, "no-env"); ok && s.Explicit["no-env"] {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Settings{}, nerr.New(nerr.InvalidArgument, "cli.Resolve", "invalid --no-env")
		}
		s.NoEnv = b
	}
	if s.Explicit["env-file"] {
		s.EnvFile, _ = flagString(fs, "env-file")
		s.EnvFile = strings.TrimSpace(s.EnvFile)
	}

	files, err := loadSettingsFiles(s)
	if err != nil {
		return Settings{}, err
	}

	s.Addr = pickString(s.Explicit, fs, "addr", envAddr, files, s.Addr)
	s.User = pickString(s.Explicit, fs, "user", envDatabaseUser, files, s.User)
	s.PasswordFile = pickString(s.Explicit, fs, "password-file", envDatabasePassFile, files, s.PasswordFile)
	s.ServerUser = pickString(s.Explicit, fs, "server-user", envServerUser, files, s.ServerUser)
	s.ServerPass = pickSecret(envServerPass, files)
	s.ServerPassFile = pickString(s.Explicit, fs, "server-password-file", envServerPassFile, files, s.ServerPassFile)
	s.Database = pickString(s.Explicit, fs, "database", envDatabase, files, s.Database)
	s.TLSCA = pickString(s.Explicit, fs, "tls-ca", envTLSCA, files, s.TLSCA)
	s.TLSServerName = pickString(s.Explicit, fs, "tls-server-name", envTLSServerName, files, s.TLSServerName)
	s.TLSClientCert = pickString(s.Explicit, fs, "tls-client-cert", envTLSClientCert, files, s.TLSClientCert)
	s.TLSClientKey = pickString(s.Explicit, fs, "tls-client-key", envTLSClientKey, files, s.TLSClientKey)
	s.IdP = pickString(s.Explicit, fs, "idp", envIdP, files, s.IdP)
	s.IdPConfig = pickString(s.Explicit, fs, "idp-config", envIdPConfig, files, s.IdPConfig)
	s.MigrationsDir = pickString(s.Explicit, fs, "dir", envMigrationDir, files, s.MigrationsDir)
	s.DataDir = pickString(s.Explicit, fs, "data-dir", envDataDir, files, s.DataDir)
	s.KeyFile = pickString(s.Explicit, fs, "key-file", envKeyFile, files, s.KeyFile)
	s.InstanceKeyFile = pickString(s.Explicit, fs, "instance-key-file", envInstanceKey, files, s.InstanceKeyFile)
	s.Realm = pickString(s.Explicit, fs, "realm", envRealmName, files, s.Realm)
	s.Password = pickSecret(envDatabasePass, files)
	if s.Password != "" {
		s.inlinePasswordEnv = envDatabasePass
	}
	insecure, err := pickBool(s.Explicit, fs, "insecure", envInsecure, files, s.Insecure)
	if err != nil {
		return Settings{}, err
	}
	s.Insecure = insecure

	pages, err := pickInt(s.Explicit, fs, "buffer-pages", envBufferPages, files, s.BufferPages)
	if err != nil {
		return Settings{}, err
	}
	s.BufferPages = pages

	hostingConfirm, err := pickBool(s.Explicit, fs, "confirm", envHostingConfirm, files, s.HostingConfirm)
	if err != nil {
		return Settings{}, err
	}
	s.HostingConfirm = hostingConfirm
	s.HostingManifest = pickString(s.Explicit, fs, "hosting-manifest", envHostingManifest, files, s.HostingManifest)
	for _, field := range []struct {
		flagName string
		envKey   string
	}{
		{"addr", envAddr}, {"user", envDatabaseUser}, {"password-file", envDatabasePassFile},
		{"server-user", envServerUser}, {"server-pass", envServerPass}, {"server-password-file", envServerPassFile},
		{"database", envDatabase}, {"realm", envRealmName}, {"data-dir", envDataDir},
		{"key-file", envKeyFile}, {"instance-key-file", envInstanceKey},
		{"buffer-pages", envBufferPages}, {"confirm", envHostingConfirm}, {"hosting-manifest", envHostingManifest},
	} {
		s.Supplied[field.flagName] = supplied(s.Explicit, field.flagName, field.envKey, files)
	}
	return s, nil
}

func supplied(explicit map[string]bool, flagName, envKey string, files map[string]string) bool {
	if explicit[flagName] {
		return true
	}
	if lookupEnv(envKey) != "" {
		return true
	}
	return strings.TrimSpace(files[envKey]) != ""
}

func loadSettingsFiles(s Settings) (map[string]string, error) {
	out := make(map[string]string)
	if s.NoEnv {
		return out, nil
	}
	if s.Explicit["env-file"] {
		if s.EnvFile == "" {
			return nil, nerr.New(nerr.InvalidArgument, "cli.Resolve", "--env-file requires a path")
		}
		m, err := loadDotenvFile(s.EnvFile)
		if err != nil {
			return nil, err
		}
		mergeNonEmpty(out, m)
		return out, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "cli.Resolve", "getcwd", err)
	}
	base, local := discoverEnvFiles(cwd)
	if base != "" {
		m, err := loadDotenvFile(base)
		if err != nil {
			return nil, err
		}
		mergeNonEmpty(out, m)
	}
	if local != "" {
		m, err := loadDotenvFile(local)
		if err != nil {
			return nil, err
		}
		mergeNonEmpty(out, m)
	}
	return out, nil
}

func mergeNonEmpty(dst, src map[string]string) {
	for k, v := range src {
		if strings.TrimSpace(v) == "" {
			continue
		}
		dst[k] = v
	}
}

func pickString(explicit map[string]bool, fs *flag.FlagSet, flagName, envKey string, files map[string]string, def string) string {
	if explicit[flagName] {
		v, _ := flagString(fs, flagName)
		return strings.TrimSpace(v)
	}
	if v := lookupEnv(envKey); v != "" {
		return v
	}
	if v := strings.TrimSpace(files[envKey]); v != "" {
		return v
	}
	return def
}

func pickSecret(envKey string, files map[string]string) string {
	if v, ok := os.LookupEnv(envKey); ok && strings.TrimSpace(v) != "" {
		return v
	}
	if v := files[envKey]; strings.TrimSpace(v) != "" {
		return v
	}
	return ""
}

func pickBool(explicit map[string]bool, fs *flag.FlagSet, flagName, envKey string, files map[string]string, def bool) (bool, error) {
	if explicit[flagName] {
		v, _ := flagString(fs, flagName)
		b, err := strconv.ParseBool(v)
		if err != nil {
			return false, nerr.New(nerr.InvalidArgument, "cli.Resolve", "invalid boolean flag")
		}
		return b, nil
	}
	if v := lookupEnv(envKey); v != "" {
		return parseEnvBool(envKey, v)
	}
	if v := strings.TrimSpace(files[envKey]); v != "" {
		return parseEnvBool(envKey, v)
	}
	return def, nil
}

func pickInt(explicit map[string]bool, fs *flag.FlagSet, flagName, envKey string, files map[string]string, def int) (int, error) {
	if explicit[flagName] {
		v, _ := flagString(fs, flagName)
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, nerr.New(nerr.InvalidArgument, "cli.Resolve", "invalid integer flag")
		}
		return n, nil
	}
	if v := lookupEnv(envKey); v != "" {
		return parseEnvInt(envKey, v)
	}
	if v := strings.TrimSpace(files[envKey]); v != "" {
		return parseEnvInt(envKey, v)
	}
	return def, nil
}

func parseEnvBool(key, v string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes":
		return true, nil
	case "false", "0", "no":
		return false, nil
	default:
		return false, nerr.New(nerr.InvalidArgument, "cli.Resolve", key+" must be true, false, 1, 0, yes, or no")
	}
}

func parseEnvInt(key, v string) (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return 0, nerr.New(nerr.InvalidArgument, "cli.Resolve", key+" must be an integer")
	}
	return n, nil
}

func lookupEnv(key string) string {
	v, ok := os.LookupEnv(key)
	if !ok {
		return ""
	}
	return strings.TrimSpace(v)
}

func visitedFlags(fs *flag.FlagSet) map[string]bool {
	set := map[string]bool{}
	if fs == nil {
		return set
	}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	return set
}

func flagString(fs *flag.FlagSet, name string) (string, bool) {
	if fs == nil {
		return "", false
	}
	f := fs.Lookup(name)
	if f == nil {
		return "", false
	}
	return f.Value.String(), true
}
