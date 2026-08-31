# Changelog

All notable changes to **NextSQL** are documented in this file.

NextSQL is currently under active development as `0.1.0-dev`.

This changelog follows the project source-of-truth model:

```text
TODO.md    = current implementation/status truth
PROJECT.md = intended finished product
TODO.md    = implementation status, sequencing, dependencies, and phase gates
SKILLS.md  = engineering/agent contract
AGENTS.md  = repository agent instructions
USAGE.md   = current user/operator manual
README.md  = project overview
CHANGELOG.md = notable shipped/verified changes
```

A roadmap item is not recorded as completed here until its implementation, tests, documentation, and applicable exit gate are complete.

---

## [Unreleased]

### P25 Security 2.0 — authentication broker skeleton (2026-08-31)

- New package `internal/oidc`: pure, offline OpenID Connect primitives — compact
  JWS signature verification (RS/PS/ES 256/384/512; `none` and every MAC
  algorithm rejected), JWKS document parsing (RSA and EC keys), a JWKS cache
  with soft / hard TTL and rate-limited refresh that serves soft-stale keys
  through a brief IdP outage and fails closed past the hard TTL, ID-token
  validation (`iss` / `aud` / `azp` / `exp` / `iat` / `nbf` / `nonce`, skew
  ceiling 300 s), and a replay guard. Decoders are fuzzed
  (`FuzzParseJWKS`, `FuzzParseCompact`, `FuzzVerify`).
- New package `internal/authbroker` and command `cmd/nextsql-auth-broker`: the
  NextSQL **authentication broker**. `POST /v1/exchange` takes an OIDC ID token,
  validates it against the cached JWKS for the named IdP profile, maps the
  verified claims through the `NSIP` identity policy, and mints an ordinary
  `NSSC1.` short-lived credential signed by a private `NSTK` key. The broker is
  the only component that speaks OIDC — `nextsqld` keeps verifying `NSSC1.`
  credentials offline and unchanged; the broker's public issuing key simply
  goes in every server's `token_verify_keyset`.
- The minted credential's lifetime is `min(configured TTL, time until the IdP
  token expires)`; its audience is the deployment audience; its roles are the
  policy-mapped set, intersected with the principal's real RBAC membership when
  a membership feed is wired (a later increment) — otherwise the server's
  `ACL.AllowedScoped` still drops any role the principal does not hold.
- Every exchange attempt emits a structured audit record (issuer, hashed
  subject, matched rule id, principal, mapped and effective roles, outcome,
  minted token id, expiry). It never logs the ID token, the minted credential,
  or a client secret. Rejections return a generic message; the specific reason
  goes only to the audit log.
- `SIGHUP` reloads the identity policy and the issuing keyset with last
  known-good rollback.
- Integration test (`internal/authbroker`, fake IdP → broker → real
  `auth.TokenVerifier`): happy path, RBAC intersection, replay, `alg=none` /
  MAC alg / wrong `iss` / wrong `aud` / bad `nonce` / unmapped subject /
  unmapped groups / missing group claim, JWKS outage fails closed, credential
  TTL bounded by the IdP token expiry, reload keeps last known-good.
- Not built yet: the client `nextsql login` flow, the `oidc` / `mtls+oidc`
  audit `identity_source`, client credentials, the embedded broker mode
  (`nextsqld --auth-broker-listen`), and optional JIT provisioning.

### P25 Security 2.0 — `NSIP` identity-policy engine (2026-08-31)

- New `internal/auth/identitypolicy.go`: the offline **`NSIP` (NextSQL Identity
  Policy)** engine an external-identity broker will consult to turn verified IdP
  claims into a native principal and a no-escalation role set. Pure — no
  network, no dependency on the SQL engine.
- `PolicyDoc` is a versioned, magic-tagged, fully corruption-validated binary
  document written mode `0600` with an atomic rename; `IdentityPolicy.Reload`
  keeps the last known-good policy when a new file fails to parse, validate, or
  compile (same on-disk contract as `NSTK`/`NSTR`).
- `IdentityPolicy.Map` applies ordered, issuer-scoped subject rules (claim
  `equals`/`prefix`/`suffix`/anchored-RE2 conditions, ANDed) and a bounded pure
  transform pipeline (`lower`, `before`, `after`, `replace`) to derive the
  principal; the result must be a valid `[a-z0-9._-]{1,128}` login or the
  mapping fails closed. Groups map to roles by literal match or anchored RE2
  with `${n}` capture templates; the union is capped at 16.
- `IdentityPolicy.Authorize` runs `Map` then intersects the mapped roles with
  the principal's real RBAC membership (`IntersectRoles`); an empty intersection
  is a denial. This is the no-escalation guarantee — an external identity can
  only narrow what a native grant already allows.
- Every unmatched, ambiguous, or over-cap input is a typed `Forbidden` error.
  Tests: `internal/auth/identitypolicy_test.go`, `FuzzDecodeIdentityPolicy`,
  `FuzzMapClaims`.
- Not wired to anything yet: no broker, no `nextsqld` path, no audit change, no
  config key. `docs/security.md`'s P25 audit records the OIDC end-to-end path as
  still not implemented; the three mapping-policy rows move to
  `implemented: partial` / `tested: yes` for the engine.

### P25 Security 2.0 — external IdP (OIDC) design accepted (2026-08-31)

- Accepted design `docs/design-oidc-external-idp.md`. No code ships; this is a
  design-only increment and `docs/security.md`'s P25 audit still records OIDC
  as not implemented / not tested.
- Chosen architecture: a standalone or embedded **authentication broker** runs
  the OIDC Authorization Code + PKCE (interactive) or client-credentials
  (workload) flow, validates the IdP token against a soft/hard-TTL cached JWKS
  (`iss` / `aud` / `alg` allowlist rejecting `none` and MAC algs / `exp` /
  `nonce` / replay), and mints an existing `NSSC1.` short-lived credential. The
  `nextsqld` SQL authentication path is unchanged and never contacts the IdP;
  the broker's issuing key is just another `NSTK` key in `token_verify_keyset`.
- `NSIP` (NextSQL Identity Policy): versioned, deployment-encrypted, `SIGHUP`
  last-known-good. Issuer-scoped subject→principal rules and group→role
  mappings; the mapped role set is intersected with the principal's real RBAC
  membership (mapped-but-not-member dropped, empty ⇒ deny), so an external
  identity can only narrow a native grant and never bypass RBAC —
  `ACL.AllowedScoped` is enforced on every statement exactly as for
  hand-minted tokens.
- Broker-issued credential logins will audit as `identity_source` `oidc` /
  `mtls+oidc`, derived from the verifying key rather than attacker-controlled
  bytes. Direct server-side JWT verification (`NSIDP1.`) is documented as the
  rejected alternative.

### P25 Security 2.0 — signed short-lived credentials (2026-08-31)

- Clients may authenticate with a signed short-lived credential presented **in
  place of the password** (same `Auth` password field, same native principal,
  same RBAC). Wire form `NSSC1.` + base64url of Ed25519-signed claims
  (`internal/auth`). No new frame or auth method.
- Claims carry a signing-key id, a random token id, issued-at / not-before /
  expires-at, the native principal, and optional audience, database, realm, and
  role scopes. `TokenVerifier` fails closed on a bad/retired key, invalid
  signature, the validity window (60 s skew), a lifetime over the verifier
  maximum (default 24 h, hard ceiling 30 d), an audience mismatch against
  `token_audience` (a configured audience also rejects an unscoped credential),
  a database-scope mismatch, or revocation.
- The protocol server additionally requires the credential principal to equal
  the Hello user and to be a known native user, narrows the session to the
  credential's role scope (`ACL.AllowedScoped`, with a no-escalation guard —
  the principal must already hold every listed role), and closes the session at
  the credential's expiry.
- `token_verify_keyset=FILE` enables verification; optional
  `token_revocations=FILE` and `token_audience=STRING`. The keyset (`NSTK` v1)
  is a rotatable set of Ed25519 keys with `current`/`retired` flags; servers
  keep a verify-only copy. The revocation set (`NSTR` v1) holds revoked token
  ids (pruned at their own expiry) and per-principal "issued at or before"
  cutoffs. `SIGHUP` atomically reloads both, last known-good on failure.
- New `nextsql token` subcommands: `keygen`, `rotate`, `retire`, `list-keys`,
  `export-public`, `mint`, `revoke`, `verify`. Auth audit records
  `identity_source` `token` / `mtls+token`.
- Official drivers are unchanged — the credential goes wherever the password
  would. Non-Go driver convenience helpers are a documented follow-on.

No persistent database or NSQL wire-format change is introduced. `go build
./...`, targeted functional/race tests (`internal/auth`, `internal/security`,
`internal/protocol`, `internal/executor`, `internal/config`,
`tests/integration`), and 8 s `FuzzDecodeTokenClaims` / `FuzzDecodeTokenKeys`
are green.

### P25 Security 2.0 — mTLS identity, rotation, and revocation (2026-08-31)

- `nextsqld --tls-client-ca` / `tls_client_ca` enables TLS 1.3 mutual
  authentication with `RequireAndVerifyClientCert` against an explicit client
  CA bundle. Missing, untrusted, expired, or wrong-EKU client certificates fail
  during the standard `crypto/x509` handshake.
- The verified leaf must carry exactly one NextSQL URI SAN
  `nextsql://service/<principal>` matching the native login user. Native
  password authentication and RBAC still run; the certificate does not grant
  privileges by itself.
