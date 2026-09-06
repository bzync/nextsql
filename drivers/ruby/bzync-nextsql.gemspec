# frozen_string_literal: true

require_relative "lib/nextsql/version"

Gem::Specification.new do |spec|
  spec.name = "bzync-nextsql"
  spec.version = NextSQL::VERSION
  spec.authors = ["Rayan Reynaldo"]
  spec.summary = "Official NextSQL Ruby driver. Speaks the native NSQL v1 protocol."
  spec.description = "Native-protocol client for NextSQL: TLS 1.3, prepared statements, " \
                     "follower-read routing, and NSCE1 client-side field encryption. " \
                     "Keys and passwords are never accepted in a URL."
  spec.homepage = "https://nextsql.bzync.com/docs/drivers"
  spec.license = "MIT"
  spec.required_ruby_version = ">= 3.0"

  spec.metadata = {
    "homepage_uri" => spec.homepage,
    "source_code_uri" => "https://github.com/bzync/nextsql/tree/master/drivers/ruby",
    "bug_tracker_uri" => "https://github.com/bzync/nextsql/issues",
    "changelog_uri" => "https://github.com/bzync/nextsql/blob/master/CHANGELOG.md",
    "rubygems_mfa_required" => "true"
  }

  spec.files = Dir["lib/**/*.rb"] + ["LICENSE", "README.md"]
  spec.require_paths = ["lib"]

  # Pure stdlib (socket, openssl, securerandom) — no runtime dependencies.
end
