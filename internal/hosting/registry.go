// Package hosting owns the deployment-level registry used to bootstrap
// multi-database hosting. It is intentionally independent of every user
// database catalog.
package hosting

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/bzync/nextsql/internal/crypto"
	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

const (
	RegistryFileName = "nextsql.instance"

	manifestMagic          = "NSRM"
	manifestVersion        = 3
	manifestVersionInitial = 1
	// manifestVersionKeyRef is the first version with the managed-database
	// KeyRef field; manifestVersionCaps is the first with realm/database
	// StorageCapBytes.
	manifestVersionKeyRef = 2
	manifestVersionCaps   = 3
	fileMagic             = "NSRE"
	fileVersion           = 1

	maxNameLen       = 63
	maxKeyRefLen     = 1024
	maxRealms        = 1024
	maxDatabases     = 8192
	maxDatabasesEach = 4096
	maxManifestBytes = 4 << 20
	fileHeaderSize   = 24
	nonceSize        = 12
)

// ID is a stable, non-reusing deployment, realm, or database identity.
type ID [16]byte

func (id ID) String() string { return fmt.Sprintf("%x", id[:]) }

func (id ID) zero() bool { return id == ID{} }

// State is a durable realm/database lifecycle state.
type State uint8

const (
	StateProvisioning State = iota + 1
	StateActive
	StateSuspended
	StateDeleting
	StateTombstoned
	StateFailed
)

func (s State) valid() bool {
	return s >= StateProvisioning && s <= StateFailed
}

// CanTransition reports whether a lifecycle transition is valid. Repeating a
// state is idempotent.
func CanTransition(from, to State) bool {
	if from == to {
		return from.valid()
	}
	switch from {
	case StateProvisioning:
		return to == StateActive || to == StateFailed || to == StateDeleting
	case StateActive:
		return to == StateSuspended || to == StateFailed || to == StateDeleting
	case StateSuspended:
		return to == StateActive || to == StateFailed || to == StateDeleting
	case StateFailed:
		return to == StateProvisioning || to == StateDeleting
	case StateDeleting:
		return to == StateTombstoned
	default:
		return false
	}
}

// Layout identifies the bounded storage layout for a database.
type Layout uint8

const (
	// LayoutLegacyDefault is DATA-DIR/nextsql.db. It preserves the current
	// single-database layout during the compatibility window.
	LayoutLegacyDefault Layout = iota + 1
	// LayoutManaged is realms/<RealmID>/databases/<DatabaseID>/nextsql.db.
	LayoutManaged
)

func (l Layout) valid() bool {
	return l == LayoutLegacyDefault || l == LayoutManaged
}

// Database is one independently encrypted database registered in a realm.
// ID must equal Identity.Database so storage and routing cannot disagree.
type Database struct {
	ID       ID
	Name     string
	State    State
	Layout   Layout
	Identity format.Identity
	// KeyRef identifies the external root-key provider for managed databases.
	// Local bootstrap stores a canonical key-file path, never raw key bytes.
	// Legacy-default v1 records leave this empty.
	KeyRef string
	// StorageCapBytes is the maximum on-disk size for this database, or 0 for
	// no database-level cap. NSRM v3+. This records the operator's durable
	// intent; write-path enforcement is a documented follow-on. A non-zero
	// value may not exceed a non-zero realm cap.
	StorageCapBytes uint64
}

// Realm is a subscription/account security boundary. Row-level SQL tenants
// remain a separate concept inside each Database.
type Realm struct {
	ID    ID
	Name  string
	State State
	// StorageCapBytes is the maximum combined on-disk size across every
	// database in the realm, or 0 for no realm-level cap. NSRM v3+. A non-zero
	// value must be at least every per-database cap already set in the realm.
	StorageCapBytes uint64
	// RealmRootAuthHash is SHA-256 of the realm-root delegation secret, or all
	// zero when the deployment admin has not delegated realm-root management for
	// this realm. NSRM v3+. A holder of the matching secret may set the storage
	// caps of databases in this realm (bounded by the realm cap) without the
	// deployment registry root; it never lets them change the realm cap itself.
	RealmRootAuthHash [32]byte
	Databases         []Database
}

// Manifest is the decrypted deployment registry payload.
type Manifest struct {
	DeploymentID    ID
	Generation      uint64
	DefaultRealm    ID
	DefaultDatabase ID
	Realms          []Realm
}

// Bootstrap describes the one default realm/database adopted during init.
type Bootstrap struct {
	RealmName        string
	DatabaseName     string
	DatabaseIdentity format.Identity
	DatabaseState    State
}

// Registry serializes durable updates and owns the unlocked registry envelope.
type Registry struct {
	mu       sync.Mutex
	path     string
	envelope *crypto.Envelope
	manifest Manifest
	closed   bool
}

// Path returns the deployment registry path for a data directory.
func Path(dataDir string) string { return filepath.Join(dataDir, RegistryFileName) }

// KeyStorePath returns the wrapped-key sidecar path. It never contains the raw
// deployment root key.
func KeyStorePath(registryPath string) string { return crypto.KeystorePath(registryPath) }

