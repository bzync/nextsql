// Package oidcclient is the client half of NextSQL's external-identity login.
//
// It runs an OpenID Connect Authorization Code + PKCE flow from the `nextsql`
// CLI against an operator-configured identity provider, then exchanges the
// resulting ID token at a NextSQL authentication broker (`internal/authbroker`)
// for an ordinary short-lived `NSSC1.` credential (`internal/auth`). The
// credential is stored `0600` on disk keyed by broker host + IdP profile and
// presented in place of a password on subsequent connections; when it expires
// and a cached IdP refresh token is still valid the flow is re-run silently.
//
// Nothing in this package verifies an ID token — that is the broker's job. The
// client only obtains the token, forwards it, and stores what comes back.
package oidcclient

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/nerr"
)

// maxHTTPBody bounds every response body this package will read.
const maxHTTPBody = 1 << 20 // 1 MiB

// Doer is the subset of *http.Client this package needs; tests inject a fake.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DefaultHTTP returns an HTTP client suitable for talking to an IdP and broker:
// a bounded timeout and no automatic credential-leaking redirects to other
// hosts.
func DefaultHTTP() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 5 {
				return nerr.New(nerr.Protocol, "oidcclient", "too many redirects")
			}
			return nil
		},
	}
}

// IdPProfile is a resolved external identity provider the CLI can log in to.
type IdPProfile struct {
	Name         string
	Issuer       string
	ClientID     string
	ClientSecret string // empty => public client (PKCE only)
	BrokerURL    string
	Scopes       []string
}

