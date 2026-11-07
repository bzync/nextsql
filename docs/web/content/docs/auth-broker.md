# Authentication broker (OIDC & SSO)

`nextsql-auth-broker` is NextSQL's authentication broker: a lightweight HTTPS microservice that exchanges OpenID Connect (OIDC) ID tokens and OAuth2 JWT access tokens for ordinary, short-lived `NSSC1.` NextSQL credentials.

The database engine ([`nextsqld`](/docs/config)) **never talks to external identity providers and contains no OIDC, OAuth2, or JWKS parsing logic**. It verifies compact Ed25519-signed credentials completely offline.

```arch
Developer / App
  │
  ├─ 1. Authenticate with IdP (Okta / Azure / Google / Keycloak)
  │
  ├─ 2. POST /v1/exchange (Sends OIDC Token) ──► nextsql-auth-broker
  │                                                     │
  │                                             Validates JWKS,
  │                                             evaluates NSIP policy,
  │                                             mints NSSC1. credential
  │                                                     │
  └─ 3. Connect with NSSC1. credential ◄────────────────┘
        │
        ▼
     nextsqld (Verifies Ed25519 signature offline; zero network calls)
```

---

## Why an external broker?

1. **Air-gapped database engine**: `nextsqld` requires no outbound internet or internal network access to your Identity Provider. If Okta, Azure AD, or Google suffers a temporary outage, existing database connections and offline token validations are completely unaffected.
2. **Minimal attack surface**: Complex HTTP clients, JSON Web Key Set (JWKS) caches, redirect callbacks, and claim-mapping engines are kept outside the database process boundary.
3. **No privilege escalation**: Mapped roles from the IdP are strictly intersected with real database RBAC (`ACL.AllowedScoped`). An external claim cannot grant permissions that do not exist in the database catalog.

---

## Installation & startup

Install the broker binary with Go:

```bash
go install github.com/bzync/nextsql/cmd/nextsql-auth-broker@v0.0.10
```

Start the standalone daemon with a configuration file:

```bash
nextsql-auth-broker --config /etc/nextsql/auth-broker.conf
```

> [!NOTE]
> **Embedded mode for single-node setups**:
> For non-HA single-node environments, `nextsqld` can run the broker internally on a separate listener:
> ```bash
> nextsqld --auth-broker-listen 127.0.0.1:8645 \
>   --auth-broker-config /etc/nextsql/auth-broker.conf ...
> ```
> Embedded mode requires TLS off loopback, verifies key compatibility before starting, and rejects Raft/HA clusters.

---

## Broker configuration (`auth-broker.conf`)

The configuration file is a line-based `key = value` format with per-provider sections.

```ini
# /etc/nextsql/auth-broker.conf

# Global listener and TLS
listen               = 127.0.0.1:8645
tls_cert             = /etc/nextsql/broker.crt
tls_key              = /etc/nextsql/broker.key
log_level            = info

# Deployment scope & credential validity
deployment_audience  = nextsql-prod
oidc_credential_ttl  = 1h

# Key material and mapping policy
issuing_keyset       = /etc/nextsql/broker-issuing.nstk
identity_policy      = /etc/nextsql/identity-policy.nsip

# Optional JIT provisioning
jit_provisioning     = false
max_principals       = 1000

# Provider profile: Okta
[idp "okta"]
issuer               = https://company.okta.com
client_id            = 0oa123456789abcdef
jwks_uri             = https://company.okta.com/oauth2/v1/keys
allowed_algs         = RS256,ES256
jwks_soft_ttl        = 1h
jwks_hard_ttl        = 24h
skew                 = 1m

# Provider profile: Google Workspace
[idp "google"]
issuer               = https://accounts.google.com
client_id            = 123456789-apps.googleusercontent.com
allowed_algs         = RS256
```

### Global configuration keys

| Key | Description | Default |
|---|---|---|
| `listen` | IP address and port for the broker HTTPS listener. | `127.0.0.1:8645` |
| `tls_cert` / `tls_key` | TLS certificate and private key (required off loopback). | None |
| `issuing_keyset` | Path to the private `NSTK` signing keyset used to mint credentials. | Required |
| `deployment_audience`| String matching the server's `token_audience`. | Required |
| `identity_policy` | Path to the compiled `NSIP` claim-to-role policy file. | Required |
| `oidc_credential_ttl`| Maximum duration for minted credentials (`min(ttl, idp_expiry)`). | `1h` (max `12h`) |
| `jit_provisioning` | Auto-create native users when a verified claim arrives. | `false` |
| `allowed_role_boundary`| Comma-separated list of roles JIT users can hold (prevents ADMIN). | None |
| `max_principals` | Maximum number of active JIT principals before fail-closed denial. | `1000` |