// EnsureBootstrap creates a new deployment registry or opens the matching
// existing registry. If a crash left only the wrapped-key sidecar, it safely
// reconstructs and publishes the deterministic bootstrap manifest.
func EnsureBootstrap(path string, root *crypto.DEK, bootstrap Bootstrap) (*Registry, bool, error) {
	if root == nil {
		return nil, false, nerr.New(nerr.InvalidArgument, "hosting.EnsureBootstrap", "nil deployment root key")
	}
	bootstrap, err := normalizeBootstrap(bootstrap)
	if err != nil {
		return nil, false, err
	}
	if _, err := os.Stat(path); err == nil {
		reg, err := Open(path, root)
		if err != nil {
			return nil, false, err
		}
		if err := reg.matchesBootstrap(bootstrap); err != nil {
			_ = reg.Close()
			return nil, false, err
		}
		return reg, false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, nerr.Wrap(nerr.IO, "hosting.EnsureBootstrap", "stat registry", err)
	}

	keyPath := KeyStorePath(path)
	if _, err := os.Stat(keyPath); err == nil {
		env, err := crypto.OpenEnvelope(keyPath, root)
		if err != nil {
			return nil, false, err
		}
		reg := &Registry{path: path, envelope: env}
		reg.manifest = bootstrapManifest(ID(env.Identity().Database), bootstrap)
		if err := reg.persistLocked(reg.manifest); err != nil {
			_ = env.Close()
			return nil, false, err
		}
		return reg, true, nil
	} else if !os.IsNotExist(err) {
		return nil, false, nerr.Wrap(nerr.IO, "hosting.EnsureBootstrap", "stat registry keys", err)
	}

	deployment, err := newID()
	if err != nil {
		return nil, false, err
	}
	fileID, err := format.NewUUID()
	if err != nil {
		return nil, false, err
	}
	env, err := crypto.CreateEnvelope(keyPath, format.Identity{
		Database: [16]byte(deployment),
		File:     fileID,
	}, root)
	if err != nil {
		return nil, false, err
	}
	if err := syncFileAndDir(keyPath); err != nil {
		_ = env.Close()
		return nil, false, err
	}
	reg := &Registry{path: path, envelope: env}
	reg.manifest = bootstrapManifest(deployment, bootstrap)
	if err := reg.persistLocked(reg.manifest); err != nil {
		_ = env.Close()
		return nil, false, err
	}
	return reg, true, nil
}

// EnsureManifest creates or opens a deployment registry containing the full
// manifest returned by build. The builder receives the durable DeploymentID,
// allowing stable realm/database identities to be derived before publication.
// A new manifest is published in one encrypted generation; an existing
// registry must match exactly (apart from generation) for idempotent reapply.
func EnsureManifest(path string, root *crypto.DEK, build func(ID) (Manifest, error)) (*Registry, bool, error) {
	if root == nil {
		return nil, false, nerr.New(nerr.InvalidArgument, "hosting.EnsureManifest", "nil deployment root key")
	}
	if build == nil {
		return nil, false, nerr.New(nerr.InvalidArgument, "hosting.EnsureManifest", "nil manifest builder")
	}
	if _, err := os.Stat(path); err == nil {
		reg, err := Open(path, root)
		if err != nil {
			return nil, false, err
		}
		expected, err := build(reg.manifest.DeploymentID)
		if err != nil {
			_ = reg.Close()
			return nil, false, err
		}
		if err := matchManifest(reg.manifest, expected); err != nil {
			_ = reg.Close()
			return nil, false, err
		}
		return reg, false, nil
	} else if !os.IsNotExist(err) {
		return nil, false, nerr.Wrap(nerr.IO, "hosting.EnsureManifest", "stat registry", err)
	}

	keyPath := KeyStorePath(path)
	var (
		env        *crypto.Envelope
		deployment ID
		err        error
	)
	if _, statErr := os.Stat(keyPath); statErr == nil {
		env, err = crypto.OpenEnvelope(keyPath, root)
		if err != nil {
			return nil, false, err
		}
		deployment = ID(env.Identity().Database)
	} else if !os.IsNotExist(statErr) {
		return nil, false, nerr.Wrap(nerr.IO, "hosting.EnsureManifest", "stat registry keys", statErr)
	} else {
		deployment, err = newID()
		if err != nil {
			return nil, false, err
		}
		fileID, err := format.NewUUID()
		if err != nil {
			return nil, false, err
		}
		env, err = crypto.CreateEnvelope(keyPath, format.Identity{Database: [16]byte(deployment), File: fileID}, root)
		if err != nil {
			return nil, false, err
		}
		if err := syncFileAndDir(keyPath); err != nil {
			_ = env.Close()
			return nil, false, err
		}
	}
	expected, err := build(deployment)
	if err != nil {
		_ = env.Close()
		return nil, false, err
	}
	if expected.DeploymentID != deployment {
		_ = env.Close()
		return nil, false, nerr.New(nerr.InvalidArgument, "hosting.EnsureManifest", "manifest deployment identity mismatch")
	}
	reg := &Registry{path: path, envelope: env}
	if err := reg.persistLocked(expected); err != nil {
		_ = env.Close()
		return nil, false, err
	}
	return reg, true, nil
}

// Open decrypts and validates an existing registry.
func Open(path string, root *crypto.DEK) (*Registry, error) {
	if root == nil {
		return nil, nerr.New(nerr.InvalidArgument, "hosting.Open", "nil deployment root key")
	}
	env, err := crypto.OpenEnvelope(KeyStorePath(path), root)
	if err != nil {
		return nil, err
	}
	raw, err := readBounded(path, maxManifestBytes+fileHeaderSize+nonceSize+32)
	if err != nil {
		_ = env.Close()
		return nil, err
	}
	manifest, outerGeneration, err := openRegistryFile(env, raw)
	if err != nil {
		_ = env.Close()
		return nil, err
	}
	if manifest.Generation != outerGeneration {
		_ = env.Close()
		return nil, nerr.New(nerr.Corruption, "hosting.Open", "registry generation mismatch")
	}
	if manifest.DeploymentID != ID(env.Identity().Database) {
		_ = env.Close()
		return nil, nerr.New(nerr.Corruption, "hosting.Open", "registry identity does not match keystore")
	}
	if env.NonceHigh() <= outerGeneration {
		_ = env.Close()
		return nil, nerr.New(nerr.Corruption, "hosting.Open", "registry nonce high-water moved backwards")
	}
	return &Registry{path: path, envelope: env, manifest: manifest}, nil
}

