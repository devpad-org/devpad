// Package docs holds devpad's user documentation: one Markdown page per file,
// served at /docs in the app and read by the IDE assistant. internal/docs
// parses the pages; this package only embeds them, since go:embed cannot
// reach outside its own directory.
//
// A file is named NN-slug.md, where NN orders the pages and slug is the
// page's URL. Each starts with front matter:
//
//	---
//	title: Branches and merge requests
//	summary: One sentence on what the page covers.
//	audience: admin   (optional: only admins see the page)
//	---
//
// The title is the page's only h1. Every other heading ends with an explicit
// anchor, as in "## Protected branches {#protected-branches}", so links into
// a page survive its headings being reworded. Link to another page as
// /docs/<slug> or /docs/<slug>#<anchor>.
package docs

import "embed"

// FS holds the pages.
//
//go:embed *.md
var FS embed.FS
