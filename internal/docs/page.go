// Package docs serves devpad's user documentation, the Markdown pages of the
// top-level docs package, to the app and to the IDE assistant.
package docs

import (
	"bufio"
	"fmt"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"

	"github.com/devpad-org/devpad/docs"
)

// Page is one page of the documentation.
type Page struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	// Admin pages are only for admins: they cover running devpad.
	Admin    bool      `json:"admin"`
	Sections []Section `json:"sections"`
	// Body is the page's Markdown, its headings still carrying their
	// {#anchor}s.
	Body string `json:"-"`
}

// Section is a heading of a page, which links can point at.
type Section struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Level int    `json:"level"`
}

var (
	fileName = regexp.MustCompile(`^\d{2}-([a-z0-9]+(?:-[a-z0-9]+)*)\.md$`)
	heading  = regexp.MustCompile(`^(#{1,6})[ \t]+(.*?)[ \t]*$`)
	anchor   = regexp.MustCompile(`[ \t]+\{#([a-z0-9]+(?:-[a-z0-9]+)*)\}$`)
	fence    = regexp.MustCompile("^[ \t]{0,3}(```|~~~)")
)

// pages is every page, in order, parsed once from the embedded files: they
// cannot change while devpad runs, and the tests parse them on every build.
var pages = mustLoad(docs.FS)

func mustLoad(fsys fs.FS) []Page {
	p, err := load(fsys)
	if err != nil {
		panic(fmt.Sprintf("devpad's documentation: %v", err))
	}
	return p
}

// load parses every page in fsys, in the order of their file names.
func load(fsys fs.FS) ([]Page, error) {
	names, err := fs.Glob(fsys, "*.md")
	if err != nil {
		return nil, err
	}
	sort.Strings(names)
	var out []Page
	seen := map[string]bool{}
	for _, name := range names {
		m := fileName.FindStringSubmatch(path.Base(name))
		if m == nil {
			return nil, fmt.Errorf("%s: a page's file is named NN-slug.md", name)
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		page, err := parse(m[1], string(data))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		if seen[page.Slug] {
			return nil, fmt.Errorf("%s: another page is also %q", name, page.Slug)
		}
		seen[page.Slug] = true
		out = append(out, page)
	}
	return out, nil
}

// parse reads a page's front matter and finds its sections.
func parse(slug, data string) (Page, error) {
	page := Page{Slug: slug}
	data = strings.ReplaceAll(data, "\r\n", "\n")
	rest, ok := strings.CutPrefix(data, "---\n")
	if !ok {
		return page, fmt.Errorf("the page does not start with front matter")
	}
	front, body, ok := strings.Cut(rest, "\n---\n")
	if !ok {
		return page, fmt.Errorf("the front matter is not closed")
	}
	for _, line := range strings.Split(front, "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return page, fmt.Errorf("front matter line %q is not key: value", line)
		}
		value = strings.TrimSpace(value)
		switch strings.TrimSpace(key) {
		case "title":
			page.Title = value
		case "summary":
			page.Summary = value
		case "audience":
			if value != "admin" {
				return page, fmt.Errorf("audience is admin or left out, not %q", value)
			}
			page.Admin = true
		default:
			return page, fmt.Errorf("unknown front matter key %q", key)
		}
	}
	if page.Title == "" || page.Summary == "" {
		return page, fmt.Errorf("a page needs a title and a summary")
	}
	page.Body = strings.TrimLeft(body, "\n")

	ids := map[string]bool{}
	inFence := ""
	scanner := bufio.NewScanner(strings.NewReader(page.Body))
	for scanner.Scan() {
		line := scanner.Text()
		if m := fence.FindStringSubmatch(line); m != nil {
			switch inFence {
			case "":
				inFence = m[1]
			case m[1]:
				inFence = ""
			}
			continue
		}
		if inFence != "" {
			continue
		}
		m := heading.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		level := len(m[1])
		if level == 1 {
			return page, fmt.Errorf("heading %q: the title is the page's only h1", m[2])
		}
		a := anchor.FindStringSubmatch(m[2])
		if a == nil {
			return page, fmt.Errorf("heading %q has no {#anchor}", m[2])
		}
		if ids[a[1]] {
			return page, fmt.Errorf("two headings are #%s", a[1])
		}
		ids[a[1]] = true
		page.Sections = append(page.Sections, Section{
			ID:    a[1],
			Title: strings.TrimSpace(strings.TrimSuffix(m[2], a[0])),
			Level: level,
		})
	}
	if inFence != "" {
		return page, fmt.Errorf("a code block is not closed")
	}
	return page, nil
}

// Pages returns the pages a user may read, in order.
func Pages(admin bool) []Page {
	var out []Page
	for _, p := range pages {
		if !p.Admin || admin {
			out = append(out, p)
		}
	}
	return out
}

// Find returns the page with a slug, if the user may read it.
func Find(slug string, admin bool) (Page, bool) {
	for _, p := range pages {
		if p.Slug == slug && (!p.Admin || admin) {
			return p, true
		}
	}
	return Page{}, false
}

// headingAnchors matches the {#anchor} that ends a heading line.
var headingAnchors = regexp.MustCompile(`(?m)^(#{2,6}[ \t].*?)[ \t]+\{#[a-z0-9-]+\}[ \t]*$`)

// PlainBody is a page's Markdown without its headings' anchors, as the
// assistant reads it.
func (p Page) PlainBody() string {
	return headingAnchors.ReplaceAllString(p.Body, "$1")
}
