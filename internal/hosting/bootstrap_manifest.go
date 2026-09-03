package hosting

import (
	"bytes"
	"crypto/sha256"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
	"gopkg.in/yaml.v3"
)

const (
	bootstrapManifestVersion = 1
	maxBootstrapBytes        = 1 << 20
	maxBootstrapYAMLNodes    = 32768
	maxBootstrapYAMLDepth    = 32
)

// DeploymentBootstrap is the fully validated declarative bootstrap input.
// KeyFile values are canonical external paths and contain no key material.
type DeploymentBootstrap struct {
	Version         int
	DefaultRealm    string
	DefaultDatabase string
	Realms          []BootstrapRealm
}

type BootstrapRealm struct {
	Name      string
	Databases []BootstrapDatabase
}

type BootstrapDatabase struct {
	Name    string
	KeyFile string
}

type bootstrapYAML struct {
	Version int              `yaml:"version"`
	Default bootstrapDefault `yaml:"default"`
	Realms  []bootstrapRealm `yaml:"realms"`
}

type bootstrapDefault struct {
	Realm    string `yaml:"realm"`
	Database string `yaml:"database"`
}

type bootstrapRealm struct {
	Name      string              `yaml:"name"`
	Databases []bootstrapDatabase `yaml:"databases"`
}

type bootstrapDatabase struct {
	Name    string `yaml:"name"`
	KeyFile string `yaml:"key_file"`
}

// LoadDeploymentBootstrap reads and validates one bounded YAML document. The
// whole document and every referenced key file are validated before callers
// mutate deployment state.
func LoadDeploymentBootstrap(path string) (DeploymentBootstrap, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.LoadDeploymentBootstrap", "manifest path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.LoadDeploymentBootstrap", "stat manifest", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxBootstrapBytes {
		return DeploymentBootstrap{}, nerr.New(nerr.InvalidFormat, "hosting.LoadDeploymentBootstrap", "invalid manifest file size")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.LoadDeploymentBootstrap", "read manifest", err)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.LoadDeploymentBootstrap", "resolve manifest path", err)
	}
	return ParseDeploymentBootstrap(raw, filepath.Dir(abs))
}

// EnsureBootstrapManifestKeyFiles creates any per-database root key file
// named in the manifest that does not exist yet, each a fresh independent
// AES-256 root (mode 0600), and returns the absolute paths it created.
// It performs the same bounded read and YAML-shape validation as
// LoadDeploymentBootstrap but does not fully normalize the document — full
// validation (including key independence and the default-pair check) still
// happens in the subsequent LoadDeploymentBootstrap call, which is what a
// caller must run before mutating any deployment state. Parent directories
// are not created; a missing directory fails closed. Existing key files are
// left untouched, so re-running against an already-provisioned deployment
// creates nothing.
func EnsureBootstrapManifestKeyFiles(path string) ([]string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, nerr.New(nerr.InvalidArgument, "hosting.EnsureBootstrapManifestKeyFiles", "manifest path is required")
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.EnsureBootstrapManifestKeyFiles", "stat manifest", err)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > maxBootstrapBytes {
		return nil, nerr.New(nerr.InvalidFormat, "hosting.EnsureBootstrapManifestKeyFiles", "invalid manifest file size")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.EnsureBootstrapManifestKeyFiles", "read manifest", err)
	}
	if err := validateYAMLShape(raw); err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.EnsureBootstrapManifestKeyFiles", "resolve manifest path", err)
	}
	baseDir := filepath.Dir(abs)
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var in bootstrapYAML
	if err := dec.Decode(&in); err != nil {
		return nil, nerr.Wrap(nerr.InvalidFormat, "hosting.EnsureBootstrapManifestKeyFiles", "decode manifest", err)
	}
	seen := make(map[string]struct{})
	var created []string
	for _, realm := range in.Realms {
		for _, database := range realm.Databases {
			keyPath := strings.TrimSpace(database.KeyFile)
			if keyPath == "" || strings.IndexByte(keyPath, 0) >= 0 {
				return created, nerr.New(nerr.InvalidArgument, "hosting.EnsureBootstrapManifestKeyFiles", "database key_file is required")
			}
			if !filepath.IsAbs(keyPath) {
				keyPath = filepath.Join(baseDir, keyPath)
			}
			keyPath = filepath.Clean(keyPath)
			if len(keyPath) > maxKeyRefLen {
				return created, nerr.New(nerr.InvalidArgument, "hosting.EnsureBootstrapManifestKeyFiles", "database key_file path exceeds limit")
			}
			if _, dup := seen[keyPath]; dup {
				continue
			}
			seen[keyPath] = struct{}{}
			if _, err := os.Stat(keyPath); err == nil {
				continue
			} else if !os.IsNotExist(err) {
				return created, nerr.Wrap(nerr.IO, "hosting.EnsureBootstrapManifestKeyFiles", "stat database key_file", err)
			}
			dek, err := crypto.CreateKeyFile(keyPath, 1)
			if err != nil {
				return created, err
			}
			dek.Zero()
			created = append(created, keyPath)
		}
	}
	return created, nil
}

