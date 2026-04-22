# Chat History Design

## 1. Context & Problem Statement

`AiAgentPanel.vue` holds all conversation state in component-local `ref` arrays that are discarded on unmount. There is no persistence layer for conversations. Users lose context when they navigate away from a workspace, refresh, or close the browser.

The goal is to persist AI chat conversations scoped per user and per workspace, so a user can return to a workspace and resume or review previous sessions. The design must keep the provider message round-trip correct — especially for providers that use opaque `ThinkingState json.RawMessage` blobs (MiniMax, Moonshot) and for the agentic loop which accumulates `role: "tool"` and `role: "assistant"` messages with `tool_calls`.

**Files directly affected:**
- `internal/ai/repository.go` — extend the existing repository interface
- `internal/ai/service.go` — add conversation service methods
- `internal/ai/handler.go` — add new HTTP handlers
- `internal/server/server.go` — register new routes
- `frontend/src/api/ai.ts` — add conversation API calls
- `frontend/src/components/ide/AiAgentPanel.vue` — conversation list/selection UI and auto-save
- New: `internal/database/migrations/015_chat_history.sql`
- New: `frontend/src/stores/chatHistory.ts`

---

## 2. Current Architecture Analysis

### What works well
- The `Repository` interface in `ai/repository.go` cleanly separates persistence from business logic. Adding conversation methods here follows the established pattern.
- `Message` in `provider.go` already has the right shape: `Role`, `Content`, `ReasoningContent`, `ThinkingState json.RawMessage`, `ToolCalls []ToolCall`, `ToolCallID`. This is exactly what must survive the round-trip.
- The `Service` interface is the right place to add conversation CRUD — it already owns `ChatStream` and has access to the repository.
- The `Handler` already receives a user from context via `auth.UserFromContext`. Ownership scoping is straightforward.

### Architectural issues to address

**Issue 1 — The `Repository` interface conflates two concerns.** It currently only stores `ProviderConfig`. Adding conversation methods to the same interface violates the Interface Segregation Principle: a component that only needs to read provider config would now also depend on conversation query methods. The fix is to introduce a second, focused interface: `ConversationRepository`.

**Issue 2 — `AiAgentPanel.vue` mixes display metadata with API-facing message shape.** The component builds `DisplayMessage` (with `segments: MessageSegment[]`) from the stream and separately builds a flat `ChatMessage[]` for the API payload. The display segments (tool cards, approval prompts, plan state) are UI artifacts only — they must NOT be stored as-is. Storage holds raw `Message[]` only. Display segments are reconstructed on load.

**Issue 3 — `handleAgentChat` prepends the system prompt and appends messages locally.** When restoring a conversation the stored messages must NOT include the system prompt (it is injected by the handler each time). The system message row must be excluded from persistence and excluded from the frontend's stored slice.

**Issue 4 — Body size limit.** `server.go` applies a 1MB `maxBodySize` middleware globally. Conversations with many tool results can exceed this. The `SaveMessages` endpoint sends the full message slice. The body limit must be raised or exempted for that specific endpoint (see Section 8).

---

## 3. Proposed Design

### 3.1 Database Schema

**Migration: `015_chat_history.sql`**

```sql
CREATE TABLE ai_conversations (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workspace_id INTEGER NOT NULL REFERENCES workspaces(id) ON DELETE CASCADE,
    title        TEXT    NOT NULL DEFAULT '',
    model        TEXT    NOT NULL DEFAULT '',
    created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE INDEX idx_ai_conversations_user_workspace
    ON ai_conversations(user_id, workspace_id);

CREATE TABLE ai_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    conversation_id INTEGER NOT NULL REFERENCES ai_conversations(id) ON DELETE CASCADE,
    position        INTEGER NOT NULL,
    role            TEXT    NOT NULL,
    content         TEXT    NOT NULL DEFAULT '',
    reasoning_content TEXT  NOT NULL DEFAULT '',
    thinking_state  TEXT    NOT NULL DEFAULT '',  -- JSON blob, stored as TEXT
    tool_calls      TEXT    NOT NULL DEFAULT '',  -- JSON array, stored as TEXT
    tool_call_id    TEXT    NOT NULL DEFAULT ''
);

CREATE INDEX idx_ai_messages_conversation
    ON ai_messages(conversation_id, position);
```

