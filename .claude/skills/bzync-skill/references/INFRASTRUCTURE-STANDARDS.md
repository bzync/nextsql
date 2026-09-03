# Infrastructure Engineering Standards

## Runtime discovery first

Inspect the actual runtime path before changing it. Do not assume Docker, Podman, containerd, Kubernetes, Proxmox or a cloud provider merely because one appears in development tooling.

## Network path

For externally reachable services be able to describe:

```text
client -> DNS -> edge/LB -> proxy/ingress -> workload -> dependency
```

Define TLS termination, firewall/trust boundaries, internal exposure, source-IP behavior and administrative access.

## Production baseline

Where applicable use:

- private-by-default services;
- least-privilege identities;
- secrets outside source control;
- reproducible builds/images;
- readiness/health checks;
- explicit resource/capacity planning;
- safe rollout and rollback;
- actionable logs/metrics;
- tested backup and restore for stateful systems.

## Stateful systems

Define:

- durable data path;
- replication semantics;
- RPO/RTO;
- backup retention;
- restore procedure;
- credential rotation;
- upgrade path;
- disk-full/degraded behavior.

## Infrastructure changes

Do not expose new public ports, weaken TLS/auth, or remove backup/recovery behavior as a convenience fix without explicit requirement and risk acknowledgement.
