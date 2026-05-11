# Copilot instructions for Devpad

## Commands

Run commands from the repository root unless noted.

| Task | Command |
| --- | --- |
| Full build | `make build` |
| Backend build only | `make backend` |
| Frontend install | `cd frontend && npm install` or `make install-frontend` |
| Frontend build and type-check | `make frontend` or `cd frontend && npm run build` |
| Frontend type-check only | `cd frontend && npm run type-check` |
| Backend dev server | `make dev-backend` or `go run .` |
| Frontend dev server | `make dev-frontend` |
| All Go tests with coverage | `make test` |
| All Go tests without coverage | `go test ./...` |
| Package tests | `go test ./internal/auth` |
| Single Go test | `go test ./internal/auth -run '^TestService_LoginLogout$'` |
| Agent tests | `go test ./cmd/agent` |

`make build` is the safest build path: it builds the Vue frontend into `web/dist/`, builds `cmd/agent`, copies the embedded agent and Dockerfile into `internal/agentbin/` and `internal/dockerfile/`, then builds `bin/devpad`. The Vite dev server proxies `/api` to `http://localhost:8080`, so run it alongside the backend dev server.

## Architecture

Devpad is a Go web server that serves an embedded Vue 3 IDE frontend and manages Docker-backed workspaces. `main.go` reads flags/env vars, builds `server.Config`, and delegates application assembly to `internal/server`.

`internal/server/server.go` is the composition root. It opens SQLite, runs embedded migrations, creates the encryption cipher, wires auth/settings/workspace/admin/AI/preview services and handlers, registers all `net/http` `ServeMux` routes, applies auth middleware and body limits, routes preview subdomains by host, then serves `web.Handler()` as the SPA fallback. With `--domain`, TLS is configured through CertMagic/Cloudflare DNS-01; without it, the server listens on plain HTTP.

The backend follows domain packages under `internal/`: handlers translate HTTP to service calls, services own business logic, and repositories own SQLite access. Common examples are `internal/auth`, `internal/settings`, `internal/workspace`, `internal/wsservice`, `internal/admin`, and `internal/preview`. Prefer depending on the narrow interfaces already defined in each package, such as the workspace capability interfaces in `internal/workspace/interfaces.go`.

Workspace execution is split between the host server and an in-container agent. The host uses the Docker SDK through `internal/container.Manager` to build/run `devpad-workspace:latest`, create per-workspace volumes and networks, inject SSH material, and manage sidecar services. `cmd/agent` is built as `bin/devpad-agent`, copied into the embedded assets, and runs inside each workspace container. Host workspace handlers proxy terminal, file watching, file operations, commands, and Git operations to the agent over HTTP/WebSocket using the workspace agent token.

Workspace previews are handled by `internal/preview`: authenticated users generate one-time preview URLs, preview subdomains follow the `{workspaceID}-{port}.preview-domain` format, and preview traffic is reverse-proxied through the workspace agent's port proxy after token/cookie validation.

SQLite schema changes live in numbered embedded migrations under `internal/database/migrations/`. `internal/database.Migrate` applies `.sql` files lexicographically and records versions in `schema_migrations`; add the next numbered migration rather than editing an applied migration.

The AI subsystem is intentionally layered. `internal/ai` is only the composition facade; domain types live in `internal/ai/domain`, use cases in `internal/ai/app`, persistence in `internal/ai/storage`, provider adapters in `internal/ai/provider/*`, workspace tool execution in `internal/ai/tools`, and HTTP/SSE transport in `internal/ai/transport/http`.

The frontend is a Vite/Vue 3 app in `frontend/src`. It uses Vue Router guards for setup/auth/admin routing, Pinia stores for shared state, typed API modules under `frontend/src/api`, composables under `frontend/src/composables`, and IDE-specific components under `frontend/src/components/ide`. Vite outputs production assets to `web/dist`, which `web/embed.go` embeds and serves with an SPA fallback. Monaco is split into its own Vite chunk.

## Codebase conventions

- Backend routes use Go's standard `net/http` method-pattern syntax in `internal/server/server.go`, such as `mux.Handle("GET /api/workspaces/{id}", ...)`; path params are read with `r.PathValue`.
- HTTP handlers return JSON through package-local `writeJSON`/`writeError` helpers and map domain errors to status codes near the handler boundary.
- Auth is session-cookie based (`devpad_session`). Use `auth.UserFromContext` in authenticated handlers, and wrap protected routes with `RequireAuth` or `RequireAdmin` in the central route registration.
- Wrap lower-level errors with context using `%w`; do not discard errors except for existing best-effort cleanup/logging paths around Docker resources, SSH injection, sidecars, and agent readiness.
- Database repositories use `context.Context`, parameterized SQL, and `database/sql`; SQLite is opened with WAL, foreign keys, busy timeout, a single open connection, and immediate transactions.
- Tests are Go `testing` tests colocated as `*_test.go`; existing tests use table-style subtests where useful, `t.Helper()` for setup helpers, and in-memory SQLite schemas for repository/service tests.
- Frontend code uses Vue 3 `<script setup lang="ts">`, strict TypeScript, the `@/` alias for `frontend/src`, and typed API response interfaces colocated with API clients.
- Components should call API modules or stores instead of constructing endpoint logic inline. Exceptions already exist for streaming/text/WebSocket cases; match the nearby API module pattern when adding similar behavior.
- UI styling uses the Gruvbox-themed CSS custom properties in `frontend/src/assets/styles/main.css`; prefer existing tokens such as `--bg-*`, `--text-*`, `--accent`, `--border-*`, spacing, radius, and transition variables over hardcoded values.
- Workspace-related application code should use `internal/container.Manager` and the Docker SDK abstraction, not shell out to the `docker` CLI.
- The workspace agent boundary matters: code that needs live filesystem, terminal, command, or Git data for a workspace usually belongs in `cmd/agent` plus a proxy/client path in the host, not only in the Vue frontend. Agent requests use the `X-Devpad-Agent-Token` header; health/version endpoints are intentionally unauthenticated.
- Keep generated build artifacts out of source changes unless the task is specifically about embedded assets. `web/dist/`, `bin/`, `internal/agentbin/devpad-agent`, and `internal/dockerfile/Dockerfile` are produced by Make targets.
