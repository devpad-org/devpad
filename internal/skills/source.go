package skills

import (
	"context"
	"errors"
	"net/http"
	"path"
	"sort"

	"github.com/devpad-org/devpad/internal/agent"
)

// Limits on what finding a source's skills reads. A workspace is hostile,
// so these, not anything in it, bound the work it can make the host do.
const (
	// maxSkills is the most skills one source offers.
	maxSkills = 50
	// maxListedEntries is the most entries read from one directory.
	maxListedEntries = 200
	// maxDiscoveryBytes bounds the SKILL.md bytes read to build a catalog;
	// skills past it are left out.
	maxDiscoveryBytes = 2 << 20
)

// errNotFound reports a file or directory a source does not have.
var errNotFound = errors.New("not found")

// entry is one entry of a directory in a source. An entry that is neither
// dir nor file may be either, such as a symlink in a workspace.
type entry struct {
	name string
	dir  bool
	file bool
}

// source is a tree of files skills are read from: a workspace, or a branch
// of the canonical repo.
type source interface {
	// list returns a directory's entries, failing with errNotFound when
	// there is no such directory.
	list(ctx context.Context, dir string) ([]entry, error)
	// read returns a file's contents, failing with errNotFound when there
	// is no such file.
	read(ctx context.Context, path string) ([]byte, error)
	// describe says where a skill's directory is, for the model.
	describe(dir string) string
}

// discover finds the skills in a source, in name order.
func discover(ctx context.Context, src source) ([]Skill, error) {
	var found []Skill
	seen := map[string]bool{}
	budget := maxDiscoveryBytes
	for _, root := range Roots {
		entries, err := src.list(ctx, root)
		if errors.Is(err, errNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
		for _, e := range entries {
			if e.file || !namePattern.MatchString(e.name) || seen[e.name] {
				continue
			}
			if len(found) == maxSkills || budget <= 0 {
				break
			}
			dir := root + "/" + e.name
			// One with no SKILL.md, or one that cannot be read, is no skill.
			data, err := src.read(ctx, dir+"/"+skillFile)
			if err != nil {
				if ctx.Err() != nil {
					return nil, ctx.Err()
				}
				continue
			}
			budget -= len(data)
			if s, ok := parseSkill(e.name, dir, data); ok {
				seen[e.name] = true
				found = append(found, s)
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// Workspaces reads files in a workspace.
type Workspaces interface {
	ListFiles(ctx context.Context, userID, workspaceID int64, path string, opts agent.ListFilesOptions) (*agent.FileList, error)
	ReadFile(ctx context.Context, userID, workspaceID int64, path string) ([]byte, error)
}

// workspaceSource reads skills from a workspace's /workspace.
type workspaceSource struct {
	files       Workspaces
	userID      int64
	workspaceID int64
}

func (s workspaceSource) list(ctx context.Context, dir string) ([]entry, error) {
	list, err := s.files.ListFiles(ctx, s.userID, s.workspaceID, dir, agent.ListFilesOptions{MaxEntries: maxListedEntries})
	if err != nil {
		return nil, notFound(err)
	}
	if list == nil {
		return nil, errNotFound
	}
	entries := make([]entry, 0, min(len(list.Entries), maxListedEntries))
	for _, e := range list.Entries {
		if len(entries) == maxListedEntries {
			break
		}
		// A symlink lists as a file, though it may lead to a directory.
		entries = append(entries, entry{name: path.Base(e.Name), dir: e.Type == "directory"})
	}
	return entries, nil
}

func (s workspaceSource) read(ctx context.Context, p string) ([]byte, error) {
	data, err := s.files.ReadFile(ctx, s.userID, s.workspaceID, p)
	return data, notFound(err)
}

func (s workspaceSource) describe(dir string) string {
	return "/workspace/" + dir
}

// Projects reads the canonical repo of a project.
type Projects interface {
	TreeAt(ctx context.Context, userID, projectID int64, rev, path string) (*agent.RepoTree, error)
	FileAt(ctx context.Context, userID, projectID int64, rev, path string) ([]byte, error)
}

// branchSource reads skills from a branch of a project's canonical repo.
type branchSource struct {
	projects  Projects
	userID    int64
	projectID int64
	branch    string
}

func (s branchSource) list(ctx context.Context, dir string) ([]entry, error) {
	tree, err := s.projects.TreeAt(ctx, s.userID, s.projectID, s.branch, dir)
	if err != nil {
		return nil, notFound(err)
	}
	entries := make([]entry, 0, min(len(tree.Entries), maxListedEntries))
	for _, e := range tree.Entries {
		if len(entries) == maxListedEntries {
			break
		}
		// A symlink in a commit is a blob, which is not followed: only
		// directories hold skills. Submodules are left out.
		if e.Type == "blob" || e.Type == "tree" {
			entries = append(entries, entry{name: e.Name, dir: e.Type == "tree", file: e.Type == "blob"})
		}
	}
	return entries, nil
}

func (s branchSource) read(ctx context.Context, p string) ([]byte, error) {
	data, err := s.projects.FileAt(ctx, s.userID, s.projectID, s.branch, p)
	return data, notFound(err)
}

func (s branchSource) describe(dir string) string {
	return dir + " on the branch `" + s.branch + "`"
}

// notFound turns an agent's 404 into errNotFound, and its 400 too: a path
// the agent refuses, such as a symlink out of /workspace, has nothing there
// to read.
func notFound(err error) error {
	var agentErr *agent.AgentError
	if errors.As(err, &agentErr) && (agentErr.StatusCode == http.StatusNotFound || agentErr.StatusCode == http.StatusBadRequest) {
		return errNotFound
	}
	return err
}
