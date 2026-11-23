package mergerequest

import (
	"cmp"
	"path"
	"slices"
	"strings"
	"unicode"

	"github.com/devpad-org/devpad/internal/agent"
	"github.com/devpad-org/devpad/internal/project"
)

// The look-closely mark. A pipeline reads its jobs from the target branch,
// so a change can't rewrite them, but it can still weaken what they
// exercise: delete a test, change a script the jobs run, what a build
// script or a dependency does, or a lint or type-check setting, or the CI
// configuration of another system. Devpad marks a merge request whose
// change does any of that, from its diff, so that the user, and the
// reviewer agent, know where to look closely. It is a mark, never a gate:
// nothing waits on it.
//
// LookCloselyAt is the one rule, a pure function of the change's files and
// the scripts of the target's jobs; lookclosely_mark.go works it out and
// keeps it, and reviewPrompt tells the reviewer of it.

// MarkReason is why the look-closely mark lists a file.
type MarkReason string

const (
	// MarkTestsDeleted is a test file the change deletes, turns into
	// something else (a symlink, say), renames to a name no test runner
	// takes for a test, or moves where its runner never looks.
	MarkTestsDeleted MarkReason = "tests_deleted"
	// MarkPipeline is the pipeline file itself, which decides the jobs of
	// every merge request after this one.
	MarkPipeline MarkReason = "pipeline"
	// MarkJobFile is a file the target's pipeline jobs name in their
	// scripts, such as scripts/test.sh.
	MarkJobFile MarkReason = "job_file"
	// MarkCI is another CI system's configuration.
	MarkCI MarkReason = "ci"
	// MarkChecks is a lint, type-check, formatting or test runner setting.
	MarkChecks MarkReason = "checks"
	// MarkBuild is a build or task script, a dependency list, a lockfile
	// or a toolchain pin.
	MarkBuild MarkReason = "build"
)

// MarkReasons lists every reason, in the order the mark shows them and
// keeps files by, which the app's LookCloselyReason lists too.
func MarkReasons() []MarkReason {
	return []MarkReason{MarkTestsDeleted, MarkPipeline, MarkJobFile, MarkCI, MarkChecks, MarkBuild}
}

func reasonRank(r MarkReason) int {
	return slices.Index(MarkReasons(), r)
}

// MarkedFile is a file the look-closely mark lists. Its paths are the
// change's, which its author chose.
type MarkedFile struct {
	Path    string     `json:"path"`
	OldPath string     `json:"oldPath,omitempty"`
	Reason  MarkReason `json:"reason"`
}

// ReasonCount is how many files of a change the mark lists for a reason.
type ReasonCount struct {
	Reason MarkReason `json:"reason"`
	Files  int        `json:"files"`
}

// LookClosely is the look-closely mark of a change: at most maxMarkedFiles
// of the files it lists, those of the first reasons first, and how many
// files it lists for each reason, so every reason shows however many files
// are cut.
type LookClosely struct {
	Files   []MarkedFile  `json:"files"`
	Reasons []ReasonCount `json:"reasons"`
}

// LookCloselySummary is what a merge request keeps of its mark, and the
// list of merge requests shows: how many files it lists for each reason,
// and none of their paths.
type LookCloselySummary struct {
	Reasons []ReasonCount `json:"reasons"`
}

// Marked reports whether the mark lists anything.
func (s *LookCloselySummary) Marked() bool {
	return s != nil && len(s.Reasons) > 0
}

// maxMarkedFiles bounds the files a mark lists by name.
const maxMarkedFiles = 50

// Marked reports whether the mark lists anything.
func (l *LookClosely) Marked() bool {
	return l != nil && len(l.Reasons) > 0
}

// More is how many files the mark counts but doesn't name.
func (l *LookClosely) More() int {
	n := 0
	for _, c := range l.Reasons {
		n += c.Files
	}
	return n - len(l.Files)
}

// Summary is what the merge request keeps of the mark.
func (l *LookClosely) Summary() LookCloselySummary {
	return LookCloselySummary{Reasons: slices.Clone(l.Reasons)}
}

