package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// searchResult represents a single matching line from a search.
type searchResult struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func handleSearchFiles(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Pattern    string `json:"pattern"`
		PathFilter string `json:"path_filter"`
		MaxResults int    `json:"max_results"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Pattern == "" {
		writeErr(w, http.StatusBadRequest, "pattern is required")
		return
	}

	if req.MaxResults <= 0 {
		req.MaxResults = 100
	}
	if req.MaxResults > 500 {
		req.MaxResults = 500
	}

	re, err := regexp.Compile(req.Pattern)
	if err != nil {
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("invalid regex: %v", err))
		return
	}

	var results []searchResult
	count := 0

	err = filepath.Walk(workspaceRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // skip files we can't access
		}
		if count >= req.MaxResults {
			return filepath.SkipAll
		}
		if info.IsDir() {
			// Skip hidden directories and common large directories
			name := info.Name()
			if strings.HasPrefix(name, ".") && name != "." {
				return filepath.SkipDir
			}
			if name == "node_modules" || name == "vendor" || name == "__pycache__" {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip binary/large files
		if info.Size() > 1<<20 { // 1MB limit per file
			return nil
		}

		relPath, _ := filepath.Rel(workspaceRoot, path)

		// Apply path filter if specified
		if req.PathFilter != "" {
			matched, _ := filepath.Match(req.PathFilter, filepath.Base(path))
			if !matched && !strings.Contains(relPath, req.PathFilter) {
				return nil
			}
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil
		}

		lines := strings.Split(string(data), "\n")
		for i, line := range lines {
			if count >= req.MaxResults {
				break
			}
			if re.MatchString(line) {
				results = append(results, searchResult{
					File:    relPath,
					Line:    i + 1,
					Content: truncate(line, 200),
				})
				count++
			}
		}
		return nil
	})

	if err != nil {
		writeErr(w, http.StatusInternalServerError, "search failed")
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"results": results,
		"count":   len(results),
	})
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "…"
}
