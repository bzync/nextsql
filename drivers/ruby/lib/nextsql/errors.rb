# frozen_string_literal: true

module NextSQL
  # An error a stable +error_code+ string plus a human message.
  #
  # +error_code+ matches the +nerr.Code+ string the server (or the client
  # itself, for local protocol/argument errors) sends, e.g. "unavailable",
  # "invalid_argument", "forbidden", "not_found". It is stable across
  # releases and is the right thing to branch on, not the message text.
  class Error < StandardError
    attr_reader :error_code

    # +public_code+ is the stable ERR_* name from docs/error-codes.md. It is
    # set only on an error received over a connection that negotiated the
    # public taxonomy, and is "" otherwise -- for a local error, or an older
    # server. +error_code+ always carries the legacy class, so retry logic
    # keeps reading that and nothing should require +public_code+.
    attr_reader :public_code

    def initialize(error_code, message = "", public_code = "")
      @error_code = error_code
      @public_code = public_code
      super(message.empty? ? error_code : message)
    end
  end
end
