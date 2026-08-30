.PHONY: test build router helper mac-app docker-dev docker-production docker-restart-test helper-e2e-test

VERSION ?= $(shell tr -d '[:space:]' < VERSION)
COMMIT ?= $(shell git rev-parse --short=12 HEAD 2>/dev/null || printf unknown)
LDFLAGS = -s -w -X github.com/Dodelidoo-Labs/open-cdx/internal/version.Version=$(VERSION) -X github.com/Dodelidoo-Labs/open-cdx/internal/version.Commit=$(COMMIT)

test:
	go test ./...

build: router helper

router:
	go build -trimpath -ldflags="$(LDFLAGS)" -o bin/routerd ./cmd/routerd

helper:
	CGO_ENABLED=0 go build -trimpath -ldflags="$(LDFLAGS)" -o bin/router-helper ./cmd/router-helper

mac-app:
	./scripts/build-macos-app.sh

docker-dev:
	./scripts/generate-docker-secrets.sh
	docker compose -f docker/compose.dev.yml up --build

docker-production:
	./scripts/generate-docker-secrets.sh
	docker compose --env-file docker/.env -f docker/compose.production.yml up -d --build

docker-restart-test:
	./scripts/test-docker-restart.sh

helper-e2e-test:
	./scripts/test-helper-e2e.sh
