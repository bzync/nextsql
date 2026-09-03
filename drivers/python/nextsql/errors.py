"""Error type shared by every NextSQL Python driver call."""

from __future__ import annotations


class NextSQLError(RuntimeError):
    """A NextSQL error: a stable `error_code` string plus a human message.

    `error_code` matches the `nerr.Code` string the server (or the client
    itself, for local protocol/argument errors) sends, e.g. "unavailable",
    "invalid_argument", "forbidden", "not_found". It is stable across
    releases and is the right thing to branch on, not the message text.
    """

    def __init__(self, error_code: str, message: str = "") -> None:
        self.error_code = error_code
        super().__init__(message or error_code)
