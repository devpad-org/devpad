package domain

import "time"

// Attachment is a stored image a person attached to a message or a tool
// returned during a run.
type Attachment struct {
	ID     int64
	UserID int64
	// ConversationID is the conversation that refers to the image, or 0
	// before one does.
	ConversationID int64
	// RunID is the run whose tool returned the image, or 0 for an upload.
	RunID     int64
	MediaType string
	Width     int
	Height    int
	Data      []byte
	CreatedAt time.Time
}

// ImagePart returns the reference turns carry for the attachment, with its
// data loaded.
func (a Attachment) ImagePart() ImagePart {
	return ImagePart{
		AttachmentID: a.ID,
		MediaType:    a.MediaType,
		Width:        a.Width,
		Height:       a.Height,
		Data:         a.Data,
	}
}
