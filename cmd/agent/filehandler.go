package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// validatePath ensures the requested path is under the workspace root,
// preventing path traversal attacks (including via symlinks).
func validatePath(p string) (string, error) {
	if p == "" {
		return workspaceRoot, nil
	}

	if !filepath.IsAbs(p) {
		p = filepath.Join(workspaceRoot, p)
	}

	cleaned := filepath.Clean(p)

	if cleaned != workspaceRoot && !strings.HasPrefix(cleaned, workspaceRoot+"/") {
		return "", fmt.Errorf("path must be under %s", workspaceRoot)
	}

	// Resolve symlinks to prevent escaping the workspace root via symlink chains.
	// If the path doesn't exist yet (e.g. new file write), resolve the parent.
	resolved := cleaned
	if _, err := os.Lstat(cleaned); err == nil {
		resolved, err = filepath.EvalSymlinks(cleaned)
		if err != nil {
			return "", fmt.Errorf("resolving path: %w", err)
		}
	} else if os.IsNotExist(err) {
		parentResolved, err := filepath.EvalSymlinks(filepath.Dir(cleaned))
		if err != nil {
			return "", fmt.Errorf("resolving parent path: %w", err)
		}
		resolved = filepath.Join(parentResolved, filepath.Base(cleaned))
	} else {
		return "", fmt.Errorf("checking path: %w", err)
	}

	if resolved != workspaceRoot && !strings.HasPrefix(resolved, workspaceRoot+"/") {
		return "", fmt.Errorf("path must be under %s", workspaceRoot)
	}

	return resolved, nil
}

func handleListFiles(w http.ResponseWriter, r *http.Request) {
	dirPath, err := validatePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	opts, err := parseListFilesOptions(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	list, err := listFiles(dirPath, opts)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "directory not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to read directory")
		return
	}

	writeJSON(w, http.StatusOK, list)
}

func handleReadFile(w http.ResponseWriter, r *http.Request) {
	filePath, err := validatePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			writeErr(w, http.StatusNotFound, "file not found")
			return
		}
		writeErr(w, http.StatusInternalServerError, "failed to stat file")
		return
	}

	if info.IsDir() {
		writeErr(w, http.StatusBadRequest, "path is a directory")
		return
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to read file")
		return
	}

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Write(data)
}

func handleWriteFile(w http.ResponseWriter, r *http.Request) {
	filePath, err := validatePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create parent directory")
		return
	}

	data, err := io.ReadAll(io.LimitReader(r.Body, 10<<20)) // 10MB limit
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to read request body")
		return
	}

	if err := os.WriteFile(filePath, data, 0644); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to write file")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleDeleteFile(w http.ResponseWriter, r *http.Request) {
	filePath, err := validatePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if filePath == workspaceRoot {
		writeErr(w, http.StatusBadRequest, "cannot delete workspace root")
		return
	}

	if err := os.RemoveAll(filePath); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to delete")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleMkdir(w http.ResponseWriter, r *http.Request) {
	dirPath, err := validatePath(r.URL.Query().Get("path"))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}

	if err := os.MkdirAll(dirPath, 0755); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to create directory")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func handleRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OldPath string `json:"oldPath"`
		NewPath string `json:"newPath"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	oldPath, err := validatePath(req.OldPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid old path: "+err.Error())
		return
	}

	newPath, err := validatePath(req.NewPath)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "invalid new path: "+err.Error())
		return
	}

	if err := os.Rename(oldPath, newPath); err != nil {
		writeErr(w, http.StatusInternalServerError, "failed to rename")
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeErr(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
