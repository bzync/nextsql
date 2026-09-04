package crypto

import (
	"os"
	"sync"

	"github.com/bzync/nextsql/internal/encoding"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// ShredPhrase is the exact confirmation required to destroy the only remaining keys.
const ShredPhrase = "NO KEY = NO RECOVERY"

// RevokeEvent is emitted when a key version is revoked or the envelope is shredded.
// It never contains key material.
type RevokeEvent struct {
	Kind    string
	Domain  byte
	Version format.KeyVersion
}

// Envelope is the unlocked key hierarchy:
//
//	root unlock → KEK → database master → domain DEKs
//
// The root is never written to the keystore. Persistent bytes are wrapped
// DEKs, versions, and crypto metadata.
type Envelope struct {
	mu        sync.Mutex
	path      string
	ident     format.Identity
	root      *DEK
	kek       *DEK
	master    *DEK
	rings     map[byte]*domainRing
	persist   keystore
	nonceHigh uint64
	shredded  bool
	listeners []func(RevokeEvent)
}

type domainRing struct {
	current format.KeyVersion
	keys    map[format.KeyVersion]*DEK
	flags   map[format.KeyVersion]byte
}

const (
	flagRevoked byte = 1 << 0
	flagRetired byte = 1 << 1
)

// KeystorePath is the sidecar next to a data file. It holds wrapped keys only.
func KeystorePath(dbPath string) string { return dbPath + ".keys" }

// CreateEnvelope generates a full domain set and persists wrapped keys.
func CreateEnvelope(path string, ident format.Identity, root *DEK) (*Envelope, error) {
	if root == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.CreateEnvelope", "nil root unlock key")
	}
	if _, err := os.Stat(path); err == nil {
		return nil, nerr.New(nerr.AlreadyExists, "crypto.CreateEnvelope", "keystore exists")
	}
	kek, err := GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	master, err := GenerateDEK(1)
	if err != nil {
		return nil, err
	}
	e := &Envelope{
		path:      path,
		ident:     ident,
		root:      root.clone(),
		kek:       kek,
		master:    master,
		rings:     make(map[byte]*domainRing, len(AllDomains)),
		nonceHigh: 1,
	}
	for _, d := range AllDomains {
		dek, err := GenerateDEK(1)
		if err != nil {
			return nil, err
		}
		e.rings[d] = &domainRing{
			current: 1,
			keys:    map[format.KeyVersion]*DEK{1: dek},
			flags:   map[format.KeyVersion]byte{1: 0},
		}
	}
	if err := e.rebuildPersistLocked(); err != nil {
		return nil, err
	}
	if err := e.writeLocked(); err != nil {
		return nil, err
	}
	return e, nil
}

// OpenEnvelope unwraps the keystore with the external root.
func OpenEnvelope(path string, root *DEK) (*Envelope, error) {
	if root == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.OpenEnvelope", "nil root unlock key")
	}
	ks, err := readKeystore(path)
	if err != nil {
		return nil, err
	}
	e := &Envelope{path: path, persist: ks, ident: ks.Identity, nonceHigh: ks.NonceHigh}
	if err := e.unlockLocked(root); err != nil {
		return nil, err
	}
	return e, nil
}

// OpenLocked loads keystore metadata without the root. Unlock must be called
// before any KeyProvider method succeeds.
func OpenLocked(path string) (*Envelope, error) {
	ks, err := readKeystore(path)
	if err != nil {
		return nil, err
	}
	return &Envelope{path: path, persist: ks, ident: ks.Identity, nonceHigh: ks.NonceHigh}, nil
}

func (e *Envelope) Path() string { return e.path }

func (e *Envelope) Identity() format.Identity {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.ident
}

func (e *Envelope) Unlocked() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.master != nil && !e.shredded
}

func (e *Envelope) NonceHigh() uint64 {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.nonceHigh
}

// NoteNonceHigh records a durable nonce high-water that must not move backwards
// across restore or snapshot rollback of the data file alone.
func (e *Envelope) NoteNonceHigh(n uint64) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if n <= e.nonceHigh {
		return nil
	}
	e.nonceHigh = n
	e.persist.NonceHigh = n
	return e.writeLocked()
}

