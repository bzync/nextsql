package main

import (
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
)

func tokenCmd(args []string) error {
	if len(args) == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql token", "expected keygen, rotate, retire, list-keys, export-public, mint, revoke, or verify")
	}
	switch args[0] {
	case "keygen":
		return tokenKeygen(args[1:])
	case "rotate":
		return tokenRotate(args[1:])
	case "retire":
		return tokenRetire(args[1:])
	case "list-keys":
		return tokenListKeys(args[1:])
	case "export-public":
		return tokenExportPublic(args[1:])
	case "mint":
		return tokenMint(args[1:])
	case "revoke":
		return tokenRevoke(args[1:])
	case "verify":
		return tokenVerify(args[1:])
	default:
		return nerr.New(nerr.InvalidArgument, "nextsql token", "unknown token command")
	}
}

type rolesFlag []string

func (r *rolesFlag) String() string { return strings.Join(*r, ",") }
func (r *rolesFlag) Set(v string) error {
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			*r = append(*r, p)
		}
	}
	return nil
}

func tokenKeygen(args []string) error {
	fs := flag.NewFlagSet("token keygen", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the issuer keyset file to create")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql token keygen", "--keyset is required")
	}
	ks, err := auth.CreateTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	for _, k := range ks.List() {
		fmt.Printf("created issuer keyset %s\ncurrent key id: %d\n", *keyset, k.ID)
	}
	return nil
}

func tokenRotate(args []string) error {
	fs := flag.NewFlagSet("token rotate", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the issuer keyset file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql token rotate", "--keyset is required")
	}
	ks, err := auth.OpenTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	id, err := ks.AddKey()
	if err != nil {
		return err
	}
	fmt.Printf("added key id %d and made it current; retire the previous key after the overlap window with `nextsql token retire`\n", id)
	return nil
}

func tokenRetire(args []string) error {
	fs := flag.NewFlagSet("token retire", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the issuer keyset file")
	id := fs.Uint("key-id", 0, "signing key id to retire")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" || *id == 0 {
		return nerr.New(nerr.InvalidArgument, "nextsql token retire", "--keyset and --key-id are required")
	}
	ks, err := auth.OpenTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	if err := ks.Retire(uint32(*id)); err != nil {
		return err
	}
	fmt.Printf("retired key id %d; credentials signed by it no longer verify\n", *id)
	return nil
}

func tokenListKeys(args []string) error {
	fs := flag.NewFlagSet("token list-keys", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to a keyset file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql token list-keys", "--keyset is required")
	}
	ks, err := auth.OpenTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	fmt.Printf("%-6s %-8s %-8s %-8s %s\n", "ID", "CURRENT", "RETIRED", "PRIVATE", "CREATED")
	for _, k := range ks.List() {
		fmt.Printf("%-6d %-8t %-8t %-8t %s\n", k.ID, k.Current, k.Retired, k.HasPrivate, k.Created.Format(time.RFC3339))
	}
	return nil
}

func tokenExportPublic(args []string) error {
	fs := flag.NewFlagSet("token export-public", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the issuer keyset file")
	out := fs.String("out", "", "path to write the verify-only keyset for servers")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" || *out == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql token export-public", "--keyset and --out are required")
	}
	ks, err := auth.OpenTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	if err := ks.WritePublic(*out); err != nil {
		return err
	}
	fmt.Printf("wrote verify-only keyset to %s (no private material); set token_verify_keyset to this path on each server\n", *out)
	return nil
}

func tokenMint(args []string) error {
	fs := flag.NewFlagSet("token mint", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to the issuer keyset file")
	principal := fs.String("principal", "", "native login user the credential authenticates as")
	ttl := fs.Duration("ttl", 15*time.Minute, "credential lifetime, e.g. 15m, 1h (max 720h)")
	audience := fs.String("audience", "", "deployment audience the credential is bound to")
	database := fs.String("database", "", "database scope (empty = any the principal may reach)")
	realm := fs.String("realm", "", "realm scope (empty = any)")
	var roles rolesFlag
	fs.Var(&roles, "role", "restrict to a role the principal holds (repeatable, or comma-separated)")
	notBefore := fs.String("not-before", "", "RFC3339 time the credential becomes valid (default now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *keyset == "" || *principal == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql token mint", "--keyset and --principal are required")
	}
	req := auth.TokenMintRequest{
		Principal: *principal,
		Audience:  *audience,
		Database:  *database,
		Realm:     *realm,
		Roles:     roles,
		TTL:       *ttl,
	}
	if *notBefore != "" {
		t, err := time.Parse(time.RFC3339, *notBefore)
		if err != nil {
			return nerr.New(nerr.InvalidArgument, "nextsql token mint", "--not-before must be RFC3339")
		}
		req.NotBefore = t
	}
	ks, err := auth.OpenTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	token, id, expiresAt, err := ks.Mint(req, time.Now())
	if err != nil {
		return err
	}
	fmt.Printf("token id: %s\nexpires:  %s\n\n%s\n", hex.EncodeToString(id[:]), expiresAt.Format(time.RFC3339), token)
	fmt.Fprintln(os.Stderr, "present this value in place of the password; it is not stored server-side")
	return nil
}

