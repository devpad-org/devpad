# AI Package Refactor Plan

## Purpose

This document describes a concrete refactor plan for the current `internal/ai` package in this repo.

The plan is designed to:

- keep the current frontend SSE contract stable during the refactor
- move the package to a clean `handler -> service -> repository` shape
- separate provider integration from Devpad-specific agent behavior
- make the orchestration unit-testable without HTTP infrastructure
- make Anthropic Messages API and OpenAI Responses API additive work, not another rewrite

This plan is written against the current repo state on 2026-04-22.

## Current Baseline

Today `internal/ai` contains all of the following in one package:

- HTTP handlers and SSE streaming
- provider adapters for OpenAI, Mistral, MiniMax, and Moonshot
- model catalog and provider configuration logic
- the full agent loop with tool calls and approvals
- workspace-backed tool execution
- provider configuration persistence
- conversation persistence

Current files:

- `handler.go`
- `service.go`
- `provider.go`
- `repository.go`
- `conversation_repository.go`
- `executor.go`
- `tools.go`
- `openai.go`
- `mistral.go`
- `minimax.go`
- `moonshot.go`
- tests colocated in the root package

Current composition root:

- `internal/server/server.go` constructs `ai.NewRepository`, `ai.NewConversationRepository`, `ai.NewService`, `ai.NewToolExecutor`, and `ai.NewHandler` directly.

## Refactor Goals

1. Keep `internal/ai` as the repo entry point to minimize churn in `internal/server` and route registration.
2. Move all business logic out of the root package into focused subpackages.
3. Separate provider protocol concerns from assistant runtime concerns.
4. Introduce normalized domain types that can represent:
   - chat-completions style providers
   - Anthropic content blocks and tool use
   - OpenAI Responses items and structured outputs
5. Keep the first migration compatible with the current frontend request and SSE response shapes.

## Target Directory Layout

```text
internal/ai/
  REFACTOR_PLAN.md
  doc.go
  module.go

  domain/
    turn.go
    model.go
    tool.go
    event.go
    errors.go
    conversation.go

  app/
    catalog_service.go
    conversation_service.go
    chat_service.go

  orchestrator/
    chat_orchestrator.go
    simple_chat.go
    agent_chat.go

  provider/
    adapter.go
    registry.go
    protocol.go
    resolver.go
    shared/
      http_client.go
      sse_decoder.go
      json_stream.go
    openaichat/
      adapter.go
    openairesponses/
      adapter.go
    anthropic/
      adapter.go
    mistral/
      adapter.go
    minimax/
      adapter.go
    moonshot/
      adapter.go

  storage/
    provider_config_repository.go
    conversation_repository.go

  tools/
    catalog.go
    executor.go
    workspace_executor.go
    policy.go

  approval/
    broker.go
    memory_broker.go

  transport/
    http/
      handler.go
      dto.go
      sse.go

  testkit/
    provider_contract.go
    stream_fixtures.go
```

## Directory-By-Directory Plan

### `internal/ai`

Role after refactor:

- package-level composition facade only
- no business logic
- no SQL
- no provider protocol code
- no agent loop

Files to add:

- `doc.go`: package overview and boundaries
- `module.go`: constructors that assemble subpackages for `internal/server/server.go`

Files to delete from the root package by the end of the refactor:

- `handler.go`
- `service.go`
- `provider.go`
- `repository.go`
- `conversation_repository.go`
- `executor.go`
- `tools.go`
- `openai.go`
- `mistral.go`
- `minimax.go`
- `moonshot.go`

Compatibility rule:

- during the migration, the root package may temporarily re-export constructors or types so `internal/server/server.go` does not need to change in the first PR

### `internal/ai/domain`

Role:

- provider-agnostic domain model
- no HTTP DTO tags unless the type is intentionally part of the transport contract
- no dependency on `workspace`, `database/sql`, or `net/http`

This package replaces the mixed concerns currently spread across `provider.go`, `service.go`, and `conversation_repository.go`.

New files:

