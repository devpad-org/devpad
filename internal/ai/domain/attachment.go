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
	// Size is the size of Data in bytes, known also when Data is not loaded.
	Size int
	Data []byte
	// SourceSHA256 is the hash of the file a tool's image was made from, so
	// the same file read again in a run is not stored again. Empty for
	// uploads.
	SourceSHA256 string
	CreatedAt    time.Time
}

// ImagePart returns the reference turns carry for the attachment, with its
// size and whatever of its data is loaded.
func (a Attachment) ImagePart() ImagePart {
	size := a.Size
	if size == 0 {
		size = len(a.Data)
	}
	return ImagePart{
		AttachmentID: a.ID,
		MediaType:    a.MediaType,
		Width:        a.Width,
		Height:       a.Height,
		Size:         size,
		Data:         a.Data,
	}
}
