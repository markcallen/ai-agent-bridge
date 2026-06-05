# BUILD_FROM selects the binary source:
#   source   - build from Go source (default, for local docker build)
#   prebuilt - use binaries already compiled by GoReleaser
ARG BUILD_FROM=source

# Source build stage
FROM golang:1.25 AS source

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /out/ai-agent-bridge ./cmd/bridge && \
    CGO_ENABLED=0 go build -o /out/ai-agent-bridge-ca ./cmd/bridge-ca

# Pre-built binaries stage (GoReleaser provides these in the build context)
FROM scratch AS prebuilt
COPY ai-agent-bridge ai-agent-bridge-ca /out/

# Select binary source — BuildKit skips whichever stage is not referenced
FROM ${BUILD_FROM} AS build

# Runtime stage
FROM ubuntu:24.04

WORKDIR /app

RUN apt-get update && \
    apt-get install -y --no-install-recommends bubblewrap ca-certificates curl && \
    curl -fsSL https://deb.nodesource.com/setup_24.x | bash - && \
    apt-get install -y --no-install-recommends nodejs && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

RUN useradd -m -s /bin/bash bridge && \
    mkdir -p /home/bridge/.gemini && \
    chown -R bridge:bridge /home/bridge/.gemini

COPY --from=build /out/ai-agent-bridge /usr/local/bin/ai-agent-bridge
COPY --from=build /out/ai-agent-bridge-ca /usr/local/bin/ai-agent-bridge-ca
COPY .nvmrc /app/.nvmrc
COPY package.json package-lock.json /app/
RUN npm ci --omit=dev --no-audit --no-fund && npm cache clean --force && \
    (sed -i "s|'  Type your message or @path/to/file'|' '|g" \
        /app/node_modules/@google/gemini-cli/dist/src/ui/components/Composer.js \
        /app/node_modules/@google/gemini-cli/dist/src/ui/components/InputPrompt.js \
    || true)
COPY config/bridge.yaml /app/config/bridge.yaml
COPY config/bridge-docker.yaml /app/config/bridge-docker.yaml
COPY docker-entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

RUN mkdir -p /app/scripts
COPY e2e/scripts/opencode_repl.js /app/scripts/opencode_repl.js

EXPOSE 9445

ENTRYPOINT ["/app/entrypoint.sh"]
