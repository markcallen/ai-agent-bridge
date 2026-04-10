.PHONY: build proto test test-e2e test-cover test-cover-maintained lint clean certs dev-certs dev-setup agents-setup setup-hosts fmt run dev-run docker-run smoke up down logs up-local down-local logs-local chat-example chat-claude chat-opencode chat-codex chat-gemini chat-ts-example chat-ts-claude chat-ts-opencode chat-ts-codex chat-ts-gemini chat-web-install chat-web-dev chat-web-build chat-web-start chat-web-docker-dev chat-web-docker-start

BIN_DIR := bin
BRIDGE := $(BIN_DIR)/bridge
BRIDGE_CA := $(BIN_DIR)/bridge-ca
CONFIG ?= config/bridge.yaml
DEV_CONFIG ?= config/bridge-dev.yaml
CHAT_TARGET ?= bridge.local:9445
CHAT_PROVIDER ?= claude
CHAT_PROJECT ?= dev
CHAT_REPO ?= /repos/penduin
CHAT_JWT_KEY ?= ../../certs/jwt-signing.key
build: proto
	@mkdir -p $(BIN_DIR)
	go build -o $(BRIDGE) ./cmd/bridge
	go build -o $(BRIDGE_CA) ./cmd/bridge-ca

proto:
	protoc \
		--proto_path=proto \
		--proto_path=/home/linuxbrew/.linuxbrew/include \
		--go_out=gen --go_opt=paths=source_relative \
		--go-grpc_out=gen --go-grpc_opt=paths=source_relative \
		bridge/v1/bridge.proto

test:
	./scripts/test-go.sh

E2E_ONLY ?=

test-e2e:
	@set +e; \
	E2E_ONLY=$(E2E_ONLY) docker compose -f e2e/docker-compose.yml up --build --abort-on-container-exit --exit-code-from test-client; \
	rc=$$?; \
	docker compose -f e2e/docker-compose.yml down -v; \
	exit $$rc

test-cover:
	./scripts/test-go-coverage.sh
	go tool cover -html=coverage.out -o coverage.html

test-cover-maintained:
	./scripts/check-go-coverage.sh

lint:
	./scripts/lint-go.sh

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

certs: build
	$(BRIDGE_CA) init --name ai-agent-bridge --out certs/

dev-certs: build
	./scripts/dev_certs.sh

dev-setup: dev-certs agents-setup setup-hosts
	@echo "Dev environment ready. Certs in certs/"

setup-hosts:
	./scripts/setup_hosts.sh

agents-setup:
	./scripts/setup_ai_agents.sh

fmt:
	gofmt -s -w .
	goimports -w .

run: build
	$(BRIDGE) --config $(CONFIG)

dev-run: dev-setup
	./scripts/with_env_secrets.sh $(BRIDGE) --config $(DEV_CONFIG)

docker-run:
	./scripts/with_env_secrets.sh docker compose up --build bridge

smoke:
	./scripts/with_env_secrets.sh ./scripts/smoke.sh

up:
	./scripts/with_env_secrets.sh docker compose up --build

down:
	docker compose down

logs:
	docker compose logs -f

up-local:
	docker compose -f docker-compose.yml -f docker-compose.local.yaml up --build --watch

down-local:
	docker compose -f docker-compose.yml -f docker-compose.local.yaml down

logs-local:
	docker compose -f docker-compose.yml -f docker-compose.local.yaml logs -f

chat-example:
	./scripts/with_env_secrets.sh go run ./examples/chat \
		-target $(CHAT_TARGET) \
		-provider $(CHAT_PROVIDER) \
		-project $(CHAT_PROJECT) \
		-cacert certs/ca-bundle.crt \
		-cert certs/dev-client.crt \
		-key certs/dev-client.key \
		-jwt-key certs/jwt-signing.key \
		-jwt-issuer dev \
		-timeout 5m \
		$(CHAT_REPO)

chat-claude: CHAT_PROVIDER=claude
chat-claude: chat-example

chat-opencode: CHAT_PROVIDER=opencode
chat-opencode: chat-example

chat-codex: CHAT_PROVIDER=codex
chat-codex: chat-example

chat-gemini: CHAT_PROVIDER=gemini
chat-gemini: chat-example

chat-ts-example:
	cd examples/chat-ts && \
	../../scripts/with_env_secrets.sh npx tsx src/index.ts \
		--target $(CHAT_TARGET) \
		--provider $(CHAT_PROVIDER) \
		--project $(CHAT_PROJECT) \
		--cacert ../../certs/ca-bundle.crt \
		--cert ../../certs/dev-client.crt \
		--key ../../certs/dev-client.key \
		--jwt-key $(CHAT_JWT_KEY) \
		$(CHAT_REPO)

chat-ts-claude: CHAT_PROVIDER=claude
chat-ts-claude: chat-ts-example

chat-ts-opencode: CHAT_PROVIDER=opencode
chat-ts-opencode: chat-ts-example

chat-ts-codex: CHAT_PROVIDER=codex
chat-ts-codex: chat-ts-example

chat-ts-gemini: CHAT_PROVIDER=gemini
chat-ts-gemini: chat-ts-example

chat-web-install:
	cd packages/bridge-client-node && npm run build
	cd examples/chat-web && pnpm install

chat-web-dev: chat-web-install
	cd examples/chat-web && pnpm dev

chat-web-build: chat-web-install
	cd examples/chat-web && pnpm build

chat-web-start: chat-web-build
	cd examples/chat-web && pnpm start

chat-web-docker-dev:
	docker compose -f docker-compose.yml -f docker-compose.local.yaml up --build --watch

chat-web-docker-start:
	docker compose up --build chat-web
