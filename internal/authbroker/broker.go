package authbroker

import (
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidc"
)

// RoleMembershipFunc reports the native roles a principal actually holds in
// NextSQL RBAC, within the named realm (realm is the raw exchange-request
// realm name, "" for a non-hosted/deployment-wide deployment — resolving it
// to a hosting.ID, when applicable, is the caller's job, keeping this
// package decoupled from internal/hosting). The broker intersects the
// policy-mapped roles with this set so an external identity can only ever
// narrow a real grant (design invariant I1). It is optional: when nil the
// broker mints the policy-mapped roles as-is and relies on the server's
// `ACL.AllowedScopedInRealm` to drop any role the principal does not hold.
type RoleMembershipFunc func(realm, principal string) ([]string, error)

// Options configures a Broker beyond what the config file carries.
type Options struct {
	// Fetcher retrieves JWKS / discovery documents. Defaults to an HTTPS
	// fetcher; tests inject a fake.
	Fetcher oidc.Fetcher
	// RoleMembership, when set, enables the no-escalation RBAC intersection.
	RoleMembership RoleMembershipFunc
	// Logger receives structured audit and operational records. Required.
	Logger *slog.Logger
	// Now overrides the clock (tests only).
	Now func() time.Time
}

// Broker is the running authentication broker. Its zero value is not usable;
// build it with New.
type Broker struct {
	cfg     Config
	fetcher oidc.Fetcher
	members RoleMembershipFunc
	log     *slog.Logger
	now     func() time.Time

	mu        sync.RWMutex
	policy    *auth.IdentityPolicy
	keyset    *auth.TokenKeyset
	verifiers map[string]*profileVerifier // by profile name
	replay    *oidc.ReplayGuard
}

type profileVerifier struct {
	profile IdPProfile
	idtoken *oidc.IDTokenVerifier
	access  *oidc.AccessTokenVerifier
}

type brokerSnapshot struct {
	policy    *auth.IdentityPolicy
	keyset    *auth.TokenKeyset
	verifiers map[string]*profileVerifier
}

