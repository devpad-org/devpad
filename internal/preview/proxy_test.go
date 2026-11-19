package preview

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// agentAt is the real service with previews proxied to a test server.
type agentAt struct {
	Service
	addr string
}

func (a agentAt) ResolveContainerAddr(context.Context, int64) (string, string, error) {
	return a.addr, "agent-token", nil
}

// handlerTransport sends a client's requests straight to the handler, so a
// cookie jar sees the preview hosts.
type handlerTransport struct{ handler *Handler }

func (t handlerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	r.Host = req.URL.Host
	w := httptest.NewRecorder()
	t.handler.HandleProxy(w, r)
	return w.Result(), nil
}

// proxyRig is a handler in front of a fake agent, which records what reaches it.
type proxyRig struct {
	handler  *Handler
	service  Service
	agent    *httptest.Server
	addr     string
	requests []*http.Request
	respond  http.HandlerFunc
}

func newProxyRig(t *testing.T) *proxyRig {
	t.Helper()
	svc, db := setupTestService(t)
	createRunningWorkspace(t, db, 1, "first")
	createRunningWorkspace(t, db, 1, "second")
	rig := &proxyRig{service: svc}
	rig.agent = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rig.requests = append(rig.requests, r)
		if rig.respond != nil {
			rig.respond(w, r)
			return
		}
		io.WriteString(w, r.URL.Path)
	}))
	t.Cleanup(rig.agent.Close)
	rig.addr = strings.TrimPrefix(rig.agent.URL, "http://")
	rig.handler = NewHandler(agentAt{Service: svc, addr: rig.addr}, "preview.example.com", 443)
	return rig
}

// cookieFor returns a valid sign-in for a preview.
func (rig *proxyRig) cookieFor(workspaceID int64, port int) *http.Cookie {
	return &http.Cookie{Name: PreviewCookieName, Value: rig.handler.signCookie(previewClaims{
		userID: 1, workspaceID: workspaceID, port: port, expiresAt: time.Now().Add(time.Hour),
	})}
}

func (rig *proxyRig) serve(r *http.Request) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	rig.handler.HandleProxy(w, r)
	return w
}

func TestPreviewsKeepSeparateSignIns(t *testing.T) {
	rig := newProxyRig(t)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar, Transport: handlerTransport{rig.handler}}

	get := func(url string) (int, string) {
		t.Helper()
		resp, err := client.Get(url)
		if err != nil {
			t.Fatalf("GET %s: %v", url, err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}
	open := func(port int) {
		t.Helper()
		link, err := rig.service.GenerateURL(context.Background(), 1, 1, port, "preview.example.com", 443)
		if err != nil {
			t.Fatal(err)
		}
		if status, body := get(link); status != http.StatusOK {
			t.Fatalf("opening port %d: %d %s", port, status, body)
		}
	}

	open(3000)
	open(8000)
	for port, want := range map[string]string{"3000": "/proxy/3000/page", "8000": "/proxy/8000/page"} {
		status, body := get("https://1-" + port + ".preview.example.com/page")
		if status != http.StatusOK || body != want {
			t.Errorf("port %s after opening both: %d %q, want 200 %q", port, status, body, want)
		}
	}
	if status, _ := get("https://1-9000.preview.example.com/"); status != http.StatusUnauthorized {
		t.Errorf("a preview never opened: status %d, want 401", status)
	}
}

func TestPreviewCookieStaysOnItsHost(t *testing.T) {
	rig := newProxyRig(t)
	link, err := rig.service.GenerateURL(context.Background(), 1, 1, 3000, "preview.example.com", 443)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("GET", link, nil)
	cookies := rig.serve(r).Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies = %v, want one", cookies)
	}
	c := cookies[0]
	if c.Name != PreviewCookieName || c.Domain != "" || c.Path != "/" || !c.Secure || !c.HttpOnly {
		t.Fatalf("cookie = %+v, want a host-only __Host- cookie", c)
	}
}

func TestProxyRefusesPagesOfOtherWorkspaces(t *testing.T) {
	rig := newProxyRig(t)
	for _, method := range []string{"GET", "POST", "OPTIONS"} {
		r := httptest.NewRequest(method, "https://1-8000.preview.example.com/api", nil)
		r.Header.Set("Origin", "https://2-3000.preview.example.com")
		r.Header.Set("Access-Control-Request-Method", "POST")
		r.AddCookie(rig.cookieFor(1, 8000))
		if w := rig.serve(r); w.Code != http.StatusForbidden {
			t.Errorf("%s: status %d, want 403", method, w.Code)
		}
	}
	if len(rig.requests) != 0 {
		t.Fatalf("%d requests reached the workspace", len(rig.requests))
	}
}

// pageRequest is a request as a browser sends it for a page.
type pageRequest struct {
	name, method, origin, site, mode, dest, referer string
}