func (e *Envelope) OnRevoke(fn func(RevokeEvent)) {
	if fn == nil {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	e.listeners = append(e.listeners, fn)
}

// Master returns the database master. WAL/UNDO wrap domain DEKs under this key.
func (e *Envelope) Master() (*DEK, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return nil, err
	}
	return e.master, nil
}

// Current implements KeyProvider for the page domain.
func (e *Envelope) Current() (*DEK, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.currentLocked(DomainPage)
}

// Key implements KeyProvider for the page domain.
func (e *Envelope) Key(version format.KeyVersion) (*DEK, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.keyLocked(DomainPage, version)
}

// Provider returns a KeyProvider bound to one domain.
func (e *Envelope) Provider(domain byte) KeyProvider {
	return &domainProvider{env: e, domain: domain}
}

type domainProvider struct {
	env    *Envelope
	domain byte
}

func (p *domainProvider) Current() (*DEK, error) {
	p.env.mu.Lock()
	defer p.env.mu.Unlock()
	return p.env.currentLocked(p.domain)
}

func (p *domainProvider) Key(version format.KeyVersion) (*DEK, error) {
	p.env.mu.Lock()
	defer p.env.mu.Unlock()
	return p.env.keyLocked(p.domain, version)
}

func (p *domainProvider) Master() (*DEK, error) {
	return p.env.Master()
}

func (e *Envelope) Unlock(root *DEK) error {
	if root == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.Unlock", "nil root unlock key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shredded {
		return nerr.New(nerr.Crypto, "crypto.Envelope.Unlock", "keystore has been shredded")
	}
	if e.master != nil {
		return e.verifyRootLocked(root)
	}
	return e.unlockLocked(root)
}

func (e *Envelope) VerifyRoot(root *DEK) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.verifyRootLocked(root)
}

func (e *Envelope) verifyRootLocked(root *DEK) error {
	if e.shredded {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRoot", "keystore has been shredded")
	}
	if len(e.persist.WrappedKEK) == 0 {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRoot", "keystore is locked and empty")
	}
	kek, err := UnwrapDEK(root, e.persist.WrappedKEK, DomainKEK)
	if err != nil {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRoot", "root does not unlock keystore")
	}
	if e.kek != nil && !e.kek.Equal(kek) {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRoot", "root does not match unlocked keystore")
	}
	return nil
}

func (e *Envelope) unlockLocked(root *DEK) error {
	if e.persist.Shredded {
		e.shredded = true
		return nerr.New(nerr.Crypto, "crypto.Envelope.Unlock", "keystore has been shredded")
	}
	kek, err := UnwrapDEK(root, e.persist.WrappedKEK, DomainKEK)
	if err != nil {
		return nerr.New(nerr.Crypto, "crypto.Envelope.Unlock", "root does not unlock keystore")
	}
	master, err := UnwrapDEK(kek, e.persist.WrappedMaster, DomainMaster)
	if err != nil {
		return err
	}
	rings := make(map[byte]*domainRing, len(e.persist.Domains))
	for _, d := range e.persist.Domains {
		r := &domainRing{
			current: d.Current,
			keys:    make(map[format.KeyVersion]*DEK, len(d.Keys)),
			flags:   make(map[format.KeyVersion]byte, len(d.Keys)),
		}
		for _, k := range d.Keys {
			r.flags[k.Version] = k.Flags
			if k.Flags&flagRevoked != 0 {
				continue
			}
			dek, err := UnwrapDEK(master, k.Wrap, d.Domain)
			if err != nil {
				return err
			}
			r.keys[k.Version] = dek
		}
		rings[d.Domain] = r
	}
	e.root = root.clone()
	e.kek = kek
	e.master = master
	e.rings = rings
	e.ident = e.persist.Identity
	e.nonceHigh = e.persist.NonceHigh
	return nil
}

