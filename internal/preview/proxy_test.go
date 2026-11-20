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
	rig.handler = NewHandler(agentAt{Service: svc, addr: rig.addr}, testConfig)
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
		link, err := rig.service.GenerateURL(context.Background(), 1, 1, port)
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
	link, err := rig.service.GenerateURL(context.Background(), 1, 1, 3000)
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

const (
	ownOrigin     = "https://1-8000.preview.example.com"
	siblingOrigin = "https://1-5173.preview.example.com"
	foreignOrigin = "https://2-3000.preview.example.com"
)

// pageRequest is a request as a browser sends it for a page.
type pageRequest struct {
	name, method, origin, site, mode, dest string
}

func (p pageRequest) build() *http.Request {
	r := httptest.NewRequest(p.method, ownOrigin+"/api", nil)
	for name, value := range map[string]string{
		"Origin":         p.origin,
		"Sec-Fetch-Site": p.site,
		"Sec-Fetch-Mode": p.mode,
		"Sec-Fetch-Dest": p.dest,
	} {
		if value != "" {
			r.Header.Set(name, value)
		}
	}
	return r
}

func TestProxyRefusesOtherPages(t *testing.T) {
	for _, p := range []pageRequest{
		{"another workspace's fetch", "POST", foreignOrigin, "same-site", "cors", "empty"},
		{"another port's fetch", "POST", siblingOrigin, "same-site", "cors", "empty"},
		{"another port's preflight", "OPTIONS", siblingOrigin, "same-site", "cors", "empty"},
		{"another port's no-cors POST", "POST", siblingOrigin, "same-site", "no-cors", "empty"},
		{"fetch redirected across origins", "POST", "null", "same-site", "cors", "empty"},
		{"image", "GET", "", "same-site", "no-cors", "image"},
		{"script", "GET", "", "same-site", "no-cors", "script"},
		{"frame", "GET", "", "same-site", "navigate", "iframe"},
		{"form", "POST", "null", "same-site", "navigate", "document"},
		{"image redirected through another site", "GET", "", "cross-site", "no-cors", "image"},
		{"the app's own page", "GET", "https://devpad.example.com", "same-site", "cors", "empty"},
		{"another workspace without fetch metadata", "POST", foreignOrigin, "", "", ""},
		{"another port without fetch metadata", "OPTIONS", siblingOrigin, "", "", ""},
		{"null origin without fetch metadata", "POST", "null", "", "", ""},
	} {
		t.Run(p.name, func(t *testing.T) {
			rig := newProxyRig(t)
			r := p.build()
			r.AddCookie(rig.cookieFor(1, 8000))
			w := rig.serve(r)
			if w.Code != http.StatusForbidden {
				t.Errorf("status %d, want 403", w.Code)
			}
			if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("Access-Control-Allow-Origin = %q", got)
			}
			if len(rig.requests) != 0 {
				t.Error("the request reached the workspace")
			}
		})
	}
}

func TestProxyAllowsThePagesItShould(t *testing.T) {
	for _, p := range []pageRequest{
		{"the preview's own image", "GET", "", "same-origin", "no-cors", "image"},
		{"the preview's own fetch", "POST", ownOrigin, "same-origin", "cors", "empty"},
		// A page with Referrer-Policy: no-referrer sends this for its own forms.
		{"the preview's own form with its referrer hidden", "POST", "null", "same-origin", "navigate", "document"},
		{"a typed address", "GET", "", "none", "navigate", "document"},
		{"a link from another preview", "GET", "", "same-site", "navigate", "document"},
		{"a link from another site", "GET", "", "cross-site", "navigate", "document"},
		{"a client without fetch metadata", "GET", "", "", "", ""},
		{"its own origin without fetch metadata", "POST", ownOrigin, "", "", ""},
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