- `turn.go`
- `model.go`
- `tool.go`
- `event.go`
- `errors.go`
- `conversation.go`

Key design change:

- replace the current `Message` struct with a normalized `Turn` plus `Part` model internally
- keep current HTTP request and SSE payloads in `transport/http/dto.go` and map them into `domain`

### `internal/ai/app`

Role:

- service layer for catalog lookup, conversation persistence, and chat entry points
- thin coordination over repositories and provider resolution
- no SSE formatting
- no HTTP request parsing

New files:

- `catalog_service.go`
- `conversation_service.go`
- `chat_service.go`

Responsibilities:

- `CatalogService` manages provider configuration and model listing
- `ConversationService` manages conversation CRUD and message snapshot persistence
- `ChatService` resolves the configured provider adapter for a model and delegates to the right orchestrator

### `internal/ai/orchestrator`

Role:

- own the full runtime state machine for chat execution
- absorb the logic currently embedded in `handler.go`
- be unit-testable with fake adapters, fake tools, and fake approvals

New files:

- `chat_orchestrator.go`
- `simple_chat.go`
- `agent_chat.go`

Responsibilities:

- system prompt injection
- tool-call iteration loop
- approval checks and waits
- message accumulation across rounds
- conversion from provider events to client events
- cancellation handling

Important rule:

- keepalive scheduling belongs here or in `transport/http/sse.go`, but the orchestration decisions must live here, not in the HTTP handler

### `internal/ai/provider`

Role:

- provider registry
- model-to-adapter resolution
- protocol-specific request and stream translation
- shared streaming and HTTP helpers for compatible providers

New files:

- `adapter.go`
- `registry.go`
- `protocol.go`
- `resolver.go`
- `shared/http_client.go`
- `shared/sse_decoder.go`
- `shared/json_stream.go`

Provider subdirectories:

- `openaichat/`
- `openairesponses/`
- `anthropic/`
- `mistral/`
- `minimax/`
- `moonshot/`

Concrete mapping from current files:

- current `openai.go` moves to `provider/openaichat/adapter.go`
- current `mistral.go` moves to `provider/mistral/adapter.go`
- current `minimax.go` moves to `provider/minimax/adapter.go`
- current `moonshot.go` moves to `provider/moonshot/adapter.go`

Additive future work:

- `provider/anthropic/adapter.go` handles Anthropic Messages API
- `provider/openairesponses/adapter.go` handles OpenAI Responses API

### `internal/ai/storage`

Role:

- persistence interfaces and SQL-backed implementations
- return domain objects, not SQL-shaped objects
- isolate all `database/sql` code here

New files:

- `provider_config_repository.go`
- `conversation_repository.go`

Concrete mapping from current files:

- current `repository.go` moves here
- current `conversation_repository.go` moves here

Schema note:

- first phase should reuse the existing tables and schema
- schema changes are optional and should be deferred until the new domain shape is stable

### `internal/ai/tools`

Role:

- Devpad tool definitions and execution policies
- isolate the dependency on workspace capabilities
- return structured execution results instead of stringly-typed failures

New files:

- `catalog.go`
- `executor.go`
- `workspace_executor.go`
- `policy.go`

Concrete mapping from current files:

- current `tools.go` becomes `catalog.go`
- current `executor.go` becomes `workspace_executor.go`

Design rule:

- this package owns a small local `WorkspaceOps` interface instead of depending on all of `workspace.Service`

### `internal/ai/approval`

Role:

- explicit approval lifecycle for privileged tools like `sudo`
- move pending approval state out of the HTTP handler

New files:

- `broker.go`
- `memory_broker.go`

Phase-one implementation:

- in-memory broker

Future option:

- persistent broker backed by SQLite or Redis if the app needs multi-process coordination

### `internal/ai/transport/http`

Role:

- request parsing
- auth/context extraction
- status code mapping
- SSE response writing
- request/response DTOs for the existing frontend contract

New files:

- `handler.go`
- `dto.go`
- `sse.go`

