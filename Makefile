.PHONY: all build clean dev frontend backend install-frontend test

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
