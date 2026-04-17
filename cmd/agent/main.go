package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

	// Search and command execution
	mux.HandleFunc("POST /api/search", handleSearchFiles)
	mux.HandleFunc("POST /api/command", handleRunCommand)

	// Git operations
	mux.HandleFunc("GET /api/git/status", handleGitStatus)
	mux.HandleFunc("GET /api/git/log", handleGitLog)
	mux.HandleFunc("GET /api/git/branches", handleGitBranches)
	mux.HandleFunc("GET /api/git/diff", handleGitDiff)
	mux.HandleFunc("POST /api/git/action", handleGitAction)

	// Terminal WebSocket
	mux.HandleFunc("GET /ws/terminal", handleTerminal)

	// Filesystem watch WebSocket
	mux.HandleFunc("GET /ws/watch", handleWatch(fsWatcher))

	// Port proxy — allows the Devpad server to reach any port through the agent.
	mux.HandleFunc("/proxy/", handlePortProxy)

	addr := ":" + port
	log.Printf("devpad-agent listening on %s (workspace: %s)", addr, workspaceRoot)

	srv := &http.Server{Addr: addr, Handler: mux}

	// Graceful shutdown: close the filesystem watcher and HTTP server on SIGINT/SIGTERM.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-sigCh
		log.Println("shutting down agent...")
		fsWatcher.Close()
		srv.Shutdown(context.Background())
	}()

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
