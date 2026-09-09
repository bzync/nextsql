package crypto

import (
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/storage/format"
)

// Recovery keys.
//
// A keystore seals the KEK under the external root unlock key. If that key
// file is lost the data files stay ciphertext forever — correct, but it makes
// one file a single point of total, unrecoverable data loss.
//
// A recovery key is a second, independent AES-256 key that seals the *same*
// KEK. It is generated once, handed to the operator to store offline, and
// never retained by the server: only the sealed blob reaches the keystore.
// Either key unlocks the database; neither still does not. This is not escrow
// and not a password — losing both remains unrecoverable, which is exactly
// what ShredPhrase says out loud.
//
// Configuring one moves the keystore to format v2 (see keystore.go). That is
// a deliberate, operator-initiated one-way door with respect to releases that
// only understand v1, and RemoveRecoveryKey returns the file to v1.

// HasRecoveryKey reports whether a recovery key is configured. It does not
// require the envelope to be unlocked, so an operator can ask before they
// discover which key they still hold.
func (e *Envelope) HasRecoveryKey() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.recoveryWrap) != 0
}

// RecoveryKeyVersion returns the configured recovery key's version, or 0 when
// none is configured.
func (e *Envelope) RecoveryKeyVersion() format.KeyVersion {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.recoveryWrap) == 0 {
		return 0
	}
	return e.recoveryVersion
}

// Shredded reports whether the keystore has been crypto-shredded. Like
// HasRecoveryKey it answers without an unlock, because it is precisely the
// question an operator who cannot unlock needs answered.
func (e *Envelope) Shredded() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.shredded || e.persist.Shredded
}

// KeystoreFormatVersion reports the on-disk format this keystore will be
// written as: keystoreV1 without a recovery key, keystoreV2 with one.
func (e *Envelope) KeystoreFormatVersion() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.recoveryWrap) != 0 {
		return keystoreV2
	}
	return keystoreV1
}

// HasRoot reports whether a root unlock key is loaded. It is false after
// UnlockWithRecovery, where the envelope can read and write data but cannot
// rewrite its own key material until RotateRoot installs a root.
func (e *Envelope) HasRoot() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.root != nil
}

// NextRecoveryKeyVersion is the version a newly generated recovery key should
// carry. Versions increase across replacements so a keystore and an exported
// key file can be told apart from a superseded pair.
func (e *Envelope) NextRecoveryKeyVersion() format.KeyVersion {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.recoveryWrap) == 0 {
		return 1
	}
	return e.recoveryVersion + 1
}

// SetRecoveryKey seals the current KEK under rec and persists the result,
// replacing any previously configured recovery key. The envelope must be
// unlocked by its root: establishing a recovery path is something you do
// while you still hold the root, not instead of holding it.
//
// The caller owns rec and must persist it off the data volume before relying
// on it. This function keeps only the sealed blob.
func (e *Envelope) SetRecoveryKey(rec *DEK) error {
	if rec == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.SetRecoveryKey", "nil recovery key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	if e.root == nil {
		return nerr.New(nerr.Unauthorized, "crypto.Envelope.SetRecoveryKey",
			"a recovery key can only be set from a root-unlocked envelope")
	}
	if rec.Equal(e.root) {
		// Two names for one secret is not a second unlock path; it just
		// doubles the blast radius of losing that one file.
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.SetRecoveryKey",
			"recovery key must differ from the root unlock key")
	}
	wrap, err := WrapDEK(rec, e.kek, DomainKEK)
	if err != nil {
		return err
	}
	prevVersion, prevWrap := e.recoveryVersion, e.recoveryWrap
	e.recoveryVersion, e.recoveryWrap = rec.Version, wrap
	if err := e.persistAndWriteLocked(); err != nil {
		e.recoveryVersion, e.recoveryWrap = prevVersion, prevWrap
		return err
	}
	return nil
}

// RemoveRecoveryKey drops the recovery wrap and persists the result, which
// returns the keystore to format v1. The root unlock key becomes the only way
// in again. It is not an error to call this when none is configured.
func (e *Envelope) RemoveRecoveryKey() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	if len(e.recoveryWrap) == 0 {
		return nil
	}
	if e.root == nil {
		return nerr.New(nerr.Unauthorized, "crypto.Envelope.RemoveRecoveryKey",
			"a recovery-unlocked envelope cannot remove the recovery key that unlocked it; set a new root unlock key first")
	}
	prevVersion, prevWrap := e.recoveryVersion, e.recoveryWrap
	e.recoveryVersion, e.recoveryWrap = 0, nil
	if err := e.persistAndWriteLocked(); err != nil {
		e.recoveryVersion, e.recoveryWrap = prevVersion, prevWrap
		return err
	}
	return nil
}

