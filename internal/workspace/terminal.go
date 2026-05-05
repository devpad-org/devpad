package workspace

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/gorilla/websocket"
)

// upgrader returns a WebSocket upgrader that validates the Origin header
// against the handler's allowed origins list.
func (h *Handler) upgrader() *websocket.Upgrader {
	return &websocket.Upgrader{
		CheckOrigin: h.checkWebSocketOrigin,
	}
}

func (h *Handler) checkWebSocketOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Same-origin requests from browsers don't include an Origin header.
		return true
	}
	for _, allowed := range h.allowedOrigins {
		if origin == allowed {
			return true
		}
	}

	originURL, err := url.Parse(origin)
	if err != nil || originURL.Host == "" {
		log.Printf("websocket: rejected invalid origin %q", origin)
		return false
	}

	originHost := normalizeOriginHost(originURL.Host)
	requestHost := normalizeOriginHost(r.Host)
	if originHost == requestHost {
		return true
	}
	if sameLoopbackHostname(originHost, requestHost) {
		return true
	}

	log.Printf("websocket: rejected origin %q", origin)
	return false
}

func normalizeOriginHost(host string) string {
	return strings.ToLower(strings.TrimSpace(host))
}

func sameLoopbackHostname(a, b string) bool {
	aHost := hostnameWithoutPort(a)
	bHost := hostnameWithoutPort(b)
	return aHost != "" && aHost == bHost && isLoopbackHostname(aHost)
}

func hostnameWithoutPort(host string) string {
	host = normalizeOriginHost(host)
	if parsedHost, _, err := net.SplitHostPort(host); err == nil {
		return strings.Trim(parsedHost, "[]")
	}
	return strings.Trim(host, "[]")
}

func isLoopbackHostname(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// HandleTerminal upgrades to WebSocket and proxies the connection to the
// workspace agent's terminal WebSocket endpoint.
func (h *Handler) HandleTerminal(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	addr, agentToken, err := h.service.AgentAddr(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	// Upgrade client connection to WebSocket
	clientConn, err := h.upgrader().Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer clientConn.Close()

	// Connect to the agent's terminal WebSocket
	agentURL := fmt.Sprintf("ws://%s/ws/terminal", addr)
	agentHeaders := http.Header{}
	if agentToken != "" {
		agentHeaders.Set("X-Devpad-Agent-Token", agentToken)
	}
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		log.Printf("connecting to agent terminal: %v", err)
		clientConn.WriteMessage(websocket.TextMessage, []byte("Failed to connect to workspace terminal\r\n"))
		return
	}
	defer agentConn.Close()

	// When one goroutine exits, close both connections so the other unblocks.
	var once sync.Once
	done := make(chan struct{})
	closeDone := func() { once.Do(func() { close(done) }) }

	// Agent → Client
	go func() {
		defer closeDone()
		for {
			msgType, msg, err := agentConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Client → Agent
	go func() {
		defer closeDone()
		for {
			msgType, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := agentConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Wait for either goroutine to finish, then close both connections
	// so the other goroutine unblocks and exits.
	<-done
	clientConn.Close()
	agentConn.Close()
}

// HandleWatch upgrades to WebSocket and proxies the connection to the
// workspace agent's filesystem watch WebSocket endpoint.
func (h *Handler) HandleWatch(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	addr, agentToken, err := h.service.AgentAddr(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	// Upgrade client connection to WebSocket
	clientConn, err := h.upgrader().Upgrade(w, r, nil)
	if err != nil {
		log.Printf("watch websocket upgrade: %v", err)
		return
	}
	defer clientConn.Close()

	// Connect to the agent's watch WebSocket
	agentURL := fmt.Sprintf("ws://%s/ws/watch", addr)
	agentHeaders := http.Header{}
	if agentToken != "" {
		agentHeaders.Set("X-Devpad-Agent-Token", agentToken)
	}
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, agentHeaders)
	if err != nil {
		log.Printf("connecting to agent watcher: %v", err)
		clientConn.WriteMessage(websocket.TextMessage, []byte(`{"error":"failed to connect to workspace watcher"}`))
		return
	}
	defer agentConn.Close()

	// When one goroutine exits, close both connections so the other unblocks.
	var once sync.Once
	done := make(chan struct{})
	closeDone := func() { once.Do(func() { close(done) }) }

	// Agent → Client
	go func() {
		defer closeDone()
		for {
			msgType, msg, err := agentConn.ReadMessage()
			if err != nil {
				return
			}
			if err := clientConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Client → Agent (for keepalive / disconnect detection)
	go func() {
		defer closeDone()
		for {
			msgType, msg, err := clientConn.ReadMessage()
			if err != nil {
				return
			}
			if err := agentConn.WriteMessage(msgType, msg); err != nil {
				return
			}
		}
	}()

	// Wait for either goroutine to finish, then close both connections
	// so the other goroutine unblocks and exits.
	<-done
	clientConn.Close()
	agentConn.Close()
}
