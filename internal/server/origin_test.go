package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/devpad-org/devpad/internal/preview"
)

const testAppOrigin = "https://devpad.example.com"

func guardedApp(t *testing.T) (http.Handler, *bool) {
	t.Helper()
	reached := new(bool)
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*reached = true
		w.WriteHeader(http.StatusOK)
	})
	return sameOriginGuard(app, []string{testAppOrigin}), reached
}

type guardRequest struct {
	method      string
	path        string
	host        string
	secFetch    string
	origin      string
	contentType string
	body        string
	websocket   bool
	upgrade     string
}

func (g guardRequest) build() *http.Request {
	method := g.method
	if method == "" {
		method = http.MethodPost
	}
	path := g.path
	if path == "" {
		path = "/api/projects/1/merge-requests/2/merge"
	}
	r := httptest.NewRequest(method, path, strings.NewReader(g.body))
	r.Host = "devpad.example.com"
	if g.host != "" {
		r.Host = g.host
	}
	if g.secFetch != "" {
		r.Header.Set("Sec-Fetch-Site", g.secFetch)
	}
	if g.origin != "" {
		r.Header.Set("Origin", g.origin)
	}
	if g.contentType != "" {
		r.Header.Set("Content-Type", g.contentType)
	}
	if g.websocket {
		r.Header.Set("Connection", "keep-alive, Upgrade")
		r.Header.Set("Upgrade", "websocket")
		if g.upgrade != "" {
			r.Header.Set("Upgrade", g.upgrade)
		}
	}
	return r
}

