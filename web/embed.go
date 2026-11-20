package web

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed dist/*
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded frontend.
// All unmatched routes fall back to index.html for client-side routing.
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("failed to create sub filesystem: " + err.Error())
	}
	h, err := newHandler(sub)
	if err != nil {
		panic("failed to index embedded frontend: " + err.Error())
	}
	return h
}

// Caching is safe across deploys because of what Vite names its output.
// Everything under assets/ carries a hash of its content in its name, so a
// new build never reuses a name for other bytes: browsers keep those for a
// year without asking again. Every other response, index.html and the SPA
// fallback above all, must be checked with the server on every use (its
// ETag makes that a 304 when nothing changed), so a deploy reaches the next
// page load and that page asks for the new build's assets.
const (
	hashedAssetDir     = "assets/"
	hashedAssetCaching = "public, max-age=31536000, immutable"
	revalidateCaching  = "no-cache"
)

// Compressing an asset that is already compressed (fonts in woff2, images)
// gains nothing, so only these are gzipped.
var compressible = map[string]bool{
	".html": true, ".js": true, ".mjs": true, ".css": true, ".svg": true,
	".json": true, ".map": true, ".txt": true, ".xml": true, ".wasm": true,
	".ttf": true, ".otf": true,
}

type handler struct {
	assets map[string]*asset // by path in the build, without a leading slash
	index  *asset
}

// asset is one built file. Its content, ETag and gzipped copy are worked out
// on its first request and kept, since an embedded file never changes.
type asset struct {
	fsys fs.FS
	name string

	once        sync.Once
	err         error
	contentType string
	data        []byte
	etag        string
	gz          []byte // nil when gzip isn't worth it
	gzEtag      string
}

func newHandler(fsys fs.FS) (*handler, error) {
	h := &handler{assets: map[string]*asset{}}
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		h.assets[p] = &asset{fsys: fsys, name: p}
		return nil
	})
	if err != nil {
		return nil, err
	}
	h.index = h.assets["index.html"]
	return h, nil
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	a, ok := h.assets[name]
	if !ok {
		// A missing hashed asset is one a page from another build asked
		// for. Answering with index.html would hand it HTML for a script;
		// a plain 404 lets the page see the chunk is gone and reload.
		if strings.HasPrefix(name, hashedAssetDir) {
			w.Header().Set("Cache-Control", "no-store")
			http.NotFound(w, r)
			return
		}
		a = h.index
	}
	if a == nil {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err := a.load(); err != nil {
		http.Error(w, "failed to read frontend asset", http.StatusInternalServerError)
		return
	}

	header := w.Header()
	header.Set("Content-Type", a.contentType)
	if strings.HasPrefix(a.name, hashedAssetDir) {
		header.Set("Cache-Control", hashedAssetCaching)
	} else {
		header.Set("Cache-Control", revalidateCaching)
	}

	data, etag := a.data, a.etag
	if a.gz != nil {
		header.Add("Vary", "Accept-Encoding")
		if acceptsGzip(r.Header.Get("Accept-Encoding")) {
			data, etag = a.gz, a.gzEtag
			header.Set("Content-Encoding", "gzip")
		}
	}
	header.Set("ETag", etag)
	// ServeContent answers If-None-Match with a 304 and handles HEAD and
	// Range. Embedded files have no modification time, so the ETag is the
	// only validator.
	http.ServeContent(w, r, a.name, time.Time{}, bytes.NewReader(data))
}

func (a *asset) load() error {
	a.once.Do(func() {
		data, err := fs.ReadFile(a.fsys, a.name)
		if err != nil {
			a.err = err
			return
		}
		a.data = data
		ext := path.Ext(a.name)
		a.contentType = mime.TypeByExtension(ext)
		if a.contentType == "" {
			a.contentType = http.DetectContentType(data)
		}
		sum := sha256.Sum256(data)
		tag := hex.EncodeToString(sum[:12])
		a.etag = `"` + tag + `"`
		if compressible[ext] && len(data) >= 1024 {
			if gz := gzipBytes(data); len(gz) < len(data)*9/10 {
				a.gz = gz
				// Each encoding is its own representation, so it needs its
				// own strong ETag.
				a.gzEtag = `"` + tag + `-gzip"`
			}
		}
	})
	return a.err
}

func gzipBytes(data []byte) []byte {
	var buf bytes.Buffer
	zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if _, err := zw.Write(data); err != nil {
		return nil
	}
	if err := zw.Close(); err != nil {
		return nil
	}
	return buf.Bytes()
}

// acceptsGzip reports whether an Accept-Encoding header allows gzip: listed
// (or covered by "*") with a q-value above zero.
func acceptsGzip(header string) bool {
	accepted := false
	for _, part := range strings.Split(header, ",") {
		coding, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		coding = strings.ToLower(strings.TrimSpace(coding))
		if coding != "gzip" && coding != "*" {
			continue
		}
		q := 1.0
		for _, param := range strings.Split(params, ";") {
			key, value, ok := strings.Cut(strings.TrimSpace(param), "=")
			if ok && strings.EqualFold(strings.TrimSpace(key), "q") {
				if v, err := strconv.ParseFloat(strings.TrimSpace(value), 64); err == nil {
					q = v
				}
			}
		}
		// An explicit gzip entry overrides "*" whatever their order.
		if coding == "gzip" {
			return q > 0
		}
		accepted = q > 0
	}
	return accepted
}

// DevHandler serves from the filesystem during development.
func DevHandler(dir string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := filepath.Join(dir, r.URL.Path)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			http.ServeFile(w, r, filepath.Join(dir, "index.html"))
			return
		}
		http.FileServer(http.Dir(dir)).ServeHTTP(w, r)
	})
}
