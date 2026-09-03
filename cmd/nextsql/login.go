package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/cli"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidcclient"
)

// loginProfile resolves the named `[idp.<name>]` profile from the client config.
func loginProfile(s cli.Settings, clientSecretFile string) (oidcclient.IdPProfile, string, error) {
	if strings.TrimSpace(s.IdP) == "" {
		return oidcclient.IdPProfile{}, "", nerr.New(nerr.InvalidArgument, "nextsql login", "--idp is required")
	}
	cfgPath := strings.TrimSpace(s.IdPConfig)
	if cfgPath == "" {
		var err error
		if cfgPath, err = oidcclient.DefaultConfigPath(); err != nil {
			return oidcclient.IdPProfile{}, "", err
		}
	}
	cc, err := oidcclient.LoadClientConfig(cfgPath)
	if err != nil {
		return oidcclient.IdPProfile{}, "", err
	}
	prof, ok := cc.Profile(s.IdP)
	if !ok {
		return oidcclient.IdPProfile{}, "", nerr.New(nerr.NotFound, "nextsql login", "no [idp."+s.IdP+"] section in "+cfgPath)
	}
	if clientSecretFile = strings.TrimSpace(clientSecretFile); clientSecretFile != "" {
		prof.ClientSecretFile = clientSecretFile
	}
	ip, err := prof.Resolve()
	if err != nil {
		return oidcclient.IdPProfile{}, "", err
	}
	return ip, cfgPath, nil
}

func loginCmd(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	fs.String("addr", "", "server address the credential is for (host:port)")
	fs.String("idp", "", "identity provider profile name from the client config")
	fs.String("idp-config", "", "client identity-provider config file (default ~/.config/nextsql/config.toml)")
	fs.String("database", "", "restrict the minted credential to this database")
	fs.String("realm", "", "realm scope for hosted routing")
	clientCredentials := fs.Bool("client-credentials", false, "use the non-interactive OAuth2 client_credentials grant")
	clientSecretFile := fs.String("client-secret-file", "", "mode-0600 OAuth2 client secret file (overrides the profile)")
	noBrowser := fs.Bool("no-browser", false, "print the sign-in URL instead of opening a browser")
	timeout := fs.Duration("timeout", 3*time.Minute, "how long to wait for the browser sign-in")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	addr := strings.TrimSpace(s.Addr)
	if addr == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql login", "--addr (the server this credential is for) is required")
	}
	ip, _, err := loginProfile(s, *clientSecretFile)
	if err != nil {
		return err
	}

	var res oidcclient.BrokerResult
	var ts oidcclient.TokenSet
	if *clientCredentials {
		res, ts, err = oidcclient.ClientCredentials(context.Background(), oidcclient.ClientCredentialsOptions{
			Profile: ip, Database: strings.TrimSpace(s.Database), Realm: strings.TrimSpace(s.Realm),
		})
	} else {
		opts := oidcclient.LoginOptions{
			Profile:  ip,
			Progress: os.Stderr,
			Database: strings.TrimSpace(s.Database),
			Realm:    strings.TrimSpace(s.Realm),
			Timeout:  *timeout,
		}
		if !*noBrowser {
			opts.Browser = oidcclient.DefaultBrowserOpener
		}
		res, ts, err = oidcclient.Login(context.Background(), opts)
	}
	if err != nil {
		return err
	}
	store, err := oidcclient.DefaultStore()
	if err != nil {
		return err
	}
	cred := oidcclient.NewCredential(ip, addr, strings.TrimSpace(s.Database), strings.TrimSpace(s.Realm), res, ts, time.Now())
	if *clientCredentials {
		cred = oidcclient.NewClientCredential(ip, addr, strings.TrimSpace(s.Database), strings.TrimSpace(s.Realm), res, time.Now())
	}
	if err := store.Save(cred); err != nil {
		return err
	}
	fmt.Printf("signed in as %s\n", res.Principal)
	fmt.Printf("roles:      %s\n", rolesOrInherit(res.Roles))
	fmt.Printf("expires:    %s\n", res.ExpiresAt.Format(time.RFC3339))
	if res.TokenID != "" {
		fmt.Printf("token id:   %s\n", res.TokenID)
	}
	fmt.Printf("stored:     %s\n", store.Path(ip.Name, addr))
	if *clientCredentials {
		fmt.Fprintln(os.Stderr, "note: this workload credential renews non-interactively from the protected client secret file")
	} else if ts.RefreshToken == "" {
		fmt.Fprintln(os.Stderr, "note: the identity provider issued no refresh token; re-run `nextsql login` when the credential expires")
	}
	fmt.Fprintf(os.Stderr, "\nconnect with: nextsql exec --addr %s --idp %s -c '<sql>'\n", addr, ip.Name)
	return nil
}

