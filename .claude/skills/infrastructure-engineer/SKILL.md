---
name: infrastructure-engineer
description: Design, implement, and review production infrastructure across Linux, containers, Kubernetes, VMs, Proxmox, networking, load balancing, DNS/TLS, storage, databases, IAM, secrets, CI/CD, observability, backup/restore, capacity, HA, and disaster recovery. Use for infrastructure changes, deployment design, runtime debugging, networking, security hardening, or platform engineering.
---

# Infrastructure Engineer

Treat infrastructure as a production system with failure modes, lifecycle, cost and security boundaries.

## Inspect first

Find existing:

- Dockerfiles/Containerfiles;
- Compose/Kubernetes/Helm/Kustomize manifests;
- Terraform/OpenTofu/Ansible/Pulumi;
- Proxmox/VM provisioning;
- CI/CD workflows;
- ingress/load balancers;
- DNS/TLS configuration;
- secrets/config patterns;
- backup scripts;
- monitoring/logging.

Do not design a parallel deployment model without need.

## Production principles

- private-by-default networking;
- least-privilege IAM and service accounts;
- immutable/reproducible artifacts;
- secrets outside source control;
- explicit resource requests/limits where meaningful;
- health/readiness checks tied to actual dependency behavior;
- rolling/blue-green/canary strategies chosen according to failure risk;
- tested backups and restore;
- observable infrastructure;
- documented failure and rollback paths.

## Networking

For each exposed service define:

```text
client -> DNS -> edge/LB -> ingress/proxy -> workload -> dependency
```

Specify trust boundary, TLS termination, source IP behavior, firewall policy and internal exposure. Avoid exposing databases/admin ports publicly unless there is an explicit controlled requirement.

## Stateful systems

For databases/object storage/queues define:

- data path and durability;
- replication/HA semantics;
- backup frequency and retention;
- restore procedure and RPO/RTO;
- upgrade strategy;
- disk-full behavior;
- encryption and credential rotation.

## Kubernetes

Do not confuse Podman/Docker developer tooling with the Kubernetes node runtime. Validate the runtime actually used by the cluster. For workload design consider namespaces, RBAC, network policies, PDBs, probes, storage classes, ingress, autoscaling and disruption behavior.

## Operations

Every critical service should have enough telemetry to answer:

- Is it available?
- Is it slow?
- Is it failing?
- Is it saturated?
- What changed?

See `references/INFRA-REVIEW.md`.