// Manifest returns a detached copy safe for callers to inspect.
func (r *Registry) Manifest() Manifest {
	if r == nil {
		return Manifest{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return cloneManifest(r.manifest)
}

// Default returns detached copies of the configured compatibility
// realm/database pair.
func (r *Registry) Default() (Realm, Database, error) {
	if r == nil {
		return Realm{}, Database{}, nerr.New(nerr.InvalidArgument, "hosting.Default", "nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, realm := range r.manifest.Realms {
		if realm.ID != r.manifest.DefaultRealm {
			continue
		}
		for _, db := range realm.Databases {
			if db.ID == r.manifest.DefaultDatabase {
				realm.Databases = append([]Database(nil), realm.Databases...)
				return realm, db, nil
			}
		}
	}
	return Realm{}, Database{}, nerr.New(nerr.Corruption, "hosting.Default", "default realm/database does not resolve")
}

// Lookup returns detached copies of the named realm and one of its
// databases. Names are matched case-insensitively via normalizeName,
// mirroring every other name-taking Registry method. Used by dbmanager
// (M2-3a) to resolve a connection's Hello-selected realm/database to the
// registry records needed to open it.
func (r *Registry) Lookup(realmName, databaseName string) (Realm, Database, error) {
	if r == nil {
		return Realm{}, Database{}, nerr.New(nerr.InvalidArgument, "hosting.Lookup", "nil registry")
	}
	rName, err := normalizeName(realmName, nerr.InvalidArgument)
	if err != nil {
		return Realm{}, Database{}, err
	}
	dName, err := normalizeName(databaseName, nerr.InvalidArgument)
	if err != nil {
		return Realm{}, Database{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, realm := range r.manifest.Realms {
		if realm.Name != rName {
			continue
		}
		for _, db := range realm.Databases {
			if db.Name == dName {
				realm.Databases = append([]Database(nil), realm.Databases...)
				return realm, db, nil
			}
		}
		return Realm{}, Database{}, nerr.New(nerr.NotFound, "hosting.Lookup", "unknown database")
	}
	return Realm{}, Database{}, nerr.New(nerr.NotFound, "hosting.Lookup", "unknown realm")
}

// LookupRealm returns a detached copy of the named realm, without requiring
// a database name. Mirrors Lookup's name matching (case-insensitive via
// normalizeName). Used by realm-scoped authorization (M2-4b-1) to resolve a
// connection's Hello-selected realm to its stable ID before any auth check.
func (r *Registry) LookupRealm(realmName string) (Realm, error) {
	if r == nil {
		return Realm{}, nerr.New(nerr.InvalidArgument, "hosting.LookupRealm", "nil registry")
	}
	rName, err := normalizeName(realmName, nerr.InvalidArgument)
	if err != nil {
		return Realm{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, realm := range r.manifest.Realms {
		if realm.Name != rName {
			continue
		}
		realm.Databases = append([]Database(nil), realm.Databases...)
		return realm, nil
	}
	return Realm{}, nerr.New(nerr.NotFound, "hosting.LookupRealm", "unknown realm")
}

// SetDatabaseState durably applies one validated lifecycle transition.
func (r *Registry) SetDatabaseState(realmID, databaseID ID, state State) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.SetDatabaseState", "nil registry")
	}
	if !state.valid() {
		return nerr.New(nerr.InvalidArgument, "hosting.SetDatabaseState", "invalid database state")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nerr.New(nerr.Unavailable, "hosting.SetDatabaseState", "registry is closed")
	}
	next := cloneManifest(r.manifest)
	for ri := range next.Realms {
		if next.Realms[ri].ID != realmID {
			continue
		}
		for di := range next.Realms[ri].Databases {
			db := &next.Realms[ri].Databases[di]
			if db.ID != databaseID {
				continue
			}
			if !CanTransition(db.State, state) {
				return nerr.New(nerr.Conflict, "hosting.SetDatabaseState", "invalid database lifecycle transition")
			}
			db.State = state
			if err := r.persistLocked(next); err != nil {
				return err
			}
			return nil
		}
		return nerr.New(nerr.NotFound, "hosting.SetDatabaseState", "unknown database")
	}
	return nerr.New(nerr.NotFound, "hosting.SetDatabaseState", "unknown realm")
}

// SetAllDatabaseStates atomically advances every registered database to state
// in one encrypted registry generation. It is used by declarative bootstrap so
// a partially provisioned set is never partially advertised ACTIVE.
func (r *Registry) SetAllDatabaseStates(state State) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.SetAllDatabaseStates", "nil registry")
	}
	if !state.valid() {
		return nerr.New(nerr.InvalidArgument, "hosting.SetAllDatabaseStates", "invalid database state")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nerr.New(nerr.Unavailable, "hosting.SetAllDatabaseStates", "registry is closed")
	}
	next := cloneManifest(r.manifest)
	changed := false
	for ri := range next.Realms {
		for di := range next.Realms[ri].Databases {
			db := &next.Realms[ri].Databases[di]
			if db.State == state {
				continue
			}
			if !CanTransition(db.State, state) {
				return nerr.New(nerr.Conflict, "hosting.SetAllDatabaseStates", "invalid database lifecycle transition")
			}
			db.State = state
			changed = true
		}
	}
	if !changed {
		return nil
	}
	return r.persistLocked(next)
}

// SetDatabaseStorageCap durably sets the per-database on-disk storage cap in
// bytes; 0 clears the database-level cap. It fails closed when the cap would
// exceed a non-zero realm-level cap. Enforcement on the write path is a
// documented follow-on; this records the operator's intent durably.
//
// This is the hosting/deployment-admin entry point (the caller holds the
// registry root). A realm-root secret holder uses
// SetDatabaseStorageCapAsRealmRoot instead.
func (r *Registry) SetDatabaseStorageCap(realmID, databaseID ID, capBytes uint64) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.SetDatabaseStorageCap", "nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.setDatabaseStorageCapLocked("hosting.SetDatabaseStorageCap", realmID, databaseID, capBytes, nil)
}

// SetDatabaseStorageCapAsRealmRoot durably sets one per-database storage cap
// after authenticating secret against the realm's configured realm-root
// delegation secret. It enforces the realm-cap ceiling exactly as the admin
// entry point does and can never change the realm cap itself. It fails closed
// when the realm has no realm-root delegation configured or the secret does not
// match.
func (r *Registry) SetDatabaseStorageCapAsRealmRoot(realmID, databaseID ID, capBytes uint64, secret []byte) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.SetDatabaseStorageCapAsRealmRoot", "nil registry")
	}
	if len(secret) == 0 {
		return nerr.New(nerr.Unauthorized, "hosting.SetDatabaseStorageCapAsRealmRoot", "realm-root secret is required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.setDatabaseStorageCapLocked("hosting.SetDatabaseStorageCapAsRealmRoot", realmID, databaseID, capBytes, secret)
}

// setDatabaseStorageCapLocked applies one per-database cap change. When secret
// is non-nil the realm must have a realm-root delegation hash and secret must
// match it; when secret is nil the caller is trusted (admin path). r.mu is held.
func (r *Registry) setDatabaseStorageCapLocked(op string, realmID, databaseID ID, capBytes uint64, secret []byte) error {
	if r.closed {
		return nerr.New(nerr.Unavailable, op, "registry is closed")
	}
	next := cloneManifest(r.manifest)
	for ri := range next.Realms {
		if next.Realms[ri].ID != realmID {
			continue
		}
		realm := &next.Realms[ri]
		if secret != nil {
			if realm.RealmRootAuthHash == ([32]byte{}) {
				return nerr.New(nerr.Forbidden, op, "realm-root cap management is not delegated for this realm")
			}
			got := sha256.Sum256(secret)
			if subtle.ConstantTimeCompare(got[:], realm.RealmRootAuthHash[:]) != 1 {
				return nerr.New(nerr.Unauthorized, op, "realm-root secret does not match")
			}
		}
		realmCap := realm.StorageCapBytes
		for di := range realm.Databases {
			db := &realm.Databases[di]
			if db.ID != databaseID {
				continue
			}
			if capBytes != 0 && realmCap != 0 && capBytes > realmCap {
				return nerr.New(nerr.InvalidArgument, op, "database storage cap exceeds realm storage cap")
			}
			if db.StorageCapBytes == capBytes {
				return nil
			}
			db.StorageCapBytes = capBytes
			return r.persistLocked(next)
		}
		return nerr.New(nerr.NotFound, op, "unknown database")
	}
	return nerr.New(nerr.NotFound, op, "unknown realm")
}

// EffectiveStorageCapBytes returns the binding data-file growth cap for a
// database: the smaller of the realm and database caps, treating 0 as
// unlimited at each level. 0 means neither level caps the database.
func EffectiveStorageCapBytes(realmCap, databaseCap uint64) uint64 {
	switch {
	case realmCap == 0:
		return databaseCap
	case databaseCap == 0:
		return realmCap
	case databaseCap < realmCap:
		return databaseCap
	default:
		return realmCap
	}
}

// SetRealmRootAuth sets or clears the realm-root delegation secret for a realm.
// A non-empty secret (at least 16 bytes) lets a holder set the per-database
// storage caps of databases in this realm via SetDatabaseStorageCapAsRealmRoot,
// bounded by the realm cap, without the deployment registry root. An empty
// secret clears the delegation. Only the deployment/hosting admin calls this.
func (r *Registry) SetRealmRootAuth(realmID ID, secret []byte) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.SetRealmRootAuth", "nil registry")
	}
	var want [32]byte
	if len(secret) > 0 {
		if len(secret) < 16 {
			return nerr.New(nerr.InvalidArgument, "hosting.SetRealmRootAuth", "realm-root secret must be at least 16 bytes")
		}
		want = sha256.Sum256(secret)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nerr.New(nerr.Unavailable, "hosting.SetRealmRootAuth", "registry is closed")
	}
	next := cloneManifest(r.manifest)
	for ri := range next.Realms {
		if next.Realms[ri].ID != realmID {
			continue
		}
		if next.Realms[ri].RealmRootAuthHash == want {
			return nil
		}
		next.Realms[ri].RealmRootAuthHash = want
		return r.persistLocked(next)
	}
	return nerr.New(nerr.NotFound, "hosting.SetRealmRootAuth", "unknown realm")
}