// ParseDeploymentBootstrap validates bounded YAML bytes. baseDir resolves
// relative key-file paths and is primarily exposed for fuzz and unit tests.
func ParseDeploymentBootstrap(raw []byte, baseDir string) (DeploymentBootstrap, error) {
	if len(raw) < 1 || len(raw) > maxBootstrapBytes {
		return DeploymentBootstrap{}, nerr.New(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "invalid manifest size")
	}
	if err := validateYAMLShape(raw); err != nil {
		return DeploymentBootstrap{}, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true)
	var in bootstrapYAML
	if err := dec.Decode(&in); err != nil {
		return DeploymentBootstrap{}, nerr.Wrap(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "decode manifest", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return DeploymentBootstrap{}, nerr.New(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "multiple YAML documents are not allowed")
		}
		return DeploymentBootstrap{}, nerr.Wrap(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "decode trailing document", err)
	}
	return normalizeDeploymentBootstrap(in, baseDir)
}

func validateYAMLShape(raw []byte) error {
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		return nerr.Wrap(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "parse YAML", err)
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nerr.New(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "multiple YAML documents are not allowed")
		}
		return nerr.Wrap(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "parse trailing YAML", err)
	}
	nodes := 0
	var walk func(*yaml.Node, int) error
	walk = func(node *yaml.Node, depth int) error {
		if node == nil {
			return nil
		}
		nodes++
		if nodes > maxBootstrapYAMLNodes || depth > maxBootstrapYAMLDepth {
			return nerr.New(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "YAML structure exceeds limits")
		}
		if node.Kind == yaml.AliasNode || node.Anchor != "" {
			return nerr.New(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "YAML aliases and anchors are not allowed")
		}
		if node.Kind == yaml.ScalarNode && len(node.Value) > maxKeyRefLen {
			return nerr.New(nerr.InvalidFormat, "hosting.ParseDeploymentBootstrap", "YAML scalar exceeds limit")
		}
		for _, child := range node.Content {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(&doc, 0)
}

func normalizeDeploymentBootstrap(in bootstrapYAML, baseDir string) (DeploymentBootstrap, error) {
	if in.Version != bootstrapManifestVersion {
		return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "unsupported bootstrap manifest version")
	}
	if len(in.Realms) < 1 || len(in.Realms) > maxRealms {
		return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "invalid realm count")
	}
	baseDir, err := filepath.Abs(baseDir)
	if err != nil {
		return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.DeploymentBootstrap", "resolve manifest directory", err)
	}
	out := DeploymentBootstrap{Version: in.Version}
	out.DefaultRealm, err = normalizeName(in.Default.Realm, nerr.InvalidArgument)
	if err != nil {
		return DeploymentBootstrap{}, err
	}
	out.DefaultDatabase, err = normalizeName(in.Default.Database, nerr.InvalidArgument)
	if err != nil {
		return DeploymentBootstrap{}, err
	}
	realmNames := make(map[string]struct{}, len(in.Realms))
	keyPaths := make(map[string]struct{})
	keyDigests := make(map[[32]byte]string)
	totalDatabases := 0
	defaultFound := false
	for _, inputRealm := range in.Realms {
		realmName, err := normalizeName(inputRealm.Name, nerr.InvalidArgument)
		if err != nil {
			return DeploymentBootstrap{}, err
		}
		if _, exists := realmNames[realmName]; exists {
			return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "duplicate realm name")
		}
		realmNames[realmName] = struct{}{}
		if len(inputRealm.Databases) < 1 || len(inputRealm.Databases) > maxDatabasesEach {
			return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "invalid databases per realm")
		}
		totalDatabases += len(inputRealm.Databases)
		if totalDatabases > maxDatabases {
			return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "too many databases")
		}
		realm := BootstrapRealm{Name: realmName}
		databaseNames := make(map[string]struct{}, len(inputRealm.Databases))
		for _, inputDatabase := range inputRealm.Databases {
			databaseName, err := normalizeName(inputDatabase.Name, nerr.InvalidArgument)
			if err != nil {
				return DeploymentBootstrap{}, err
			}
			if _, exists := databaseNames[databaseName]; exists {
				return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "duplicate database name")
			}
			databaseNames[databaseName] = struct{}{}
			keyPath := strings.TrimSpace(inputDatabase.KeyFile)
			if keyPath == "" || strings.IndexByte(keyPath, 0) >= 0 {
				return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "database key_file is required")
			}
			if !filepath.IsAbs(keyPath) {
				keyPath = filepath.Join(baseDir, keyPath)
			}
			keyPath, err = filepath.EvalSymlinks(filepath.Clean(keyPath))
			if err != nil {
				return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.DeploymentBootstrap", "resolve database key_file", err)
			}
			keyPath, err = filepath.Abs(keyPath)
			if err != nil {
				return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.DeploymentBootstrap", "resolve database key_file", err)
			}
			if len(keyPath) > maxKeyRefLen {
				return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "database key_file path exceeds limit")
			}
			if _, exists := keyPaths[keyPath]; exists {
				return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "database key_file paths must be independent")
			}
			info, err := os.Stat(keyPath)
			if err != nil {
				return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.DeploymentBootstrap", "stat database key_file", err)
			}
			if !info.Mode().IsRegular() {
				return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "database key_file must be regular")
			}
			root, err := crypto.ReadKeyFile(keyPath)
			if err != nil {
				return DeploymentBootstrap{}, err
			}
			root.Zero()
			keyRaw, err := os.ReadFile(keyPath)
			if err != nil {
				return DeploymentBootstrap{}, nerr.Wrap(nerr.IO, "hosting.DeploymentBootstrap", "read database key_file", err)
			}
			digest := sha256.Sum256(keyRaw)
			for i := range keyRaw {
				keyRaw[i] = 0
			}
			if _, exists := keyDigests[digest]; exists {
				return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "database root keys must be independent")
			}
			keyPaths[keyPath] = struct{}{}
			keyDigests[digest] = keyPath
			realm.Databases = append(realm.Databases, BootstrapDatabase{Name: databaseName, KeyFile: keyPath})
			if realmName == out.DefaultRealm && databaseName == out.DefaultDatabase {
				defaultFound = true
			}
		}
		sort.Slice(realm.Databases, func(i, j int) bool { return realm.Databases[i].Name < realm.Databases[j].Name })
		out.Realms = append(out.Realms, realm)
	}
	if !defaultFound {
		return DeploymentBootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "default realm/database does not resolve")
	}
	sort.Slice(out.Realms, func(i, j int) bool { return out.Realms[i].Name < out.Realms[j].Name })
	return out, nil
}

