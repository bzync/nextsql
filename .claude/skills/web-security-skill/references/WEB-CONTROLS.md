# Web Security Controls

## Input and output

- validate type, format, length, cardinality and business invariants;
- use allowlists where the domain is finite;
- parameterize DB operations;
- contextual escaping/encoding;
- never render user HTML without deliberate sanitization policy;
- reject ambiguous duplicate/security-sensitive parameters when framework behavior is unclear.

## Files

- validate server-side independent of filename/content-type claims;
- randomize storage names;
- store outside executable/static paths when appropriate;
- enforce size/count quotas;
- prevent path traversal;
- malware/content scanning where risk warrants;
- authorization on download, not only upload;
- content-disposition/content-type chosen safely.

## SSRF/outbound network

- allow known schemes/hosts where feasible;
- resolve and validate destinations against private/link-local/metadata ranges;
- control redirects and DNS rebinding risks;
- use egress proxies/network policy for high-risk workloads;
- bound timeout/size;
- never expose internal response content blindly.

## Secrets

Do not place credentials in source, client bundles, public environment variables, URLs or routine logs. Rotate on exposure and audit resulting access.