Concrete mapping from current files:

- current `handler.go` splits into this package and `orchestrator/`

Rule:

- the HTTP handler should not know how the tool loop works
- the HTTP handler should not mutate conversation state directly outside the service layer

### `internal/ai/testkit`

Role:

- shared adapter contract tests
- fixture replay helpers for SSE and JSON stream parsing

New files:

- `provider_contract.go`
- `stream_fixtures.go`

Test data recommendation:

- add per-provider fixtures under the provider package that owns them

## New Type Interfaces

The following interfaces and core types are the recommended target API.

The names are concrete enough to implement directly, but they are still a plan, not a requirement to copy verbatim.

### `internal/ai/domain`

```go
package domain

import (
    "context"
    "encoding/json"
    "time"
)

type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

type PartKind string

const (
    PartText       PartKind = "text"
    PartReasoning  PartKind = "reasoning"
    PartToolCall   PartKind = "tool_call"
    PartToolResult PartKind = "tool_result"
)

type Turn struct {
    Role  Role
    Parts []Part
}

type Part struct {
    Kind          PartKind
    Text          string
    ToolCall      *ToolCall
    ToolResult    *ToolResultPart
    ProviderState json.RawMessage
}

type ToolCall struct {
    ID        string
    Name      string
    Arguments json.RawMessage
}

type ToolResultPart struct {
    ToolCallID string
    Name       string
    Content    string
    IsError    bool
}

type ThinkingConfig struct {
    Enabled *bool
}

type ThinkingCapability struct {
    Supported        bool
    EnabledByDefault bool
    CanDisable       bool
}

type Model struct {
    ID         string
    Name       string
    ProviderID string
    Thinking   ThinkingCapability
}

type ModelInfo struct {
    Model
    ProviderName string
    Configured   bool
}

type Conversation struct {
    ID          int64
    UserID      int64
    WorkspaceID int64
    Title       string
    ModelID     string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type ProviderEvent struct {
    TextDelta       string
    ReasoningDelta  string
    ReasoningState  json.RawMessage
    ToolCalls       []ToolCall
    Done            bool
    Err             error
}

type ClientEvent struct {
    TextDelta      string
    ReasoningDelta string
    ReasoningState json.RawMessage
    ToolCalls      []ToolCall
    ToolResult     *ToolResultPart
    Approval       *ApprovalRequest
    Plan           []PlanStep
    Done           bool
    ErrorMessage   string
}

type ApprovalRequest struct {
    ID      string
    Command string
}

type PlanStep struct {
    Title  string
    Status string
}

var (
    ErrModelNotFound            = errors.New("model not found")
    ErrProviderNotFound         = errors.New("provider not found")
    ErrProviderNotEnabled       = errors.New("provider not enabled")
    ErrNoAPIKey                 = errors.New("no api key configured")
    ErrThinkingNotSupported     = errors.New("thinking not supported")
    ErrThinkingCannotBeDisabled = errors.New("thinking cannot be disabled")
    ErrConversationNotFound     = errors.New("conversation not found")
)

type TurnStore interface {
    ReplaceTurns(ctx context.Context, conversationID int64, turns []Turn) error
    LoadTurns(ctx context.Context, conversationID int64) ([]Turn, error)
}
```

Notes:

- `Turn` plus `Part` is the key change that makes Anthropic and Responses fit cleanly.
- `ProviderEvent` and `ClientEvent` are separate on purpose.

### `internal/ai/provider`

```go
package provider

import (
    "context"

    "github.com/devpad-org/devpad/internal/ai/domain"
)

type Protocol string

const (
    ProtocolOpenAIChat      Protocol = "openai_chat"
    ProtocolOpenAIResponses Protocol = "openai_responses"
    ProtocolAnthropic       Protocol = "anthropic_messages"
)

type Credentials struct {
    APIKey string
}

type StreamRequest struct {
    ModelID   string
    System    string
    Turns     []domain.Turn
    Thinking  *domain.ThinkingConfig
    Tools     []domain.ToolDefinition
}

type Adapter interface {
    ProviderID() string
    ProviderName() string
    Protocol() Protocol
    Models() []domain.Model
    Stream(ctx context.Context, creds Credentials, req StreamRequest) (<-chan domain.ProviderEvent, error)
}

type Registry interface {
    All() []Adapter
    ByProviderID(providerID string) (Adapter, bool)
    ResolveModel(modelID string) (Adapter, domain.Model, bool)
}
```

