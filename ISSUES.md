# Devpad — Code Review Issues

Comprehensive review covering security flaws, bugs, architecture issues, and code quality.

---

## Medium

### 1. Git Log `count` Parameter Not Validated on Agent

**File:** `cmd/agent/git.go`

The `count` query param for `handleGitLog` is passed directly to `git log -n <count>` with no validation on the agent side. While the main server handler validates `count` to `[1, 200]`, the agent is a separately-exposed service that can be called directly.

```go
count := r.URL.Query().Get("count")
if count == "" {
    count = "50"
}
out, err := gitOutput(ctx, "log", "--oneline", "--format=...", "-n", count)
```

A value like `"99999999"` could cause excessive output; a value starting with `-` could inject flags.

**Fix:** Parse and validate `count` as an integer within `[1, 200]` on the agent side.

---

### 2. AI Provider API Keys Stored in Plaintext

**File:** `internal/ai/repository.go`

API keys for AI providers (Mistral, MiniMax, etc.) are stored directly in the SQLite database as plain text. Anyone with read access to the database file (e.g., via a backup, a path traversal bug, or filesystem access) can extract all API keys.

**Fix:** Encrypt API keys at rest using AES-GCM with a key derived from a server secret (e.g., via `DEVPAD_ENCRYPTION_KEY` environment variable). Decrypt only when needed for API calls.

---

### 3. SameSite Lax Allows GET-Based CSRF

**File:** `internal/auth/handler.go:111`

```go
SameSite: http.SameSiteLaxMode,
```

`SameSite: Lax` allows cookies to be sent on top-level GET navigations from cross-origin sites. If any state-changing endpoint accepts GET requests (either intentionally or via permissive routing), cross-site requests can trigger them.

**Fix:** Verify at the router level that all mutation endpoints only accept POST/PUT/DELETE. Consider upgrading to `SameSite: Strict` if the UX impact is acceptable, or add explicit CSRF tokens for state-changing operations.

---

### 4. Preview Cookie Secret Is Ephemeral

**File:** `internal/preview/handler.go:33-36`

```go
secret := make([]byte, 32)
if _, err := rand.Read(secret); err != nil {
    log.Fatalf("failed to generate preview cookie secret: %v", err)
}
```

The preview cookie signing secret is regenerated on every server restart. This means:

- All active preview sessions are invalidated on restart
- Horizontal scaling is impossible (multiple instances have different secrets)
- Rolling deployments break preview functionality

**Fix:** Derive the secret from a persistent source — either a config file, environment variable, or the database. Example: `HMAC(serverSecret, "preview-cookie-v1")`.

---

### 5. Dead Code: `pullImageIfNeeded`

**File:** `internal/container/container.go:161-179`

```go
func (m *manager) pullImageIfNeeded(ctx context.Context, imageName string) error {
    _, _, err := m.cli.ImageInspectWithRaw(ctx, imageName)
    if err == nil {
        return nil
    }
    log.Printf("pulling image %s...", imageName)
    reader, err := m.cli.ImagePull(ctx, imageName, image.PullOptions{})
    // ...
}
```

This function is fully implemented but never called. The `Create` method assumes the image already exists. If the image isn't pre-built, workspace creation fails with a confusing Docker error rather than attempting to pull.

**Fix:** Either remove the dead code or wire it into `Create()` before `ContainerCreate`.

---

## Bugs

### 6. `push` Without `--set-upstream` Fails on First Push

**File:** `cmd/agent/git.go:240-248`

When a new branch is pushed for the first time, `git push origin` without `--set-upstream` fails because there's no upstream tracking branch configured. The error is returned to the user but the UX is poor — there's no affordance to set upstream or retry with the correct flags.

**Fix:** Detect the "no upstream" error and automatically retry with `--set-upstream`, or add a `set-upstream` option to the push action.

---

### 7. `discard` Action Silently Succeeds on Untracked Files

**File:** `cmd/agent/git.go:268-272`

```go
case "discard":
    if len(req.Files) == 0 {
        writeErr(w, http.StatusBadRequest, "files are required for discard")
        return
    }
    args := append([]string{"checkout", "--"}, req.Files...)
    out, err = gitOutput(ctx, args...)
```

