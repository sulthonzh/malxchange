# malxchange development tasks. Everything runs from the repo root;
# compose files live in deploy/ and read deploy/.env (make dev-up seeds
# a throwaway one from deploy/env.example if missing).

COMPOSE      = docker compose -f deploy/docker-compose.yaml
COMPOSE_DEV  = $(COMPOSE) -f deploy/compose.override.yaml
COMPOSE_TEST = $(COMPOSE) -f deploy/compose.test.yaml

.PHONY: help dev-up dev-down dev-env test test-integration test-chain-e2e member-e2e test-profile-up test-profile-down test-profile-probe lint build-images installer-check hooks

help:
	@echo "dev-up            start postgres+redpanda (loopback ports) for local dev"
	@echo "dev-down          stop dev infra"
	@echo "hooks             install the shared git hooks (main-checkout commit guard)"
	@echo "test              unit tests"
	@echo "test-integration  integration tests against dev infra (boot it first)"
	@echo "test-chain-e2e    chain E2E against the test profile (boot it first)"
	@echo "member-e2e        Playwright member journeys through the Caddy edge (issue #51)"
	@echo "test-profile-up   boot the test profile (simnet chains + EVM devnet + tronstub)"
	@echo "test-profile-down stop the test profile"
	@echo "test-profile-probe health-check the chain nodes of the test profile"
	@echo "lint              go vet + gofmt + env-doc drift + checks"
	@echo "build-images      build all service images"
	@echo "installer-check   syntax-check the installer"

test-chain-e2e:
	@set -a; . deploy/.env; set +a; \
	MAL_TEST_BTCD_USER=$${MAL_BTCD_RPCUSER:-malxchange} MAL_TEST_BTCD_PASS=$${MAL_BTCD_RPCPASSWORD} \
	MAL_TEST_LTCD_USER=$${MAL_LTCD_RPCUSER:-malxchange} MAL_TEST_LTCD_PASS=$${MAL_LTCD_RPCPASSWORD} \
	go test -count=1 -timeout 300s -tags chain_e2e ./internal/wallet/chain/...

# Member E2E (spec #24, issue #51): boots the test+e2e profiles (simnet
# chains, mail sink, web container, Caddy edge) and runs the Playwright
# suites in apps/web. The browser reaches the app through the edge at
# https://exchange.example.com (mapped inside Chromium), so api and
# WebSocket are same-origin as in production.
member-e2e:
	@set -a; . deploy/.env; set +a; \
	$(COMPOSE_TEST) --profile test --profile e2e up -d --build; \
	cd apps/web && npx playwright test

# Health-check the test-profile chain nodes: each must answer its
# canonical "where am I" RPC. Run before debugging anything chain-side —
# a node that latched (see deploy/README "Chain test-profile gotchas")
# answers garbage until recreated.
test-profile-probe:
	@set -a; . deploy/.env; set +a; \
	echo "btcd:" && curl -fsSk -H 'Content-Type: application/json' -u "$${MAL_BTCD_RPCUSER:-malxchange}:$$MAL_BTCD_RPCPASSWORD" --data-binary '{"jsonrpc":"1.0","id":"probe","method":"getblockcount","params":[]}' https://127.0.0.1:18556 && \
	echo "ltcd:" && curl -fsSk -H 'Content-Type: application/json' -u "$${MAL_LTCD_RPCUSER:-malxchange}:$$MAL_LTCD_RPCPASSWORD" --data-binary '{"jsonrpc":"1.0","id":"probe","method":"getblockcount","params":[]}' https://127.0.0.1:19556 && \
	echo "anvil:" && curl -fsS -H 'Content-Type: application/json' --data-binary '{"jsonrpc":"2.0","id":"probe","method":"eth_blockNumber","params":[]}' http://127.0.0.1:8545 && \
	echo "all chain nodes answering"

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
	go test -count=1 -timeout 240s -tags integration -p 1 ./internal/...

test-profile-up: deploy/.env
	$(COMPOSE_TEST) --profile test up -d --wait

test-profile-down:
	$(COMPOSE_TEST) --profile test down

lint:
	go vet ./...
	@out=$$(gofmt -l cmd internal); if [ -n "$$out" ]; then echo "gofmt needed: $$out"; exit 1; fi
	@hits=$$(grep -rnF 'Status()[' --include='*.go' cmd internal); if [ -n "$$hits" ]; then \
		echo "positional Status() index — map order is random; select by market (docs/agents/review-checklist.md):"; echo "$$hits"; exit 1; fi
	@{ grep -rhoE '\$$\{MAL_[A-Z0-9_]+' deploy/*.yaml 2>/dev/null | sed 's/\$${//'; \
	   grep -rhoE '(os\.Getenv|EnvDefault)\("MAL_[A-Z0-9_]+' --include='*.go' cmd internal | grep -oE 'MAL_[A-Z0-9_]+'; } \
	  | grep -vE 'MAL_(TEST_|PG_|KAFKA_|HTTP_|UNSET_)' | sort -u > /tmp/mal_wanted.$$$$; \
	 grep -hoE '^MAL_[A-Z0-9_]+' deploy/env.example | sort -u > /tmp/mal_have.$$$$; \
	 missing=$$(comm -23 /tmp/mal_wanted.$$$$ /tmp/mal_have.$$$$); rm -f /tmp/mal_wanted.$$$$ /tmp/mal_have.$$$$; \
	 if [ -n "$$missing" ]; then echo "operator-tunable MAL_* vars absent from deploy/env.example:"; echo "$$missing"; exit 1; fi

build-images:
	cd deploy && docker compose -f docker-compose.yaml build

installer-check:
	bash -n install/install.sh && echo "installer syntax OK"

# Retro (#23 session): agents commit in their own worktrees; the main
# checkout hosts the shared spec-* integration branches. The pre-commit
# hook blocks stray commits there (MAL_ALLOW_MAIN_COMMIT=1 overrides;
# ff-only merges create no commit and pass untouched).
hooks:
	@set -e; hook="$$(git rev-parse --git-common-dir)/hooks/pre-commit"; \
	printf '%s\n' \
	  '#!/bin/sh' \
	  '# Installed by `make hooks`. Agents commit in their own worktrees;' \
	  '# the main checkout hosts the shared spec-* integration branches.' \
	  '# Stray commits there pollute every sibling worktree that merges the tip.' \
	  'branch=$$(git branch --show-current)' \
	  'case "$$branch" in' \
	  'spec-*)' \
	  '  if [ "$$(git rev-parse --git-dir)" = "$$(git rev-parse --git-common-dir)" ] && [ -z "$${MAL_ALLOW_MAIN_COMMIT:-}" ]; then' \
	  '    echo "main checkout on $$branch: commit in a worktree, or set MAL_ALLOW_MAIN_COMMIT=1" >&2' \
	  '    exit 1' \
	  '  fi' \
	  '  ;;' \
	  'esac' >"$$hook"; \
	chmod +x "$$hook"; \
	echo "installed $$hook"
