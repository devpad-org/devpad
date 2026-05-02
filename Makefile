.PHONY: all build clean dev frontend backend install-frontend test agent workspace-image

# Version derived from git — short commit hash, or "dev" if not in a git repo.
VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo "dev")
AGENT_LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION)"
EMBED_LDFLAGS := -ldflags "-s -w -X github.com/devpad-org/devpad/internal/agentbin.Version=$(VERSION)"

# Default target
all: build

# Install frontend dependencies
install-frontend:
	cd frontend && npm install

# Build frontend (outputs to web/dist/)
frontend: install-frontend
	cd frontend && npm run build

# Build the workspace agent binary for the host architecture
agent:
	GOOS=linux GOARCH=$(shell go env GOHOSTARCH) CGO_ENABLED=0 go build $(AGENT_LDFLAGS) -o bin/devpad-agent ./cmd/agent

# Copy the agent binary into the embed directory so the main binary can embed it
embed-agent: agent
	cp bin/devpad-agent internal/agentbin/devpad-agent

# Copy the workspace Dockerfile into the embed directory
embed-dockerfile:
	cp docker/workspace/Dockerfile internal/dockerfile/Dockerfile

# Build Go binary (requires frontend and embedded agent + Dockerfile)
backend: embed-agent embed-dockerfile
	go build $(EMBED_LDFLAGS) -o bin/devpad .

# Build the workspace Docker image (requires agent to be built first)
workspace-image: agent
	cp bin/devpad-agent docker/workspace/devpad-agent
	docker build -t devpad-workspace:latest docker/workspace/
	rm docker/workspace/devpad-agent

# Full build: frontend then backend (backend already depends on embed-agent)
build: frontend backend
	@echo "Build complete: bin/devpad"

# Run frontend dev server with HMR
dev-frontend:
	cd frontend && npm run dev

# Run backend in dev mode
dev-backend:
	go run .

# Run unit tests with coverage
test:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out

# Clean build artifacts
clean:
	rm -rf bin/ web/dist/ coverage.out internal/agentbin/devpad-agent internal/dockerfile/Dockerfile
