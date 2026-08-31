# Proposed External Identity Provider (OIDC) Integration

> Status: **ACCEPTED DESIGN — PARTIALLY IMPLEMENTED (`NSIP` policy engine +
> authentication broker); NOT PRODUCTION-GATED**
>
> This document is a design and delivery plan for Phase 25's "External IdP"
> checklist group. `TODO.md` remains authoritative for implementation status and
> sequencing. As of 2026-08-31 the offline `NSIP` identity-policy engine
> (§6, `internal/auth/identitypolicy.go`) and the authentication broker
> (§5, `cmd/nextsql-auth-broker`, `internal/oidc`, `internal/authbroker`) are
> implemented and tested: the broker's `POST /v1/exchange` validates an OIDC ID
> token against a cached JWKS, maps it through the `NSIP` policy, and mints an
> `NSSC1.` credential. There is no `nextsql login` client flow yet, no `oidc`
> audit `identity_source`, and no client-credentials / embedded / JIT support;
> the server does not accept an OIDC identity in any form (it only ever sees the
> minted `NSSC1.` credential).

## 1. Purpose

Let an operator delegate *who a person is* to an external OpenID Connect
provider (Okta, Entra ID, Google Workspace, Keycloak, Auth0, …) while NextSQL
keeps full, unbypassable ownership of *what that person may do*.

Concretely, a human should be able to run:

```text
nextsql login --idp corp
# ... browser SSO against the corporate IdP ...
nextsql sql --host db.internal 'SELECT ...'
```

and land in a NextSQL session as a native principal with a role set derived
from their IdP group membership, an explicit short expiry, and a full audit
trail — without a NextSQL password and without any new privilege the operator
did not grant in NextSQL RBAC.

Machine identities (CI, services, cron) are already served by mTLS service
identities and `nextsql token mint`. OIDC targets **interactive human login**
and, secondarily, the OAuth2 **client-credentials** grant for workloads that
already have an IdP client but no NextSQL credential.

## 2. Current baseline this design reuses

Phase 25 already shipped the hard part. A short-lived credential
(`internal/auth`) is:

- an Ed25519-signed `NSSC1.` claims blob presented **in place of the password**
  on the existing `Auth` frame — no new wire message, no driver change;
- scoped by `principal`, optional `audience` / `database` / `realm`, and a
  no-escalation `roles` list applied through `security.ACL.AllowedScoped`;
- bounded by issued-at / not-before / expires-at with a verifier maximum
  lifetime (default 24 h, ceiling 30 d) and 60 s skew;
- revocable by token id or per-principal "issued at or before" cutoff (`NSTR`),
  reloadable on `SIGHUP` with last-known-good rollback;
- audited via `identity_source` (`token`, `mtls+token`);
- terminated server-side when the credential expires.

**The core design decision below is to make OIDC a _new way to obtain that
existing credential_, not a new thing the SQL server has to understand.**

## 3. Goals and non-goals

### Goals

- OIDC Authorization Code + PKCE for interactive login from the `nextsql` CLI
  and, later, NextSQL Studio.
- OIDC/OAuth2 client-credentials grant for confidential workloads.
- Deterministic, auditable IdP-claim → native-principal mapping.
- Deterministic IdP-group → native-role mapping that can only *narrow*, never
  *expand*, what the mapped principal already holds in NextSQL RBAC.
- No outbound network dependency on the IdP in the SQL authentication hot path.
- Fail-closed on every ambiguous or unverifiable input.
- Truthful phase accounting: this lands as `designed` now, `implemented` and
  `tested` only when code and tests exist.

### Non-goals

- SAML, LDAP/Active Directory bind, Kerberos/GSSAPI. OIDC only for P25. The
  mapping layer is designed to be protocol-agnostic so SAML could reuse it
  later, but no SAML code is planned here.