Notes:

- the provider interface is no longer named around chat completions
- Anthropic and Responses plug in by implementing `Stream`
- protocol identity is explicit instead of implicit in the adapter implementation

### `internal/ai/app`

```go
package app

import (
    "context"

    "github.com/devpad-org/devpad/internal/ai/domain"
    "github.com/devpad-org/devpad/internal/ai/provider"
)

type ProviderConfig struct {
    ID        string
    APIKey    string
    Enabled   bool
}

type ProviderConfigRepository interface {
    Get(ctx context.Context, providerID string) (*ProviderConfig, error)
    List(ctx context.Context) ([]ProviderConfig, error)
    Upsert(ctx context.Context, cfg ProviderConfig) error
}

type CatalogService interface {
    ListModels(ctx context.Context) ([]domain.ModelInfo, error)
    ListProviders(ctx context.Context) ([]ProviderInfo, error)
    UpdateProvider(ctx context.Context, providerID, apiKey string, enabled bool) error
    ResolveChatTarget(ctx context.Context, modelID string) (ResolvedTarget, error)
}

type ProviderInfo struct {
    ID        string
    Name      string
    Enabled   bool
    HasAPIKey bool
}

type ResolvedTarget struct {
    Adapter provider.Adapter
    Model   domain.Model
    Creds   provider.Credentials
}

type ConversationRepository interface {
    Create(ctx context.Context, conv *domain.Conversation) error
    Get(ctx context.Context, id, userID int64) (*domain.Conversation, error)
    ListByWorkspace(ctx context.Context, userID, workspaceID int64) ([]domain.Conversation, error)
    Delete(ctx context.Context, id, userID int64) error
    ReplaceTurns(ctx context.Context, conversationID, userID int64, turns []domain.Turn) error
    LoadTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error)
    UpdateMetadata(ctx context.Context, conversationID, userID int64, title, modelID string) error
}

type ConversationService interface {
    CreateConversation(ctx context.Context, userID, workspaceID int64, modelID string) (*domain.Conversation, error)
    ListConversations(ctx context.Context, userID, workspaceID int64) ([]domain.Conversation, error)
    GetConversation(ctx context.Context, id, userID int64) (*domain.Conversation, error)
    DeleteConversation(ctx context.Context, id, userID int64) error
    SaveTurns(ctx context.Context, conversationID, userID int64, turns []domain.Turn) error
    GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error)
}

type ChatService interface {
    StreamSimple(ctx context.Context, req SimpleChatRequest) (<-chan domain.ClientEvent, error)
    StreamAgent(ctx context.Context, req AgentChatRequest) (<-chan domain.ClientEvent, error)
}

type SimpleChatRequest struct {
    UserID   int64
    ModelID  string
    System   string
    Turns    []domain.Turn
    Thinking *domain.ThinkingConfig
}

type AgentChatRequest struct {
    UserID      int64
    WorkspaceID int64
    ModelID     string
    System      string
    Turns       []domain.Turn
    Thinking    *domain.ThinkingConfig
}
```

Notes:

- `CatalogService` is configuration and resolution only
- `ChatService` is the app-layer entry point for runtime execution

### `internal/ai/orchestrator`

