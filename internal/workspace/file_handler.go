package workspace

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/auth"
)

const maxWorkspaceUploadBytes = int64(10 << 20)

// HandleListFiles lists files in a workspace directory.
func (h *Handler) HandleListFiles(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "/workspace"
	}

	opts, err := parseListFilesOptions(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	list, err := h.service.ListFiles(r.Context(), user.ID, id, path, opts)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, list)
}

func parseListFilesOptions(r *http.Request) (agent.ListFilesOptions, error) {
	query := r.URL.Query()
	var opts agent.ListFilesOptions

	if value := query.Get("recursive"); value != "" {
		recursive, err := strconv.ParseBool(value)
		if err != nil {
			return opts, fmt.Errorf("recursive must be a boolean")
		}
		opts.Recursive = recursive
	}

	maxDepth, err := parsePositiveQueryInt(query.Get("max_depth"), "max_depth")
	if err != nil {
		return opts, err
	}
	opts.MaxDepth = maxDepth

	maxEntries, err := parsePositiveQueryInt(query.Get("max_entries"), "max_entries")
	if err != nil {
		return opts, err
	}
	opts.MaxEntries = maxEntries

	return opts, nil
}

func parsePositiveQueryInt(value, name string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

// HandleReadFile reads a file from the workspace.
func (h *Handler) HandleReadFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	data, err := h.service.ReadFile(r.Context(), user.ID, id, path)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

// HandleWriteFile writes content to a file in the workspace.
func (h *Handler) HandleWriteFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := h.service.WriteFile(r.Context(), user.ID, id, req.Path, []byte(req.Content)); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleUploadFile accepts a browser multipart upload and stores the file in a workspace directory.
func (h *Handler) HandleUploadFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	if err := r.ParseMultipartForm(maxWorkspaceUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart upload")
		return
	}

	targetDir := r.FormValue("path")
	if targetDir == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}
	targetDir, err = normalizeUploadDirectory(targetDir)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	filename, err := sanitizeUploadFilename(header.Filename)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	targetPath := path.Join(targetDir, filename)

	limited := http.MaxBytesReader(w, file, maxWorkspaceUploadBytes)
	if err := h.service.UploadFile(r.Context(), user.ID, id, targetPath, limited); err != nil {
		handleServiceError(w, err)
		return
	}

	writeJSON(w, http.StatusCreated, map[string]string{"path": targetPath})
}

func normalizeUploadDirectory(dir string) (string, error) {
	cleaned := strings.TrimSpace(dir)
	if cleaned == "" {
		return "", fmt.Errorf("path is required")
	}
	if !strings.HasPrefix(cleaned, "/") {
		cleaned = path.Join("/workspace", cleaned)
	}
	cleaned = path.Clean(cleaned)
	if cleaned != "/workspace" && !strings.HasPrefix(cleaned, "/workspace/") {
		return "", fmt.Errorf("path must be under /workspace")
	}
	return cleaned, nil
}

func sanitizeUploadFilename(filename string) (string, error) {
	name := strings.TrimSpace(filename)
	if name == "" {
		return "", fmt.Errorf("filename is required")
	}
	if strings.ContainsAny(name, `/\`) {
		return "", fmt.Errorf("filename must not contain path separators")
	}
	if name == "." || name == ".." {
		return "", fmt.Errorf("invalid filename")
	}
	return name, nil
}

// HandleDeleteFile deletes a file or directory from the workspace.
func (h *Handler) HandleDeleteFile(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := h.service.DeleteFile(r.Context(), user.ID, id, path); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleMkdir creates a directory in the workspace.
func (h *Handler) HandleMkdir(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Path == "" {
		writeError(w, http.StatusBadRequest, "path is required")
		return
	}

	if err := h.service.CreateDirectory(r.Context(), user.ID, id, req.Path); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// HandleRename renames or moves a file or directory in the workspace.
func (h *Handler) HandleRename(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.OldPath == "" || req.NewPath == "" {
		writeError(w, http.StatusBadRequest, "oldPath and newPath are required")
		return
	}

	if err := h.service.RenameFile(r.Context(), user.ID, id, req.OldPath, req.NewPath); err != nil {
		handleServiceError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
