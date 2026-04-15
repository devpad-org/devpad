package workspace

import (
	"encoding/json"
	"io"
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

// terminalResize is sent from the client to resize the terminal.
type terminalResize struct {
	Type string `json:"type"`
	Cols uint   `json:"cols"`
	Rows uint   `json:"rows"`
}

// HandleTerminal upgrades to WebSocket and proxies stdin/stdout to a bash
// exec session in the workspace's Docker container.
func (h *Handler) HandleTerminal(w http.ResponseWriter, r *http.Request) {
	user := auth.UserFromContext(r.Context())
	id, err := parseID(r, "id")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid workspace id")
		return
	}

	ws, err := h.service.Get(r.Context(), user.ID, id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	if ws.ContainerID == "" || ws.Status != StatusRunning {
		writeError(w, http.StatusConflict, "workspace is not running")
		return
	}

	cm := h.service.ContainerManager()

	// Create exec instance for bash
	execID, err := cm.Exec(r.Context(), ws.ContainerID, []string{"/bin/bash"})
	if err != nil {
		log.Printf("creating exec: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to create terminal session")
		return
	}

	// Attach to the exec instance
	hijacked, err := cm.ExecAttach(r.Context(), execID)
	if err != nil {
		log.Printf("attaching exec: %v", err)
		writeError(w, http.StatusInternalServerError, "failed to attach terminal")
		return
	}
	defer hijacked.Closer()

	// Upgrade HTTP to WebSocket
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("websocket upgrade: %v", err)
		return
	}
	defer conn.Close()

	var wg sync.WaitGroup

	// Container stdout → WebSocket
	wg.Add(1)
	go func() {
		defer wg.Done()
		buf := make([]byte, 4096)
		for {
			n, err := hijacked.Reader.Read(buf)
			if n > 0 {
				if writeErr := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); writeErr != nil {
					return
				}
			}
			if err != nil {
				if err != io.EOF {
					log.Printf("exec read error: %v", err)
				}
				return
			}
		}
	}()

	// WebSocket → Container stdin
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			msgType, msg, err := conn.ReadMessage()
			if err != nil {
				return
			}

			if msgType == websocket.TextMessage {
				// Check if it's a resize message
				var resize terminalResize
				if json.Unmarshal(msg, &resize) == nil && resize.Type == "resize" {
					if resize.Cols > 0 && resize.Rows > 0 {
						if err := cm.ExecResize(r.Context(), execID, resize.Rows, resize.Cols); err != nil {
							log.Printf("resize error: %v", err)
						}
					}
					continue
				}
				// Otherwise treat as stdin data
				if _, err := hijacked.Conn.Write(msg); err != nil {
					return
				}
			} else if msgType == websocket.BinaryMessage {
				if _, err := hijacked.Conn.Write(msg); err != nil {
					return
				}
			}
		}
	}()

	wg.Wait()
}
