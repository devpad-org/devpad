package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strconv"
	"strings"
)

// handlePortProxy reverse-proxies requests from /proxy/{port}/... to localhost:{port}/...
// This allows the Devpad server to reach any port inside the container through the agent
// instead of connecting directly to the container port (which may not be ready).
func handlePortProxy(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimPrefix(r.URL.Path, "/proxy/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		http.Error(w, "missing port", http.StatusBadRequest)
		return
	}

	port, err := strconv.Atoi(parts[0])
	if err != nil || port < 1 || port > 65535 {
		http.Error(w, "invalid port", http.StatusBadRequest)
		return
	}

	remaining := "/"
	if len(parts) > 1 {
		remaining = "/" + parts[1]
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("127.0.0.1:%d", port),
	}

	proxy := &httputil.ReverseProxy{
		Director: func(req *http.Request) {
			req.URL.Scheme = target.Scheme
			req.URL.Host = target.Host
			req.Host = r.Host
			req.URL.Path = remaining
			req.URL.RawQuery = r.URL.RawQuery
		},
		// Flush immediately for streaming responses (SSE, chunked).
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			if isConnRefused(err) {
				serveWaitingPage(w, port)
				return
			}
			log.Printf("port proxy %d: %v", port, err)
			http.Error(w, "backend error", http.StatusBadGateway)
		},
	}

	proxy.ServeHTTP(w, r)
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	if ok := errorAs(err, &opErr); ok {
		return opErr.Op == "dial"
	}
	return false
}

// errorAs is a thin wrapper so we avoid importing errors in this file.
func errorAs(err error, target any) bool {
	// Use interface assertion chain for *net.OpError.
	for err != nil {
		if t, ok := err.(*net.OpError); ok {
			*target.(**net.OpError) = t
			return true
		}
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return false
		}
		err = u.Unwrap()
	}
	return false
}

func serveWaitingPage(w http.ResponseWriter, port int) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	fmt.Fprintf(w, `<!DOCTYPE html>
<html>
<head>
  <meta charset="utf-8">
  <meta http-equiv="refresh" content="2">
  <title>Waiting for service…</title>
  <style>
    * { margin: 0; padding: 0; box-sizing: border-box; }
    body {
      background: #1a1a2e;
      color: #a0a0b8;
      display: flex;
      align-items: center;
      justify-content: center;
      height: 100vh;
      font-family: 'Inter', system-ui, -apple-system, sans-serif;
    }
    .container { text-align: center; }
    .spinner {
      width: 32px; height: 32px;
      border: 3px solid rgba(0, 212, 255, 0.15);
      border-top-color: #00d4ff;
      border-radius: 50%%;
      animation: spin 0.8s linear infinite;
      margin: 0 auto 16px;
    }
    @keyframes spin { to { transform: rotate(360deg); } }
    h1 { font-size: 1rem; font-weight: 500; margin-bottom: 6px; color: #c0c0d8; }
    p { font-size: 0.8rem; opacity: 0.5; }
  </style>
</head>
<body>
  <div class="container">
    <div class="spinner"></div>
    <h1>Waiting for service on port %d…</h1>
    <p>This page will auto-refresh</p>
  </div>
</body>
</html>`, port)
}
