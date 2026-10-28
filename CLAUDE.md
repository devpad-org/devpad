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

**Projects and workspaces** — every new workspace belongs to a project (`internal/project`): either its one project workspace (`kind = 'interactive'`, the user's IDE for the project) or an AI sandbox (`task`/`review`, each owning one branch). Each project's canonical bare repo lives in an isolated repo container; devpad moves commits between it and workspaces with git bundles over the agent channel, and no project-linked workspace gets a mount, route or credential to that repo or the user's SSH keys. A project's protected branches, its default branch plus `projects.protected_branches` (such as `main` in a project that works on `develop`), move only through merge requests: no sandbox owns one, the project workspace never publishes one, merge requests target only them, and only they are pushed upstream. Only the user deletes a branch, from the project's Branches dialog (`DeleteBranch`, under the branch lock and holding the branch as a publish does): never a protected one, one a sandbox owns, an open merge request's source, or one a task not merged or cancelled works on or starts from. Each `BranchGuard` runs the deletion under the lock that opening merge requests or creating tasks takes, so its answer still holds when the branch goes; `git push devpad :<branch>` is refused. `SetBranches` protects a branch under the same lock as sandbox and publish reservations, which re-read the project's branches. Workspaces without a project are left from before projects: devpad no longer creates them, and they can only be used, converted into a project, or deleted.

**Project environments and setup** — a project's environment is the body of a Dockerfile (no `FROM`, `ADD` or `COPY`: the daemon fetches `ADD` URLs on the host's network and `COPY --from` reads any local image) that `internal/project/environment.go` builds in the background on `devpad-workspace:latest`, inside a frame devpad owns (agent entrypoint, `devpad` user, `/workspace`), into `devpad-project-<id>:<hash>`, where the hash covers the base image ID and the Dockerfile. Builds run on a network of their own with memory, CPU and time limits and no build context. New workspaces get the last image that built (devpad's image before any has); the repo container always keeps devpad's image; the project workspace moves to a new image only when the user rebuilds it, which keeps only `/workspace`. The setup script (`internal/project/setup.go`) then runs once in each newly seeded workspace, and task and review agents start only after any environment build and their sandbox's setup have finished, waiting outside their locks.

**The devpad git remote** — a project workspace's repo has a `devpad` remote (`devpad::project`) served by `git-remote-devpad`, the agent binary linked under that name. The helper talks to the agent over a unix socket; `internal/gitbridge` long-polls each running project workspace's agent for those requests and answers them through the project service. `git fetch devpad` refreshes `refs/remotes/devpad/*` by bundle, and `git push devpad <branch>` is only a request: devpad pulls the branch and publishes it exactly like the Publish button (never a protected branch, never a sandbox-owned branch, fast-forward only), and the IDE shows each push. The IDE assistant runs in the same workspace and can push this way too, with no more power than the Publish button. When the project has an upstream, the workspace also has an `origin` remote (`devpad::upstream`), a read-only view of it: listing it makes devpad sync from the upstream (`internal/project/upstream.go`: fetch into the canonical repo's `refs/upstream/heads/*`, then fast-forward only protected branches that are strictly behind; never create, delete or force), fetching it brings those refs as `refs/remotes/origin/*`, and pushing to it is refused. `git fetch devpad` also syncs when the last sync is over a minute old. Syncs are shared per project and only ever asked for, never shaped, by the workspace. Local branches track `devpad/<branch>` and `remote.pushDefault` is `devpad`, so a plain `git pull` and `git push` work. Sandboxes and standalone workspaces are never polled, so the helper refuses there. The agent is inside the workspace and so hostile: every limit on the work a workspace can make devpad do (poll pacing, response size, requests and fetched branches per minute, log lines) is enforced in `internal/gitbridge`, never in the agent.

**Workspace previews** — `internal/preview` generates one-time preview URLs. Preview subdomains follow `{workspaceID}-{port}.preview-domain` format; traffic is reverse-proxied through the workspace agent's port proxy after token/cookie validation.

**SQLite migrations** live in numbered `.sql` files under `internal/database/migrations/`. `database.Migrate` applies them lexicographically and records versions in `schema_migrations`. Always add the next numbered migration; never edit an applied one.

**AI subsystem** is intentionally layered. `internal/ai` is only the composition facade. Domain types live in `internal/ai/domain`, use cases in `internal/ai/app`, persistence in `internal/ai/storage`, provider adapters in `internal/ai/provider/*` (Anthropic, Mistral, Minimax, Moonshot, OpenAI-responses), workspace tool execution in `internal/ai/tools`, and HTTP/SSE transport in `internal/ai/transport/http`. Other packages add workspace-specific tools as an `aitools.ToolProvider`, which can also add system-prompt context (`PromptProvider`) and require the user's approval for some calls (`ApprovalGate`): `internal/mergerequest` gives review agents their review tools, and `internal/projecttools` gives the assistant in a project workspace the board (list, create and start tasks, read merge requests). Provider tools run on the host, derive the project from the run's workspace, and never merge, close, push or move a protected branch. A run stops, and fails, when its `domain.Budget` is spent or after 500 model requests; a budget is shared by a run and its sub-agents, and the admin's `ai_budget_defaults` set one per task (kept on the task with its running `cost_usd`), per review, and per assistant reply.

**Frontend** is a Vite/Vue 3 app in `frontend/src`. It uses Vue Router guards for setup/auth/admin routing, Pinia stores for shared state, typed API modules under `frontend/src/api`, composables under `frontend/src/composables`, and IDE-specific components under `frontend/src/components/ide`. Vite outputs production assets to `web/dist/`, which `web/embed.go` embeds and serves with an SPA fallback. Monaco is split into its own Vite chunk.

## Conventions

**Backend**
- Routes use Go's standard `net/http` method-pattern syntax (`mux.Handle("GET /api/workspaces/{id}", ...)`); read path params with `r.PathValue`.
- HTTP handlers return JSON through package-local `writeJSON`/`writeError` helpers and map domain errors to status codes near the handler boundary.
- Auth is session-cookie based (`devpad_session`, or `__Host-devpad_session` when cookies are Secure, i.e. with `--domain` or `--preview-domain`; use `auth.SessionCookieName`). Use `auth.UserFromContext` in authenticated handlers; wrap protected routes with `RequireAuth` or `RequireAdmin` in the central route registration.
- `internal/server/origin.go` refuses state-changing requests and WebSocket upgrades from any origin but the app's own (preview subdomains are same-site, so SameSite cookies don't stop them), and requires JSON bodies under `/api/`. A new non-JSON endpoint must be exempted there explicitly.
- Wrap lower-level errors with `%w`; do not discard errors except for existing best-effort cleanup paths around Docker resources, SSH injection, sidecars, and agent readiness.
- Database repositories use `context.Context`, parameterized SQL, and `database/sql`; SQLite is opened with WAL, foreign keys, busy timeout, a single open connection, and immediate transactions.
- Tests are Go `testing` tests colocated as `*_test.go`; use table-style subtests where useful, `t.Helper()` for setup helpers, and in-memory SQLite for repository/service tests.
- Use `internal/container.Manager` and the Docker SDK abstraction; never shell out to the `docker` CLI.

**Agent boundary**
- Code that needs live filesystem, terminal, command, or Git data for a workspace belongs in `cmd/agent` plus a proxy/client path in the host — not only in the Vue frontend.
- Agent requests use the `X-Devpad-Agent-Token` header; `/healthz` and `/api/version` are intentionally unauthenticated.
- The agent cannot call the host. When something inside a workspace needs devpad (like `git push devpad`), the agent queues it and the host polls for it; treat everything queued as untrusted input.

**Frontend**
- Use Vue 3 `<script setup lang="ts">`, strict TypeScript, and the `@/` alias for `frontend/src`.
- Components call API modules or stores instead of constructing endpoint logic inline (except for streaming/WebSocket cases — match the nearby pattern).
- UI styling uses the Gruvbox-themed CSS custom properties in `frontend/src/assets/styles/main.css`; use existing tokens (`--bg-*`, `--text-*`, `--accent-*`, `--border-*`, spacing, radius, transition) over hardcoded values.

**Build artifacts** — do not include generated artifacts in source changes unless the task is specifically about embedded assets. `web/dist/`, `bin/`, `internal/agentbin/devpad-agent`, and `internal/dockerfile/Dockerfile` are produced by Make targets.