// RegistryManifest derives stable, non-user-overridable identities for every
// declared realm/database and returns one complete managed-layout manifest.
func (b DeploymentBootstrap) RegistryManifest(deployment ID, state State) (Manifest, error) {
	if deployment.zero() || !state.valid() || b.Version != bootstrapManifestVersion {
		return Manifest{}, nerr.New(nerr.InvalidArgument, "hosting.DeploymentBootstrap", "invalid deployment bootstrap state")
	}
	manifest := Manifest{DeploymentID: deployment, Generation: 1}
	for _, inputRealm := range b.Realms {
		realmID := deriveRealmID(deployment, inputRealm.Name)
		realm := Realm{ID: realmID, Name: inputRealm.Name, State: StateActive}
		for _, inputDatabase := range inputRealm.Databases {
			identity := deriveDatabaseIdentity(deployment, inputRealm.Name, inputDatabase.Name)
			database := Database{
				ID:       ID(identity.Database),
				Name:     inputDatabase.Name,
				State:    state,
				Layout:   LayoutManaged,
				Identity: identity,
				KeyRef:   inputDatabase.KeyFile,
			}
			realm.Databases = append(realm.Databases, database)
			if inputRealm.Name == b.DefaultRealm && inputDatabase.Name == b.DefaultDatabase {
				manifest.DefaultRealm = realmID
				manifest.DefaultDatabase = database.ID
			}
		}
		manifest.Realms = append(manifest.Realms, realm)
	}
	if err := validateManifest(manifest, nerr.InvalidArgument, false); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

// ManagedDatabasePath returns the ID-based managed database location.
func ManagedDatabasePath(dataDir string, realmID, databaseID ID) string {
	return filepath.Join(dataDir, "realms", realmID.String(), "databases", databaseID.String(), "nextsql.db")
}

func deriveDatabaseIdentity(deployment ID, realmName, databaseName string) format.Identity {
	database := deriveUUID("nextsql-database-v1\x00", deployment, realmName, databaseName)
	file := deriveUUID("nextsql-database-file-v1\x00", deployment, realmName, databaseName)
	return format.Identity{Database: database, File: file}
}

func deriveUUID(domain string, deployment ID, realmName, databaseName string) [16]byte {
	h := sha256.New()
	_, _ = h.Write([]byte(domain))
	_, _ = h.Write(deployment[:])
	_, _ = h.Write([]byte(realmName))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(databaseName))
	sum := h.Sum(nil)
	var id [16]byte
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}
