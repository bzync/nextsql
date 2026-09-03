# Infrastructure Review Checklist

## Compute
- Capacity headroom and overcommit assumptions.
- Restart/replacement behavior.
- Image/OS patch strategy.

## Network
- Public/private address plan.
- Firewall/security groups.
- East-west isolation.
- DNS and TLS lifecycle.
- Load-balancer health checks.

## Storage
- Persistent vs ephemeral classification.
- Backup and restore verified.
- Snapshot consistency.
- Disk monitoring and expansion path.

## Security
- SSH/admin access controlled.
- Root/admin use minimized.
- Secrets managed and rotated.
- Supply-chain/image scanning where appropriate.
- Audit logs retained appropriately.

## Reliability
- Single points of failure explicitly accepted or mitigated.
- RPO/RTO stated for stateful services.
- Dependency failure behavior known.
- Rollback tested or realistically executable.

## Cost
- Expensive always-on resources justified.
- Egress/storage/backup costs considered.
- Scaling policy has upper bounds.
