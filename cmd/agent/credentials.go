package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// A standalone workspace may hold credentials the user put there: SSH keys,
// tokens in git config or remote URLs, credential stores. Converting it in
// place makes it a project workspace, where the IDE assistant runs and no
// credential may stay, so devpad lists them for the user and, with their
// consent, strips exactly those. Values never leave the container, and never
// appear in a location or error: only their kind and where they are.
//
// Git config is read file by file. Every config file devpad can find is
// searched: system, global, each repo's (nested repos, bare repos,
// submodules under .git/modules and worktree config included), and every
// file those include, followed whatever an includeIf condition says, so a
// credential is found and stripped in the file that actually holds it.

// Credential kinds.
const (
	credSSHKey         = "ssh-key"
	credGHToken        = "gh-token"
	credGitCredentials = "git-credentials"
	credNetrc          = "netrc"
	credHelper         = "credential-helper"
	credURLRewrite     = "url-rewrite"
	credHTTPHeader     = "http-header"
	credRemoteURL      = "remote-url"
	// credConfigInclude is an include directive whose file devpad could not
	// read, so whatever it holds is unknown.
	credConfigInclude = "config-include"
)

const (
	// credentialScanMaxDirs bounds the search for repos, in the workspace
	// and the home directory together.
	credentialScanMaxDirs = 100000
	// maxIncludeDepth bounds following config includes.
	maxIncludeDepth = 10
	// sshKeyMaxDepth is how deep ~/.ssh is searched for keys.
	sshKeyMaxDepth = 3
)

// devpadSSHFiles are always removed from a converted workspace, whatever
// they hold, so they are not listed as the user's own credentials.
var devpadSSHFiles = []string{"id_ed25519", "id_ed25519.pub"}

