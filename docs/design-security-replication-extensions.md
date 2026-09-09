# Security and multi-primary extensions

Status: **Client encryption v2 and External IdP extensions implemented and verified; Multi-primary writes remain an un-shipped, single-leader Raft consensus boundary**.

## Client encryption v2

Implemented and verified: HKDF-SHA256-domain-separated RFC 5297 AES-SIV deterministic field-level client encryption (`NSCE2.`) across Go, Node/TypeScript, Bun, and PHP drivers. `NSCE1.` remains the default randomized AES-256-GCM envelope. Deterministic equality is explicit in DDL (`ENCRYPTED CLIENT DETERMINISTIC`), exposed with its leakage characteristics in `system.capabilities`, and strictly forbidden for range, full-text, vector, ordering, or server-side expressions. Downgrade rejection, envelope fuzzing, key rotation, PITR, and HA/failover tests pass. General searchable encryption is a separate mode and is not supported.

## External IdP extensions

Implemented and verified:
1. **RFC 7662 Opaque Token Introspection**: `internal/authbroker/introspect.go` queries operator-configured introspection endpoints using HTTP POST with bounded timeouts (3s default), 64 KiB body cap, disabled redirects (`http.ErrUseLastResponse`), bounded LRU caching (1024 entries), mode-0600 client-secret files, and strict active/sub/exp/aud/iss claim validation.
2. **JIT Principal Provisioning**: `internal/authbroker/jit.go` supports opt-in provisioning with strict role boundary enforcement (`allowed_role_boundary`), absolute prohibition of administrative role escalation (`admin`, `cluster_admin`, `superuser`, `operator`), and principal count ceilings (`max_principals`).
3. **OCSP Certificate Status Checking**: `internal/security/ocsp.go` verifies client certificate status against stapled OCSP responses or live AIA responders with in-memory caching, timeout bounds, and fail-closed enforcement under `--tls-ocsp-mode=enforce`. Redacted OCSP posture is reported via `system.tls.ocsp_mode`.

## Multi-primary writes

Multi-primary writes remain outside current release scope. NextSQL strictly enforces single-leader Raft consensus across clustered deployments and a single-database, one-deployment architecture. Multi-primary writes will not be introduced without versioned conflict ordering, multi-master anti-entropy, and comprehensive partition/split-brain verification. All deployments remain single-leader.
