.PHONY: build proto test test-e2e test-cover lint clean certs dev-certs dev-setup fmt run dev-run chat-example runprompt-example

BIN_DIR := bin
BRIDGE := $(BIN_DIR)/bridge
BRIDGE_CA := $(BIN_DIR)/bridge-ca
CONFIG ?= config/bridge.yaml
DEV_CONFIG ?= config/bridge-dev.yaml
CHAT_TARGET ?= 127.0.0.1:9445
CHAT_PROVIDER ?= claude-chat
CHAT_PROJECT ?= dev
CHAT_REPO ?= $(PWD)
RUNPROMPT_TARGET ?= 127.0.0.1:9445
RUNPROMPT_PROJECT ?= dev
RUNPROMPT_AGENT ?= claude-chat
RUNPROMPT_DIR ?= $(PWD)
RUNPROMPT_PROMPT ?=

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
	go test -race -count=1 ./...

test-e2e:
	@set +e; \
	docker compose -f e2e/docker-compose.yml up --build --abort-on-container-exit --exit-code-from test-client; \
	rc=$$?; \
	docker compose -f e2e/docker-compose.yml down -v; \
	exit $$rc

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

certs: build
	$(BRIDGE_CA) init --name ai-agent-bridge --out certs/

dev-certs: build
	./scripts/dev_certs.sh

dev-setup: dev-certs
	@echo "Dev environment ready. Certs in certs/"

fmt:
	gofmt -s -w .
	goimports -w .

run: build
	$(BRIDGE) --config $(CONFIG)

dev-run: dev-setup
	$(BRIDGE) --config $(DEV_CONFIG)

chat-example:
	go run ./examples/chat \
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

runprompt-example:
	@if [ -z "$(RUNPROMPT_PROMPT)" ]; then \
		echo "RUNPROMPT_PROMPT is required"; \
		echo "example: make runprompt-example RUNPROMPT_AGENT=claude-chat RUNPROMPT_DIR=$(PWD) RUNPROMPT_PROMPT='list 5 TODOs'"; \
		exit 1; \
	fi
	go run ./examples/runprompt \
		-target $(RUNPROMPT_TARGET) \
		-project $(RUNPROMPT_PROJECT) \
		-cacert certs/ca-bundle.crt \
		-cert certs/dev-client.crt \
		-key certs/dev-client.key \
		-jwt-key certs/jwt-signing.key \
		-jwt-issuer dev \
		-timeout 5m \
		$(RUNPROMPT_AGENT) \
		$(RUNPROMPT_DIR) \
		"$(RUNPROMPT_PROMPT)"
