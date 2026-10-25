package server

import (
	"encoding/json"
	"fmt"
	"log"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
)

// appOrigins returns the origins the app itself is served from, used to
// accept WebSocket upgrades and state-changing requests from the frontend
// when a request's Host differs from the browser's view of it (e.g. behind a
// proxy that rewrites Host).
func appOrigins(cfg Config) []string {
	if cfg.Domain != "" {
		origin := "https://" + cfg.Domain
		if cfg.Port != 443 {
			origin = fmt.Sprintf("https://%s:%d", cfg.Domain, cfg.Port)
		}
		return []string{origin}
	}
	// Development mode: allow localhost on the configured port.
	return []string{
		fmt.Sprintf("http://localhost:%d", cfg.Port),
		fmt.Sprintf("http://127.0.0.1:%d", cfg.Port),
	}
}

// sameOriginGuard refuses state-changing requests and WebSocket upgrades that
// a browser sent from any origin other than the app's own.
//
// SameSite cookies do not cover this: preview subdomains serve content an AI
// controls, and when they share a registrable domain with the app the browser
// treats them as the same site, so it attaches the session cookie to their
// form posts, simple fetches and WebSocket upgrades. Those requests always
// carry Sec-Fetch-Site (same-site/cross-site) or, from older browsers, an
// Origin naming the preview host, so either header is enough to refuse them.
//
// A request with neither header did not come from a cross-origin browser
// context (browsers always send Origin on cross-origin POSTs and WebSocket
// upgrades), so it is allowed; that keeps curl and other non-browser clients
// working, and they cannot ride the user's cookie anyway.
//
// Requests to /api/ that pass also need a JSON body, except the multipart
// file upload, so a form post can never be mistaken for an API call.
func sameOriginGuard(next http.Handler, trustedOrigins []string) http.Handler {
	trusted := make(map[string]bool, len(trustedOrigins))
	for _, o := range trustedOrigins {
		trusted[strings.ToLower(o)] = true
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isStateChanging(r) && !isWebSocketUpgrade(r) {
			next.ServeHTTP(w, r)
			return
		}
		if !sameOrigin(r, trusted) {
			log.Printf("refused %s %s from origin %q (Sec-Fetch-Site %q)", r.Method, r.URL.Path, r.Header.Get("Origin"), r.Header.Get("Sec-Fetch-Site"))
			writeGuardError(w, http.StatusForbidden, "cross-origin request refused")
			return
		}
		if isStateChanging(r) && strings.HasPrefix(r.URL.Path, "/api/") && !acceptableBody(r) {
			writeGuardError(w, http.StatusUnsupportedMediaType, "unsupported content type")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func isStateChanging(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	}
	return true
}

func isWebSocketUpgrade(r *http.Request) bool {
	return headerHasToken(r.Header, "Connection", "upgrade") && strings.EqualFold(r.Header.Get("Upgrade"), "websocket")
}

func headerHasToken(h http.Header, name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.TrimSpace(part), token) {
				return true
			}
		}
	}
	return false
}

// sameOrigin reports whether a browser sent r from the app's own origin.
// Sec-Fetch-Site is set by the browser and cannot be forged by page script,
// so it decides when present; Origin is the fallback for browsers without it.
func sameOrigin(r *http.Request, trusted map[string]bool) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none":
		// "none" is a user-initiated navigation (typed URL, bookmark).
		return true
	case "":
	default:
		// same-site (a sibling subdomain such as a preview) or cross-site.
		return false
	}

	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	if trusted[strings.ToLower(origin)] {
		return true
	}
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" {
		// Includes the opaque "null" origin of sandboxed frames and data: URLs.
		return false
	}
	// The scheme is not compared: the threat is another host, and the app
	// may sit behind a TLS-terminating proxy that hides the browser's scheme.
	originHost := strings.ToLower(u.Host)
	requestHost := strings.ToLower(r.Host)
	if originHost == requestHost {
		return true
	}
	// The Vite dev server proxies the API from another localhost port.
	oh, rh := hostname(originHost), hostname(requestHost)
	return oh == rh && isLoopback(oh)
}

func hostname(host string) string {
	if h, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(h, "[]")
	}
	return strings.Trim(host, "[]")
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// acceptableBody reports whether a state-changing API request's body is one
// the API reads: JSON everywhere, multipart only for the file upload. A
// request without a body needs no content type.
func acceptableBody(r *http.Request) bool {
	if r.ContentLength == 0 {
		return true
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return false
	}
	if isFileUpload(r) {
		return mediaType == "multipart/form-data"
	}
	return mediaType == "application/json"
}

func isFileUpload(r *http.Request) bool {
	return r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/api/workspaces/") && strings.HasSuffix(r.URL.Path, "/file/upload")
}

func writeGuardError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}
