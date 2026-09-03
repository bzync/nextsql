package oidcclient

import (
	"context"

	"github.com/bzync/nextsql/internal/nerr"
)

// ClientCredentialsOptions configures a non-interactive OAuth2 workload
// login. The resolved profile must carry a secret read from a protected file.
type ClientCredentialsOptions struct {
	Profile  IdPProfile
	HTTP     Doer
	Database string
	Realm    string
}

// ClientCredentials performs discovery, obtains a JWT access token with the
// client_credentials grant, and exchanges it at the authentication broker for
// an ordinary NSSC1 credential.
func ClientCredentials(ctx context.Context, opts ClientCredentialsOptions) (BrokerResult, TokenSet, error) {
	const op = "oidcclient.ClientCredentials"
	if opts.Profile.BrokerURL == "" {
		return BrokerResult{}, TokenSet{}, nerr.New(nerr.InvalidArgument, op, "identity provider profile has no broker_url")
	}
	hc := opts.HTTP
	if hc == nil {
		hc = DefaultHTTP()
	}
	md, err := Discover(ctx, hc, opts.Profile.Issuer)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	ts, err := ObtainClientCredentials(ctx, hc, md, opts.Profile)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	res, err := ExchangeAccessTokenAtBroker(ctx, hc, opts.Profile.BrokerURL, opts.Profile.Name, ts.AccessToken, opts.Database, opts.Realm)
	if err != nil {
		return BrokerResult{}, TokenSet{}, err
	}
	return res, ts, nil
}
