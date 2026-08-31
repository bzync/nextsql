package cli

import (
	"context"
	"strings"

	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidcclient"
)

// applyOIDC resolves an external-identity (`--idp`) login into a concrete user
// and NSSC1. credential, renewing the stored credential silently when it has
// expired. The returned Settings has User / oidcCredential populated.
func applyOIDC(ctx context.Context, s Settings) (Settings, error) {
	const op = "cli.applyOIDC"
	addr := strings.TrimSpace(s.Addr)
	if addr == "" {
		return Settings{}, nerr.New(nerr.InvalidArgument, op, "address is required with --idp")
	}

	cfgPath := strings.TrimSpace(s.IdPConfig)
	if cfgPath == "" {
		var err error
		if cfgPath, err = oidcclient.DefaultConfigPath(); err != nil {
			return Settings{}, err
		}
	}
	cc, err := oidcclient.LoadClientConfig(cfgPath)
	if err != nil {
		return Settings{}, err
	}
	prof, ok := cc.Profile(s.IdP)
	if !ok {
		return Settings{}, nerr.New(nerr.NotFound, op, "no [idp."+s.IdP+"] section in "+cfgPath)
	}
	ip, err := prof.Resolve()
	if err != nil {
		return Settings{}, err
	}
	store, err := oidcclient.DefaultStore()
	if err != nil {
		return Settings{}, err
	}
	cred, err := oidcclient.EnsureFresh(ctx, oidcclient.RenewOptions{
		Profile: ip,
		Store:   store,
		Host:    addr,
		HTTP:    oidcclient.DefaultHTTP(),
	})
	if err != nil {
		return Settings{}, err
	}
	if u := strings.TrimSpace(s.User); u != "" && !strings.EqualFold(u, cred.Principal) {
		return Settings{}, nerr.New(nerr.InvalidArgument, op,
			"the stored --idp credential authenticates as "+cred.Principal+", not "+u)
	}
	s.User = cred.Principal
	s.oidcCredential = cred.Credential
	return s, nil
}
