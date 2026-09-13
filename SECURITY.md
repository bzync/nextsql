# Security policy

NextSQL is encrypted by default. It is not “unhackable” and does not claim
absolute security.

A privileged attacker who controls a live, unlocked `nextsqld` process may
read plaintext and active key material in memory, because the database must
decrypt data to execute queries. Encryption protects data at rest. TLS 1.3
protects data in transit off loopback.

## Supported versions

| Version | Status |
|---|---|
| 0.0.1 | Current preview release |

Treat security guarantees as development-stage until you have run the security,
crash, and HA suites on your deployment.

## Reporting a vulnerability

Do not open a public issue for an exploitable vulnerability.

Report privately through [GitHub private vulnerability reporting](https://github.com/bzync/nextsql/security/advisories/new).

A useful report includes:

- affected version or commit
- environment
- reproduction steps
- expected and observed behavior
- impact
- a proof of concept where it is safe to include one
- whether secrets, authentication, or tenant isolation are involved

Do not include real customer secrets or personal data.

## What we protect

- Authentication and authorization (RBAC)
- Root unlock keys and data-encryption keys (never in URLs, never logged)
- Page, WAL, UNDO, backup, and replication encryption (AES-256-GCM)
- TLS 1.3 for any non-loopback listener
- Protocol parsing of untrusted input
- Audit integrity
- NextSQL Admin authorization (Setup, Operations, and Studio)

One deployment serves one database. Physical table partitioning is never an
authorization boundary.

## Cryptography rules

- Use established algorithms only. Never invent a primitive.
- Keys must not appear in connection URLs.
- Secrets must not be logged.
- Encrypted units carry version and key metadata.
- Nonce uniqueness is preserved.
- Wrong or missing keys fail closed.

## Claims we will not make

Do not describe NextSQL as unhackable, 100% secure, guaranteed zero-downtime,
or impossible to lose data. Use threat-model-specific, tested claims.