**Design notes:**
- `thinking_state` is stored as TEXT (the raw JSON string). When unmarshalled back into `json.RawMessage` it round-trips perfectly — no re-encoding needed.
- `tool_calls` is stored as a JSON array TEXT (marshalled `[]ToolCall`). Empty means `NULL`-equivalent (`''`).
- `position` is an explicit ordering column (integer, 0-based). Do not rely on `id` for ordering as bulk inserts in a single transaction may not guarantee insertion order if a gap ever appears.
- The `system` role message injected by `HandleAgentChat` is NOT stored. Only user-visible roles (`user`, `assistant`, `tool`) are persisted.
- `title` on `ai_conversations` starts empty and is auto-derived (see Section 3.3).

### 3.2 Go: New Types

In `internal/ai/repository.go` (or a new file `internal/ai/conversation_repository.go`):

```
// Conversation is a named chat session.
type Conversation struct {
    ID          int64
    UserID      int64
    WorkspaceID int64
    Title       string
    Model       string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

// StoredMessage is a persisted Message with its ordering position.
// It maps directly to a row in ai_messages.
type StoredMessage struct {
    ID             int64
    ConversationID int64
    Position       int
    Message                   // embedded: Role, Content, ReasoningContent, ThinkingState, ToolCalls, ToolCallID
}
```

### 3.3 Go: `ConversationRepository` Interface

New interface, separate from the existing `Repository`:

```
type ConversationRepository interface {
    CreateConversation(ctx context.Context, conv *Conversation) error
    // CreateConversation sets conv.ID on success.

    GetConversation(ctx context.Context, id, userID int64) (*Conversation, error)
    // Returns nil, nil if not found. userID scoping prevents cross-user access.

    ListConversations(ctx context.Context, userID, workspaceID int64) ([]Conversation, error)
    // Ordered by updated_at DESC.

    UpdateConversation(ctx context.Context, id, userID int64, title, model string) error

    DeleteConversation(ctx context.Context, id, userID int64) error

    SaveMessages(ctx context.Context, conversationID int64, messages []Message) error
    // Replaces all messages for the conversation in a single transaction.
    // Caller is responsible for excluding the system prompt.

    GetMessages(ctx context.Context, conversationID, userID int64) ([]Message, error)
    // Returns messages ordered by position ASC. userID validated via JOIN on ai_conversations.
}
```

**Implementation notes for `SaveMessages`:**

The implementation must DELETE existing rows for `conversation_id` and re-INSERT the full slice in order within a single transaction. This is simpler and more correct than diffing. The `position` column is set to the slice index. `ThinkingState` is marshalled to string with `string(msg.ThinkingState)` (it is already valid JSON). `ToolCalls` is marshalled with `json.Marshal(msg.ToolCalls)`.

**Implementation notes for `GetMessages`:**

`ThinkingState` is unmarshalled by casting `[]byte(row.thinkingStateStr)` to `json.RawMessage` directly — no additional JSON parse needed. `ToolCalls` is unmarshalled with `json.Unmarshal`. Empty strings produce zero-value slices (nil / empty `json.RawMessage`), which is correct.

### 3.4 Go: `ConversationService` Interface

Add to `internal/ai/service.go` as a second interface (keeping `Service` unchanged):

