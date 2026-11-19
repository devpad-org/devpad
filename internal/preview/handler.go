package preview

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/devpad-org/devpad/internal/workspace"
)

// Handler holds HTTP handlers for preview endpoints.
type Handler struct {
	service       Service
	previewDomain string
	httpsPort     int
	cookieSecret  []byte
}

// NewHandler creates a new preview Handler.
// httpsPort is the HTTPS port of the main server; used when generating preview URLs.
func NewHandler(service Service, previewDomain string, httpsPort int) *Handler {
	secret := make([]byte, 32)
	if _, err := rand.Read(secret); err != nil {
		log.Fatalf("failed to generate preview cookie secret: %v", err)
	}
	return &Handler{
		service:       service,
		previewDomain: previewDomain,
		httpsPort:     httpsPort,
		cookieSecret:  secret,
	}
}

// HandleGenerateURL creates a preview URL for a workspace port.
// POST /api/workspaces/{id}/preview
// Body: {"port": 3000}
func (h *Handler) HandleGenerateURL(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	if user == nil {
		writeError(w, http.StatusUnauthorized, "authentication required")
		return
	}

	wsID, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	var req struct {
		Port int `json:"port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Port < 1 || req.Port > 65535 {
		writeError(w, http.StatusBadRequest, "port must be between 1 and 65535")
		return
	}

	previewURL, err := h.service.GenerateURL(r.Context(), user.ID, wsID, req.Port, h.previewDomain, h.httpsPort)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) || errors.Is(err, workspace.ErrForbidden) {
			writeError(w, http.StatusNotFound, "workspace not found")
			return
		}
		if errors.Is(err, ErrWorkspaceDown) {
			writeError(w, http.StatusConflict, "workspace is not running")
			return
		}
		log.Printf("preview: generate URL error: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to generate preview URL")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"url": previewURL})
}

// HandleProxy handles all requests on preview subdomains.
// It parses {workspaceID}-{port}.preview.example.com from the Host header,
// validates the token (on first request) or cookie (subsequent requests),
// and reverse-proxies to the container via the agent.
func (h *Handler) HandleProxy(w http.ResponseWriter, r *http.Request) {
	wsID, port, err := h.parseSubdomain(r.Host)
	if err != nil {
		http.Error(w, "invalid preview URL", http.StatusBadRequest)
		return
	}

	if h.refusesCaller(r, wsID) {
		http.Error(w, "this preview cannot be reached from that page", http.StatusForbidden)
		return
	}
	if origin := r.Header.Get("Origin"); isPreflight(r) && h.relationOf(origin, wsID) == pageSameWorkspace {
		answerPreflight(w, r, origin)
		return
	}

	// If a token is present, try to exchange it for a cookie.
	if tokenStr := r.URL.Query().Get("token"); tokenStr != "" {
		t, err := h.service.ValidateToken(r.Context(), tokenStr)
		if err == nil && t.WorkspaceID == wsID && t.Port == port {
			h.setPreviewCookie(w, r, t)
			return
		}
		// Token invalid/used/expired — fall through to cookie-based auth.
		// This handles the case where the URL contains a stale token but
		// the browser already has a valid preview cookie.
	}

	// Authenticate via preview cookie.
	cookie, err := r.Cookie(PreviewCookieName)
	if err != nil {
		h.proxyError(w, r, wsID, "preview authentication required", http.StatusUnauthorized)
		return
	}

	claims, err := h.validateCookie(cookie.Value)
	if err != nil {
		h.proxyError(w, r, wsID, "invalid preview session", http.StatusUnauthorized)
		return
	}

	// Ensure cookie is scoped to this workspace+port.
	if claims.workspaceID != wsID || claims.port != port {
		h.proxyError(w, r, wsID, "preview session mismatch", http.StatusForbidden)
		return
	}

	h.proxyToContainer(w, r, wsID, port)
}

// pageRelation is how the page a URL names relates to a workspace's previews.
type pageRelation int

const (
	pageElsewhere pageRelation = iota
	pageSameWorkspace
	pageOtherWorkspace
)

// relationOf places the page named by an Origin or Referer header. Only an
// HTTPS page counts as the workspace's own, as previews are served over HTTPS.
func (h *Handler) relationOf(pageURL string, wsID int64) pageRelation {
	u, err := url.Parse(pageURL)
	if err != nil {
		return pageElsewhere
	}
	pageWS, _, err := h.parseSubdomain(u.Host)
	switch {
	case err != nil:
		return pageElsewhere
	case pageWS != wsID:
		return pageOtherWorkspace
	case u.Scheme == "https":
		return pageSameWorkspace
	default:
		return pageElsewhere
	}
}

// refusesCaller reports whether a request may have been made by a page outside
// this workspace. Previews are same-site with each other, so the browser sends
// this preview's cookie with requests from another workspace's page, and Origin
// alone can't catch them: images, scripts and frames send none, and a fetch
// redirected across origins sends "null". Such a request must show, by its
// Referer, that it comes from this workspace, which a page can hide but not
// forge. Top-level navigations stay allowed, so links to a preview work.
func (h *Handler) refusesCaller(r *http.Request, wsID int64) bool {
	origin := r.Header.Get("Origin")
	switch h.relationOf(origin, wsID) {
	case pageSameWorkspace:
		return false
	case pageOtherWorkspace:
		return true
	}
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-site", "cross-site":
		// Cross-site too: a redirect through another site can carry the cookie.
	default:
		if origin != "null" {
			return false
		}
	}
	if isTopLevelNavigation(r) {
		return false
	}
	return h.relationOf(r.Header.Get("Referer"), wsID) != pageSameWorkspace
}

func isTopLevelNavigation(r *http.Request) bool {
	return r.Method == http.MethodGet &&
		r.Header.Get("Sec-Fetch-Mode") == "navigate" &&
		r.Header.Get("Sec-Fetch-Dest") == "document"
}

func isPreflight(r *http.Request) bool {
	return r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != ""
}

// allowPage lets a page read a response and send credentials with its request.
func allowPage(header http.Header, origin string) {
	header.Set("Access-Control-Allow-Origin", origin)
	header.Set("Access-Control-Allow-Credentials", "true")
	header.Add("Vary", "Origin")
}

// proxyError writes an error of devpad's own. The workspace's other previews
// may read it, so a frontend can tell that its API's sign-in has ended rather
// than see a network failure. Responses from the app never get these headers:
// its own CORS settings decide those.
func (h *Handler) proxyError(w http.ResponseWriter, r *http.Request, wsID int64, message string, status int) {
	if origin := r.Header.Get("Origin"); h.relationOf(origin, wsID) == pageSameWorkspace {
		allowPage(w.Header(), origin)
	}
	http.Error(w, message, status)
}

// answerPreflight lets one of a workspace's previews call another, such as a
// frontend calling its API. Browsers send preflights without cookies, so
// devpad answers them itself rather than let unauthenticated requests reach
// the workspace. The app's own CORS headers still decide whether the page may
// read the response.
func answerPreflight(w http.ResponseWriter, r *http.Request, origin string) {
	header := w.Header()
	allowPage(header, origin)
	header.Set("Access-Control-Allow-Methods", r.Header.Get("Access-Control-Request-Method"))
	if requested := r.Header.Get("Access-Control-Request-Headers"); requested != "" {
		header.Set("Access-Control-Allow-Headers", requested)
	}
	header.Add("Vary", "Access-Control-Request-Method")
	header.Add("Vary", "Access-Control-Request-Headers")
	w.WriteHeader(http.StatusNoContent)
}

// setPreviewCookie sets the preview session cookie and redirects to the clean URL.
func (h *Handler) setPreviewCookie(w http.ResponseWriter, r *http.Request, t *Token) {
	cookieValue := h.signCookie(previewClaims{
		userID:      t.UserID,
		workspaceID: t.WorkspaceID,
		port:        t.Port,
		expiresAt:   time.Now().Add(PreviewSessionDuration),
	})

	http.SetCookie(w, &http.Cookie{
		Name:     PreviewCookieName,
		Value:    cookieValue,
		Path:     "/",
		Expires:  time.Now().Add(PreviewSessionDuration),
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   true,
	})

	// Redirect to same path without the token query param.
	cleanURL := *r.URL
	q := cleanURL.Query()
	q.Del("token")
	cleanURL.RawQuery = q.Encode()

	http.Redirect(w, r, cleanURL.String(), http.StatusFound)
}

// proxyToContainer reverse-proxies the request to the workspace container
// via the agent's port proxy endpoint.
func (h *Handler) proxyToContainer(w http.ResponseWriter, r *http.Request, wsID int64, port int) {
	agentAddr, agentToken, err := h.service.ResolveContainerAddr(r.Context(), wsID)
	if err != nil {
		if errors.Is(err, workspace.ErrNotFound) {
			h.proxyError(w, r, wsID, "workspace not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, ErrWorkspaceDown) {
			h.proxyError(w, r, wsID, "workspace is not running", http.StatusServiceUnavailable)
			return
		}
		log.Printf("preview: resolve container error: %v", err)
		h.proxyError(w, r, wsID, "preview backend unavailable", http.StatusBadGateway)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   agentAddr,
	}

	publicHost := r.Host
	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// The app sees the agent's address as its host, so tell it the
			// address the browser used.
			req.Header.Set("X-Forwarded-Host", publicHost)
			req.Header.Set("X-Forwarded-Proto", "https")
			// Route through the agent's port proxy endpoint.
			req.URL.Path = fmt.Sprintf("/proxy/%d%s", port, req.URL.Path)
			// Remove the preview cookies so they don't leak into the container.
			removeCookies(req, PreviewCookieName, legacyPreviewCookieName)
			// Authenticate with the agent.
			if agentToken != "" {
				req.Header.Set("X-Devpad-Agent-Token", agentToken)
			}
		},
		ModifyResponse: func(resp *http.Response) error {
			sanitizeSetCookies(resp.Header)
			rewriteRedirect(resp.Header, target.Host, publicHost)
			return nil
		},
		// Flush immediately for streaming responses (SSE, chunked).
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("preview: proxy error for ws %d port %d: %v", wsID, port, err)
			h.proxyError(w, r, wsID, "preview backend error", http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

// rewriteRedirect points a redirect an app built from the host it saw, the
// agent's address the browser cannot reach, back at the preview.
func rewriteRedirect(h http.Header, agentHost, publicHost string) {
	location := h.Get("Location")
	if location == "" {
		return
	}
	u, err := url.Parse(location)
	if err != nil || u.Host != agentHost {
		return
	}
	u.Scheme, u.Host = "https", publicHost
	h.Set("Location", u.String())
}

// parseSubdomain extracts workspace ID and port from a hostname like "3-8080.preview.example.com".
func (h *Handler) parseSubdomain(host string) (int64, int, error) {
	// Strip port number from host if present.
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	suffix := "." + h.previewDomain
	if !strings.HasSuffix(host, suffix) {
		return 0, 0, fmt.Errorf("host %q does not match preview domain", host)
	}

	prefix := strings.TrimSuffix(host, suffix)
	parts := strings.SplitN(prefix, "-", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("invalid subdomain format: %q", prefix)
	}

	wsID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid workspace id in subdomain: %w", err)
	}

	port, err := strconv.Atoi(parts[1])
	if err != nil || port < 1 || port > 65535 {
		return 0, 0, fmt.Errorf("invalid port in subdomain: %q", parts[1])
	}

	return wsID, port, nil
}

// previewClaims holds the data encoded in the preview cookie.
type previewClaims struct {
	userID      int64
	workspaceID int64
	port        int
	expiresAt   time.Time
}

// signCookie creates an HMAC-signed cookie value encoding the preview claims.
// Format: userID:workspaceID:port:expiresUnix:signature
func (h *Handler) signCookie(c previewClaims) string {
	payload := fmt.Sprintf("%d:%d:%d:%d", c.userID, c.workspaceID, c.port, c.expiresAt.Unix())
	sig := h.hmacSign(payload)
	return payload + ":" + sig
}

// validateCookie parses and validates an HMAC-signed preview cookie.
func (h *Handler) validateCookie(value string) (*previewClaims, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 5 {
		return nil, fmt.Errorf("malformed cookie")
	}

	payload := strings.Join(parts[:4], ":")
	sig := parts[4]

	if !h.hmacVerify(payload, sig) {
		return nil, fmt.Errorf("invalid signature")
	}

	userID, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid user id")
	}
	wsID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid workspace id")
	}
	port, err := strconv.Atoi(parts[2])
	if err != nil {
		return nil, fmt.Errorf("invalid port")
	}
	expiresUnix, err := strconv.ParseInt(parts[3], 10, 64)
	if err != nil {
		return nil, fmt.Errorf("invalid expiry")
	}

	if time.Now().Unix() > expiresUnix {
		return nil, fmt.Errorf("cookie expired")
	}

	return &previewClaims{
		userID:      userID,
		workspaceID: wsID,
		port:        port,
		expiresAt:   time.Unix(expiresUnix, 0),
	}, nil
}

func (h *Handler) hmacSign(data string) string {
	mac := hmac.New(sha256.New, h.cookieSecret)
	mac.Write([]byte(data))
	return hex.EncodeToString(mac.Sum(nil))
}

func (h *Handler) hmacVerify(data, signature string) bool {
	expected := h.hmacSign(data)
	return hmac.Equal([]byte(expected), []byte(signature))
}

// legacyPreviewCookieName is the cookie all previews once shared. It was set
// for the whole preview domain, so browsers that still hold one send it to
// every preview until it expires.
const legacyPreviewCookieName = "devpad_preview"

// reservedCookieNames are cookies devpad itself sets; a container may never
// set them through the preview proxy.
var reservedCookieNames = []string{"devpad_session", "__Host-devpad_session", PreviewCookieName, legacyPreviewCookieName}

// sanitizeSetCookies makes every cookie a container sets host-only, by
// dropping its Domain attribute, and drops cookies named like devpad's own.
// Without this a preview could set a cookie for the parent domain that the
// app (or other previews) would read in place of the real one. It is defense
// in depth: page script can still write document.cookie, which the app's
// __Host- session cookie name guards against.
func sanitizeSetCookies(h http.Header) {
	lines := h.Values("Set-Cookie")
	if len(lines) == 0 {
		return
	}
	h.Del("Set-Cookie")
	for _, line := range lines {
		parts := strings.Split(line, ";")
		name, _, _ := strings.Cut(parts[0], "=")
		if isReservedCookie(strings.TrimSpace(name)) {
			continue
		}
		kept := parts[:1]
		for _, attr := range parts[1:] {
			key, _, _ := strings.Cut(attr, "=")
			if strings.EqualFold(strings.TrimSpace(key), "domain") {
				continue
			}
			kept = append(kept, attr)
		}
		h.Add("Set-Cookie", strings.Join(kept, ";"))
	}
}

func isReservedCookie(name string) bool {
	for _, reserved := range reservedCookieNames {
		if strings.EqualFold(name, reserved) {
			return true
		}
	}
	return false
}

// removeCookies removes the named cookies from the request before proxying.
func removeCookies(r *http.Request, names ...string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if !slices.Contains(names, c.Name) {
			r.AddCookie(c)
		}
	}
}

func parseID(r *http.Request, name string) (int64, error) {
	raw := r.PathValue(name)
	return strconv.ParseInt(raw, 10, 64)
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