// LookCloselyAt works out the look-closely mark of a change from its diff,
// as git's name-status lists it, renames found, and the scripts of the
// jobs its merge request's pipeline runs, which come from the target's
// pipeline and never from the change.
func LookCloselyAt(files []agent.RepoDiffFile, jobScripts []string) LookClosely {
	named := jobPaths(jobScripts)
	var marked []MarkedFile
	for _, f := range files {
		if reason, ok := markOf(f, named); ok {
			marked = append(marked, MarkedFile{Path: f.Path, OldPath: f.OldPath, Reason: reason})
		}
	}
	slices.SortStableFunc(marked, func(a, b MarkedFile) int { return cmp.Compare(reasonRank(a.Reason), reasonRank(b.Reason)) })
	mark := LookClosely{Files: marked[:min(len(marked), maxMarkedFiles)], Reasons: []ReasonCount{}}
	if mark.Files == nil {
		mark.Files = []MarkedFile{}
	}
	for _, r := range MarkReasons() {
		if n := len(slices.DeleteFunc(slices.Clone(marked), func(f MarkedFile) bool { return f.Reason != r })); n > 0 {
			mark.Reasons = append(mark.Reasons, ReasonCount{Reason: r, Files: n})
		}
	}
	return mark
}

// markOf is why the mark lists a file of a change, if it does: the first of
// MarkReasons that applies, its old path counting as much as its new one.
// named are the paths the target's jobs name.
func markOf(f agent.RepoDiffFile, named map[string]bool) (MarkReason, bool) {
	paths := []string{f.Path}
	if f.OldPath != "" {
		paths = append(paths, f.OldPath)
	}
	switch {
	case deletesTest(f):
		return MarkTestsDeleted, true
	case slices.ContainsFunc(paths, project.IsPipelineFile):
		return MarkPipeline, true
	case slices.ContainsFunc(paths, func(p string) bool { return named[p] }):
		return MarkJobFile, true
	case slices.ContainsFunc(paths, isCIConfig):
		return MarkCI, true
	case slices.ContainsFunc(paths, isCheckSetting):
		return MarkChecks, true
	case slices.ContainsFunc(paths, isBuildFile):
		return MarkBuild, true
	}
	return "", false
}

// jobPaths are the paths in the repo the scripts name, as tokens of the
// shell that look like paths: each holding a "/" or a ".", relative to
// /workspace, where a job's checkout is, or given under it. A command's
// path is also taken from the directory a "cd" before it in the script
// went to, and from the one a -C (make, git), --prefix (npm) or --cwd
// (yarn) of its own names; both the path as written and as joined count,
// since a cd that can't be followed, such as to a variable, leaves which
// is meant unknown. A path held in a variable, a glob or wildcard such as
// Go's ./..., or a directory names no file here.
func jobPaths(scripts []string) map[string]bool {
	named := map[string]bool{}
	for _, script := range scripts {
		dir := ""
		for _, command := range strings.FieldsFunc(script, func(r rune) bool { return strings.ContainsRune(";&|()\n", r) }) {
			tokens := strings.FieldsFunc(command, func(r rune) bool {
				return unicode.IsSpace(r) || strings.ContainsRune("<>'\"`=,", r)
			})
			if len(tokens) == 0 {
				continue
			}
			if tokens[0] == "cd" {
				next, ok := "", false
				if len(tokens) > 1 {
					next, ok = repoPath(dir, tokens[1])
				}
				// Where a cd that can't be followed went is unknown, and
				// the paths after it count as written.
				dir = next
				if !ok {
					dir = ""
				}
				continue
			}
			bases := []string{"", dir}
			for i, token := range tokens[:len(tokens)-1] {
				if token == "-C" || token == "--prefix" || token == "--cwd" {
					if base, ok := repoPath(dir, tokens[i+1]); ok {
						bases = append(bases, base)
					}
				}
			}
			for _, token := range tokens {
				if !strings.ContainsAny(token, "/.") {
					continue
				}
				for _, base := range bases {
					if p, ok := repoPath(base, token); ok && p != "" {
						named[p] = true
					}
				}
			}
		}
	}
	return named
}

// repoPath is where token, a path a job's script gives from dir, is in the
// checkout, "" for its root, or false when it is no plain path there.
func repoPath(dir, token string) (string, bool) {
	if strings.ContainsAny(token, "$*?[{~\\") || strings.Contains(token, "...") || strings.HasPrefix(token, "-") {
		return "", false
	}
	if rest, ok := strings.CutPrefix(token, "/workspace/"); ok {
		dir, token = "", rest
	} else if token == "/workspace" {
		dir, token = "", "."
	}
	if strings.HasPrefix(token, "/") {
		return "", false
	}
	p := path.Clean(path.Join(dir, token))
	if p == ".." || strings.HasPrefix(p, "../") {
		return "", false
	}
	if p == "." {
		return "", true
	}
	return p, true
}