// VerifyRecoveryKey reports whether rec actually opens the configured
// recovery wrap, without changing anything. An exported recovery key that was
// never verified is a backup nobody has restored.
func (e *Envelope) VerifyRecoveryKey(rec *DEK) error {
	if rec == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.VerifyRecoveryKey", "nil recovery key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shredded || e.persist.Shredded {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRecoveryKey", "keystore has been shredded")
	}
	if len(e.recoveryWrap) == 0 {
		return nerr.New(nerr.NotFound, "crypto.Envelope.VerifyRecoveryKey", "no recovery key is configured")
	}
	kek, err := UnwrapDEK(rec, e.recoveryWrap, DomainKEK)
	if err != nil {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRecoveryKey", "recovery key does not unlock keystore")
	}
	defer kek.Zero()
	if e.kek != nil && !e.kek.Equal(kek) {
		return nerr.New(nerr.Crypto, "crypto.Envelope.VerifyRecoveryKey", "recovery key does not match unlocked keystore")
	}
	return nil
}

// UnlockWithRecovery unlocks the envelope using the recovery key instead of
// the root. The envelope is left rootless: reads and writes work, but nothing
// that rewrites key material does until RotateRoot installs a new root. That
// is the intended recovery sequence — open with the recovery key, then adopt
// a freshly generated root unlock key.
func (e *Envelope) UnlockWithRecovery(rec *DEK) error {
	if rec == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.UnlockWithRecovery", "nil recovery key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.shredded {
		return nerr.New(nerr.Crypto, "crypto.Envelope.UnlockWithRecovery", "keystore has been shredded")
	}
	if e.persist.Shredded {
		e.shredded = true
		return nerr.New(nerr.Crypto, "crypto.Envelope.UnlockWithRecovery", "keystore has been shredded")
	}
	if len(e.persist.WrappedRecovery) == 0 {
		return nerr.New(nerr.NotFound, "crypto.Envelope.UnlockWithRecovery",
			"no recovery key is configured for this keystore")
	}
	kek, err := UnwrapDEK(rec, e.persist.WrappedRecovery, DomainKEK)
	if err != nil {
		return nerr.New(nerr.Crypto, "crypto.Envelope.UnlockWithRecovery", "recovery key does not unlock keystore")
	}
	return e.unlockWithKEKLocked(kek, nil)
}

// OpenEnvelopeWithRecovery opens a keystore using the recovery key. See
// UnlockWithRecovery for the rootless state this leaves behind.
func OpenEnvelopeWithRecovery(path string, rec *DEK) (*Envelope, error) {
	if rec == nil {
		return nil, nerr.New(nerr.InvalidArgument, "crypto.OpenEnvelopeWithRecovery", "nil recovery key")
	}
	e, err := OpenLocked(path)
	if err != nil {
		return nil, err
	}
	if err := e.UnlockWithRecovery(rec); err != nil {
		return nil, err
	}
	return e, nil
}

// RotateKEKWithRecovery rotates the KEK and re-seals it under rec in the same
// operation, so the recovery path never points at a superseded KEK. rec must
// be the currently configured recovery key; supplying a different one would
// quietly replace the operator's exported copy with one they do not hold.
func (e *Envelope) RotateKEKWithRecovery(rec *DEK) error {
	if rec == nil {
		return nerr.New(nerr.InvalidArgument, "crypto.Envelope.RotateKEKWithRecovery", "nil recovery key")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if err := e.requireUnlockedLocked(); err != nil {
		return err
	}
	if len(e.recoveryWrap) == 0 {
		return nerr.New(nerr.NotFound, "crypto.Envelope.RotateKEKWithRecovery", "no recovery key is configured")
	}
	kek, err := UnwrapDEK(rec, e.recoveryWrap, DomainKEK)
	if err != nil {
		return nerr.New(nerr.Crypto, "crypto.Envelope.RotateKEKWithRecovery", "recovery key does not unlock keystore")
	}
	matches := e.kek != nil && e.kek.Equal(kek)
	kek.Zero()
	if !matches {
		return nerr.New(nerr.Crypto, "crypto.Envelope.RotateKEKWithRecovery", "recovery key does not match unlocked keystore")
	}
	return e.rotateKEKLocked(rec)
}
