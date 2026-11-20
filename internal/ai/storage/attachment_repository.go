package storage

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/ai/domain"
)

// AttachmentRepository stores image attachments for AI conversations.
type AttachmentRepository struct {
	db *sql.DB
}

// NewAttachmentRepository creates an attachment repository backed by the given database.
func NewAttachmentRepository(db *sql.DB) *AttachmentRepository {
	return &AttachmentRepository{db: db}
}

// CreateAttachment stores the attachment and sets its ID, size and creation
// time.
func (r *AttachmentRepository) CreateAttachment(ctx context.Context, a *domain.Attachment) error {
	now := time.Now().UTC()
	result, err := r.db.ExecContext(ctx,
		`INSERT INTO ai_attachments (user_id, conversation_id, run_id, media_type, width, height, byte_size, data, source_sha256, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		a.UserID, nullableID(a.ConversationID), nullableID(a.RunID), a.MediaType, a.Width, a.Height, len(a.Data), a.Data, a.SourceSHA256, now,
	)
	if err != nil {
		return fmt.Errorf("inserting attachment: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("getting attachment ID: %w", err)
	}
	a.ID = id
	a.Size = len(a.Data)
	a.CreatedAt = now
	return nil
}

// GetAttachment returns the user's attachment, or domain.ErrAttachmentNotFound.
func (r *AttachmentRepository) GetAttachment(ctx context.Context, userID, id int64) (*domain.Attachment, error) {
	attachments, err := r.GetAttachments(ctx, userID, []int64{id})
	if err != nil {
		return nil, err
	}
	a, ok := attachments[id]
	if !ok {
		return nil, domain.ErrAttachmentNotFound
	}
	return &a, nil
}

// GetAttachments returns those of the given attachments that belong to the
// user, keyed by ID.
func (r *AttachmentRepository) GetAttachments(ctx context.Context, userID int64, ids []int64) (map[int64]domain.Attachment, error) {
	return r.getAttachments(ctx, userID, ids, true)
}

// GetAttachmentInfo is GetAttachments without the images' data.
func (r *AttachmentRepository) GetAttachmentInfo(ctx context.Context, userID int64, ids []int64) (map[int64]domain.Attachment, error) {
	return r.getAttachments(ctx, userID, ids, false)
}

func (r *AttachmentRepository) getAttachments(ctx context.Context, userID int64, ids []int64, withData bool) (map[int64]domain.Attachment, error) {
	found := make(map[int64]domain.Attachment, len(ids))
	for _, chunk := range chunkIDs(ids) {
		args := make([]any, 0, len(chunk)+1)
		args = append(args, userID)
		for _, id := range chunk {
			args = append(args, id)
		}
		if err := r.queryAttachments(ctx, found, withData,
			`WHERE user_id = ? AND id IN (`+placeholders(len(chunk))+`)`,
			args...,
		); err != nil {
			return nil, err
		}
	}
	return found, nil
}

// FindToolImage returns the image a tool already stored in the run and
// conversation from a file with the given hash, or
// domain.ErrAttachmentNotFound.
func (r *AttachmentRepository) FindToolImage(ctx context.Context, userID, conversationID, runID int64, sourceSHA256 string) (*domain.Attachment, error) {
	found := make(map[int64]domain.Attachment, 1)
	if err := r.queryAttachments(ctx, found, true,
		`WHERE user_id = ? AND conversation_id IS ? AND run_id IS ? AND source_sha256 = ?
		 ORDER BY id LIMIT 1`,
		userID, nullableID(conversationID), nullableID(runID), sourceSHA256,
	); err != nil {
		return nil, err
	}
	for _, a := range found {
		return &a, nil
	}
	return nil, domain.ErrAttachmentNotFound
}

// queryAttachments adds the attachments matching where to found.
func (r *AttachmentRepository) queryAttachments(ctx context.Context, found map[int64]domain.Attachment, withData bool, where string, args ...any) error {
	data := "NULL"
	if withData {
		data = "data"
	}
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, user_id, conversation_id, run_id, media_type, width, height, byte_size, source_sha256, created_at, `+data+`
		 FROM ai_attachments `+where,
		args...,
	)
	if err != nil {
		return fmt.Errorf("querying attachments: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var (
			a                     domain.Attachment
			conversationID, runID sql.NullInt64
		)
		if err := rows.Scan(&a.ID, &a.UserID, &conversationID, &runID, &a.MediaType, &a.Width, &a.Height, &a.Size, &a.SourceSHA256, &a.CreatedAt, &a.Data); err != nil {
			return fmt.Errorf("scanning attachment: %w", err)
		}
		a.ConversationID = conversationID.Int64
		a.RunID = runID.Int64
		found[a.ID] = a
	}
	return rows.Err()
}

// DeleteUnreferencedAttachments deletes the user's attachments created before
// the given time that no conversation or run refers to.
func (r *AttachmentRepository) DeleteUnreferencedAttachments(ctx context.Context, userID int64, before time.Time) error {
	_, err := r.db.ExecContext(ctx,
		`DELETE FROM ai_attachments
		 WHERE user_id = ? AND conversation_id IS NULL AND run_id IS NULL AND created_at < ?`,
		userID, before.UTC(),
	)
	if err != nil {
		return fmt.Errorf("deleting unreferenced attachments: %w", err)
	}
	return nil
}

// linkAttachments gives the conversation the images its turns refer to that
// belong to the conversation's owner and are not yet in a conversation.
func linkAttachments(ctx context.Context, tx *sql.Tx, conversationID int64, turns []domain.Turn) error {
	for _, chunk := range chunkIDs(attachmentIDs(turns)) {
		args := make([]any, 0, len(chunk)+2)
		args = append(args, conversationID, conversationID)
		for _, id := range chunk {
			args = append(args, id)
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE ai_attachments SET conversation_id = ?
			 WHERE conversation_id IS NULL
			   AND user_id = (SELECT user_id FROM ai_conversations WHERE id = ?)
			   AND id IN (`+placeholders(len(chunk))+`)`,
			args...,
		)
		if err != nil {
			return fmt.Errorf("linking attachments: %w", err)
		}
	}
	return nil
}

func attachmentIDs(turns []domain.Turn) []int64 {
	seen := make(map[int64]bool)
	var ids []int64
	domain.EachImage(turns, func(image domain.ImagePart) {
		if image.AttachmentID > 0 && !seen[image.AttachmentID] {
			seen[image.AttachmentID] = true
			ids = append(ids, image.AttachmentID)
		}
	})
	return ids
}

// chunkIDs splits IDs into groups small enough for SQLite's limit on
// query parameters.
func chunkIDs(ids []int64) [][]int64 {
	const size = 500
	var chunks [][]int64
	for len(ids) > size {
		chunks = append(chunks, ids[:size])
		ids = ids[size:]
	}
	if len(ids) > 0 {
		chunks = append(chunks, ids)
	}
	return chunks
}

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func nullableID(id int64) any {
	if id <= 0 {
		return nil
	}
	return id
}