```
type ConversationService interface {
    CreateConversation(ctx context.Context, userID, workspaceID int64, model string) (*Conversation, error)
    // Generates a blank title; title is updated after the first assistant turn.

    ListConversations(ctx context.Context, userID, workspaceID int64) ([]Conversation, error)

    GetConversation(ctx context.Context, id, userID int64) (*Conversation, error)

    DeleteConversation(ctx context.Context, id, userID int64) error

    SaveMessages(ctx context.Context, conversationID, userID int64, messages []Message) error
    // Validates ownership via GetConversation before delegating to repository.
    // Updates conversations.updated_at and derives title if it is still empty
    // (uses the content of the first user message, truncated to 60 chars).

    GetMessages(ctx context.Context, conversationID, userID int64) ([]Message, error)
}
```

The `service` struct gains a `convRepo ConversationRepository` field. The constructor becomes:

```
func NewService(repo Repository, convRepo ConversationRepository, providers ...Provider) Service
```

A separate concrete type `conversationService` implements `ConversationService`, receiving `ConversationRepository`:

```
func NewConversationService(convRepo ConversationRepository) ConversationService
```

This keeps the existing `Service` interface clean (OCP — new capability via extension, not modification) and satisfies ISP by not forcing callers of `Service` to depend on conversation methods.

### 3.5 Go: New HTTP Handlers on `Handler`

`Handler` gains a `convService ConversationService` field. `NewHandler` signature becomes:

```
func NewHandler(service Service, convService ConversationService, executor ToolExecutor) *Handler
```

New handler methods:

```
HandleListConversations(w, r)
// GET /api/ai/conversations?workspaceId=<id>
// Returns []Conversation for the authenticated user scoped to workspaceId.

HandleCreateConversation(w, r)
// POST /api/ai/conversations
// Body: { workspaceId: int, model: string }
// Creates and returns a Conversation.

HandleDeleteConversation(w, r)
// DELETE /api/ai/conversations/{id}
// Deletes the conversation (and cascades to messages via FK).

HandleGetMessages(w, r)
// GET /api/ai/conversations/{id}/messages
// Returns []Message for the conversation.

HandleSaveMessages(w, r)
// PUT /api/ai/conversations/{id}/messages
// Body: { messages: ChatMessage[] }
// Full replacement save. Called by the frontend after each completed turn.
```

All new handlers call `auth.UserFromContext` and enforce user ownership.

### 3.6 Route Registration

In `server.go`, `registerRoutes` adds:

```
mux.Handle("GET /api/ai/conversations",
    authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleListConversations)))
mux.Handle("POST /api/ai/conversations",
    authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleCreateConversation)))
mux.Handle("DELETE /api/ai/conversations/{id}",
    authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleDeleteConversation)))
mux.Handle("GET /api/ai/conversations/{id}/messages",
    authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleGetMessages)))
mux.Handle("PUT /api/ai/conversations/{id}/messages",
    authMiddleware.RequireAuth(http.HandlerFunc(aiHandler.HandleSaveMessages)))
```

`SaveMessages` must be excluded from the 1MB body cap (see Section 8). The recommended approach is to apply `maxBodySize` as middleware on individual handlers rather than globally, or to exempt the pattern `PUT /api/ai/conversations/` with a higher limit (e.g., 10MB).

### 3.7 DI Wiring in `server.go`

```
aiRepo     := ai.NewRepository(db.Conn())
aiConvRepo := ai.NewConversationRepository(db.Conn())
aiService  := ai.NewService(aiRepo, aiConvRepo, ...providers)
aiConvSvc  := ai.NewConversationService(aiConvRepo)
aiHandler  := ai.NewHandler(aiService, aiConvSvc, toolExecutor)
```

`NewConversationRepository` is a new constructor returning `ConversationRepository`. The existing `repository` struct can implement both `Repository` and `ConversationRepository` (since it already holds `*sql.DB`), or they can be separate structs. Separate structs are preferred for clarity: `repository` implements `Repository`; `conversationRepository` implements `ConversationRepository`.

---

### 3.8 Frontend Design

#### New Pinia Store: `frontend/src/stores/chatHistory.ts`

