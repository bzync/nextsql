# Public error-code taxonomy

NextSQL reserves uppercase `ERR_*` names as its stable public error taxonomy.
Do not match error messages.

An error frame carries **both** spellings. The `code` field always carries the
legacy lowercase internal class and is never replaced in place, because
existing clients branch their retry logic on it. The stable `ERR_*` name is an
additional optional field, sent only to a client that negotiated the
`public error codes` capability in its `Hello` flags (`docs/protocol.md`,
"Capability negotiation"). A client that did not ask sees the byte-identical
NSQL v1 error frame.

Every official driver — Go, Node, Bun, PHP, Python, Ruby — requests the
capability and exposes the name alongside the legacy class:

| Driver | Legacy class | Stable public name |
| --- | --- | --- |
| Go | `nerr.Error.Code` | `nerr.Error.Public` |
| Node / Bun | `err.code` | `err.publicCode` |
| PHP | `$e->errorCode` | `$e->publicCode` |
| Python | `err.error_code` | `err.public_code` |
| Ruby | `err.error_code` | `err.public_code` |

The public field is empty when the peer is an older server that ignored the
request, and for an error the client raised locally. Nothing should require it
to be present.

| Internal class | Reserved public code |
| --- | --- |
| `invalid_argument` | `ERR_INVALID_ARGUMENT` |
| `invalid_format` | `ERR_INVALID_FORMAT` |
| `corruption` | `ERR_CORRUPTION` |
| `not_found` | `ERR_NOT_FOUND` |
| `already_exists` | `ERR_ALREADY_EXISTS` |
| `page_full` | `ERR_PAGE_FULL` |
| `exhausted` | `ERR_EXHAUSTED` |
| `crypto` | `ERR_CRYPTO` |
| `io` | `ERR_IO` |
| `internal` | `ERR_INTERNAL` |
| `unavailable` | `ERR_UNAVAILABLE` |
| `conflict` | `ERR_CONFLICT` |
| `deadlock` | `ERR_DEADLOCK` |
| `serialization` | `ERR_SERIALIZATION` |
| `syntax` | `ERR_SYNTAX` |
| `unauthorized` | `ERR_UNAUTHORIZED` |
| `forbidden` | `ERR_FORBIDDEN` |
| `protocol` | `ERR_PROTOCOL` |
| `canceled` | `ERR_CANCELED` |
| `foreign_key` | `ERR_FOREIGN_KEY` |

Only documented codes are stable. Clients must treat an unknown `ERR_*` code
as non-retryable unless a later client release defines it. The server emits the
public field only for a class in the table above: an internal class with no
reserved public spelling is sent with its legacy class alone, rather than given
an invented name that would look stable.