- Auth audit events now record `identity_source` (`native`, `mtls`, or
  `mtls+native`). The CLI accepts paired `--tls-client-cert` /
  `--tls-client-key` flags and matching environment variables.
- `nextsqld` atomically reloads its server certificate/key, client trust bundle,
  and optional `--tls-client-crl` PEM bundle on `SIGHUP`. Invalid reloads retain
  the last known-good snapshot. Trust rotation supports an explicit old+new CA
  overlap window.
- CRLs must be current, signed by an authority in the client bundle, and cover
  every non-root certificate in the verified chain. Missing coverage, stale or
  invalid CRLs, and revoked serials fail the handshake. Successful mTLS reloads
  terminate all accepted connections, including pre-authentication handshakes,
  so clients reauthenticate. OCSP is not implemented.
- The P25 audit in `docs/security.md` explicitly separates designed,
  implemented, tested, and production-gated state. Short-lived credentials,
  IdP integration, field-level client encryption, password-hash evolution, and
  signed audit remain open.

No persistent or NSQL wire-format change is introduced.
Targeted functional/race tests, command builds, and serialized
`go test -p 1 ./... -count=1` are green.

### P24 Full-text Search 2.0 — exit gate (2026-08-31)

- **Bounded fuzzy vocabulary work.** Fuzzy and typo-tolerant SEARCH now fails
  closed after inspecting 4096 distinct vocabulary terms, for both inverted
  indexes and sequential-scan fallback. Matching expansions retain the tighter
  256-term / 8192-byte / 4096-work-unit limits. OSA Damerau-Levenshtein now
  uses three bounded rows instead of a full term-length-squared matrix.
- **Compatibility and quality gate.** A golden fixture pins Phase-10 BM25
  constants and adjacent phrase behavior. End-to-end quality fixtures cover
  exact BM25 ordering, phrases, prefix, fuzzy, typo tolerance, English
  stop/stem/synonym phrases, and French/German/Spanish analyzers.
- **Encrypted recovery gate.** An analyzer-aware kill/reopen test proves
  committed English postings and analyzer metadata recover, uncommitted
  posting changes do not survive, and distinctive terms do not appear as
  plaintext in database, WAL, or UNDO files.
- Tests: `TestP24BM25PhraseCompatibilityGolden`,
  `TestFuzzyWithinMatchesReference`, `TestFuzzyVocabularyBudgetFailClosed`,
  `TestP24SearchQualityFixtures`, `TestP24FuzzyVocabularyCap`, and
  `TestP24EncryptedCrashRecovery`. `go build ./...`, targeted functional and
  race suites, a 5-second `FuzzTokenize`, and serialized
  `go test -p 1 ./... -count=1` are green.

### P24 Full-text Search 2.0 — faceting (2026-08-31)

- **Faceting.** `SELECT * FROM t SEARCH col FOR '…' FACET cat [, year …]`
  returns independent histograms over the full SEARCH match set (query-time
  only, no catalog or posting-format bump). Output is `facet STRING` (column
  name), `value STRING` (canonical display), `count DECIMAL`. Each facet
  column is its own histogram, not a `GROUP BY` cross-product. `LIMIT n` is
  per-facet top-N; `NULL` is skipped; buckets are count descending then value
  ascending. Allowed types: `STRING`, `TEXT`, `DECIMAL`, `BOOL`, `UUID`,
  `TIMESTAMPTZ`. Requires `SELECT *` and `SEARCH`. Fails closed with `JOIN`,
  `GROUP BY` / `HAVING`, `DISTINCT`, `ORDER BY`, `OFFSET`, `NEAREST`, duplicate
  columns, more than eight columns, JSON/vector/geo types, and more than 1024
  distinct values on one facet column. `FACET` is not a reserved keyword.
  Field weighting, prefix, fuzzy, typo, phrase, analyzer, and
  `HIGHLIGHT`/`SNIPPET` matching is unchanged. `EXPLAIN` shows `Facet`.
- Tests: `TestFulltextFacet` (index + seq-scan + WHERE + LIMIT + NULL skip +
  typo + WEIGHT no-op + fail-closed), `TestFacetDistinctValueCap`,
  `TestBindFulltextFacet`, `TestSearchFacetPlan`. Parser fuzz seeds include
  `FACET`. `go build ./...` + `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/fulltext` / `internal/executor` `go test`
  + `-race` green; `FuzzTokenize` / `FuzzParse` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/optimizer.md`, `USAGE.md`,
  `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` /
  `limits.md` / `sql.md`.

### P24 Full-text Search 2.0 — field weighting (2026-08-31)

- **Field weighting.** Optional `WEIGHT <number>` after a `SEARCH` column
  (`SEARCH title WEIGHT 3, body FOR '…'`) scales that field's BM25 term
  frequency from existing position bands. Omitted weights are 1, so
  unweighted SEARCH keeps Phase 10 / multi-field BM25. Weights are
  query-time only (no catalog or posting-format bump) and apply to
  inverted-index SEARCH, seq-scan SEARCH, and hybrid RRF. A weight must be
  a finite numeric literal in `(0, 64]`; zero, negative, non-finite, and
  oversized values fail closed. `WEIGHT` is not a reserved keyword. Prefix,
  fuzzy, typo, phrase, analyzer, and `HIGHLIGHT`/`SNIPPET` matching is
  unchanged. `EXPLAIN` shows `weights=` when a non-default weight is used.
- Tests: `TestWeightedTF`, `TestQueryScoreWeighted`, `TestCheckFieldWeight`,
  `TestBindFulltextMultiField` (weights), `TestSearchChoosesMultiFieldFulltextIndex`
  (`weights=3,1`), `TestFulltextFieldWeight` (index + seq-scan + WEIGHT 1
  no-op + HIGHLIGHT + no cross-field phrase + fail-closed 0/65). Parser
  fuzz seeds include weighted SEARCH. `go build ./...` + `internal/fulltext`
  / `internal/sql/parser` / `internal/sql/binder` / `internal/sql/optimizer`
  / `internal/executor` `go test` + `-race` green; `FuzzTokenize` /
  `FuzzParse` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/optimizer.md`, `USAGE.md`,
  `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` /
  `limits.md` / `sql.md`.

### P24 Full-text Search 2.0 — multi-field search (2026-08-31)

- **Multi-field search.** `CREATE FULLTEXT INDEX` and `SEARCH` accept one to
  eight `STRING`/`TEXT` columns (`CREATE FULLTEXT INDEX ix ON t (title, body)` /
  `SEARCH title, body FOR '…'`). A multi-column `SEARCH` uses an inverted index
  whose column list matches in the same order; a different subset or order
  seq-scans those columns. Fields are analyzed independently and scored as one
  BM25 document (term frequency and length summed). Phrase matching is
  per-field via reserved position bands (`i·(MaxDocTokens+2)`); `"database
  performance"` does not match across `title`/`body`. Duplicate columns, more
  than eight fields, and a combined token count above 100 000 fail closed.
  Prefix, fuzzy, typo, analyzer, and `HIGHLIGHT`/`SNIPPET` behaviour is
  unchanged (highlight remains per column). No catalog or posting-format bump:
  single-column indexes keep the Phase 10 posting layout.
- Tests: `TestAnalyzeFieldsPositions`, `TestBindFulltextMultiField`,
  `TestSearchChoosesMultiFieldFulltextIndex`, `TestFulltextMultiFieldSearch`
  (index + seq-scan + cross-field AND + in-field phrase + no cross-field
  phrase + subset/reorder fallback + HIGHLIGHT + prefix/fuzzy/typo + UPDATE).
  Parser fuzz seeds include multi-column CREATE/SEARCH. `go build ./...` +
  `internal/fulltext` / `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/executor` / `internal/catalog` /
  `internal/xport` `go test` + `-race` green; `FuzzTokenize` / `FuzzParse` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/optimizer.md`, `USAGE.md`,
  `CHANGELOG.md`, `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` /
  `limits.md` / `sql.md`.

### P24 Full-text Search 2.0 — highlight/snippet generation (2026-08-31)

- **Highlight/snippet generation.** `HIGHLIGHT(col)` and `SNIPPET(col)` are
  SELECT-list functions that require `SEARCH` (no catalog or posting-format
  bump). They wrap original document tokens whose analyzed form participates
  in the SEARCH query (exact, synonym, prefix, fuzzy, and typo), using the
  SEARCH column's analyzer, so `runs` marks `running` and `car` marks
  `automobile`. Default markers are `<mark>` and `</mark>`.
  `HIGHLIGHT(col, pre, post)` and `SNIPPET(col, width [, pre, post])` override
  markers (max 32 runes, no NUL). `HIGHLIGHT` returns the full field.
  `SNIPPET` returns a window of `width` Unicode code points (default 160,
  range 16–4096) around the densest match cluster, with `…` on a truncated
  edge. Both fail closed outside the SELECT list of a SEARCH query, in
  `WHERE` / `JOIN` / `GROUP BY` / `HAVING` / DML, and on oversize markers or
  snippet width. Seq-scan SEARCH highlights the same way. Default
  BM25/phrase/prefix/fuzzy/typo ranking is unchanged.
- Tests: `TestTokenizeSpans`, `TestHighlightExact`,
  `TestHighlightPreservesOriginalCase`, `TestHighlightPrefixFuzzyTypo`,
  `TestHighlightEnglishStemAndSynonym`, `TestHighlightEnglishDropsStops`,
  `TestHighlightCustomMarkersAndEmptyQuery`, `TestHighlightMarkerLimits`,
  `TestSnippetWindow`, `TestSnippetShortTextNoEllipsis`,
  `TestSnippetWidthBounds`, `TestHighlightsTermPrefixAndFuzzy`,
  `TestBindHighlightRequiresSearch`, `TestFulltextHighlight` (index +
  seq-scan + custom markers + prefix/fuzzy/typo + english stem/synonym +
  snippet + fail-closed without SEARCH / width). `go build ./...` +
  `internal/fulltext` / `internal/sql/binder` / `internal/sql/parser` /
  `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — typo tolerance (2026-08-31)

