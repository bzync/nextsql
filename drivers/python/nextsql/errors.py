"""Error type shared by every NextSQL Python driver call."""

from __future__ import annotations


class NextSQLError(RuntimeError):
    """A NextSQL error: a stable `error_code` string plus a human message.

    `error_code` matches the `nerr.Code` string the server (or the client
    itself, for local protocol/argument errors) sends, e.g. "unavailable",
    "invalid_argument", "forbidden", "not_found". It is stable across
    releases and is the right thing to branch on, not the message text.
    """

    def __init__(self, error_code: str, message: str = "", public_code: str = "") -> None:
        self.error_code = error_code
        # public_code is the stable ERR_* name from docs/error-codes.md. It is
        # set only on an error received over a connection that negotiated the
        # public taxonomy, and is "" otherwise -- for a local error, or an
        # older server. error_code always carries the legacy class, so retry
        # logic keeps reading that and nothing should require public_code.
        self.public_code = public_code
        super().__init__(message or error_code)