```go
package orchestrator

import (
    "context"
    "encoding/json"

    "github.com/devpad-org/devpad/internal/ai/domain"
    "github.com/devpad-org/devpad/internal/ai/provider"
)

type ToolDefinition struct {
    Name        string
    Description string
    Parameters  json.RawMessage
}

type ToolCatalog interface {
    SystemPrompt() string
    Definitions() []domain.ToolDefinition
}

type ToolExecutionRequest struct {
    UserID      int64
    WorkspaceID int64
    ToolCall    domain.ToolCall
}

type ToolExecutionResult struct {
    ToolCallID string
    Name       string
    Content    string
    IsError    bool
}

type ToolExecutor interface {
    Execute(ctx context.Context, req ToolExecutionRequest) (ToolExecutionResult, error)
}

type ApprovalBroker interface {
    Open(ctx context.Context, req domain.ApprovalRequest) (string, error)
    Await(ctx context.Context, approvalID string) (bool, error)
    Resolve(ctx context.Context, userID int64, approvalID string, approved bool) error
}

type StreamRequest struct {
    UserID      int64
    WorkspaceID int64
    Adapter     provider.Adapter
    Credentials provider.Credentials
    Model       domain.Model
    System      string
    Turns       []domain.Turn
    Thinking    *domain.ThinkingConfig
}

type ChatOrchestrator interface {
    Stream(ctx context.Context, req StreamRequest) (<-chan domain.ClientEvent, error)
}
```

Notes:

- the orchestrator only sees normalized turns and provider events
- tool approval is a dependency, not handler-local state

### `internal/ai/tools`

```go
package tools

import (
    "context"

    "github.com/devpad-org/devpad/internal/agent"
)

type WorkspaceOps interface {
    ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
    WriteFile(ctx context.Context, userID, workspaceID int64, path string, content []byte) error
    ListFiles(ctx context.Context, userID, workspaceID int64, path string) ([]agent.FileEntry, error)
    DeleteFile(ctx context.Context, userID, workspaceID int64, path string) error
    SearchFiles(ctx context.Context, userID, workspaceID int64, pattern, pathFilter string, maxResults int) ([]agent.SearchResult, error)
    RunCommand(ctx context.Context, userID, workspaceID int64, command string) (*agent.CommandResult, error)
}
```

Notes:

- this interface is intentionally smaller than `workspace.Service`
- the AI package owns the abstraction it needs

### `internal/ai/transport/http`

```go
package http

import (
    "context"
    "encoding/json"

    "github.com/devpad-org/devpad/internal/ai/app"
    "github.com/devpad-org/devpad/internal/ai/domain"
    "github.com/devpad-org/devpad/internal/ai/orchestrator"
)

type Handler struct {
    catalog       app.CatalogService
    chat          app.ChatService
    conversations app.ConversationService
    approvals     orchestrator.ApprovalBroker
}

type ChatRequestDTO struct {
    Model       string          `json:"model"`
    Messages    []MessageDTO    `json:"messages"`
    Thinking    *ThinkingDTO    `json:"thinking,omitempty"`
    WorkspaceID int64           `json:"workspaceId,omitempty"`
}

type MessageDTO struct {
    Role             string          `json:"role"`
    Content          string          `json:"content"`
    ReasoningContent string          `json:"reasoning_content,omitempty"`
    ThinkingState    json.RawMessage `json:"thinking_state,omitempty"`
    ToolCalls        []ToolCallDTO   `json:"tool_calls,omitempty"`
    ToolCallID       string          `json:"tool_call_id,omitempty"`
}

type StreamEventDTO struct {
    ReasoningContent string             `json:"reasoningContent,omitempty"`
    ThinkingState    json.RawMessage    `json:"thinkingState,omitempty"`
    Content          string             `json:"content,omitempty"`
    ToolCalls        []ToolCallDTO      `json:"toolCalls,omitempty"`
    ToolResult       *ToolResultDTO     `json:"toolResult,omitempty"`
    ApprovalRequired *ApprovalRequestDTO `json:"approvalRequired,omitempty"`
    Plan             []PlanStepDTO      `json:"plan,omitempty"`
    Done             bool               `json:"done,omitempty"`
    Error            string             `json:"error,omitempty"`
}

func ToDomainTurns(messages []MessageDTO) []domain.Turn
func FromClientEvent(event domain.ClientEvent) StreamEventDTO
```