// deletesTest reports whether a change to f deletes a test: it removes a
// test file or changes its type (to a symlink, say), renames one to a name
// that is no test's, or moves one where its runner never looks.
func deletesTest(f agent.RepoDiffFile) bool {
	switch f.Status {
	case "D", "T":
		return isTestFile(f.Path)
	case "R":
		return isTestFile(f.OldPath) && (!isTestFile(f.Path) || neverRun(f.Path) && !neverRun(f.OldPath))
	}
	return false
}

// neverRun reports whether p is in a directory test runners pass over by
// convention: Go's testdata and those starting with "_" or ".", and vendored
// or installed dependencies. Jest's __tests__ and the like are run.
func neverRun(p string) bool {
	for _, dir := range strings.Split(path.Dir(p), "/") {
		switch {
		case dir == ".":
		case dir == "testdata", dir == "vendor", dir == "node_modules":
			return true
		case strings.HasPrefix(dir, "."), strings.HasPrefix(dir, "_") && !strings.HasPrefix(dir, "__"):
			return true
		}
	}
	return false
}

// testDirs are directories whose files are tests, or what tests read, by
// the common conventions.
var testDirs = []string{"test", "tests", "__tests__", "__snapshots__", "spec", "specs", "testdata", "e2e"}

// isTestFile reports whether p names a test file by the common
// conventions: Go's *_test.go, JavaScript's *.test.*, *.spec.* and
// Cypress's *.cy.*, Python's test_*.py, *_test.py and conftest.py, Ruby's
// *_spec.rb, Elixir's *_test.exs, the *Test and *Tests classes of Java,
// Kotlin, C#, PHP, Scala, Swift and Groovy, and anything in a test
// directory such as tests/, __tests__/ or Maven's src/test/.
func isTestFile(p string) bool {
	if p == "" {
		return false
	}
	lower := strings.ToLower(p)
	for _, dir := range strings.Split(path.Dir(lower), "/") {
		if slices.Contains(testDirs, dir) {
			return true
		}
	}
	base := path.Base(p)
	ext := path.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	lowerStem, lowerExt := strings.ToLower(stem), strings.ToLower(ext)
	switch {
	case strings.HasSuffix(lowerStem, "_test"), strings.HasSuffix(lowerStem, "_spec"):
		return true
	case strings.HasSuffix(lowerStem, ".test"), strings.HasSuffix(lowerStem, ".spec"), strings.HasSuffix(lowerStem, ".cy"):
		return true
	case lowerExt == ".py" && (strings.HasPrefix(lowerStem, "test_") || lowerStem == "conftest"):
		return true
	}
	switch lowerExt {
	case ".java", ".kt", ".kts", ".cs", ".php", ".scala", ".swift", ".groovy":
		return strings.HasSuffix(stem, "Test") || strings.HasSuffix(stem, "Tests")
	}
	return false
}

// isCIConfig reports whether p is a CI system's configuration: GitHub
// Actions' workflows and actions, Gitea and Forgejo Actions, GitLab CI,
// CircleCI, Buildkite, Jenkins, Azure Pipelines, Travis, Bitbucket
// Pipelines, Drone, Woodpecker, AppVeyor and Cloud Build.
func isCIConfig(p string) bool {
	lower := strings.ToLower(p)
	for _, dir := range []string{".github/workflows/", ".github/actions/", ".gitea/workflows/", ".forgejo/workflows/", ".circleci/", ".buildkite/", ".woodpecker/", ".gitlab/ci/"} {
		if strings.HasPrefix(lower, dir) {
			return true
		}
	}
	switch path.Base(lower) {
	case ".gitlab-ci.yml", ".gitlab-ci.yaml", "jenkinsfile", "azure-pipelines.yml", "azure-pipelines.yaml", ".travis.yml",
		"bitbucket-pipelines.yml", ".drone.yml", ".woodpecker.yml", ".woodpecker.yaml", "appveyor.yml", ".appveyor.yml", "cloudbuild.yaml", "cloudbuild.yml":
		return true
	}
	return false
}