// SetRealmStorageCap durably sets the realm-wide on-disk storage cap in bytes;
// 0 clears it. A non-zero cap must be at least every per-database cap already
// set in the realm. Enforcement on the write path is a documented follow-on.
func (r *Registry) SetRealmStorageCap(realmID ID, capBytes uint64) error {
	if r == nil {
		return nerr.New(nerr.InvalidArgument, "hosting.SetRealmStorageCap", "nil registry")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nerr.New(nerr.Unavailable, "hosting.SetRealmStorageCap", "registry is closed")
	}
	next := cloneManifest(r.manifest)
	for ri := range next.Realms {
		if next.Realms[ri].ID != realmID {
			continue
		}
		if capBytes != 0 {
			for _, db := range next.Realms[ri].Databases {
				if db.StorageCapBytes != 0 && db.StorageCapBytes > capBytes {
					return nerr.New(nerr.InvalidArgument, "hosting.SetRealmStorageCap", "realm storage cap is below an existing database cap")
				}
			}
		}
		if next.Realms[ri].StorageCapBytes == capBytes {
			return nil
		}
		next.Realms[ri].StorageCapBytes = capBytes
		return r.persistLocked(next)
	}
	return nerr.New(nerr.NotFound, "hosting.SetRealmStorageCap", "unknown realm")
}

