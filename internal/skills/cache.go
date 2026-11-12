package skills

import (
	"container/list"
	"slices"
	"sync"
)

const (
	// commitCacheBytes bounds what the commit cache holds; the least
	// recently used reads go first.
	commitCacheBytes = 16 << 20
	// maxCachedBytes is the largest file the commit cache keeps. SKILL.md
	// and AGENTS.md files are far smaller; a larger file a skill refers to
	// is read each time rather than crowd them out.
	maxCachedBytes = 1 << 20
	// cachedOverhead is roughly what an entry costs besides its contents.
	cachedOverhead = 128
)

// commitKey is one read of a commit of a project's canonical repo: a
// directory's listing, or a file. The user is part of the key so that one
// user's reads are never served to another, whatever their scopes say.
type commitKey struct {
	userID    int64
	projectID int64
	commit    string
	path      string
	dir       bool
}

// cached is what a read found: a file's contents, a directory's entries, or
// that there is no such file or directory.
type cached struct {
	key     commitKey
	data    []byte
	entries []entry
	missing bool
	size    int
}

// commitCache keeps what has been read from commits of canonical repos,
// least recently used first out. A commit never changes, so nothing it
// keeps goes stale, and a read that failed for any reason other than there
// being nothing there is never kept.
type commitCache struct {
	mu       sync.Mutex
	capacity int
	size     int
	// order holds the entries, most recently used first.
	order *list.List
	items map[commitKey]*list.Element
}

func newCommitCache(capacity int) *commitCache {
	return &commitCache{capacity: capacity, order: list.New(), items: map[commitKey]*list.Element{}}
}

// get returns a copy of what a read found, so a caller that sorts or
// changes it never changes what another is given.
func (c *commitCache) get(k commitKey) (cached, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.items[k]
	if !ok {
		return cached{}, false
	}
	c.order.MoveToFront(el)
	v := *el.Value.(*cached)
	v.data, v.entries = slices.Clone(v.data), slices.Clone(v.entries)
	return v, true
}

// put keeps what a read found, unless it is too large to keep.
func (c *commitCache) put(v cached) {
	v.size = cachedOverhead + len(v.key.commit) + len(v.key.path) + len(v.data)
	for _, e := range v.entries {
		v.size += cachedOverhead/4 + len(e.name)
	}
	if v.size > maxCachedBytes || v.size > c.capacity {
		return
	}
	v.data, v.entries = slices.Clone(v.data), slices.Clone(v.entries)

	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.items[v.key]; ok {
		c.size -= el.Value.(*cached).size
		c.order.Remove(el)
	}
	c.items[v.key] = c.order.PushFront(&v)
	c.size += v.size
	for c.size > c.capacity {
		oldest := c.order.Back()
		old := oldest.Value.(*cached)
		c.order.Remove(oldest)
		delete(c.items, old.key)
		c.size -= old.size
	}
}
