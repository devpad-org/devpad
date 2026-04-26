package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// ConversationRepository stores AI conversations and messages.
type ConversationRepository struct {
	db *sql.DB
}

// NewConversationRepository creates a new conversation repository backed by the given database.
func NewConversationRepository(db *sql.DB) *ConversationRepository {
	return &ConversationRepository{db: db}
}

func (r *ConversationRepository) CreateConversation(ctx context.Context, conv *domain.Conversation) error {
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

func (r *ConversationRepository) GetConversation(ctx context.Context, id, userID int64) (*domain.Conversation, error) {
	row := r.db.QueryRowContext(ctx,
		`SELECT id, user_id, workspace_id, title, model, created_at, updated_at
		 FROM ai_conversations
		 WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	var conversation domain.Conversation
	err := row.Scan(
		&conversation.ID,
		&conversation.UserID,
		&conversation.WorkspaceID,
		&conversation.Title,
		&conversation.Model,
		&conversation.CreatedAt,
		&conversation.UpdatedAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scanning conversation: %w", err)
	}

	return &conversation, nil
}

func (r *ConversationRepository) ListConversations(ctx context.Context, userID, workspaceID int64) ([]domain.Conversation, error) {
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

	var conversations []domain.Conversation
	for rows.Next() {
		var conversation domain.Conversation
		if err := rows.Scan(
			&conversation.ID,
			&conversation.UserID,
			&conversation.WorkspaceID,
			&conversation.Title,
			&conversation.Model,
			&conversation.CreatedAt,
			&conversation.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scanning conversation: %w", err)
		}
		conversations = append(conversations, conversation)
	}

	return conversations, rows.Err()
}

func (r *ConversationRepository) UpdateConversation(ctx context.Context, id, userID int64, title, model string) error {
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

func (r *ConversationRepository) DeleteConversation(ctx context.Context, id, userID int64) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM ai_conversations WHERE id = ? AND user_id = ?`,
		id, userID,
	)
	if err != nil {
		return fmt.Errorf("deleting conversation: %w", err)
	}

	return nil
}

func (r *ConversationRepository) SaveTurns(ctx context.Context, conversationID int64, turns []domain.Turn) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_messages WHERE conversation_id = ?`, conversationID); err != nil {
		return fmt.Errorf("deleting existing messages: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO ai_messages (conversation_id, position, role, content, reasoning_content, thinking_state, tool_calls, tool_call_id)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing insert statement: %w", err)
	}
	defer stmt.Close()

	for i, turn := range turns {
		thinkingStateStr := ""
		if state := turn.ReasoningState(); len(state) > 0 {
			thinkingStateStr = string(state)
		}

		toolCallsStr := ""
		if toolCalls := turn.ToolCalls(); len(toolCalls) > 0 {
			data, err := json.Marshal(toolCalls)
			if err != nil {
				return fmt.Errorf("marshalling tool calls at position %d: %w", i, err)
			}
			toolCallsStr = string(data)
		}

		content := turn.Text()
		reasoningContent := turn.ReasoningText()
		toolCallID := ""
		if turn.Role == domain.RoleTool {
			toolResult := turn.ToolResult()
			if toolResult != nil {
				content = toolResult.Content
				toolCallID = toolResult.ToolCallID
			}
			reasoningContent = ""
			thinkingStateStr = ""
			toolCallsStr = ""
		}

		if _, err := stmt.ExecContext(ctx,
			conversationID,
			i,
			string(turn.Role),
			content,
			reasoningContent,
			thinkingStateStr,
			toolCallsStr,
			toolCallID,
		); err != nil {
			return fmt.Errorf("inserting turn at position %d: %w", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	return nil
}

func (r *ConversationRepository) GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error) {
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

	var turns []domain.Turn
	for rows.Next() {
		var role string
		var content string
		var reasoningContent string
		var thinkingStateStr string
		var toolCallsStr string
		var toolCallID string
		if err := rows.Scan(
			&role,
			&content,
			&reasoningContent,
			&thinkingStateStr,
			&toolCallsStr,
			&toolCallID,
		); err != nil {
			return nil, fmt.Errorf("scanning turn: %w", err)
		}

		turn := domain.Turn{Role: domain.Role(role)}
		if turn.Role == domain.RoleTool {
			turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolResult, ToolResult: &domain.ToolResultPart{
				ToolCallID: toolCallID,
				Content:    content,
			}})
			turns = append(turns, turn)
			continue
		}

		if thinkingStateStr != "" {
			turn.Parts = append(turn.Parts, domain.Part{
				Kind:          domain.PartReasoning,
				Text:          reasoningContent,
				ProviderState: json.RawMessage(thinkingStateStr),
			})
		} else if reasoningContent != "" {
			turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartReasoning, Text: reasoningContent})
		}

		if content != "" {
			turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartText, Text: content})
		}

		if toolCallsStr != "" {
			var toolCalls []domain.ToolCall
			if err := json.Unmarshal([]byte(toolCallsStr), &toolCalls); err != nil {
				return nil, fmt.Errorf("unmarshalling tool calls: %w", err)
			}
			for _, toolCall := range toolCalls {
				toolCall := toolCall
				turn.Parts = append(turn.Parts, domain.Part{Kind: domain.PartToolCall, ToolCall: &toolCall})
			}
		}

		turns = append(turns, turn)
	}

	return turns, rows.Err()
}
