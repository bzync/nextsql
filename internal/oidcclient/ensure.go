package oidcclient

import (
	"context"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// defaultSkew is how far before expiry a stored credential is treated as stale.
const defaultSkew = 60 * time.Second

// NewCredential builds a storable Credential from a completed login.
func NewCredential(p IdPProfile, host, database, realm string, res BrokerResult, ts TokenSet, now time.Time) *Credential {
	return &Credential{
		Version:      credentialVersion,
		IdP:          p.Name,
		Host:         host,
		Issuer:       p.Issuer,
		ClientID:     p.ClientID,
		BrokerURL:    p.BrokerURL,
		Scopes:       append([]string(nil), p.Scopes...),
		Principal:    res.Principal,
		Roles:        append([]string(nil), res.Roles...),
		Database:     database,
		Realm:        realm,
		Credential:   res.Credential,
		TokenID:      res.TokenID,
		ExpiresAt:    res.ExpiresAt,
		ObtainedAt:   now,
		RefreshToken: ts.RefreshToken,
	}
}

// RenewOptions configures EnsureFresh.
type RenewOptions struct {
	Profile IdPProfile
	Store   *Store
	Host    string
	HTTP    Doer
	Now     func() time.Time
	Skew    time.Duration
}

// EnsureFresh returns a currently-valid stored credential for
// (opts.Profile.Name, opts.Host), silently renewing it through the cached IdP
// refresh token when it has expired. It returns a Forbidden/Unauthorized error
// when no non-interactive renewal is possible; the caller should then prompt
// for `nextsql login`.
func EnsureFresh(ctx context.Context, opts RenewOptions) (*Credential, error) {
	const op = "oidcclient.EnsureFresh"
	now := time.Now
	if opts.Now != nil {
		now = opts.Now
	}
	skew := opts.Skew
	if skew <= 0 {
		skew = defaultSkew
	}
	cur, err := opts.Store.Load(opts.Profile.Name, opts.Host)
	if err != nil {
		return nil, err
	}
	if cur.Fresh(now(), skew) {
		return cur, nil
	}
	if cur.RefreshToken == "" {
		return nil, nerr.New(nerr.Unauthorized, op, "stored credential for idp "+opts.Profile.Name+" has expired and cannot be renewed without a browser; run: nextsql login --idp "+opts.Profile.Name)
	}
	hc := opts.HTTP
	if hc == nil {
		hc = DefaultHTTP()
	}
	md, err := Discover(ctx, hc, opts.Profile.Issuer)
	if err != nil {
		return nil, err
	}
	ts, err := RefreshTokens(ctx, hc, md, opts.Profile, cur.RefreshToken)
	if err != nil {
		return nil, err
	}
	res, err := ExchangeAtBroker(ctx, hc, opts.Profile.BrokerURL, opts.Profile.Name, ts.IDToken, "", cur.Database, cur.Realm)
	if err != nil {
		return nil, err
	}
	next := NewCredential(opts.Profile, opts.Host, cur.Database, cur.Realm, res, ts, now())
	if next.RefreshToken == "" {
		next.RefreshToken = cur.RefreshToken
	}
	if err := opts.Store.Save(next); err != nil {
		return nil, err
	}
	return next, nil
}