func (p IdPProfile) scopeParam() string {
	seen := map[string]bool{}
	out := []string{"openid"}
	seen["openid"] = true
	for _, s := range p.Scopes {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return strings.Join(out, " ")
}

// ProviderMetadata is the subset of the OIDC discovery document we use.
type ProviderMetadata struct {
	Issuer                string `json:"issuer"`
	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
}

// Discover fetches and validates `<issuer>/.well-known/openid-configuration`.
func Discover(ctx context.Context, hc Doer, issuer string) (ProviderMetadata, error) {
	const op = "oidcclient.Discover"
	if !strings.HasPrefix(issuer, "https://") {
		return ProviderMetadata{}, nerr.New(nerr.InvalidArgument, op, "issuer must be an https URL")
	}
	u := strings.TrimRight(issuer, "/") + "/.well-known/openid-configuration"
	body, err := httpGet(ctx, hc, u)
	if err != nil {
		return ProviderMetadata{}, err
	}
	var md ProviderMetadata
	if err := json.Unmarshal(body, &md); err != nil {
		return ProviderMetadata{}, nerr.Wrap(nerr.InvalidFormat, op, "decode discovery document", err)
	}
	if strings.TrimRight(md.Issuer, "/") != strings.TrimRight(issuer, "/") {
		return ProviderMetadata{}, nerr.New(nerr.InvalidFormat, op, "discovery issuer does not match configured issuer")
	}
	if !isHTTPS(md.AuthorizationEndpoint) || !isHTTPS(md.TokenEndpoint) {
		return ProviderMetadata{}, nerr.New(nerr.InvalidFormat, op, "discovery endpoints must be https URLs")
	}
	return md, nil
}

// PKCE is a generated code verifier / challenge pair (RFC 7636, S256).
type PKCE struct {
	Verifier  string
	Challenge string
}

// NewPKCE generates a fresh PKCE pair.
func NewPKCE() (PKCE, error) {
	v, err := randToken(32)
	if err != nil {
		return PKCE{}, err
	}
	sum := sha256.Sum256([]byte(v))
	return PKCE{Verifier: v, Challenge: base64.RawURLEncoding.EncodeToString(sum[:])}, nil
}

// randToken returns n random bytes as an unpadded base64url string.
func randToken(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", nerr.Wrap(nerr.Internal, "oidcclient", "read random", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// AuthCodeURL builds the IdP authorization-endpoint URL for the code flow.
func AuthCodeURL(md ProviderMetadata, p IdPProfile, redirectURI, state, nonce, challenge string) string {
	q := url.Values{}
	q.Set("response_type", "code")
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", p.scopeParam())
	q.Set("state", state)
	q.Set("nonce", nonce)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	sep := "?"
	if strings.Contains(md.AuthorizationEndpoint, "?") {
		sep = "&"
	}
	return md.AuthorizationEndpoint + sep + q.Encode()
}

// TokenSet is the useful part of an IdP token-endpoint response.
type TokenSet struct {
	IDToken      string
	AccessToken  string
	RefreshToken string
	ExpiresIn    int
}

type tokenEndpointResponse struct {
	IDToken          string `json:"id_token"`
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ExpiresIn        int    `json:"expires_in"`
	TokenType        string `json:"token_type"`
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
}

// RedeemCode exchanges an authorization code for tokens at the token endpoint.
func RedeemCode(ctx context.Context, hc Doer, md ProviderMetadata, p IdPProfile, code, verifier, redirectURI string) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", redirectURI)
	form.Set("client_id", p.ClientID)
	form.Set("code_verifier", verifier)
	return postTokenForm(ctx, hc, md.TokenEndpoint, p, form, "oidcclient.RedeemCode")
}

// RefreshTokens obtains a fresh token set from a refresh token. A response that
// omits a new refresh token keeps the caller's existing one.
func RefreshTokens(ctx context.Context, hc Doer, md ProviderMetadata, p IdPProfile, refreshToken string) (TokenSet, error) {
	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", refreshToken)
	form.Set("client_id", p.ClientID)
	form.Set("scope", p.scopeParam())
	ts, err := postTokenForm(ctx, hc, md.TokenEndpoint, p, form, "oidcclient.RefreshTokens")
	if err != nil {
		return TokenSet{}, err
	}
	if ts.RefreshToken == "" {
		ts.RefreshToken = refreshToken
	}
	return ts, nil
}

func postTokenForm(ctx context.Context, hc Doer, endpoint string, p IdPProfile, form url.Values, op string) (TokenSet, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return TokenSet{}, nerr.Wrap(nerr.Internal, op, "build request", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	if p.ClientSecret != "" {
		req.SetBasicAuth(url.QueryEscape(p.ClientID), url.QueryEscape(p.ClientSecret))
	}
	resp, err := hc.Do(req)
	if err != nil {
		return TokenSet{}, nerr.Wrap(nerr.Unavailable, op, "contact token endpoint", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody))
	if err != nil {
		return TokenSet{}, nerr.Wrap(nerr.IO, op, "read token response", err)
	}
	var ter tokenEndpointResponse
	_ = json.Unmarshal(body, &ter)
	if resp.StatusCode != http.StatusOK {
		msg := "token endpoint rejected the request"
		if ter.Error != "" {
			msg = "token endpoint error: " + ter.Error
		}
		return TokenSet{}, nerr.New(nerr.Unauthorized, op, msg)
	}
	if ter.IDToken == "" {
		return TokenSet{}, nerr.New(nerr.InvalidFormat, op, "token response carried no id_token")
	}
	return TokenSet{
		IDToken:      ter.IDToken,
		AccessToken:  ter.AccessToken,
		RefreshToken: ter.RefreshToken,
		ExpiresIn:    ter.ExpiresIn,
	}, nil
}

// BrokerResult is the authentication broker's /v1/exchange response.
type BrokerResult struct {
	Credential string
	Principal  string
	Roles      []string
	ExpiresAt  time.Time
	TokenID    string
}

type brokerExchangeRequest struct {
	IdP      string `json:"idp"`
	IDToken  string `json:"id_token"`
	Nonce    string `json:"nonce"`
	Database string `json:"database,omitempty"`
	Realm    string `json:"realm,omitempty"`
}

type brokerExchangeResponse struct {
	Credential string   `json:"credential"`
	Principal  string   `json:"principal"`
	Roles      []string `json:"roles"`
	ExpiresAt  string   `json:"expires_at"`
	TokenID    string   `json:"token_id"`
	Error      string   `json:"error"`
}

// ExchangeAtBroker POSTs the ID token to `<brokerURL>/v1/exchange` and returns
// the minted credential.
func ExchangeAtBroker(ctx context.Context, hc Doer, brokerURL, idpName, idToken, nonce, database, realm string) (BrokerResult, error) {
	const op = "oidcclient.ExchangeAtBroker"
	if !isHTTPS(brokerURL) && !isLoopbackHTTP(brokerURL) {
		return BrokerResult{}, nerr.New(nerr.InvalidArgument, op, "broker_url must be an https URL")
	}
	reqBody, _ := json.Marshal(brokerExchangeRequest{
		IdP: idpName, IDToken: idToken, Nonce: nonce, Database: database, Realm: realm,
	})
	u := strings.TrimRight(brokerURL, "/") + "/v1/exchange"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(string(reqBody)))
	if err != nil {
		return BrokerResult{}, nerr.Wrap(nerr.Internal, op, "build request", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return BrokerResult{}, nerr.Wrap(nerr.Unavailable, op, "contact authentication broker", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody))
	if err != nil {
		return BrokerResult{}, nerr.Wrap(nerr.IO, op, "read broker response", err)
	}
	var ber brokerExchangeResponse
	_ = json.Unmarshal(body, &ber)
	if resp.StatusCode != http.StatusOK {
		msg := "authentication broker denied the token exchange"
		if ber.Error != "" {
			msg = "authentication broker: " + ber.Error
		}
		code := nerr.Forbidden
		if resp.StatusCode >= 500 {
			code = nerr.Unavailable
		}
		return BrokerResult{}, nerr.New(code, op, msg)
	}
	if ber.Credential == "" || ber.Principal == "" {
		return BrokerResult{}, nerr.New(nerr.InvalidFormat, op, "broker response missing credential or principal")
	}
	exp, err := time.Parse(time.RFC3339, ber.ExpiresAt)
	if err != nil {
		return BrokerResult{}, nerr.Wrap(nerr.InvalidFormat, op, "parse credential expiry", err)
	}
	return BrokerResult{
		Credential: ber.Credential,
		Principal:  ber.Principal,
		Roles:      ber.Roles,
		ExpiresAt:  exp,
		TokenID:    ber.TokenID,
	}, nil
}

func httpGet(ctx context.Context, hc Doer, u string) ([]byte, error) {
	const op = "oidcclient.httpGet"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nerr.Wrap(nerr.Internal, op, "build request", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nerr.Wrap(nerr.Unavailable, op, "fetch "+u, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBody))
	if err != nil {
		return nil, nerr.Wrap(nerr.IO, op, "read "+u, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, nerr.New(nerr.Unavailable, op, "unexpected status fetching "+u)
	}
	return body, nil
}

func isHTTPS(u string) bool { return strings.HasPrefix(u, "https://") }

func isLoopbackHTTP(u string) bool {
	p, err := url.Parse(u)
	if err != nil || p.Scheme != "http" {
		return false
	}
	host := p.Hostname()
	return host == "127.0.0.1" || host == "localhost" || host == "::1"
}
