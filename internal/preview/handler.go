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
		http.Error(w, "preview authentication required", http.StatusUnauthorized)
		return
	}

	claims, err := h.validateCookie(cookie.Value)
	if err != nil {
		http.Error(w, "invalid preview session", http.StatusUnauthorized)
		return
	}

	// Ensure cookie is scoped to this workspace+port.
	if claims.workspaceID != wsID || claims.port != port {
		http.Error(w, "preview session mismatch", http.StatusForbidden)
		return
	}

	h.proxyToContainer(w, r, wsID, port)
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
		Domain:   "." + h.previewDomain,
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
			http.Error(w, "workspace not found", http.StatusNotFound)
			return
		}
		if errors.Is(err, ErrWorkspaceDown) {
			http.Error(w, "workspace is not running", http.StatusServiceUnavailable)
			return
		}
		log.Printf("preview: resolve container error: %v", err)
		http.Error(w, "preview backend unavailable", http.StatusBadGateway)
		return
	}

	target := &url.URL{
		Scheme: "http",
		Host:   agentAddr,
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = target.Host
			// Route through the agent's port proxy endpoint.
			req.URL.Path = fmt.Sprintf("/proxy/%d%s", port, req.URL.Path)
			// Remove the preview cookie so it doesn't leak into the container.
			removeCookie(req, PreviewCookieName)
			// Authenticate with the agent.
			if agentToken != "" {
				req.Header.Set("X-Devpad-Agent-Token", agentToken)
			}
		},
		// Flush immediately for streaming responses (SSE, chunked).
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("preview: proxy error for ws %d port %d: %v", wsID, port, err)
			http.Error(w, "preview backend error", http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
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

// removeCookie removes a named cookie from the request before proxying.
func removeCookie(r *http.Request, name string) {
	cookies := r.Cookies()
	r.Header.Del("Cookie")
	for _, c := range cookies {
		if c.Name != name {
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
