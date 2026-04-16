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

### 4. No HTTP Client Timeout on Agent Client

**File:** `internal/agent/client.go:36-38`

```go
return &Client{
    baseURL: fmt.Sprintf("http://%s:%d", host, port),
    http:    &http.Client{},
}
```

The HTTP client has no timeout configured. If a workspace agent hangs, becomes unresponsive, or is deliberately slow, the main Devpad server thread blocks indefinitely waiting for a response. This is a denial-of-service vector — a single malicious workspace can exhaust server goroutines.

**Fix:** Add a timeout:

```go
http: &http.Client{
    Timeout: 30 * time.Second,
},
```

---

### 5. ~~Error Discarded After Login~~ ✅ RESOLVED (fixed alongside issue #2)

**File:** `internal/auth/handler.go`

**Was:** After creating a session, `ValidateSession` was called with the error discarded (`user, _ := ...`). If it failed, a nil user would be serialized.

**Fix applied:** The error is now checked; if `ValidateSession` fails or returns nil, the handler returns a 500 error.

**Fix:** Check the error and handle it:

```go
user, err := h.service.ValidateSession(r.Context(), session.Token)
if err != nil || user == nil {
    writeError(w, http.StatusInternalServerError, "login succeeded but failed to load user")
    return
}
```

---

### 6. No Rate Limiting on Authentication

**Files:** `internal/auth/handler.go` (HandleLogin, HandleSetup), `internal/settings/handler.go` (TOTP endpoints)

There is no rate limiting on any authentication endpoint. This allows:

- **Password brute-force:** Unlimited login attempts per username.
- **TOTP brute-force:** 6-digit TOTP codes have only 1,000,000 possible values. At even modest request rates, an attacker can exhaust all codes within the 30-second TOTP window.
- **Setup endpoint abuse:** If the setup check has a race condition, multiple admin accounts could be created.

**Fix:** Implement rate limiting middleware (per-IP and per-username) on `/api/auth/login`, `/api/auth/setup`, and any MFA endpoints. Consider using a token bucket or sliding window algorithm. Lock accounts after N failed attempts.

---

### 7. Preview Iframe Sandbox Too Permissive

**File:** `frontend/src/components/ide/PreviewPanel.vue`

```html
sandbox="allow-scripts allow-same-origin allow-forms allow-popups allow-modals"
```

The combination of `allow-scripts` and `allow-same-origin` together **completely negates sandbox isolation**. Scripts inside the iframe can:

- Access the parent page's cookies and DOM
- Make authenticated API requests as the logged-in user
- Modify or read workspace files