// CreateRealm durably creates a new realm together with its first database
// in one registry generation — a realm may never have zero databases (see
// validateManifest), so realm and database creation cannot be split into
// two independent operations the way SetDatabaseState mutates one already
// present. The database starts in StateProvisioning; the caller is
// responsible for physically creating the database file at
// ManagedDatabasePath(dataDir, realm.ID, database.ID) and then calling
// SetDatabaseState to publish StateActive once it has verified the file
// opens (see cmd/nextsql's createOrResumeDatabase for the established
// create-or-resume pattern this is meant to be paired with).
//
// The realm identity is derived deterministically from its name
// (deriveRealmID), like the declarative-bootstrap path already does, so a
// retried call with the same name always targets the same realm instead of
// risking an orphaned duplicate. If the realm already exists, this call
// forwards to the same idempotent database-append logic CreateDatabase
// uses: reapplying with a database of the same name and identity, already
// in StateProvisioning or StateActive, returns the existing records with
// created=false rather than erroring — matching EnsureBootstrap's reapply
// semantics — so a caller that crashes between this call and physically
// creating the database file can safely retry from scratch.
func (r *Registry) CreateRealm(realmName, databaseName string, identity format.Identity, keyRef string) (Realm, Database, bool, error) {
	const op = "hosting.CreateRealm"
	if r == nil {
		return Realm{}, Database{}, false, nerr.New(nerr.InvalidArgument, op, "nil registry")
	}
	rName, err := normalizeName(realmName, nerr.InvalidArgument)
	if err != nil {
		return Realm{}, Database{}, false, err
	}
	dName, err := normalizeName(databaseName, nerr.InvalidArgument)
	if err != nil {
		return Realm{}, Database{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Realm{}, Database{}, false, nerr.New(nerr.Unavailable, op, "registry is closed")
	}
	for _, realm := range r.manifest.Realms {
		if realm.Name != rName {
			continue
		}
		db, created, err := r.addDatabaseLocked(op, realm.ID, dName, identity, keyRef)
		if err != nil {
			return Realm{}, Database{}, false, err
		}
		for _, updated := range r.manifest.Realms {
			if updated.ID == realm.ID {
				return updated, db, created, nil
			}
		}
		return Realm{}, Database{}, false, nerr.New(nerr.Internal, op, "realm not found after update")
	}
	realmID := deriveRealmID(r.manifest.DeploymentID, rName)
	next := cloneManifest(r.manifest)
	next.Realms = append(next.Realms, Realm{
		ID:    realmID,
		Name:  rName,
		State: StateActive,
		Databases: []Database{{
			ID:       ID(identity.Database),
			Name:     dName,
			State:    StateProvisioning,
			Layout:   LayoutManaged,
			Identity: identity,
			KeyRef:   keyRef,
		}},
	})
	if err := r.persistLocked(next); err != nil {
		return Realm{}, Database{}, false, err
	}
	for _, realm := range r.manifest.Realms {
		if realm.ID == realmID {
			return realm, realm.Databases[0], true, nil
		}
	}
	return Realm{}, Database{}, false, nerr.New(nerr.Internal, op, "created realm not found after persist")
}

// CreateDatabase durably adds a new database (StateProvisioning) to an
// existing, active realm. Same create-or-resume responsibility split and
// idempotent-reapply semantics as CreateRealm's database-append path.
func (r *Registry) CreateDatabase(realmID ID, databaseName string, identity format.Identity, keyRef string) (Database, bool, error) {
	const op = "hosting.CreateDatabase"
	if r == nil {
		return Database{}, false, nerr.New(nerr.InvalidArgument, op, "nil registry")
	}
	dName, err := normalizeName(databaseName, nerr.InvalidArgument)
	if err != nil {
		return Database{}, false, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return Database{}, false, nerr.New(nerr.Unavailable, op, "registry is closed")
	}
	return r.addDatabaseLocked(op, realmID, dName, identity, keyRef)
}

// addDatabaseLocked appends a new database to realmID, or — if a database
// with this normalized name already exists in that realm — validates it
// matches (same identity, State in {Provisioning, Active}) and returns it
// unchanged (created=false) rather than erroring. r.mu is held; dName is
// already normalized.
func (r *Registry) addDatabaseLocked(op string, realmID ID, dName string, identity format.Identity, keyRef string) (Database, bool, error) {
	for ri := range r.manifest.Realms {
		realm := &r.manifest.Realms[ri]
		if realm.ID != realmID {
			continue
		}
		if realm.State != StateActive {
			return Database{}, false, nerr.New(nerr.Conflict, op, "realm is not active")
		}
		for _, db := range realm.Databases {
			if db.Name != dName {
				continue
			}
			if db.Identity != identity {
				return Database{}, false, nerr.New(nerr.AlreadyExists, op, "database name is already registered with a different identity")
			}
			switch db.State {
			case StateProvisioning, StateActive:
				return db, false, nil
			default:
				return Database{}, false, nerr.New(nerr.Conflict, op, "database exists but is not resumable from its current state")
			}
		}
		databaseID := ID(identity.Database)
		for _, otherRealm := range r.manifest.Realms {
			for _, db := range otherRealm.Databases {
				if db.ID == databaseID {
					return Database{}, false, nerr.New(nerr.Conflict, op, "database identity collides with an existing registration")
				}
			}
		}
		next := cloneManifest(r.manifest)
		newDB := Database{
			ID:       databaseID,
			Name:     dName,
			State:    StateProvisioning,
			Layout:   LayoutManaged,
			Identity: identity,
			KeyRef:   keyRef,
		}
		for nri := range next.Realms {
			if next.Realms[nri].ID == realmID {
				next.Realms[nri].Databases = append(next.Realms[nri].Databases, newDB)
				break
			}
		}
		if err := r.persistLocked(next); err != nil {
			return Database{}, false, err
		}
		return newDB, true, nil
	}
	return Database{}, false, nerr.New(nerr.NotFound, op, "unknown realm")
}

// Close zeros the unlocked registry keys.
func (r *Registry) Close() error {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	r.closed = true
	if r.envelope != nil {
		return r.envelope.Close()
	}
	return nil
}

func (r *Registry) matchesBootstrap(b Bootstrap) error {
	m := r.Manifest()
	if len(m.Realms) != 1 || m.Realms[0].Name != b.RealmName || len(m.Realms[0].Databases) != 1 {
		return nerr.New(nerr.Conflict, "hosting.EnsureBootstrap", "existing registry does not match bootstrap realm")
	}
	db := m.Realms[0].Databases[0]
	if db.Name != b.DatabaseName || db.Identity != b.DatabaseIdentity {
		return nerr.New(nerr.Conflict, "hosting.EnsureBootstrap", "existing registry does not match bootstrap database")
	}
	return nil
}

func matchManifest(current, expected Manifest) error {
	expected.Generation = current.Generation
	currentRaw, err := EncodeManifest(current)
	if err != nil {
		return err
	}
	expectedRaw, err := EncodeManifest(expected)
	if err != nil {
		return err
	}
	if !bytes.Equal(currentRaw, expectedRaw) {
		// Declarative bootstrap initially describes every database as
		// PROVISIONING. An exact reapply after the one-shot activation is still
		// idempotent; other lifecycle states remain conflicts.
		activated := cloneManifest(current)
		for ri := range activated.Realms {
			for di := range activated.Realms[ri].Databases {
				if activated.Realms[ri].Databases[di].State == StateActive {
					activated.Realms[ri].Databases[di].State = StateProvisioning
				}
			}
		}
		activated.Generation = current.Generation
		activatedRaw, activatedErr := EncodeManifest(activated)
		if activatedErr != nil || !bytes.Equal(activatedRaw, expectedRaw) {
			return nerr.New(nerr.Conflict, "hosting.EnsureManifest", "existing registry does not match bootstrap manifest")
		}
	}
	return nil
}

func (r *Registry) persistLocked(next Manifest) error {
	if r.envelope == nil {
		return nerr.New(nerr.Internal, "hosting.persist", "nil registry envelope")
	}
	generation := r.envelope.NonceHigh()
	if generation == 0 || generation == math.MaxUint64 {
		return nerr.New(nerr.Exhausted, "hosting.persist", "registry nonce generation exhausted")
	}
	next.Generation = generation
	plain, err := EncodeManifest(next)
	if err != nil {
		return err
	}
	key, err := r.envelope.Current()
	if err != nil {
		return err
	}
	header := make([]byte, fileHeaderSize)
	copy(header[0:4], fileMagic)
	encoding.PutU16(header, 4, fileVersion)
	encoding.PutU16(header, 6, uint16(key.Suite))
	encoding.PutU32(header, 8, uint32(key.Version))
	encoding.PutU64(header, 12, generation)
	encoding.PutU32(header, 20, uint32(len(plain)))

	// Persist the next high-water before using this generation. A crash can
	// skip a generation but cannot reuse one.
	if err := r.envelope.NoteNonceHigh(generation + 1); err != nil {
		return err
	}
	if err := syncFileAndDir(r.envelope.Path()); err != nil {
		return err
	}
	nonce, ciphertext, err := crypto.SealBytes(key, generation, header, plain)
	if err != nil {
		return err
	}
	if len(nonce) != nonceSize {
		return nerr.New(nerr.Internal, "hosting.persist", "unexpected nonce size")
	}
	raw := make([]byte, 0, len(header)+len(nonce)+len(ciphertext))
	raw = append(raw, header...)
	raw = append(raw, nonce...)
	raw = append(raw, ciphertext...)
	if err := writeAtomicDurable(r.path, raw); err != nil {
		return err
	}
	r.manifest = cloneManifest(next)
	return nil
}

func openRegistryFile(env *crypto.Envelope, raw []byte) (Manifest, uint64, error) {
	if len(raw) < fileHeaderSize+nonceSize+16 {
		return Manifest{}, 0, nerr.New(nerr.InvalidFormat, "hosting.Open", "truncated registry file")
	}
	header := raw[:fileHeaderSize]
	if string(header[0:4]) != fileMagic {
		return Manifest{}, 0, nerr.New(nerr.InvalidFormat, "hosting.Open", "bad registry file magic")
	}
	if encoding.U16(header, 4) != fileVersion {
		return Manifest{}, 0, nerr.New(nerr.InvalidFormat, "hosting.Open", "unsupported registry file version")
	}
	if format.CipherSuite(encoding.U16(header, 6)) != format.CipherAES256GCM {
		return Manifest{}, 0, nerr.New(nerr.InvalidFormat, "hosting.Open", "unsupported registry cipher suite")
	}
	keyVersion := format.KeyVersion(encoding.U32(header, 8))
	generation := encoding.U64(header, 12)
	plainLen := uint64(encoding.U32(header, 20))
	if generation == 0 || plainLen > maxManifestBytes {
		return Manifest{}, 0, nerr.New(nerr.InvalidFormat, "hosting.Open", "invalid registry file limits")
	}
	want := uint64(fileHeaderSize+nonceSize+16) + plainLen
	if want != uint64(len(raw)) {
		return Manifest{}, 0, nerr.New(nerr.InvalidFormat, "hosting.Open", "registry file length mismatch")
	}
	key, err := env.Key(keyVersion)
	if err != nil {
		return Manifest{}, 0, err
	}
	plain, err := crypto.OpenBytes(key, raw[fileHeaderSize:fileHeaderSize+nonceSize], header, raw[fileHeaderSize+nonceSize:])
	if err != nil {
		return Manifest{}, 0, err
	}
	manifest, err := DecodeManifest(plain)
	if err != nil {
		return Manifest{}, 0, err
	}
	return manifest, generation, nil
}

func bootstrapManifest(deployment ID, b Bootstrap) Manifest {
	realmID := deriveRealmID(deployment, b.RealmName)
	databaseID := ID(b.DatabaseIdentity.Database)
	return Manifest{
		DeploymentID:    deployment,
		DefaultRealm:    realmID,
		DefaultDatabase: databaseID,
		Realms: []Realm{{
			ID:    realmID,
			Name:  b.RealmName,
			State: StateActive,
			Databases: []Database{{
				ID:       databaseID,
				Name:     b.DatabaseName,
				State:    b.DatabaseState,
				Layout:   LayoutLegacyDefault,
				Identity: b.DatabaseIdentity,
			}},
		}},
	}
}

func normalizeBootstrap(b Bootstrap) (Bootstrap, error) {
	var err error
	b.RealmName, err = normalizeName(b.RealmName, nerr.InvalidArgument)
	if err != nil {
		return Bootstrap{}, err
	}
	b.DatabaseName, err = normalizeName(b.DatabaseName, nerr.InvalidArgument)
	if err != nil {
		return Bootstrap{}, err
	}
	if b.DatabaseIdentity.Database == [16]byte{} || b.DatabaseIdentity.File == [16]byte{} {
		return Bootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.Bootstrap", "zero database identity")
	}
	if !b.DatabaseState.valid() {
		return Bootstrap{}, nerr.New(nerr.InvalidArgument, "hosting.Bootstrap", "invalid database state")
	}
	return b, nil
}

func deriveRealmID(deployment ID, name string) ID {
	h := sha256.New()
	_, _ = h.Write([]byte("nextsql-realm-v1\x00"))
	_, _ = h.Write(deployment[:])
	_, _ = h.Write([]byte(name))
	sum := h.Sum(nil)
	var id ID
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x50
	id[8] = (id[8] & 0x3f) | 0x80
	return id
}

func newID() (ID, error) {
	u, err := format.NewUUID()
	return ID(u), err
}

func cloneManifest(in Manifest) Manifest {
	out := in
	out.Realms = make([]Realm, len(in.Realms))
	for i := range in.Realms {
		out.Realms[i] = in.Realms[i]
		out.Realms[i].Databases = append([]Database(nil), in.Realms[i].Databases...)
	}
	return out
}

func readBounded(path string, max int64) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.Open", "open registry", err)
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.Open", "stat registry", err)
	}
	if !st.Mode().IsRegular() || st.Size() < 0 || st.Size() > max {
		return nil, nerr.New(nerr.InvalidFormat, "hosting.Open", "invalid registry file size")
	}
	raw := make([]byte, st.Size())
	if _, err := io.ReadFull(f, raw); err != nil {
		return nil, nerr.Wrap(nerr.IO, "hosting.Open", "read registry", err)
	}
	return raw, nil
}

