# Secrets and Cryptography

## Secret lifecycle

```text
create -> distribute -> use -> observe access -> rotate -> revoke -> destroy
```

Keep owners, purpose, scope, location and rotation/revocation method.

## Exposure response

When a secret may be exposed:

1. Assume compromise according to risk.
2. Revoke/rotate promptly.
3. Identify access made with the credential.
4. Remove the source of exposure and history/cache copies where possible.
5. Improve prevention/detection.

Deleting the visible string from the latest commit is not sufficient by itself.

## Crypto rule

Use established protocols/libraries and explicit key management. "Encrypted" is not enough: define algorithm/protocol selection via maintained platform defaults, key ownership, rotation, nonce/IV handling where relevant, integrity/authentication, certificate validation and failure behavior.
