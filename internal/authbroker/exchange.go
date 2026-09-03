package authbroker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/bzync/nextsql/internal/auth"
	"github.com/bzync/nextsql/internal/nerr"
	"github.com/bzync/nextsql/internal/oidc"
)

// maxExchangeBody bounds the request body the broker will read.
const maxExchangeBody = 1 << 16 // 64 KiB

// exchangeRequest is the JSON body of POST /v1/exchange.
type exchangeRequest struct {
	IdP         string `json:"idp"`
	IDToken     string `json:"id_token"`
	AccessToken string `json:"access_token"`
	Nonce       string `json:"nonce"`
	Database    string `json:"database"`
	Realm       string `json:"realm"`
}

// exchangeResponse is the JSON body returned on success.
type exchangeResponse struct {
	Credential string   `json:"credential"`
	Principal  string   `json:"principal"`
	Roles      []string `json:"roles"`
	ExpiresAt  string   `json:"expires_at"`
	TokenID    string   `json:"token_id"`
}

func (b *Broker) handleExchange(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, maxExchangeBody+1))
	if err != nil || int64(len(body)) > maxExchangeBody {
		writeErr(w, http.StatusRequestEntityTooLarge, "request body too large")
		return
	}
	var req exchangeRequest
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "malformed request body")
		return
	}
	var trailing any
	if err := dec.Decode(&trailing); err != io.EOF {
		writeErr(w, http.StatusBadRequest, "malformed request body")
		return
	}

	resp, aud, herr := b.exchange(r.Context(), req)
	b.logAudit(aud)
	if herr != nil {
		writeErr(w, herr.status, herr.public)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// httpError carries a status and a deliberately generic public message; the
// specific reason goes only to the audit log (design I5 / §8).
type httpError struct {
	status int
	public string
}

func (e *httpError) Error() string { return e.public }

func denyHTTP(status int, public string) *httpError {
	return &httpError{status: status, public: public}
}

// exchange runs the full token exchange and returns the response, an audit
// record (always), and an error suitable for the HTTP layer.
func (b *Broker) exchange(ctx context.Context, req exchangeRequest) (*exchangeResponse, exchangeAudit, *httpError) {
	aud := exchangeAudit{Time: b.now().UTC(), IdP: req.IdP}

	policy, keyset, verifiers := b.snapshot()

	pv, ok := verifiers[req.IdP]
	if !ok || req.IdP == "" {
		aud.Outcome, aud.Reason = "denied", "unknown idp profile"
		return nil, aud, denyHTTP(http.StatusBadRequest, "unknown identity provider")
	}
	aud.Issuer = pv.profile.Issuer
	hasID := strings.TrimSpace(req.IDToken) != ""
	hasAccess := strings.TrimSpace(req.AccessToken) != ""
	if hasID == hasAccess {
		aud.Outcome, aud.Reason = "denied", "request did not carry exactly one token type"
		return nil, aud, denyHTTP(http.StatusBadRequest, "exactly one of id_token or access_token is required")
	}

	var tok *oidc.VerifiedToken
	var err error
	if hasID {
		tok, err = pv.idtoken.Verify(ctx, req.IDToken, req.Nonce)
	} else if strings.TrimSpace(req.Nonce) != "" {
		aud.Outcome, aud.Reason = "denied", "nonce supplied with access token"
		return nil, aud, denyHTTP(http.StatusBadRequest, "nonce is not valid with access_token")
	} else if pv.access == nil {
		aud.Outcome, aud.Reason = "denied", "jwt access-token exchange is disabled for this profile"
		return nil, aud, denyHTTP(http.StatusForbidden, "token exchange denied")
	} else {
		tok, err = pv.access.Verify(ctx, req.AccessToken)
	}
	if err != nil {
		aud.Outcome, aud.Reason = "denied", "identity-provider token verification failed: "+errText(err)
		return nil, aud, denyHTTP(verifyStatus(err), "token exchange denied")
	}
	aud.SubjectHash = hashSubject(tok.Subject)

	if err := b.replay.Observe(tok); err != nil {
		aud.Outcome, aud.Reason = "denied", "replayed token"
		return nil, aud, denyHTTP(http.StatusForbidden, "token exchange denied")
	}

	mapped, err := policy.Map(tok.Issuer, tok.Claims)
	if err != nil {
		aud.Outcome, aud.Reason = "denied", "identity policy did not map this subject: "+errText(err)
		return nil, aud, denyHTTP(http.StatusForbidden, "token exchange denied")
	}
	aud.RuleID = mapped.RuleID
	aud.Principal = mapped.Principal
	aud.MappedRoles = mapped.Roles

	effectiveRoles := mapped.Roles
	if held, wired, herr := b.heldRoles(req.Realm, mapped.Principal); wired {
		if herr != nil {
			aud.Outcome, aud.Reason = "error", "rbac membership lookup failed"
			return nil, aud, denyHTTP(http.StatusServiceUnavailable, "token exchange temporarily unavailable")
		}
		effectiveRoles = auth.IntersectRoles(mapped.Roles, held)
		if len(effectiveRoles) == 0 {
			aud.Outcome, aud.Reason = "denied", "principal holds none of the policy-mapped roles"
			return nil, aud, denyHTTP(http.StatusForbidden, "token exchange denied")
		}
	}
	aud.EffectiveRoles = effectiveRoles

	ttl := b.mintTTL(tok.Expiry)
	if ttl <= 0 {
		aud.Outcome, aud.Reason = "denied", "identity-provider token expires too soon to mint a credential"
		return nil, aud, denyHTTP(http.StatusForbidden, "token exchange denied")
	}

	credential, tokenID, expiresAt, err := keyset.Mint(auth.TokenMintRequest{
		Principal: mapped.Principal,
		Audience:  b.cfg.DeploymentAudience,
		Database:  strings.TrimSpace(req.Database),
		Realm:     strings.TrimSpace(req.Realm),
		Roles:     effectiveRoles,
		TTL:       ttl,
	}, b.now())
	if err != nil {
		aud.Outcome, aud.Reason = "error", "credential minting failed"
		return nil, aud, denyHTTP(http.StatusInternalServerError, "token exchange failed")
	}
	aud.TokenID = hex.EncodeToString(tokenID[:])
	aud.ExpiresAt = expiresAt.UTC()
	aud.Outcome = "granted"

	return &exchangeResponse{
		Credential: credential,
		Principal:  mapped.Principal,
		Roles:      effectiveRoles,
		ExpiresAt:  expiresAt.UTC().Format(time.RFC3339),
		TokenID:    aud.TokenID,
	}, aud, nil
}

// mintTTL is min(configured TTL, time until the IdP token expires).
func (b *Broker) mintTTL(idpExpiry time.Time) time.Duration {
	ttl := b.cfg.CredentialTTL
	if until := idpExpiry.Sub(b.now()); until < ttl {
		ttl = until
	}
	return ttl
}

func hashSubject(sub string) string {
	sum := sha256.Sum256([]byte(sub))
	return hex.EncodeToString(sum[:8])
}

func verifyStatus(err error) int {
	var ne *nerr.Error
	if errors.As(err, &ne) {
		switch ne.Code {
		case nerr.Unavailable:
			return http.StatusServiceUnavailable
		case nerr.InvalidFormat, nerr.InvalidArgument:
			return http.StatusBadRequest
		}
	}
	return http.StatusForbidden
}

func errText(err error) string {
	var ne *nerr.Error
	if errors.As(err, &ne) {
		return ne.Message
	}
	return "error"
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
