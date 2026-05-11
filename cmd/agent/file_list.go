package main

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	defaultRecursiveListDepth   = 3
	defaultRecursiveListEntries = 500
	maxRecursiveListDepth       = 10
	maxRecursiveListEntries     = 2000
)

type fileEntry struct {
	Name    string `json:"name"`
	Path    string `json:"path"`
	Type    string `json:"type"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
}

type listFilesOptions struct {
	Recursive  bool
	MaxDepth   int
	MaxEntries int
}

type fileListResponse struct {
	Entries   []fileEntry `json:"entries"`
	Truncated bool        `json:"truncated,omitempty"`
}

func parseListFilesOptions(r *http.Request) (listFilesOptions, error) {
	query := r.URL.Query()
	opts := listFilesOptions{}

	if value := query.Get("recursive"); value != "" {
		recursive, err := strconv.ParseBool(value)
		if err != nil {
			return opts, fmt.Errorf("recursive must be a boolean")
		}
		opts.Recursive = recursive
	}

	maxDepth, err := parsePositiveIntQuery(query.Get("max_depth"), "max_depth")
	if err != nil {
		return opts, err
	}
	opts.MaxDepth = clampInt(maxDepth, 0, maxRecursiveListDepth)

	maxEntries, err := parsePositiveIntQuery(query.Get("max_entries"), "max_entries")
	if err != nil {
		return opts, err
	}
	opts.MaxEntries = clampInt(maxEntries, 0, maxRecursiveListEntries)

	if opts.Recursive {
		if opts.MaxDepth == 0 {
			opts.MaxDepth = defaultRecursiveListDepth
		}
		if opts.MaxEntries == 0 {
			opts.MaxEntries = defaultRecursiveListEntries
		}
	}

	return opts, nil
}

func parsePositiveIntQuery(value, name string) (int, error) {
	if value == "" {
		return 0, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func listFiles(dirPath string, opts listFilesOptions) (fileListResponse, error) {
	if opts.Recursive {
		return listFilesRecursive(dirPath, opts)
	}
	return listFilesFlat(dirPath, opts)
}

func listFilesFlat(dirPath string, opts listFilesOptions) (fileListResponse, error) {
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return fileListResponse{}, err
	}

	result := fileListResponse{Entries: make([]fileEntry, 0, len(entries))}
	for _, entry := range entries {
		if opts.MaxEntries > 0 && len(result.Entries) >= opts.MaxEntries {
			result.Truncated = true
			break
		}
		file, err := fileEntryFromDirEntry(dirPath, entry)
		if err != nil {
			continue
		}
		result.Entries = append(result.Entries, file)
	}

	return result, nil
}

func listFilesRecursive(dirPath string, opts listFilesOptions) (fileListResponse, error) {
	result := fileListResponse{Entries: make([]fileEntry, 0)}

	err := filepath.WalkDir(dirPath, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			if path == dirPath {
				return err
			}
			if entry != nil && entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if path == dirPath {
			return nil
		}

		depth, err := relativeDepth(dirPath, path)
		if err != nil {
			return err
		}
		if depth > opts.MaxDepth {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		if opts.MaxEntries > 0 && len(result.Entries) >= opts.MaxEntries {
			result.Truncated = true
			return filepath.SkipAll
		}

		file, err := fileEntryFromDirEntry(filepath.Dir(path), entry)
		if err != nil {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		result.Entries = append(result.Entries, file)

		if entry.IsDir() && (depth == opts.MaxDepth || shouldSkipRecursiveDir(entry.Name())) {
			return filepath.SkipDir
		}
		return nil
	})
	if err != nil {
		return fileListResponse{}, err
	}

	return result, nil
}

func fileEntryFromDirEntry(parentPath string, entry os.DirEntry) (fileEntry, error) {
	info, err := entry.Info()
	if err != nil {
		return fileEntry{}, err
	}

	entryPath := filepath.Join(parentPath, entry.Name())
	typ := "file"
	if entry.IsDir() {
		typ = "directory"
	}

	return fileEntry{
		Name:    entry.Name(),
		Path:    entryPath,
		Type:    typ,
		Size:    info.Size(),
		ModTime: info.ModTime().UTC().Format("2006-01-02T15:04:05Z"),
	}, nil
}

func relativeDepth(basePath, path string) (int, error) {
	rel, err := filepath.Rel(basePath, path)
	if err != nil {
		return 0, err
	}
	if rel == "." {
		return 0, nil
	}
	return len(strings.Split(rel, string(os.PathSeparator))), nil
}

func shouldSkipRecursiveDir(name string) bool {
	switch name {
	case ".git", "node_modules", "vendor", "dist", "build", "__pycache__":
		return true
	default:
		return false
	}
}

func clampInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