var (
	helperKeyRe     = regexp.MustCompile(`^credential\.(.*\.)?helper$`)
	urlRewriteKeyRe = regexp.MustCompile(`^url\..*\.(insteadof|pushinsteadof)$`)
	extraHeaderRe   = regexp.MustCompile(`^http\.(.*\.)?extraheader$`)
	urlValueKeyRe   = regexp.MustCompile(`^(remote\..*\.(url|pushurl)|submodule\..*\.url)$`)
	includeKeyRe    = regexp.MustCompile(`^include(if\..*)?\.path$`)
	// urlUserinfoRe matches the userinfo of a URL inside a config key or
	// message, so locations and errors never carry a token.
	urlUserinfoRe = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/\s]*@`)
)

// Strip actions.
const (
	stripRemoveFile = "remove-file"
	stripUnset      = "unset"
	stripUnsetValue = "unset-value"
	stripCleanURL   = "clean-url"
)

// stripTarget is one thing to remove for a credential. A credential can
// have several: a helper and the store file it names, or a key set in two
// files of the same scope.
type stripTarget struct {
	action string
	// path is the file to delete for stripRemoveFile.
	path string
	// file is the git config file holding key; system says it is the system
	// file, which may need root.
	file   string
	system bool
	// key is the raw config key and value the exact value; neither is shown.
	key   string
	value string
}

type credentialItem struct {
	// ID is stable across detections, so the user's consent names exactly
	// the items they saw.
	ID       string `json:"id"`
	Kind     string `json:"kind"`
	Location string `json:"location"`

	targets []stripTarget
}

type credentialScan struct {
	Items []credentialItem
	// Includes names the config includes that were followed and searched.
	Includes []string
	// Incomplete is set when the search for repos stopped at its limit.
	Incomplete bool
}

// configFile is a git config file to search.
type configFile struct {
	path   string
	label  string
	system bool
	depth  int
}

type credentialDetector struct {
	ctx        context.Context
	root, home string
	scan       credentialScan
	byID       map[string]int
	gitDirs    map[string]bool
	configs    map[string]bool
	queue      []configFile
	dirs       int
}

// detectCredentials lists the credentials in the home directory, in git's
// system and global config, and in every repo in the workspace or home.
func detectCredentials(ctx context.Context, root string) credentialScan {
	d := &credentialDetector{
		ctx:     ctx,
		root:    root,
		byID:    map[string]int{},
		gitDirs: map[string]bool{},
		configs: map[string]bool{},
		scan:    credentialScan{Items: []credentialItem{}, Includes: []string{}},
	}
	if home, err := os.UserHomeDir(); err == nil {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		d.home = home
	}
	if d.home != "" {
		d.homeFiles()
	}
	d.queueSystemAndGlobal()

	// Search the workspace and the home directory for repos, once each.
	switch {
	case d.home == "" || inside(d.home, root):
		d.walkGitDirs(root)
	case inside(root, d.home):
		d.walkGitDirs(d.home)
	default:
		d.walkGitDirs(root)
		d.walkGitDirs(d.home)
	}

	for len(d.queue) > 0 {
		f := d.queue[0]
		d.queue = d.queue[1:]
		d.scanConfig(f)
	}
	return d.scan
}

// inside reports whether path is dir or below it.
func inside(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// display names a path as the user sees it: /workspace/... or ~/...
func (d *credentialDetector) display(path string) string {
	if inside(path, d.root) {
		rel, _ := filepath.Rel(d.root, path)
		if rel == "." {
			return "/workspace"
		}
		return "/workspace/" + filepath.ToSlash(rel)
	}
	if d.home != "" && inside(path, d.home) {
		rel, _ := filepath.Rel(d.home, path)
		if rel == "." {
			return "~"
		}
		return "~/" + filepath.ToSlash(rel)
	}
	return path
}

func (d *credentialDetector) add(kind, location string, targets ...stripTarget) {
	id := kind + ":" + location
	if i, ok := d.byID[id]; ok {
		d.scan.Items[i].targets = append(d.scan.Items[i].targets, targets...)
		return
	}
	d.byID[id] = len(d.scan.Items)
	d.scan.Items = append(d.scan.Items, credentialItem{ID: id, Kind: kind, Location: location, targets: targets})
}

func xdgConfigHome(home string) string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return dir
	}
	return filepath.Join(home, ".config")
}

func (d *credentialDetector) homeFiles() {
	sshDir := filepath.Join(d.home, ".ssh")
	filepath.WalkDir(sshDir, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if e != nil && e.IsDir() && path != sshDir {
				return fs.SkipDir
			}
			return nil
		}
		rel, _ := filepath.Rel(sshDir, path)
		if e.IsDir() {
			if path != sshDir && strings.Count(rel, string(filepath.Separator))+1 > sshKeyMaxDepth {
				return fs.SkipDir
			}
			return nil
		}
		rel = filepath.ToSlash(rel)
		if slices.Contains(devpadSSHFiles, rel) || strings.HasSuffix(rel, ".pub") {
			return nil
		}
		if isPrivateKey(path) {
			d.add(credSSHKey, "~/.ssh/"+rel, stripTarget{action: stripRemoveFile, path: path})
		}
		return nil
	})

	xdg := xdgConfigHome(d.home)
	files := []struct{ kind, path string }{
		{credGHToken, filepath.Join(xdg, "gh", "hosts.yml")},
		{credGitCredentials, filepath.Join(d.home, ".git-credentials")},
		{credGitCredentials, filepath.Join(xdg, "git", "credentials")},
		{credNetrc, filepath.Join(d.home, ".netrc")},
		{credNetrc, filepath.Join(d.home, "_netrc")},
	}
	for _, f := range files {
		if _, err := os.Lstat(f.path); err == nil {
			d.add(f.kind, d.display(f.path), stripTarget{action: stripRemoveFile, path: f.path})
		}
	}
}

// isPrivateKey reports whether a file looks like a private key: PEM or
// OpenSSH, or PuTTY's format.
func isPrivateKey(path string) bool {
	f, err := os.Open(path)
	if err != nil {
		return false
	}
	defer f.Close()
	head := make([]byte, 4096)
	n, _ := f.Read(head)
	text := string(head[:n])
	return strings.Contains(text, "PRIVATE KEY") || strings.HasPrefix(text, "PuTTY-User-Key-File-")
}

func (d *credentialDetector) queueSystemAndGlobal() {
	system := os.Getenv("GIT_CONFIG_SYSTEM")
	if system == "" {
		system = "/etc/gitconfig"
	}
	d.queueConfig(configFile{path: system, label: "system git config", system: true})

	var globals []string
	if path := os.Getenv("GIT_CONFIG_GLOBAL"); path != "" {
		globals = []string{path}
	} else if d.home != "" {
		globals = []string{filepath.Join(d.home, ".gitconfig"), filepath.Join(xdgConfigHome(d.home), "git", "config")}
	}
	for _, path := range globals {
		d.queueConfig(configFile{path: path, label: d.display(path)})
	}
}

// queueConfig adds a config file to search, once, if it exists.
func (d *credentialDetector) queueConfig(f configFile) {
	real, err := filepath.EvalSymlinks(f.path)
	if err != nil {
		return
	}
	if info, err := os.Stat(real); err != nil || !info.Mode().IsRegular() || d.configs[real] {
		return
	}
	d.configs[real] = true
	d.queue = append(d.queue, f)
}

// walkGitDirs finds every git directory under start without stopping at a
// repo root: nested repos, bare repos, and the git directories that .git
// files of submodules and worktrees point at.
func (d *credentialDetector) walkGitDirs(start string) {
	filepath.WalkDir(start, func(path string, e fs.DirEntry, err error) error {
		if err != nil {
			if e != nil && e.IsDir() && path != start {
				return fs.SkipDir
			}
			return nil
		}
		if e.IsDir() {
			if d.dirs++; d.dirs > credentialScanMaxDirs {
				d.scan.Incomplete = true
				return fs.SkipAll
			}
			if e.Name() == ".git" || isBareRepo(path) {
				d.addGitDir(path)
				return fs.SkipDir
			}
			return nil
		}
		if e.Name() == ".git" && e.Type().IsRegular() {
			if gitDir := readGitFile(path); gitDir != "" {
				d.addGitDir(gitDir)
			}
		}
		return nil
	})
}

func isBareRepo(dir string) bool {
	if info, err := os.Lstat(filepath.Join(dir, "HEAD")); err != nil || !info.Mode().IsRegular() {
		return false
	}
	if info, err := os.Lstat(filepath.Join(dir, "objects")); err != nil || !info.IsDir() {
		return false
	}
	info, err := os.Lstat(filepath.Join(dir, "config"))
	return err == nil && info.Mode().IsRegular()
}

// readGitFile returns the git directory a .git file points at.
func readGitFile(path string) string {
	content, err := os.ReadFile(path)
	if err != nil || len(content) > 4096 {
		return ""
	}
	target, ok := strings.CutPrefix(strings.TrimSpace(string(content)), "gitdir:")
	if !ok {
		return ""
	}
	target = strings.TrimSpace(target)
	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(path), target)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		return ""
	}
	return target
}

// addGitDir queues a git directory's config files: its config and worktree
// config, those of its worktrees, and those of its submodules.
func (d *credentialDetector) addGitDir(gitDir string) {
	real, err := filepath.EvalSymlinks(gitDir)
	if err != nil || d.gitDirs[real] {
		return
	}
	d.gitDirs[real] = true

	label := d.display(filepath.Join(gitDir, "config"))
	if filepath.Base(gitDir) == ".git" {
		label = "the repository at " + d.display(filepath.Dir(gitDir))
	}
	d.queueConfig(configFile{path: filepath.Join(gitDir, "config"), label: label})
	worktreeConfig := filepath.Join(gitDir, "config.worktree")
	d.queueConfig(configFile{path: worktreeConfig, label: d.display(worktreeConfig)})

	if entries, err := os.ReadDir(filepath.Join(gitDir, "worktrees")); err == nil {
		for _, e := range entries {
			path := filepath.Join(gitDir, "worktrees", e.Name(), "config.worktree")
			d.queueConfig(configFile{path: path, label: d.display(path)})
		}
	}

	modules := filepath.Join(gitDir, "modules")
	filepath.WalkDir(modules, func(path string, e fs.DirEntry, err error) error {
		if err != nil || !e.IsDir() || path == modules {
			return nil
		}
		if d.dirs++; d.dirs > credentialScanMaxDirs {
			d.scan.Incomplete = true
			return fs.SkipAll
		}
		if isBareRepo(path) {
			d.addGitDir(path)
			return fs.SkipDir
		}
		return nil
	})
}

// configEntries lists every key and value set in one config file, without
// following its includes; those are searched as files of their own.
func (d *credentialDetector) configEntries(path string) [][2]string {
	out, err := gitCommandIn(d.ctx, "/", "config", "--file", path, "--null", "--list").Output()
	if err != nil {
		return nil
	}
	var entries [][2]string
	for _, entry := range bytes.Split(out, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		key, value, _ := strings.Cut(string(entry), "\n")
		entries = append(entries, [2]string{key, value})
	}
	return entries
}

// scanConfig lists the credentials in one config file and queues the files
// it includes.
func (d *credentialDetector) scanConfig(f configFile) {
	target := func(action, key, value string) stripTarget {
		return stripTarget{action: action, file: f.path, system: f.system, key: key, value: value}
	}
	where := func(key string) string { return f.label + ": " + redactURLs(key) }

	for _, e := range d.configEntries(f.path) {
		key, value := e[0], e[1]
		switch {
		case includeKeyRe.MatchString(key):
			d.include(f, key, value)
		case value == "" && (helperKeyRe.MatchString(key) || extraHeaderRe.MatchString(key)):
			// An empty value only resets the list git builds up.
		case helperKeyRe.MatchString(key):
			targets := []stripTarget{}
			location := where(key)
			// A store helper keeps its tokens in a file of its own, deleted
			// before the helper is unset so it is never left unreferenced.
			if store := d.storeFile(value); store != "" {
				targets = append(targets, stripTarget{action: stripRemoveFile, path: store})
				location += " (and its store " + d.display(store) + ")"
			}
			d.add(credHelper, location, append(targets, target(stripUnset, key, ""))...)
		case key == "core.askpass":
			d.add(credHelper, where(key), target(stripUnset, key, ""))
		case urlRewriteKeyRe.MatchString(key):
			base, _ := urlRewriteParts(key)
			if _, baseHas := stripURLCredentials(base); baseHas {
				d.add(credURLRewrite, where(key), target(stripUnset, key, ""))
			} else if _, valueHas := stripURLCredentials(value); valueHas {
				d.add(credURLRewrite, where(key), target(stripUnsetValue, key, value))
			}
		case extraHeaderRe.MatchString(key):
			// Any extra header may carry a token: Authorization, a GitLab
			// PRIVATE-TOKEN, an API key.
			d.add(credHTTPHeader, where(key), target(stripUnset, key, ""))
		case urlValueKeyRe.MatchString(key):
			if _, has := stripURLCredentials(value); has {
				d.add(credRemoteURL, where(key), target(stripCleanURL, key, value))
			}
		}
	}
}

// include queues a file a config includes. One devpad cannot read is listed
// as a credential: what it holds is unknown, so its include must go.
func (d *credentialDetector) include(f configFile, key, value string) {
	path := value
	switch {
	case path == "":
		return
	case path == "~" || strings.HasPrefix(path, "~/"):
		if d.home == "" {
			return
		}
		path = filepath.Join(d.home, strings.TrimPrefix(path, "~"))
	case !filepath.IsAbs(path):
		path = filepath.Join(filepath.Dir(f.path), path)
	}
	info, err := os.Stat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return // nothing is included
	}
	readable := err == nil && info.Mode().IsRegular()
	if readable {
		if file, openErr := os.Open(path); openErr != nil {
			readable = false
		} else {
			file.Close()
		}
	}
	if !readable || f.depth >= maxIncludeDepth {
		d.add(credConfigInclude, f.label+": "+redactURLs(key)+" ("+d.display(path)+")",
			stripTarget{action: stripUnsetValue, file: f.path, system: f.system, key: key, value: value})
		return
	}
	d.scan.Includes = append(d.scan.Includes, f.label+" includes "+d.display(path))
	d.queueConfig(configFile{path: path, label: d.display(path), system: f.system, depth: f.depth + 1})
}

// storeFile returns the file a "store --file <path>" helper keeps tokens in.
// The default store, ~/.git-credentials, is listed on its own.
func (d *credentialDetector) storeFile(helper string) string {
	fields := strings.Fields(helper)
	if len(fields) == 0 || (fields[0] != "store" && filepath.Base(fields[0]) != "git-credential-store") {
		return ""
	}
	var path string
	for i, field := range fields[1:] {
		if v, ok := strings.CutPrefix(field, "--file="); ok {
			path = v
		} else if field == "--file" && i+2 < len(fields) {
			path = fields[i+2]
		}
	}
	path = strings.Trim(path, `"'`)
	if path == "" {
		return ""
	}
	if strings.HasPrefix(path, "~/") && d.home != "" {
		path = filepath.Join(d.home, path[2:])
	}
	if !filepath.IsAbs(path) {
		return ""
	}
	if _, err := os.Lstat(path); err != nil {
		return ""
	}
	return path
}