`git checkout -- <file>` does nothing for untracked files. The frontend correctly hides the discard button for untracked files, but there's no server-side guard — a direct API call with `action: "discard"` and an untracked file silently reports success while doing nothing.

**Fix:** For untracked files, use `git clean -f -- <file>` instead, or return an error indicating the file is untracked.

---

## Code Quality

### 8. No Action Whitelist Validation on Server Side

**File:** `internal/workspace/handler.go:417-460`

The workspace handler validates only that `action != ""` then blindly forwards it to the agent. The agent has a whitelist (`switch` statement), but the server should also validate to fail fast and avoid unnecessary network round-trips to the container.

**Fix:** Add a server-side whitelist of allowed actions.

---

### 9. Agent Client Does Not Limit Git Response Body Size

**File:** `internal/agent/client.go:338-475`

All Git response decoders (`json.NewDecoder(resp.Body).Decode(...)`) read unbounded response bodies from the agent. A compromised or buggy agent could return a huge payload. Contrast with `ReadFile` which correctly uses `io.LimitReader`.

**Fix:** Wrap response bodies with `io.LimitReader` before decoding, consistent with the existing pattern in `ReadFile`.

---

### 10. GitPanel 5-Second Polling With No Debounce

**File:** `frontend/src/components/ide/GitPanel.vue:276`

The panel polls `git status` every 5 seconds unconditionally, even when the IDE tab isn't focused or the git panel isn't visible. Each poll also triggers `git log` or `git branches` depending on the active tab, creating unnecessary load on the agent.

**Fix:** Pause polling when the panel isn't visible or the window is blurred. Use the existing file watcher WebSocket to trigger refreshes instead of blind polling.

---

### 11. `json.Marshal` Errors Swallowed in AI Streaming

**File:** `internal/ai/handler.go:145`

```go
data, _ := json.Marshal(event)
fmt.Fprintf(w, "data: %s\n\n", data)
```

And `internal/ai/executor.go:66`:

```go
data, _ := json.Marshal(formatFileEntries(entries))
return string(data), nil
```

While `json.Marshal` rarely fails for simple structs, ignoring errors is a bad practice that makes debugging difficult. If a custom type with a broken `MarshalJSON` is introduced later, this will silently produce corrupt output.

**Fix:** Check errors or at minimum add a comment documenting why they're safe to ignore.

---

### 12. File Upload Silently Truncated at 10MB

**File:** `cmd/agent/filehandler.go:87`

```go
data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20))
```

If the uploaded file exceeds 10MB, it is silently truncated — the first 10MB is written and the rest is discarded with no error to the client. The user believes their file was saved completely.

**Fix:** Read up to limit+1 bytes and check if there's more:

```go
const maxSize = 10 << 20
lr := io.LimitReader(r.Body, maxSize+1)
data, err := io.ReadAll(lr)
if len(data) > maxSize {
    writeErr(w, http.StatusRequestEntityTooLarge, "file exceeds 10MB limit")
    return
}
```

---

### 13. Terminal Resize Missing Upper Bounds

**File:** `cmd/agent/terminal.go:80`

```go
if resize.Cols > 0 && resize.Rows > 0 {
    pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(resize.Cols), Rows: uint16(resize.Rows)})
}
```

Only checks that values are positive, but doesn't enforce an upper bound. Extremely large values (e.g., 65535×65535) could cause excessive memory allocation in the terminal emulator.

**Fix:** Add reasonable upper bounds:

```go
if resize.Cols > 0 && resize.Rows > 0 && resize.Cols <= 500 && resize.Rows <= 500 {
```

---

### 14. Watcher Event Deduplication Missing

**File:** `cmd/agent/watcher.go`

Multiple filesystem events are emitted for a single user action (e.g., a file save produces `WRITE` + `CHMOD` events). Each event is broadcast to all connected WebSocket clients separately, causing the frontend to refresh the same directory multiple times in rapid succession.

**Fix:** Batch events within a short debounce window (e.g., 100ms) before broadcasting:

