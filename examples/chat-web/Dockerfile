# Build context: repo root (../../)
# docker build -f examples/chat-web/Dockerfile .
FROM node:24-bookworm AS base

RUN corepack enable && corepack prepare pnpm@latest --activate

WORKDIR /app

# Copy bridge-client-node source (vite resolves it directly from TypeScript source)
COPY packages/bridge-client-node/package.json packages/bridge-client-node/
COPY packages/bridge-client-node/tsconfig.json packages/bridge-client-node/
COPY packages/bridge-client-node/src/ packages/bridge-client-node/src/
COPY packages/bridge-client-node/react/ packages/bridge-client-node/react/

# Install chat-web deps using lockfile
COPY examples/chat-web/package.json examples/chat-web/pnpm-lock.yaml examples/chat-web/

WORKDIR /app/examples/chat-web
RUN pnpm install --frozen-lockfile --ignore-scripts

# Copy remaining source
COPY examples/chat-web/ .

# Production: build Vite output then run Express server
FROM base AS production
RUN pnpm run build
CMD ["pnpm", "start"]

# Dev Express server only (no static serving — proxied via Vite)
FROM base AS server-dev
CMD ["pnpm", "exec", "tsx", "server/index.ts"]

# Dev Vite server only
FROM base AS vite-dev
CMD ["pnpm", "exec", "vite", "--host"]
