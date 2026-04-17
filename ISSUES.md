# Devpad — Code Review Issues

Comprehensive review covering security flaws, resource leaks, dead code, and code quality issues.

---

## Critical

### 1. ~~WebSocket CSRF — No Origin Validation~~ ✅ RESOLVED

**Files:** `internal/workspace/terminal.go`, `internal/workspace/handler.go`, `internal/server/server.go`

**Was:** The WebSocket upgrader accepted connections from any origin (`CheckOrigin` always returned `true`), allowing cross-site WebSocket hijacking of terminal sessions.

**Fix applied:**
- Replaced the package-level `upgrader` variable with an `upgrader()` method on `Handler` that validates the `Origin` header against a configured allowlist.
- `Handler` now accepts `allowedOrigins []string` in its constructor.
- `server.go` builds the origin list from `Config.Domain` (production: `https://domain`) or defaults to `http://localhost:{port}` and `http://127.0.0.1:{port}` (development).
- Requests with no `Origin` header (same-origin browser requests) are still permitted.
- Mismatched origins are rejected and logged.

---

### 2. ~~Session Cookie Missing `Secure` Flag~~ ✅ RESOLVED

**Files:** `internal/auth/handler.go`, `internal/server/server.go`

**Was:** The session cookie never set the `Secure` flag, allowing it to be transmitted over plaintext HTTP even when the server ran in HTTPS mode.

