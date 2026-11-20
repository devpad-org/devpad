package web

import (
	"bytes"
	"compress/gzip"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"
)

var (
	bigScript = strings.Repeat("console.log('devpad');\n", 200)
	indexHTML = "<!doctype html><title>devpad</title>" + strings.Repeat("<!-- padding -->", 100)
)

func testHandler(t *testing.T) *handler {
	t.Helper()
	h, err := newHandler(fstest.MapFS{
		"index.html":                 {Data: []byte(indexHTML)},
		"favicon.svg":                {Data: []byte("<svg/>")},
		"assets/IdeView-Ab12Cd34.js": {Data: []byte(bigScript)},
		"assets/font-Zz99Yy88.woff2": {Data: bytes.Repeat([]byte{0}, 4096)},
	})
	if err != nil {
		t.Fatalf("newHandler: %v", err)
	}
	return h
}

func get(t *testing.T, h http.Handler, target string, headers map[string]string) *http.Response {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec.Result()
}

func body(t *testing.T, res *http.Response) string {
	t.Helper()
	b, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}

func TestHandler_CachePolicy(t *testing.T) {
	h := testHandler(t)
	tests := []struct {
		name, path, cache string
		status            int
	}{
		{"hashed asset", "/assets/IdeView-Ab12Cd34.js", hashedAssetCaching, http.StatusOK},
		{"root", "/", revalidateCaching, http.StatusOK},
		{"SPA route", "/projects/3", revalidateCaching, http.StatusOK},
		{"unhashed file", "/favicon.svg", revalidateCaching, http.StatusOK},
		{"asset from another build", "/assets/IdeView-Old00000.js", "no-store", http.StatusNotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := get(t, h, tt.path, nil)
			if res.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.status)
			}
			if got := res.Header.Get("Cache-Control"); got != tt.cache {
				t.Errorf("Cache-Control = %q, want %q", got, tt.cache)
			}
		})
	}
}

func TestHandler_SPAFallbackServesIndex(t *testing.T) {
	res := get(t, testHandler(t), "/projects/3", nil)
	if got := body(t, res); got != indexHTML {
		t.Errorf("body = %q, want index.html", got)
	}
	if ct := res.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
}

func TestHandler_Gzip(t *testing.T) {
	h := testHandler(t)
	path := "/assets/IdeView-Ab12Cd34.js"

	plain := get(t, h, path, nil)
	if enc := plain.Header.Get("Content-Encoding"); enc != "" {
		t.Fatalf("Content-Encoding without Accept-Encoding = %q", enc)
	}
	if got := body(t, plain); got != bigScript {
		t.Fatalf("plain body differs")
	}

	gz := get(t, h, path, map[string]string{"Accept-Encoding": "br, gzip"})
	if enc := gz.Header.Get("Content-Encoding"); enc != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", enc)
	}
	if ct := gz.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/javascript") {
		t.Errorf("Content-Type = %q, want text/javascript", ct)
	}
	for _, res := range []*http.Response{plain, gz} {
		if v := res.Header.Get("Vary"); v != "Accept-Encoding" {
			t.Errorf("Vary = %q, want Accept-Encoding", v)
		}
	}
	if plain.Header.Get("ETag") == gz.Header.Get("ETag") {
		t.Errorf("gzip and plain share ETag %q", plain.Header.Get("ETag"))
	}
	zr, err := gzip.NewReader(gz.Body)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	unzipped, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("gunzip: %v", err)
	}
	if string(unzipped) != bigScript {
		t.Errorf("gunzipped body differs")
	}

	// Already compressed formats and tiny files go as they are.
	for _, p := range []string{"/assets/font-Zz99Yy88.woff2", "/favicon.svg"} {
		res := get(t, h, p, map[string]string{"Accept-Encoding": "gzip"})
		if enc := res.Header.Get("Content-Encoding"); enc != "" {
			t.Errorf("%s: Content-Encoding = %q, want none", p, enc)
		}
	}
}

func TestHandler_ETagRevalidation(t *testing.T) {
	h := testHandler(t)
	for _, enc := range []string{"", "gzip"} {
		first := get(t, h, "/", map[string]string{"Accept-Encoding": enc})
		etag := first.Header.Get("ETag")
		if etag == "" {
			t.Fatalf("encoding %q: no ETag", enc)
		}
		again := get(t, h, "/", map[string]string{"Accept-Encoding": enc, "If-None-Match": etag})
		if again.StatusCode != http.StatusNotModified {
			t.Errorf("encoding %q: status = %d, want 304", enc, again.StatusCode)
		}
	}
}

func TestAcceptsGzip(t *testing.T) {
	tests := map[string]bool{
		"":                     false,
		"gzip":                 true,
		"GZIP":                 true,
		"br, gzip, deflate":    true,
		"gzip;q=0":             false,
		"gzip; q=0.5":          true,
		"identity":             false,
		"*":                    true,
		"*;q=0":                false,
		"gzip;q=0, *":          false,
		"*, gzip;q=0":          false,
		"deflate, *;q=0, gzip": true,
		"x-gzip":               false,
	}
	for header, want := range tests {
		if got := acceptsGzip(header); got != want {
			t.Errorf("acceptsGzip(%q) = %v, want %v", header, got, want)
		}
	}
}

// The cache policy relies on Vite putting a content hash in the name of
// everything under assets/. A build that drops it would have browsers keep
// stale files for a year, so check the build that is embedded.
func TestEmbeddedAssetsAreHashed(t *testing.T) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatal(err)
	}
	hashed := regexp.MustCompile(`-[A-Za-z0-9_-]{8,}\.[a-z0-9]+$`)
	_ = fs.WalkDir(sub, "assets", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if !hashed.MatchString(p) {
			t.Errorf("%s has no content hash in its name, but %s is cached as immutable", p, hashedAssetDir)
		}
		return nil
	})
}