Notes:

- this preserves the current frontend-facing contract while allowing the domain model to evolve underneath it

## Current File To Target Mapping

| Current file | Target location | Notes |
| --- | --- | --- |
| `internal/ai/handler.go` | `internal/ai/transport/http/handler.go` and `internal/ai/orchestrator/agent_chat.go` | split transport from runtime |
| `internal/ai/service.go` | `internal/ai/app/catalog_service.go` and `internal/ai/app/conversation_service.go` | split provider config from conversation logic |
| `internal/ai/provider.go` | `internal/ai/domain/*.go` and `internal/ai/provider/adapter.go` | separate domain types from provider contract |
| `internal/ai/repository.go` | `internal/ai/storage/provider_config_repository.go` | unchanged SQL first |
| `internal/ai/conversation_repository.go` | `internal/ai/storage/conversation_repository.go` | unchanged SQL first, then normalize |
| `internal/ai/executor.go` | `internal/ai/tools/workspace_executor.go` | return structured tool results |
| `internal/ai/tools.go` | `internal/ai/tools/catalog.go` | tool definitions only |
| `internal/ai/openai.go` | `internal/ai/provider/openaichat/adapter.go` | chat-completions adapter |
| `internal/ai/mistral.go` | `internal/ai/provider/mistral/adapter.go` | OpenAI-compatible adapter |
| `internal/ai/minimax.go` | `internal/ai/provider/minimax/adapter.go` | custom reasoning mapping remains local |
| `internal/ai/moonshot.go` | `internal/ai/provider/moonshot/adapter.go` | thinking mapping remains local |
| `internal/ai/sse_test.go` | `internal/ai/provider/shared/sse_decoder_test.go` | decoder-level tests |
| `internal/ai/handler_test.go` | `internal/ai/orchestrator/agent_chat_test.go` and `internal/ai/transport/http/handler_test.go` | split behavior and transport tests |
| `internal/ai/executor_test.go` | `internal/ai/tools/workspace_executor_test.go` | keep focused |
| `internal/ai/ai_test.go` | `internal/ai/app/catalog_service_test.go` and provider tests | split by concern |

## Outside-Of-Directory Changes Required

### `internal/server`

`internal/server/server.go` should stop constructing low-level AI pieces directly from the root package.

Target wiring shape:

```go
providerConfigs := storage.NewProviderConfigRepository(db.Conn())
conversations := storage.NewConversationRepository(db.Conn())

registry := provider.NewRegistry(
    openaichat.NewAdapter(),
    openairesponses.NewAdapter(),
    anthropic.NewAdapter(),
    mistral.NewAdapter(),
    minimax.NewAdapter(),
    moonshot.NewAdapter(),
)

catalogSvc := app.NewCatalogService(providerConfigs, registry)
conversationSvc := app.NewConversationService(conversations)
toolCatalog := tools.NewCatalog()
toolExecutor := tools.NewWorkspaceExecutor(workspaceService)
approvalBroker := approval.NewMemoryBroker()
chatSvc := app.NewChatService(catalogSvc, toolCatalog, toolExecutor, approvalBroker)
aiHandler := httptransport.NewHandler(catalogSvc, chatSvc, conversationSvc, approvalBroker)
```

Migration note:

- in the first implementation PR, keep a root `ai.NewModule(...)` or `ai.NewHandler(...)` compatibility constructor so this server change can happen once instead of being spread across every phase

### `internal/workspace`

No immediate package split is required, but the AI refactor should stop depending on `workspace.Service` directly everywhere.

Use a local adapter in `internal/ai/tools`:

- `WorkspaceOps` is defined by AI
- `WorkspaceExecutor` accepts any implementation of `WorkspaceOps`
- a small wrapper around `workspace.Service` can satisfy it

This keeps the AI runtime from depending on unrelated workspace methods.

### `frontend`

No frontend changes are required in the first refactor phase if `transport/http/dto.go` preserves the current request and SSE response shapes.