func (e *Envelope) requireUnlockedLocked() error {
	if e.shredded {
		return nerr.New(nerr.Crypto, "crypto.Envelope", "keystore has been shredded")
	}
	if e.master == nil {
		return nerr.New(nerr.Unauthorized, "crypto.Envelope", "client key required")
	}
	return nil
}

func (e *Envelope) currentLocked(domain byte) (*DEK, error) {
	if err := e.requireUnlockedLocked(); err != nil {
		return nil, err
	}
	r, ok := e.rings[domain]
	if !ok {
		return nil, nerr.New(nerr.NotFound, "crypto.Envelope.Current", "unknown domain")
	}
	d, ok := r.keys[r.current]
	if !ok {
		return nil, nerr.New(nerr.Crypto, "crypto.Envelope.Current", "current key missing")
	}
	return d, nil
}

func (e *Envelope) keyLocked(domain byte, version format.KeyVersion) (*DEK, error) {
	if err := e.requireUnlockedLocked(); err != nil {
		return nil, err
	}
	r, ok := e.rings[domain]
	if !ok {
		return nil, nerr.New(nerr.Crypto, "crypto.Envelope.Key", "unknown domain")
	}
	if r.flags[version]&flagRevoked != 0 {
		return nil, nerr.New(nerr.Crypto, "crypto.Envelope.Key", "key version revoked")
	}
	d, ok := r.keys[version]
	if !ok {
		return nil, nerr.New(nerr.Crypto, "crypto.Envelope.Key", "unknown key version")
	}
	return d, nil
}

// RotateDomain generates a new current DEK. Old versions remain until Retire.
func (e *Envelope) RotateDomain(domain byte) (format.KeyVersion, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return 0, err
	}
	r, ok := e.rings[domain]
	if !ok {
		return 0, nerr.New(nerr.NotFound, "crypto.Envelope.RotateDomain", "unknown domain")
	}
	next := r.current + 1
	if next == 0 {
		return 0, nerr.New(nerr.Exhausted, "crypto.Envelope.RotateDomain", "key version overflow")
	}
	dek, err := GenerateDEK(next)
	if err != nil {
		return 0, err
	}
	r.keys[next] = dek
	r.flags[next] = 0
	r.current = next
	if err := e.rebuildPersistLocked(); err != nil {
		return 0, err
	}
	if err := e.writeLocked(); err != nil {
		return 0, err
	}
	return next, nil
}

// KeyStatus is a redacted snapshot of one key's rotation state — current
// version and how many versions are retained/revoked/retired. It never
// contains key material (there is nothing in this struct to redact; the
// bytes simply aren't here), same convention as security.TLSStatus.
type KeyStatus struct {
	Domain         string
	CurrentVersion format.KeyVersion
	// VersionCount is how many versions still have live key material in
	// this key's ring (len(ring.keys) — the current version plus any older
	// one not yet Revoked). Revoke deletes a version's DEK from the ring
	// immediately (dropping VersionCount right away) but leaves a flag
	// entry behind — counted in RevokedCount — until a later Retire drops
	// that too. Always 1 for "kek"/"master": RotateKEK/RotateMaster discard
	// the prior key immediately rather than retaining a ring of versions.
	VersionCount int
	// RevokedCount and RetiredCount both come from the ring's flags map
	// (not its keys map), so a version keeps counting in RevokedCount after
	// Revoke even though its key material and VersionCount slot are
	// already gone — until Retire removes the flag entry entirely.
	// RetiredCount is always 0 today: Retire's current implementation
	// deletes a version's flags entry outright rather than ever setting
	// flagRetired first, so there is no code path that produces a nonzero
	// value yet. Reported anyway (not omitted) so this table's shape
	// doesn't need to change if that ever does.
	RevokedCount int
	RetiredCount int
}