- SCIM / directory provisioning. NextSQL users and roles are still created with
  `CREATE USER` / `CREATE ROLE` / `GRANT`. OIDC does not auto-create principals
  (see §6.3 for the optional, off-by-default "just-in-time" variant and why it
  is gated).
- IdP-driven *authorization*. Group claims influence role selection within
  limits the operator pre-granted; they never carry privileges themselves.
- Replacing native passwords or mTLS. OIDC is additive.
- Being an OAuth2 *authorization server* for third parties. NextSQL is only a
  relying party / token consumer.

## 4. Threat model and invariants

Adversaries considered:

1. **A valid IdP user with no NextSQL grant.** Must reach *no* session. Mapping
   to a principal that does not exist, or to one holding no `CONNECT`, fails
   closed.
2. **A valid IdP user attempting privilege escalation via forged/extra group
   claims.** Group→role mapping is intersected with the principal's existing
   RBAC membership; unknown or unmapped groups are dropped, never honored.
3. **A stolen IdP access/ID token.** Bounded by the IdP token lifetime *and* by
   the short NextSQL credential lifetime it is exchanged for; revocable in
   NextSQL independently of the IdP via `NSTR`.
4. **A compromised or unavailable IdP.** Cannot mint NextSQL privileges beyond
   what the mapping policy + RBAC already allow. IdP unavailability blocks *new*
   logins only; existing sessions and non-OIDC auth are unaffected. The SQL
   commit path never calls the IdP.
5. **A malicious or misconfigured broker.** The broker holds an issuing key
   (§5) and is therefore trusted to the same degree as `nextsql token mint`
   today. It is deployed and locked down as a control-plane component, not
   exposed to tenants. Its issued credentials are still bounded by the server's
   `TokenVerifier` (max lifetime, audience, revocation) and by RBAC.

Invariants (all enforced server-side, independent of the broker):

- **I1.** Every OIDC session is a native principal that exists and independently
  holds the privileges exercised. Deleting the NextSQL user or revoking its
  roles takes effect regardless of IdP state.
- **I2.** `ACL.AllowedScoped` is evaluated on every statement for OIDC sessions
  exactly as for `NSSC1.` token sessions. There is no OIDC bypass path.
- **I3.** An OIDC session's lifetime ≤ the minted credential's `expires-at` ≤
  the verifier maximum. Session closes at expiry.
- **I4.** OIDC logins are distinguishable in the audit log
  (`identity_source` `oidc`, or `mtls+oidc`).
- **I5.** Any verification failure — bad signature, wrong issuer, wrong
  audience, expired, unmapped subject, unknown principal, group-mapping
  conflict, JWKS unavailable — denies the login and audits a failure. No
  partial or "best effort" admission.

## 5. Architecture: brokered token exchange (recommended)

```text
                         (1) Authorization Code + PKCE  /  client_credentials
 ┌──────────┐   OIDC    ┌─────────────────┐
 │  human   │◄─────────►│  external IdP    │
 │ + nextsql│           │  (Okta/Entra/…)  │
 │   CLI    │           └─────────────────┘
 └────┬─────┘                     ▲
      │ (2) POST /v1/exchange     │ (3) validate ID token:
      │     { id_token }          │     iss, aud, exp, nonce,
      ▼                           │     signature vs cached JWKS
 ┌─────────────────────────────┐  │
 │  nextsql-auth-broker        │──┘
 │  - JWKS cache + refresh     │
 │  - mapping policy (NSIP)    │  (4) mint NSSC1. credential:
 │  - holds NSTK issuing key   │      principal + roles + short exp
 └────────────┬────────────────┘
              │ (5) returns NSSC1.<base64url>
              ▼
 ┌──────────┐   Auth frame: password = "NSSC1...."   ┌──────────────┐
 │  nextsql │──────────────────────────────────────► │   nextsqld    │
 │   CLI    │                                        │ TokenVerifier │
 └──────────┘   (6) normal session, RBAC on every stmt└──────────────┘
```