```typescript
interface Conversation {
  id: number
  workspaceId: number
  title: string
  model: string
  createdAt: string
  updatedAt: string
}

// Store shape
const useConversationStore = defineStore('chatHistory', () => {
  const conversations = ref<Conversation[]>([])
  const activeConversationId = ref<number | null>(null)

  // Actions:
  // fetchConversations(workspaceId: number): Promise<void>
  // createConversation(workspaceId: number, model: string): Promise<Conversation>
  // deleteConversation(id: number): Promise<void>
  // setActive(id: number | null): void
})
```

State is per-store instance; different workspace tabs have different `AiAgentPanel` instances so there is no cross-workspace collision.

#### New API methods in `frontend/src/api/ai.ts`

```typescript
// Add to aiApi:

listConversations(workspaceId: number): Promise<{ conversations: Conversation[] }>

createConversation(workspaceId: number, model: string): Promise<{ conversation: Conversation }>

deleteConversation(id: number): Promise<void>

getMessages(conversationId: number): Promise<{ messages: ChatMessage[] }>

saveMessages(conversationId: number, messages: ChatMessage[]): Promise<void>
```

The `Conversation` TypeScript interface mirrors the Go `Conversation` struct (camelCase fields).

#### Changes to `AiAgentPanel.vue`

**New props / state:**

The component already receives `workspaceId: number` as a prop. Add:

```typescript
// Internal state additions
const activeConversationId = ref<number | null>(null)
const conversationStore = useConversationStore()
const historyOpen = ref(false)  // toggles the conversation list panel
```

**Lifecycle: `onMounted`**

After loading models, call `conversationStore.fetchConversations(props.workspaceId)`. Do NOT auto-resume the most recent conversation — start with an empty state (current behavior). The user explicitly selects a past conversation.

**`newChat()` changes:**

```
function newChat() {
  if (streaming.value) abortController.value?.abort()
  messages.value = []
  inputValue.value = ''
  planExpanded.value = false
  resetInputHeight()
  activeConversationId.value = null  // <-- clears the active session
}
```

**Auto-save after each completed turn:**

At the end of `sendMessage`, in the `finally` block (after streaming completes and `streaming.value = false`), call `saveCurrentConversation()`. This function:

1. If `activeConversationId.value` is null, call `aiApi.createConversation(workspaceId, selectedModel.value)` to get a new conversation ID, then set `activeConversationId.value`. Refresh the conversation list.
2. Build the `ChatMessage[]` payload from `messages.value` (the same transformation already used to build the API payload — mapping `DisplayMessage` fields to `ChatMessage`, excluding display-only segments). Include ALL roles: user, assistant (with reasoning, thinking state, tool_calls), and the tool messages that were added to the in-memory slice during streaming. The component currently strips tool roles when building the API payload — this must be changed for the save path to capture the full context.
3. Call `aiApi.saveMessages(activeConversationId.value, chatMessages)`.

**Key invariant:** The `messages.value` array in the component only holds `DisplayMessage` objects for `role: "user"` and `role: "assistant"`. The tool messages (role: "tool") are accumulated invisibly inside the API request payload built in `sendMessage` but are NOT part of `messages.value` (since they have no display representation). This means the save payload cannot be derived from `messages.value` alone.

**Solution:** Maintain a parallel `rawMessages = ref<ChatMessage[]>([])` array that mirrors the full message slice being sent to the API (including tool roles). This is the source of truth for saving. The `DisplayMessage[]` array remains the source of truth for rendering. After each completed turn, save `rawMessages.value`.

Concretely:
- On `sendMessage`, push the user message to `rawMessages` as a `ChatMessage`.
- After streaming completes, push the assistant message (with `reasoning_content`, `thinking_state`, `tool_calls` if present) and all `tool` messages to `rawMessages`.
- On `newChat()`, clear both `messages` and `rawMessages`.
- On conversation load, populate both arrays (see below).

**Loading a past conversation:**

When the user selects a conversation from the history panel:

