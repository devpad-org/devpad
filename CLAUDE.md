# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

Run all commands from the repository root unless noted.

| Task | Command |
|------|---------|
| Full build | `make build` |
| Backend build only | `make backend` |
| Frontend install | `make install-frontend` |
| Frontend build + type-check | `make frontend` |
| Frontend type-check only | `cd frontend && npm run type-check` |
| Backend dev server | `go run .` |
| Frontend dev server | `make dev-frontend` |
| All Go tests with coverage | `make test` |
| All Go tests without coverage | `go test ./...` |
| Package tests | `go test ./internal/auth` |
| Single Go test | `go test ./internal/auth -run '^TestService_LoginLogout$'` |
| Agent tests | `go test ./cmd/agent` |

`make build` is the correct full build path: it builds the Vue frontend into `web/dist/`, builds `cmd/agent`, copies the embedded agent binary and Dockerfile into `internal/agentbin/` and `internal/dockerfile/`, then builds `bin/devpad`. The Vite dev server proxies `/api` to `http://localhost:8080`, so run it alongside the backend dev server.

## Architecture

Devpad is a Go web server that serves an embedded Vue 3 IDE frontend and manages Docker-backed workspaces. `main.go` reads flags/env vars, builds `server.Config`, and delegates application assembly to `internal/server`.

**Composition root** — `internal/server/server.go` opens SQLite, runs embedded migrations, creates the encryption cipher, wires auth/settings/workspace/admin/AI/preview services and handlers, registers all `net/http` `ServeMux` routes, applies auth middleware and body limits, routes preview subdomains by host, and serves `web.Handler()` as the SPA fallback. With `--domain`, TLS is configured via CertMagic/Cloudflare DNS-01; without it, the server listens on plain HTTP.

**Backend layout** — domain packages under `internal/`: handlers translate HTTP to service calls, services own business logic, and repositories own SQLite access. Key packages: `internal/auth`, `internal/settings`, `internal/workspace`, `internal/wsservice`, `internal/admin`, `internal/preview`. Prefer the narrow interfaces already defined in each package (e.g. `internal/workspace/interfaces.go`).

**Workspace execution** is split between the host and an in-container agent. The host uses the Docker SDK through `internal/container.Manager` to build/run `devpad-workspace:latest`, create per-workspace Docker volumes and networks, inject SSH material, and manage sidecar containers. `cmd/agent` is built as `bin/devpad-agent`, embedded into the main binary, and runs inside each workspace container at port 9100. Host workspace handlers proxy terminal, file watching, file operations, commands, and Git operations to the agent over HTTP/WebSocket using a per-workspace auth token.

**Workspace previews** — `internal/preview` generates one-time preview URLs. Preview subdomains follow `{workspaceID}-{port}.preview-domain` format; traffic is reverse-proxied through the workspace agent's port proxy after token/cookie validation.

**SQLite migrations** live in numbered `.sql` files under `internal/database/migrations/`. `database.Migrate` applies them lexicographically and records versions in `schema_migrations`. Always add the next numbered migration; never edit an applied one.

**AI subsystem** is intentionally layered. `internal/ai` is only the composition facade. Domain types live in `internal/ai/domain`, use cases in `internal/ai/app`, persistence in `internal/ai/storage`, provider adapters in `internal/ai/provider/*` (Anthropic, Mistral, Minimax, Moonshot, OpenAI-responses), workspace tool execution in `internal/ai/tools`, and HTTP/SSE transport in `internal/ai/transport/http`.

**Frontend** is a Vite/Vue 3 app in `frontend/src`. It uses Vue Router guards for setup/auth/admin routing, Pinia stores for shared state, typed API modules under `frontend/src/api`, composables under `frontend/src/composables`, and IDE-specific components under `frontend/src/components/ide`. Vite outputs production assets to `web/dist/`, which `web/embed.go` embeds and serves with an SPA fallback. Monaco is split into its own Vite chunk.

## Conventions

**Backend**
- Routes use Go's standard `net/http` method-pattern syntax (`mux.Handle("GET /api/workspaces/{id}", ...)`); read path params with `r.PathValue`.
- HTTP handlers return JSON through package-local `writeJSON`/`writeError` helpers and map domain errors to status codes near the handler boundary.
- Auth is session-cookie based (`devpad_session`). Use `auth.UserFromContext` in authenticated handlers; wrap protected routes with `RequireAuth` or `RequireAdmin` in the central route registration.
- Wrap lower-level errors with `%w`; do not discard errors except for existing best-effort cleanup paths around Docker resources, SSH injection, sidecars, and agent readiness.
- Database repositories use `context.Context`, parameterized SQL, and `database/sql`; SQLite is opened with WAL, foreign keys, busy timeout, a single open connection, and immediate transactions.
- Tests are Go `testing` tests colocated as `*_test.go`; use table-style subtests where useful, `t.Helper()` for setup helpers, and in-memory SQLite for repository/service tests.
- Use `internal/container.Manager` and the Docker SDK abstraction; never shell out to the `docker` CLI.

**Agent boundary**
- Code that needs live filesystem, terminal, command, or Git data for a workspace belongs in `cmd/agent` plus a proxy/client path in the host — not only in the Vue frontend.
- Agent requests use the `X-Devpad-Agent-Token` header; `/healthz` and `/api/version` are intentionally unauthenticated.

**Frontend**
- Use Vue 3 `<script setup lang="ts">`, strict TypeScript, and the `@/` alias for `frontend/src`.
- Components call API modules or stores instead of constructing endpoint logic inline (except for streaming/WebSocket cases — match the nearby pattern).
- UI styling uses the Gruvbox-themed CSS custom properties in `frontend/src/assets/styles/main.css`; use existing tokens (`--bg-*`, `--text-*`, `--accent-*`, `--border-*`, spacing, radius, transition) over hardcoded values.

**Build artifacts** — do not include generated artifacts in source changes unless the task is specifically about embedded assets. `web/dist/`, `bin/`, `internal/agentbin/devpad-agent`, and `internal/dockerfile/Dockerfile` are produced by Make targets.