The **broker** is a small new component (`cmd/nextsql-auth-broker`, or a
subcommand mode of `nextsqld` for single-node deployments — see §5.4). It is the
*only* new network service. `nextsqld` itself gains **no** outbound HTTP and
**no** OIDC parsing on the connection path; it keeps doing exactly what it does
today for `NSSC1.` credentials.

### 5.1 Why brokered exchange over direct server-side JWT verification

A direct design (server accepts a raw IdP JWT in the password slot, prefix
`NSIDP1.`, validates it against a cached JWKS itself) was considered and is
documented as the **alternative** in §9. Brokered exchange is recommended
because:

| Concern | Brokered exchange | Direct server verification |
|---|---|---|
| SQL auth hot path | unchanged; offline; ~microseconds Ed25519 verify | must consult cached JWKS, handle refresh, tolerate IdP outage per-connection |
| IdP outage blast radius | new logins via broker only | every reconnect of every OIDC client |
| Config surface on `nextsqld` | none new on the data plane | issuer list, JWKS URLs, HTTP client, proxy, timeouts, clock, cache on every node |
| Mapping policy location | one place (broker) | replicated to every node, kept in sync |
| Multi-node / HA | broker is stateless behind a load balancer; nodes need nothing | JWKS cache + policy must be consistent across the voter set |
| Audit of the *exchange* (claims seen, mapping applied) | centralized in the broker | spread across nodes |
| Reuses shipped `NSSC1.` verifier, `NSTR` revocation, expiry-close | fully | partially (needs a parallel path) |
| Token size on the wire | small fixed `NSSC1.` | full JWT (often 1–4 KiB) every connection |

The cost is running one more component. For deployments that will not run it,
§5.4 gives an embedded mode, and §9's direct mode remains a documented option if
a future requirement (e.g. "absolutely no additional process") demands it.

### 5.2 Interactive flow (Authorization Code + PKCE)

1. `nextsql login --idp corp` reads the named IdP profile from client config
   (§7.2): `issuer`, `client_id`, `broker_url`, optional `scopes`.
2. CLI performs OIDC discovery (`<issuer>/.well-known/openid-configuration`),
   generates a PKCE `code_verifier`/`code_challenge` and a `state` + `nonce`,
   starts a transient `http://127.0.0.1:<random>/callback` listener, and opens
   the system browser to the authorization endpoint (`response_type=code`,
   `scope=openid profile email` plus configured extras, e.g. `groups`).
3. After IdP authentication the browser redirects to the local callback with
   `code` + `state`. CLI verifies `state`, exchanges `code` + `code_verifier`
   at the token endpoint over TLS, and receives an `id_token` (JWT) and
   possibly an `access_token`.
4. CLI POSTs `{ id_token, nonce }` to `broker_url/v1/exchange` over TLS 1.3
   (broker cert verified against the OS/pinned trust store).
5. Broker validates the ID token (§5.5), applies the mapping policy (§6), and
   mints an `NSSC1.` credential (§5.6).
6. Broker returns `{ credential, principal, roles, expires_at }`. CLI stores it
   in the OS keychain / `0600` file under `~/.config/nextsql/credentials/` keyed
   by host+principal, and uses it as the password for subsequent connections
   until `expires_at`.
7. On expiry the CLI silently re-runs the flow if a cached IdP refresh token is
   still valid, otherwise prompts for interactive re-login.

No NextSQL credential is ever written to a shell history, URL, or log. The IdP
`refresh_token`, if issued, is stored with the same protection and is the only
long-lived secret on the client.

### 5.3 Client-credentials flow (workloads)

`nextsql login --idp corp --client-credentials --client-secret-file S` (or a
private-key-JWT client assertion) obtains an `access_token` directly from the
IdP and exchanges it at `broker_url/v1/exchange` with `{ access_token }`. The
broker validates it as an access token (introspection endpoint per RFC 7662, or
JWT validation if the IdP issues JWT access tokens) and maps its `sub` /
`client_id` and any roles/groups claim the same way. Recommended for workloads
that lack an mTLS identity; mTLS + `nextsql token mint` remains the first-choice
machine path.