func (p pageRequest) build() *http.Request {
	r := httptest.NewRequest(p.method, "https://1-8000.preview.example.com/api", nil)
	for name, value := range map[string]string{
		"Origin":         p.origin,
		"Sec-Fetch-Site": p.site,
		"Sec-Fetch-Mode": p.mode,
		"Sec-Fetch-Dest": p.dest,
		"Referer":        p.referer,
	} {
		if value != "" {
			r.Header.Set(name, value)
		}
	}
	return r
}

const (
	siblingPage = "https://1-5173.preview.example.com/"
	foreignPage = "https://2-3000.preview.example.com/"
)

func TestProxyRefusesPagesThatDoNotNameTheirWorkspace(t *testing.T) {
	for _, p := range []pageRequest{
		{"fetch redirected across origins", "POST", "null", "same-site", "cors", "empty", foreignPage},
		{"image", "GET", "", "same-site", "no-cors", "image", foreignPage},
		{"script", "GET", "", "same-site", "no-cors", "script", foreignPage},
		{"frame", "GET", "", "same-site", "navigate", "iframe", foreignPage},
		{"image with its referrer hidden", "GET", "", "same-site", "no-cors", "image", ""},
		{"form with its referrer hidden", "POST", "null", "same-site", "navigate", "document", ""},
		{"image redirected through another site", "GET", "", "cross-site", "no-cors", "image", foreignPage},
		{"null origin without fetch metadata", "POST", "null", "", "", "", foreignPage},
		{"the app's own page", "GET", "https://devpad.example.com", "same-site", "cors", "empty", "https://devpad.example.com/"},
	} {
		t.Run(p.name, func(t *testing.T) {
			rig := newProxyRig(t)
			r := p.build()
			r.AddCookie(rig.cookieFor(1, 8000))
			if w := rig.serve(r); w.Code != http.StatusForbidden {
				t.Errorf("status %d, want 403", w.Code)
			}
			if len(rig.requests) != 0 {
				t.Error("the request reached the workspace")
			}
		})
	}
}

func TestProxyAllowsThePagesItShould(t *testing.T) {
	for _, p := range []pageRequest{
		{"the preview's own image", "GET", "", "same-origin", "no-cors", "image", "https://1-8000.preview.example.com/"},
		{"a typed address", "GET", "", "none", "navigate", "document", ""},
		{"a link from another workspace's preview", "GET", "", "same-site", "navigate", "document", foreignPage},
		{"a link from another site", "GET", "", "cross-site", "navigate", "document", "https://example.org/"},
		{"an image on a sibling preview", "GET", "", "same-site", "no-cors", "image", siblingPage},
		{"a sibling's fetch redirected across origins", "POST", "null", "same-site", "cors", "empty", siblingPage},
		{"a client without fetch metadata", "GET", "", "", "", "", ""},
	} {
		t.Run(p.name, func(t *testing.T) {
			rig := newProxyRig(t)
			r := p.build()
			r.AddCookie(rig.cookieFor(1, 8000))
			if w := rig.serve(r); w.Code != http.StatusOK {
				t.Errorf("status %d, want 200", w.Code)
			}
		})
	}
}

func TestProxyErrorsAreReadableByTheWorkspacesPreviews(t *testing.T) {
	const origin = "https://1-5173.preview.example.com"
	allowed := func(w *httptest.ResponseRecorder) string { return w.Header().Get("Access-Control-Allow-Origin") }
	rig := newProxyRig(t)

	unsigned := httptest.NewRequest("POST", "https://1-8000.preview.example.com/api", nil)
	unsigned.Header.Set("Origin", origin)
	w := rig.serve(unsigned)
	if w.Code != http.StatusUnauthorized || allowed(w) != origin || w.Header().Get("Access-Control-Allow-Credentials") != "true" {
		t.Errorf("no sign-in: status %d, allowed %q", w.Code, allowed(w))
	}

	elsewhere := httptest.NewRequest("POST", "https://1-8000.preview.example.com/api", nil)
	elsewhere.Header.Set("Origin", "https://example.org")
	if w := rig.serve(elsewhere); w.Code != http.StatusUnauthorized || allowed(w) != "" {
		t.Errorf("another site: status %d, allowed %q", w.Code, allowed(w))
	}

	signedIn := httptest.NewRequest("POST", "https://1-8000.preview.example.com/api", nil)
	signedIn.Header.Set("Origin", origin)
	signedIn.AddCookie(rig.cookieFor(1, 8000))
	if w := rig.serve(signedIn); w.Code != http.StatusOK || allowed(w) != "" {
		t.Errorf("the app's response: status %d, allowed %q, want it left to the app", w.Code, allowed(w))
	}

	rig.agent.Close()
	down := httptest.NewRequest("POST", "https://1-8000.preview.example.com/api", nil)
	down.Header.Set("Origin", origin)
	down.AddCookie(rig.cookieFor(1, 8000))
	if w := rig.serve(down); w.Code != http.StatusBadGateway || allowed(w) != origin {
		t.Errorf("agent down: status %d, allowed %q", w.Code, allowed(w))
	}
}