This is a well-documented browser security pitfall. See [MDN sandbox docs](https://developer.mozilla.org/en-US/docs/Web/HTML/Element/iframe#sandbox).

**Fix:** Remove `allow-same-origin`. The preview feature already uses a separate preview domain, so the iframe shouldn't need same-origin access. If previews break without it, that indicates they're relying on parent-page resources, which should be fixed independently.

---

### 8. Unbounded JSON Request Body Parsing

**Files:** All handlers that decode JSON — `internal/auth/handler.go`, `internal/workspace/handler.go`, `internal/ai/handler.go`, `internal/settings/handler.go`, `internal/preview/handler.go`, `internal/admin/handler.go`

Every handler decodes request bodies without size limits:

```go
json.NewDecoder(r.Body).Decode(&req)
```

An attacker can send a multi-gigabyte JSON body to exhaust server memory and cause an out-of-memory crash.

**Fix:** Wrap `r.Body` with `http.MaxBytesReader` at each handler or via middleware:

```go
r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB limit
```

Or apply globally in middleware:

```go
func MaxBodySize(maxBytes int64) func(http.Handler) http.Handler {
    return func(next http.Handler) http.Handler {
        return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
            r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
            next.ServeHTTP(w, r)
        })
    }
}
```

---

## Medium

### 9. Expired Sessions Never Cleaned Up

**File:** `internal/auth/session_repository.go:82-87`

```go
func (r *sessionRepository) DeleteExpired(ctx context.Context) error {
    _, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, time.Now())
    // ...
}
```

The `DeleteExpired` method exists and is correctly implemented, but it is never called anywhere in the codebase. Expired sessions accumulate in the database indefinitely, growing the `sessions` table without bound.

**Fix:** Add a periodic cleanup goroutine in the server startup:

```go
go func() {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()
    for range ticker.C {
        if err := sessionRepo.DeleteExpired(context.Background()); err != nil {
            log.Printf("session cleanup: %v", err)
        }
    }
}()
```

---

### 10. Expired Preview Tokens Never Cleaned Up

**File:** `internal/preview/repository.go`

Same issue as sessions. The `DeleteExpired` method exists for preview tokens but is never called. Stale tokens accumulate forever.

**Fix:** Same approach — periodic cleanup goroutine.

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

### 16. Agent fsnotify Watcher Never Closed

**File:** `cmd/agent/watcher.go:33`

The `fsnotify.NewWatcher()` is created when the agent starts but `Close()` is never called. Each agent process leaks inotify file descriptors for the lifetime of the process. On Linux, the default inotify limit (`/proc/sys/fs/inotify/max_user_watches`) can be exhausted if many watchers are created.

**Fix:** Add a signal handler in `cmd/agent/main.go` to gracefully shut down:

```go
sigCh := make(chan os.Signal, 1)
signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
go func() {
    <-sigCh
    watcher.Close()
    os.Exit(0)
}()
```

---

### 17. Frontend Timer Leak in FileExplorer

**File:** `frontend/src/components/ide/FileExplorer.vue`

The `refreshTimers` Map stores debounce `setTimeout` IDs but is never cleaned up in `onUnmounted()`. When the IdeView navigates away and the FileExplorer is destroyed, pending timers continue to fire, making API calls against a destroyed component.

**Fix:** Add cleanup:

```ts
onUnmounted(() => {
    refreshTimers.forEach((timer) => clearTimeout(timer))
    refreshTimers.clear()
})
```

---

### 18. Editor Race Condition on Rapid File Switching

**File:** `frontend/src/components/ide/EditorPanel.vue`

The watcher on `props.filePath` triggers an async file load (`workspaceApi.readFile(...)`) without cancelling any in-flight request. If the user clicks through files quickly:

1. File A load starts (takes 500ms)
2. File B load starts (takes 100ms)
3. File B load completes → editor shows file B
4. File A load completes → editor shows file A (wrong!)

**Fix:** Use `AbortController` to cancel the previous request when a new file is selected:

```ts
let abortController: AbortController | null = null

watch(() => props.filePath, async (newPath) => {
    abortController?.abort()
    abortController = new AbortController()
    try {
        const content = await workspaceApi.readFile(wsId, newPath, abortController.signal)
        // set editor content
    } catch (e) {
        if (e instanceof DOMException && e.name === 'AbortError') return
        // handle real error
    }
})
```

---

### 19. Goroutine Leak in Terminal WebSocket Proxy

**File:** `internal/workspace/terminal.go:59-83`

Two goroutines bidirectionally proxy WebSocket messages between the client and the agent:

```go
wg.Add(1)
go func() {
    defer wg.Done()
    for {
        msgType, msg, err := agentConn.ReadMessage()
        if err != nil {
            return
        }
        clientConn.WriteMessage(msgType, msg)
    }
}()
```

If one goroutine encounters an error and returns, the other continues blocking on `ReadMessage()` indefinitely. The `wg.Wait()` also blocks, keeping the handler goroutine alive.

**Fix:** Use a shared cancellation mechanism:

```go
ctx, cancel := context.WithCancel(r.Context())
defer cancel()

go func() {
    defer cancel()
    // read from agent, write to client
}()

go func() {
    defer cancel()
    // read from client, write to agent
}()

<-ctx.Done()
```

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

### 23. No Graceful Shutdown in Agent

**File:** `cmd/agent/main.go`

The agent's `main()` calls `http.ListenAndServe` and has no signal handling. When the container receives `SIGTERM` (e.g., during `docker stop`):

- Active WebSocket connections are abruptly severed
- The fsnotify watcher is never closed (see issue #16)
- Pending file writes may be interrupted
- No cleanup of PTY processes

**Fix:** Use `http.Server` with `Shutdown()` and a signal handler:

```go
srv := &http.Server{Addr: ":9100", Handler: mux}

go func() {
    sigCh := make(chan os.Signal, 1)
    signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
    <-sigCh
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()
    srv.Shutdown(ctx)
}()

srv.ListenAndServe()
```

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
| 4  | High     | Security | No HTTP client timeout on agent client | Open |
| 5  | High     | Security | Error discarded after login → nil panic | ✅ Resolved |
| 6  | High     | Security | No rate limiting on authentication | Open |
| 7  | High     | Security | Preview iframe sandbox too permissive | Open |
| 8  | High     | Security | Unbounded JSON request body parsing | Open |
| 9  | Medium   | Security | Expired sessions never cleaned up | Open |
| 10 | Medium   | Security | Expired preview tokens never cleaned up | Open |
| 11 | Medium   | Dead Code | `pullImageIfNeeded` never called | Open |
| 12 | Medium   | Security | AI API keys stored in plaintext | Open |
| 13 | Medium   | Security | Internal error details leaked to clients | Open |
| 14 | Medium   | Security | SameSite Lax allows GET-based CSRF | Open |
| 15 | Medium   | Security | Preview cookie secret is ephemeral | Open |
| 16 | Medium   | Resource Leak | Agent fsnotify watcher never closed | Open |
| 17 | Medium   | Resource Leak | Frontend timer leak in FileExplorer | Open |
| 18 | Medium   | Resource Leak | Editor race condition on rapid file switching | Open |
| 19 | Medium   | Resource Leak | Goroutine leak in terminal WebSocket proxy | Open |
| 20 | Low      | Code Quality | `json.Marshal` errors swallowed | Open |
| 21 | Low      | Code Quality | File upload silently truncated at 10MB | Open |
| 22 | Low      | Code Quality | Terminal resize missing upper bounds | Open |
| 23 | Low      | Code Quality | No graceful shutdown in agent | Open |
| 24 | Low      | Code Quality | Watcher event deduplication missing | Open |
| 25 | Low      | Code Quality | Module-level singleton state in router | Open |
| 26 | Low      | Code Quality | No request body size limit on file writes | Open |
