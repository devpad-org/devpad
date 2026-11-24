package docs

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	aitools "github.com/devpad-org/devpad/internal/ai/tools"
	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/workspace"
)

func TestPagesLoad(t *testing.T) {
	if len(pages) == 0 {
		t.Fatal("no pages")
	}
	var admin, user int
	for _, p := range pages {
		if p.Admin {
			admin++
		} else {
			user++
		}
		if len(p.Sections) == 0 {
			t.Errorf("%s has no sections", p.Slug)
		}
	}
	if admin == 0 || user == 0 {
		t.Errorf("want pages for admins and for everyone, have %d and %d", admin, user)
	}
	if got := len(Pages(false)); got != user {
		t.Errorf("Pages(false) lists %d pages, want %d", got, user)
	}
}

func TestParse(t *testing.T) {
	const front = "---\ntitle: T\nsummary: S\n---\n"
	tests := []struct {
		name    string
		data    string
		wantErr string
		want    []Section
	}{
		{name: "sections", data: front + "Intro\n\n## One {#one}\n\n### Two `x` {#two}\n", want: []Section{{"one", "One", 2}, {"two", "Two `x`", 3}}},
		{name: "headings in code blocks are not sections", data: front + "```sh\n# a comment\n## not a heading\n```\n## One {#one}\n", want: []Section{{"one", "One", 2}}},
		{name: "admin", data: "---\ntitle: T\nsummary: S\naudience: admin\n---\n## One {#one}\n", want: []Section{{"one", "One", 2}}},
		{name: "no front matter", data: "## One {#one}\n", wantErr: "front matter"},
		{name: "unclosed front matter", data: "---\ntitle: T\n", wantErr: "not closed"},
		{name: "no summary", data: "---\ntitle: T\n---\n## One {#one}\n", wantErr: "summary"},
		{name: "unknown key", data: "---\ntitle: T\nsummary: S\norder: 1\n---\n", wantErr: "unknown"},
		{name: "unknown audience", data: "---\ntitle: T\nsummary: S\naudience: users\n---\n", wantErr: "audience"},
		{name: "h1", data: front + "# Title\n", wantErr: "only h1"},
		{name: "no anchor", data: front + "## One\n", wantErr: "no {#anchor}"},
		{name: "duplicate anchor", data: front + "## One {#one}\n## Again {#one}\n", wantErr: "two headings"},
		{name: "unclosed code block", data: front + "```\ncode\n", wantErr: "not closed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			page, err := parse("p", tt.data)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one mentioning %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(page.Sections) != len(tt.want) {
				t.Fatalf("sections = %+v, want %+v", page.Sections, tt.want)
			}
			for i := range tt.want {
				if page.Sections[i] != tt.want[i] {
					t.Errorf("section %d = %+v, want %+v", i, page.Sections[i], tt.want[i])
				}
			}
		})
	}
}

func TestLoadRefusesBadFiles(t *testing.T) {
	page := "---\ntitle: T\nsummary: S\n---\n## One {#one}\n"
	if _, err := load(fstest.MapFS{"intro.md": {Data: []byte(page)}}); err == nil {
		t.Error("a file not named NN-slug.md loaded")
	}
	if _, err := load(fstest.MapFS{"01-a.md": {Data: []byte(page)}, "02-a.md": {Data: []byte(page)}}); err == nil {
		t.Error("two pages with one slug loaded")
	}
	got, err := load(fstest.MapFS{"02-b.md": {Data: []byte(page)}, "01-a.md": {Data: []byte(page)}})
	if err != nil {
		t.Fatal(err)
	}
	if got[0].Slug != "a" || got[1].Slug != "b" {
		t.Errorf("pages are %s, %s; want them in file name order", got[0].Slug, got[1].Slug)
	}
}

// resolve finds the page and section a /docs link points at.
func resolve(link string) (Page, error) {
	rest, ok := strings.CutPrefix(link, "/docs/")
	if !ok {
		return Page{}, fmt.Errorf("%q is not a /docs/ link", link)
	}
	slug, anchor, _ := strings.Cut(rest, "#")
	page, ok := Find(slug, true)
	if !ok {
		return Page{}, fmt.Errorf("%q: there is no page %q", link, slug)
	}
	if anchor == "" {
		return page, nil
	}
	for _, s := range page.Sections {
		if s.ID == anchor {
			return page, nil
		}
	}
	return Page{}, fmt.Errorf("%q: %s has no section #%s", link, slug, anchor)
}

