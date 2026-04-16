package workspace

import (
	"fmt"
	"log"
	"net/http"
	"sync"

	"github.com/devpad-org/devpad/internal/auth"
	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true // Same-origin enforced by auth cookie
	},
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

	addr, err := h.service.AgentAddr(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	// Upgrade client connection to WebSocket
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer clientConn.Close()

	// Connect to the agent's terminal WebSocket
	agentURL := fmt.Sprintf("ws://%s/ws/terminal", addr)
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, nil)
	if err != nil {
		log.Printf("connecting to agent terminal: %v", err)
		clientConn.WriteMessage(websocket.TextMessage, []byte("Failed to connect to workspace terminal\r\n"))
		return
	}
	defer agentConn.Close()

	var wg sync.WaitGroup

	// Agent → Client
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
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

	wg.Wait()
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

	addr, err := h.service.AgentAddr(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	// Upgrade client connection to WebSocket
	clientConn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("watch websocket upgrade: %v", err)
		return
	}
	defer clientConn.Close()

	// Connect to the agent's watch WebSocket
	agentURL := fmt.Sprintf("ws://%s/ws/watch", addr)
	agentConn, _, err := websocket.DefaultDialer.Dial(agentURL, nil)
	if err != nil {
		log.Printf("connecting to agent watcher: %v", err)
		clientConn.WriteMessage(websocket.TextMessage, []byte(`{"error":"failed to connect to workspace watcher"}`))
		return
	}
	defer agentConn.Close()

	var wg sync.WaitGroup

	// Agent → Client
	wg.Add(1)
	go func() {
		defer wg.Done()
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
	wg.Add(1)
	go func() {
		defer wg.Done()
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

	wg.Wait()
}