**Fix applied:**
- `auth.Handler` now accepts a `secureCookie bool` parameter, set to `true` when `cfg.Domain != ""` (HTTPS mode).
- Both the login and logout `Set-Cookie` calls now include `Secure: h.secureCookie`.
- Also fixed the swallowed error on `ValidateSession` after login (see issue #5) — the error is now checked and a 500 is returned if session validation fails.

---

### 3. ~~Agent Path Traversal via Symlinks~~ ✅ RESOLVED

**File:** `cmd/agent/filehandler.go`

**Was:** `validatePath()` used `filepath.Clean` and a prefix check but never resolved symlinks. A symlink inside `/workspace` pointing outside could bypass the path restriction.

**Fix applied:**
- `validatePath()` now calls `filepath.EvalSymlinks()` after cleaning to resolve the real path.
- For paths that don't exist yet (new file writes), it resolves the parent directory instead.
- If `os.Lstat` returns an unexpected error (e.g., permission denied), the request is rejected rather than falling through.
- The function returns the resolved path so callers operate on the real filesystem location, not the symlink.

---

## High

### 4. ~~No HTTP Client Timeout on Agent Client~~ ✅ RESOLVED

**File:** `internal/agent/client.go`

**Was:** The HTTP client had no timeout configured, allowing a hanging agent to block Devpad server goroutines indefinitely.

**Fix applied:** Added `Timeout: 3 * time.Minute` to the `http.Client` in `NewClient`.

---

### 5. ~~Error Discarded After Login~~ ✅ RESOLVED (fixed alongside issue #2)

**File:** `internal/auth/handler.go`

**Was:** After creating a session, `ValidateSession` was called with the error discarded (`user, _ := ...`). If it failed, a nil user would be serialized.

**Fix applied:** The error is now checked; if `ValidateSession` fails or returns nil, the handler returns a 500 error.

---

### 6. ~~No Rate Limiting on Authentication~~ ✅ RESOLVED

**Files:** `internal/auth/ratelimit.go`, `internal/server/server.go`

**Was:** No rate limiting on authentication endpoints, allowing brute-force attacks on passwords and TOTP codes.

**Fix applied:**
- Created `RateLimiter` in `internal/auth/ratelimit.go` using `golang.org/x/time/rate` with per-IP token bucket tracking and automatic visitor cleanup.
- Applied rate limiting middleware to `POST /api/auth/login`, `POST /api/auth/setup`, `POST /api/settings/mfa/enable`, and `POST /api/settings/mfa/disable` in `server.go`.
- Excess requests receive `429 Too Many Requests`.

---

### 7. ~~Preview Iframe Sandbox Too Permissive~~ ✅ RESOLVED

**File:** `frontend/src/components/ide/PreviewPanel.vue`

**Was:** The iframe sandbox included both `allow-scripts` and `allow-same-origin`, which negates sandbox isolation entirely.

**Fix applied:** Removed `allow-same-origin` from the sandbox attribute. The preview uses a separate domain, so same-origin access is not needed.

---

### 8. ~~Unbounded JSON Request Body Parsing~~ ✅ RESOLVED

**Files:** `internal/server/server.go`

**Was:** All handlers decoded JSON request bodies without size limits, allowing memory exhaustion via multi-gigabyte payloads.

**Fix applied:** Added a global `maxBodySize` middleware in `server.go` that wraps `r.Body` with `http.MaxBytesReader(w, r.Body, 1<<20)` (1 MB limit) for all incoming requests.

---

## Medium

### 9. ~~Expired Sessions Never Cleaned Up~~ ✅ RESOLVED

**File:** `internal/server/server.go`

**Was:** The `DeleteExpired` method existed in `session_repository.go` but was never called. Expired sessions accumulated in the database indefinitely.

**Fix applied:** Added a periodic cleanup goroutine in `server.New()` that runs every hour, calling `sessionRepo.DeleteExpired()` and `previewRepo.DeleteExpired()`. The goroutine is cancelled gracefully on server shutdown via a `context.WithCancel`.

---

### 10. ~~Expired Preview Tokens Never Cleaned Up~~ ✅ RESOLVED

**File:** `internal/server/server.go`

**Was:** The `DeleteExpired` method existed in `preview/repository.go` but was never called. Stale tokens accumulated forever.

**Fix applied:** Handled in the same cleanup goroutine as issue #9 — expired preview tokens are purged every hour alongside expired sessions.

---

### 11. Dead Code: `pullImageIfNeeded`

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

### 12. AI Provider API Keys Stored in Plaintext

**File:** `internal/ai/repository.go`

API keys for AI providers (Mistral, MiniMax, etc.) are stored directly in the SQLite database as plain text. Anyone with read access to the database file (e.g., via a backup, a path traversal bug, or filesystem access) can extract all API keys.

**Fix:** Encrypt API keys at rest using AES-GCM with a key derived from a server secret (e.g., via `DEVPAD_ENCRYPTION_KEY` environment variable). Decrypt only when needed for API calls.

---

### 13. Internal Error Details Leaked to Clients

**File:** `internal/ai/handler.go:176`

```go
writeError(w, http.StatusInternalServerError, fmt.Sprintf("failed to start chat: %v", err))
```

The raw error message (which may contain database errors, file paths, connection strings, or internal state) is returned to the client in the HTTP response body. This leaks implementation details to potential attackers.

Similar patterns exist in other handlers where `err.Error()` is included in client-facing responses.

**Fix:** Log the detailed error server-side, return a generic message to the client:

```go
log.Printf("failed to start chat: %v", err)
writeError(w, http.StatusInternalServerError, "failed to start chat")
```

---

### 14. SameSite Lax Allows GET-Based CSRF

**File:** `internal/auth/handler.go:111`

```go
SameSite: http.SameSiteLaxMode,
```

`SameSite: Lax` allows cookies to be sent on top-level GET navigations from cross-origin sites. If any state-changing endpoint accepts GET requests (either intentionally or via permissive routing), cross-site requests can trigger them.

**Fix:** Verify at the router level that all mutation endpoints only accept POST/PUT/DELETE. Consider upgrading to `SameSite: Strict` if the UX impact is acceptable, or add explicit CSRF tokens for state-changing operations.

---

### 15. Preview Cookie Secret Is Ephemeral

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

## Resource Leaks

### 16. ~~Agent fsnotify Watcher Never Closed~~ ✅ RESOLVED

**Files:** `cmd/agent/watcher.go`, `cmd/agent/main.go`

**Was:** The `fsnotify.NewWatcher()` was created when the agent started but `Close()` was never called, leaking inotify file descriptors.

**Fix applied:**
- Added a `Close()` method to the `watcher` struct that delegates to `fsw.Close()`.
- Replaced bare `http.ListenAndServe` with `http.Server` and added a signal handler for `SIGTERM`/`SIGINT` that closes the watcher and gracefully shuts down the HTTP server.

---

### 17. ~~Frontend Timer Leak in FileExplorer~~ ✅ RESOLVED

**File:** `frontend/src/components/ide/FileExplorer.vue`

**Was:** The `refreshTimers` Map was never cleaned up in `onUnmounted()`, so pending timers continued to fire after the component was destroyed.

**Fix applied:** Added an `onUnmounted` hook that clears all pending debounce timers and empties the map.

---

### 18. ~~Editor Race Condition on Rapid File Switching~~ ✅ RESOLVED

**Files:** `frontend/src/components/ide/EditorPanel.vue`, `frontend/src/api/workspaces.ts`

**Was:** The watcher on `props.filePath` triggered an async file load without cancelling in-flight requests, causing a race condition where a slow earlier load could overwrite a faster later load.

**Fix applied:**
- Added an optional `AbortSignal` parameter to `workspaceApi.readFile()`.
- The file-loading watcher now creates a new `AbortController` for each load and aborts the previous one.
- `AbortError` exceptions are silently caught. The `loading` state is only cleared if the request wasn't aborted.
- The controller is also aborted in `onUnmounted` for cleanup.

---

### 19. ~~Goroutine Leak in Terminal WebSocket Proxy~~ ✅ RESOLVED

**File:** `internal/workspace/terminal.go`

**Was:** Two goroutines bidirectionally proxied WebSocket messages, but if one exited on error the other continued blocking on `ReadMessage()` indefinitely, leaking the goroutine and handler.

**Fix applied:**
- Replaced `sync.WaitGroup` with a shared `done` channel. Each goroutine defers `close(done)` so the first to exit signals the other.
- After `<-done`, both connections are explicitly closed, unblocking any pending `ReadMessage()` in the other goroutine.
- Applied the same fix to `HandleWatch` which had the identical pattern.
- Removed the unused `sync` import.

---

## Code Quality

### 20. `json.Marshal` Errors Swallowed in AI Streaming

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

### 21. File Upload Silently Truncated at 10MB

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

### 22. Terminal Resize Missing Upper Bounds

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

### 23. ~~No Graceful Shutdown in Agent~~ ✅ RESOLVED

**File:** `cmd/agent/main.go`

**Was:** The agent's `main()` called `http.ListenAndServe` with no signal handling, so `SIGTERM` would abruptly sever connections and skip cleanup.

**Fix applied:** Resolved as part of issue #16 — the agent now uses `http.Server` with `Shutdown()` and a `SIGINT`/`SIGTERM` signal handler that closes the fsnotify watcher and gracefully drains connections.

---

### 24. Watcher Event Deduplication Missing

**File:** `cmd/agent/watcher.go`

Multiple filesystem events are emitted for a single user action (e.g., a file save produces `WRITE` + `CHMOD` events). Each event is broadcast to all connected WebSocket clients separately, causing the frontend to refresh the same directory multiple times in rapid succession.

**Fix:** Batch events within a short debounce window (e.g., 100ms) before broadcasting:

```go
case event := <-w.fsw.Events:
    // buffer event, reset 100ms timer
    // on timer fire, broadcast unique affected directories
```

---

### 25. Module-Level Singleton State in Router

**File:** `frontend/src/router/index.ts`

```ts
let initialized = false
```

A module-level boolean controls one-time initialization in the navigation guard. This is problematic for:

- Unit testing (state carries between tests)
- SSR (shared across requests)

**Fix:** Move initialization state into a Pinia store or router meta, which can be properly reset in tests.

---

### 26. No Request Body Size Limit on Workspace File Writes

**File:** `internal/workspace/handler.go` (HandleWriteFile)

```go
var req struct {
    Path    string `json:"path"`
    Content string `json:"content"`
}
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
```

The `Content` field is a string that could be arbitrarily large. Unlike the agent's 10MB limit on direct file uploads, the main server's JSON-based write endpoint has no size restriction at all. Combined with issue #8 (no `MaxBytesReader`), a single request can exhaust server memory.

**Fix:** Apply `MaxBytesReader` (issue #8) and additionally validate `len(req.Content)` before forwarding to the agent.

---

## Summary

| #  | Severity | Category | Issue | Status |
|----|----------|----------|-------|--------|
| 1  | Critical | Security | WebSocket CSRF — no origin validation | ✅ Resolved |
| 2  | Critical | Security | Session cookie missing `Secure` flag | ✅ Resolved |
| 3  | Critical | Security | Agent path traversal via symlinks | ✅ Resolved |
| 4  | High     | Security | No HTTP client timeout on agent client | ✅ Resolved |
| 5  | High     | Security | Error discarded after login → nil panic | ✅ Resolved |
| 6  | High     | Security | No rate limiting on authentication | ✅ Resolved |
| 7  | High     | Security | Preview iframe sandbox too permissive | ✅ Resolved |
| 8  | High     | Security | Unbounded JSON request body parsing | ✅ Resolved |
| 9  | Medium   | Security | Expired sessions never cleaned up | ✅ Resolved |
| 10 | Medium   | Security | Expired preview tokens never cleaned up | ✅ Resolved |
| 11 | Medium   | Dead Code | `pullImageIfNeeded` never called | Open |
| 12 | Medium   | Security | AI API keys stored in plaintext | Open |
| 13 | Medium   | Security | Internal error details leaked to clients | Open |
| 14 | Medium   | Security | SameSite Lax allows GET-based CSRF | Open |
| 15 | Medium   | Security | Preview cookie secret is ephemeral | Open |
| 16 | Medium   | Resource Leak | Agent fsnotify watcher never closed | ✅ Resolved |
| 17 | Medium   | Resource Leak | Frontend timer leak in FileExplorer | ✅ Resolved |
| 18 | Medium   | Resource Leak | Editor race condition on rapid file switching | ✅ Resolved |
| 19 | Medium   | Resource Leak | Goroutine leak in terminal WebSocket proxy | ✅ Resolved |
| 20 | Low      | Code Quality | `json.Marshal` errors swallowed | Open |
| 21 | Low      | Code Quality | File upload silently truncated at 10MB | Open |
| 22 | Low      | Code Quality | Terminal resize missing upper bounds | Open |
| 23 | Low      | Code Quality | No graceful shutdown in agent | ✅ Resolved |
| 24 | Low      | Code Quality | Watcher event deduplication missing | Open |
| 25 | Low      | Code Quality | Module-level singleton state in router | Open |
| 26 | Low      | Code Quality | No request body size limit on file writes | Open |
