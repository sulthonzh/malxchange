# malxchange development tasks. Everything runs from the repo root;
# compose files live in deploy/ and read deploy/.env (make dev-up seeds
# a throwaway one from deploy/env.example if missing).

COMPOSE      = docker compose -f deploy/docker-compose.yaml
COMPOSE_DEV  = $(COMPOSE) -f deploy/compose.override.yaml
COMPOSE_TEST = $(COMPOSE) -f deploy/compose.test.yaml

.PHONY: help dev-up dev-down dev-env test test-integration test-profile-up test-profile-down lint build-images installer-check

help:
	@echo "dev-up            start postgres+redpanda (loopback ports) for local dev"
	@echo "dev-down          stop dev infra"
	@echo "test              unit tests"
	@echo "test-integration  integration tests against dev infra (boot it first)"
	@echo "test-profile-up   boot the test profile (simnet chains + EVM devnet)"
	@echo "lint              go vet + gofmt check"
	@echo "build-images      build all five service images"
	@echo "installer-check   syntax-check the installer"

deploy/.env:
	@echo "no deploy/.env found — seeding a throwaway dev one (DO NOT use in production)"
	@sed -e 's/replace-me/'"$$(openssl rand -hex 16)"'/g' deploy/env.example > deploy/.env
	@chmod 600 deploy/.env

dev-up: deploy/.env
	$(COMPOSE_DEV) up -d --wait postgres redpanda
	$(COMPOSE_DEV) run --rm redpanda-setup

dev-down:
	$(COMPOSE_DEV) down

test:
	go test ./...

# Integration tests talk to the dev infra over loopback; the superuser
# credential comes from deploy/.env (same one the compose init uses).
test-integration:
	set -a; . deploy/.env; set +a; \
	MAL_TEST_PG_HOST=127.0.0.1 MAL_TEST_PG_SUPERUSER=$${MAL_PG_SUPERUSER} \
	MAL_TEST_PG_SUPERUSER_PASSWORD=$${MAL_PG_SUPERUSER_PASSWORD} \
	MAL_TEST_KAFKA_SEEDS=127.0.0.1:9092 \
	MAL_TEST_KAFKA_USER=$${MAL_KAFKA_USER} MAL_TEST_KAFKA_PASSWORD=$${MAL_KAFKA_PASSWORD} \
	go test -count=1 -timeout 120s -tags integration ./internal/it/...

test-profile-up: deploy/.env
	$(COMPOSE_TEST) --profile test up -d --wait

test-profile-down:
	$(COMPOSE_TEST) --profile test down

lint:
	go vet ./...
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed: $$out"; exit 1; fi

build-images:
	cd deploy && docker compose -f docker-compose.yaml build

installer-check:
	bash -n install/install.sh && echo "installer syntax OK"
