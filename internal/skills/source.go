package skills

import (
	"context"
	"errors"
	"net/http"
	"path"
	"sort"
	"sync"

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
	// readBatch is how many SKILL.md files are read at once. Each read of a
	// branch goes through the repo container, so reading them one by one
	// could take most of the time a run waits for its skills.
	readBatch = 8
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
		var names []string
		for _, e := range entries {
			if !e.file && namePattern.MatchString(e.name) && !seen[e.name] {
				names = append(names, e.name)
			}
		}
		for start := 0; start < len(names) && len(found) < maxSkills && budget > 0; start += readBatch {
			batch := names[start:min(start+readBatch, len(names))]
			files, err := readSkillFiles(ctx, src, root, batch)
			if err != nil {
				return nil, err
			}
			for i, name := range batch {
				if len(found) == maxSkills || budget <= 0 {
					break
				}
				// One with no SKILL.md, or one that cannot be read, is no skill.
				if files[i] == nil {
					continue
				}
				budget -= len(files[i])
				if s, ok := parseSkill(name, root+"/"+name, files[i]); ok {
					seen[name] = true
					found = append(found, s)
				}
			}
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Name < found[j].Name })
	return found, nil
}

// readSkillFiles reads the SKILL.md of each named directory under root at
// once, leaving nil for each that cannot be read.
func readSkillFiles(ctx context.Context, src source, root string, names []string) ([][]byte, error) {
	files := make([][]byte, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Go(func() {
			data, err := src.read(ctx, root+"/"+name+"/"+skillFile)
			if err == nil {
				files[i] = data
			}
		})
	}
	wg.Wait()
	return files, ctx.Err()
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

// branchSource reads skills from a branch of a project's canonical repo: at
// commit when it is set, which every read is then pinned to, and otherwise
// at wherever the branch is at the time of each read. Only reads of a commit
// are cached, since only a commit cannot change.
type branchSource struct {
	projects  Projects
	cache     *commitCache
	userID    int64
	projectID int64
	branch    string
	commit    string
}

// rev is what each read is of.
func (s branchSource) rev() string {
	if s.commit != "" {
		return s.commit
	}
	return s.branch
}

// cached reads through the cache when the source is pinned to a commit,
// keeping what was found, including that nothing is there, and leaving out
// any other failure.
func (s branchSource) cached(ctx context.Context, k commitKey, fetch func(context.Context) (cached, error)) (cached, error) {
	if s.commit == "" || s.cache == nil {
		return fetch(ctx)
	}
	k.userID, k.projectID, k.commit = s.userID, s.projectID, s.commit
	if v, ok := s.cache.get(k); ok {
		if v.missing {
			return cached{}, errNotFound
		}
		return v, nil
	}
	v, err := fetch(ctx)
	switch {
	case errors.Is(err, errNotFound):
		s.cache.put(cached{key: k, missing: true})
	case err == nil:
		v.key = k
		s.cache.put(v)
	}
	return v, err
}

func (s branchSource) list(ctx context.Context, dir string) ([]entry, error) {
	v, err := s.cached(ctx, commitKey{path: dir, dir: true}, func(ctx context.Context) (cached, error) {
		tree, err := s.projects.TreeAt(ctx, s.userID, s.projectID, s.rev(), dir)
		if err != nil {
			return cached{}, notFound(err)
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
		return cached{entries: entries}, nil
	})
	return v.entries, err
}

func (s branchSource) read(ctx context.Context, p string) ([]byte, error) {
	v, err := s.cached(ctx, commitKey{path: p}, func(ctx context.Context) (cached, error) {
		data, err := s.projects.FileAt(ctx, s.userID, s.projectID, s.rev(), p)
		return cached{data: data}, notFound(err)
	})
	return v.data, err
}

func (s branchSource) describe(dir string) string {
	if s.commit == "" {
		return dir + " on the branch `" + s.branch + "`"
	}
	return dir + " on the branch `" + s.branch + "` at commit " + s.commit[:min(12, len(s.commit))]
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
