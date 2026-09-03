# Test Types and When to Use Them

## Property-based tests
Useful for parsers, serialization, calculations, permission invariants and domains with broad input spaces.

## Mutation testing
Useful to assess whether tests actually detect changes in critical pure/domain logic. Apply selectively due to cost.

## Fuzzing
Useful at untrusted parsing/protocol/file boundaries. Keep it defensive, bounded and authorized.

## Load/soak
Use when capacity/latency/resource exhaustion materially affects the product. Model realistic workload shapes rather than only maximum requests per second.

## Resilience/failure injection
Test timeouts, retries, partial failures, queue duplication, dependency outages and restore/failover where architecture promises resilience.

## Accessibility
Automated tools catch only part of accessibility problems. Combine tooling with keyboard/focus/screen-reader-oriented manual checks for critical flows.
