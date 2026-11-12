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
	// readBatch is how many reads of a workspace run at once. Each goes to
	// its agent, so reading them one by one could take most of the time a
	// run waits for its skills.
	readBatch = 8
)

var (
	// errNotFound reports a file or directory a source does not have.
	errNotFound = errors.New("not found")
	// errSkipped reports a file left unread because the bytes a fetch could
	// read were spent.
	errSkipped = errors.New("not read: past the byte budget")
	// errTooLarge reports a file too large to read.
	errTooLarge = errors.New("file is too large to read")
)

// entry is one entry of a directory in a source. An entry that is neither
// dir nor file may be either, such as a symlink in a workspace.
type entry struct {
	name string
	dir  bool
	file bool
}

// listing is a directory's entries, or why there are none.
type listing struct {
	entries []entry
	err     error
}

// file is a file's contents, or why there are none.
type file struct {
	data []byte
	err  error
}

// fetched is what a fetch found, in the order it was asked.
type fetched struct {
	dirs  []listing
	files []file
}

// source is a tree of files skills are read from: a workspace, or a commit
// of the canonical repo.
type source interface {
	// fetch lists dirs and reads files, in one request where the source
	// can. Each answer is the entries or contents found, errNotFound when
	// nothing is there, or why it could not be read; fetch itself fails
	// only when nothing could be. When maxBytes is above zero, files are
	// read in order until that many bytes of them have been, so the one
	// that reaches it is still read, and the rest get errSkipped.
	fetch(ctx context.Context, dirs, files []string, maxBytes int) (fetched, error)
	// chunk is how many files discovery fetches at once, 0 for all of them.
	chunk() int
	// describe says where a skill's directory is, for the model.
	describe(dir string) string
}

// readFile is a fetch of one file.
func readFile(ctx context.Context, src source, p string) ([]byte, error) {
	f, err := src.fetch(ctx, nil, []string{p}, 0)
	if err != nil {
		return nil, err
	}
	return f.files[0].data, f.files[0].err
}

// discover finds the skills in a source, in name order.
func discover(ctx context.Context, src source) ([]Skill, error) {
	f, err := src.fetch(ctx, Roots, nil, 0)
	if err != nil {
		return nil, err
	}
	return discoverIn(ctx, src, f.dirs)
}

// candidate is a directory under a root that may be a skill.
type candidate struct {
	name string
	dir  string
}