// KeyStatus returns a redacted snapshot of every key this envelope manages:
// the KEK and master first (single current version each — see VersionCount's
// doc comment), then each data domain in AllDomains order with its full
// retained/revoked/retired counts. Requires the envelope to be unlocked
// (same precondition every other Envelope method has).
func (e *Envelope) KeyStatus() ([]KeyStatus, error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return nil, err
	}
	out := make([]KeyStatus, 0, 2+len(AllDomains))
	out = append(out,
		KeyStatus{Domain: "kek", CurrentVersion: e.kek.Version, VersionCount: 1},
		KeyStatus{Domain: "master", CurrentVersion: e.master.Version, VersionCount: 1},
	)
	for _, d := range AllDomains {
		ring, ok := e.rings[d]
		if !ok {
			continue
		}
		st := KeyStatus{
			Domain:         DomainName(d),
			CurrentVersion: ring.current,
			VersionCount:   len(ring.keys),
		}
		for _, fl := range ring.flags {
			if fl&flagRevoked != 0 {
				st.RevokedCount++
			}
			if fl&flagRetired != 0 {
				st.RetiredCount++
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// RotateKEK generates a new KEK and re-wraps the master. Domain DEKs are unchanged.
func (e *Envelope) RotateKEK() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	next := e.kek.Version + 1
	kek, err := GenerateDEK(next)
	if err != nil {
		return err
	}
	e.kek.Zero()
	e.kek = kek
	return e.persistAndWriteLocked()
}

// RotateMaster generates a new master and re-wraps every domain DEK.
func (e *Envelope) RotateMaster() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	next := e.master.Version + 1
	master, err := GenerateDEK(next)
	if err != nil {
		return err
	}
	e.master.Zero()
	e.master = master
	return e.persistAndWriteLocked()
}

// RotateRoot re-wraps the KEK under a new external root. The caller must persist the new root off the data volume.
func (e *Envelope) RotateRoot(newRoot *DEK) error {
	if newRoot == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.RotateRoot", "nil root unlock key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	e.root.Zero()
	e.root = newRoot.clone()
	return e.persistAndWriteLocked()
}

// Revoke removes a version from the live keyring. Remaining ciphertext encrypted
// under it becomes unreadable without a restore of the old keystore.
func (e *Envelope) Revoke(domain byte, version format.KeyVersion) error {
	e.mu.Lock()
	listeners := e.listeners
	var ev RevokeEvent
	err := func() error {
		if err := e.requireUnlockedLocked(); err != nil {
			return err
		}
		r, ok := e.rings[domain]
		if !ok {
			return nerr.New(nerr.NotFound, "crypto.Envelope.Revoke", "unknown domain")
		}
		if version == r.current {
			return nerr.New(nerr.InvalidArgument, "crypto.Envelope.Revoke", "cannot revoke the current key version")
		}
		if _, ok := r.flags[version]; !ok {
			return nerr.New(nerr.NotFound, "crypto.Envelope.Revoke", "unknown key version")
		}
		r.flags[version] |= flagRevoked
		if d := r.keys[version]; d != nil {
			d.Zero()
		}
		delete(r.keys, version)
		if err := e.persistAndWriteLocked(); err != nil {
			return err
		}
		ev = RevokeEvent{Kind: "key-version", Domain: domain, Version: version}
		return nil
	}()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for _, fn := range listeners {
		fn(ev)
	}
	return nil
}

// Retire drops a retired version after callers have re-encrypted remaining units.
func (e *Envelope) Retire(domain byte, version format.KeyVersion) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	r, ok := e.rings[domain]
	if !ok {
		return nerr.New(nerr.NotFound, "crypto.Envelope.Retire", "unknown domain")
	}
	if version == r.current {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.Retire", "cannot retire the current key version")
	}
	if _, ok := r.flags[version]; !ok {
		return nerr.New(nerr.NotFound, "crypto.Envelope.Retire", "unknown key version")
	}
	if d := r.keys[version]; d != nil {
		d.Zero()
	}
	delete(r.keys, version)
	delete(r.flags, version)
	return e.persistAndWriteLocked()
}