```go
case event := <-w.fsw.Events:
    // buffer event, reset 100ms timer
    // on timer fire, broadcast unique affected directories
```

---

### 15. Module-Level Singleton State in Router

**File:** `frontend/src/router/index.ts`

```ts
let initialized = false
```

A module-level boolean controls one-time initialization in the navigation guard. This is problematic for:

- Unit testing (state carries between tests)
- SSR (shared across requests)

**Fix:** Move initialization state into a Pinia store or router meta, which can be properly reset in tests.

---

### 16. No Request Body Size Limit on Workspace File Writes

**File:** `internal/workspace/handler.go` (HandleWriteFile)

```go
var req struct {
    Path    string `json:"path"`
    Content string `json:"content"`
}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
```

The `Content` field is a string that could be arbitrarily large. The global `MaxBytesReader` middleware (1 MB) caps the overall body, but file content may legitimately need to be larger. If the limit is raised or per-route overrides are added, this endpoint has no secondary validation.

**Fix:** Validate `len(req.Content)` before forwarding to the agent.

---

## Summary

| #  | Severity | Category | Issue |
|----|----------|----------|-------|
| 1  | Medium   | Security | Git log `count` param not validated on agent |
| 2  | Medium   | Security | AI API keys stored in plaintext |
| 3  | Medium   | Security | SameSite Lax allows GET-based CSRF |
| 4  | Medium   | Security | Preview cookie secret is ephemeral |
| 5  | Medium   | Dead Code | `pullImageIfNeeded` never called |
| 6  | Medium   | Bug | `push` without `--set-upstream` fails on first push |
| 7  | Low      | Bug | `discard` silently succeeds on untracked files |
| 8  | Low      | Code Quality | No action whitelist on server side |
| 9  | Low      | Code Quality | Agent client unbounded git response bodies |
| 10 | Low      | Code Quality | GitPanel 5-second polling with no debounce |
| 11 | Low      | Code Quality | `json.Marshal` errors swallowed |
| 12 | Low      | Code Quality | File upload silently truncated at 10MB |
| 13 | Low      | Code Quality | Terminal resize missing upper bounds |
| 14 | Low      | Code Quality | Watcher event deduplication missing |
| 15 | Low      | Code Quality | Module-level singleton state in router |
| 16 | Low      | Code Quality | No request body size limit on file writes |

---

## Feature: Workspace Database Services

Allow users to attach database containers (Postgres, MySQL, MongoDB, CouchDB) to workspaces. Each workspace gets its own Docker network for isolation, with sidecar database containers started/stopped alongside the primary workspace container.

### Phase 1: Workspace Network Isolation

Create a per-workspace Docker network (`devpad-net-{ID}`) and attach the workspace container to it. All existing functionality continues working, but workspaces are now network-isolated from each other.

**Changes:**
- Add `CreateNetwork`, `RemoveNetwork`, `ConnectToNetwork` methods to the container `Manager` interface
- Add `network_id` column to workspaces table (migration)
- Update workspace create: create network first, attach workspace container
- Update workspace delete: remove network after removing container
- Update workspace start/stop: handle network reconnection

**Status:** Not started

---

### Phase 2: Backend — Database Service Lifecycle

Add a `workspace_services` table and full CRUD for attaching database containers to workspaces. Services are managed alongside the workspace lifecycle.

**Changes:**
- New migration: `workspace_services` table (id, workspace_id, service_type, container_id, volume_name, status, config)
- New model, repository, and service layer for workspace services
- Extend container manager with sidecar lifecycle methods
- API endpoints: create/list/delete services for a workspace
- Wire into workspace start/stop/delete to cascade to attached services
- Inject connection info (host, port, default credentials) as env vars into workspace container

**Supported service types:** `postgres`, `mysql`, `mongodb`, `couchdb`

**Status:** Not started

---

### Phase 3: Frontend — Service Management UI

Add UI for managing database services attached to a workspace.

**Changes:**
- Service picker in workspace create/edit modal (select DB type)
- Service status display on workspace card or detail view
- Connection info panel (host, port, credentials) visible from the IDE
- Start/stop controls for individual services

**Status:** Not started
