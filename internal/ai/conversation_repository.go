package ai

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Conversation is a named chat session.
type Conversation struct {
	ID          int64     `json:"id"`
	UserID      int64     `json:"userId"`
	WorkspaceID int64     `json:"workspaceId"`
	Title       string    `json:"title"`
	Model       string    `json:"model"`
	CreatedAt   time.Time `json:"createdAt"`
	UpdatedAt   time.Time `json:"updatedAt"`
}

// StoredMessage is a persisted Message with its ordering position.
// It maps directly to a row in ai_messages.
type StoredMessage struct {
	ID             int64
	ConversationID int64
	Position       int
	Message // embedded: Role, Content, ReasoningContent, ThinkingState, ToolCalls, ToolCallID
}

// ConversationRepository handles persistence of AI chat conversations and messages.
type ConversationRepository interface {
	// CreateConversation inserts a new conversation and sets conv.ID on success.
	CreateConversation(ctx context.Context, conv *Conversation) error

	// GetConversation returns the conversation with the given id scoped to userID.
	// Returns nil, nil if not found.
	GetConversation(ctx context.Context, id, userID int64) (*Conversation, error)

	// ListConversations returns conversations ordered by updated_at DESC.
	ListConversations(ctx context.Context, userID, workspaceID int64) ([]Conversation, error)

	// UpdateConversation updates title, model, and updated_at for a conversation.
	UpdateConversation(ctx context.Context, id, userID int64, title, model string) error

	// DeleteConversation deletes the conversation; messages cascade via FK.
	DeleteConversation(ctx context.Context, id, userID int64) error

	// SaveMessages replaces all messages for the conversation in a single transaction.
	// The caller is responsible for excluding the system prompt.
	SaveMessages(ctx context.Context, conversationID int64, messages []Message) error

	// GetMessages returns messages ordered by position ASC.
	// userID is validated via JOIN on ai_conversations.
	GetMessages(ctx context.Context, conversationID, userID int64) ([]Message, error)
}

type conversationRepository struct {
	db *sql.DB
}

// NewConversationRepository creates a new ConversationRepository backed by the given database.
func NewConversationRepository(db *sql.DB) ConversationRepository {
	return &conversationRepository{db: db}
}

func (r *conversationRepository) CreateConversation(ctx context.Context, conv *Conversation) error {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_conversations (user_id, workspace_id, title, model, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		conv.UserID, conv.WorkspaceID, conv.Title, conv.Model, now, now,
	)
	if err != nil {
		return fmt.Errorf("inserting conversation: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting conversation ID: %w", err)
	}
	conv.ID = id
	conv.CreatedAt = now
	conv.UpdatedAt = now
	return nil
}

func (r *conversationRepository) GetConversation(ctx context.Context, id, userID int64) (*Conversation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, workspace_id, title, model, created_at, updated_at
		 FROM ai_conversations
		 WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	var c Conversation
	err := row.Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.Title, &c.Model, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning conversation: %w", err)
	}
	return &c, nil
}

func (r *conversationRepository) ListConversations(ctx context.Context, userID, workspaceID int64) ([]Conversation, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, workspace_id, title, model, created_at, updated_at
		 FROM ai_conversations
		 WHERE user_id = ? AND workspace_id = ?
		 ORDER BY updated_at DESC`,
		userID, workspaceID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying conversations: %w", err)
	}
	defer rows.Close()

	var convs []Conversation
	for rows.Next() {
		var c Conversation
		if err := rows.Scan(&c.ID, &c.UserID, &c.WorkspaceID, &c.Title, &c.Model, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scanning conversation: %w", err)
		}
		convs = append(convs, c)
	}
	return convs, rows.Err()
}

func (r *conversationRepository) UpdateConversation(ctx context.Context, id, userID int64, title, model string) error {
	now := time.Now().UTC()
	_, err := r.db.ExecContext(ctx,
		`UPDATE ai_conversations SET title = ?, model = ?, updated_at = ?
		 WHERE id = ? AND user_id = ?`,
		title, model, now, id, userID,
	)
	if err != nil {
		return fmt.Errorf("updating conversation: %w", err)
	}
	return nil
}

func (r *conversationRepository) DeleteConversation(ctx context.Context, id, userID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM ai_conversations WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}
	return nil
}

func (r *conversationRepository) SaveMessages(ctx context.Context, conversationID int64, messages []Message) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing messages for this conversation.
	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_messages WHERE conversation_id = ?`, conversationID); err != nil {
		return fmt.Errorf("deleting existing messages: %w", err)
	}

	// Insert all messages with their position.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO ai_messages (conversation_id, position, role, content, reasoning_content, thinking_state, tool_calls, tool_call_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing insert statement: %w", err)
	}
	defer stmt.Close()

	for i, msg := range messages {
		thinkingStateStr := ""
		if len(msg.ThinkingState) > 0 {
			thinkingStateStr = string(msg.ThinkingState)
		}

		toolCallsStr := ""
		if len(msg.ToolCalls) > 0 {
			b, err := json.Marshal(msg.ToolCalls)
			if err != nil {
				return fmt.Errorf("marshalling tool calls at position %d: %w", i, err)
			}
			toolCallsStr = string(b)
		}

		if _, err := stmt.ExecContext(ctx,
			conversationID, i, msg.Role, msg.Content, msg.ReasoningContent,
			thinkingStateStr, toolCallsStr, msg.ToolCallID,
		); err != nil {
			return fmt.Errorf("inserting message at position %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

func (r *conversationRepository) GetMessages(ctx context.Context, conversationID, userID int64) ([]Message, error) {
	// Validate ownership via JOIN on ai_conversations.
	rows, err := r.db.QueryContext(ctx,
		`SELECT m.role, m.content, m.reasoning_content, m.thinking_state, m.tool_calls, m.tool_call_id
		 FROM ai_messages m
		 JOIN ai_conversations c ON c.id = m.conversation_id
		 WHERE m.conversation_id = ? AND c.user_id = ?
		 ORDER BY m.position ASC`,
		conversationID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying messages: %w", err)
	}
	defer rows.Close()

	var msgs []Message
	for rows.Next() {
		var msg Message
		var thinkingStateStr, toolCallsStr string
		if err := rows.Scan(
			&msg.Role, &msg.Content, &msg.ReasoningContent,
			&thinkingStateStr, &toolCallsStr, &msg.ToolCallID,
		); err != nil {
			return nil, fmt.Errorf("scanning message: %w", err)
		}

		if thinkingStateStr != "" {
			msg.ThinkingState = json.RawMessage(thinkingStateStr)
		}

		if toolCallsStr != "" {
			if err := json.Unmarshal([]byte(toolCallsStr), &msg.ToolCalls); err != nil {
				return nil, fmt.Errorf("unmarshalling tool calls: %w", err)
			}
		}

		msgs = append(msgs, msg)
	}
	return msgs, rows.Err()
}
