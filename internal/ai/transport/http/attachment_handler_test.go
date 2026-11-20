package httptransport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/devpad-org/devpad/internal/ai/app"
	"github.com/devpad-org/devpad/internal/ai/domain"
	"github.com/devpad-org/devpad/internal/ai/storage"
	"github.com/devpad-org/devpad/internal/auth"
	appdb "github.com/devpad-org/devpad/internal/database"
)

func newAttachmentTestHandler(t *testing.T) (*Handler, int64, int64) {
	t.Helper()
	db, err := appdb.Open(":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := db.Migrate(); err != nil {
		t.Fatalf("migrate db: %v", err)
	}
	var users []int64
	for _, name := range []string{"owner", "other"} {
		result, err := db.Conn().Exec(`INSERT INTO users (username, email, password) VALUES (?, ?, 'hashed')`, name, name+"@example.com")
		if err != nil {
			t.Fatalf("insert user: %v", err)
		}
		id, _ := result.LastInsertId()
		users = append(users, id)
	}
	h := &Handler{attachments: app.NewAttachmentService(storage.NewAttachmentRepository(db.Conn()))}
	return h, users[0], users[1]
}

func asUser(r *http.Request, userID int64) *http.Request {
	return r.WithContext(context.WithValue(r.Context(), auth.UserContextKey, &auth.User{ID: userID}))
}

func uploadRequest(t *testing.T, data []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	part, err := form.CreateFormFile("file", "screenshot.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = part.Write(data)
	_ = form.Close()
	req := httptest.NewRequest(http.MethodPost, "/api/ai/attachments", &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	return req
}

func TestHandleUploadAttachment_StoresAndServesTheImage(t *testing.T) {
	h, owner, other := newAttachmentTestHandler(t)
	var img bytes.Buffer
	if err := png.Encode(&img, image.NewRGBA(image.Rect(0, 0, 3, 2))); err != nil {
		t.Fatalf("encode png: %v", err)
	}

	w := httptest.NewRecorder()
	h.HandleUploadAttachment(w, asUser(uploadRequest(t, img.Bytes()), owner))
	if w.Code != http.StatusCreated {
		t.Fatalf("upload status = %d: %s", w.Code, w.Body.String())
	}
	var resp struct {
		Attachment AttachmentDTO `json:"attachment"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	a := resp.Attachment
	if a.AttachmentID == 0 || a.MediaType != "image/png" || a.Width != 3 || a.Height != 2 || a.URL != fmt.Sprintf("/api/ai/attachments/%d", a.AttachmentID) {
		t.Fatalf("unexpected attachment: %+v", a)
	}

	get := func(userID int64) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, a.URL, nil)
		req.SetPathValue("id", fmt.Sprint(a.AttachmentID))
		w := httptest.NewRecorder()
		h.HandleGetAttachment(w, asUser(req, userID))
		return w
	}
	served := get(owner)
	if served.Code != http.StatusOK || served.Header().Get("Content-Type") != "image/png" || served.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Fatalf("unexpected response: %d %v", served.Code, served.Header())
	}
	if _, err := png.Decode(served.Body); err != nil {
		t.Fatalf("served data is not a png: %v", err)
	}
	if w := get(other); w.Code != http.StatusNotFound {
		t.Fatalf("another user's status = %d, want 404", w.Code)
	}
}

func TestHandleUploadAttachment_RefusesWhatIsNotAnImage(t *testing.T) {
	h, owner, _ := newAttachmentTestHandler(t)
	w := httptest.NewRecorder()
	h.HandleUploadAttachment(w, asUser(uploadRequest(t, []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`)), owner))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", w.Code, w.Body.String())
	}
}

func TestWriteAgentRunError_ReportsMissingAttachmentsAsBadRequests(t *testing.T) {
	w := httptest.NewRecorder()
	(&Handler{}).writeAgentRunError(w, fmt.Errorf("hydrating: %w", domain.ErrAttachmentNotFound))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
}