Frontend changes only become necessary if we later decide to expose richer part-based messages instead of the current `Message` DTO.

## Refactor Phases

### Phase 1: Create the new subpackages without changing behavior

Scope:

- add `domain`, `app`, `storage`, `tools`, `transport/http`
- copy existing types and logic into the new packages with minimal renaming
- keep root `internal/ai` constructors as wrappers

Files touched:

- `internal/ai/*`
- `internal/server/server.go` only if the wrapper is not used

Validation:

- move existing unit tests alongside the new packages
- keep `go test ./internal/ai/...` green

### Phase 2: Extract the agent loop into `orchestrator`

Scope:

- move the tool loop out of `handler.go`
- create `ChatOrchestrator` and `AgentChatOrchestrator`
- keep HTTP DTOs unchanged

Behavioral checkpoint:

- existing tool-call ordering test still passes
- add tests for approval wait, cancellation, and max iteration behavior

### Phase 3: Normalize provider adapters

Scope:

- replace the current `Provider` interface with the new `provider.Adapter`
- introduce `provider.Registry`
- move OpenAI, Mistral, MiniMax, and Moonshot into dedicated adapter directories

Behavioral checkpoint:

- adapter contract suite passes for all providers
- SSE parser tests are moved to `provider/shared`

### Phase 4: Add the approval broker

Scope:

- remove pending approval state from the handler
- move it into `approval.MemoryBroker`
- route approval endpoints through the broker

Behavioral checkpoint:

- add tests for missing approval IDs, timeout, wrong-user resolution, and duplicate resolution

### Phase 5: Add Anthropic Messages adapter

Scope:

- implement `provider/anthropic/adapter.go`
- verify system prompt mapping, tool use blocks, and tool results

Behavioral checkpoint:

- no orchestrator or transport changes required

### Phase 6: Add OpenAI Responses adapter

Scope:

- implement `provider/openairesponses/adapter.go`
- normalize response items into `domain.ProviderEvent`

Behavioral checkpoint:

- no orchestrator or conversation service changes required

### Phase 7: Remove root-package compatibility shims

Scope:

- delete transitional wrappers from `internal/ai`
- keep only module wiring and docs at the root

## Testing Plan

### Unit tests

Add focused unit tests for:

- `app/CatalogService`
- `app/ConversationService`
- `orchestrator/SimpleChatOrchestrator`
- `orchestrator/AgentChatOrchestrator`
- `tools/WorkspaceExecutor`
- `approval/MemoryBroker`

### Contract tests

Each provider adapter must pass a shared contract suite that verifies:

- model registration
- authentication header behavior
- cancellation propagation
- tool-call accumulation
- error propagation
- stream completion semantics

### Fixture tests

Add replay tests for:

- OpenAI chat-completions SSE
- MiniMax reasoning split SSE
- Moonshot thinking payloads
- Anthropic tool-use blocks
- OpenAI Responses streamed items

### Integration tests

Add end-to-end tests for:

- HTTP handler to orchestrator to fake adapter
- conversation persistence round-trip
- approval request, resolution, and timeout

## Recommended First PR

The lowest-risk first PR is:

1. add `internal/ai/domain`
2. add `internal/ai/storage`
3. add `internal/ai/tools`
4. move existing root implementations into those packages with minimal behavior change
5. keep the root package as wrappers so the server wiring stays stable

This first PR should not introduce Anthropic or Responses yet.

It should only create the shape that makes those additions straightforward.

## Definition Of Done

The refactor is complete when all of the following are true:

1. `internal/ai` root no longer contains business logic.
2. the agent loop is fully outside HTTP handlers.
3. provider adapters implement a protocol-agnostic streaming contract.
4. Anthropic Messages can be added as a provider adapter without changing orchestrator code.
5. OpenAI Responses can be added as a provider adapter without changing transport code.
6. tool execution returns structured results instead of only string error payloads.
7. approval lifecycle is owned by `approval.Broker`, not by the HTTP handler.
8. all package tests pass with the new structure.