// redactURLs removes the userinfo of every URL in s.
func redactURLs(s string) string {
	return urlUserinfoRe.ReplaceAllString(s, "$1")
}

// urlRewriteParts splits url.<base>.insteadof into its base and suffix. The
// base can hold dots, so git takes the last one as the separator.
func urlRewriteParts(key string) (base, suffix string) {
	rest := strings.TrimPrefix(key, "url.")
	i := strings.LastIndex(rest, ".")
	if i < 0 {
		return rest, ""
	}
	return rest[:i], rest[i+1:]
}

// stripCredential removes one detected credential. Errors name where, never
// a value or a raw key.
func (d *credentialDetector) stripCredential(item credentialItem) error {
	for _, t := range item.targets {
		var err error
		switch t.action {
		case stripRemoveFile:
			err = removeCredentialFile(t.path, d.root, d.home)
		case stripUnset:
			err = runConfig(d.ctx, t, true, "--unset-all", t.key)
		case stripUnsetValue:
			err = runConfig(d.ctx, t, true, "--fixed-value", "--unset-all", t.key, t.value)
		case stripCleanURL:
			// Replacing in one write keeps the remote's URL at every moment.
			clean, _ := stripURLCredentials(t.value)
			err = runConfig(d.ctx, t, false, "--fixed-value", "--replace-all", t.key, clean, t.value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// removeCredentialFile deletes a credential file. A symlink goes with the
// file it points at, as long as that is in the home directory or the
// workspace; one pointing elsewhere is reported, not followed.
func removeCredentialFile(path string, allowed ...string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("checking it: %w", err)
	}
	switch {
	case info.Mode()&fs.ModeSymlink != 0:
		target, err := filepath.EvalSymlinks(path)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("resolving the link: %w", err)
		}
		if err == nil {
			if !slices.ContainsFunc(allowed, func(dir string) bool { return dir != "" && inside(target, dir) }) {
				return fmt.Errorf("it links to %s, outside the home directory and /workspace; remove that file yourself", target)
			}
			if t, err := os.Stat(target); err == nil && t.IsDir() {
				return errors.New("it links to a directory")
			}
			if err := os.Remove(target); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return fmt.Errorf("removing the file it links to: %w", err)
			}
		}
	case info.IsDir():
		return errors.New("it is a directory")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("removing it: %w", err)
	}
	return nil
}

// gitConfigNotFound is git config's exit status for a key or value that is
// not set.
const gitConfigNotFound = 5

// runConfig changes one config file. The system file belongs to root, so a
// refused write there is retried with sudo. git's output is never part of
// the error: it can echo a key holding a token.
func runConfig(ctx context.Context, t stripTarget, missingOK bool, args ...string) error {
	full := append([]string{"config", "--file", t.file}, args...)
	err := gitCommandIn(ctx, "/", full...).Run()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if missingOK && errors.As(err, &exitErr) && exitErr.ExitCode() == gitConfigNotFound {
		return nil // already gone
	}
	if t.system {
		cmd := exec.CommandContext(ctx, "sudo", append([]string{"-n", "git"}, full...)...)
		cmd.Dir = "/"
		if sudoErr := cmd.Run(); sudoErr == nil {
			return nil
		}
		return fmt.Errorf("git config could not change the system config, even with sudo: %w", err)
	}
	return fmt.Errorf("git config could not change it: %w", err)
}

type credentialFailure struct {
	credentialItem
	// Reason says why, with no value in it.
	Reason string `json:"reason"`
}

// handleCredentialsStrip strips the credentials named by id, then lists what
// is still there. Ids no longer detected are skipped: they are already gone.
func handleCredentialsStrip(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxGitRequestBody)
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid request body")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), repoTimeout)
	defer cancel()

	root, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "resolving workspace root: "+err.Error())
		return
	}
	d := &credentialDetector{ctx: ctx, root: root}
	if home, err := os.UserHomeDir(); err == nil {
		if resolved, err := filepath.EvalSymlinks(home); err == nil {
			home = resolved
		}
		d.home = home
	}
	stripped := []credentialItem{}
	failed := []credentialFailure{}
	for _, item := range detectCredentials(ctx, root).Items {
		if !slices.Contains(req.IDs, item.ID) {
			continue
		}
		if err := d.stripCredential(item); err != nil {
			failed = append(failed, credentialFailure{item, redactURLs(err.Error())})
			continue
		}
		stripped = append(stripped, item)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"stripped":  stripped,
		"failed":    failed,
		"remaining": detectCredentials(ctx, root).Items,
	})
}
