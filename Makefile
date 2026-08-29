.PHONY: test build router helper mac-app docker-dev docker-production docker-restart-test helper-e2e-test

test:
	go test ./...

build: router helper

router:
	go build -o bin/routerd ./cmd/routerd

helper:
	CGO_ENABLED=0 go build -o bin/router-helper ./cmd/router-helper

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