func writeAtomicDurable(path string, raw []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".nextsql-instance-*")
	if err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "create temporary registry", err)
	}
	tmpPath := tmp.Name()
	ok := false
	defer func() {
		_ = tmp.Close()
		if !ok {
			_ = os.Remove(tmpPath)
		}
	}()
	if err := tmp.Chmod(0o600); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "chmod temporary registry", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "write temporary registry", err)
	}
	if err := tmp.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "sync temporary registry", err)
	}
	if err := tmp.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "close temporary registry", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "publish registry", err)
	}
	if err := syncDir(dir); err != nil {
		return err
	}
	ok = true
	return nil
}

func syncFileAndDir(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "open durable file", err)
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return nerr.Wrap(nerr.IO, "hosting.persist", "sync durable file", err)
	}
	if err := f.Close(); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "close durable file", err)
	}
	return syncDir(filepath.Dir(path))
}

func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "open registry directory", err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return nerr.Wrap(nerr.IO, "hosting.persist", "sync registry directory", err)
	}
	return nil
}

// EncodeManifest emits deterministic NSRM v2 bytes.
func EncodeManifest(manifest Manifest) ([]byte, error) {
	manifest = cloneManifest(manifest)
	if err := validateManifest(manifest, nerr.InvalidArgument, false); err != nil {
		return nil, err
	}
	sort.Slice(manifest.Realms, func(i, j int) bool {
		return bytes.Compare(manifest.Realms[i].ID[:], manifest.Realms[j].ID[:]) < 0
	})
	for i := range manifest.Realms {
		sort.Slice(manifest.Realms[i].Databases, func(a, b int) bool {
			return bytes.Compare(manifest.Realms[i].Databases[a].ID[:], manifest.Realms[i].Databases[b].ID[:]) < 0
		})
	}
	out := make([]byte, 0, 128)
	out = append(out, manifestMagic...)
	out = appendU16(out, manifestVersion)
	out = appendU64(out, manifest.Generation)
	out = append(out, manifest.DeploymentID[:]...)
	out = append(out, manifest.DefaultRealm[:]...)
	out = append(out, manifest.DefaultDatabase[:]...)
	out = appendU16(out, uint16(len(manifest.Realms)))
	for _, realm := range manifest.Realms {
		out = append(out, realm.ID[:]...)
		out = append(out, byte(realm.State))
		out = appendString(out, realm.Name)
		out = appendU64(out, realm.StorageCapBytes)
		out = append(out, realm.RealmRootAuthHash[:]...)
		out = appendU16(out, uint16(len(realm.Databases)))
		for _, db := range realm.Databases {
			out = append(out, db.ID[:]...)
			out = append(out, db.Identity.Database[:]...)
			out = append(out, db.Identity.File[:]...)
			out = append(out, byte(db.State), byte(db.Layout))
			out = appendString(out, db.Name)
			out = appendString(out, db.KeyRef)
			out = appendU64(out, db.StorageCapBytes)
		}
	}
	if len(out) > maxManifestBytes {
		return nil, nerr.New(nerr.InvalidArgument, "hosting.EncodeManifest", "registry manifest exceeds limit")
	}
	return out, nil
}

