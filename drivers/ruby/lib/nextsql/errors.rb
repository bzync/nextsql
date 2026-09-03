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

    def initialize(error_code, message = "")
      @error_code = error_code
      super(message.empty? ? error_code : message)
    end
  end
end