- **Typo tolerance.** Unadorned SEARCH tokens (no trailing `*` / `~`) stay
  exact when any analyzed alternative is in the searchable vocabulary, so
  Phase 10 BM25/phrase behaviour is unchanged (`cat` does not match `cot`
  when `cat` is indexed; `cats` does not match `cat`). When every alternative
  is absent, SEARCH rewrites the group as an AUTO-distance fuzzy group
  (query-time only, no catalog or posting-format bump): `databse` matches
  `database`. Typo AUTO is stricter than explicit `~` (0 for 1–4 runes, 1
  for 5–8, 2 for 9+). Prefix and explicit fuzzy groups are unchanged. Phrase
  slots follow the same rule (`"databse performance"`). BM25 scores the best
  matching term. Distinct typo-matched terms consume the existing
  query-expansion caps (256 terms / 8192 bytes / 4096 work units) and fail
  closed. Seq-scan `SEARCH` without an index uses the scanned corpus as the
  vocabulary. Analyzers still run first (stem/stop/synonym); typo fallback
  is on the analyzed term, so a typo of a synonym partner is not rewritten
  into that group.
- Tests: `TestApplyTypoToleranceMissing`,
  `TestApplyTypoTolerancePresentExactUnchanged`,
  `TestApplyTypoToleranceShortStaysExactMiss`, `TestAutoTypoDistance`,
  `TestApplyTypoTolerancePrefixAndFuzzyUnchanged`,
  `TestApplyTypoTolerancePhrase`, `TestApplyTypoToleranceSynonymGroup`,
  `TestApplyTypoToleranceNilPresent`, `TestQueryMatchesTypo`,
  `TestQueryScoreTypoBestMatch`, `TestFulltextTypoSearch` (index + seq-scan
  + short-token miss + english `catalag` + synonym skip + expansion cap).
  `go build ./...` + `internal/fulltext` / `internal/executor` `go test` +
  `-race` green; `FuzzTokenize` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — fuzzy matching (2026-08-31)

- **Fuzzy matching.** A trailing ASCII `~` on a SEARCH token is a fuzzy group
  (query-time only, no catalog or posting-format bump): `cat~` matches indexed
  terms within a bounded OSA Damerau-Levenshtein distance (insert, delete,
  substitute, adjacent transpose), so `cot` matches and `catalog` does not.
  Distance is AUTO from the token's rune length (0 for 1–2 runes, 1 for 3–5,
  2 for 6+) or explicit `~1` / `~2`. `~0` and `~3` or higher fail closed.
  Fuzzy tokens skip stemming, stop-word filtering, and synonym expansion
  (a misspelled word is not a complete token); French elision still applies
  (`l'homm~` is fuzzy `homm`). Matching terms are a disjunction at that
  position (AND with other groups); phrase slots accept a fuzzy term
  (`"databas~ performance"`). BM25 scores the best matching term in each
  fuzzy group. Distinct fuzzy-matched terms consume the existing
  query-expansion caps (256 terms / 8192 bytes / 4096 work units) and fail
  closed. Mixing `*` and `~` on one token fails closed. A leading or infix
  `~` is not fuzzy (`~cat` is exact `cat`). Default BM25/phrase/prefix
  behaviour for unadorned tokens is unchanged (`cat` does not match `cot`).
  Seq-scan `SEARCH` without an index uses the same fuzzy rules.
- Tests: `TestParseQueryFuzzy`, `TestParseQueryFuzzyPhrase`,
  `TestParseQueryFuzzySkipsStemAndSynonym`, `TestQueryMatchesFuzzy`,
  `TestFuzzyWithin`, `TestAutoFuzzyDistance`, `TestQueryScoreFuzzyBestMatch`,
  `TestFuzzyExpanderFailClosed`, `TestFulltextFuzzySearch` (index + seq-scan
  + english `run~` vs `running~` + synonym skip + expansion cap).
  `go build ./...` + `internal/fulltext` / `internal/executor` `go test` +
  `-race` green; `FuzzTokenize` 5 s clean with fuzzy seeds.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — prefix search (2026-08-31)

- **Prefix search.** A trailing ASCII `*` on a SEARCH token is a prefix group
  (query-time only, no catalog or posting-format bump): `cat*` matches indexed
  terms that start with `cat` (`cat`, `catalog`, …); exact `cat` still does
  not match `catalog`. Prefix tokens skip stemming, stop-word filtering, and
  synonym expansion (a truncated word is not a complete token); French elision
  still applies (`l'hom*` is prefix `hom`). Matching terms are a disjunction
  at that position (AND with other groups); phrase slots accept a prefix
  (`"data* performance"`). BM25 scores the best matching term in each prefix
  group. Distinct prefix-matched terms consume the existing query-expansion
  caps (256 terms / 8192 bytes / 4096 work units) and fail closed. A leading
  or infix `*` is not a wildcard (`*cat` is exact `cat`). Default BM25/phrase
  behaviour for unadorned tokens is unchanged. Seq-scan `SEARCH` without an
  index uses the same prefix rules.
- Tests: `TestParseQueryPrefix`, `TestParseQueryPrefixPhrase`,
  `TestParseQueryPrefixSkipsStemAndSynonym`, `TestQueryMatchesPrefix`,
  `TestPrefixExpanderFailClosed`, `TestPostingPrefixBounds`,
  `TestFulltextPrefixSearch` (index + seq-scan + english `run*` vs
  `running*` + expansion cap). `go build ./...` + `internal/fulltext` /
  `internal/executor` `go test` + `-race` green; `FuzzTokenize` 5 s clean
  with prefix seeds.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `USAGE.md`, `CHANGELOG.md`,
  `ROADMAP.md`, `SKILLS.md`, `AGENTS.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — synonym dictionaries (2026-08-31)

- **English synonym dictionary v1.** `CREATE FULLTEXT INDEX … WITH
  (ANALYZER = 'english')` now writes analyzer revision 3: stop-word
  dictionary v1, Porter2, then synonym dictionary v1 (15 tight bidirectional
  groups: `car`/`automobile`, `database`/`db`, `buy`/`purchase`, …). Expansion
  is query-time only — index terms stay 1:1 like english v2 — and alternatives
  share the query token's position so they are a disjunction (AND across
  tokens, OR within a token). Phrase slots accept any alternative, so
  `"red car"` matches `red automobile`. BM25 scores the best alternative in
  each group (no double-count). Extra terms consume the existing query-
  expansion caps (256 terms / 8192 bytes / 4096 work units, max 8 extras per
  token). english v1 (stem only) and v2 (stem+stops) still decode and do not
  expand. Default `simple` is unchanged. Unknown names/revisions fail closed.
- Tests: `TestEnglishSynonymV1Membership`, `TestAnalyzeEnglishNoIndexSynonyms`,
  `TestParseQueryEnglishSynonyms`, `TestParseQueryEnglishSynonymPhrase`,
  `TestQueryMatchesSynonymDisjunction`, `TestEnglishSynonymWorkCounts`,
  `TestLookupAnalyzer` (writes v3), `TestTableEncodeFulltextAnalyzerV9` (v3),
  binder ANALYZER writes v3, `TestFulltextEnglishSynonyms`. `go build ./...` +
  `internal/fulltext` / `internal/catalog` / `internal/sql/parser` /
  `internal/sql/binder` / `internal/sql/optimizer` / `internal/upgrade`
  `go test` + `-race` green; `internal/executor` `TestFulltext*` green +
  `-race`; `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — versioned language analyzers (2026-08-31)

- **French, German, and Spanish analyzers.** `CREATE FULLTEXT INDEX … WITH
  (ANALYZER = 'french' | 'german' | 'spanish')` writes analyzer revision 1 on
  existing `NSCT` v9 (id `2`/`3`/`4`, no format bump): the published Snowball
  3.x stemmer plus that language's Snowball stop-word dictionary v1, applied
  identically at index time and query time. Remaining terms re-pack to
  consecutive positions. French elides `l'` / `qu'` / … before the stop list
  so `l'homme` matches `homme`. Default `simple` and `english` (v1 stem-only,
  v2 stem+stops) are unchanged. Unknown names and unknown catalog revisions
  fail closed. `EXPLAIN` shows `analyzer=french` (etc.).
- Tests: `TestStemFrenchFixtures`, `TestStemGermanFixtures`,
  `TestStemSpanishFixtures`, `TestAnalyzeFrenchStopsThenStems`,
  `TestAnalyzeGermanStopsThenStems`, `TestAnalyzeSpanishStopsThenStems`,
  `TestParseQueryFrenchElision`, `TestFrenchStopV1Membership` (153),
  `TestGermanStopV1Membership` (231), `TestSpanishStopV1Membership` (308),
  `TestTableEncodeFulltextAnalyzerV9` (fr/de/es), binder ANALYZER cases,
  `TestFulltextLanguageAnalyzers`. `go build ./...` + `internal/fulltext` /
  `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/upgrade` / `internal/xport` `go test`
  + `-race` green; `internal/executor` `TestFulltext*` green + `-race`;
  `FuzzTokenize` / `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` / `limits.md` /
  `sql.md`.

