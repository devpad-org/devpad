.PHONY: all build clean dev frontend backend install-frontend test agent workspace-image

# Default target
all: build

# Install frontend dependencies
install-frontend:
	cd frontend && npm install

# Build frontend (outputs to web/dist/)
frontend: install-frontend
	cd frontend && npm run build

# Build Go binary (requires frontend to be built first)
backend:
	go build -o bin/devpad .

# Build the workspace agent binary (linux/amd64 for containers)
agent:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o bin/devpad-agent ./cmd/agent

# Build the workspace Docker image (requires agent to be built first)
workspace-image: agent
	cp bin/devpad-agent docker/workspace/devpad-agent
	docker build -t devpad-workspace:latest docker/workspace/
	rm docker/workspace/devpad-agent

# Full build: frontend then backend
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
	rm -rf bin/ web/dist/ coverage.out