1. Call `aiApi.getMessages(conversationId)`.
2. Set `rawMessages.value` to the returned `ChatMessage[]`.
3. Reconstruct `messages.value` (`DisplayMessage[]`) from the raw messages:
   - `role: "user"` → `DisplayMessage { role: "user", content, segments: [] }`
   - `role: "assistant"` → `DisplayMessage { role: "assistant", content, reasoningContent, thinkingState, segments: reconstructSegments(rawMessages, index) }`
   - `role: "tool"` → skip (no display representation, but kept in `rawMessages`)
4. `reconstructSegments` scans forward from the assistant message to find any `tool` messages in `rawMessages` and builds `ToolSegment` entries from the assistant's `tool_calls` cross-referenced with the `tool` role messages that follow it.

Reconstruction detail for tool segments: for each `tool_call` in the assistant message's `tool_calls`, create a `ToolSegment` with `toolCallId`, `name`, `args`. Then find the matching `role: "tool"` message in `rawMessages` with the same `tool_call_id` and set `result`. This gives a read-only rendering of what happened — approval segments are not reconstructed (their interaction is complete), plan segments are not reconstructed (the plan's final state is embedded in the tool results).

**History panel UI (inline in `AiAgentPanel.vue`):**

A small toggle button in the header (next to the existing "New Chat" button) opens/closes a conversation list panel. The panel renders a scrollable list of past conversations from `conversationStore.conversations`, filtered to `workspaceId`. Each row shows:
- `title` (or "Untitled" if empty, showing timestamp)
- `model` name
- `updatedAt` relative time
- A delete button (calls `conversationStore.deleteConversation(id)` with optimistic removal)

Clicking a row calls the load flow described above. The active conversation is highlighted. This panel overlays or replaces the empty state view — it is shown when the user is not actively chatting (or can slide in from the side).

No new Vue component is strictly required — the list can live inside `AiAgentPanel.vue` given the existing panel structure. However, extracting a `ConversationList.vue` subcomponent is a clean option if the panel grows complex.

---

## 4. Refactoring Plan

### Step 1 (prerequisite): Extract `ConversationRepository` into a separate file
**Scope: Small**
Create `internal/ai/conversation_repository.go` with the new interface and `conversationRepository` struct. The existing `repository.go` is untouched. This avoids modifying the existing `Repository` interface (OCP adherence).

### Step 2 (prerequisite): Update `NewService` and `NewHandler` signatures
**Scope: Small**
Add `convRepo ConversationRepository` to `NewService`. Add `convService ConversationService` to `NewHandler`. Update the DI wiring in `server.go`. This is a breaking change to the constructor signatures — existing tests that call `NewService` or `NewHandler` need to be updated to pass a stub/nil. Risk: low, tests are in the same package.

### Step 3: Add `rawMessages` to `AiAgentPanel.vue`
**Scope: Small, isolated**
This is purely additive. No existing behavior changes until the save/load paths are wired in.

### Step 4: Add save path
**Scope: Medium**
Wire auto-save into the `finally` block of `sendMessage`. The save is fire-and-forget with a logged error (non-blocking — failure to save must not interrupt the chat flow). Create the conversation on first save.

### Step 5: Add conversation list panel and load path
**Scope: Medium**
Add the Pinia store, new `aiApi` methods, UI toggle and list, and the `reconstructSegments` function. This is the largest frontend change.

### Step 6 (follow-up): Conversation title auto-derivation
**Scope: Small**
The `SaveMessages` service method derives the title from the first user message when `title` is still empty. This runs server-side, no additional endpoint needed.

### Step 7 (follow-up): Raise body size limit for save endpoint
**Scope: Small**
Exempt `PUT /api/ai/conversations/{id}/messages` from the 1MB cap, or switch to per-handler middleware. Implement before or alongside Step 4.

---

## 5. Database Migration Requirements

**Migration required: YES**

File: `internal/database/migrations/015_chat_history.sql`