// discoverIn finds the skills in a source from its roots' listings, one for
// each of Roots in order. It reads the SKILL.md files in chunks the source
// picks, all at once from a commit.
func discoverIn(ctx context.Context, src source, roots []listing) ([]Skill, error) {
	var candidates []candidate
	for i, root := range Roots {
		l := roots[i]
		if errors.Is(l.err, errNotFound) {
			continue
		}
		if l.err != nil {
			return nil, l.err
		}
		sort.Slice(l.entries, func(a, b int) bool { return l.entries[a].name < l.entries[b].name })
		for _, e := range l.entries {
			if !e.file && namePattern.MatchString(e.name) {
				candidates = append(candidates, candidate{name: e.name, dir: root + "/" + e.name})
			}
		}
	}

	var found []Skill
	seen := map[string]bool{}
	budget := maxDiscoveryBytes
	size := src.chunk()
	if size <= 0 {
		size = len(candidates)
	}
	for start := 0; start < len(candidates) && len(found) < maxSkills && budget > 0; start += size {
		// A name an earlier chunk found is shadowed, so it is not read.
		var batch []candidate
		var paths []string
		for _, c := range candidates[start:min(start+size, len(candidates))] {
			if !seen[c.name] {
				batch = append(batch, c)
				paths = append(paths, c.dir+"/"+skillFile)
			}
		}
		if len(batch) == 0 {
			continue
		}
		f, err := src.fetch(ctx, nil, paths, budget)
		if err != nil {
			return nil, err
		}
		for i, c := range batch {
			if len(found) == maxSkills || budget <= 0 {
				break
			}
			r := f.files[i]
			if errors.Is(r.err, errSkipped) {
				budget = 0
				break
			}
			// One with no SKILL.md, or one that cannot be read, is no skill.
			if r.err != nil {
				continue
			}
			budget -= len(r.data)
			// An earlier root's skill in the same chunk shadows this one.
			if seen[c.name] {
				continue
			}
			if s, ok := parseSkill(c.name, c.dir, r.data); ok {
				seen[c.name] = true
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

// workspaceSource reads skills from a workspace's /workspace, one request
// for each directory or file, readBatch at a time.
type workspaceSource struct {
	files       Workspaces
	userID      int64
	workspaceID int64
}

func (s workspaceSource) chunk() int { return readBatch }

func (s workspaceSource) fetch(ctx context.Context, dirs, files []string, maxBytes int) (fetched, error) {
	out := fetched{dirs: make([]listing, len(dirs)), files: make([]file, len(files))}
	slots := make(chan struct{}, readBatch)
	var wg sync.WaitGroup
	run := func(read func()) {
		wg.Go(func() {
			slots <- struct{}{}
			defer func() { <-slots }()
			read()
		})
	}
	for i, d := range dirs {
		run(func() { out.dirs[i].entries, out.dirs[i].err = s.list(ctx, d) })
	}
	for i, p := range files {
		run(func() { out.files[i].data, out.files[i].err = s.read(ctx, p) })
	}
	wg.Wait()
	// Sizes are known only once read, so the budget is kept after, the
	// same as a commit's: what is past it is left out.
	if maxBytes > 0 {
		spent := 0
		for i := range out.files {
			if out.files[i].err != nil {
				continue
			}
			if spent >= maxBytes {
				out.files[i] = file{err: errSkipped}
				continue
			}
			spent += len(out.files[i].data)
		}
	}
	return out, ctx.Err()
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
	ReadAt(ctx context.Context, userID, projectID int64, in agent.RepoReadRequest) (*agent.RepoRead, error)
}

// branchSource reads skills from a branch of a project's canonical repo,
// each fetch in one request. Every read is of one commit: the one the review
// compares against, or, for a review from before devpad recorded that, the
// one its first fetch found the branch at. Reads of the commit are cached,
// and a commit cannot change, so only that first fetch of a branch goes to
// the repo every time. A branchSource is used by one call at a time.
type branchSource struct {
	projects  Projects
	cache     *commitCache
	userID    int64
	projectID int64
	branch    string
	commit    string
}

func (s *branchSource) chunk() int { return 0 }

func (s *branchSource) key(p string, dir bool) commitKey {
	return commitKey{userID: s.userID, projectID: s.projectID, commit: s.commit, path: p, dir: dir}
}

// cached is what the cache has for a path, once the commit is known.
func (s *branchSource) cached(p string, dir bool) (cached, bool) {
	if s.commit == "" || s.cache == nil {
		return cached{}, false
	}
	return s.cache.get(s.key(p, dir))
}

func (s *branchSource) keep(p string, dir bool, v cached) {
	if s.commit == "" || s.cache == nil {
		return
	}
	v.key = s.key(p, dir)
	s.cache.put(v)
}

func (s *branchSource) fetch(ctx context.Context, dirs, files []string, maxBytes int) (fetched, error) {
	out := fetched{dirs: make([]listing, len(dirs)), files: make([]file, len(files))}
	// What the cache has is answered from it, and only the rest asked for.
	var askDirs, askFiles []string
	var dirAt, fileAt []int
	for i, d := range dirs {
		if v, ok := s.cached(d, true); ok {
			out.dirs[i] = listing{entries: v.entries, err: v.err()}
			continue
		}
		askDirs, dirAt = append(askDirs, d), append(dirAt, i)
	}
	spent := 0
	for i, p := range files {
		if v, ok := s.cached(p, false); ok {
			out.files[i] = file{data: v.data, err: v.err()}
			spent += len(v.data)
			continue
		}
		askFiles, fileAt = append(askFiles, p), append(fileAt, i)
	}
	// The budget is the request's less what the cache answered.
	if maxBytes > 0 {
		if maxBytes -= spent; maxBytes <= 0 {
			for _, i := range fileAt {
				out.files[i] = file{err: errSkipped}
			}
			askFiles, fileAt = nil, nil
		}
	}
	if len(askDirs) == 0 && len(askFiles) == 0 {
		return out, nil
	}

	rev := s.commit
	if rev == "" {
		rev = s.branch
	}
	res, err := s.projects.ReadAt(ctx, s.userID, s.projectID, agent.RepoReadRequest{Rev: rev, Dirs: askDirs, Files: askFiles, MaxBytes: maxBytes})
	if err != nil {
		// A branch that is not there has nothing on it to read.
		if errors.Is(notFound(err), errNotFound) {
			for _, i := range dirAt {
				out.dirs[i].err = errNotFound
			}
			for _, i := range fileAt {
				out.files[i].err = errNotFound
			}
			return out, nil
		}
		return fetched{}, err
	}
	if s.commit == "" && isCommit(res.Commit) {
		s.commit = res.Commit
	}
	for j, d := range res.Dirs {
		l := listing{err: errNotFound}
		if d.Found {
			l = listing{entries: treeEntries(d.Entries)}
		}
		out.dirs[dirAt[j]] = l
		s.keep(askDirs[j], true, cached{entries: l.entries, missing: !d.Found})
	}
	for j, f := range res.Files {
		r := file{data: f.Content}
		switch {
		case !f.Found:
			r = file{err: errNotFound}
			s.keep(askFiles[j], false, cached{missing: true})
		case f.TooLarge:
			r = file{err: errTooLarge}
		case f.Skipped:
			r = file{err: errSkipped}
		default:
			s.keep(askFiles[j], false, cached{data: f.Content})
		}
		out.files[fileAt[j]] = r
	}
	return out, nil
}

// treeEntries keeps the entries of a commit's directory skills can be in,
// up to maxListedEntries. A symlink in a commit is a blob, which is not
// followed: only directories hold skills. Submodules are left out.
func treeEntries(in []agent.RepoTreeEntry) []entry {
	entries := make([]entry, 0, min(len(in), maxListedEntries))
	for _, e := range in {
		if len(entries) == maxListedEntries {
			break
		}
		if e.Type == "blob" || e.Type == "tree" {
			entries = append(entries, entry{name: e.Name, dir: e.Type == "tree", file: e.Type == "blob"})
		}
	}
	return entries
}

// isCommit reports whether s is a full commit hash, SHA-1 or SHA-256.
func isCommit(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func (s *branchSource) describe(dir string) string {
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