### 5.4 Embedded broker mode

For single-node / non-HA deployments the broker logic ships as
`nextsqld --auth-broker-listen 127.0.0.1:8645` (separate listener, separate
handler, still no OIDC on the SQL listener). It uses the same `NSIP` policy file
and an `NSTK` issuing key held in the data directory. This keeps small
deployments to one process while preserving the "SQL path stays offline"
property. HA deployments run the standalone `cmd/nextsql-auth-broker`.

### 5.5 ID-token validation (in the broker)

Fail-closed unless *all* hold:

- JWT is well-formed, alg is one of the configured allowed asymmetric algs
  (`RS256`, `ES256`, `PS256`; **`none` and all MAC algs rejected**).
- `iss` exactly equals the configured issuer for the profile.
- `aud` contains the configured `client_id`; if `azp` is present it equals the
  `client_id`.
- Signature verifies against a key from the issuer's JWKS (cached; §5.7),
  selected by `kid`.
- `exp` in the future, `iat`/`nbf` valid, within 120 s skew (configurable,
  ceiling 300 s).
- `nonce` equals the value the CLI generated (bound through the exchange
  request); replay-cache the `jti`/`sub|iat` for the token's remaining lifetime
  to reject double-exchange.
- Required claims present per policy (default: `sub`; plus whichever claim the
  mapping uses).

Access tokens (client-credentials): same issuer/sig/exp checks; audience check
against a configured resource identifier; or RFC 7662 introspection with
`active == true`.

### 5.6 Minted credential

The broker builds `TokenClaims` (existing struct) with:

- `Principal` = mapped native principal (§6.1);
- `Roles` = mapped native roles (§6.2) — always non-empty; if the mapping
  yields no roles the exchange **fails** (no silent full-inheritance);
- `Database` / `Realm` = from the exchange request's requested target, if any,
  else empty;
- `Audience` = the deployment audience (so a broker for deployment A cannot mint
  for deployment B if `token_audience` is set);
- `IssuedAt` = now; `ExpiresAt` = `min(now + configured_oidc_ttl,
  now + verifier_max, idp_token_exp)`. Default `oidc_ttl` = 1 h.
- `KeyID` / signature via the broker's `NSTK` current key.

`nextsqld` needs the broker's public key in its `token_verify_keyset` — i.e. the
broker's issuing key is just another key in the existing `NSTK` set
(`nextsql token export-public`). **No new server config beyond what short-lived
credentials already require**, except the optional `identity_source` labeling
hint (§8).

### 5.7 JWKS caching and key rotation

- Broker fetches `jwks_uri` from discovery, caches keys with a soft TTL
  (default 1 h) and a hard TTL (default 24 h).
- On an unknown `kid`, one immediate refresh is attempted (rate-limited to once
  per 5 min per issuer) before failing.
- Stale-within-hard-TTL keys are used if a refresh fails, so a brief JWKS
  outage does not break logins; past the hard TTL, exchange fails closed.
- No key material is persisted; a broker restart re-fetches.

## 6. Identity and role mapping (`NSIP` policy)

A single versioned, operator-authored policy document named **`NSIP` (NextSQL
Identity Policy) v1**. It is written mode `0600` with an atomic rename,
magic/version-tagged and fully corruption-validated on decode, and reloadable on
`SIGHUP` with last-known-good rollback — the same on-disk contract as
`NSTK`/`NSTR` today. (Envelope-at-rest encryption under a deployment key is a
follow-on that will land for `NSTK`/`NSTR`/`NSIP` together; `NSIP` holds no
secret material — issuer URLs, claim names, principal templates, role names.)
Loaded by the broker.

