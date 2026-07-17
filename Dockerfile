# syntax=docker/dockerfile:1

# ---- Stage 1: build the Vue frontend into web/dist/ ----
FROM node:22-trixie AS frontend
WORKDIR /app/frontend

# Install deps first for better layer caching.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

# Build (outputs to /app/web/dist via vite.config.ts).
COPY frontend/ ./
COPY web/ /app/web/
RUN npm run build

# ---- Stage 2: build the Go binaries ----
# mattn/go-sqlite3 requires CGO, so we need a toolchain with a C compiler.
# Using the trixie variant keeps glibc compatible with the runtime image.
FROM golang:1.26-trixie AS backend
WORKDIR /src

ARG VERSION=dev

# Download modules first for caching.
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    go mod download

# Copy the full source, then the built frontend from stage 1.
COPY . .
COPY --from=frontend /app/web/dist/ ./web/dist/

# Build the workspace agent (static, no CGO) and stage it for embedding.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    GOOS=linux GOARCH=$(go env GOHOSTARCH) CGO_ENABLED=0 \
    go build -ldflags "-s -w -X main.Version=${VERSION}" \
    -o internal/agentbin/devpad-agent ./cmd/agent

# Embed the workspace Dockerfile.
RUN cp docker/workspace/Dockerfile internal/dockerfile/Dockerfile

# Build the main binary. CGO on for go-sqlite3.
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux \
    go build -ldflags "-s -w -X github.com/devpad-org/devpad/internal/agentbin.Version=${VERSION}" \
    -o /out/devpad .

# ---- Stage 3: runtime ----
FROM debian:trixie-slim AS runtime

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=backend /out/devpad /usr/local/bin/devpad

# Persist the SQLite database and ACME/CertMagic material under /data.
ENV DEVPAD_DB_PATH=/data/devpad.db \
    XDG_DATA_HOME=/data \
    XDG_CONFIG_HOME=/data
RUN mkdir -p /data
VOLUME ["/data"]
WORKDIR /data

# Default HTTP port (443 is used automatically when --domain is set).
EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/devpad"]
