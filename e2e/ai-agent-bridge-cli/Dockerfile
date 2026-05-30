FROM golang:1.25-bookworm

WORKDIR /src

# Copy module files first for layer caching.
COPY go.mod go.sum ./
RUN go mod download

# Copy the full source tree.
COPY . .

# Pre-build the binary to warm the Go build cache (TestMain rebuilds it).
RUN go build -o /usr/local/bin/ai-agent-bridge-cli ./cmd/ai-agent-bridge-cli

# Run the e2e test suite.
CMD ["go", "test", "-v", "-count=1", "-race", "-timeout", "120s", "./e2e/ai-agent-bridge-cli/"]
