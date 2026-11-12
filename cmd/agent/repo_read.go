package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
)

// Reading several directories and files of a commit in one request. Finding
// a project's skills lists a few directories and then reads a file in each of
// their subdirectories; a request for each would go through the repo
// container and start git several times over. This resolves the rev once,
// finds every object with one cat-file --batch-check, and reads the ones
// wanted with one cat-file --batch.

const (
	// maxRepoReadDirs and maxRepoReadFiles cap the paths one read asks for.
	// The host's client has the same limits, agent.MaxRepoReadDirs and
	// agent.MaxRepoReadFiles.
	maxRepoReadDirs  = 16
	maxRepoReadFiles = 512
	// maxRepoReadBytes caps the file contents one read returns, and is the
	// budget when the request sets none.
	maxRepoReadBytes = 8 << 20
	// maxRepoReadRequestBytes caps the request body.
	maxRepoReadRequestBytes = 256 << 10
)

type repoReadRequest struct {
	Rev   string   `json:"rev"`
	Dirs  []string `json:"dirs"`
	Files []string `json:"files"`
	// MaxBytes is the most file content to return. Files are read in order
	// until it is reached, so the one that reaches it is still read; the
	// rest are skipped.
	MaxBytes int `json:"maxBytes"`
}

type repoReadDir struct {
	Path string `json:"path"`
	// Found is false when there is no directory at the path.
	Found   bool            `json:"found"`
	Entries []repoTreeEntry `json:"entries"`
	// Truncated says entries were left out: past maxRepoTreeEntries, or
	// all of them for a directory too large to read.
	Truncated bool `json:"truncated"`
}

type repoReadFile struct {
	Path string `json:"path"`
	// Found is false when there is no file at the path.
	Found   bool   `json:"found"`
	Content []byte `json:"content,omitempty"`
	// TooLarge is a file over maxRepoFileBytes, which is never read.
	TooLarge bool `json:"tooLarge,omitempty"`
	// Skipped is a file left unread because the budget was spent.
	Skipped bool `json:"skipped,omitempty"`
}

type repoReadResult struct {
	// Commit is what the rev resolved to, which everything was read at.
	Commit string         `json:"commit"`
	Dirs   []repoReadDir  `json:"dirs"`
	Files  []repoReadFile `json:"files"`
}