// Shred destroys every remaining wrap. Data files stay ciphertext with no key.
func (e *Envelope) Shred(confirm string) error {
	if confirm != ShredPhrase {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.Shred", "crypto-shredding requires exact confirmation: NO KEY = NO RECOVERY")
	}
	e.mu.Lock()
	listeners := e.listeners
	err := func() error {
		if e.shredded {
			return nil
		}
		e.zeroLocked()
		e.shredded = true
		if err := writeShredded(e.path); err != nil {
			return err
		}
		return nil
	}()
	e.mu.Unlock()
	if err != nil {
		return err
	}
	for _, fn := range listeners {
		fn(RevokeEvent{Kind: "shred"})
	}
	return nil
}

// Lock wipes RAM keys. Wrapped blobs stay on disk.
func (e *Envelope) Lock() {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.zeroLocked()
}

func (e *Envelope) Close() error {
	e.Lock()
	return nil
}

func (e *Envelope) zeroLocked() {
	if e.root != nil {
		e.root.Zero()
		e.root = nil
	}
	if e.kek != nil {
		e.kek.Zero()
		e.kek = nil
	}
	if e.master != nil {
		e.master.Zero()
		e.master = nil
	}
	for _, r := range e.rings {
		for _, d := range r.keys {
			d.Zero()
		}
	}
	e.rings = nil
}

func (e *Envelope) persistAndWriteLocked() error {
	if err := e.rebuildPersistLocked(); err != nil {
		return err
	}
	return e.writeLocked()
}

func (e *Envelope) rebuildPersistLocked() error {
	if e.root == nil || e.kek == nil || e.master == nil {
		return nerr.New(nerr.Internal, "crypto.Envelope", "cannot persist a locked envelope")
	}
	wrappedKEK, err := WrapDEK(e.root, e.kek, DomainKEK)
	if err != nil {
		return err
	}
	wrappedMaster, err := WrapDEK(e.kek, e.master, DomainMaster)
	if err != nil {
		return err
	}
	ks := keystore{
		Identity:      e.ident,
		KEKVersion:    e.kek.Version,
		MasterVersion: e.master.Version,
		NonceHigh:     e.nonceHigh,
		WrappedKEK:    wrappedKEK,
		WrappedMaster: wrappedMaster,
	}
	for _, d := range AllDomains {
		r := e.rings[d]
		if r == nil {
			continue
		}
		pd := persistedDomain{Domain: d, Current: r.current}
		for ver, fl := range r.flags {
			pk := persistedKey{Version: ver, Flags: fl}
			if dek := r.keys[ver]; dek != nil {
				wrap, err := WrapDEK(e.master, dek, d)
				if err != nil {
					return err
				}
				pk.Wrap = wrap
			} else {
				// Keep the last persisted wrap for revoked versions so the
				// file format stays explicit; they are not unwrap-able live.
				for _, old := range e.persist.Domains {
					if old.Domain != d {
						continue
					}
					for _, ok := range old.Keys {
						if ok.Version == ver {
							pk.Wrap = append([]byte(nil), ok.Wrap...)
						}
					}
				}
			}
			pd.Keys = append(pd.Keys, pk)
		}
		ks.Domains = append(ks.Domains, pd)
	}
	e.persist = ks
	return nil
}

func (e *Envelope) writeLocked() error {
	raw, err := encodeKeystore(e.persist)
	if err != nil {
		return err
	}
	return writeAtomic(e.path, raw)
}

// EncodeUnlockMaterial is the on-wire client-key payload. Never log it.
func EncodeUnlockMaterial(d *DEK) ([]byte, error) {
	if d == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.EncodeUnlockMaterial", "nil DEK")
	}
	out := make([]byte, 4+AES256KeySize)
	encoding.PutU32(out, 0, uint32(d.Version))
	copy(out[4:], d.keyBytes())
	return out, nil
}

// ParseUnlockMaterial reconstructs a root from a client unlock frame.
func ParseUnlockMaterial(b []byte) (*DEK, error) {
	if len(b) != 4+AES256KeySize {
		return nil, nerr.New(nerr.InvalidFormat, "crypto.ParseUnlockMaterial", "invalid unlock payload")
	}
	ver := format.KeyVersion(encoding.U32(b, 0))
	return DEKFromBytes(ver, b[4:])
}