### P24 Full-text Search 2.0 — stop-word dictionaries (2026-08-31)

- **English stop-word dictionary v1.** `CREATE FULLTEXT INDEX … WITH
  (ANALYZER = 'english')` now writes analyzer revision 2: stop-word
  dictionary v1 (classic 33-term Lucene EnglishAnalyzer / Snowball-small
  set) is applied before Porter2, identically at index time and query time.
  Remaining terms are re-packed to consecutive positions so BM25 length and
  phrase matching stay aligned (`"the cat sat"` matches `"cat sat"`). Default
  `simple` still has no stop list (`the` is searchable). english v1 (stem
  only) catalogs still decode and search with that pipeline. A SEARCH of only
  stop words returns no rows. Dropped stop words still consume query-expansion
  work units.
- Tests: `TestEnglishStopV1Membership`, `TestAnalyzeEnglishDropsStops`,
  `TestAnalyzeEnglishStopsThenStems`, `TestParseQueryEnglishDropsStops`,
  `TestParseQueryEnglishPhraseDropsStops`, `TestEnglishStopWorkCounts`,
  `TestTableEncodeFulltextAnalyzerV9` (v1 + v2), binder ANALYZER writes v2,
  `TestFulltextEnglishStopWords`. `go build ./...` + `internal/fulltext` /
  `internal/catalog` / `internal/sql/parser` / `internal/sql/binder` /
  `internal/sql/optimizer` / `internal/upgrade` `go test` + `-race` green;
  `internal/executor` `TestFulltext*` green + `-race`; `FuzzTokenize` /
  `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` / `limits.md`.

### P24 Full-text Search 2.0 — stemming (2026-08-31)

- **English stemming + versioned analyzer metadata.** `CREATE FULLTEXT INDEX
  … WITH (ANALYZER = 'simple' | 'english')`. Default `simple` is the Phase 10
  tokenizer (no stemming), so existing BM25/phrase behaviour is unchanged.
  `english` is Snowball English (Porter2) revision 1, applied identically at
  index time and query time. Analyzer id + revision are stored per index on
  `NSCT` v9 (v1–v8 still decode; missing trailer is simple). Unknown analyzer
  names and unknown catalog revisions fail closed.
- **Query-expansion caps.** SEARCH expansion is fail-closed at 256 terms,
  8192 bytes, and 4096 work units (stemming is 1:1; synonym dictionaries will
  reuse the same budget).
- Tests: `TestStemEnglishFixtures`, `TestAnalyzeEnglishStems`,
  `TestParseQueryEnglishPhrase`, `TestQueryExpansionCapsFailClosed`,
  `TestTableEncodeFulltextAnalyzerV9`, `TestPartitionCatalogV5ReadsNextID`
  (NextID is read for every v5+ descriptor, not only the current write
  version), parser/binder ANALYZER cases, `TestFulltextEnglishStemming`.
  `go build ./...` + touched-package `go test` + `-race` green; `FuzzTokenize`
  / `FuzzDecodePartitionedTable` 5 s clean.
- Docs: `docs/fulltext.md`, `docs/sql.md`, `docs/storage-format.md`,
  `docs/ops.md`, `USAGE.md`, `CHANGELOG.md`, `ROADMAP.md`, web `fulltext.md` /
  `limits.md`.

### Current release gate

P16 correctness/SLO closure, **P22 follower reads / read scaling**, and
**P23 Vector Engine 2.0** are all **complete** (P16 paper-closed 2026-08-30;
P22 exit gate closed 2026-08-30 with the linearizability/consistency sign-off
in `docs/ha.md` and the `TestFollowerReadFailoverSessionGuarantee` failover
session-guarantee test; P23 exit gate closed 2026-08-31 with the
production-gating sign-off in `docs/vector.md`). The current release gate is
**P24 Full-text Search 2.0**.

P22 exit gate, all satisfied:

- three read-consistency modes — `STRONG` (linearizable behind a
  `raft.VerifyLeader` quorum read barrier), `BOUNDED` (within `MAX STALENESS`),
  `STALE` (unbounded) — all consistent committed prefixes, `STALE`/`BOUNDED`
  never mislabelled `STRONG`;
- replica lag + follower health via `system.replica_health` and `NodeStatus`;
- follower-read routing in the server and every official driver (Go, Node, Bun,
  Deno, PHP);