**Status: the policy engine is implemented** in
`internal/auth/identitypolicy.go` (2026-08-31) — `PolicyDoc` encode/decode,
`compilePolicy` validation (RE2 compile, login-charset and role-name checks,
all bounds), `IdentityPolicy.Map` (subject match → principal → group→role),
`IdentityPolicy.Authorize` (Map + RBAC intersection), `IntersectRoles`,
`Reload` last-known-good, `FuzzDecodeIdentityPolicy`, `FuzzMapClaims`. The
broker (increment 3) now consumes it via `IdentityPolicy.Map`; the automatic
`security.ACL` membership feed for `Authorize`'s `held` argument is a later
increment (the broker accepts an optional `RoleMembershipFunc` hook today, and
the server's `ACL.AllowedScoped` enforces no-escalation on every statement
independently of the broker).

### 6.1 Subject → principal

Ordered rules, first match wins. Each rule:

```text
issuer   = "https://corp.okta.com/oauth2/abc"
match    = claim "email" ends_with "@corp.example"      # or: sub equals, claim regex (RE2, anchored)
principal = lower( claim "email" before "@" )            # or a literal, or a template
require  = claim "email_verified" equals true            # optional extra gates
```

- `principal` templates support only explicit named claims and a small set of
  pure transforms (`lower`, `before`/`after` a literal delimiter, `replace` of
  a fixed set). No arbitrary code.
- The resolved principal is normalized to the same charset as native logins
  (1–128 lowercase `[a-z0-9._-]`). Anything else fails closed.
- If no rule matches → **deny**.

### 6.2 Groups → roles

```text
group_claim = "groups"           # array claim; or "roles", or a nested path
mappings:
  "db-readers"   -> ["reporting_ro"]
  "db-admins"    -> ["app_admin", "reporting_ro"]
  "/^team-(.+)$/" -> ["team_${1}_rw"]     # RE2 capture into a role template
default_roles = []                # roles applied when the user matched a
                                  # principal rule but no group mapping; empty
                                  # means "deny if no group mapped a role"
```

- The union of mapped roles is computed, then **intersected with the roles the
  resolved principal is actually a member of in NextSQL RBAC**. This intersection
  is what goes into the `NSSC1.` `Roles` list, which `ACL.AllowedScoped` already
  enforces as no-escalation. A mapping that names a role the principal does not
  hold is a no-op, logged at the broker.
- If the intersection is empty → **deny** (`I1`: no session without a real
  grant).
- Unknown groups in the token are ignored. Absence of the group claim when
  `group_claim` is configured → deny.
- Hard cap: 16 mapped roles (matches `maxTokenRoles`).

### 6.3 Just-in-time principal creation (optional, off by default)

`jit_provision = true` in the policy lets the broker, on a matched subject with
no existing native principal, call a privileged control connection to
`CREATE USER <principal> LOGIN` and `GRANT` the mapped roles. This is:

- **off by default** and gated behind a dedicated broker capability + a
  privileged NextSQL role for the broker's own control connection;
- still bounded by `I1` at session time (the just-created user genuinely holds
  the roles);
- fully audited as a DDL event with `actor = broker` and the source claim.

Recommended only for deployments that accept "IdP membership is the source of
truth for who may connect". Everyone else pre-creates principals.

## 7. Configuration surface

### 7.1 Broker config (`nextsql-auth-broker.conf` / `nextsqld` keys for embedded)

```text
listen                = 127.0.0.1:8645
tls_cert / tls_key    = ...                 # broker's own server TLS 1.3
identity_policy       = /etc/nextsql/idp-policy.nsip
issuing_keyset        = /etc/nextsql/broker-issuing.nstk   # holds a private NSTK key
deployment_audience   = prod-eu             # -> minted credential Audience
oidc_credential_ttl   = 1h                  # <= server verifier max
skew                  = 120s

[idp "corp"]
issuer                = https://corp.okta.com/oauth2/abc
client_id             = 0oa...
client_secret_file    = /etc/nextsql/corp-client-secret   # code flow (confidential client) / CC flow
allowed_algs          = RS256,ES256
jwks_soft_ttl         = 1h
jwks_hard_ttl         = 24h
group_claim           = groups
```