### Provider profile keys (`[idp "name"]`)

| Key | Description |
|---|---|
| `issuer` | Exact OIDC issuer URL (must match the token's `iss` claim). |
| `client_id` | Expected OAuth2 client ID (must match `aud` or `azp`). |
| `jwks_uri` | Remote JWKS URI. If omitted, discovered via `/.well-known/openid-configuration`. |
| `allowed_algs` | Comma-separated allowed JWS signature algorithms (`RS256,ES256,PS256`). MAC and `none` algorithms are permanently rejected. |
| `access_token_audience`| Resource audience to enable OAuth2 JWT client-credentials exchange. |
| `jwks_soft_ttl` | Cache TTL for IdP public keys before asynchronous refresh. Default: `1h`. |
| `jwks_hard_ttl` | Maximum cache TTL during IdP outages before token validation fails closed. Default: `24h`. |

---

## Server configuration (`nextsqld.conf`)

Configure `nextsqld` to accept the broker's minted credentials:

```ini
# /etc/nextsql/nextsql.conf

# Public verify keyset exported from the broker's issuing keyset
token_verify_keyset        = /etc/nextsql/broker-verify.nstk
token_audience             = nextsql-prod

# Tag sessions issued by broker key id 1 as 'oidc' in audit logs
token_identity_source_hint = 1:oidc
```

Generate the keypair with the `nextsql token` CLI:

```bash
# 1. Generate the private issuing keyset on the broker
nextsql token keygen --out /etc/nextsql/broker-issuing.nstk

# 2. Export the public-only verify keyset to distribute to nextsqld nodes
nextsql token export-public \
  --in /etc/nextsql/broker-issuing.nstk \
  --out /etc/nextsql/broker-verify.nstk
```

---

## Identity policy (`NSIP`)

The `NSIP` policy translates verified IdP claims into NextSQL users and roles. It is compiled and managed via the `nextsql` CLI:

```text
# Subject rules: map verified claim to a database principal
rule sub-to-user:
  claim: email
  transform: lowercase
  pattern: ^([a-z0-9._%+-]+)@company\.com$
  principal: $1

# Group mappings: map IdP groups claim to NextSQL RBAC roles
mapping groups:
  claim: groups
  match "okta-engineers"  -> role developer
  match "okta-analysts"   -> role analyst
  match "okta-finance"    -> role reporting
  default                 -> role guest
```

`SIGHUP` to `nextsql-auth-broker` reloads both the identity policy and the issuing keyset with last-known-good rollback.

---

## Client & developer workflow

### 1. Interactive login (Single Sign-On)

Developers log in with their browser using Authorization Code + PKCE:

```bash
nextsql login --idp okta
```

1. The CLI opens the default browser to Okta/Google.
2. The user signs in and completes corporate MFA.
3. The browser redirects to a transient, bounded loopback listener (`127.0.0.1`).
4. The CLI exchanges the authorization code for an ID token, submits it to `nextsql-auth-broker`, and securely stores the minted `NSSC1.` credential locally in mode `0600`.

### 2. Running queries

Once logged in, all commands automatically use the active credential:

```bash
nextsql exec --idp okta --database app "SELECT CURRENT_USER, CURRENT_ROLES;"
```

If the credential expires, the CLI silently renews it using the cached IdP refresh token.

### 3. Machine-to-machine (Client Credentials)

Services authenticate using an OAuth2 client secret file:

```bash
nextsql login --client-credentials \
  --idp service-backend \
  --secret-file /etc/service/oauth-secret.key
```

### 4. Whoami & logout

Inspect the active identity metadata:

```bash
nextsql whoami
```

Revoke and remove local credentials:

```bash
nextsql logout --idp okta
```

---

## Security & threat model

* **Fail-closed key caching**: During an IdP outage, cached JWKS keys are honored up to `jwks_hard_ttl`. Beyond that threshold, token exchanges fail closed.
* **Audit trail**: When `token_identity_source_hint` is configured, sessions authenticated through the broker are tagged `identity_source = oidc` or `identity_source = mtls+oidc` in `system.sessions` and the hash-chained audit log.
* **No secrets in URLs or logs**: ID tokens, refresh tokens, client secrets, and `NSSC1.` tokens are never logged or exposed in process arguments.
* **Bounded execution**: All HTTP requests, JSON payloads, and token buffers are strictly capped at 1 MiB to prevent memory exhaustion.
