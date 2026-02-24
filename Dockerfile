# Build stage
FROM golang:1.25 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/bridge ./cmd/bridge && \
    CGO_ENABLED=0 go build -o /out/bridge-ca ./cmd/bridge-ca

# Runtime stage
FROM ubuntu:24.04

RUN apt-get update && \
    apt-get install -y --no-install-recommends ca-certificates curl && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    npm install -g \
      @anthropic-ai/claude-code \
      @openai/codex \
      @google/gemini-cli \
      opencode-ai && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash bridge

WORKDIR /app

COPY --from=build /out/bridge /usr/local/bin/bridge
COPY --from=build /out/bridge-ca /usr/local/bin/bridge-ca
COPY config/bridge.yaml /app/config/bridge.yaml
COPY docker-entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

EXPOSE 9445

ENTRYPOINT ["/app/entrypoint.sh"]