func tokenRevoke(args []string) error {
	fs := flag.NewFlagSet("token revoke", flag.ContinueOnError)
	path := fs.String("revocations", "", "path to the revocation file (created if absent)")
	tokenID := fs.String("token-id", "", "hex token id from `nextsql token mint` to revoke")
	principal := fs.String("principal", "", "revoke every credential for this principal issued at/before --before")
	before := fs.String("before", "", "RFC3339 cutoff for --principal (default now)")
	expires := fs.String("expires", "", "RFC3339 expiry of the revoked token id (default max lifetime from now)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return nerr.New(nerr.InvalidArgument, "nextsql token revoke", "--revocations is required")
	}
	if (*tokenID == "") == (*principal == "") {
		return nerr.New(nerr.InvalidArgument, "nextsql token revoke", "exactly one of --token-id or --principal is required")
	}
	rev, err := auth.OpenOrCreateTokenRevocations(*path)
	if err != nil {
		return err
	}
	if *tokenID != "" {
		raw, err := hex.DecodeString(strings.TrimSpace(*tokenID))
		if err != nil || len(raw) != 16 {
			return nerr.New(nerr.InvalidArgument, "nextsql token revoke", "--token-id must be 32 hex characters")
		}
		var id [16]byte
		copy(id[:], raw)
		exp := time.Time{}
		if *expires != "" {
			if exp, err = time.Parse(time.RFC3339, *expires); err != nil {
				return nerr.New(nerr.InvalidArgument, "nextsql token revoke", "--expires must be RFC3339")
			}
		}
		if err := rev.Revoke(id, exp); err != nil {
			return err
		}
		fmt.Printf("revoked token id %s\n", strings.ToLower(*tokenID))
		return nil
	}
	cutoff := time.Now()
	if *before != "" {
		var err error
		if cutoff, err = time.Parse(time.RFC3339, *before); err != nil {
			return nerr.New(nerr.InvalidArgument, "nextsql token revoke", "--before must be RFC3339")
		}
	}
	if err := rev.RevokePrincipal(*principal, cutoff); err != nil {
		return err
	}
	fmt.Printf("revoked every credential for %q issued at or before %s\n", strings.ToLower(strings.TrimSpace(*principal)), cutoff.UTC().Format(time.RFC3339))
	return nil
}

func tokenVerify(args []string) error {
	fs := flag.NewFlagSet("token verify", flag.ContinueOnError)
	keyset := fs.String("keyset", "", "path to a keyset file")
	revocations := fs.String("revocations", "", "optional revocation file")
	audience := fs.String("audience", "", "required audience (empty = any)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	rest := fs.Args()
	if *keyset == "" || len(rest) != 1 {
		return nerr.New(nerr.InvalidArgument, "nextsql token verify", "--keyset and a single token argument are required")
	}
	ks, err := auth.OpenTokenKeyset(*keyset)
	if err != nil {
		return err
	}
	var rev *auth.TokenRevocations
	if *revocations != "" {
		if rev, err = auth.OpenTokenRevocations(*revocations); err != nil {
			return err
		}
	}
	claims, err := auth.NewTokenVerifier(ks, rev, *audience).Verify(rest[0])
	if err != nil {
		return err
	}
	fmt.Printf("principal: %s\nkey id:    %d\nissued:    %s\nnot before:%s\nexpires:   %s\naudience:  %s\ndatabase:  %s\nrealm:     %s\nroles:     %s\n",
		claims.Principal, claims.KeyID,
		claims.IssuedAt.Format(time.RFC3339), claims.NotBefore.Format(time.RFC3339), claims.ExpiresAt.Format(time.RFC3339),
		orAny(claims.Audience), orAny(claims.Database), orAny(claims.Realm), rolesOrInherit(claims.Roles))
	return nil
}

func orAny(s string) string {
	if s == "" {
		return "(any)"
	}
	return s
}

func rolesOrInherit(roles []string) string {
	if len(roles) == 0 {
		return "(inherit all principal roles)"
	}
	return strings.Join(roles, ", ")
}