// handleRepoRead lists directories and reads files at a commit or branch,
// in the order asked for.
func handleRepoRead(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), gitTimeout)
	defer cancel()

	var req repoReadRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, maxRepoReadRequestBytes)).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	switch {
	case len(req.Dirs) == 0 && len(req.Files) == 0:
		writeErr(w, http.StatusBadRequest, "dirs or files are required")
		return
	case len(req.Dirs) > maxRepoReadDirs || len(req.Files) > maxRepoReadFiles:
		writeErr(w, http.StatusBadRequest, fmt.Sprintf("at most %d dirs and %d files can be read at once", maxRepoReadDirs, maxRepoReadFiles))
		return
	}
	for _, p := range append(append([]string{}, req.Dirs...), req.Files...) {
		if err := validateBatchPath(p); err != nil {
			writeErr(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	maxBytes := req.MaxBytes
	if maxBytes <= 0 || maxBytes > maxRepoReadBytes {
		maxBytes = maxRepoReadBytes
	}
	sha, err := resolveRev(ctx, req.Rev, "rev")
	if err != nil {
		writeRevError(w, err)
		return
	}
	result, err := readAtCommit(ctx, sha, req.Dirs, req.Files, maxBytes)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// validateBatchPath is validateRepoPath for a path that goes on a line of
// cat-file's input, which a newline would split.
func validateBatchPath(p string) error {
	if strings.ContainsAny(p, "\n\r\x00") {
		return fmt.Errorf("path must be one line")
	}
	return validateRepoPath(p)
}

// readAtCommit reads dirs and files at sha, a full commit hash, reading
// files until maxBytes of them have been read.
func readAtCommit(ctx context.Context, sha string, dirs, files []string, maxBytes int) (*repoReadResult, error) {
	names := make([]string, 0, len(dirs)+len(files))
	for _, p := range dirs {
		names = append(names, sha+":"+p)
	}
	for _, p := range files {
		names = append(names, sha+":"+p)
	}
	objects, err := catFileCheck(ctx, names)
	if err != nil {
		return nil, err
	}

	result := &repoReadResult{Commit: sha, Dirs: make([]repoReadDir, len(dirs)), Files: make([]repoReadFile, len(files))}
	// wanted are the objects to read, and where each one's contents go.
	var wanted []string
	var assign []func([]byte)
	for i, p := range dirs {
		d := &result.Dirs[i]
		d.Path, d.Entries = p, []repoTreeEntry{}
		o := objects[i]
		if !o.found || o.typ != "tree" {
			continue
		}
		d.Found = true
		if o.size > maxRepoFileBytes {
			d.Truncated = true
			continue
		}
		wanted = append(wanted, o.oid)
		assign = append(assign, func(data []byte) {
			tree := parseTreeObject(data, len(sha)/2)
			d.Entries, d.Truncated = tree.Entries, tree.Truncated
		})
	}
	spent := 0
	for i, p := range files {
		f := &result.Files[i]
		f.Path = p
		o := objects[len(dirs)+i]
		if !o.found || o.typ != "blob" {
			continue
		}
		f.Found = true
		switch {
		case o.size > maxRepoFileBytes:
			f.TooLarge = true
			continue
		case spent >= maxBytes:
			f.Skipped = true
			continue
		}
		spent += o.size
		wanted = append(wanted, o.oid)
		assign = append(assign, func(data []byte) { f.Content = data })
	}

	contents, err := catFileRead(ctx, wanted)
	if err != nil {
		return nil, err
	}
	for i, data := range contents {
		assign[i](data)
	}
	return result, nil
}

// catFileObject is what cat-file --batch-check says of an object name.
type catFileObject struct {
	oid   string
	typ   string
	size  int
	found bool
}

// catFileCheck looks up each object name, as <rev>:<path>.
func catFileCheck(ctx context.Context, names []string) ([]catFileObject, error) {
	cmd := gitCommand(ctx, "cat-file", "--batch-check=%(objectname) %(objecttype) %(objectsize)")
	cmd.Stdin = strings.NewReader(strings.Join(names, "\n") + "\n")
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git cat-file: %s", strings.TrimSpace(stderr.String()))
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) != len(names) {
		return nil, fmt.Errorf("git cat-file: expected %d objects, got %d", len(names), len(lines))
	}
	objects := make([]catFileObject, len(names))
	for i, line := range lines {
		// A name that is not found comes back as "<name> missing", and the
		// name may have spaces; one that is found ends in its size.
		fields := strings.Fields(line)
		if len(fields) != 3 {
			continue
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil {
			continue
		}
		objects[i] = catFileObject{oid: fields[0], typ: fields[1], size: size, found: true}
	}
	return objects, nil
}

// catFileRead reads each object's contents, in order.
func catFileRead(ctx context.Context, oids []string) ([][]byte, error) {
	if len(oids) == 0 {
		return nil, nil
	}
	cmd := gitCommand(ctx, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(strings.Join(oids, "\n") + "\n")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("starting git cat-file: %w", err)
	}
	contents, readErr := readCatFileBatch(bufio.NewReader(stdout), len(oids))
	if readErr != nil {
		// Stop git rather than wait on output nobody reads.
		cmd.Process.Kill()
	}
	waitErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if waitErr != nil {
		return nil, fmt.Errorf("git cat-file: %s", strings.TrimSpace(stderr.String()))
	}
	return contents, nil
}

// readCatFileBatch reads n objects of cat-file --batch output: for each, a
// "<oid> <type> <size>" line, its contents, and a newline.
func readCatFileBatch(r *bufio.Reader, n int) ([][]byte, error) {
	contents := make([][]byte, n)
	for i := range n {
		header, err := r.ReadString('\n')
		if err != nil {
			return nil, fmt.Errorf("reading git cat-file: %w", err)
		}
		fields := strings.Fields(header)
		if len(fields) != 3 {
			return nil, fmt.Errorf("git cat-file: %s", strings.TrimSpace(header))
		}
		size, err := strconv.Atoi(fields[2])
		if err != nil || size < 0 || size > maxRepoFileBytes {
			return nil, fmt.Errorf("git cat-file: unexpected size in %q", strings.TrimSpace(header))
		}
		data := make([]byte, size)
		if _, err := io.ReadFull(r, data); err != nil {
			return nil, fmt.Errorf("reading git cat-file: %w", err)
		}
		if _, err := r.Discard(1); err != nil {
			return nil, fmt.Errorf("reading git cat-file: %w", err)
		}
		contents[i] = data
	}
	return contents, nil
}

// parseTreeObject reads a raw tree object: "<mode> <name>\0<oid>" entries,
// the oid oidLen raw bytes.
func parseTreeObject(data []byte, oidLen int) repoTree {
	tree := repoTree{Entries: []repoTreeEntry{}}
	for len(data) > 0 {
		sp := bytes.IndexByte(data, ' ')
		nul := bytes.IndexByte(data, 0)
		if sp < 0 || nul < sp || len(data) < nul+1+oidLen {
			break
		}
		mode, name := string(data[:sp]), string(data[sp+1:nul])
		data = data[nul+1+oidLen:]
		if len(tree.Entries) == maxRepoTreeEntries {
			tree.Truncated = true
			break
		}
		typ := "blob"
		switch mode {
		case "40000":
			typ = "tree"
		case "160000":
			typ = "commit"
		}
		tree.Entries = append(tree.Entries, repoTreeEntry{Name: name, Type: typ})
	}
	return tree
}