// DecodeManifest validates untrusted NSRM v1/v2 bytes and fails closed.
func DecodeManifest(raw []byte) (Manifest, error) {
	if len(raw) < 4+2+8+16+16+16+2 || len(raw) > maxManifestBytes {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "invalid registry manifest size")
	}
	if string(raw[:4]) != manifestMagic {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "bad registry manifest magic")
	}
	version := encoding.U16(raw, 4)
	if version < manifestVersionInitial || version > manifestVersion {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "unsupported registry manifest version")
	}
	off := 6
	manifest := Manifest{Generation: encoding.U64(raw, off)}
	off += 8
	copy(manifest.DeploymentID[:], raw[off:off+16])
	off += 16
	copy(manifest.DefaultRealm[:], raw[off:off+16])
	off += 16
	copy(manifest.DefaultDatabase[:], raw[off:off+16])
	off += 16
	realmCount := int(encoding.U16(raw, off))
	off += 2
	if realmCount < 1 || realmCount > maxRealms {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "invalid realm count")
	}
	manifest.Realms = make([]Realm, 0, realmCount)
	totalDatabases := 0
	for i := 0; i < realmCount; i++ {
		if off+16+1+2 > len(raw) {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated realm")
		}
		var realm Realm
		copy(realm.ID[:], raw[off:off+16])
		off += 16
		realm.State = State(raw[off])
		off++
		var err error
		realm.Name, off, err = readString(raw, off)
		if err != nil {
			return Manifest{}, err
		}
		if version >= manifestVersionCaps {
			if off+8+32 > len(raw) {
				return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated realm storage cap")
			}
			realm.StorageCapBytes = encoding.U64(raw, off)
			off += 8
			copy(realm.RealmRootAuthHash[:], raw[off:off+32])
			off += 32
		}
		if off+2 > len(raw) {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated database count")
		}
		dbCount := int(encoding.U16(raw, off))
		off += 2
		if dbCount < 1 || dbCount > maxDatabasesEach || totalDatabases+dbCount > maxDatabases {
			return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "invalid database count")
		}
		totalDatabases += dbCount
		realm.Databases = make([]Database, 0, dbCount)
		for j := 0; j < dbCount; j++ {
			if off+16+16+16+2+2 > len(raw) {
				return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated database")
			}
			var db Database
			copy(db.ID[:], raw[off:off+16])
			off += 16
			copy(db.Identity.Database[:], raw[off:off+16])
			off += 16
			copy(db.Identity.File[:], raw[off:off+16])
			off += 16
			db.State = State(raw[off])
			db.Layout = Layout(raw[off+1])
			off += 2
			db.Name, off, err = readString(raw, off)
			if err != nil {
				return Manifest{}, err
			}
			if version >= manifestVersionKeyRef {
				db.KeyRef, off, err = readOptionalString(raw, off, maxKeyRefLen)
				if err != nil {
					return Manifest{}, err
				}
			}
			if version >= manifestVersionCaps {
				if off+8 > len(raw) {
					return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated database storage cap")
				}
				db.StorageCapBytes = encoding.U64(raw, off)
				off += 8
			}
			realm.Databases = append(realm.Databases, db)
		}
		manifest.Realms = append(manifest.Realms, realm)
	}
	if off != len(raw) {
		return Manifest{}, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "trailing registry manifest bytes")
	}
	if err := validateManifest(manifest, nerr.InvalidFormat, version == 1); err != nil {
		return Manifest{}, err
	}
	return manifest, nil
}

