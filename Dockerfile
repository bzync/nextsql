FROM golang:1.23-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nextsql ./cmd/nextsql \
 && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/nextsqld ./cmd/nextsqld

FROM debian:bookworm-slim

RUN apt-get update \
 && apt-get install -y --no-install-recommends ca-certificates \
 && rm -rf /var/lib/apt/lists/* \
 && groupadd --system --gid 10001 nextsql \
 && useradd --system --uid 10001 --gid 10001 --home-dir /nonexistent --shell /usr/sbin/nologin nextsql \
 && install -d -o nextsql -g nextsql -m 0700 /var/lib/nextsql /run/secrets /run/bootstrap /run/tls

COPY --from=build /out/nextsql /usr/local/bin/nextsql
COPY --from=build /out/nextsqld /usr/local/bin/nextsqld
COPY docker/entrypoint.sh /usr/local/bin/nextsql-entrypoint
RUN chmod 0755 /usr/local/bin/nextsql-entrypoint \
 && chown root:root /usr/local/bin/nextsql /usr/local/bin/nextsqld /usr/local/bin/nextsql-entrypoint \
 && chmod 0755 /usr/local/bin/nextsql /usr/local/bin/nextsqld

ENV NEXTSQL_DATA_DIR=/var/lib/nextsql \
    NEXTSQL_KEY_FILE=/run/secrets/root.key \
    NEXTSQL_LISTEN=0.0.0.0:7210

VOLUME ["/var/lib/nextsql", "/run/secrets"]
EXPOSE 7210
USER nextsql
ENTRYPOINT ["/usr/local/bin/nextsql-entrypoint"]
