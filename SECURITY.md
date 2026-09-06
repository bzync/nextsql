# NextSQL Security Policy

## Security Model

NextSQL is an encrypted-by-default database, but it is not “unhackable” and does not claim absolute security.

A privileged attacker controlling a live unlocked `nextsqld` process may access plaintext and active key material in memory because the database must decrypt data to execute queries.

Encryption protects persisted data and TLS protects remote transport.

---

## Supported Development Status

NextSQL is currently `0.0.1`.

Security guarantees should be treated as development-stage until the applicable release gates and security suites are green.

---

## Reporting a Vulnerability

Do not publish exploitable security vulnerabilities in a public issue before remediation.

Report privately through the project's designated security contact/channel.

A useful report includes:

- affected version/commit;
- environment;
- reproduction steps;
- expected behavior;
- observed behavior;
- impact;
- proof of concept where safe;
- whether secrets or tenant isolation are involved.

Do not include real customer secrets or personal data.

---

## Security Priorities

Security-sensitive areas include:

- authentication;
- authorization;
- realm/database isolation;
- root/key handling;
- encryption;
- WAL/backup confidentiality;
- protocol parsing;
- TLS;
- Raft/replication authentication;
- audit integrity;
- NextSQL Admin authorization (Setup/Operations/Studio modes).

---

## Cryptography Rules

- use established cryptographic algorithms;
- never invent a custom primitive;
- keys must not be placed in connection URLs;
- secrets must not be logged;
- encrypted units must carry version/key metadata;
- nonce uniqueness must be preserved;
- wrong/missing keys must fail closed.

Current page encryption uses AES-256-GCM.

---

## Realm and Database Isolation

Cross-realm/database data leakage tolerance is zero. Shared row tenancy has
been removed; a connection is bound to one resolved hosted realm/database and
physical table partitioning is never an authorization boundary.

Every new feature must preserve:

- immutable session realm/database context;
- RBAC;
- realm-local authentication and database-scoped authorization;
- authorization checks;
- auditability.

Partitioning is not an authorization boundary.

---

## Protocol Security

Remote production connections require secure TLS configuration.

Protocol implementations must validate:

- packet sizes;
- SQL lengths;
- parameter counts;
- attacker-controlled lengths;
- result bounds;
- cancellation state;
- authentication state.

---

## Security Claims

Do not describe NextSQL as:

- unhackable;
- 100% secure;
- guaranteed zero downtime;
- impossible to lose data.

Use threat-model-specific, tested claims.