// TestLinksResolve checks every link from one page to another, or within a
// page, and that only admin pages link to admin pages, which no one else can
// open.
func TestLinksResolve(t *testing.T) {
	link := regexp.MustCompile(`\]\(((?:/docs/|#)[^)\s]*)\)`)
	for _, p := range pages {
		for _, m := range link.FindAllStringSubmatch(p.Body, -1) {
			target := m[1]
			if strings.HasPrefix(target, "#") {
				target = "/docs/" + p.Slug + target
			}
			to, err := resolve(target)
			if err != nil {
				t.Errorf("%s: %v", p.Slug, err)
				continue
			}
			if to.Admin && !p.Admin {
				t.Errorf("%s links to %s, an admin page its readers may not open", p.Slug, m[1])
			}
		}
		if strings.Contains(p.Body, "](docs/") || strings.Contains(p.Body, ".md)") {
			t.Errorf("%s links to a page by file: link to /docs/<slug>", p.Slug)
		}
	}
}

// TestFrontendDocLinks checks that every link the app makes into the docs,
// DOC_LINKS in frontend/src/utils/docs.ts, points at a page and section
// that exist.
func TestFrontendDocLinks(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "..", "frontend", "src", "utils", "docs.ts"))
	if err != nil {
		t.Fatalf("reading the frontend's doc links: %v", err)
	}
	block := regexp.MustCompile(`(?s)export const DOC_LINKS = \{(.*?)\n\} as const`).FindSubmatch(source)
	if block == nil {
		t.Fatal("cannot find DOC_LINKS in frontend/src/utils/docs.ts")
	}
	entries := regexp.MustCompile(`(?m)^\s*(\w+): '([^']*)',?\s*$`).FindAllSubmatch(block[1], -1)
	if len(entries) == 0 {
		t.Fatal("DOC_LINKS lists no links")
	}
	lines := strings.Count(strings.TrimSpace(string(block[1])), "\n") + 1
	if len(entries) != lines {
		t.Errorf("DOC_LINKS has %d lines but %d plain '/docs/...' entries: keep each link a plain string on its own line", lines, len(entries))
	}
	for _, e := range entries {
		page, err := resolve(string(e[2]))
		if err != nil {
			t.Errorf("DOC_LINKS.%s: %v", e[1], err)
			continue
		}
		if page.Admin && string(e[1]) != "administration" {
			t.Logf("DOC_LINKS.%s points at an admin page: show it only to admins", e[1])
		}
	}
}

func TestPlainBody(t *testing.T) {
	p := Page{Body: "Intro {#not-a-heading}\n\n## One {#one}\n\ntext\n\n### Two {#two}\n"}
	want := "Intro {#not-a-heading}\n\n## One\n\ntext\n\n### Two\n"
	if got := p.PlainBody(); got != want {
		t.Errorf("PlainBody() = %q, want %q", got, want)
	}
}

func adminPage(t *testing.T) Page {
	t.Helper()
	for _, p := range pages {
		if p.Admin {
			return p
		}
	}
	t.Fatal("no admin page")
	return Page{}
}

