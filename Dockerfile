# syntax=docker/dockerfile:1.7
FROM golang:1.27.0-bookworm AS build
WORKDIR /src
RUN apt-get update && apt-get install -y --no-install-recommends gcc libc6-dev && rm -rf /var/lib/apt/lists/*
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION=dev
ARG COMMIT=unknown
RUN CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X github.com/opencdx/opencdx/internal/version.Version=${VERSION} -X github.com/opencdx/opencdx/internal/version.Commit=${COMMIT}" -o /out/routerd ./cmd/routerd

FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 65532 opencdx && useradd --uid 65532 --gid 65532 --no-create-home --shell /usr/sbin/nologin opencdx \
    && mkdir -p /var/lib/opencdx && chown 65532:65532 /var/lib/opencdx
COPY --from=build /out/routerd /usr/local/bin/routerd
USER 65532:65532
EXPOSE 8080
VOLUME ["/var/lib/opencdx"]
HEALTHCHECK --interval=15s --timeout=3s --start-period=10s --retries=5 CMD ["curl", "--fail", "--silent", "http://127.0.0.1:8080/readyz"]
ENTRYPOINT ["/usr/local/bin/routerd"]