### 7.2 Client config (`~/.config/nextsql/config.toml`)

```text
[idp.corp]
issuer     = "https://corp.okta.com/oauth2/abc"
client_id  = "0oa..."
broker_url = "https://auth.db.internal"
scopes     = ["openid", "profile", "email", "groups"]
```

### 7.3 `nextsqld` (data plane)

Only the already-existing short-lived-credential keys. The broker's public
issuing key is added to `token_verify_keyset`. Optionally a new
`token_identity_source_hint` map (§8) to label broker-issued credentials as
`oidc` in the audit log; if unset they simply audit as `token`.

## 8. Audit

- **Broker**: one structured record per exchange attempt — `issuer`, `sub`
  (hashed if policy sets `audit_hash_subject`), matched principal rule id,
  resolved principal, IdP groups seen, roles mapped, roles after RBAC
  intersection, outcome, minted token id, `expires_at`. Failures record the
  reason from `I5`. Never logs the ID token, access token, client secret, or
  the minted credential.
- **`nextsqld`**: `auditAuth` records `identity_source`:
  - `oidc` — broker-issued credential, no client cert;
  - `mtls+oidc` — broker-issued credential presented on an mTLS connection.

  The server distinguishes an OIDC-origin `NSSC1.` from a hand-minted one via an
  optional `source` marker in the claims (new optional claim field, backward
  compatible: absent ⇒ `token`) set by the broker, cross-checked against the
  `token_identity_source_hint` for that `KeyID`. A minted credential cannot
  upgrade its own label; the label is a function of the verifying key, not of
  attacker-controlled bytes.
- The broker↔`nextsqld` correlation id (minted token id) lets an operator join
  the two logs.

## 9. Alternative: direct server-side JWT verification (documented, not chosen)

Prefix `NSIDP1.` on the password slot carries a raw compact JWT. `nextsqld`
gains `oidc_issuer`, `oidc_jwks_uri` (or discovery), `oidc_client_id`,
`oidc_identity_policy`, an HTTP client, and a JWKS cache. On `Auth` it validates
the JWT (§5.5 checks), applies an embedded copy of the mapping policy, and binds
the session exactly like a token session (`ACL.AllowedScoped`, expiry-close at
`exp`).

Pros: no new component; nothing to deploy. Cons: every node needs outbound
HTTPS to the IdP, a synchronized JWKS cache and policy across the voter set, and
per-connection tolerance of IdP latency/outage; larger tokens on every
connection; the SQL auth path is no longer fully offline. Kept here as a
fallback if operational constraints ever rule out the broker; **not** part of
the accepted delivery plan.

A thin variant — a `urn:ietf:params:oauth:grant-type:token-exchange`
(RFC 8693) endpoint *on `nextsqld`* that returns an `NSSC1.` — is essentially
the embedded broker of §5.4 and is preferred over `NSIDP1.` if the broker must
live inside `nextsqld`.

## 10. Delivery plan

Each increment is independently shippable and testable. Checkboxes mirror
`TODO.md` "External IdP".

1. **OIDC design** — this document. *(done, 2026-08-31)*
2. **`NSIP` policy core** — versioned encoded policy type in `internal/auth`
   (`identitypolicy.go`): parse, validate, subject-match, group-map,
   RBAC-intersection, `SIGHUP` reload + last-known-good. Pure, no network. Unit +
   fuzz (`FuzzDecodeIdentityPolicy`, `FuzzMapClaims`). *(done, 2026-08-31 — the
   current increment)*