// checkSettingPrefixes are the starts of the names of lint, type-check,
// formatting and test runner settings and setup files that come in several
// extensions, such as eslint.config.js and eslint.config.mjs.
var checkSettingPrefixes = []string{
	".eslintrc", "eslint.config.", ".prettierrc", "prettier.config.", ".golangci.", "vitest.config.", "vitest.workspace.",
	"vitest.setup.", "jest.config.", "jest.setup.", "setuptests.", "vite.config.", "karma.conf.", "playwright.config.",
	"cypress.config.", "babel.config.", ".babelrc", ".stylelintrc", "stylelint.config.", ".mocharc", ".nycrc",
}

// checkSettingNames are the names of the rest.
var checkSettingNames = []string{
	".eslintignore", ".prettierignore", "biome.json", "biome.jsonc", "ruff.toml", ".ruff.toml", "mypy.ini",
	".mypy.ini", "pytest.ini", ".coveragerc", ".flake8", ".pylintrc", "pylintrc", "setup.cfg", "tox.ini", "noxfile.py",
	"phpunit.xml", "phpunit.xml.dist", ".rubocop.yml", "rustfmt.toml", ".rustfmt.toml", "clippy.toml", ".clippy.toml",
	"staticcheck.conf", ".swiftlint.yml", "checkstyle.xml", "detekt.yml", ".pre-commit-config.yaml",
}

// isCheckSetting reports whether p is a lint, type-check, formatting or
// test runner setting or setup file: TypeScript's tsconfig*.json and
// jsconfig*.json, ESLint, Prettier, Biome, golangci-lint, Ruff, mypy,
// pytest, coverage, flake8, pylint, tox, nox, Vitest, Jest, Vite, Karma,
// Playwright, Cypress, Babel, Stylelint, Mocha, PHPUnit, RuboCop, rustfmt,
// Clippy, pre-commit and the like.
func isCheckSetting(p string) bool {
	base := path.Base(strings.ToLower(p))
	if (strings.HasPrefix(base, "tsconfig") || strings.HasPrefix(base, "jsconfig")) && strings.HasSuffix(base, ".json") {
		return true
	}
	if slices.Contains(checkSettingNames, base) {
		return true
	}
	return slices.ContainsFunc(checkSettingPrefixes, func(prefix string) bool { return strings.HasPrefix(base, prefix) })
}

// buildFileNames are the names of build and task scripts, dependency
// lists, lockfiles and toolchain pins.
var buildFileNames = []string{
	"makefile", "gnumakefile", "justfile", "taskfile.yml", "taskfile.yaml", "package.json", "package-lock.json",
	"npm-shrinkwrap.json", "yarn.lock", ".yarnrc", ".yarnrc.yml", "pnpm-lock.yaml", "pnpm-workspace.yaml", "bun.lock",
	"bun.lockb", ".npmrc", "deno.json", "deno.jsonc", "deno.lock", "go.mod", "go.sum", "go.work", "go.work.sum",
	"cargo.toml", "cargo.lock", "build.rs", "pyproject.toml", "setup.py", "pipfile", "pipfile.lock", "poetry.lock",
	"uv.lock", "gemfile", "gemfile.lock", "rakefile", "composer.json", "composer.lock", "build.gradle", "build.gradle.kts",
	"settings.gradle", "settings.gradle.kts", "gradle.properties", "pom.xml", "cmakelists.txt", "build.sbt", "mix.exs",
	"mix.lock", "build", "build.bazel", "workspace", "workspace.bazel", "module.bazel", ".bazelrc", ".bazelversion",
	".nvmrc", ".node-version", ".tool-versions", ".python-version", ".ruby-version", ".go-version", ".java-version",
	"rust-toolchain", "rust-toolchain.toml", "toolchain.toml",
}

// isBuildFile reports whether p is a build or task script, a dependency
// list, a lockfile or a toolchain pin: Make, just, Task, npm, Yarn, pnpm,
// Bun, Deno, Go modules (and their toolchain line), Cargo, Python's
// pyproject.toml, setup.py and requirements, Pipenv, Poetry, uv, Bundler,
// Rake, Composer, Gradle, Maven, CMake, sbt, Mix and Bazel, and the
// versions nvm, asdf, pyenv, rbenv and rustup pin.
func isBuildFile(p string) bool {
	base := path.Base(strings.ToLower(p))
	switch {
	case slices.Contains(buildFileNames, base), strings.HasSuffix(base, ".mk"), strings.HasSuffix(base, ".bzl"):
		return true
	case strings.HasPrefix(base, "requirements") && strings.HasSuffix(base, ".txt"):
		return true
	}
	return false
}