func TestProxyLetsAWorkspacesPreviewsCallEachOther(t *testing.T) {
	rig := newProxyRig(t)
	const origin = "https://1-5173.preview.example.com"

	preflight := httptest.NewRequest("OPTIONS", "https://1-8000.preview.example.com/api", nil)
	preflight.Header.Set("Origin", origin)
	preflight.Header.Set("Access-Control-Request-Method", "POST")
	preflight.Header.Set("Access-Control-Request-Headers", "content-type")
	w := rig.serve(preflight)
	if w.Code != http.StatusNoContent {
		t.Fatalf("preflight: status %d, want 204", w.Code)
	}
	for name, want := range map[string]string{
		"Access-Control-Allow-Origin":      origin,
		"Access-Control-Allow-Credentials": "true",
		"Access-Control-Allow-Methods":     "POST",
		"Access-Control-Allow-Headers":     "content-type",
	} {
		if got := w.Header().Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if len(rig.requests) != 0 {
		t.Fatal("the unauthenticated preflight reached the workspace")
	}

	call := httptest.NewRequest("POST", "https://1-8000.preview.example.com/api", nil)
	call.Header.Set("Origin", origin)
	call.AddCookie(rig.cookieFor(1, 8000))
	if w := rig.serve(call); w.Code != http.StatusOK {
		t.Fatalf("call: status %d, want 200", w.Code)
	}

	unsigned := httptest.NewRequest("POST", "https://1-8000.preview.example.com/api", nil)
	unsigned.Header.Set("Origin", origin)
	if w := rig.serve(unsigned); w.Code != http.StatusUnauthorized {
		t.Fatalf("call without its sign-in: status %d, want 401", w.Code)
	}
}

func TestProxyAnswersNoPreflightFromElsewhere(t *testing.T) {
	rig := newProxyRig(t)
	for origin, want := range map[string]int{
		"https://evil.example.org":          http.StatusUnauthorized,
		"http://1-5173.preview.example.com": http.StatusUnauthorized,
		"null":                              http.StatusForbidden,
	} {
		r := httptest.NewRequest("OPTIONS", "https://1-8000.preview.example.com/api", nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Access-Control-Request-Method", "POST")
		w := rig.serve(r)
		if w.Code != want || w.Header().Get("Access-Control-Allow-Origin") != "" {
			t.Errorf("origin %q: status %d, allowed %q", origin, w.Code, w.Header().Get("Access-Control-Allow-Origin"))
		}
	}
}

func TestProxyTellsTheAppItsPublicAddress(t *testing.T) {
	rig := newProxyRig(t)
	r := httptest.NewRequest("GET", "https://1-8000.preview.example.com/api", nil)
	r.Header.Set("X-Forwarded-Host", "forged.example.org")
	r.Header.Set("X-Forwarded-Proto", "http")
	r.AddCookie(rig.cookieFor(1, 8000))
	r.AddCookie(&http.Cookie{Name: legacyPreviewCookieName, Value: "from-before-the-upgrade"})
	r.AddCookie(&http.Cookie{Name: "app", Value: "kept"})
	if w := rig.serve(r); w.Code != http.StatusOK {
		t.Fatalf("status %d", w.Code)
	}
	got := rig.requests[0]
	if host := got.Header.Get("X-Forwarded-Host"); host != "1-8000.preview.example.com" {
		t.Errorf("X-Forwarded-Host = %q", host)
	}
	if proto := got.Header.Get("X-Forwarded-Proto"); proto != "https" {
		t.Errorf("X-Forwarded-Proto = %q", proto)
	}
	if _, err := got.Cookie(PreviewCookieName); err == nil {
		t.Error("the preview cookie reached the workspace")
	}
	if _, err := got.Cookie(legacyPreviewCookieName); err == nil {
		t.Error("the old preview cookie reached the workspace")
	}
	if c, err := got.Cookie("app"); err != nil || c.Value != "kept" {
		t.Errorf("the app's cookie = %v, %v", c, err)
	}
}

func TestProxyRewritesRedirectsToTheAgentAddress(t *testing.T) {
	tests := []struct {
		name     string
		location func(addr string) string
		want     string
	}{
		{"agent address", func(addr string) string { return "http://" + addr + "/api/items/?page=2" }, "https://1-8000.preview.example.com/api/items/?page=2"},
		{"relative", func(string) string { return "/api/items/" }, "/api/items/"},
		{"elsewhere", func(string) string { return "https://accounts.example.org/login" }, "https://accounts.example.org/login"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rig := newProxyRig(t)
			rig.respond = func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", tt.location(rig.addr))
				w.WriteHeader(http.StatusTemporaryRedirect)
			}
			r := httptest.NewRequest("GET", "https://1-8000.preview.example.com/api/items", nil)
			r.AddCookie(rig.cookieFor(1, 8000))
			if got := rig.serve(r).Header().Get("Location"); got != tt.want {
				t.Fatalf("Location = %q, want %q", got, tt.want)
			}
		})
	}
}