- read-scaling benchmark `nextsql-bench --readscale`;
- linearizability/consistency sign-off (`docs/ha.md` "Consistency model and
  sign-off") and failover session-guarantee test.

P16 exit gate, all satisfied:

- corrected 1M-vector HNSW v10: p95 **8.061 ms**, recall@10 **1.000**,
  recall@100 **0.998**;
- 10M DELETE published (**25 ms**), crash-during-merge recovers `Check()`-clean;
- 100M analytics `< 60 s`; 10M INSERT/UPDATE published;
- security gate signed off; no unresolved correctness regressions.

The terminal randomized 100M-operation B+Tree invariant soak is a deferred
standalone measurement, not a release gate (same disposition as P18). Structural
correctness is covered by `TestRandomizedDeleteMerges`, `TestCrashDuringMerge`,
`TestBulkDeleteSoak`, the 10M DELETE run, and the soak at every scale reached
(v8: 44M clean operations).

### P23 Vector Engine 2.0 — complete (2026-08-31)

- **Production-gating sign-off.** Dated review in `docs/vector.md`
  "Production-gating sign-off (Phase 23)": `VECTOR<F16,N>` / `VECTOR<I8,N>` /
  `BITVECTOR<N>` and the quantised HNSW index are production-gated; IVF /
  IVF-PQ / sparse retrieval / dense+sparse+BM25 fusion are production-gated
  ANN paths. Official `--vecquant` 2026-08-31 reference run republished with
  p50/p95/p99, QPS, and resident heap alongside recall@10/@100, index/db size,
  and build time (encryption + WAL + fsync on). `TestPortableProductionPath`
  now covers `internal/float16` / `internal/int8vec` / `internal/bitvec` as
  well as `internal/vector`. Documented follow-ons (not gate items): a
  `BITVECTOR`/Hamming `--vecquant` row, a process-local IVF-PQ cache, a
  re-rank-free quantised HNSW mode, IVF/IVF-PQ/SPARSE on partitioned tables,
  SIMD after profiling. Tests: `go build ./...` + `internal/vector` /
  `internal/float16` / `internal/int8vec` / `internal/bitvec` /
  `internal/bench` / `internal/executor` (vector suites) `go test` + `-race`
  green. Docs: `docs/vector.md`, `docs/ops.md`, `USAGE.md`, `ROADMAP.md`,
  web `vectors.md`.

- **Official `--vecquant` sparse size/latency/recall row.**
  `nextsql-bench --vecquant` measures a `SPARSE` configuration on a
  high-dimension, low-nnz corpus independent of `--vecquant-dim`
  (`SPARSEVECTOR<N>` + `USING SPARSE`; `--vecquant-sparse-dim` default 4096,
  `--vecquant-sparse-nnz` default 24). Reports NSSV raw payload, index-build
  page delta, database size, build time, resident heap, and `NEAREST`
  p50/p95/p99 + recall@10/@100 vs exact-cosine `SparseFlat`. Reference
  (linux/amd64, 12 vCPU, encryption + WAL + fsync on; 2000 × 4096-d nnz=24,
  64 queries): raw payload 282 KiB, index 1.0 MiB, database 2.1 MiB, build
  1.17 s, p50 428 µs, recall@10 **1.000**, recall@100 **1.000**. Tests:
  `TestVectorQuantBench` (8 reports). Docs: `docs/vector.md` "Size / recall
  comparison", `docs/ops.md`, `USAGE.md`, web `vectors.md`.

- **Dense + sparse + BM25 fusion.** A `SELECT` may name two `NEAREST`
  clauses — one dense `VECTOR` column and one `SPARSEVECTOR` column — with
  optional `SEARCH`. The optimizer unions candidates from each retriever
  (HNSW/IVF/IVF-PQ, the sparse inverted index, and BM25) and reciprocal-rank
  fuses the lists (`k = 60`). A document contributes to a channel only when
  that retriever scored it. `EXPLAIN` shows `Rerank bm25+vector+sparse fusion`
  (or `vector+sparse fusion` without `SEARCH`). At most two `NEAREST`
  clauses; the pair must be one dense vector and one sparse vector
  (`BITVECTOR` is rejected). Existing `SEARCH` + single `NEAREST` hybrid
  plans are unchanged. Tests: `TestDenseSparseBM25Fusion` (each single
  channel uniquely owns one row; fused `LIMIT 3` returns all three),
  `TestDenseSparseBM25FusionPlan`, parser (third `NEAREST` rejected) and
  binder (same-column / two-dense rejected) cases. Docs: `docs/vector.md`,
  `docs/optimizer.md`, `docs/sql.md`, `USAGE.md`, web `hybrid.md` /
  `vectors.md` / `sql.md`. The official `--vecquant` sparse size/latency
  row landed in the following increment.

- **`SPARSEVECTOR<N>` SQL surface + `USING SPARSE`.** `SPARSEVECTOR<N>` is a
  distinct top-level type (`N` is the ambient dimension, 1…65535; catalog
  `VecElem = 5`). Runtime values stay sparse (index/value pairs, never widened
  to a dense `float32` array). Dense vector literals such as `(1, 0, 0.5, 0)`
  coerce by dropping zeros. Payload store uses `NSSV` v1. `CREATE VECTOR INDEX
  … USING SPARSE` (no `WITH` options) builds an inverted index over a detached
  encrypted index tree (`sqlSparse` implements `vector.SparseStore`: `NSSM`
  header + one `NSSP` posting list per dimension). Binder: requires a
  `SPARSEVECTOR` column; rejected with `QUANTIZATION`, on dense/`BITVECTOR`
  columns, on partitioned tables, and with `USING HNSW`/`IVF`/`IVFPQ` on a
  sparse column. Default `NEAREST` metric is `COSINE`; `INNER_PRODUCT` is
  accepted; `L2`/`HAMMING` are rejected. Executor: `buildSparseIndex` (CREATE
  + `REBUILD INDEX`), `maintainSparseIndex` on INSERT/UPDATE/DELETE (uses
  in-memory old/new coordinates because DELETE drops the payload first),
  `nearestSparseIndex` = `SearchSparse` with residual over-fetch ×4.
  `EXPLAIN` labels `sparse`; `nextsql export` emits `USING SPARSE`. Wire
  format flag `0x02` carries nnz + `(u32 index, f32 value)` pairs (Go protocol
  via `EncodeScalar`; JS/Node/PHP decode). Tests: `TestSparseVectorIndex`
  (HNSW/IVF/L2 rejected, exact NEAREST, INSERT/UPDATE/DELETE, no `NSSV`/`NSSM`/
  `NSSP` plaintext, restart, `REBUILD INDEX`), parser/binder cases, catalog
  fuzz seed. `go build ./...` + touched-package `go test` + `-race` green;
  `FuzzParse` + `FuzzDecodePartitionedTable` 10 s clean. Docs: `docs/vector.md`,
  `docs/sql.md`, `USAGE.md`, web `vectors.md` / `limits.md` / `sql.md`.
  Dense+sparse+BM25 fusion landed in the increment above; the official
  `--vecquant` sparse size/latency/recall row landed 2026-08-31.

- **Sparse retrieval core.** Portable inverted index over sparse vectors in
  `internal/vector/sparse.go`. A sparse vector is a strictly-ascending list of
  dimension indices plus parallel non-zero `float32` weights (`MaxSparseDim`
  `2^24`, `MaxSparseNNZ` `2^16`). `NewSparseVec` / `CheckSparse` reject zeros,
  duplicates, non-finite values, and out-of-range indices; `SparseDot` is a
  merge-join; `SparseDistance` is `−dot` (`INNER_PRODUCT`) or `1 − cosine`
  (`COSINE`). Retrieval walks one posting list per query coordinate and
  accumulates the exact inner product (`SearchSparse`); `COSINE` re-ranks the
  top `4·k` candidates against full-precision payloads when the store can
  supply them. Versioned encodings: `NSSV` v1 (dimension, nnz, delta-varint
  indices, little-endian `f32` values — overflowing deltas fail closed before
  wrap), `NSSM` v1 21-byte meta (`MaxDim`, metric, count; `COSINE` /
  `INNER_PRODUCT` only), `NSSP` v1 front-coded posting lists (varint count,
  shared-prefix + suffix primary key + `f32` weight; 4096-byte key bound before
  `make`). `SparseStore` + `SparseMem` + `AddSparse` / `RemoveSparse` /
  `PersistSparse` / `LoadSparseMem`; index keys `0x00` meta / `0x01`+`u32`
  posting. Tests: `TestSparseVecRoundTrip` / `TestNewSparseVecRejects` /
  `TestSparseDot` / `TestSparseMetaRoundTrip` / `TestSparseListRoundTrip` /
  `TestSparseSearchRecall` (inner-product inverted-index recall@10 1.0; COSINE
  rerank-all 1.0; COSINE `4·k` ≥ 0.90 on 400×4096-d nnz=24) /
  `TestSparseAddRemove` / `TestSparsePersistLoad` / `TestSparseKeyRoundTrip`;
  `FuzzDecodeSparse` / `FuzzDecodeSparseList` / `FuzzDecodeSparseMeta` (15 / 15
  / 10 s clean). `go build ./...` + `internal/vector` `go test` + `-race` green.
  Docs: `docs/vector.md` ("Sparse retrieval" + storage table), `ROADMAP.md`,
  `USAGE.md`, web `vectors.md`. SQL (`SPARSEVECTOR<N>` + `USING SPARSE`) landed
  in the increment above.

- **IVF-PQ vector index SQL surface + lifecycle wiring.** `CREATE VECTOR INDEX …
  USING IVFPQ WITH (LISTS = n, SUBSPACES = M [, PROBES = m])`. Parser
  (`ast.CreateIndex.IVFSubspaces`; `USING IVFPQ` shares the IVF `WITH` loop and
  adds `SUBSPACES`), binder (`catalog.VecMethodIVFPQ`; `SUBSPACES` required, ≤ 128,
  must divide the vector dimension; `LISTS` required ≤ 65 536, `PROBES` ≤ `LISTS`;
  rejected with `QUANTIZATION`, on a `BITVECTOR` column, and on partitioned
  tables), catalog table descriptor format **v8** (one `SUBSPACES` `u32` per index
  after the v7 method + IVF `LISTS` / `PROBES`; `DecodeTable` accepts v1..v8;
  `internal/upgrade` `FamilyCatalog` window 1..8), and the executor
  (`internal/executor/ivfpqstore.go`: `sqlIVFPQ` implements `vector.IVFPQStore`
  over the detached encrypted index tree — coarse centroids grouped like IVF, the
  codebook split into fixed-size chunks under an `IVPCG` header since it never
  fits one leaf record, one front-coded `NSPL` posting list per centroid;
  `buildIVFPQIndex` trains over a ≤ 50 000-vector heap sample and is shared by
  `CREATE` and `REBUILD INDEX`; `maintainIVFPQIndex` on `INSERT` / `UPDATE` /
  `DELETE`; `nearestIVFPQIndex` probes, ADC-scores, and re-ranks the top
  candidates exactly against the payload store). `EXPLAIN` labels the plan
  `ivfpq`; `nextsql export` emits `USING IVFPQ WITH (…)`. Crash-recovery, backup,
  PITR, and Raft are inherited from the encrypted index-tree WAL path. No
  process-local cached copy yet — a committed `NEAREST` reloads the quantiser per
  query (a documented follow-on, matching plain IVF's first increment). A new
  `F32/ivfpq` row in `nextsql-bench --vecquant` (LISTS / PROBES / SUBSPACES, index
  / db size, build time, `NEAREST` latency, recall@10/@100). Tests:
  `internal/executor` `TestIVFPQVectorIndex` (SUBSPACES required + must divide
  dim, exact-rerank search, INSERT/UPDATE/DELETE maintenance, restart, `REBUILD
  INDEX`, no `NSVV` / `NSPQ` / `NSPC` / `NSPL` / `NSIC` plaintext); parser +
  binder cases; `catalog` v8 round-trip + trailer fix + `FuzzDecodePartitionedTable`
  IVF-PQ seed; `internal/upgrade` window test; `internal/bench`
  `TestVectorQuantBench` (7 reports). `go build ./...` + touched-package `go test`
  + `-race` green; `FuzzDecodePartitionedTable` / `FuzzParse` 15 s clean. Docs:
  `docs/vector.md` ("IVF-PQ (product quantisation)" + storage table + `--vecquant`
  numbers + catalog v8), `docs/sql.md`, `docs/storage-format.md`, `docs/ops.md`,
  `USAGE.md`, `ROADMAP.md`, web `vectors.md` / `limits.md`.

- **IVF-PQ index core.** Portable in-memory inverted-file index with product
  quantisation in `internal/vector` (`ivfpq.go`): `TrainIVFPQ` trains an
  `NLIST`-centroid coarse quantiser then, over the residuals `v − centroid`, an
  `M`-subspace product-quantisation codebook of up to 256 sub-centroids each
  (deterministic k-means). `AddIVFPQ` / `RemoveIVFPQ` store an `M`-byte code per
  vector in its centroid's posting list; `SearchIVFPQ` ranks the coarse
  centroids, scores each probed list with asymmetric distance computation (a
  per-subspace query-to-sub-centroid table summed over the code bytes), and
  re-ranks the top candidates exactly when the store can supply the
  full-precision payloads (recall then tracks an unquantised IVF; ADC-only
  otherwise). `COSINE` (unit-normalised first) and `L2` only; `INNER_PRODUCT`
  rejected. Versioned encodings: `NSPQ` meta (32 bytes), `NSPC` codebook
  (contiguous `f32`), `NSPL` posting list (front-coded primary keys, `NSIL`
  scheme, plus `M` code bytes per entry); every decoder bounds its varints
  before allocating. `IVFPQStore` interface + `IVFPQMem`; `PQCodebook` with
  `EncodePQCodebook` / `DecodePQCodebook`. `internal/vector` `TestIVFPQ*`
  (meta/codebook/list round trips, probe-all + exact re-rank recall@10 ≈ 1.0,
  ADC-only recall@10 ≈ 0.70 on 700×32-d M=8, add/remove, persist+reload,
  determinism) + `FuzzDecodePQList` / `FuzzDecodePQCodebook` /
  `FuzzDecodeIVFPQMeta`. `go build ./...` + `internal/vector` `go test` + `-race`
  + fuzz green. The SQL surface (`CREATE VECTOR INDEX … USING IVFPQ`) and
  executor lifecycle wiring are a following increment. Docs: `docs/vector.md`
  ("IVF-PQ (product quantisation)" + storage table), `ROADMAP.md`, `USAGE.md`,
  web `vectors.md`.

- **Process-local IVF quantiser cache.** A committed `NEAREST` through an IVF
  index no longer reloads and decrypts the centroids, posting lists, and
  full-precision vectors from the index tree on every query: `ivfSearchStore`
  serves a shared in-memory `lockedIVF` copy, built once at commit time
  (`buildIVFIndex` hands its trained `IVFMem` to `s.pendingIVF`) or lazily on
  first search, and installed under the same generation counter and lock as the
  HNSW `lockedMem` cache. It is evicted on any mutation (`maintainIVFIndex` marks
  `s.dirtyIVF`), `REBUILD INDEX`, `DROP INDEX`, table drop/rename, or a
  replicated apply — all of which already funnel through `dropHNSW` /
  `dropAllHNSW`, now extended to the IVF map. A transaction that has modified the
  index still reads its own uncommitted state directly from the index tree.
  `internal/executor` `TestIVFProcessLocalCache`; `TestIVFVectorIndex` /
  `TestIVFCentroidGrouping` unchanged. `go build ./...` + `internal/executor`
  (vector suites) / `internal/vector` / `internal/bench` `go test` + `-race`
  green. Docs: `docs/vector.md` ("IVF index"), `ROADMAP.md`, `USAGE.md`, web
  `vectors.md`.
- **IVF row in `nextsql-bench --vecquant`** plus grouped centroid storage. The
  quantised-vector benchmark now builds an IVF index (`LISTS = 2·√rows`,
  `PROBES = LISTS/4`) over the `F32` column as a sixth configuration and reports
  the same index/db size, build time, `NEAREST` latency, and recall@10/@100 as
  the HNSW rows (reference 2000 × 128-d run: index 112 KiB vs 610–707 KiB for
  HNSW, build 0.25 s vs ~2.1 s, recall@10 0.62 at a 25 %-of-`LISTS` probe ratio
  on synthetic uniform vectors). Surfacing this hit the B+Tree leaf-record
  ceiling — a wide centroid set (many `LISTS`, high dimension) exceeds ~½ a
  logical page — so `sqlIVF.SaveCentroids` / `LoadCentroids` now split the
  centroid set across several `IVFCG`-indexed group records (legacy single-record
  `NSIC` blocks still load). The binder now also rejects `USING IVF` on a
  very-high-dimensional column (one centroid past the leaf-record ceiling, ~`N >
  2000` for `VECTOR<F32,N>`) instead of failing mid-build. `internal/bench/vecquant.go`,
  `TestVectorQuantBench`, `internal/executor` `TestIVFCentroidGrouping`,
  `internal/sql/binder` dimension-guard case; `docs/vector.md` "Size / recall
  comparison" + IVF notes + storage table, `ROADMAP.md`, `USAGE.md`, web
  `vectors.md`.

- **IVF vector index SQL surface.** `CREATE VECTOR INDEX … USING IVF WITH
  (LISTS = n [, PROBES = m])` — parser (`ast.CreateIndex.IVFLists` / `IVFProbes`),
  binder (`LISTS` required and ≤ 65 536, `PROBES` ≤ `LISTS`; rejected with
  `QUANTIZATION`, on a `BITVECTOR` column, or on a partitioned table), and
  catalog table descriptor format **v7** (`Index.VecMethod` byte + IVF
  `LISTS` / `PROBES` `u32` per index; `internal/upgrade` window 1..7). The
  executor binds the IVF store to the index's own detached encrypted B+Tree
  (`sqlIVF` over `vector.IVFStore`): `CREATE` / `REBUILD INDEX` train a coarse
  quantiser over a deterministic ≤ 50 000-vector heap sample and write the
  centroids, front-coded posting lists, and `NSIV` header in one transaction;
  `INSERT` / `UPDATE` / `DELETE` move a row's primary key between posting lists;
  `NEAREST` ranks the centroids, probes the `PROBES` nearest lists, and scores
  their vectors exactly (a differing `USING` metric falls back to exact flat).
  Crash-recovery, backup, PITR, and Raft are inherited from the encrypted
  index-tree WAL path. `EXPLAIN` labels the plan `ivf`; `nextsql export` emits
  `USING IVF WITH (…)`. `internal/executor` `TestIVFVectorIndex`, parser/binder
  cases, `catalog` v7 round-trip + `FuzzDecodePartitionedTable` seed;
  `docs/vector.md` "IVF index", `docs/sql.md`, `docs/storage-format.md`,
  `docs/ops.md`, `USAGE.md`, web `vectors.md`.

- **IVF index core.** Portable in-memory inverted-file coarse-quantiser index in
  `internal/vector`: `TrainIVF` (deterministic k-means++ + Lloyd, unit-normalised
  for `COSINE`), `AddIVF` / `RemoveIVF` (assign to the nearest centroid's posting
  list), `SearchIVF` (rank centroids, probe the `NPROBE` nearest lists, score
  exactly — recall reaches 1.0 when every list is probed), the `IVFStore`
  interface, and `IVFMem`. Versioned on-disk encodings: `NSIV` meta (25 bytes),
  `NSIC` centroid block, `NSIL` front-coded posting list (same shared-prefix +
  suffix scheme as HNSW nodes, bounded before allocation). Real-valued metrics
  only (`COSINE` / `L2` / `INNER_PRODUCT`). Not yet exposed through SQL —
  `CREATE VECTOR INDEX … USING IVF` and the executor build/rebuild/maintenance
  wiring are the next increment. `internal/vector` `TestIVF*`,
  `TestTrainIVFDeterministic`, `FuzzDecodeIVFList` / `FuzzDecodeIVFMeta`;
  `docs/vector.md` "IVF index".

- **Compressed HNSW neighbour lists.** Every HNSW graph node is written in node
  format v2: each layer's neighbour keys are sorted ascending (order is not
  meaningful in the graph) and front-coded — a varint neighbour count, then per
  key a varint shared-prefix length with the previous key plus the differing
  suffix, replacing the fixed 16-bit count and per-key length fields. Row
  primary keys in one table share a column prefix and, in a dense id space,
  several leading bytes, so the on-disk graph shrinks by roughly a third with no
  change to the decoded neighbour set, recall, or latency. v1 (fixed-width) node
  records still decode; `REBUILD INDEX` and ordinary writes re-emit v2. No
  catalog or `NSHM` meta format change. `nextsql-bench --vecquant` index-build
  delta drops accordingly (F32 610 KiB vs 980 KiB). `internal/vector`
  `TestCompressedNeighborLists`, `FuzzDecodeNode` v1/v2 seeds; `docs/vector.md`
  "Compressed neighbour lists".

- **`VECTOR<F16,N>` quantised element type.** Columns declared `VECTOR<F16,N>`
  store each element as an IEEE 754 half (2 bytes) in the detached vector
  payload store, halving that store on disk. The runtime value, distance
  functions, bounded algebra, `NEAREST`, and HNSW stay `float32` — half
  payloads are widened on read. Writes quantise at the boundary
  (round-to-nearest, ties to even) so reads match what is on disk.
  - portable `internal/float16` conversion package (no unsafe/cgo/assembly);
  - `NSVV` payload format v2 (element tag + halves), backward compatible with
    v1 F32 payloads;
  - `CREATE VECTOR INDEX ... USING HNSW` works on `F16` columns unchanged;
  - restart, encryption (`NSVV` never plaintext on disk), dimension and
    finite-value checks, and fuzz coverage all hold.

- **`VECTOR<I8,N>` quantised element type.** Columns declared `VECTOR<I8,N>`
  store each element as a signed byte with a per-vector `float32` scale
  (`absmax(v) / 127`, symmetric), roughly quartering the payload store at high
  dimension. As with `F16` the runtime value and every distance, algebra,
  `NEAREST`, and HNSW path stay `float32` — quantised payloads are widened on
  read. The scale is derived per vector at write time, so there is no
  catalog-side calibration or data scan; a zero vector round-trips exactly.
  - portable `internal/int8vec` conversion package (no unsafe/cgo/assembly);
  - `NSVV` payload format v2 extended with the `I8` element tag (`f32` scale +
    signed bytes); `F32` v1 and `F16` v2 payloads keep decoding unchanged;
  - `CREATE VECTOR INDEX ... USING HNSW` works on `I8` columns unchanged;
  - restart, encryption, dimension / finite-value checks, and fuzz coverage
    (`internal/int8vec` unit + `FuzzRoundTrip`, `internal/vector`
    `FuzzDecodePayload` I8 seed, `TestVectorI8Quantized`) all hold.

- **Quantised-vector benchmark (`nextsql-bench --vecquant`).** Seeds one vector
  set into an `F32`, an `F16`, and an `I8` column, builds an HNSW index over
  each, and reports per-element on-disk width, raw payload size, index-build page
  delta, total database size, build time, resident heap, mean quantisation
  error, and `NEAREST` p50/p95/p99 + recall@10/@100. Recall is scored against an
  exact-cosine flat search over the full-precision source vectors, so the
  `F32`→`F16`/`I8` gap is the quantisation penalty alone. Reference run
  (2000 × 128-d, linux/amd64): database 3.4 → 2.4 → 1.9 MiB as the payload store
  halves then quarters; recall@10 0.916 / 0.916 / 0.914; latency and QPS within
  noise (runtime is `float32` for every element type). `internal/bench/vecquant.go`,
  `TestVectorQuantBench`, `docs/vector.md` "Size / recall comparison". The suite
  also measures an `F32` column with an `F16`- and an `I8`-quantised HNSW graph.

- **Quantised HNSW index (`CREATE VECTOR INDEX … USING HNSW WITH (QUANTIZATION =
  'F16' | 'I8' | 'NONE')`).** The graph keeps a compact quantised copy of every
  vector beside its nodes (new `0x02` key in the index tree, `NSVV` encoding) and
  computes all traversal distances from it; `Search` then re-ranks the `ef`
  candidates against the full-precision column payloads, so the reported order
  and distances are exact and recall tracks an unquantised graph (reference
  2000 × 128-d: recall@10 0.916 for `qh-F16`, 0.912 for `qh-I8`, vs 0.916 `F32`).
  The traversal encoding is independent of the column element type. `NSHM` meta
  format v2 carries the tag (v1 headers decode with no quantisation);
  `types` catalog table format v6 stores one traversal-quantisation byte per
  index. Rows inserted or updated after the build are quantised into the graph on
  write; `REBUILD INDEX` rebuilds the quantised store; the store is encrypted and
  WAL/backup-recovered like every other index structure. This trades a small
  on-disk increase (the quantised copies are additive to the retained full
  payloads) for smaller, more cache-local traversal reads. `docs/vector.md`
  "Quantised HNSW index", `TestQuantizedHNSWIndex`, `internal/vector`
  `TestMetaQuantRoundTrip` / `TestQuantizedHNSWRerank` + `FuzzDecodeMeta` seed.
  - Default is `NONE`; existing `USING HNSW` indexes are unchanged.

- **`BITVECTOR<N>` binary vector type.** A distinct top-level column type (not a
  `VECTOR<...>` element) storing `N` single-bit elements as `ceil(N/8)` packed
  bytes — one thirty-second of `VECTOR<F32,N>`. Elements must each be `0` or `1`
  on write (a real-valued vector is rejected, never rounded); on read each widens
  back to a `float32` `0`/`1` so distance and HNSW math stay `float32`.
  - portable `internal/bitvec` packing package (no unsafe/cgo/assembly; unit +
    `FuzzRoundTrip`);
  - `NSVV` payload format v2 extended with the `BIT` element tag (packed bits,
    LSB-first), still backward compatible with v1 F32 / v2 F16 / v2 I8;
  - new `HAMMING` distance metric (`vector.MetricHamming`, differing-bit count) —
    the default and only metric for a `BITVECTOR` column; `USING HAMMING` is
    rejected on any other vector column and `USING COSINE | L2 | INNER_PRODUCT`
    is rejected on a `BITVECTOR` column;
  - `CREATE VECTOR INDEX … USING HNSW` builds a Hamming graph over a `BITVECTOR`
    column; `WITH (QUANTIZATION = …)` is rejected (the payload is already one bit
    per element);
  - `NEAREST … USING HAMMING`, `KwBitvector` / `KwHamming` lexer keywords,
    `types.VectorBit`, `Type.String()` → `BITVECTOR<N>`;
  - restart, encryption (`NSVV` never plaintext), dimension checks, parser +
    binder cases, and `internal/vector` payload/meta fuzz seeds all covered
    (`TestVectorBitvector`, `TestPayloadBitPacked`, `TestHammingDistance`,
    `TestBindBitvector`). `docs/vector.md` "BITVECTOR<N>".
  - Not yet: IVF / IVF-PQ, compressed HNSW neighbour lists, sparse retrieval.

### Multi-database hosting (track is PARTIAL)

- **Registry storage caps.** The encrypted deployment registry (`NSRM` v3) now
  carries a `StorageCapBytes` on every realm and every database (`0` = no cap).
  - `Registry.SetRealmStorageCap` / `Registry.SetDatabaseStorageCap` apply one
    durable change per encrypted generation; a no-op set does not advance the
    generation.
  - Invariants (enforced on set and revalidated on decode): a non-zero
    per-database cap may not exceed a non-zero realm cap; a realm cap may not be
    lowered below a per-database cap already set in the realm.
  - `NSRM` v1/v2 manifests decode with both caps `0`; the encoder always emits
    v3. Deterministic round-trip and decoder fuzz coverage hold.
  - CLI: `nextsql hosting set-realm-cap`, `nextsql hosting set-database-cap`,
    `nextsql hosting show` (registry root key `KEY-FILE.instance` or
    `--instance-key-file`).
  - **Realm-root delegation.** The admin runs `SetRealmRootAuth(realmID,
    secret)` (CLI `nextsql hosting set-realm-root --secret-file … | --clear`) to
    store `sha256(secret)` on the realm (`RealmRootAuthHash`, `NSRM` v3). A
    realm-root secret holder then sets only that realm's per-database caps via
    `SetDatabaseStorageCapAsRealmRoot` (CLI `set-database-cap
    --realm-secret-file …`) — constant-time secret check, still bounded by the
    realm cap, no path to the realm cap or any other realm; `Forbidden` when not
    delegated, `Unauthorized` on a bad secret.
  - **Write-path enforcement.** `nextsqld` applies `min` non-zero of the realm
    and database cap to the engine page allocator at open
    (`EffectiveStorageCapBytes`, `bytes / PhysicalPageSize`). Once the data
    file's page high-water hits the ceiling, allocating a new page fails with
    `nerr.Exhausted` ("storage cap exceeded") — `INSERT`, row-splitting
    `UPDATE`, index growth — while `DELETE` / `ROLLBACK` / in-place `UPDATE`
    keep working (freelist reuse). Data file only, not WAL/UNDO; not persisted
    (re-derived from the registry each start).
  - Cap changes take the exclusive data-directory lock (`set-realm-cap` /
    `set-database-cap` / `set-realm-root` fail with `Unavailable` against a
    running deployment); a cap edit is an overwrite and takes effect on the
    next restart. Live cap changes without a restart, and advisory
    `system.quotas` surfacing, are follow-ons (`docs/design-multidatabase-dbaas.md`
    §10.1).

### Deferred

- `REBUILD INDEX ... ONLINE`
  - blocking `REBUILD INDEX` is shipped;
  - `ONLINE` remains rejected until concurrent-write correctness is proven.

- partition-wise aggregation and partition-wise joins
  - waits for physical partitioning in P21.

### Planned roadmap

The following phases remain planned/open and are **not** current shipped functionality:

- P19 — WORKFLOW / TRIGGER / SCHEDULE / TASK
- P20 — CDC / Change Streams
- P21 — Native Table Partitioning
- P22 — Follower Reads / Read Scaling
- P23 — Vector Engine 2.0
- P24 — Full-text Search 2.0
- P25 — Security 2.0
- P26 — System Catalog / Introspection 2.0
- P27 — Operational Maturity / Workload Governance
- P28 — Professional Installer + NextSQL Manager
- P29 — Web-based NextSQL Studio
- P30 — NextSQL Intelligence + Built-in RAG

---

## [0.1.0-dev]

### Added

#### Native database foundation

- Native NextSQL storage engine.
- Native NextSQL SQL dialect.
- Native NSQL wire protocol.
- Official driver implementations.
- 16 KiB logical page format.
- Versioned persistent formats.
- Versioned wire formats.
- Explicit page validation and corruption handling.
- Clustered B+Tree primary storage.
- Secondary indexes.
- Range scans.
- Buffer manager.
- Crash-safe persistence.

#### Transactions and durability

- ACID transaction model.
- MVCC version chains.
- READ COMMITTED isolation.
- SNAPSHOT isolation.
- SERIALIZABLE isolation with lock-based semantics.
- Transaction rollback.
- Deadlock detection.
- UNDO integration.
- LSN-based WAL.
- WAL segmentation and rotation.
- Group commit.
- fsync before commit acknowledgement.
- Checkpoints.
- REDO recovery.
- Partial-WAL-tail handling.
- Partial-data-write handling.
- Crash-injection coverage.

#### Encryption and security

- Encryption-by-default production storage model.
- AES-256-GCM authenticated page encryption.
- Encrypted WAL.
- Encrypted UNDO.
- Encrypted backup structures.
- Encrypted vector structures.
- Encrypted full-text structures.
- Encrypted temp/spill domains where applicable.
- Root unlock key kept outside the data volume.
- KEK → database master → domain-specific DEK hierarchy.
- Key rotation support.
- Key revocation support.
- Crypto-shredding support.
- TLS 1.3 requirements for remote production connections.
- Password authentication.
- RBAC.
- Tenant-aware access controls.
- Session auditing.
- Fail-closed handling for malformed or unauthorized operations.

#### SQL engine

- Lexer.
- Parser.
- AST.
- Catalog.
- Binder.
- Logical planner.
- Physical planner.
- Deterministic cost optimizer.
- Vectorized executor.
- Parallel execution.
- Statistics.
- Plan cache.
- `EXPLAIN`.
- `EXPLAIN ANALYZE`.

#### Relational SQL

- `CREATE TABLE`.
- `CREATE INDEX`.
- `CREATE UNIQUE INDEX`.
- `CREATE DATABASE`.
- `ALTER TABLE`.
- `DROP TABLE`.
- `INSERT`.
- `SELECT`.
- `UPDATE`.
- `DELETE`.
- `BEGIN`.
- `COMMIT`.
- `ROLLBACK`.
- `ANALYZE`.
- Foreign keys.
- `RESTRICT`.
- `NO ACTION`.
- `CASCADE`.
- `SET NULL`.
- `SET DEFAULT`.
- Inner joins.
- Left joins.
- Right joins.
- Full outer joins.
- Cross joins.
- Aggregation.
- Grouping.
- Ordering.
- `LIMIT`.
- `OFFSET`.

#### Modern SQL completeness

- `SELECT DISTINCT`.
- `HAVING`.
- searched `CASE`.
- simple `CASE`.
- `UNION`.
- `UNION ALL`.
- `INTERSECT`.
- `EXCEPT`.
- scalar subqueries.
- `IN` / `NOT IN` subqueries.
- `EXISTS` / `NOT EXISTS`.
- correlated subqueries.
- derived tables.
- CTEs.
- recursive CTEs.
- window functions.
- `ROW_NUMBER`.
- `RANK`.
- `DENSE_RANK`.
- `LAG`.
- `LEAD`.
- `FIRST_VALUE`.
- `LAST_VALUE`.
- aggregate window functions.
- UPSERT.
- `INSERT ... RETURNING`.
- `UPDATE ... RETURNING`.
- `DELETE ... RETURNING`.
- covering indexes / `INCLUDE`.
- index-only scans.
- partial indexes.
- expression indexes.
- Top-N optimization.
- improved join reordering.

#### Native JSON

- Native compact binary JSON storage.
- Typed JSON values.
- Object/array/scalar support.
- JSON path traversal.
- Partial decoding.
- JSON-path indexes.
- Transaction integration.
- WAL/recovery integration.
- Encrypted JSON persistence.
- JSON depth and size limits.
- JSON parser fuzzing.

#### Full-text search

- Native inverted index.
- Tokenizer.
- Normalization.
- Posting lists.
- Term/document frequency tracking.
- Positions.
- BM25-style ranking.
- Phrase search.
- `SEARCH column FOR '...'`.
- Transaction integration.
- WAL/recovery integration.
- Encrypted full-text index structures.

#### Vector search

- `VECTOR<F32,N>`.
- Out-of-row vector storage.
- Contiguous vector store.
- COSINE distance.
- L2 distance.
- INNER_PRODUCT.
- Exact flat vector search.
- `NEAREST ... TO`.
- HNSW.
- Encrypted ANN/vector structures.
- Bounded dimensions.
- Parallel distance calculation.

#### Hybrid query planning

- Unified relational + JSON + full-text + vector planning.
- Cost-based structured-filter-first or ANN-first execution.
- Candidate generation.
- Reranking.
- Reciprocal-rank fusion for hybrid result merging.
- `EXPLAIN` visibility into hybrid planning.

#### Geospatial

- `POINT`.
- `LOCATION`.
- `BOX`.
- `LINESTRING`.
- `POLYGON`.
- Coordinate validation.
- WKT coercion.
- `LON`.
- `LAT`.
- `DISTANCE`.
- `DISTANCE_SPHEROID`.
- `DWITHIN`.
- `WITHIN`.
- `COVERS`.
- Line length support.
- Spatial indexes.
- Optimizer integration.
- Exact residual spatial predicates.

#### Schema lifecycle and storage maintenance

- `DROP INDEX` for shipped index types.
- `DROP INDEX IF EXISTS`.
- Blocking `REBUILD INDEX`.
- Crash-safe index rebuild.
- Page reclamation.
- Durable freelist.
- Safe page reuse after restart.
- Orphan detection.
- MVCC-safe garbage eligibility.
- UNDO cleanup.
- Dead-version cleanup.
- B+Tree compaction.
- Full-text tombstone cleanup.
- HNSW tombstone strategy.
- WAL retention respecting PITR.
- `MAINTAIN DATABASE`.
- `MAINTAIN TABLE`.
- `MAINTAIN INDEX`.
- Bounded maintenance coordinator.
- Maintenance CPU budgets.
- Maintenance memory budgets.
- Maintenance I/O budgets.
- One active maintenance pass per database.
- Pause/resume support.
- Admission-aware maintenance.
- Maintenance metrics.
- Automatic statistics refresh policy.
- Bounded automatic maintenance scheduling.

#### Migrations

- Timestamped migration files.
- `migrate validate`.
- `migrate create`.
- `migrate status`.
- `migrate pending`.
- `migrate version`.
- `migrate up`.
- `migrate down`.
- `migrate force`.
- `migrate repair`.
- Transactional migration application.
- Checksum validation.
- Dirty-state detection.
- Dry-run parsing.
- Server-mode migration execution over NSQL.
- `DROP INDEX` migration parsing/validation support.

#### Native protocol and drivers

- TLS-aware NSQL connections.
- Authentication handshake.
- Typed parameters.
- Prepared statements.
- Streaming results.
- Backpressure.
- Cancellation.
- Packet-size limits.
- SQL-length limits.
- Result-size limits.
- Runtime limits.
- Worker limits.
- Memory limits.
- Attacker-controlled length validation.

Official driver surfaces include:

- Go.
- Node.js.
- Bun.
- Deno.
- TypeScript types.
- PHP.

#### Backups and recovery

- Encrypted physical backup.
- Restore.
- Backup verification.
- Restore verification.
- WAL archive integration.
- PITR.
- Restore by LSN.
- Restore by timestamp.
- Logical export.
- Logical import.

#### High availability

- Raft-based HA.
- Minimum 3-voter cluster model.
- Leader election.
- Replicated state/log.
- Synchronous quorum durability.
- Leader failover.
- Replica repair.
- Rolling maintenance support.
- Safe write rejection under quorum loss.
- Split-brain prevention.
- Deterministic follower application.
- Engineering target: leader election under 3 seconds.
- Engineering target: service recovery under 5 seconds.
- Availability target expressed as an SLO, not a zero-downtime guarantee.

#### Operational tooling

- `nextsql` CLI.
- `nextsqld` server.
- `nextsql-bench`.
- `nextsql init`.
- `nextsql exec`.
- `nextsql backup`.
- `nextsql restore`.
- `nextsql verify`.
- `nextsql export`.
- `nextsql import`.
- `nextsql diagnose`.
- `nextsql status`.
- cluster status tooling.
- Official benchmark workloads.
- Admission control.
- Bounded query queues.
- Query cancellation.
- Result limits.
- Operational diagnostics.

#### Packaging

- Linux `.deb` packaging.
- Linux `.run` packaging.
- Linux `.tar.gz` packaging.
- Windows `.zip` packaging.
- Windows installer support.
- Installer build scripts.

### Changed

- Expanded SQL from the original P0–P15 surface through the P18 implementable SQL-completeness scope.
- Expanded schema lifecycle from create-only index behavior to full shipped `DROP INDEX` plus blocking rebuild.
- Added durable storage reclamation and reuse instead of leaving detached pages permanently unreclaimed.
- Added bounded maintenance as a first-class engine responsibility.
- Migration validation now understands shipped `DROP INDEX` behavior.
- Project documentation now separates:
  - final product intent;
  - implementation/status truth;
  - sequencing;
  - agent engineering rules;
  - user/operator documentation.

### Fixed

- Corrected large sequential `DELETE` behavior after the B+Tree leaf-merge issue.
- Preserved B+Tree structural correctness through restart/recovery testing.
- Corrected vector benchmark methodology to use distinct-vector validation and report recall with latency.
- Improved consistency between README, usage documentation, project specification, and engineering-agent documentation.

### Security

- Documented the live-unlocked-host threat-model limitation explicitly.
- Reinforced the rule that keys and passwords must never be carried in connection URLs.
- Kept encryption and durability enabled in official benchmark methodology.
- Reinforced fail-closed behavior for malformed, unauthorized, or unsupported operations.

### Performance

Tracked engineering targets include:

- cached primary-key lookup p50 < 0.5 ms;
- indexed query p95 < 3 ms;
- 25K-row workload < 1 s;
- optimized 1M-row aggregation < 1 s;
- optimized 10M-row aggregation < 5 s;
- 100M analytical workload < 30–60 s;
- 1M HNSW top-10 p95 < 25 ms with recall reported.

Performance figures are hardware/context-specific engineering targets or measurements, not universal guarantees.

### Known limitations

- `0.1.0-dev` remains under measurement.
- P16 is not yet closed.
- `REBUILD INDEX ... ONLINE` is not implemented.
- Partition-wise aggregation/join waits for native physical partitioning.
- P19–P30 are not shipped.
- Multi-primary writes are not part of the current core roadmap.
- Studio, Manager, and Intelligence are not current production surfaces until their roadmap phases complete.

---

## Changelog policy

Use the following categories when recording changes:

```text
Added
Changed
Deprecated
Removed
Fixed
Security
Performance
```

Rules:

1. Record **shipped or verified behavior**, not aspirations.
2. Put active development under `[Unreleased]`.
3. Do not mark roadmap items completed until `TODO.md` says the owning gate is green.
4. Include correctness-impacting fixes even if they are internal.
5. Include persistent-format or wire-format changes prominently.
6. Include security-relevant behavior under `Security`.
7. Include benchmark methodology changes under `Performance`.
8. Do not convert targets into measured claims.
9. Do not describe blocking operations as online.
10. Never make unsupported claims such as:
    - “unhackable”;
    - “100% secure”;
    - “zero downtime guaranteed”;
    - “impossible to lose data”.

---

## Links

- [README.md](README.md) — project overview and quick start
- [USAGE.md](USAGE.md) — current operator/application manual
- [PROJECT.md](PROJECT.md) — intended finished product
- [TODO.md](TODO.md) — current implementation/status truth
- [ROADMAP.md](ROADMAP.md) — simplified, non-authoritative roadmap derived from `TODO.md`
- [SKILLS.md](SKILLS.md) — engineering/agent contract
- [AGENTS.md](AGENTS.md) — repository agent instructions