3. **Broker skeleton** — `cmd/nextsql-auth-broker`: TLS listener, `/v1/exchange`,
   JWKS cache with soft/hard TTL + rate-limited refresh, ID-token validation,
   mint via `NSTK`. Integration test with a fake IdP (self-signed JWKS + issued
   JWTs) and a real `TokenVerifier`. *(done, 2026-08-31 — `internal/oidc`,
   `internal/authbroker`; the RBAC-membership feed for `Authorize`'s `held`
   argument is an optional hook, wired in a later increment; the server's
   `ACL.AllowedScoped` still enforces no-escalation on every statement.)*
4. **CLI `nextsql login`** — discovery, PKCE code flow, local callback, exchange,
   secure credential storage, silent refresh, `nextsql logout`, `nextsql whoami`.
5. **Server audit labeling** — optional `source` claim + `token_identity_source_hint`,
   `identity_source` `oidc` / `mtls+oidc`, `auditAuth` plumbing, redaction test.
6. **Client-credentials grant** + optional JWT access-token / introspection.
7. **Embedded broker mode** (`nextsqld --auth-broker-listen`).
8. **Optional JIT provisioning**, off by default, behind its own gate.
9. **P25 doc + audit update**: flip `docs/security.md` OIDC rows to
   `implemented` / `tested` as each lands; `ENCRYPTED CLIENT` and password
   hashing are separate P25 groups.

Non-Go driver login helpers (JS/PHP `login()` convenience) are a follow-on after
increment 4, like the existing short-lived-credential helper follow-on.

## 11. Testing plan

- **Policy unit tests**: rule ordering, template transforms, normalization
  rejects, group RE2 captures, RBAC intersection (mapped-but-not-member ⇒
  dropped; empty ⇒ deny), 16-role cap, `SIGHUP` last-known-good.
- **Fuzz**: `NSIP` decoder, claim-mapping over arbitrary JSON claim sets.
- **Broker integration**: fake IdP; happy path; wrong `iss`; wrong `aud`;
  `alg=none`; MAC alg; expired; bad `nonce`; replayed token; unknown `kid` +
  refresh; JWKS hard-TTL expiry ⇒ deny; JWKS soft-stale ⇒ allow; unmapped
  subject ⇒ deny; unmapped groups ⇒ deny; escalation attempt (group maps to a
  role the principal lacks) ⇒ role dropped, deny if that leaves none.
- **End-to-end** (`tests/integration/oidc_login_test.go`): fake IdP → broker →
  `nextsqld`; assert session principal + effective roles, that a statement
  outside the mapped roles is `Forbidden`, that the session closes at the minted
  credential's expiry, and that `nextsql token revoke` on the minted id kills it
  immediately.
- **Failure isolation**: IdP down ⇒ existing sessions and password/mTLS logins
  unaffected; broker down ⇒ same; neither blocks commit.
- **Audit**: `identity_source` values; no token/secret/credential in any log;
  broker↔server correlation by minted token id.
- Race (`-race`) on broker and policy; `go build ./...`; serialized
  `go test -p 1 ./...` before the increment closes.

## 12. Open questions

- **Subject stability**: some IdPs rotate `sub` or `email`. Recommend keying
  principal rules on `sub` where the IdP guarantees stability, else document the
  `email` risk. Do we want an explicit `subject_claim` per profile? (Leaning
  yes, default `sub`.)
- **Multi-IdP for one principal**: allowed — rules are issuer-scoped and
  converge on the same native principal. No conflict handling needed beyond
  "first matching rule wins".
- **Realm routing**: in multi-database hosting (`docs/design-multidatabase-dbaas.md`),
  should the broker pick the realm from a claim, or only from the client's
  requested target? Leaning: client requests target, policy may *constrain*
  which realms an issuer may mint for.
- **Refresh-token custody**: OS keychain vs encrypted file — support both,
  keychain preferred where available.
- **Studio**: the browser-based Studio (P29) will use the same broker
  `/v1/exchange` with a browser-side PKCE flow; no separate design needed, but
  CORS + a stricter `redirect_uri` allowlist on the broker will be required.
