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
RUN CGO_ENABLED=0 go build -o /out/bridgectl ./cmd/bridgectl && \
    CGO_ENABLED=0 go build -o /out/bridge-ca ./cmd/bridge-ca

# Pre-built binaries stage (GoReleaser provides these in the build context)
FROM scratch AS prebuilt
COPY bridgectl bridge-ca /out/

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

# Install step CLI for Step CA integration (Tier 2 PKI).
# The binary is used by bridgectl to obtain certificates from a Step CA instance.
ARG STEP_VERSION=0.30.6
RUN ARCH=$(dpkg --print-architecture) && \
    curl -fsSL "https://github.com/smallstep/cli/releases/download/v${STEP_VERSION}/step-cli_${ARCH}.deb" -o /tmp/step-cli.deb && \
    dpkg -i /tmp/step-cli.deb && \
    rm /tmp/step-cli.deb

RUN useradd -m -s /bin/bash bridge && \
    mkdir -p /home/bridge/.gemini && \
    chown -R bridge:bridge /home/bridge/.gemini

COPY --from=build /out/bridgectl /usr/local/bin/bridgectl
COPY --from=build /out/bridge-ca /usr/local/bin/bridge-ca
COPY .nvmrc /app/.nvmrc
RUN corepack enable && corepack prepare pnpm@11.22.0 --activate
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml /app/
RUN pnpm install --frozen-lockfile --prod && pnpm store prune && \
    ln -sf /app/node_modules/.bin/codex /usr/local/bin/codex && \
    ln -sf /app/node_modules/.bin/claude /usr/local/bin/claude && \
    ln -sf /app/node_modules/.bin/opencode /usr/local/bin/opencode && \
    ln -sf /app/node_modules/.bin/gemini /usr/local/bin/gemini && \
    (sed -i "s|'  Type your message or @path/to/file'|' '|g" \
        /app/node_modules/@google/gemini-cli/dist/src/ui/components/Composer.js \
        /app/node_modules/@google/gemini-cli/dist/src/ui/components/InputPrompt.js \
    || true)
COPY config/bridge.yaml /app/config/bridge.yaml
COPY config/bridge-docker.yaml /app/config/bridge-docker.yaml
COPY config/bridge-docker-stepca.yaml /app/config/bridge-docker-stepca.yaml
COPY docker-entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# e2e test helpers — excluded from production images by default.
# Pass --build-arg INCLUDE_E2E_SCRIPTS=true to include them (e.g. in e2e compose).
ARG INCLUDE_E2E_SCRIPTS=false
RUN mkdir -p /app/scripts
RUN --mount=type=bind,source=e2e/scripts/opencode_repl.js,target=/tmp/opencode_repl.js \
    if [ "$INCLUDE_E2E_SCRIPTS" = "true" ]; then \
      cp /tmp/opencode_repl.js /app/scripts/opencode_repl.js; \
    fi

EXPOSE 9445

ENTRYPOINT ["/app/entrypoint.sh"]
