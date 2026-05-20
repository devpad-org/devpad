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

	if _, err := tx.ExecContext(ctx, `DELETE FROM ai_turns WHERE conversation_id = ?`, conversationID); err != nil {
		return fmt.Errorf("deleting existing turns: %w", err)
	}

	turnStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO ai_turns (conversation_id, position, role) VALUES (?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing turn insert: %w", err)
	}
	defer turnStmt.Close()

	partStmt, err := tx.PrepareContext(ctx,
		`INSERT INTO ai_parts (turn_id, position, kind, text, thinking_state,
		 image_mime_type, image_data,
		 tool_call_id, tool_call_item_id, tool_call_name, tool_call_args,
		 tool_result_call_id, tool_result_name, tool_result_content, tool_result_is_error)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("preparing part insert: %w", err)
	}
	defer partStmt.Close()

	for i, turn := range turns {
		result, err := turnStmt.ExecContext(ctx, conversationID, i, string(turn.Role))
		if err != nil {
			return fmt.Errorf("inserting turn at position %d: %w", i, err)
		}
		turnID, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("getting turn ID at position %d: %w", i, err)
		}

		for j, part := range turn.Parts {
			if err := insertPart(ctx, partStmt, turnID, j, part); err != nil {
				return fmt.Errorf("inserting part %d for turn %d: %w", j, i, err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}
	return nil
}

func insertPart(ctx context.Context, stmt *sql.Stmt, turnID int64, position int, part domain.Part) error {
	var (
		text              string
		thinkingState     string
		imageMIMEType     string
		imageData         string
		toolCallID        string
		toolCallItemID    string
		toolCallName      string
		toolCallArgs      string
		toolResultCallID  string
		toolResultName    string
		toolResultContent string
		toolResultIsError int
	)

	switch part.Kind {
	case domain.PartText:
		text = part.Text
	case domain.PartThinking:
		if part.Thinking != nil {
			text = part.Thinking.Text
			if len(part.Thinking.State) > 0 {
				thinkingState = string(part.Thinking.State)
			}
		}
	case domain.PartImage:
		if part.Image != nil {
			imageMIMEType = part.Image.MIMEType
			imageData = domain.NormalizeImageData(part.Image.Data)
		}
	case domain.PartToolCall:
		if part.ToolCall != nil {
			toolCallID = part.ToolCall.ID
			toolCallItemID = part.ToolCall.ItemID
			toolCallName = part.ToolCall.Function.Name
			toolCallArgs = part.ToolCall.Function.Arguments
		}
	case domain.PartToolResult:
		if part.ToolResult != nil {
			toolResultCallID = part.ToolResult.ToolCallID
			toolResultName = part.ToolResult.Name
			toolResultContent = part.ToolResult.Content
			if part.ToolResult.IsError {
				toolResultIsError = 1
			}
		}
	}

	_, err := stmt.ExecContext(ctx,
		turnID, position, string(part.Kind),
		text, thinkingState,
		imageMIMEType, imageData,
		toolCallID, toolCallItemID, toolCallName, toolCallArgs,
		toolResultCallID, toolResultName, toolResultContent, toolResultIsError,
	)
	return err
}

func (r *ConversationRepository) GetTurns(ctx context.Context, conversationID, userID int64) ([]domain.Turn, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT t.id, t.position, t.role,
		        p.position, p.kind, p.text, p.thinking_state,
		        p.image_mime_type, p.image_data,
		        p.tool_call_id, p.tool_call_item_id, p.tool_call_name, p.tool_call_args,
		        p.tool_result_call_id, p.tool_result_name, p.tool_result_content, p.tool_result_is_error
		 FROM ai_turns t
		 LEFT JOIN ai_parts p ON p.turn_id = t.id
		 JOIN ai_conversations c ON c.id = t.conversation_id
		 WHERE t.conversation_id = ? AND c.user_id = ?
		 ORDER BY t.position, p.position`,
		conversationID, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("querying turns: %w", err)
	}
	defer rows.Close()

	type turnKey struct {
		id       int64
		position int
		role     string
	}

	var orderedKeys []turnKey
	turnParts := make(map[int64][]domain.Part)
	seenTurns := make(map[int64]bool)

	for rows.Next() {
		var (
			turnID, turnPos                                        int64
			role                                                   string
			partPos                                                sql.NullInt64
			kind, text, thinkingState                              sql.NullString
			imageMIMEType, imageData                               sql.NullString
			toolCallID, toolCallItemID, toolCallName, toolCallArgs sql.NullString
			toolResultCallID, toolResultName, toolResultContent    sql.NullString
			toolResultIsError                                      sql.NullInt64
		)
		if err := rows.Scan(
			&turnID, &turnPos, &role,
			&partPos, &kind, &text, &thinkingState,
			&imageMIMEType, &imageData,
			&toolCallID, &toolCallItemID, &toolCallName, &toolCallArgs,
			&toolResultCallID, &toolResultName, &toolResultContent, &toolResultIsError,
		); err != nil {
			return nil, fmt.Errorf("scanning row: %w", err)
		}

		if !seenTurns[turnID] {
			seenTurns[turnID] = true
			orderedKeys = append(orderedKeys, turnKey{id: turnID, position: int(turnPos), role: role})
		}

		if !kind.Valid {
			continue
		}

		part, err := scanPart(kind.String, text.String, thinkingState.String,
			imageMIMEType.String, imageData.String,
			toolCallID.String, toolCallItemID.String, toolCallName.String, toolCallArgs.String,
			toolResultCallID.String, toolResultName.String, toolResultContent.String, toolResultIsError.Int64 != 0)
		if err != nil {
			return nil, fmt.Errorf("building part: %w", err)
		}
		turnParts[turnID] = append(turnParts[turnID], part)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	turns := make([]domain.Turn, 0, len(orderedKeys))
	for _, key := range orderedKeys {
		turns = append(turns, domain.Turn{
			Role:  domain.Role(key.role),
			Parts: turnParts[key.id],
		})
	}
	return turns, nil
}

func scanPart(kind, text, thinkingState, imageMIMEType, imageData string,
	toolCallID, toolCallItemID, toolCallName, toolCallArgs,
	toolResultCallID, toolResultName, toolResultContent string,
	toolResultIsError bool) (domain.Part, error) {

	switch domain.PartKind(kind) {
	case domain.PartText:
		return domain.Part{Kind: domain.PartText, Text: text}, nil

	case domain.PartThinking:
		tp := &domain.ThinkingPart{Text: text}
		if thinkingState != "" {
			tp.State = json.RawMessage(thinkingState)
		}
		return domain.Part{Kind: domain.PartThinking, Thinking: tp}, nil

	case domain.PartImage:
		return domain.Part{
			Kind: domain.PartImage,
			Image: &domain.ImagePart{
				MIMEType: imageMIMEType,
				Data:     imageData,
			},
		}, nil

	case domain.PartToolCall:
		return domain.Part{
			Kind: domain.PartToolCall,
			ToolCall: &domain.ToolCall{
				ID:     toolCallID,
				ItemID: toolCallItemID,
				Type:   "function",
				Function: domain.ToolCallFunction{
					Name:      toolCallName,
					Arguments: toolCallArgs,
				},
			},
		}, nil

	case domain.PartToolResult:
		return domain.Part{
			Kind: domain.PartToolResult,
			ToolResult: &domain.ToolResultPart{
				ToolCallID: toolResultCallID,
				Name:       toolResultName,
				Content:    toolResultContent,
				IsError:    toolResultIsError,
			},
		}, nil

	default:
		return domain.Part{}, fmt.Errorf("unknown part kind %q", kind)
	}
}
