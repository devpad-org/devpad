package httptransport

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"

	"github.com/devpad-org/devpad/internal/ai/attachment"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/auth"
)

// AttachmentDTO describes a stored image attachment.
type AttachmentDTO struct {
	ImageRefDTO
	URL string `json:"url"`
}

// HandleUploadAttachment stores an image attached to a chat message, sent as
// the "file" field of a multipart form.
func (h *Handler) HandleUploadAttachment(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.attachments == nil {
		writeError(w, http.StatusServiceUnavailable, "attachments are not available")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, fmt.Sprintf("images can be at most %d MB", attachment.MaxUploadBytes>>20))
			return
		}
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, attachment.MaxUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid upload")
		return
	}

	a, err := h.attachments.Upload(r.Context(), user.ID, data)
	if err != nil {
		if errors.Is(err, domain.ErrInvalidImage) {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		log.Printf("failed to store attachment: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to store attachment")
		return
	}

	writeJSON(w, http.StatusCreated, map[string]any{"attachment": AttachmentDTO{
		ImageRefDTO: fromDomainImage(a.ImagePart()),
		URL:         attachmentURL(a.ID),
	}})
}

// HandleGetAttachment serves one of the user's image attachments.
func (h *Handler) HandleGetAttachment(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "not authenticated")
		return
	}
	if h.attachments == nil {
		writeError(w, http.StatusServiceUnavailable, "attachments are not available")
		return
	}
	id, ok := parsePositivePathID(w, r, "id", "invalid attachment ID")
	if !ok {
		return
	}

	a, err := h.attachments.GetAttachment(r.Context(), user.ID, id)
	if err != nil {
		if errors.Is(err, domain.ErrAttachmentNotFound) {
			writeError(w, http.StatusNotFound, "attachment not found")
			return
		}
		log.Printf("failed to load attachment %d: %v", id, err)
		writeError(w, http.StatusInternalServerError, "failed to load attachment")
		return
	}

	// Attachments are PNG or JPEG devpad encoded itself, and never change.
	w.Header().Set("Content-Type", a.MediaType)
	w.Header().Set("Content-Length", strconv.Itoa(len(a.Data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(a.Data)
}

func attachmentURL(id int64) string {
	return fmt.Sprintf("/api/ai/attachments/%d", id)
}

// isAttachmentError reports whether err is about the images a request
// referred to, which the client can correct.
func isAttachmentError(err error) bool {
	return errors.Is(err, domain.ErrAttachmentNotFound) || errors.Is(err, domain.ErrInvalidImage)
}
