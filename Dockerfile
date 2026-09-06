# syntax=docker/dockerfile:1

# Run the compiler on the build host's native architecture and cross-compile to
# the target — CGO is disabled, so this is a pure Go cross-build and avoids
# emulating the whole toolchain under QEMU for non-native target platforms.
#
# The build base is pinned by digest (the tag in the comment is the
# human-readable equivalent); Dependabot's docker ecosystem bumps both together.
FROM --platform=$BUILDPLATFORM golang:1.27-bookworm@sha256:648f440f42a0958804efb24df176f806f9d353b41f1c0627f666428e40310f6b AS build

# GOTOOLCHAIN=local: never silently download a toolchain other than the one
# baked into the pinned image. CGO_ENABLED=0: fully static binaries.
ENV GOTOOLCHAIN=local \
    CGO_ENABLED=0

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download
COPY . .
ARG TARGETOS TARGETARCH
# -trimpath: no host paths in the binary. -buildid=: drop the last
# nondeterministic ldflags field. -buildvcs=false: .git is not in the context.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    set -eux; \
    for cmd in nextsql nextsqld nextsql-entrypoint; do \
      GOOS="$TARGETOS" GOARCH="$TARGETARCH" \
        go build -trimpath -buildvcs=false -ldflags='-s -w -buildid=' \
          -o "/out/$cmd" "./cmd/$cmd"; \
    done

# Named user, CA bundle, and writable directories for the scratch runtime.
# The engine is a static Go binary; a Debian (or Alpine) userland is unused
# attack surface that image scanners score as Critical/High CVEs the process
# cannot load.
RUN printf '%s\n' \
      'root:x:0:0:root:/root:/sbin/nologin' \
      'nextsql:x:10001:10001:NextSQL:/nonexistent:/sbin/nologin' \
      'nobody:x:65534:65534:nobody:/nonexistent:/sbin/nologin' \
      > /out/passwd \
 && printf '%s\n' \
      'root:x:0:' \
      'nextsql:x:10001:' \
      'nobody:x:65534:' \
      > /out/group \
 && install -d -m 0755 /out/empty \
 && touch /out/empty/.keep \
 && cp /etc/ssl/certs/ca-certificates.crt /out/ca-certificates.crt

FROM scratch

COPY --from=build /out/passwd /etc/passwd
COPY --from=build /out/group /etc/group
COPY --from=build /out/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build --chown=10001:10001 /out/empty/. /tmp/
COPY --from=build --chown=10001:10001 /out/empty/. /var/lib/nextsql/
COPY --from=build --chown=10001:10001 /out/empty/. /run/secrets/
COPY --from=build --chown=10001:10001 /out/empty/. /run/bootstrap/
COPY --from=build --chown=10001:10001 /out/empty/. /run/tls/
COPY --from=build --chown=10001:10001 /out/empty/. /seed/
COPY --from=build /out/nextsql /usr/local/bin/nextsql
COPY --from=build /out/nextsqld /usr/local/bin/nextsqld
COPY --from=build /out/nextsql-entrypoint /usr/local/bin/nextsql-entrypoint

ENV NEXTSQL_DATA_DIR=/var/lib/nextsql \
    NEXTSQL_KEY_FILE=/run/secrets/root.key \
    NEXTSQL_LISTEN=0.0.0.0:7210

VOLUME ["/var/lib/nextsql", "/run/secrets", "/seed"]
EXPOSE 7210
USER 10001:10001
STOPSIGNAL SIGTERM
ENTRYPOINT ["/usr/local/bin/nextsql-entrypoint"]

# Static OCI metadata; the publish workflow's metadata-action injects the
# per-build version/revision/created labels on top.
LABEL org.opencontainers.image.title="NextSQL" \
      org.opencontainers.image.description="Native, encrypted-by-default multimodel database engine" \
      org.opencontainers.image.source="https://github.com/bzync/nextsql" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.vendor="Bzync Software Development Services" \
      org.opencontainers.image.base.name="scratch"