New tables:
- `ai_conversations` — one row per named session (columns: id, user_id, workspace_id, title, model, created_at, updated_at)
- `ai_messages` — one row per message (columns: id, conversation_id, position, role, content, reasoning_content, thinking_state, tool_calls, tool_call_id)

New indexes:
- `idx_ai_conversations_user_workspace ON ai_conversations(user_id, workspace_id)`
- `idx_ai_messages_conversation ON ai_messages(conversation_id, position)`

Foreign keys:
- `ai_conversations.user_id → users(id) ON DELETE CASCADE`
- `ai_conversations.workspace_id → workspaces(id) ON DELETE CASCADE`
- `ai_messages.conversation_id → ai_conversations(id) ON DELETE CASCADE`

Existing data: None — applies only to new conversations. No backfill required.

Reversible: YES — `DROP TABLE ai_messages; DROP TABLE ai_conversations;` (reverse order due to FK).

SQLite FK enforcement note: SQLite requires `PRAGMA foreign_keys = ON` per connection for FK constraints to be enforced. Verify the database.Open call enables this pragma if cascade deletes are required at the DB layer. If not enabled, the application must manually delete child rows. The cleanest approach is to enable `PRAGMA foreign_keys = ON` in `database.Open` and rely on the database cascade.

---

## 6. Component Interaction Diagram

### Save flow (after each completed turn)

```
AiAgentPanel.vue
  │
  ├── sendMessage() completes
  │     (streaming.value = false in finally)
  │
  ├── saveCurrentConversation()
  │     │
  │     ├── [first save] aiApi.createConversation(workspaceId, model)
  │     │       → POST /api/ai/conversations
  │     │       → Handler.HandleCreateConversation
  │     │       → ConversationService.CreateConversation
  │     │       → ConversationRepository.CreateConversation
  │     │       ← { conversation: { id, ... } }
  │     │
  │     └── aiApi.saveMessages(conversationId, rawMessages)
  │             → PUT /api/ai/conversations/{id}/messages
  │             → Handler.HandleSaveMessages
  │             → ConversationService.SaveMessages (validates ownership, derives title)
  │             → ConversationRepository.SaveMessages (DELETE + bulk INSERT in tx)
  │             ← 200 OK
  │
  └── conversationStore.fetchConversations(workspaceId)  [refresh list]
```

### Load flow (user selects a conversation)

```
User clicks conversation in history panel
  │
  ├── aiApi.getMessages(conversationId)
  │     → GET /api/ai/conversations/{id}/messages
  │     → Handler.HandleGetMessages
  │     → ConversationService.GetMessages (validates ownership)
  │     → ConversationRepository.GetMessages (SELECT ordered by position)
  │     ← { messages: ChatMessage[] }
  │
  ├── rawMessages.value = response.messages
  │
  └── messages.value = reconstructDisplayMessages(rawMessages.value)
        (user messages → DisplayMessage{role:"user"}
         assistant messages → DisplayMessage{role:"assistant"} with reconstructSegments()
         tool messages → skipped in display, present in rawMessages)
```

### Continuation flow (user sends another message in a loaded conversation)

```
User types and submits in a loaded conversation
  │
  ├── activeConversationId.value is already set (from load)
  │
  ├── sendMessage() appends to rawMessages and messages as normal
  │
  ├── API call uses rawMessages (which includes the restored history + new message)
  │
  └── saveCurrentConversation() calls PUT /api/ai/conversations/{id}/messages
        (full replacement save, no diff)
```

---

## 7. Testability Assessment

| Component | Test strategy | Required test doubles |
|---|---|---|
| `conversationRepository` | Integration (real SQLite in-memory via `database.Open(":memory:")`) | None — test against real DB |
| `conversationService` | Unit | `MockConversationRepository` (interface mock) |
| `Handler` (new methods) | Unit with `net/http/httptest` | `MockConversationService`, `MockService` |
| `AiAgentPanel.vue` | Component test (Vitest + Vue Test Utils) | Mock `aiApi` with `vi.mock('@/api/ai')` |
| `chatHistory.ts` store | Unit (Vitest) | Mock `aiApi` |

