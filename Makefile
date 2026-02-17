.PHONY: build proto test lint clean certs dev-setup

BIN_DIR := bin
BRIDGE := $(BIN_DIR)/bridge
BRIDGE_CA := $(BIN_DIR)/bridge-ca

build: proto
	@mkdir -p $(BIN_DIR)
	go build -o $(BRIDGE) ./cmd/bridge
	go build -o $(BRIDGE_CA) ./cmd/bridge-ca

proto:
	protoc \
		--go_out=. --go_opt=paths=source_relative \
		--go-grpc_out=. --go-grpc_opt=paths=source_relative \
		proto/bridge/v1/bridge.proto

test:
	go test -race -count=1 ./...

test-cover:
	go test -race -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

lint:
	golangci-lint run ./...

clean:
	rm -rf $(BIN_DIR) coverage.out coverage.html

certs: build
	$(BRIDGE_CA) init --name ai-agent-bridge --out certs/

dev-setup: certs
	@echo "Dev environment ready. Certs in certs/"

fmt:
	gofmt -s -w .
	goimports -w .