func TestSameOriginGuard(t *testing.T) {
	const json = "application/json"
	const previewOrigin = "https://3-8080.preview.example.com"
	tests := []struct {
		name string
		req  guardRequest
		want int
	}{
		{"same-origin fetch", guardRequest{secFetch: "same-origin", origin: testAppOrigin, contentType: json, body: "{}"}, http.StatusOK},
		{"same-origin without body", guardRequest{secFetch: "same-origin"}, http.StatusOK},
		{"user navigation", guardRequest{secFetch: "none"}, http.StatusOK},
		{"preview subdomain is same-site", guardRequest{secFetch: "same-site", origin: previewOrigin, contentType: json, body: "{}"}, http.StatusForbidden},
		{"preview form post", guardRequest{secFetch: "same-site", origin: previewOrigin, contentType: "application/x-www-form-urlencoded", body: "a=b"}, http.StatusForbidden},
		{"cross-site", guardRequest{secFetch: "cross-site", origin: "https://evil.test", contentType: json, body: "{}"}, http.StatusForbidden},
		{"same-origin header wins over a stale origin check", guardRequest{secFetch: "same-origin", origin: "https://proxy.internal", contentType: json, body: "{}"}, http.StatusOK},
		{"origin fallback: same host", guardRequest{origin: testAppOrigin, contentType: json, body: "{}"}, http.StatusOK},
		{"origin fallback: preview host", guardRequest{origin: previewOrigin, contentType: json, body: "{}"}, http.StatusForbidden},
		{"origin fallback: other port", guardRequest{origin: "https://devpad.example.com:8443", contentType: json, body: "{}"}, http.StatusForbidden},
		{"origin fallback: configured origin behind a proxy", guardRequest{host: "10.0.0.5:8080", origin: testAppOrigin, contentType: json, body: "{}"}, http.StatusOK},
		{"origin fallback: case-insensitive", guardRequest{origin: "https://DevPad.Example.com", contentType: json, body: "{}"}, http.StatusOK},
		{"origin fallback: opaque origin", guardRequest{origin: "null", contentType: json, body: "{}"}, http.StatusForbidden},
		{"origin fallback: loopback dev server", guardRequest{host: "localhost:8080", origin: "http://localhost:5173", contentType: json, body: "{}"}, http.StatusOK},
		{"origin fallback: loopback to another host", guardRequest{host: "localhost:8080", origin: "http://127.0.0.2:5173", contentType: json, body: "{}"}, http.StatusForbidden},
		{"non-browser client without either header", guardRequest{contentType: json, body: "{}"}, http.StatusOK},
		{"reads are not checked", guardRequest{method: http.MethodGet, path: "/api/projects", secFetch: "same-site", origin: previewOrigin}, http.StatusOK},
		{"delete from preview", guardRequest{method: http.MethodDelete, path: "/api/projects/1", secFetch: "same-site", origin: previewOrigin}, http.StatusForbidden},
		{"websocket from app", guardRequest{method: http.MethodGet, path: "/api/workspaces/1/terminal", secFetch: "same-origin", origin: testAppOrigin, websocket: true}, http.StatusOK},
		{"websocket from preview", guardRequest{method: http.MethodGet, path: "/api/workspaces/1/terminal", secFetch: "same-site", origin: previewOrigin, websocket: true}, http.StatusForbidden},
		{"websocket listed among upgrade tokens", guardRequest{method: http.MethodGet, path: "/api/workspaces/1/terminal", origin: "https://evil.test", websocket: true, upgrade: "foo, WebSocket"}, http.StatusForbidden},
		{"websocket with foreign origin only", guardRequest{method: http.MethodGet, path: "/api/workspaces/1/terminal", origin: "https://evil.test", websocket: true}, http.StatusForbidden},
		{"json with charset", guardRequest{secFetch: "same-origin", contentType: "application/json; charset=utf-8", body: "{}"}, http.StatusOK},
		{"text/plain body", guardRequest{secFetch: "same-origin", contentType: "text/plain", body: "{}"}, http.StatusUnsupportedMediaType},
		{"form body", guardRequest{secFetch: "same-origin", contentType: "application/x-www-form-urlencoded", body: "a=b"}, http.StatusUnsupportedMediaType},
		{"body without content type", guardRequest{secFetch: "same-origin", body: "{}"}, http.StatusUnsupportedMediaType},
		{"multipart outside the upload", guardRequest{secFetch: "same-origin", contentType: "multipart/form-data; boundary=x", body: "--x--"}, http.StatusUnsupportedMediaType},
		{"file upload", guardRequest{path: "/api/workspaces/1/file/upload", secFetch: "same-origin", contentType: "multipart/form-data; boundary=x", body: "--x--"}, http.StatusOK},
		{"file upload as json", guardRequest{path: "/api/workspaces/1/file/upload", secFetch: "same-origin", contentType: json, body: "{}"}, http.StatusUnsupportedMediaType},
		{"file upload from preview", guardRequest{path: "/api/workspaces/1/file/upload", secFetch: "same-site", origin: previewOrigin, contentType: "multipart/form-data; boundary=x", body: "--x--"}, http.StatusForbidden},
		{"image attachment upload", guardRequest{path: "/api/ai/attachments", secFetch: "same-origin", contentType: "multipart/form-data; boundary=x", body: "--x--"}, http.StatusOK},
		{"image attachment upload from preview", guardRequest{path: "/api/ai/attachments", secFetch: "same-site", origin: previewOrigin, contentType: "multipart/form-data; boundary=x", body: "--x--"}, http.StatusForbidden},
		{"multipart under an attachment", guardRequest{path: "/api/ai/attachments/1", secFetch: "same-origin", contentType: "multipart/form-data; boundary=x", body: "--x--"}, http.StatusUnsupportedMediaType},
		{"content type only checked under /api", guardRequest{path: "/login", secFetch: "same-origin", contentType: "text/plain", body: "x"}, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, reached := guardedApp(t)
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, tt.req.build())
			if rec.Code != tt.want {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tt.want, rec.Body.String())
			}
			if *reached != (tt.want == http.StatusOK) {
				t.Fatalf("app reached = %v, want %v", *reached, tt.want == http.StatusOK)
			}
		})
	}
}

func TestHostRouterKeepsPreviewHostsOffTheApp(t *testing.T) {
	tests := []struct {
		host       string
		wantApp    bool
		wantStatus int
	}{
		{"devpad.example.com", true, http.StatusOK},
		{"devpad.example.com:443", true, http.StatusOK},
		{"3-8080.preview.example.com", false, http.StatusUnauthorized},
		{"3-8080.preview.example.com:443", false, http.StatusUnauthorized},
		{"3-8080.PREVIEW.Example.com", false, http.StatusBadRequest},
		{"3-8080.preview.example.com.", false, http.StatusBadRequest},
		{"preview.example.com", false, http.StatusBadRequest},
		{"anything.at.all.preview.example.com", false, http.StatusBadRequest},
		{"notpreview.example.com", true, http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			reached := false
			app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				reached = true
				w.WriteHeader(http.StatusOK)
			})
			previews := preview.NewHandler(nil, "preview.example.com", 443)
			h := hostRouter(app, http.HandlerFunc(previews.HandleProxy), "preview.example.com")

			r := httptest.NewRequest(http.MethodGet, "/api/auth/me", nil)
			r.Host = tt.host
			r.AddCookie(&http.Cookie{Name: "devpad_session", Value: "token"})
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, r)

			if reached != tt.wantApp {
				t.Fatalf("app reached = %v, want %v", reached, tt.wantApp)
			}
			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantStatus)
			}
		})
	}
}