**Testability decisions made in this design:**

- `ConversationService` and `ConversationRepository` are interfaces, not concrete types — all callers depend on abstractions, making unit tests straightforward.
- `ConversationService.SaveMessages` validates ownership internally rather than in the handler, so the ownership logic is unit-testable without an HTTP layer.
- The `reconstructDisplayMessages` function in the frontend should be extracted as a pure function (not a method on the component) so it can be unit-tested independently of Vue.
- The `rawMessages` / `messages` split makes the save path testable in isolation: the save logic only reads `rawMessages.value` and calls `aiApi.saveMessages`, with no DOM or streaming dependency.

---

## 8. Open Questions & Risks

### Q1: Body size limit for `SaveMessages`
The existing `maxBodySize(handler, 1<<20)` middleware is applied globally in `server.go`. A long agentic run (100 iterations with large file reads) can easily exceed 1MB of message JSON. The `PUT /api/ai/conversations/{id}/messages` endpoint needs a higher limit (10MB is reasonable). Options:
- Apply `maxBodySize` per-handler rather than as a global wrap (preferred — gives precise control)
- Wrap only the save endpoint with `http.MaxBytesReader` inside the handler itself

### Q2: Save granularity — after turn vs. streaming
This design saves after each completed turn (when `streaming.value` goes false). This means a crash mid-stream loses the current turn but not previous ones. An alternative is to save only when the user explicitly clicks "New Chat" or navigates away (via `beforeunload`). The turn-based approach is preferred because `beforeunload` is unreliable and per-turn saves give a better recovery story.

### Q3: Conversation title UX
The design auto-derives the title server-side from the first user message (truncated to 60 chars). An alternative is to let the user rename conversations. This is a follow-up feature — the schema already supports it via the `title` column and the `UpdateConversation` repository method.

### Q4: Maximum conversations per workspace
No limit is imposed by this design. For a single-user or small-team deployment this is acceptable. For a multi-tenant deployment, consider adding a `LIMIT` to `ListConversations` and a cleanup policy (e.g., keep last 50 per workspace). This is a follow-up concern.

### Q5: ThinkingState storage size
`ThinkingState` from MiniMax can be large (the full `reasoning_details` JSON array). A single assistant message's `thinking_state` can be several KB. For long agentic runs this may accumulate significantly. No mitigation is included in v1 — the `TEXT` column in SQLite handles arbitrary size, and the feature is opt-in. Worth monitoring in practice.

### Q6: The `rawMessages` parallel array introduces duplication
Maintaining `rawMessages` (full API shape) alongside `messages` (display shape) is a trade-off: it avoids complex reconstruction of tool messages from display segments, but creates two arrays that must be kept in sync. The sync points are: `sendMessage` (append both), `newChat` (clear both), and load (populate both). This is manageable given the bounded mutation surface. The alternative — reconstructing the full message slice from display segments on save — would require decoding `ToolSegment` back into `role: "tool"` messages, which is fragile and would couple the save logic to display logic.

### Q7: System prompt exclusion correctness
`HandleAgentChat` prepends the system prompt to the local `messages` slice before each loop iteration. The component sends only user/assistant/tool messages; the system message is never part of the frontend's `rawMessages`. When a saved conversation is loaded and the user sends another message, the handler again prepends the system prompt before calling the provider. This is correct as long as the system prompt is stateless. If it ever becomes dynamic (e.g., includes workspace context), this assumption needs revisiting.

### Q8: Multi-tab / concurrent session behaviour
If the same user has the same workspace open in two tabs, both will write to the same conversation (or create separate ones depending on timing). The full-replacement save strategy means the last write wins. This is acceptable for v1 — conversations are per-session by nature.