func validateManifest(m Manifest, code nerr.Code, allowLegacyManagedWithoutKeyRef bool) error {
	bad := func(message string) error { return nerr.New(code, "hosting.Manifest", message) }
	if m.DeploymentID.zero() || m.DefaultRealm.zero() || m.DefaultDatabase.zero() || m.Generation == 0 {
		return bad("zero registry identity or generation")
	}
	if len(m.Realms) < 1 || len(m.Realms) > maxRealms {
		return bad("invalid realm count")
	}
	realmIDs := make(map[ID]struct{}, len(m.Realms))
	realmNames := make(map[string]struct{}, len(m.Realms))
	databaseIDs := make(map[ID]struct{})
	totalDatabases := 0
	defaultFound := false
	for _, realm := range m.Realms {
		if realm.ID.zero() || !realm.State.valid() {
			return bad("invalid realm identity or state")
		}
		name, err := normalizeName(realm.Name, code)
		if err != nil || name != realm.Name {
			return bad("invalid realm name")
		}
		if _, ok := realmIDs[realm.ID]; ok {
			return bad("duplicate realm identity")
		}
		if _, ok := realmNames[name]; ok {
			return bad("duplicate realm name")
		}
		realmIDs[realm.ID] = struct{}{}
		realmNames[name] = struct{}{}
		if len(realm.Databases) < 1 || len(realm.Databases) > maxDatabasesEach {
			return bad("invalid databases per realm")
		}
		totalDatabases += len(realm.Databases)
		if totalDatabases > maxDatabases {
			return bad("too many databases")
		}
		databaseNames := make(map[string]struct{}, len(realm.Databases))
		for _, db := range realm.Databases {
			if db.ID.zero() || !db.State.valid() || !db.Layout.valid() {
				return bad("invalid database identity, state, or layout")
			}
			if realm.StorageCapBytes != 0 && db.StorageCapBytes != 0 && db.StorageCapBytes > realm.StorageCapBytes {
				return bad("database storage cap exceeds realm storage cap")
			}
			if db.ID != ID(db.Identity.Database) || db.Identity.File == [16]byte{} {
				return bad("database identity mismatch")
			}
			if db.Layout == LayoutManaged {
				if db.KeyRef == "" && allowLegacyManagedWithoutKeyRef {
					// NSRM v1 had no key-reference field.
				} else if len(db.KeyRef) < 1 || len(db.KeyRef) > maxKeyRefLen || strings.TrimSpace(db.KeyRef) != db.KeyRef || strings.IndexByte(db.KeyRef, 0) >= 0 {
					return bad("invalid managed database key reference")
				}
			} else if db.KeyRef != "" {
				return bad("legacy default database cannot contain a managed key reference")
			}
			name, err := normalizeName(db.Name, code)
			if err != nil || name != db.Name {
				return bad("invalid database name")
			}
			if _, ok := databaseIDs[db.ID]; ok {
				return bad("duplicate database identity")
			}
			if _, ok := databaseNames[name]; ok {
				return bad("duplicate database name")
			}
			databaseIDs[db.ID] = struct{}{}
			databaseNames[name] = struct{}{}
			if realm.ID == m.DefaultRealm && db.ID == m.DefaultDatabase && db.State != StateTombstoned && realm.State != StateTombstoned {
				defaultFound = true
			}
		}
	}
	if !defaultFound {
		return bad("default realm/database does not resolve")
	}
	return nil
}

func normalizeName(name string, code nerr.Code) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if len(name) < 1 || len(name) > maxNameLen {
		return "", nerr.New(code, "hosting.Name", "invalid name length")
	}
	for i, r := range name {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || (i > 0 && (r == '_' || r == '-')) {
			continue
		}
		return "", nerr.New(code, "hosting.Name", "name must be lowercase ASCII alphanumeric with internal _ or -")
	}
	return name, nil
}

func appendU16(dst []byte, v uint16) []byte {
	var b [2]byte
	encoding.PutU16(b[:], 0, v)
	return append(dst, b[:]...)
}

func appendU64(dst []byte, v uint64) []byte {
	var b [8]byte
	encoding.PutU64(b[:], 0, v)
	return append(dst, b[:]...)
}

func appendString(dst []byte, s string) []byte {
	dst = appendU16(dst, uint16(len(s)))
	return append(dst, s...)
}

func readString(raw []byte, off int) (string, int, error) {
	if off+2 > len(raw) {
		return "", off, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated string length")
	}
	n := int(encoding.U16(raw, off))
	off += 2
	if n < 1 || n > maxNameLen || off+n > len(raw) {
		return "", off, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "invalid string length")
	}
	return string(raw[off : off+n]), off + n, nil
}

func readOptionalString(raw []byte, off, max int) (string, int, error) {
	if off+2 > len(raw) {
		return "", off, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "truncated string length")
	}
	n := int(encoding.U16(raw, off))
	off += 2
	if n < 0 || n > max || off+n > len(raw) {
		return "", off, nerr.New(nerr.InvalidFormat, "hosting.DecodeManifest", "invalid string length")
	}
	return string(raw[off : off+n]), off + n, nil
}