func TestAppOrigins(t *testing.T) {
	if got := appOrigins(Config{Domain: "devpad.example.com", Port: 443}); len(got) != 1 || got[0] != "https://devpad.example.com" {
		t.Fatalf("default port: %v", got)
	}
	if got := appOrigins(Config{Domain: "devpad.example.com", Port: 8443}); len(got) != 1 || got[0] != "https://devpad.example.com:8443" {
		t.Fatalf("custom port: %v", got)
	}
	got := appOrigins(Config{Port: 8080})
	if len(got) != 2 || got[0] != "http://localhost:8080" || got[1] != "http://127.0.0.1:8080" {
		t.Fatalf("development: %v", got)
	}
}

func TestBuildHandlerWiring(t *testing.T) {
	cfg := Config{Domain: "devpad.example.com", PreviewDomain: "preview.example.com", Port: 443}
	reached := false
	app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	})
	previews := preview.NewHandler(nil, cfg.PreviewDomain, cfg.Port)
	h := buildHandler(app, http.HandlerFunc(previews.HandleProxy), cfg)

	serve := func(r *http.Request) *httptest.ResponseRecorder {
		reached = false
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}
	assertFramingDenied := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if got := rec.Header().Get("Content-Security-Policy"); got != "frame-ancestors 'none'" {
			t.Fatalf("Content-Security-Policy = %q", got)
		}
		if got := rec.Header().Get("X-Frame-Options"); got != "DENY" {
			t.Fatalf("X-Frame-Options = %q", got)
		}
	}

	t.Run("app pages cannot be framed", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/projects/1", nil)
		r.Host = "devpad.example.com"
		rec := serve(r)
		if !reached || rec.Code != http.StatusOK {
			t.Fatalf("reached=%v status=%d", reached, rec.Code)
		}
		assertFramingDenied(t, rec)
	})

	t.Run("refusals carry frame headers too", func(t *testing.T) {
		r := guardRequest{secFetch: "same-site", origin: "https://3-8080.preview.example.com", contentType: "application/json", body: "{}"}.build()
		rec := serve(r)
		if reached || rec.Code != http.StatusForbidden {
			t.Fatalf("same-site POST: reached=%v status=%d, want refused", reached, rec.Code)
		}
		assertFramingDenied(t, rec)
	})

	t.Run("previews are not given app frame headers", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Host = "3-8080.preview.example.com"
		rec := serve(r)
		if reached {
			t.Fatal("preview host reached the app")
		}
		if rec.Header().Get("Content-Security-Policy") != "" || rec.Header().Get("X-Frame-Options") != "" {
			t.Fatalf("preview response got app frame headers: %v", rec.Header())
		}
	})

	t.Run("body limit applies", func(t *testing.T) {
		body := strings.Repeat("x", 2<<20)
		app := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if _, err := io.ReadAll(r.Body); err == nil {
				t.Error("expected the body limit to stop the read")
			}
		})
		h := buildHandler(app, http.HandlerFunc(previews.HandleProxy), cfg)
		r := guardRequest{secFetch: "same-origin", contentType: "application/json", body: body}.build()
		h.ServeHTTP(httptest.NewRecorder(), r)
	})
}

func TestValidateDomains(t *testing.T) {
	tests := []struct {
		domain, preview string
		wantErr         bool
	}{
		{"devpad.example.com", "preview.example.com", false},
		{"devpad.example.com", "devpad-preview.net", false},
		{"devpad.example.com", "", false},
		{"", "preview.example.com", false},
		{"devpad.example.com", "devpad.example.com", true},
		{"devpad.example.com", "Example.com.", true},
		{"app.preview.example.com", "preview.example.com", true},
		{"notpreview.example.com", "preview.example.com", false},
	}
	for _, tt := range tests {
		err := validateDomains(Config{Domain: tt.domain, PreviewDomain: tt.preview})
		if (err != nil) != tt.wantErr {
			t.Errorf("validateDomains(%q, %q) = %v, wantErr %v", tt.domain, tt.preview, err, tt.wantErr)
		}
	}
}

func TestSecureCookies(t *testing.T) {
	if secureCookies(Config{}) {
		t.Error("plain HTTP development must not use Secure cookies")
	}
	if !secureCookies(Config{Domain: "devpad.example.com"}) {
		t.Error("a domain means HTTPS")
	}
	if !secureCookies(Config{PreviewDomain: "preview.example.com"}) {
		t.Error("a preview domain forces Secure cookies")
	}
}