// New builds a Broker: it loads the identity policy and the issuing keyset,
// and constructs a JWKS cache and ID-token verifier for every configured
// profile. It does not start a listener.
func New(cfg Config, opts Options) (*Broker, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if opts.Logger == nil {
		return nil, nerr.New(nerr.InvalidArgument, "authbroker.New", "a logger is required")
	}
	fetcher := opts.Fetcher
	if fetcher == nil {
		fetcher = oidc.NewHTTPFetcher()
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	b := &Broker{
		cfg:     cfg,
		fetcher: fetcher,
		members: opts.RoleMembership,
		log:     opts.Logger,
		now:     now,
		replay:  oidc.NewReplayGuard(now),
	}
	if err := b.load(nil); err != nil {
		return nil, err
	}
	if b.members == nil {
		b.log.Warn("authbroker: RBAC membership feed is not wired; minted role sets are not intersected with real membership (the server still enforces ACL.AllowedScoped)")
	}
	return b, nil
}

func (b *Broker) load(validateKeyset func(*auth.TokenKeyset) error) error {
	snapshot, err := b.prepare(validateKeyset)
	if err != nil {
		return err
	}
	b.mu.Lock()
	b.policy, b.keyset, b.verifiers = snapshot.policy, snapshot.keyset, snapshot.verifiers
	b.mu.Unlock()
	return nil
}

func (b *Broker) prepare(validateKeyset func(*auth.TokenKeyset) error) (*brokerSnapshot, error) {
	policy, err := auth.OpenIdentityPolicy(b.cfg.IdentityPolicy)
	if err != nil {
		return nil, err
	}
	keyset, err := auth.OpenTokenKeyset(b.cfg.IssuingKeyset)
	if err != nil {
		return nil, err
	}
	if validateKeyset != nil {
		if err := validateKeyset(keyset); err != nil {
			return nil, err
		}
	}
	verifiers := make(map[string]*profileVerifier, len(b.cfg.Profiles))
	for _, p := range b.cfg.Profiles {
		jc, err := oidc.NewJWKSCache(oidc.JWKSCacheConfig{
			Fetcher: b.fetcher,
			Issuer:  p.Issuer,
			JWKSURI: p.JWKSURI,
			SoftTTL: p.JWKSSoftTTL,
			HardTTL: p.JWKSHardTTL,
			Now:     b.now,
		})
		if err != nil {
			return nil, err
		}
		iv, err := oidc.NewIDTokenVerifier(oidc.IDTokenConfig{
			Issuer:      p.Issuer,
			ClientID:    p.ClientID,
			AllowedAlgs: p.AllowedAlgs,
			Skew:        p.Skew,
			Now:         b.now,
		}, jc)
		if err != nil {
			return nil, err
		}
		var av *oidc.AccessTokenVerifier
		if p.AccessTokenAudience != "" {
			av, err = oidc.NewAccessTokenVerifier(oidc.AccessTokenConfig{
				Issuer: p.Issuer, ClientID: p.ClientID, Audience: p.AccessTokenAudience,
				AllowedAlgs: p.AllowedAlgs, Skew: p.Skew, Now: b.now,
			}, jc)
			if err != nil {
				return nil, err
			}
		}
		verifiers[p.Name] = &profileVerifier{profile: p, idtoken: iv, access: av}
	}
	return &brokerSnapshot{policy: policy, keyset: keyset, verifiers: verifiers}, nil
}

// Reload re-reads the identity policy and the issuing keyset. On any failure
// the running broker keeps its last known-good state (matching NSTK/NSTR/NSIP
// SIGHUP semantics). Profile verifiers and JWKS caches are rebuilt so a policy
// change takes effect without dropping in-flight requests.
func (b *Broker) Reload() error {
	return b.reload(nil)
}

// ReloadWithKeysetValidator is Reload with an additional pre-publication gate
// for the candidate issuing keyset. Embedded nextsqld uses it to prove that a
// newly current issuer key is already accepted by the co-located verifier.
func (b *Broker) ReloadWithKeysetValidator(validate func(*auth.TokenKeyset) error) error {
	if validate == nil {
		return nerr.New(nerr.InvalidArgument, "authbroker.ReloadWithKeysetValidator", "validator is required")
	}
	return b.reload(validate)
}

// ValidateReloadWithKeysetValidator validates the complete candidate policy,
// issuer keyset, and verifier set without publishing it. Embedded nextsqld
// uses this preflight before changing its co-located token verifier.
func (b *Broker) ValidateReloadWithKeysetValidator(validate func(*auth.TokenKeyset) error) error {
	if validate == nil {
		return nerr.New(nerr.InvalidArgument, "authbroker.ValidateReloadWithKeysetValidator", "validator is required")
	}
	_, err := b.prepare(validate)
	return err
}

func (b *Broker) reload(validate func(*auth.TokenKeyset) error) error {
	if err := b.load(validate); err != nil {
		b.log.Warn("authbroker: reload failed; keeping last known-good policy and keyset", "error", err.Error())
		return err
	}
	b.log.Info("authbroker: reloaded identity policy and issuing keyset")
	return nil
}

// Handler returns the broker's HTTP handler. The only route is
// `POST /v1/exchange`.
func (b *Broker) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/exchange", b.handleExchange)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	return mux
}

func (b *Broker) snapshot() (*auth.IdentityPolicy, *auth.TokenKeyset, map[string]*profileVerifier) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.policy, b.keyset, b.verifiers
}

// heldRoles returns the principal's RBAC roles within realm, or nil when no
// feed is wired.
func (b *Broker) heldRoles(realm, principal string) ([]string, bool, error) {
	if b.members == nil {
		return nil, false, nil
	}
	roles, err := b.members(strings.TrimSpace(realm), strings.ToLower(strings.TrimSpace(principal)))
	if err != nil {
		return nil, true, err
	}
	return roles, true, nil
}