func TestHandler(t *testing.T) {
	h := NewHandler()
	admin := adminPage(t)
	request := func(path, slug string, isAdmin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.SetPathValue("slug", slug)
		r = r.WithContext(context.WithValue(r.Context(), auth.UserContextKey, &auth.User{ID: 1, IsAdmin: isAdmin}))
		w := httptest.NewRecorder()
		if slug == "" {
			h.HandleList(w, r)
		} else {
			h.HandleGet(w, r)
		}
		return w
	}

	for _, isAdmin := range []bool{false, true} {
		w := request("/api/docs", "", isAdmin)
		var list struct {
			Pages []Page `json:"pages"`
		}
		if err := json.NewDecoder(w.Body).Decode(&list); err != nil {
			t.Fatal(err)
		}
		listed := false
		for _, p := range list.Pages {
			listed = listed || p.Slug == admin.Slug
		}
		if listed != isAdmin {
			t.Errorf("admin=%v: the admin page listed = %v", isAdmin, listed)
		}
	}

	if w := request("/api/docs/"+admin.Slug, admin.Slug, false); w.Code != http.StatusNotFound {
		t.Errorf("a non-admin reading the admin page got %d, want 404", w.Code)
	}
	w := request("/api/docs/"+admin.Slug, admin.Slug, true)
	var got struct {
		Title string `json:"title"`
		Body  string `json:"body"`
	}
	if err := json.NewDecoder(w.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if w.Code != http.StatusOK || got.Title != admin.Title || got.Body != admin.Body {
		t.Errorf("an admin reading the admin page got %d %q", w.Code, got.Title)
	}
	if w := request("/api/docs/nope", "nope", true); w.Code != http.StatusNotFound {
		t.Errorf("an unknown page got %d, want 404", w.Code)
	}
}

type fakeUsers map[int64]*auth.User

func (f fakeUsers) GetByID(_ context.Context, id int64) (*auth.User, error) { return f[id], nil }

func TestProvider(t *testing.T) {
	p := NewProvider(fakeUsers{1: {ID: 1}, 2: {ID: 2, IsAdmin: true}})
	admin := adminPage(t)
	ctx := context.Background()
	assistant := aitools.Scope{UserID: 1, WorkspaceID: 5, Kind: workspace.KindInteractive, ProjectID: 3}

	c := p.Contribute(ctx, assistant)
	if len(c.Tools) != 1 || c.Tools[0].Function.Name != readDocsTool {
		t.Fatalf("the assistant got tools %+v", c.Tools)
	}
	if !strings.Contains(c.Prompt, "<slug>"+pages[0].Slug+"</slug>") {
		t.Error("the prompt does not list the pages")
	}
	if strings.Contains(c.Prompt, "<slug>"+admin.Slug+"</slug>") {
		t.Error("a non-admin's prompt lists the admin page")
	}
	adminScope := assistant
	adminScope.UserID = 2
	if !strings.Contains(p.Contribute(ctx, adminScope).Prompt, "<slug>"+admin.Slug+"</slug>") {
		t.Error("an admin's prompt does not list the admin page")
	}

	for _, kind := range []workspace.Kind{workspace.KindTask, workspace.KindReview} {
		scope := assistant
		scope.Kind = kind
		if c := p.Contribute(ctx, scope); len(c.Tools) != 1 || !strings.Contains(c.Prompt, "<slug>"+pages[0].Slug+"</slug>") {
			t.Errorf("a %s sandbox did not get the docs", kind)
		}
		res, ok := p.ExecuteTool(ctx, aitools.ExecutionRequest{ToolName: readDocsTool, Scope: scope, Arguments: json.RawMessage(`{"page":"projects"}`)})
		if !ok || res.IsError {
			t.Errorf("a %s sandbox could not read a page: %+v", kind, res)
		}
	}

	noWorkspace := aitools.Scope{UserID: 1}
	if c := p.Contribute(ctx, noWorkspace); len(c.Tools) != 0 || c.Prompt != "" {
		t.Error("a run outside any workspace got the docs")
	}
	if res, ok := p.ExecuteTool(ctx, aitools.ExecutionRequest{ToolName: readDocsTool, Scope: noWorkspace, Arguments: json.RawMessage(`{"page":"projects"}`)}); !ok || !res.IsError {
		t.Errorf("a run outside any workspace read a page: %+v", res)
	}

	call := func(scope aitools.Scope, page string) (string, bool) {
		args, _ := json.Marshal(map[string]string{"page": page})
		res, ok := p.ExecuteTool(ctx, aitools.ExecutionRequest{ToolName: readDocsTool, Scope: scope, Arguments: args})
		if !ok {
			t.Fatal("the provider did not own read_devpad_docs")
		}
		return res.Content, res.IsError
	}
	first := pages[0]
	for _, name := range []string{first.Slug, "/docs/" + first.Slug} {
		got, isErr := call(assistant, name)
		if isErr || !strings.HasPrefix(got, "# "+first.Title) || strings.Contains(got, "{#") {
			t.Errorf("reading %q: error=%v content=%.80q", name, isErr, got)
		}
	}
	if got, isErr := call(assistant, "nope"); !isErr || !strings.Contains(got, first.Slug) {
		t.Errorf("an unknown page: %q", got)
	}
	if _, isErr := call(assistant, admin.Slug); !isErr {
		t.Error("a non-admin read the admin page")
	}
	if _, isErr := call(adminScope, admin.Slug); isErr {
		t.Error("an admin could not read the admin page")
	}
	if _, ok := p.ExecuteTool(ctx, aitools.ExecutionRequest{ToolName: "load_skill", Scope: assistant}); ok {
		t.Error("the provider claimed another tool")
	}
}
