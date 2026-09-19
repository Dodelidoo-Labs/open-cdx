FROM golang:1.27.0-bookworm AS build
WORKDIR /src
COPY . .
RUN go build -o /out/routerd ./cmd/routerd && go build -o /out/seed-preview ./scripts/testing/allowance-preview
FROM debian:bookworm-slim
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/ /usr/local/bin/
COPY scripts/testing/allowance-preview/start.sh /usr/local/bin/start-preview
ENV OPENCODEX_LISTEN=:8080 OPENCODEX_INSECURE_DEV=true OPENCODEX_DATABASE=/preview-data/router.db OPENCODEX_MASTER_KEY_FILE=/preview-data/master_key OPENCODEX_ADMIN_TOKEN_FILE=/preview-data/admin_token OPENCODEX_TIMEZONE=America/Argentina/Buenos_Aires
# Inference and OAuth are intentionally unusable in this synthetic preview.
ENV OPENCODEX_OPENAI_BASE_URL=https://example.invalid/codex OPENCODEX_CHATGPT_BASE_URL=https://example.invalid/backend-api OPENCODEX_AUTH_ISSUER=https://example.invalid/auth
ENTRYPOINT ["sh", "/usr/local/bin/start-preview"]
