package main

import (
	"log"
	"net/http"
	"os"
)

const defaultPort = "9100"
const workspaceRoot = "/workspace"

func main() {
	port := os.Getenv("AGENT_PORT")
	if port == "" {
		port = defaultPort
	}

	// Start filesystem watcher
	fsWatcher, err := newWatcher()
	if err != nil {
		log.Fatalf("starting filesystem watcher: %v", err)
	}

	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("GET /healthz", handleHealthz)

	// File operations
	mux.HandleFunc("GET /api/files", handleListFiles)
	mux.HandleFunc("GET /api/file", handleReadFile)
	mux.HandleFunc("PUT /api/file", handleWriteFile)
	mux.HandleFunc("DELETE /api/file", handleDeleteFile)
	mux.HandleFunc("POST /api/file/mkdir", handleMkdir)
	mux.HandleFunc("POST /api/file/rename", handleRename)

	// Terminal WebSocket
	mux.HandleFunc("GET /ws/terminal", handleTerminal)

	// Filesystem watch WebSocket
	mux.HandleFunc("GET /ws/watch", handleWatch(fsWatcher))

	addr := ":" + port
	log.Printf("devpad-agent listening on %s (workspace: %s)", addr, workspaceRoot)
	if err := http.ListenAndServe(addr, mux); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
