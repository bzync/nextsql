# Threat and Risk Register Guidance

Write risks as scenarios, not labels.

Weak:
> DDoS risk

Better:
> An unauthenticated public API can be flooded with resource-expensive requests, exhausting worker/database capacity and preventing paying customers from using the service.

Then connect controls: rate limits, quotas, caching, bounded work, upstream protection, autoscaling/capacity, monitoring and response.

## Prioritization

Consider:

- externally reachable vs internal;
- privileges required;
- exploit maturity/known exploitation;
- blast radius/tenant isolation;
- data sensitivity;
- recoverability;
- business criticality;
- compensating controls.