func whoamiCmd(args []string) error {
	fs := flag.NewFlagSet("whoami", flag.ContinueOnError)
	fs.String("addr", "", "server address the credential is for (host:port)")
	fs.String("idp", "", "identity provider profile name from the client config")
	fs.String("idp-config", "", "client identity-provider config file (default ~/.config/nextsql/config.toml)")
	asJSON := fs.Bool("json", false, "print the credential summary as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	addr := strings.TrimSpace(s.Addr)
	if addr == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql whoami", "--addr is required")
	}
	ip, _, err := loginProfile(s, "")
	if err != nil {
		return err
	}
	store, err := oidcclient.DefaultStore()
	if err != nil {
		return err
	}
	cred, err := oidcclient.EnsureFresh(context.Background(), oidcclient.RenewOptions{
		Profile: ip, Store: store, Host: addr, HTTP: oidcclient.DefaultHTTP(),
	})
	if err != nil {
		return err
	}
	if *asJSON {
		out := map[string]any{
			"idp":        cred.IdP,
			"host":       cred.Host,
			"issuer":     cred.Issuer,
			"broker_url": cred.BrokerURL,
			"principal":  cred.Principal,
			"roles":      cred.Roles,
			"database":   cred.Database,
			"realm":      cred.Realm,
			"expires_at": cred.ExpiresAt.Format(time.RFC3339),
			"token_id":   cred.TokenID,
			"renewable":  cred.RefreshToken != "" || cred.GrantType == "client_credentials",
			"grant_type": cred.GrantType,
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(out)
	}
	fmt.Printf("principal:  %s\n", cred.Principal)
	fmt.Printf("roles:      %s\n", rolesOrInherit(cred.Roles))
	fmt.Printf("idp:        %s (%s)\n", cred.IdP, cred.Issuer)
	fmt.Printf("broker:     %s\n", cred.BrokerURL)
	fmt.Printf("host:       %s\n", cred.Host)
	if cred.Database != "" {
		fmt.Printf("database:   %s\n", cred.Database)
	}
	fmt.Printf("expires:    %s\n", cred.ExpiresAt.Format(time.RFC3339))
	if cred.TokenID != "" {
		fmt.Printf("token id:   %s\n", cred.TokenID)
	}
	fmt.Printf("renewable:  %t\n", cred.RefreshToken != "" || cred.GrantType == "client_credentials")
	return nil
}

func logoutCmd(args []string) error {
	fs := flag.NewFlagSet("logout", flag.ContinueOnError)
	fs.String("addr", "", "server address the credential is for (host:port)")
	fs.String("idp", "", "identity provider profile name")
	fs.String("idp-config", "", "client identity-provider config file")
	all := fs.Bool("all", false, "remove every stored external-identity credential")
	if err := fs.Parse(args); err != nil {
		return err
	}
	s, err := cli.Resolve(fs, args)
	if err != nil {
		return err
	}
	store, err := oidcclient.DefaultStore()
	if err != nil {
		return err
	}
	if *all {
		creds, err := store.List()
		if err != nil {
			return err
		}
		for _, c := range creds {
			if err := store.Delete(c.IdP, c.Host); err != nil {
				return err
			}
			fmt.Printf("removed %s @ %s\n", c.IdP, c.Host)
		}
		if len(creds) == 0 {
			fmt.Println("no stored credentials")
		}
		return nil
	}
	addr := strings.TrimSpace(s.Addr)
	if strings.TrimSpace(s.IdP) == "" || addr == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql logout", "--idp and --addr are required (or use --all)")
	}
	if err := store.Delete(s.IdP, addr); err != nil {
		return err
	}
	fmt.Printf("removed %s @ %s\n", s.IdP, addr)
	fmt.Fprintln(os.Stderr, "note: this only clears the local credential; to kill an active session immediately use `nextsql token revoke`")
	return nil
}
