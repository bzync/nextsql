# NextSQL Security Policy

## Security Model

NextSQL is an encrypted-by-default database, but it is not “unhackable” and does not claim absolute security.

A privileged attacker controlling a live unlocked `nextsqld` process may access plaintext and active key material in memory because the database must decrypt data to execute queries.

Encryption protects persisted data and TLS protects remote transport.

---

## Supported Development Status

NextSQL is currently `0.1.0-dev`.

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
- tenant isolation;
- root/key handling;
- encryption;
- WAL/backup confidentiality;
- protocol parsing;
- TLS;
- Raft/replication authentication;
- audit integrity;
- Studio/Manager authorization;
- Intelligence/RAG data access.

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

## Tenant Isolation

Cross-tenant data leakage tolerance is zero.

Every new feature must preserve:

- session tenant context;
- RBAC;
- row isolation;
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

## AI / Intelligence Security

NextSQL Intelligence must treat retrieved rows, documents, comments, and external content as data, not trusted instructions.

AI must not:

- broaden permissions;
- bypass tenant policy;
- expose secrets;
- execute destructive operations automatically;
- override parser/binder/optimizer/RBAC/server validation.

---

## Security Claims

Do not describe NextSQL as:

- unhackable;
- 100% secure;
- guaranteed zero downtime;
- impossible to lose data.

Use threat-model-specific, tested claims.
